package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
	"wwlogs/internal/service"
)

func TestReplayNeverEntersTheOwnHistory(t *testing.T) {
	store, err := history.Open(history.Options{Path: filepath.Join(t.TempDir(), "combat-history.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	replay := filepath.Join(t.TempDir(), "capture.pcap")
	if err := os.WriteFile(replay, []byte("not a real capture"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The default history records live captures only.
	own := historyStarter(store, false, nil)
	if recorder, state, err := own(capture.SessionConfig{ReplayPath: replay}); recorder != nil || state != "" || err != nil {
		t.Fatalf("a replay was recorded into the default history: %v %q %v", recorder, state, err)
	}
	live, state, err := own(capture.SessionConfig{LocalIPv4: "192.0.2.1"})
	if err != nil || live == nil || state != "" {
		t.Fatalf("a live capture was not recorded: %q %v", state, err)
	}
	if err := live.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	// An explicit test history records a replay once.
	test := historyStarter(store, true, nil)
	first, _, err := test(capture.SessionConfig{ReplayPath: replay})
	if err != nil || first == nil {
		t.Fatalf("an explicit test history did not record the replay: %v", err)
	}
	if err := first.Finish(context.Background(), capture.StopReasonReplayEnd); err != nil {
		t.Fatal(err)
	}
	again, state, err := test(capture.SessionConfig{ReplayPath: replay})
	if again != nil || state != service.HistoryDuplicate || !errors.Is(err, history.ErrDuplicateSource) {
		t.Fatalf("the same file was recorded twice: %v %q %v", again, state, err)
	}
}
