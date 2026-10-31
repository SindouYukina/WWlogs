package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRuntimePathUsesExecutableLayoutBeforeWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	binDirectory := filepath.Join(root, "bin")
	dataDirectory := filepath.Join(root, "data")
	workingDirectory := filepath.Join(root, "elsewhere")
	for _, directory := range []string{binDirectory, dataDirectory, workingDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	masterPath := filepath.Join(dataDirectory, "master_data.db")
	if err := os.WriteFile(masterPath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	workingMasterPath := filepath.Join(workingDirectory, "data", "master_data.db")
	if err := os.MkdirAll(filepath.Dir(workingMasterPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workingMasterPath, []byte("working directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := resolveRuntimePath(filepath.Join("data", "master_data.db"), filepath.Join(binDirectory, "WWlogs.exe"), workingDirectory)
	if got != masterPath {
		t.Fatalf("resolveRuntimePath() = %q, want executable-relative path %q", got, masterPath)
	}
}
