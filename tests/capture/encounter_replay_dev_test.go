//go:build wwdev

package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This file is the development-profile, test-only PCAP replay for the encounter lifecycle. It replays archived captures through the
// same processor and datagram entry point as the executables, twice per capture, and checks that:
//   - both replays produce the same encounter segmentation and statistics (replay idempotency),
//   - the encounter partition (completed + open + outside + evicted) adds up to the interval totals,
//   - encounters of one protocol session never share a time span,
//   - every confirmed encounter ends exactly at an accepted battle-state exit signal.
//
// It writes one canonical JSON file per capture for review. It is skipped unless WWLOGS_ENCOUNTER_REPLAY_OUT is
// set, reads only paths supplied through the environment, and needs the embedded key payload in the test binary
// (scripts/embed-key.go -profile development -test-binary). Battle-state signals are read back from the
// development encounter diagnostic stream, so the product needs no test hook.
//
// Environment:
//
//	WWLOGS_ENCOUNTER_REPLAY_OUT        output directory (required to run)
//	WWLOGS_ENCOUNTER_REPLAY_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_ENCOUNTER_REPLAY_MASTER_DB  Master SQLite database

type encounterReplayResult struct {
	Name       string                   `json:"name"`
	UDPPackets int                      `json:"udpPackets"`
	Statistics DamageStatisticsSnapshot `json:"statistics"`
	Signals    []encounterReplaySignal  `json:"battleStateSignals"`
}

const encounterReplayTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

type encounterReplaySignal struct {
	InBattle bool   `json:"inBattle"`
	At       string `json:"at"`
	Accepted bool   `json:"accepted"`
}

func TestEncounterReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_ENCOUNTER_REPLAY_OUT")
	if out == "" {
		t.Skip("WWLOGS_ENCOUNTER_REPLAY_OUT is not set")
	}
	master := os.Getenv("WWLOGS_ENCOUNTER_REPLAY_MASTER_DB")
	listPath := os.Getenv("WWLOGS_ENCOUNTER_REPLAY_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_ENCOUNTER_REPLAY_MASTER_DB and WWLOGS_ENCOUNTER_REPLAY_PCAPS are required")
	}
	var pcaps []equivalencePCAP
	file, err := os.Open(listPath)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		pcaps = append(pcaps, equivalencePCAP{name: name, path: path})
	}
	file.Close()
	if len(pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := loadGameMasterDatabase(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := equivalenceInputs{localIP: "192.168.1.2"}
	for _, capture := range pcaps {
		t.Run(capture.name, func(t *testing.T) {
			first := encounterReplayOnce(t, inputs, masters, key, capture)
			second := encounterReplayOnce(t, inputs, masters, key, capture)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("two replays of the same capture produced different results")
			}
			assertEncounterTotalsConserved(t, first.Statistics)
			assertEncountersDoNotOverlap(t, first.Statistics)
			assertRoleTotalsReconcile(t, first.Statistics)
			assertConfirmedEndsAtSignals(t, first)
			data := equivalenceMarshal(t, first)
			if err := os.WriteFile(filepath.Join(out, capture.name+".encounters.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func encounterReplayOnce(t *testing.T, inputs equivalenceInputs, masters *gameMaster, key *staticRSA, capture equivalencePCAP) encounterReplayResult {
	t.Helper()
	var diagnostics bytes.Buffer
	p, stats := equivalenceReplay(t, inputs, masters, key, capture.path, func(p *processor) {
		p.encounterOut = &diagnostics
		p.damageStatistics.recordEncounterTransitions(true)
	}, nil)
	p.finishCapture()
	var signals []encounterReplaySignal
	for _, line := range bytes.Split(diagnostics.Bytes(), []byte{'\n'}) {
		var row battleStateSignalLine
		if len(line) == 0 || json.Unmarshal(line, &row) != nil || row.Type != "BATTLE_STATE_SIGNAL" {
			continue
		}
		signals = append(signals, encounterReplaySignal{InBattle: row.InBattle, At: row.ObservedAt.Format(encounterReplayTimeLayout), Accepted: row.Accepted})
	}
	normalizer := newEquivalenceNormalizer(p)
	statistics := normalizer.damage(p.damageStatisticsSnapshot())
	normalizeEncounterIDs(&statistics, normalizer.base)
	return encounterReplayResult{Name: capture.name, UDPPackets: stats.udpPackets, Statistics: statistics, Signals: signals}
}

// normalizeEncounterIDs removes the process-local capture session sequence from encounter identities.
func normalizeEncounterIDs(statistics *DamageStatisticsSnapshot, base uint64) {
	fix := func(e *EncounterSnapshot) {
		e.CaptureSessionID -= base
		if _, rest, found := strings.Cut(e.EncounterID, "-"); found {
			e.EncounterID = "1-" + rest
		}
	}
	for i := range statistics.Encounters.Completed {
		fix(&statistics.Encounters.Completed[i])
	}
	if statistics.Encounters.Current != nil {
		fix(statistics.Encounters.Current)
	}
}

func assertConfirmedEndsAtSignals(t *testing.T, result encounterReplayResult) {
	t.Helper()
	exits := map[string]bool{}
	for _, signal := range result.Signals {
		if signal.Accepted && !signal.InBattle {
			exits[signal.At] = true
		}
	}
	for _, encounter := range result.Statistics.Encounters.Completed {
		if encounter.Outcome != EncounterOutcomeConfirmed {
			continue
		}
		end := encounter.EndedAt.Format(encounterReplayTimeLayout)
		if encounter.EndBasis != encounterEndBattleState || !exits[end] || !encounter.StartConfirmed {
			t.Errorf("confirmed encounter %s does not end at an accepted exit signal: end=%s basis=%s", encounter.EncounterID, end, encounter.EndBasis)
		}
	}
}
