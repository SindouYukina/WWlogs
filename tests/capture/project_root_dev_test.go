//go:build wwdev

package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixtureProject(t *testing.T, root, gitignore string) {
	t.Helper()
	for _, directory := range []string{filepath.Join(root, "cmd", "capture"), filepath.Join(root, "bin"), filepath.Join(root, "work", "dist")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module wwlogs\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProjectRootPrefersExecutableLocation(t *testing.T) {
	root := t.TempDir()
	writeFixtureProject(t, root, "/logs/\n")
	elsewhere := t.TempDir()
	got, err := resolveProjectRoot(filepath.Join(root, "work", "dist", "WWlogs-dev.exe"), elsewhere)
	if err != nil || got != root {
		t.Fatalf("resolveProjectRoot() = %q, %v; want %q", got, err, root)
	}
}

func TestResolveProjectRootFallsBackToWorkingDirectoryAncestors(t *testing.T) {
	root := t.TempDir()
	writeFixtureProject(t, root, "/logs/\n")
	nested := filepath.Join(root, "work", "dist")
	got, err := resolveProjectRoot(filepath.Join(t.TempDir(), "WWlogs-dev.exe"), nested)
	if err != nil || got != root {
		t.Fatalf("resolveProjectRoot() = %q, %v; want %q", got, err, root)
	}
}

func TestResolveProjectRootRejectsUnrelatedModule(t *testing.T) {
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "cmd", "capture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProjectRoot(filepath.Join(other, "tool.exe"), other); err == nil {
		t.Fatal("a go.mod for a different module was accepted as the WWlogs project root")
	}
}

func TestResolveProjectRootReportsFailureInsteadOfFallingBack(t *testing.T) {
	_, err := resolveProjectRoot(filepath.Join(t.TempDir(), "WWlogs-dev.exe"), t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no project root can be found")
	}
	if !strings.Contains(err.Error(), "cannot determine the WWlogs project root") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestRequireIgnoredLogsDirectory(t *testing.T) {
	ignored := t.TempDir()
	writeFixtureProject(t, ignored, "*.exe\n/logs/\n")
	if err := requireIgnoredLogsDirectory(ignored); err != nil {
		t.Fatalf("ignored logs directory rejected: %v", err)
	}
	unignored := t.TempDir()
	writeFixtureProject(t, unignored, "*.exe\n")
	if err := requireIgnoredLogsDirectory(unignored); err == nil {
		t.Fatal("a repository that does not ignore logs/ must not receive diagnostics")
	}
}
