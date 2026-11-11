package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
	"wwlogs/internal/service"
)

// The Combat History indicator.
//
// One presentation state says what the history is doing, decided here from the facts that decide it: the user's setting,
// the store (open, read-only or unavailable, and why), the running capture's journal (recording a span, inside an encounter,
// waiting for a boundary) and the recorder's storage health. The frontend shows it; it never derives one of its own.

// Indicator states.
const (
	// historyDisabled: the user switched recording off; nothing new is stored.
	historyDisabled = "disabled"
	// historyWaiting: recording is on and no encounter is being recorded right now.
	historyWaiting = "waiting"
	// historyRecording: an encounter is being recorded.
	historyRecording = "recording"
	// historyStarting: recording was switched on during an encounter; it starts once that encounter ends.
	historyStarting = "starting"
	// historyUnavailable: the store could not be opened for recording or reading (Reason says why).
	historyUnavailable = "unavailable"
	// historyReadOnly: stored history can be read but nothing can be recorded (Reason: another process records, newer schema).
	historyReadOnly = "read_only"
	// historyWriteFailed: storage failed repeatedly during this capture and stopped; the capture itself runs on.
	historyWriteFailed = "write_failed"
	// historyNotRecorded: the running capture got no recorder (Reason open_failed: the history could not begin it;
	// duplicate_source: this replayed file is already stored); the capture itself runs on.
	historyNotRecorded = "not_recorded"
)

// historyIndicator is comparable, so the snapshot encoder can tell when it changed.
type historyIndicator struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// MigrationFailure is the conversion failure behind Reason encryption_migration_failed.
	MigrationFailure string `json:"migrationFailure,omitempty"`
	Enabled          bool   `json:"enabled"`
	// Capturing is true while a capture runs; Lost counts the journal entries of that capture that never reached storage,
	// so the encounters they belong to are stored as incomplete.
	Capturing bool   `json:"capturing"`
	Lost      uint64 `json:"lost,omitempty"`
	// Encrypted and KeyProtection describe the open file; PlaintextConverted is set when this run encrypted a plaintext
	// history of an earlier build, and PlaintextBackups counts plaintext copies that could not be converted.
	Encrypted          bool   `json:"encrypted"`
	KeyProtection      string `json:"keyProtection,omitempty"`
	PlaintextConverted bool   `json:"plaintextConverted,omitempty"`
	PlaintextBackups   int    `json:"plaintextBackups,omitempty"`
}

// recorderTracker remembers the recorder of the running capture, so the indicator can read its health.
type recorderTracker struct {
	mu       sync.Mutex
	recorder *history.Recorder
}

func (t *recorderTracker) set(recorder *history.Recorder) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.recorder = recorder
	t.mu.Unlock()
}

func (t *recorderTracker) health() history.RecorderHealth {
	if t == nil {
		return history.RecorderHealth{}
	}
	t.mu.Lock()
	recorder := t.recorder
	t.mu.Unlock()
	return recorder.Health()
}

// historyIndicatorOf decides the indicator. A store problem outranks the setting, which outranks the capture's state. begin is
// how the running capture's history began (service.Status.History).
func historyIndicatorOf(enabled bool, store history.Status, running bool, begin string, journal capture.HistoryRecordingState, health history.RecorderHealth) historyIndicator {
	indicator := historyIndicator{
		Enabled: enabled, Capturing: running, Encrypted: store.Encrypted, KeyProtection: store.KeyProtection,
		PlaintextConverted: store.PlaintextMigration == history.PlaintextConverted, PlaintextBackups: store.PlaintextBackups,
	}
	if running {
		indicator.Lost = health.Lost
	}
	switch {
	case store.State == history.StateUnavailable:
		indicator.State, indicator.Reason, indicator.MigrationFailure = historyUnavailable, store.Reason, store.MigrationFailure
	case store.State == history.StateReadOnly:
		indicator.State, indicator.Reason = historyReadOnly, store.Reason
	case !enabled:
		indicator.State = historyDisabled
	case running && begin == service.HistoryUnavailable:
		indicator.State, indicator.Reason = historyNotRecorded, history.ReasonOpenFailed
	case running && begin == service.HistoryDuplicate:
		indicator.State, indicator.Reason = historyNotRecorded, service.HistoryDuplicate
	case running && health.Stopped:
		indicator.State, indicator.Reason = historyWriteFailed, history.ReasonStorageStopped
	case running && journal.Recording && journal.InProgress:
		indicator.State = historyRecording
	case running && journal.Enabled && !journal.Recording:
		indicator.State = historyStarting
	default:
		indicator.State = historyWaiting
	}
	return indicator
}

func (b *Bridge) historyIndicator() historyIndicator {
	status := b.capture.Status()
	running := status.State == service.StateRunning
	var journal capture.HistoryRecordingState
	var health history.RecorderHealth
	if running {
		journal = b.capture.HistoryRecording()
		health = b.recorders.health()
	}
	return historyIndicatorOf(!b.settings.current().Settings.HistoryRecordingDisabled, b.history.Status(), running, status.History, journal, health)
}

// SetHistoryRecording saves whether Combat History records and applies it to the running capture: off stops recording at
// once and leaves the encounter in progress unfinished; on starts at the next clean encounter boundary.
func (b *Bridge) SetHistoryRecording(enabled bool) (SettingsState, error) {
	state, err := b.settings.setHistoryRecording(enabled)
	if err != nil {
		return state, err
	}
	b.capture.SetHistoryRecording(enabled)
	b.shell.fire()
	return state, nil
}

// historyChangeLimit bounds one deletion, including a compaction of the file.
const historyChangeLimit = 2 * time.Minute

// DeleteHistoryEncounter deletes one stored encounter the user confirmed in the UI.
func (b *Bridge) DeleteHistoryEncounter(id int64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), historyChangeLimit)
	defer cancel()
	result, err := b.history.DeleteEncounter(ctx, id)
	return historyChangeResult(result, err)
}

// ClearHistory deletes every stored encounter after the user confirmed it in the UI.
func (b *Bridge) ClearHistory() (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), historyChangeLimit)
	defer cancel()
	result, err := b.history.ClearHistory(ctx)
	return historyChangeResult(result, err)
}

// historyChangeResult answers a deletion with what it removed, or with the stable reason it was refused.
func historyChangeResult(result history.DeleteResult, err error) (json.RawMessage, error) {
	refused := ""
	switch {
	case err == nil:
	case errors.Is(err, history.ErrEncounterRecording):
		refused = "encounter_recording"
	case errors.Is(err, history.ErrRecordingActive):
		refused = "recording_active"
	case errors.Is(err, history.ErrNotWritable):
		refused = "not_writable"
	case errors.Is(err, history.ErrNotFound):
		refused = "not_found"
	default:
		return nil, err
	}
	return json.Marshal(struct {
		history.DeleteResult
		Refused string `json:"refused,omitempty"`
	}{result, refused})
}
