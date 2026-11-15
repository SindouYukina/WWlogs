package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"wwlogs/internal/assets"
	"wwlogs/internal/capture"
	"wwlogs/internal/history"
	"wwlogs/internal/service"
)

func TestEffectRowsCarryAnIconOnlyForTheirOwnBuffID(t *testing.T) {
	bridge := &Bridge{images: assets.NewResolver(fstest.MapFS{"game/buffs/1311400208/icon.webp": {Data: []byte("icon")}})}
	id := func(value int64) *int64 { return &value }
	for _, test := range []struct {
		effect *int64
		want   bool
	}{{id(1311400208), true}, {id(1311400209), false}, {id(0), false}, {id(-5), false}, {nil, false}} {
		if got := bridge.hasBuffIcon(test.effect); got != test.want {
			t.Fatalf("effect %v: icon %v, want %v", test.effect, got, test.want)
		}
	}
	// Without an image source, or with a pack that cannot answer, no effect has an icon.
	if (&Bridge{}).hasBuffIcon(id(1311400208)) || (&Bridge{images: assets.NewResolver(assets.NewPackFS("damaged"))}).hasBuffIcon(id(1311400208)) {
		t.Fatal("an icon was reported without an image")
	}
}

func TestReplayNeverEntersTheOwnHistory(t *testing.T) {
	store, err := history.Open(history.Options{Path: filepath.Join(t.TempDir(), "combat-history.db"), KeyInputs: testHistoryKeyInputs(t)})
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

// testHistoryKeyInputs are synthetic key inputs: a made-up motherboard UUID and an RSA key generated for the test. No desktop
// test derives a history key from this computer or from the protocol key a build embeds.
func testHistoryKeyInputs(t *testing.T) history.KeyInputs {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	uuid := []byte("WWLGDESKTOPTEST1")
	return history.KeyInputs{
		Machine:     func() (history.Machine, error) { return history.Machine{SystemUUID: bytes.Clone(uuid)}, nil },
		ProtocolKey: func() ([]byte, error) { return bytes.Clone(der), nil },
	}
}
