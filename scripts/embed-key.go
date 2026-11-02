package main

import (
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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"wwlogs/internal/keyembed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "embed-key: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	outputFlag := flag.String("o", "", "output executable path (default bin/WWlogs.exe for public, bin/WWlogs-dev.exe for development)")
	profileFlag := flag.String("profile", "public", "build profile: public (no raw diagnostic outputs) or development (full diagnostics)")
	versionFlag := flag.String("version", "", "product version to embed (default: the version registered in internal/versioninfo)")
	requireClean := flag.Bool("require-clean", false, "fail when the Git working tree has uncommitted changes to tracked files")
	testBinary := flag.String("test-binary", "", "build a test binary of this package (for example ./internal/capture) carrying the encrypted key payload and the committed test overlay, instead of the executable; requires -o")
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
	if *outputFlag == "" && *testBinary == "" {
		*outputFlag = filepath.Join("bin", "WWlogs.exe")
		if *profileFlag == "development" {
			*outputFlag = filepath.Join("bin", "WWlogs-dev.exe")
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

	replacements := map[string]string{
		filepath.Join(repositoryRoot, "internal", "capture", "key_payload.go"): generatedSourcePath,
	}
	if *testBinary != "" {
		if *outputFlag == "" {
			return errors.New("-test-binary requires -o")
		}
		if err := mergeTestOverlay(repositoryRoot, replacements); err != nil {
			return err
		}
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
	// -buildvcs=false keeps the result independent of Git being reachable; the identity is injected above.
	buildArgs := append([]string{"build", "-trimpath", "-buildvcs=false"}, buildTags...)
	buildArgs = append(buildArgs, "-ldflags", ldflags, "-overlay", overlayPath, "-o", temporaryExecutable, "./cmd/capture")
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
		if _, exists := replacements[absoluteFrom]; exists {
			return fmt.Errorf("test overlay replaces %s, which the key payload already replaces", from)
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
