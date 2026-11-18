package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
	"wwlogs/internal/service"
)

func TestHistoryIndicatorStates(t *testing.T) {
	ready := history.Status{State: history.StateReady, Encrypted: true, KeyProtection: "dpapi-user"}
	for _, test := range []struct {
		name    string
		enabled bool
		store   history.Status
		running bool
		begin   string
		journal capture.HistoryRecordingState
		health  history.RecorderHealth
		want    string
		reason  string
	}{
		{name: "idle", enabled: true, store: ready, want: historyWaiting},
		{name: "capturing between fights", enabled: true, store: ready, running: true, journal: capture.HistoryRecordingState{Enabled: true, Recording: true}, want: historyWaiting},
		{name: "recording a fight", enabled: true, store: ready, running: true, journal: capture.HistoryRecordingState{Enabled: true, Recording: true, InProgress: true}, want: historyRecording},
		{name: "switched on during a fight", enabled: true, store: ready, running: true, journal: capture.HistoryRecordingState{Enabled: true}, want: historyStarting},
		{name: "switched off", enabled: false, store: ready, running: true, want: historyDisabled},
		{name: "storage stopped", enabled: true, store: ready, running: true, journal: capture.HistoryRecordingState{Enabled: true, Recording: true}, health: history.RecorderHealth{Stopped: true, Lost: 9}, want: historyWriteFailed, reason: history.ReasonStorageStopped},
		{name: "key unavailable", enabled: true, store: history.Status{State: history.StateUnavailable, Reason: history.ReasonKeyUnavailable}, want: historyUnavailable, reason: history.ReasonKeyUnavailable},
		{name: "another process records", enabled: false, store: history.Status{State: history.StateReadOnly, Reason: history.ReasonLocked, Encrypted: true}, want: historyReadOnly, reason: history.ReasonLocked},
		{name: "the capture could not begin its history", enabled: true, store: ready, running: true, begin: service.HistoryUnavailable, want: historyNotRecorded, reason: history.ReasonOpenFailed},
		{name: "the replayed file is already stored", enabled: true, store: ready, running: true, begin: service.HistoryDuplicate, want: historyNotRecorded, reason: service.HistoryDuplicate},
		{name: "a begin failure of a stopped capture is over", enabled: true, store: ready, begin: service.HistoryUnavailable, want: historyWaiting},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := historyIndicatorOf(test.enabled, test.store, test.running, test.begin, test.journal, test.health)
			if got.State != test.want || got.Reason != test.reason || got.Enabled != test.enabled {
				t.Fatalf("indicator = %+v, want %s/%s", got, test.want, test.reason)
			}
			if test.running && got.Lost != test.health.Lost {
				t.Fatalf("lost = %d", got.Lost)
			}
		})
	}
}

func TestHistoryRecordingSettingPersistsAndDefaultsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// A file of an earlier build has no field: recording is on.
	if err := os.WriteFile(path, []byte(`{"version":1,"locale":"en"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := loadSettings(path, testLocaleCatalog())
	if store.current().Settings.HistoryRecordingDisabled {
		t.Fatal("an earlier settings file turned recording off")
	}
	if _, err := store.setHistoryRecording(false); err != nil {
		t.Fatal(err)
	}
	reloaded := loadSettings(path, testLocaleCatalog())
	if !reloaded.current().Settings.HistoryRecordingDisabled {
		t.Fatal("switching recording off did not persist")
	}
	var raw map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil || raw["historyRecordingDisabled"] != true {
		t.Fatalf("settings file = %s (%v)", data, err)
	}
	if _, err := reloaded.setHistoryRecording(true); err != nil {
		t.Fatal(err)
	}
	if loadSettings(path, testLocaleCatalog()).current().Settings.HistoryRecordingDisabled {
		t.Fatal("switching recording on did not persist")
	}
}

func TestDeletionAnswersCarryTheRefusal(t *testing.T) {
	raw, err := historyChangeResult(history.DeleteResult{}, history.ErrRecordingActive)
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Refused string `json:"refused"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Refused != "recording_active" {
		t.Fatalf("answer = %s (%v)", raw, err)
	}
}

// TestRecoveryAnswersSayOnlyWhetherTheHistoryIsOpen: a retry or a rebuild answers with the status and whether the history is
// open, and a refused rebuild with its refusal; the indicator offers a rebuild only for an unavailable history the store
// marks rebuildable.
func TestRecoveryAnswersSayOnlyWhetherTheHistoryIsOpen(t *testing.T) {
	if got := recoveryResult(history.Status{State: history.StateReady}, nil); !got.Open || got.Refused != "" {
		t.Fatalf("ready: %+v", got)
	}
	unavailable := history.Status{State: history.StateUnavailable, Reason: history.ReasonKeyVerification, Rebuildable: true}
	if got := recoveryResult(unavailable, errors.New("still closed")); got.Open || got.Refused != "" {
		t.Fatalf("still unavailable: %+v", got)
	}
	if got := recoveryResult(unavailable, history.ErrNotRebuildable); got.Open || got.Refused != "not_rebuildable" {
		t.Fatalf("refused: %+v", got)
	}
	if indicator := historyIndicatorOf(true, unavailable, false, "", capture.HistoryRecordingState{}, history.RecorderHealth{}); !indicator.Rebuildable || indicator.State != historyUnavailable {
		t.Fatalf("indicator = %+v", indicator)
	}
	if indicator := historyIndicatorOf(true, history.Status{State: history.StateReady, Rebuildable: true}, false, "", capture.HistoryRecordingState{}, history.RecorderHealth{}); indicator.Rebuildable {
		t.Fatalf("an open history offered a rebuild: %+v", indicator)
	}
}
