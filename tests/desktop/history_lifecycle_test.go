package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
	"wwlogs/internal/service"
)

// The recording setting, Clear All and the recovery of an unreadable history are three separate things. These tests wire a
// Bridge as app.go does (a CaptureService whose sessions begin their history through historyStarter, the settings store and
// the history store) with synthetic key inputs and a scripted capture session.

// computerInputs are synthetic key inputs of two PCs: their motherboards differ, the protocol key is the same.
func computerInputs(t *testing.T) (this, another history.KeyInputs) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	inputs := func(uuid string) history.KeyInputs {
		return history.KeyInputs{
			Machine:     func() (history.Machine, error) { return history.Machine{SystemUUID: []byte(uuid)}, nil },
			ProtocolKey: func() ([]byte, error) { return bytes.Clone(der), nil },
		}
	}
	return inputs("WWLGDESKTOPTEST1"), inputs("WWLGDESKTOPTEST2")
}

type lifecycleFixture struct {
	bridge *Bridge
	mu     sync.Mutex
	opened []capture.SessionConfig
}

func newLifecycleFixture(t *testing.T, store *history.Store, settingsPath string) *lifecycleFixture {
	t.Helper()
	fixture := &lifecycleFixture{}
	opener := func(cfg capture.SessionConfig) (service.Session, error) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.opened = append(fixture.opened, cfg)
		return &launchSession{}, nil
	}
	settings := loadSettings(settingsPath, testLocaleCatalog())
	captureService := service.NewCaptureService(opener)
	recorders := &recorderTracker{}
	captureService.SetHistory(historyStarter(store, false, recorders))
	captureService.SetHistoryRecording(!settings.current().Settings.HistoryRecordingDisabled)
	fixture.bridge = &Bridge{capture: captureService, settings: settings, history: store, recorders: recorders}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = captureService.Shutdown(ctx)
	})
	return fixture
}

// capture runs one capture session to its end and returns the configuration it opened with and how its history began.
func (f *lifecycleFixture) capture(t *testing.T) (capture.SessionConfig, string) {
	t.Helper()
	if err := f.bridge.capture.Start(capture.SessionConfig{LocalIPv4: "192.0.2.10"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.bridge.capture.Status().State != service.StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("the capture did not start: %+v", f.bridge.capture.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	began := f.bridge.capture.Status().History
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.bridge.capture.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened[len(f.opened)-1], began
}

// fileDigests is the SHA-256 of the history database and its key record.
func fileDigests(t *testing.T, path string) map[string][32]byte {
	t.Helper()
	digests := map[string][32]byte{}
	for _, name := range []string{path, filepath.Join(filepath.Dir(path), "combat-history.kdf")} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		digests[filepath.Base(name)] = sha256.Sum256(data)
	}
	return digests
}

func requireDigests(t *testing.T, want, got map[string][32]byte, when string) {
	t.Helper()
	for name, digest := range want {
		if got[name] != digest {
			t.Fatalf("%s: %s changed", when, name)
		}
	}
}

// TestRecordingSettingNeverTouchesTheHistory: switching recording off and on changes the setting and what later captures
// store, never the history itself; the setting and the history both survive a restart, each on its own.
func TestRecordingSettingNeverTouchesTheHistory(t *testing.T) {
	this, _ := computerInputs(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "history", "combat-history.db")
	settingsPath := filepath.Join(dir, "settings.json")
	store, err := history.Open(history.Options{Path: path, KeyInputs: this})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newLifecycleFixture(t, store, settingsPath)
	if cfg, began := fixture.capture(t); cfg.HistoryPaused || began != service.HistoryRecording {
		t.Fatalf("with recording on a capture opened paused=%v, history %q", cfg.HistoryPaused, began)
	}
	before := fileDigests(t, path)
	for _, enabled := range []bool{false, true, false} {
		if _, err := fixture.bridge.SetHistoryRecording(enabled); err != nil {
			t.Fatal(err)
		}
		if status := store.Status(); status.State != history.StateReady {
			t.Fatalf("switching recording to %v changed the history: %+v", enabled, status)
		}
		requireDigests(t, before, fileDigests(t, path), "a recording switch")
		if indicator := fixture.bridge.historyIndicator(); indicator.Enabled != enabled || (enabled && indicator.State != historyWaiting) || (!enabled && indicator.State != historyDisabled) {
			t.Fatalf("indicator after switching to %v: %+v", enabled, indicator)
		}
	}
	// Recording is off: a capture runs with its recorder paused and stores nothing, and the stored history can still be read.
	if cfg, began := fixture.capture(t); !cfg.HistoryPaused || began != service.HistoryRecording {
		t.Fatalf("with recording off a capture opened paused=%v, history %q", cfg.HistoryPaused, began)
	}
	requireDigests(t, before, fileDigests(t, path), "a capture with recording off")
	if _, err := store.Year(context.Background(), time.Now().Year(), time.UTC); err != nil {
		t.Fatalf("the history cannot be read while recording is off: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// A restart: the setting and the history come back separately. Closing checkpoints the log into the database, so only the
	// key record is compared byte for byte here.
	reopened, err := history.Open(history.Options{Path: path, KeyInputs: this})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status := reopened.Status(); status.State != history.StateReady || status.Migration != "" {
		t.Fatalf("after a restart: %+v", status)
	}
	if fileDigests(t, path)["combat-history.kdf"] != before["combat-history.kdf"] {
		t.Fatal("the key record changed across a restart")
	}
	restarted := newLifecycleFixture(t, reopened, settingsPath)
	if !restarted.bridge.settings.current().Settings.HistoryRecordingDisabled {
		t.Fatal("recording came back on after a restart")
	}
	if cfg, _ := restarted.capture(t); !cfg.HistoryPaused {
		t.Fatal("after a restart a capture recorded although recording is off")
	}
}

// TestUnreadableHistoryIsRecoveredWithRecordingOff: with recording off, a history of another PC is still checked, shown as
// unreadable and offered for a rebuild; the rebuild replaces it and leaves recording off, for later captures and after a
// restart.
func TestUnreadableHistoryIsRecoveredWithRecordingOff(t *testing.T) {
	this, another := computerInputs(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "history", "combat-history.db")
	settingsPath := filepath.Join(dir, "settings.json")
	foreign, err := history.Open(history.Options{Path: path, KeyInputs: another})
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}
	foreignFiles := fileDigests(t, path)
	settings := loadSettings(settingsPath, testLocaleCatalog())
	if _, err := settings.setHistoryRecording(false); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(history.Options{Path: path, KeyInputs: this})
	if err == nil || store.Status().State != history.StateUnavailable || !store.Status().Rebuildable {
		t.Fatalf("a history of another computer: %+v (%v)", store.Status(), err)
	}
	defer store.Close()
	fixture := newLifecycleFixture(t, store, settingsPath)
	if indicator := fixture.bridge.historyIndicator(); indicator.State != historyUnavailable || !indicator.Rebuildable || indicator.Enabled {
		t.Fatalf("with recording off the unreadable history shows %+v", indicator)
	}
	if cfg, began := fixture.capture(t); !cfg.HistoryPaused || began != service.HistoryUnavailable {
		t.Fatalf("a capture with recording off and an unreadable history: paused=%v, history %q", cfg.HistoryPaused, began)
	}
	requireDigests(t, foreignFiles, fileDigests(t, path), "a capture beside the unreadable history")
	if retried := fixture.bridge.RetryHistory(); retried.Open {
		t.Fatalf("a retry opened the history of another computer: %+v", retried)
	}
	requireDigests(t, foreignFiles, fileDigests(t, path), "a retry")
	rebuilt := fixture.bridge.RebuildHistory()
	if !rebuilt.Open || rebuilt.Refused != "" {
		t.Fatalf("rebuild: %+v", rebuilt)
	}
	if !loadSettings(settingsPath, testLocaleCatalog()).current().Settings.HistoryRecordingDisabled || !fixture.bridge.settings.current().Settings.HistoryRecordingDisabled {
		t.Fatal("the rebuild switched recording on")
	}
	if indicator := fixture.bridge.historyIndicator(); indicator.State != historyDisabled || indicator.Rebuildable {
		t.Fatalf("after the rebuild: %+v", indicator)
	}
	if cfg, began := fixture.capture(t); !cfg.HistoryPaused || began != service.HistoryRecording {
		t.Fatalf("a capture after the rebuild: paused=%v, history %q", cfg.HistoryPaused, began)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := history.Open(history.Options{Path: path, KeyInputs: this})
	if err != nil {
		t.Fatalf("the rebuilt history after a restart: %+v (%v)", reopened.Status(), err)
	}
	defer reopened.Close()
	if !loadSettings(settingsPath, testLocaleCatalog()).current().Settings.HistoryRecordingDisabled {
		t.Fatal("recording is on after a restart")
	}
}
