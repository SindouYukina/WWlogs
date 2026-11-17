//go:build wwdev

package capture

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

// TestEffectConsoleCollector replays one PCAP twice and writes the BuffEvent console lines that a live
// capture would print, once with the default combat policy and once with -show-all-effect-events. It is a
// test-only measurement of console volume on a real capture, skipped unless WWLOGS_CONSOLE_OUT is set; it
// adds no flag or export to any executable. Like the other collectors it needs the embedded key payload.
//
// Environment:
//
//	WWLOGS_CONSOLE_OUT        output directory (required to run); writes combat.log and all.log
//	WWLOGS_CONSOLE_PCAP       PCAP to replay
//	WWLOGS_CONSOLE_MASTER_DB  Master SQLite database
func TestEffectConsoleCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_CONSOLE_OUT")
	if out == "" {
		t.Skip("WWLOGS_CONSOLE_OUT is not set")
	}
	path, masterPath := os.Getenv("WWLOGS_CONSOLE_PCAP"), os.Getenv("WWLOGS_CONSOLE_MASTER_DB")
	if path == "" || masterPath == "" {
		t.Fatal("WWLOGS_CONSOLE_PCAP and WWLOGS_CONSOLE_MASTER_DB are required")
	}
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is unavailable (build the test binary with scripts/embed-key.go -test-binary): %v", err)
	}
	masters, err := LoadGameMasterDatabase(masterPath, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct {
		name string
		all  bool
	}{{"combat.log", false}, {"all.log", true}} {
		file, err := os.Create(filepath.Join(out, mode.name))
		if err != nil {
			t.Fatal(err)
		}
		writer := bufio.NewWriter(file)
		equivalenceReplay(t, equivalenceInputs{localIP: "192.168.1.2"}, masters, key, path, func(p *Processor) {
			p.Dev.stdout, p.Dev.showEffectEvents, p.Dev.showAllEffectEvents = writer, true, mode.all
		}, nil)
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
