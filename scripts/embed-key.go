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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tc-hib/winres"
	winversion "github.com/tc-hib/winres/version"

	"wwlogs/internal/assets"
	"wwlogs/internal/keyembed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "embed-key: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	outputFlag := flag.String("o", "", "output executable path (defaults to bin/WWlogs[-Desktop].exe for public or work/dist/WWlogs[-Desktop]-dev.exe for development)")
	profileFlag := flag.String("profile", "public", "build profile: public (no raw diagnostic outputs) or development (full diagnostics)")
	versionFlag := flag.String("version", "", "product version to embed (default: the version registered in internal/versioninfo)")
	requireClean := flag.Bool("require-clean", false, "fail when the Git working tree has uncommitted changes to tracked files")
	testBinary := flag.String("test-binary", "", "build a test binary of this package (for example ./internal/capture) carrying the encrypted key payload and the committed test overlay, instead of the executable; requires -o")
	targetFlag := flag.String("target", "capture", "executable to build: capture (console analyzer) or desktop (Wails desktop host; requires -frontend-dist)")
	frontendDist := flag.String("frontend-dist", "", "built frontend directory to embed into the desktop host; must be outside the repository (scripts/frontend.ps1 -Action Build)")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	var buildTags []string
	switch *profileFlag {
	case "public":
	case "development":
		buildTags = []string{"-tags", "wwdev"}
	default:
		return fmt.Errorf("unknown profile %q: use public or development", *profileFlag)
	}
	var buildPackage string
	switch *targetFlag {
	case "capture":
		buildPackage = "./cmd/capture"
	case "desktop":
		buildPackage = "./cmd/desktop"
		if *testBinary != "" {
			return errors.New("-test-binary builds packages, not the desktop target")
		}
		if *frontendDist == "" {
			return errors.New("-target desktop requires -frontend-dist")
		}
		// Wails' production tag removes DevTools and the dev-server hook; the Development profile keeps Wails debug mode.
		if *profileFlag == "public" {
			buildTags = []string{"-tags", "production"}
		}
	default:
		return fmt.Errorf("unknown target %q: use capture or desktop", *targetFlag)
	}
	if *outputFlag == "" && *testBinary == "" {
		name := map[string]string{"capture": "WWlogs", "desktop": "WWlogs-Desktop"}[*targetFlag]
		*outputFlag = filepath.Join("bin", name+".exe")
		if *profileFlag == "development" {
			*outputFlag = filepath.Join("work", "dist", name+"-dev.exe")
		}
	}
	version := *versionFlag
	if version == "" {
		version = registeredVersion(repositoryRootFor())
	}

	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("cannot locate build script")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(sourcePath))
	keyPath := filepath.Join(repositoryRoot, "privateKey.pem")
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read project-root privateKey.pem: %w", err)
	}
	defer clear(keyPEM)
	privateDER, err := validatePrivateKeyPEM(keyPEM)
	if err != nil {
		return fmt.Errorf("validate project-root privateKey.pem: %w", err)
	}
	defer clear(privateDER)

	payload, err := keyembed.Encrypt(keyPEM)
	if err != nil {
		return fmt.Errorf("encrypt build key payload: %w", err)
	}

	outputPath := *outputFlag
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(repositoryRoot, outputPath)
	}
	outputPath = filepath.Clean(outputPath)
	outputDirectory := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporaryExecutable, err := temporaryPath(outputDirectory, ".wwlogs-build-*.exe")
	if err != nil {
		return fmt.Errorf("prepare temporary executable path: %w", err)
	}
	defer os.Remove(temporaryExecutable)

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
	replacements := map[string]string{
		filepath.Join(repositoryRoot, "internal", "capture", "key_payload.go"): generatedSourcePath,
		filepath.Join(repositoryRoot, "internal", "capture", "master_data.db"): masterDatabasePath,
	}
	if *targetFlag == "desktop" {
		archiveSourcePath, err := writeFrontendArchiveSource(repositoryRoot, *frontendDist)
		if err != nil {
			return err
		}
		defer os.Remove(archiveSourcePath)
		replacements[filepath.Join(repositoryRoot, "internal", "desktop", "frontend_archive.go")] = archiveSourcePath
		packPath, packSourcePath, err := writeGameAssetPack(repositoryRoot)
		if err != nil {
			return err
		}
		defer os.Remove(packPath)
		defer os.Remove(packSourcePath)
		replacements[filepath.Join(repositoryRoot, "internal", "desktop", "game_asset_archive.go")] = packSourcePath
		replacements[filepath.Join(repositoryRoot, "internal", "desktop", gameAssetPackName)] = packPath
	}
	if *testBinary != "" {
		if *outputFlag == "" {
			return errors.New("-test-binary requires -o")
		}
		if err := mergeTestOverlay(repositoryRoot, replacements); err != nil {
			return err
		}
	}
	var windowsResourceSet *winres.ResourceSet
	if *testBinary == "" {
		resources, resourcePath, err := windowsResources(repositoryRoot, buildPackage, version)
		if err != nil {
			return err
		}
		defer os.Remove(resourcePath)
		windowsResourceSet = resources
	}
	overlayData, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replacements})
	if err != nil {
		return fmt.Errorf("encode Go build overlay: %w", err)
	}
	defer clear(overlayData)
	overlayPath, err := writeTemporaryFile(os.TempDir(), "wwlogs-key-overlay-*.json", overlayData)
	if err != nil {
		return fmt.Errorf("write Go build overlay: %w", err)
	}
	defer os.Remove(overlayPath)

	if *testBinary != "" {
		return buildTestBinary(repositoryRoot, *testBinary, outputPath, overlayPath, buildTags, keyPEM, privateDER)
	}

	commit, dirty := gitIdentity(repositoryRoot)
	if *requireClean && dirty != "false" {
		return fmt.Errorf("working tree state is %q; commit or stash tracked changes before a clean build", dirty)
	}
	ldflags := "-X wwlogs/internal/buildinfo.injectedVersion=" + version +
		" -X wwlogs/internal/buildinfo.injectedCommit=" + commit +
		" -X wwlogs/internal/buildinfo.injectedDirty=" + dirty
	if *targetFlag == "desktop" {
		// A GUI subsystem executable opens no console window; -version still writes to redirected output.
		ldflags += " -H windowsgui"
	}
	// -buildvcs=false keeps the result independent of Git being reachable; the identity is injected above.
	buildArgs := append([]string{"build", "-trimpath", "-buildvcs=false"}, buildTags...)
	buildArgs = append(buildArgs, "-ldflags", ldflags, "-overlay", overlayPath, "-o", temporaryExecutable, buildPackage)
	build := exec.Command("go", buildArgs...)
	build.Dir = repositoryRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build executable: %w", err)
	}
	if err := verifyNoPlaintextKey(temporaryExecutable, keyPEM, privateDER); err != nil {
		return err
	}
	if err := verifyBuildIdentity(temporaryExecutable, *profileFlag, version, commit); err != nil {
		return err
	}
	if err := verifyWindowsResources(temporaryExecutable, windowsResourceSet); err != nil {
		return err
	}
	if err := installExecutable(temporaryExecutable, outputPath); err != nil {
		return fmt.Errorf("install verified executable: %w", err)
	}

	digest, err := fileSHA256(outputPath)
	if err != nil {
		return fmt.Errorf("hash installed executable: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Built %s\n", outputPath)
	fmt.Fprintf(os.Stdout, "Profile: %s  Version: %s  Commit: %s  Working tree modified: %s\n", *profileFlag, version, commit, dirty)
	fmt.Fprintf(os.Stdout, "Executable SHA-256: %s\n", digest)
	fmt.Fprintln(os.Stdout, "Plaintext RSA PEM/DER scan: PASS")
	fmt.Fprintln(os.Stdout, "Build identity check: PASS")
	fmt.Fprintln(os.Stdout, "Embedded Windows icon and version check: PASS")
	return nil
}

func windowsResources(repositoryRoot, buildPackage, productVersion string) (*winres.ResourceSet, string, error) {
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
	originalFilename := "WWlogs.exe"
	if buildPackage == "./cmd/desktop" {
		originalFilename = "WWlogs-Desktop.exe"
		// Wails registers the native window class using IDI_APPLICATION. Reuse the
		// existing icon images so both the class and individual windows have the logo.
		const idiApplication = winres.ID(32512)
		group := resources.Get(winres.RT_GROUP_ICON, winres.ID(3), winres.LCIDNeutral)
		if err := resources.Set(winres.RT_GROUP_ICON, idiApplication, winres.LCIDNeutral, group); err != nil {
			return nil, "", fmt.Errorf("set Windows window class icon: %w", err)
		}
	}
	var info winversion.Info
	info.SetFileVersion(strings.TrimPrefix(productVersion, "v"))
	info.SetProductVersion(strings.TrimPrefix(productVersion, "v"))
	for field, value := range map[string]string{
		winversion.FileDescription:  "WWlogs",
		winversion.ProductName:      "WWlogs",
		winversion.OriginalFilename: originalFilename,
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

func temporaryPath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
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

func installExecutable(temporaryExecutable, outputPath string) error {
	if err := os.Rename(temporaryExecutable, outputPath); err == nil {
		return nil
	} else if _, statErr := os.Stat(outputPath); statErr != nil {
		return err
	}

	backupPath, err := temporaryPath(filepath.Dir(outputPath), ".wwlogs-backup-*.exe")
	if err != nil {
		return err
	}
	defer os.Remove(backupPath)
	if err := os.Rename(outputPath, backupPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryExecutable, outputPath); err != nil {
		restoreErr := os.Rename(backupPath, outputPath)
		if restoreErr != nil {
			return fmt.Errorf("replace failed (%v), and restoring previous executable failed: %w", err, restoreErr)
		}
		return err
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
func verifyBuildIdentity(path, profile, version, commit string) error {
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
	if commit != "" && !strings.Contains(text, "Commit: "+commit) {
		return fmt.Errorf("built executable does not report commit %s", commit)
	}
	return nil
}

// repositoryRootFor locates the repository root from this script's source path.
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

// writeFrontendArchiveSource zips the built frontend in dist into a temporary Go source file that replaces
// internal/desktop/frontend_archive.go through the build overlay. The directory must be outside the repository so a
// product build never reads ignored or untracked repository content. Entries are sorted and carry a fixed time, so the
// same build output gives the same archive.
func writeFrontendArchiveSource(root, dist string) (string, error) {
	dist, err := filepath.Abs(dist)
	if err != nil {
		return "", fmt.Errorf("resolve frontend directory: %w", err)
	}
	if relative, err := filepath.Rel(root, dist); err == nil && !strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("frontend directory %s is inside the repository; build it with scripts/frontend.ps1 -OutDir outside the repository", dist)
	}
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		return "", fmt.Errorf("frontend directory has no index.html: %w", err)
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
		return "", fmt.Errorf("list frontend files: %w", err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, path := range files {
		relative, err := filepath.Rel(dist, path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read frontend file: %w", err)
		}
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(relative), Method: zip.Deflate, Modified: modified})
		if err != nil {
			return "", err
		}
		if _, err := entry.Write(data); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("write frontend archive: %w", err)
	}
	source := fmt.Sprintf("package desktop\n\n// Generated by scripts/embed-key.go from the built frontend; never committed.\nvar frontendArchive = []byte(%q)\n", archive.String())
	return writeTemporaryFile(os.TempDir(), "wwlogs-frontend-archive-*.go", []byte(source))
}
