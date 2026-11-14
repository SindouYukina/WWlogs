package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestHistoryRecordingSettingReachesEverySession(t *testing.T) {
	var configs []capture.SessionConfig
	session := &fakeSession{}
	service := NewCaptureService(func(cfg capture.SessionConfig) (Session, error) {
		configs = append(configs, cfg)
		return session, nil
	})
	service.SetHistory(func(capture.SessionConfig) (HistoryRecorder, string, error) { return &fakeRecorder{}, "", nil })
	// Off before a capture: the session starts paused.
	service.SetHistoryRecording(false)
	if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if !configs[0].HistoryPaused {
		t.Fatal("a capture started recording although recording is off")
	}
	// On during the capture: the running session follows at once.
	service.SetHistoryRecording(true)
	if state := service.HistoryRecording(); !state.Enabled {
		t.Fatalf("running session state = %+v", state)
	}
	session.intervalMu.Lock()
	calls := append([]bool(nil), session.recording...)
	session.intervalMu.Unlock()
	if !reflect.DeepEqual(calls, []bool{true}) {
		t.Fatalf("the running session saw %v", calls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if state := service.HistoryRecording(); state != (capture.HistoryRecordingState{}) {
		t.Fatalf("a stopped service reports %+v", state)
	}
	// The next capture starts with the setting.
	if err := service.Start(capture.SessionConfig{LocalIPv4: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if configs[1].HistoryPaused {
		t.Fatal("the next capture ignored the setting")
	}
	if err := service.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
