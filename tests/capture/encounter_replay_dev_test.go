//go:build wwdev

package capture

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This file is the development-profile, test-only PCAP replay for the encounter lifecycle. It replays archived captures through the
// same processor and datagram entry point as the executables, twice per capture, and checks that:
//   - both replays produce the same encounter segmentation and statistics (replay idempotency),
//   - the encounter partition (completed + open + outside + evicted) adds up to the interval totals,
//   - encounters of one protocol session never share a time span,
//   - every confirmed encounter ends exactly at an accepted battle-state exit signal,
//   - every encounter's DPS timeline adds up to its confirmed local damage, per role, vehicle, and unassigned
//     contributor, and tiles exactly the span its Encounter DPS divides by,
//   - publishing view snapshots while replaying (as the host does at any replay speed) changes nothing: the second
//     replay publishes every encounterReplayPublishEvery packets and must equal the first, timelines included.
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
	Name            string                      `json:"name"`
	UDPPackets      int                         `json:"udpPackets"`
	Statistics      DamageStatisticsSnapshot    `json:"statistics"`
	Signals         []encounterReplaySignal     `json:"battleStateSignals"`
	Unassigned      []combatEventRecordLine     `json:"unassignedRoleEvents"`
	Evidence        []combatEventJSONL          `json:"unassignedRoleEvidence"`
	VehicleRecords  []combatEventRecordLine     `json:"vehicleEvents"`
	VehicleEvidence []combatEventJSONL          `json:"vehicleEvidence"`
	Timelines       []EncounterTimelineSnapshot `json:"timelines"`
	// MaxFrameGapInEncounterSeconds is the longest wait between decoded frames while an encounter was active; the
	// timeline and live Encounter DPS advance with frame times, so this is how long either could stand still.
	MaxFrameGapInEncounterSeconds float64            `json:"maxFrameGapInEncounterSeconds"`
	RawFactsSHA256                string             `json:"rawFactsSha256"`
	RawFactsCount                 int                `json:"rawFactsCount"`
	Pairing                       RPCPairingSnapshot `json:"pairing"`
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
			first := encounterReplayOnce(t, inputs, masters, key, capture, 0)
			second := encounterReplayOnce(t, inputs, masters, key, capture, encounterReplayPublishEvery)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("two replays of the same capture produced different results")
			}
			assertTimelinesConserved(t, first)
			assertEncounterTotalsConserved(t, first.Statistics)
			assertEncountersDoNotOverlap(t, first.Statistics)
			assertRoleTotalsReconcile(t, first.Statistics)
			assertConfirmedEndsAtSignals(t, first)
			data := equivalenceMarshal(t, first)
			if archive := os.Getenv("WWLOGS_ATTRIBUTION_INVENTORY_OUT"); archive != "" {
				if err := os.MkdirAll(archive, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(archive, capture.name+".json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(out, capture.name+".encounters.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// encounterReplayPublishEvery is the packet pace at which the second replay builds view snapshots.
const encounterReplayPublishEvery = 37

func encounterReplayOnce(t *testing.T, inputs equivalenceInputs, masters *gameMaster, key *staticRSA, capture equivalencePCAP, publishEvery int) encounterReplayResult {
	t.Helper()
	var diagnostics, rawEvents bytes.Buffer
	var lastFrame time.Time
	var maxGap time.Duration
	p, stats := equivalenceReplay(t, inputs, masters, key, capture.path, func(p *processor) {
		p.encounterOut = &diagnostics
		p.combatEventsJSONL = &rawEvents
		p.damageStatistics.recordEncounterTransitions(true)
	}, func(index int, p *processor) {
		if frame := p.currentFrameTime; frame.After(lastFrame) {
			if active := p.damageStatistics.encounters.current; active != nil && active.state == EncounterActive && !lastFrame.IsZero() {
				maxGap = max(maxGap, frame.Sub(lastFrame))
			}
			lastFrame = frame
		}
		if publishEvery > 0 && index%publishEvery == 0 {
			_ = p.viewSnapshot()
		}
	})
	p.finishCapture()
	var signals []encounterReplaySignal
	var unassigned, vehicleRecords []combatEventRecordLine
	vehicleKeys := map[CombatEventKey]bool{}
	keys := map[CombatEventKey]bool{}
	for _, line := range bytes.Split(diagnostics.Bytes(), []byte{10}) {
		var row combatEventRecordLine
		if json.Unmarshal(line, &row) == nil && row.Type == "COMBAT_EVENT_RECORD" && row.Kind == CombatEventDamage && row.Flow == CombatFlowLocalOutgoing && row.VehicleKind == vehicleKindMotorcycle {
			vehicleRecords = append(vehicleRecords, row)
			vehicleKeys[row.Key] = true
		}
		if json.Unmarshal(line, &row) == nil && row.Type == "COMBAT_EVENT_RECORD" && row.Kind == CombatEventDamage && row.Flow == CombatFlowLocalOutgoing && row.SourceRole == nil && row.VehicleKind != vehicleKindMotorcycle {
			unassigned = append(unassigned, row)
			keys[row.Key] = true
		}
	}
	var evidence, vehicleEvidence []combatEventJSONL
	factsHash := sha256.New()
	factsCount := 0
	for _, line := range bytes.Split(rawEvents.Bytes(), []byte{10}) {
		var row combatEventJSONL
		if json.Unmarshal(line, &row) == nil {
			if vehicleKeys[CombatEventKey{ProtocolSession: row.SessionNumber, RPCID: row.RPCID}] {
				vehicleEvidence = append(vehicleEvidence, row)
			}
			if row.Raw.ServerResultRaw != nil {
				fact := struct {
					EventType     string              `json:"eventType"`
					Timestamp     time.Time           `json:"timestamp"`
					SessionNumber int                 `json:"sessionNumber"`
					RPCID         uint64              `json:"rpcId"`
					Raw           combatEventRawJSONL `json:"raw"`
				}{row.EventType, row.Timestamp.UTC().Truncate(time.Microsecond), row.SessionNumber, row.RPCID, row.Raw}
				_ = json.NewEncoder(factsHash).Encode(fact)
				factsCount++
			}
		}
		if json.Unmarshal(line, &row) == nil && keys[CombatEventKey{ProtocolSession: row.SessionNumber, RPCID: row.RPCID}] {
			evidence = append(evidence, row)
		}
	}
	for _, line := range bytes.Split(diagnostics.Bytes(), []byte{'\n'}) {
		var row battleStateSignalLine
		if len(line) == 0 || json.Unmarshal(line, &row) != nil || row.Type != "BATTLE_STATE_SIGNAL" {
			continue
		}
		signals = append(signals, encounterReplaySignal{InBattle: row.InBattle, At: row.ObservedAt.Format(encounterReplayTimeLayout), Accepted: row.Accepted})
	}
	normalizer := newEquivalenceNormalizer(p)
	for i := range unassigned {
		if _, rest, found := strings.Cut(unassigned[i].Assignment, "-"); found {
			unassigned[i].Assignment = "1-" + rest
		}
	}
	for i := range vehicleRecords {
		if _, rest, found := strings.Cut(vehicleRecords[i].Assignment, "-"); found {
			vehicleRecords[i].Assignment = "1-" + rest
		}
	}
	statistics := normalizer.damage(p.damageStatisticsSnapshot())
	normalizeEncounterIDs(&statistics, normalizer.base)
	timelines := p.damageStatistics.encounterTimelines()
	for i := range timelines {
		if _, rest, found := strings.Cut(timelines[i].EncounterID, "-"); found {
			timelines[i].EncounterID = "1-" + rest
		}
	}
	return encounterReplayResult{Name: capture.name, Timelines: timelines, MaxFrameGapInEncounterSeconds: maxGap.Seconds(), UDPPackets: stats.udpPackets, Statistics: statistics, Signals: signals, Unassigned: unassigned, Evidence: evidence, VehicleRecords: vehicleRecords, VehicleEvidence: vehicleEvidence, RawFactsSHA256: hex.EncodeToString(factsHash.Sum(nil)), RawFactsCount: factsCount, Pairing: p.rpcPairingSnapshot()}
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

// assertTimelinesConserved checks every retained encounter's timeline against the encounter it describes.
func assertTimelinesConserved(t *testing.T, result encounterReplayResult) {
	t.Helper()
	encounters := append([]EncounterSnapshot{}, result.Statistics.Encounters.Completed...)
	if result.Statistics.Encounters.Current != nil {
		encounters = append(encounters, *result.Statistics.Encounters.Current)
	}
	if len(encounters) != len(result.Timelines) {
		t.Fatalf("%d encounters but %d timelines", len(encounters), len(result.Timelines))
	}
	for i, encounter := range encounters {
		if err := timelineConservationError(result.Timelines[i], encounter); err != nil {
			t.Errorf("encounter %s: %v", encounter.EncounterID, err)
		}
	}
	var points, zero int
	for _, timeline := range result.Timelines {
		points += len(timeline.Points)
		for _, point := range timeline.Points {
			if point.Damage == 0 {
				zero++
			}
		}
	}
	t.Logf("timeline: %d encounters, %d points, %d zero-damage points, max frame gap in an active encounter %.3f s", len(result.Timelines), points, zero, result.MaxFrameGapInEncounterSeconds)
}
