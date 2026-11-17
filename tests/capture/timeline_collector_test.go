package capture

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStateTimelineCollector is a test-only end-to-end check on a real capture. It replays one PCAP with
// the executables' offline loop and, once per elapsed interval of capture time, writes the damage
// statistics (with rolling DPS) and the effect snapshot that a live consumer would read at that frame.
// It is skipped unless WWLOGS_TIMELINE_OUT is set, adds no flag or export to any executable, and reads
// only paths supplied through the environment. Like the equivalence collector it needs the embedded key
// payload (scripts/embed-key.go -test-binary).
//
// Environment:
//
//	WWLOGS_TIMELINE_OUT        output JSONL path (required to run)
//	WWLOGS_TIMELINE_PCAP       PCAP to replay
//	WWLOGS_TIMELINE_MASTER_DB  Master SQLite database
//	WWLOGS_TIMELINE_INTERVAL   seconds of capture time between records (default 1)
//	WWLOGS_TIMELINE_HOLDERS    optional comma-separated holder entity IDs; other effects are left out
//	WWLOGS_TIMELINE_LOCAL_IP   local IPv4 used to decide packet direction (default 192.168.1.2)
func TestStateTimelineCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_TIMELINE_OUT")
	if out == "" {
		t.Skip("WWLOGS_TIMELINE_OUT is not set")
	}
	inputs := equivalenceInputs{localIP: os.Getenv("WWLOGS_TIMELINE_LOCAL_IP")}
	if inputs.localIP == "" {
		inputs.localIP = "192.168.1.2"
	}
	path, masterPath := os.Getenv("WWLOGS_TIMELINE_PCAP"), os.Getenv("WWLOGS_TIMELINE_MASTER_DB")
	if path == "" || masterPath == "" {
		t.Fatal("WWLOGS_TIMELINE_PCAP and WWLOGS_TIMELINE_MASTER_DB are required")
	}
	interval := time.Second
	if raw := os.Getenv("WWLOGS_TIMELINE_INTERVAL"); raw != "" {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || seconds <= 0 {
			t.Fatalf("invalid WWLOGS_TIMELINE_INTERVAL %q", raw)
		}
		interval = time.Duration(seconds * float64(time.Second))
	}
	holders := map[uint64]bool{}
	for _, field := range strings.Split(os.Getenv("WWLOGS_TIMELINE_HOLDERS"), ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		id, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			t.Fatalf("invalid holder %q", field)
		}
		holders[id] = true
	}
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is unavailable (build the test binary with scripts/embed-key.go -test-binary): %v", err)
	}
	masters, err := LoadGameMasterDatabase(masterPath, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	var next time.Time
	write := func(p *Processor) {
		damage := p.DamageStatisticsSnapshot()
		damage = p.DamageStatisticsSnapshotAt(CaptureQueryTime{CaptureSessionID: damage.CaptureSessionID, Timestamp: p.CurrentFrameTime})
		effects := p.EffectSnapshotAtLastFrame()
		if len(holders) > 0 {
			kept := effects.Effects[:0]
			for _, effect := range effects.Effects {
				if holders[effect.HolderEntityID] {
					kept = append(kept, effect)
				}
			}
			effects.Effects = kept
			effects.RecentEnded = nil
		}
		if err := encoder.Encode(equivalenceState{Damage: damage, Effects: effects}); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := equivalenceReplay(t, inputs, masters, key, path, nil, func(_ int, p *Processor) {
		if p.CurrentFrameTime.IsZero() || p.SessionNumber == 0 {
			return
		}
		if next.IsZero() {
			next = p.CurrentFrameTime.Truncate(interval)
		}
		if p.CurrentFrameTime.Before(next) {
			return
		}
		write(p)
		next = p.CurrentFrameTime.Truncate(interval).Add(interval)
	})
	if !p.CurrentFrameTime.IsZero() {
		write(p)
	}
}
