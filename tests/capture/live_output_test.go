//go:build wwdev

package capture

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultLiveCaptureOutputBundleUsesBoundedPrivateSpool(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 2, 11, 37, 28, 0, time.FixedZone("JST", 9*60*60))
	bundle, err := defaultLiveCaptureOutputBundle(root, now)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, "WWlogs", "capture-spool", "LIVE_20261102_113728")
	if bundle.directory != wantDir {
		t.Fatalf("directory = %q, want %q", bundle.directory, wantDir)
	}
	if info, err := os.Stat(bundle.directory); err != nil || !info.IsDir() {
		t.Fatalf("default capture directory not created: info=%v err=%v", info, err)
	}
	for _, path := range []string{bundle.pcap, bundle.console, bundle.combatEvents, bundle.buffEvents, bundle.buffSnapshots, bundle.liveDiagnostics, bundle.effectState, bundle.status, bundle.encounterSummary} {
		if filepath.Dir(path) != bundle.directory {
			t.Fatalf("output %q is outside run directory %q", path, bundle.directory)
		}
	}
	second, err := defaultLiveCaptureOutputBundle(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if second.directory == bundle.directory {
		t.Fatalf("same-second capture reused directory %q", bundle.directory)
	}
}

func TestDefaultLiveCaptureOutputBundleKeepsExistingSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 2, 11, 37, 28, 0, time.UTC)
	first, err := defaultLiveCaptureOutputBundle(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first.pcap, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := defaultLiveCaptureOutputBundle(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(second.directory) != filepath.Dir(first.directory) {
		t.Fatalf("second session %q is not beside the first %q", second.directory, first.directory)
	}
	got, err := os.ReadFile(first.pcap)
	if err != nil || string(got) != "existing" {
		t.Fatalf("existing session was modified: %q err=%v", got, err)
	}
	entries, err := os.ReadDir(second.directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("new session directory is not empty: %v err=%v", entries, err)
	}
}

func TestHasAnyOutputPath(t *testing.T) {
	if hasAnyOutputPath("", "") {
		t.Fatal("empty output paths reported as configured")
	}
	if !hasAnyOutputPath("", "capture.pcap") {
		t.Fatal("configured output path was not detected")
	}
}
