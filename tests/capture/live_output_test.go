//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestDiagnosticStorageReservationsBoundConcurrentSessions(t *testing.T) {
	root := t.TempDir()
	var releases []func()
	for i := 0; i < 4; i++ {
		dir := filepath.Join(root, fmt.Sprintf("LIVE_%d", i))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		release, err := reserveDiagnosticStorage(dir)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	fifth := filepath.Join(root, "LIVE_4")
	os.Mkdir(fifth, 0700)
	if _, err := reserveDiagnosticStorage(fifth); err == nil {
		t.Fatal("concurrent reservations exceeded spool quota")
	}
	releases[0]()
	release, err := reserveDiagnosticStorage(fifth)
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, release := range releases {
		release()
	}
	if _, err := os.Stat(filepath.Join(root, "LIVE_0", "storage.json")); err != nil {
		t.Fatal("old evidence was deleted")
	}
}

func TestDefaultSessionDiagnosticsWriteReplayPCAPAndPreserveStatistics(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	d, err := openSessionDiagnostics(true, layers.LinkTypeEthernet)
	if err != nil {
		t.Fatal(err)
	}
	p := newPairingTestProcessor()
	d.attach(p)
	payload := []byte{1, 2, 3, 4, 5, 6}
	at := time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC)
	d.packet(gopacket.CaptureInfo{Timestamp: at, CaptureLength: len(payload), Length: len(payload)}, payload)
	writeTestDamage(p, 100, 200, 1, 100)
	before := p.damageStatistics.Snapshot()
	summary := d.finish(StopReasonRequested, CaptureCounter{Status: CaptureCounterAvailable})
	after := p.damageStatistics.Snapshot()
	if !reflect.DeepEqual(before, after) || !strings.Contains(summary, "state=complete") || !strings.Contains(summary, "shutdown=clean") {
		t.Fatal("diagnostics changed statistics or did not close cleanly")
	}
	if d.finish(StopReasonRequested, CaptureCounter{}) != "" {
		t.Fatal("diagnostics finalized twice")
	}
	file, err := os.Open(d.pcap.path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := pcapgo.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, info, err := reader.ReadPacketData()
	if err != nil || !bytes.Equal(data, payload) || !info.Timestamp.Equal(at) || reader.LinkType() != layers.LinkTypeEthernet {
		t.Fatalf("PCAP replay mismatch: %v", err)
	}
	for _, name := range []string{"combat-events.jsonl", "console.log", "dps-effects.jsonl", "effect-state-final.json", "status.json"} {
		data, err := os.ReadFile(filepath.Join(d.directory, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("missing diagnostics %s: %v", name, err)
		}
	}
	var storage diagnosticReservation
	data, _ = os.ReadFile(filepath.Join(d.directory, "storage.json"))
	json.Unmarshal(data, &storage)
	if storage.ReservedBytes != 0 {
		t.Fatal("closed session retained a reservation")
	}
}

func TestDiagnosticCapacityFailureDoesNotChangeDamageOrBlock(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	d, err := openSessionDiagnostics(true, layers.LinkTypeEthernet)
	if err != nil {
		t.Fatal(err)
	}
	p := newPairingTestProcessor()
	d.attach(p)
	for _, sink := range d.pipeline.sinks {
		target, ok := sink.target.(*budgetedDiagnosticWriter)
		if ok {
			target.budget.mu.Lock()
			target.budget.remaining = 0
			target.budget.mu.Unlock()
		}
	}
	writeTestDamage(p, 100, 200, 1, 150)
	d.packet(gopacket.CaptureInfo{Timestamp: time.Now(), CaptureLength: 4, Length: 4}, []byte{1, 2, 3, 4})
	summary := d.finish(StopReasonRequested, CaptureCounter{})
	got := p.damageStatistics.Snapshot()
	assertRoleTotalsReconcile(t, got)
	if got.DamageDealt != 150 || got.Roles[0].DamageDealt != 150 || !strings.Contains(summary, "state=incomplete") || !strings.Contains(summary, "writer_failure") {
		t.Fatal("full diagnostic storage lost analysis or hid incomplete output")
	}
}
