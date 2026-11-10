package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tc-hib/winres"
	winversion "github.com/tc-hib/winres/version"

	"wwlogs/internal/assets"
	"wwlogs/internal/keyembed"
	"wwlogs/scripts/buildrun"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "embed-key: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	outputFlag := flag.String("o", "", "output executable path for a single-profile run (default: <output-root>/<profile>/WWlogs.exe for desktop, WWlogs-Capture.exe for capture)")
	outputRoot := flag.String("output-root", "bin", "root of the per-profile output directories <output-root>/development and <output-root>/public")
	profileFlag := flag.String("profile", "public", "build profile: public (no raw diagnostic outputs), development (full diagnostics), or both in one build run")
	versionFlag := flag.String("version", "", "product version to embed (default: the version registered in internal/versioninfo)")
	requireClean := flag.Bool("require-clean", false, "fail when the Git working tree has uncommitted changes to tracked files")
	testBinary := flag.String("test-binary", "", "build a test binary of this package (for example ./internal/capture) carrying the encrypted key payload and the committed test overlay, instead of the executable; requires -o")
	targetFlag := flag.String("target", "capture", "executable to build: capture (console analyzer) or desktop (Wails desktop host; requires the built frontend of each profile)")
	frontendPublic := flag.String("frontend-dist-public", "", "built Public frontend directory to embed into the desktop host; must be outside the repository (scripts/frontend.ps1 -Action Build)")
	frontendDevelopment := flag.String("frontend-dist-development", "", "built Development frontend directory to embed into the desktop host; must be outside the repository")
	unofficial := flag.Bool("unofficial", false, "build unofficial executables that neither read nor advance the official build counter")
	initCounter := flag.Bool("init-build-number", false, "owner-directed first initialization of the official build counter; refused when a counter exists or existed")
	stateDirectory := flag.String("build-state", "", "official build counter directory (default: <repository>/.local); tests point it at an isolated directory")
	checkKey := flag.Bool("check-key", false, "check for the owner-local private key input without reading its contents")
	flag.Parse()
	if *checkKey {
		checkOnly := true
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "check-key" {
				checkOnly = false
			}
		})
		if !checkOnly || flag.NArg() != 0 {
			return errors.New("-check-key must be used alone")
		}
		return checkOwnerKey(repositoryRootFor())
	}
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	var profiles []string
	switch *profileFlag {
	case "public", "development":
		profiles = []string{*profileFlag}
	case "both":
		// Development first: a Public failure after it still installs nothing.
		profiles = []string{"development", "public"}
	default:
		return fmt.Errorf("unknown profile %q: use public, development or both", *profileFlag)
	}
	var buildPackage, outputName string
	switch *targetFlag {
	case "capture":
		buildPackage, outputName = "./cmd/capture", "WWlogs-Capture.exe"
	case "desktop":
		buildPackage, outputName = "./cmd/desktop", "WWlogs.exe"
		if *testBinary != "" {
			return errors.New("-test-binary builds packages, not the desktop target")
		}
	default:
		return fmt.Errorf("unknown target %q: use capture or desktop", *targetFlag)
	}
	frontendDist := map[string]string{"public": *frontendPublic, "development": *frontendDevelopment}
	if *targetFlag == "desktop" {
		for _, profile := range profiles {
			if frontendDist[profile] == "" {
				return fmt.Errorf("-target desktop requires -frontend-dist-%s", profile)
			}
		}
	}
	if *outputFlag != "" && len(profiles) != 1 {
		return errors.New("-o names one executable; use -output-root for a run that builds both profiles")
	}
	if *testBinary != "" && (len(profiles) != 1 || *outputFlag == "") {
		return errors.New("-test-binary needs one profile and -o")
	}
	if *unofficial && *initCounter {
		return errors.New("-unofficial and -init-build-number exclude each other")
	}

	repositoryRoot := repositoryRootFor()
	version := *versionFlag
	if version == "" {
		version = registeredVersion(repositoryRoot)
	}
	keyPEM, err := readOwnerKey(repositoryRoot)
	if err != nil {
		return err
	}
	defer clear(keyPEM)
	privateDER, err := validatePrivateKeyPEM(keyPEM)
	if err != nil {
		return fmt.Errorf("validate owner-local private key: %w", err)
	}
	defer clear(privateDER)
	payload, err := keyembed.Encrypt(keyPEM)
	if err != nil {
		return fmt.Errorf("encrypt build key payload: %w", err)
	}
	generatedSource := []byte(fmt.Sprintf(`package capture

var (
	embeddedKeyCiphertext = %q
	embeddedKeyEncryptionKey = %q
)
`, payload.Ciphertext, payload.EncryptionKey))
	defer clear(generatedSource)
	generatedSourcePath, err := writeTemporaryFile(os.TempDir(), "wwlogs-key-payload-*.go", generatedSource)
	if err != nil {
		return fmt.Errorf("write temporary encrypted payload source: %w", err)
	}
	defer os.Remove(generatedSourcePath)

	masterDatabasePath := filepath.Join(repositoryRoot, "data", "master_data.db")
	masterDatabaseInfo, err := os.Stat(masterDatabasePath)
	if err != nil {
		return fmt.Errorf("inspect tracked combat Master database build input: %w", err)
	}
	if !masterDatabaseInfo.Mode().IsRegular() || masterDatabaseInfo.Size() == 0 {
		return errors.New("tracked combat Master database build input is not a non-empty regular file")
	}
	masterDatabaseHash, err := fileSHA256(masterDatabasePath)
	if err != nil {
		return fmt.Errorf("hash combat Master database build input: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Master database: %d bytes, SHA-256 %s\n", masterDatabaseInfo.Size(), masterDatabaseHash)
	baseReplacements := map[string]string{
		filepath.Join(repositoryRoot, "internal", "capture", "key_payload.go"): generatedSourcePath,
		filepath.Join(repositoryRoot, "internal", "capture", "master_data.db"): masterDatabasePath,
	}

	if *testBinary != "" {
		if err := mergeTestOverlay(repositoryRoot, baseReplacements); err != nil {
			return err
		}
		overlayPath, err := writeOverlay(baseReplacements)
		if err != nil {
			return err
		}
		defer os.Remove(overlayPath)
		return buildTestBinary(repositoryRoot, *testBinary, absolutePath(repositoryRoot, *outputFlag), overlayPath, profileTags(profiles[0], *targetFlag), keyPEM, privateDER)
	}

	commit, dirty := gitIdentity(repositoryRoot)
	if *requireClean && dirty != "false" {
		return fmt.Errorf("working tree state is %q; commit or stash tracked changes before a clean build", dirty)
	}

	// The official counter is consulted only now, after every input check, and stays locked until the run ends.
	var allocation *buildrun.Allocation
	buildNumber := ""
	if !*unofficial {
		directory := *stateDirectory
		if directory == "" {
			directory = filepath.Join(repositoryRoot, ".local")
		}
		store := &buildrun.Store{Dir: absolutePath(repositoryRoot, directory), Waiting: func() {
			fmt.Fprintln(os.Stdout, "Waiting for another official build run to finish")
		}}
		allocation, err = store.Begin(*initCounter)
		switch {
		case errors.Is(err, buildrun.ErrNotInitialized):
			allocation = nil
			fmt.Fprintln(os.Stdout, "Official build counter not found: building UNOFFICIAL executables; no Build Number is used")
		case err != nil:
			return fmt.Errorf("BLOCKED: %w", err)
		default:
			defer allocation.Abort(true)
			buildNumber = strconv.FormatUint(allocation.Number, 10)
			if allocation.Initialized {
				fmt.Fprintln(os.Stdout, "Initializing the official build counter (owner-directed first use)")
			}
			fmt.Fprintf(os.Stdout, "Build run %s: candidate Build Number for %s\n", buildNumber, strings.Join(profiles, " + "))
		}
	} else {
		fmt.Fprintln(os.Stdout, "Unofficial build requested: the official build counter is not used")
	}

	var artifacts []buildrun.Artifact
	for _, profile := range profiles {
		output := *outputFlag
		if output == "" {
			output = filepath.Join(*outputRoot, profile, outputName)
		}
		artifacts = append(artifacts, buildrun.Artifact{Profile: profile, Output: absolutePath(repositoryRoot, output)})
	}

	// The image pack is profile-independent, so one run builds it once and embeds the same bytes in every profile.
	var packPath, packSourcePath string
	defer func() {
		if packPath != "" {
			os.Remove(packPath)
			os.Remove(packSourcePath)
		}
	}()
	builder := func(artifact buildrun.Artifact, staged string) error {
		replacements := maps.Clone(baseReplacements)
		if *targetFlag == "desktop" {
			archivePath, archiveSourcePath, err := writeFrontendArchive(repositoryRoot, frontendDist[artifact.Profile])
			if err != nil {
				return err
			}
			defer os.Remove(archivePath)
			defer os.Remove(archiveSourcePath)
			replacements[filepath.Join(repositoryRoot, "internal", "desktop", "frontend_archive.go")] = archiveSourcePath
			replacements[filepath.Join(repositoryRoot, "internal", "desktop", frontendArchiveName)] = archivePath
			if packPath == "" {
				if packPath, packSourcePath, err = writeGameAssetPack(repositoryRoot); err != nil {
					return err
				}
			}
			replacements[filepath.Join(repositoryRoot, "internal", "desktop", "game_asset_archive.go")] = packSourcePath
			replacements[filepath.Join(repositoryRoot, "internal", "desktop", gameAssetPackName)] = packPath
		}
		overlayPath, err := writeOverlay(replacements)
		if err != nil {
			return err
		}
		defer os.Remove(overlayPath)
		identity := windowsIdentity{version: version, buildNumber: buildNumber, profile: artifact.Profile, commit: commit, dirty: dirty, originalFilename: outputName}
		resources, resourcePath, err := windowsResources(repositoryRoot, buildPackage, identity)
		if err != nil {
			return err
		}
		// The resource object is reserved per build and removed before the next profile writes its own.
		defer os.Remove(resourcePath)
		ldflags := "-X wwlogs/internal/buildinfo.injectedVersion=" + version +
			" -X wwlogs/internal/buildinfo.injectedBuildNumber=" + buildNumber +
			" -X wwlogs/internal/buildinfo.injectedCommit=" + commit +
			" -X wwlogs/internal/buildinfo.injectedDirty=" + dirty
		if *targetFlag == "desktop" {
			// A GUI subsystem executable opens no console window; -version still writes to redirected output.
			ldflags += " -H windowsgui"
		}
		// -buildvcs=false keeps the result independent of Git being reachable; the identity is injected above.
		buildArgs := append([]string{"build", "-trimpath", "-buildvcs=false"}, profileTags(artifact.Profile, *targetFlag)...)
		buildArgs = append(buildArgs, "-ldflags", ldflags, "-overlay", overlayPath, "-o", staged, buildPackage)
		build := exec.Command("go", buildArgs...)
		build.Dir = repositoryRoot
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		fmt.Fprintf(os.Stdout, "Building %s (%s)\n", artifact.Profile, *targetFlag)
		if err := build.Run(); err != nil {
			return fmt.Errorf("build executable: %w", err)
		}
		if err := verifyNoPlaintextKey(staged, keyPEM, privateDER); err != nil {
			return err
		}
		if err := verifyBuildIdentity(staged, artifact.Profile, version, buildNumber, commit); err != nil {
			return err
		}
		return verifyWindowsResources(staged, resources)
	}

	staged, err := buildrun.Stage(artifacts, builder)
	if err != nil {
		return fmt.Errorf("build run failed, nothing was installed and the Build Number was not used: %w", err)
	}
	stagedHashes := map[string]string{}
	for profile, path := range staged.Paths() {
		if stagedHashes[profile], err = fileSHA256(path); err != nil {
			staged.Discard()
			return fmt.Errorf("hash staged executable: %w", err)
		}
	}
	if allocation != nil {
		if err := allocation.MarkIssued(); err != nil {
			staged.Discard()
			return fmt.Errorf("record the Build Number before installing: %w", err)
		}
	}
	installation, rolledBack, err := staged.Install()
	if err != nil {
		if allocation != nil {
			allocation.Abort(rolledBack)
		}
		return fmt.Errorf("build run failed while installing: %w", err)
	}
	if allocation != nil {
		record := buildrun.SuccessRecord{CompletedAt: time.Now().UTC(), Commit: commit, Dirty: dirty, Target: *targetFlag, Profiles: profiles}
		if err := allocation.Commit(record); err != nil {
			rollbackErr := installation.Rollback()
			allocation.Abort(rollbackErr == nil)
			return fmt.Errorf("record the completed build run: %v; previous executables restored: %t", err, rollbackErr == nil)
		}
	}
	installation.Finalize()

	label := buildNumber
	if label == "" {
		label = "unofficial"
	}
	for _, artifact := range artifacts {
		fmt.Fprintf(os.Stdout, "Built %s\n", artifact.Output)
		fmt.Fprintf(os.Stdout, "Profile: %s  Version: %s  Build number: %s  Commit: %s  Working tree modified: %s\n", artifact.Profile, version, label, commit, dirty)
		fmt.Fprintf(os.Stdout, "Executable SHA-256: %s\n", stagedHashes[artifact.Profile])
	}
	fmt.Fprintln(os.Stdout, "Plaintext RSA PEM/DER scan: PASS")
	fmt.Fprintln(os.Stdout, "Build identity check: PASS")
	fmt.Fprintln(os.Stdout, "Embedded Windows icon and version check: PASS")
	fmt.Fprintf(os.Stdout, "Build run complete: %d executable(s), Build Number %s\n", len(artifacts), label)
	return nil
}

// profileTags returns the Go build tags of one profile and target.
func profileTags(profile, target string) []string {
	if profile == "development" {
		return []string{"-tags", "wwdev"}
	}
	if target == "desktop" {
		// Wails' production tag removes DevTools and the dev-server hook; the Development profile keeps Wails debug mode.
		return []string{"-tags", "production"}
	}
	return nil
}

func absolutePath(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return filepath.Clean(path)
}

func writeOverlay(replacements map[string]string) (string, error) {
	overlayData, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replacements})
	if err != nil {
		return "", fmt.Errorf("encode Go build overlay: %w", err)
	}
	defer clear(overlayData)
	path, err := writeTemporaryFile(os.TempDir(), "wwlogs-key-overlay-*.json", overlayData)
	if err != nil {
		return "", fmt.Errorf("write Go build overlay: %w", err)
	}
	return path, nil
}

// windowsIdentity is the part of the build identity written into the executable's version resource.
type windowsIdentity struct {
	version, buildNumber, profile, commit, dirty, originalFilename string
}

// productVersionText keeps the full Build Number in a string field; the numeric version fields only carry
// MAJOR.MINOR.PATCH, so a lifetime count beyond 65535 is never truncated into them.
func (w windowsIdentity) productVersionText() string {
	build := "Unofficial"
	if w.buildNumber != "" {
		build = "Build " + w.buildNumber
	}
	profile := map[string]string{"public": "Public", "development": "Development"}[w.profile]
	return strings.TrimPrefix(w.version, "v") + " (" + build + ", " + profile + ")"
}

func (w windowsIdentity) sourceText() string {
	if len(w.commit) < 12 {
		return "Source revision unavailable"
	}
	text := "Source revision " + w.commit[:12]
	switch w.dirty {
	case "true":
		text += " with uncommitted changes"
	case "unknown":
		text += ", working tree state unknown"
	}
	return text
}

func windowsResources(repositoryRoot, buildPackage string, identity windowsIdentity) (*winres.ResourceSet, string, error) {
	iconFile, err := os.Open(filepath.Join(repositoryRoot, "build", "windows", "icon.ico"))
	if err != nil {
		return nil, "", fmt.Errorf("open Windows application icon: %w", err)
	}
	defer iconFile.Close()
	icon, err := winres.LoadICO(iconFile)
	if err != nil {
		return nil, "", fmt.Errorf("decode Windows application icon: %w", err)
	}
	resources := &winres.ResourceSet{}
	// Wails v3 uses group ID 3 for window icons; Explorer uses the first icon group.
	if err := resources.SetIcon(winres.ID(3), icon); err != nil {
		return nil, "", fmt.Errorf("set Windows application icon: %w", err)
	}
	if buildPackage == "./cmd/desktop" {
		// Wails registers the native window class using IDI_APPLICATION. Reuse the
		// existing icon images so both the class and individual windows have the logo.
		const idiApplication = winres.ID(32512)
		group := resources.Get(winres.RT_GROUP_ICON, winres.ID(3), winres.LCIDNeutral)
		if err := resources.Set(winres.RT_GROUP_ICON, idiApplication, winres.LCIDNeutral, group); err != nil {
			return nil, "", fmt.Errorf("set Windows window class icon: %w", err)
		}
	}
	// One en-US string table: the numeric fields carry MAJOR.MINOR.PATCH only, while ProductVersion and Comments
	// spell out the whole build identity, including a Build Number of any size.
	var info winversion.Info
	numeric := strings.TrimPrefix(identity.version, "v")
	if err := info.Set(winversion.LangDefault, winversion.ProductName, "WWlogs"); err != nil {
		return nil, "", fmt.Errorf("set Windows version information: %w", err)
	}
	info.SetFileVersion(numeric)
	info.SetProductVersion(numeric)
	for field, value := range map[string]string{
		winversion.FileDescription:  "WWlogs",
		winversion.OriginalFilename: identity.originalFilename,
		winversion.ProductVersion:   identity.productVersionText(),
		winversion.Comments:         identity.sourceText(),
	} {
		if err := info.Set(winversion.LangDefault, field, value); err != nil {
			return nil, "", fmt.Errorf("set Windows version information: %w", err)
		}
	}
	resources.SetVersionInfo(info)
	var object bytes.Buffer
	if err := resources.WriteObject(&object, winres.ArchAMD64); err != nil {
		return nil, "", fmt.Errorf("generate Windows resources: %w", err)
	}
	// Go's packer does not apply overlays to syso files. Reserve the main package's
	// resource path exclusively so concurrent builds cannot overwrite or reuse it.
	path := filepath.Join(repositoryRoot, filepath.FromSlash(buildPackage), "wwlogs_windows_amd64.syso")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("reserve Windows icon resource (another build or interrupted build may own it): %w", err)
	}
	_, writeErr := file.Write(object.Bytes())
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(path)
		return nil, "", fmt.Errorf("write Windows icon resource: %w", err)
	}
	return resources, path, nil
}

func verifyWindowsResources(path string, expected *winres.ResourceSet) error {
	executable, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open executable resources: %w", err)
	}
	defer executable.Close()
	actual, err := winres.LoadFromEXE(executable)
	if err != nil {
		return fmt.Errorf("read executable resources: %w", err)
	}
	var mismatch bool
	expected.Walk(func(typeID, resourceID winres.Identifier, language uint16, data []byte) bool {
		mismatch = !bytes.Equal(data, actual.Get(typeID, resourceID, language))
		return !mismatch
	})
	if mismatch {
		return errors.New("executable resources do not match the expected icon and version information")
	}
	return nil
}

func validatePrivateKeyPEM(data []byte) ([]byte, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "RSA PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected one PKCS#1 RSA PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid PKCS#1 RSA private key")
	}
	if err := privateKey.Validate(); err != nil {
		return nil, errors.New("RSA private key validation failed")
	}
	if privateKey.N.BitLen() != 2048 {
		return nil, errors.New("RSA private key must have a 2048-bit modulus")
	}
	return append([]byte(nil), block.Bytes...), nil
}

func writeTemporaryFile(directory, pattern string, contents []byte) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(contents); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func verifyNoPlaintextKey(path string, pemData, derData []byte) error {
	executable, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read built executable for plaintext scan: %w", err)
	}
	defer clear(executable)
	if len(executable) == 0 {
		return errors.New("built executable is empty")
	}
	if bytes.Contains(executable, pemData) || bytes.Contains(executable, derData) {
		return errors.New("built executable contains plaintext RSA private key material")
	}
	return nil
}

// gitIdentity returns the full HEAD commit and whether tracked files are modified. When Git cannot answer,
// the commit is empty and the build reports it as UNAVAILABLE; nothing is guessed.
func gitIdentity(root string) (commit, dirty string) {
	head := exec.Command("git", "rev-parse", "HEAD")
	head.Dir = root
	output, err := head.Output()
	if err != nil {
		return "", "unknown"
	}
	commit = strings.TrimSpace(string(output))
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	status.Dir = root
	output, err = status.Output()
	if err != nil {
		return commit, "unknown"
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return commit, "false"
	}
	return commit, "true"
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// verifyBuildIdentity runs the built executable with -version and checks that the profile it reports
// is the one that was requested, so a mislabelled build can never be installed.
func verifyBuildIdentity(path, profile, version, buildNumber, commit string) error {
	output, err := exec.Command(path, "-version").Output()
	if err != nil {
		return fmt.Errorf("run built executable for identity check: %w", err)
	}
	text := string(output)
	wantProfile := "Build profile: " + profile
	if !strings.Contains(text, wantProfile+"\n") && !strings.Contains(text, wantProfile+"\r\n") {
		return fmt.Errorf("built executable does not report %q", wantProfile)
	}
	if !strings.Contains(text, "WWlogs version: "+version) {
		return fmt.Errorf("built executable does not report version %s", version)
	}
	wantBuild := "Build number: unofficial"
	if buildNumber != "" {
		wantBuild = "Build number: " + buildNumber
	}
	if !strings.Contains(text, wantBuild+"\n") && !strings.Contains(text, wantBuild+"\r\n") {
		return fmt.Errorf("built executable does not report %q", wantBuild)
	}
	if commit != "" && !strings.Contains(text, "Commit: "+commit) {
		return fmt.Errorf("built executable does not report commit %s", commit)
	}
	return nil
}

// repositoryRootFor locates the repository root from this script's source path.
// ownerKeyRelativePath is the one location of the owner-local RSA key input, relative to the repository root. This
// program is its only reader; there is no fallback location, and nothing here creates or rewrites the file.
var ownerKeyRelativePath = filepath.Join(".local", "keys", "privateKey.pem")

// errOwnerKeyMissing is matched by scripts/verify.ps1 to report NOT RUN instead of FAIL; keep the wording stable.
var errOwnerKeyMissing = errors.New("owner-local private key is not present")

// ownerKeyError reports a key access failure without the private path: only the operating-system reason is kept.
func ownerKeyError(action string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return errOwnerKeyMissing
	}
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		err = pathError.Err
	}
	return fmt.Errorf("%s owner-local private key: %w", action, err)
}

// checkOwnerKey reports whether the key input exists as a regular file, without reading its contents.
func checkOwnerKey(root string) error {
	info, err := os.Lstat(filepath.Join(root, ownerKeyRelativePath))
	if err != nil {
		return ownerKeyError("inspect", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("owner-local private key is not a regular file")
	}
	return nil
}

// readOwnerKey reads the key input for the build-time encryption step. The caller clears the returned bytes.
func readOwnerKey(root string) ([]byte, error) {
	if err := checkOwnerKey(root); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, ownerKeyRelativePath))
	if err != nil {
		return nil, ownerKeyError("read", err)
	}
	return data, nil
}

func repositoryRootFor() string {
	_, sourcePath, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(sourcePath))
}

// registeredVersion reads the product version line registered in internal/versioninfo/versions.json.
// It returns an empty string when the registry is missing, in which case the caller must pass -version.
func registeredVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "internal", "versioninfo", "versions.json"))
	if err != nil {
		return ""
	}
	var registry struct {
		Product struct {
			DevelopmentLine string `json:"developmentLine"`
		} `json:"product"`
	}
	if json.Unmarshal(data, &registry) != nil {
		return ""
	}
	return registry.Product.DevelopmentLine
}

// mergeTestOverlay adds the committed test overlay (tests/go-overlay.json) to the replacement map, with every
// path made absolute so the combined overlay does not depend on the working directory.
func mergeTestOverlay(root string, replacements map[string]string) error {
	data, err := os.ReadFile(filepath.Join(root, "tests", "go-overlay.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read test overlay: %w", err)
	}
	var overlay struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		return fmt.Errorf("parse test overlay: %w", err)
	}
	for from, to := range overlay.Replace {
		absoluteFrom := filepath.Join(root, filepath.FromSlash(from))
		absoluteTo := ""
		if to != "" {
			absoluteTo = filepath.Join(root, filepath.FromSlash(to))
		}
		if existing, exists := replacements[absoluteFrom]; exists {
			if existing == absoluteTo {
				continue
			}
			return fmt.Errorf("test overlay replaces %s with a different source than the build overlay", from)
		}
		replacements[absoluteFrom] = absoluteTo
	}
	return nil
}

// buildTestBinary compiles (without running) the tests of one package into outputPath. Test-only tools use
// it when a test needs the embedded key payload that plain "go test" does not carry.
func buildTestBinary(root, pkg, outputPath, overlayPath string, buildTags []string, keyPEM, privateDER []byte) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	args := append([]string{"test", "-c", "-trimpath", "-buildvcs=false"}, buildTags...)
	args = append(args, "-overlay", overlayPath, "-o", outputPath, pkg)
	build := exec.Command("go", args...)
	build.Dir = root
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build test binary: %w", err)
	}
	if err := verifyNoPlaintextKey(outputPath, keyPEM, privateDER); err != nil {
		os.Remove(outputPath)
		return err
	}
	digest, err := fileSHA256(outputPath)
	if err != nil {
		return fmt.Errorf("hash test binary: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Built test binary %s\n", outputPath)
	fmt.Fprintf(os.Stdout, "Test binary SHA-256: %s\n", digest)
	fmt.Fprintln(os.Stdout, "Plaintext RSA PEM/DER scan: PASS")
	return nil
}

// gameAssetPackName is the overlay-only file name the generated source embeds.
const gameAssetPackName = "game_assets.pack"

// writeGameAssetPack builds the image pack from assets/manifest.json and a Go source file that embeds it. The pack goes
// through the overlay as a file, because a Go string literal of this size would make the compiler slow and memory-hungry.
func writeGameAssetPack(root string) (packPath, sourcePath string, err error) {
	assetRoot := filepath.Join(root, "assets")
	data, err := os.ReadFile(filepath.Join(assetRoot, "manifest.json"))
	if err != nil {
		return "", "", fmt.Errorf("read game asset manifest: %w", err)
	}
	manifest, err := assets.ParseManifest(data)
	if err != nil {
		return "", "", err
	}
	var pack bytes.Buffer
	stats, err := assets.WritePack(&pack, os.DirFS(assetRoot), manifest)
	if err != nil {
		return "", "", fmt.Errorf("build game image pack: %w", err)
	}
	packPath, err = writeTemporaryFile(os.TempDir(), "wwlogs-game-assets-*.pack", pack.Bytes())
	if err != nil {
		return "", "", fmt.Errorf("write game image pack: %w", err)
	}
	source := fmt.Sprintf("package desktop\n\nimport _ \"embed\"\n\n// Generated by scripts/embed-key.go from assets/manifest.json; never committed.\n//\n//go:embed %s\nvar gameAssetArchive string\n", gameAssetPackName)
	sourcePath, err = writeTemporaryFile(os.TempDir(), "wwlogs-game-asset-archive-*.go", []byte(source))
	if err != nil {
		os.Remove(packPath)
		return "", "", fmt.Errorf("write game image pack source: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Game images: %d files (%d bytes), %d distinct (%d bytes), pack %d bytes, resource %s\n", stats.Files, stats.FileBytes, stats.Distinct, stats.DistinctSize, pack.Len(), manifest.Resource)
	return packPath, sourcePath, nil
}

// frontendArchiveName is the overlay-only file name the generated source embeds.
const frontendArchiveName = "frontend_archive.zip"

// writeFrontendArchive zips the built frontend in dist and writes a Go source file that embeds it, replacing
// internal/desktop/frontend_archive.go through the build overlay. The directory must be outside the repository so a
// product build never reads ignored or untracked repository content. Entries are sorted and carry a fixed time, so the
// same build output gives the same archive.
func writeFrontendArchive(root, dist string) (archivePath, sourcePath string, err error) {
	dist, err = filepath.Abs(dist)
	if err != nil {
		return "", "", fmt.Errorf("resolve frontend directory: %w", err)
	}
	if relative, err := filepath.Rel(root, dist); err == nil && !strings.HasPrefix(relative, "..") {
		return "", "", fmt.Errorf("frontend directory %s is inside the repository; build it with scripts/frontend.ps1 -OutDir outside the repository", dist)
	}
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		return "", "", fmt.Errorf("frontend directory has no index.html: %w", err)
	}
	var files []string
	if err := filepath.WalkDir(dist, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return "", "", fmt.Errorf("list frontend files: %w", err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, path := range files {
		relative, err := filepath.Rel(dist, path)
		if err != nil {
			return "", "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("read frontend file: %w", err)
		}
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(relative), Method: zip.Deflate, Modified: modified})
		if err != nil {
			return "", "", err
		}
		if _, err := entry.Write(data); err != nil {
			return "", "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", "", fmt.Errorf("write frontend archive: %w", err)
	}
	archivePath, err = writeTemporaryFile(os.TempDir(), "wwlogs-frontend-archive-*.zip", archive.Bytes())
	if err != nil {
		return "", "", fmt.Errorf("write frontend archive: %w", err)
	}
	source := fmt.Sprintf("package desktop\n\nimport _ \"embed\"\n\n// Generated by scripts/embed-key.go from the built frontend; never committed.\n//\n//go:embed %s\nvar frontendArchive string\n", frontendArchiveName)
	sourcePath, err = writeTemporaryFile(os.TempDir(), "wwlogs-frontend-archive-*.go", []byte(source))
	if err != nil {
		os.Remove(archivePath)
		return "", "", fmt.Errorf("write frontend archive source: %w", err)
	}
	return archivePath, sourcePath, nil
}
