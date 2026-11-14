package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleCommit = "97a84ed51a0716bafeca8c5f241743817371209b"

func noVCS() (string, string) { return "", "" }

func TestResolveUsesInjectedValues(t *testing.T) {
	got := resolve("v1.2.3", "", sampleCommit, "false", noVCS)
	if got.Version != "v1.2.3" || got.Commit != sampleCommit || got.Dirty != "false" || got.Profile != profile {
		t.Fatalf("unexpected identity: %+v", got)
	}
}

// The product version Combat History and other records keep is MAJOR.MINOR.PATCH, never the tag's v, and never a guess.
func TestProductVersionDropsTheTagPrefix(t *testing.T) {
	for version, want := range map[string]string{"v0.7.0": "0.7.0", "v1.2.3-rc.1": "1.2.3-rc.1", "not a version": ""} {
		if got := resolve(version, "", sampleCommit, "false", noVCS).ProductVersion(); got != want {
			t.Errorf("ProductVersion of %q = %q, want %q", version, got, want)
		}
	}
}

func TestResolveRejectsMalformedInjection(t *testing.T) {
	got := resolve("not a version", "", "abc", "maybe", noVCS)
	if got.Version != Unavailable {
		t.Fatalf("Version = %q, want UNAVAILABLE for a malformed injection", got.Version)
	}
	if got.Commit != Unavailable || got.Dirty != "unknown" {
		t.Fatalf("malformed commit must be UNAVAILABLE, got commit=%q dirty=%q", got.Commit, got.Dirty)
	}
}

func TestResolveFallsBackToToolchainVCS(t *testing.T) {
	got := resolve("", "", "", "", func() (string, string) { return sampleCommit, "true" })
	if got.Commit != sampleCommit || got.Dirty != "true" {
		t.Fatalf("toolchain VCS values were not used: %+v", got)
	}
	got = resolve("", "", "", "", func() (string, string) { return "short", "false" })
	if got.Commit != Unavailable {
		t.Fatalf("a malformed toolchain revision must not be reported: %q", got.Commit)
	}
}

func TestIdentityStringIsAbbreviatedAndFlagsDirtyTree(t *testing.T) {
	text := resolve("v1.2.3", "", sampleCommit, "true", noVCS).String()
	if !strings.Contains(text, sampleCommit[:12]+"-dirty") || strings.Contains(text, sampleCommit) {
		t.Fatalf("String() = %q", text)
	}
	if got := resolve("v1.2.3", "", "", "", noVCS).String(); !strings.Contains(got, Unavailable) {
		t.Fatalf("String() without a commit = %q", got)
	}
}

func TestIdentityHasNoPathsOrSecrets(t *testing.T) {
	text := resolve("v1.2.3", "", sampleCommit, "false", noVCS).String()
	for _, forbidden := range []string{`\`, "/", ":", "PRIVATE KEY", "privateKey"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("identity %q contains %q", text, forbidden)
		}
	}
}

func TestHashFileMatchesIndependentDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.bin")
	content := []byte("executable stand-in")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if got := hashFile(func() (string, error) { return path, nil }); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("hashFile = %q", got)
	}
}

func TestHashFileReportsUnavailableInsteadOfGuessing(t *testing.T) {
	if got := hashFile(func() (string, error) { return "", errors.New("no executable") }); got != Unavailable {
		t.Fatalf("locate failure: %q", got)
	}
	missing := filepath.Join(t.TempDir(), "missing.exe")
	if got := hashFile(func() (string, error) { return missing, nil }); got != Unavailable {
		t.Fatalf("open failure: %q", got)
	}
}

func TestExecutableSHA256IsCachedAndWellFormed(t *testing.T) {
	first := ExecutableSHA256()
	if first != Unavailable && len(first) != 64 {
		t.Fatalf("ExecutableSHA256 = %q", first)
	}
	if second := ExecutableSHA256(); second != first {
		t.Fatalf("hash changed between calls: %q vs %q", first, second)
	}
}

func TestResolveBuildNumber(t *testing.T) {
	official := resolve("v1.2.3", "128", sampleCommit, "false", noVCS)
	if official.BuildNumber != 128 || !official.Official() || official.BuildLabel() != "128" {
		t.Fatalf("official identity: %+v", official)
	}
	if text := official.String(); !strings.Contains(text, "build 128") {
		t.Fatalf("String() = %q", text)
	}
	// Only a positive decimal injected by the orchestrator is official; anything else is unofficial, never a guess.
	for _, injected := range []string{"", "0", "-1", "012", "12a", "9999999999999999999999", " 5"} {
		got := resolve("v1.2.3", injected, sampleCommit, "false", noVCS)
		if got.BuildNumber != 0 || got.Official() || got.BuildLabel() != "unofficial" {
			t.Fatalf("injected %q gave %+v", injected, got)
		}
	}
}
