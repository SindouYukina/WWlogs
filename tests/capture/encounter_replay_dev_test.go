//go:build wwdev

package capture

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
//     replay publishes every encounterReplayPublishEvery packets and must equal the first, timelines included,
//   - every published view's effect counts partition the tracked effects exactly (display-eligible = listed + truncated).
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
	SkillCoverage                 skillCoverage      `json:"skillCoverage"`
	// ViewSnapshotBytes is the JSON size of the final view snapshot, the payload the host publishes.
	ViewSnapshotBytes int `json:"viewSnapshotBytes"`
	// ViewSnapshotSkillBytes is the part of ViewSnapshotBytes that the skill split adds.
	ViewSnapshotSkillBytes int `json:"viewSnapshotSkillBytes"`
}

// viewWithoutSkills returns a copy of view with every skill split removed, to measure what the split costs.
func viewWithoutSkills(view ViewSnapshot) ViewSnapshot {
	strip := func(roles []ViewRole) []ViewRole {
		result := append([]ViewRole(nil), roles...)
		for i := range result {
			result[i].Skills, result[i].UnassignedSkill = nil, ViewUnassignedSkill{}
		}
		return result
	}
	stripVehicle := func(vehicle *ViewVehicle) *ViewVehicle {
		if vehicle == nil {
			return nil
		}
		copied := *vehicle
		copied.Skills, copied.UnassignedSkill = nil, ViewUnassignedSkill{}
		return &copied
	}
	view.Roles, view.Vehicle = strip(view.Roles), stripVehicle(view.Vehicle)
	view.Encounters.Recent = append([]ViewEncounter(nil), view.Encounters.Recent...)
	for i := range view.Encounters.Recent {
		view.Encounters.Recent[i].Roles, view.Encounters.Recent[i].Vehicle = strip(view.Encounters.Recent[i].Roles), stripVehicle(view.Encounters.Recent[i].Vehicle)
	}
	if view.Encounters.Current != nil {
		current := *view.Encounters.Current
		current.Roles, current.Vehicle = strip(current.Roles), stripVehicle(current.Vehicle)
		view.Encounters.Current = &current
	}
	return view
}

// skillCoverage reports how much confirmed role and vehicle damage of the capture interval has a skill source, by
// events and by damage, and how much of that has an official Master name. Unknown reasons are kept apart.
type skillCoverage struct {
	Roles            int    `json:"roles"`
	RoleEvents       uint64 `json:"roleEvents"`
	RoleDamage       int64  `json:"roleDamage"`
	SkillEvents      uint64 `json:"skillEvents"`
	SkillDamage      int64  `json:"skillDamage"`
	NamedSkillEvents uint64 `json:"namedSkillEvents"`
	NamedSkillDamage int64  `json:"namedSkillDamage"`
	// DerivedSkill* count rows named through a related record (skill_names.go), never counted as official names.
	DerivedSkillEvents uint64   `json:"derivedSkillEvents"`
	DerivedSkillDamage int64    `json:"derivedSkillDamage"`
	DerivedSkills      []string `json:"derivedSkills"`
	MissingSkillEvents uint64   `json:"missingSkillEvents"`
	MissingSkillDamage int64    `json:"missingSkillDamage"`
	OverflowEvents     uint64   `json:"overflowEvents"`
	DistinctSkills     int      `json:"distinctSkills"`
	UnnamedSkillIDs    []uint64 `json:"unnamedSkillIds"`
	// StillUnnamedSkillIDs have neither an official nor a derived name.
	StillUnnamedSkillIDs []uint64 `json:"stillUnnamedSkillIds"`
	MasterRoleConflicts  []string `json:"masterRoleConflicts"`
	VehicleEvents        uint64   `json:"vehicleEvents"`
	VehicleSkillEvents   uint64   `json:"vehicleSkillEvents"`
}

// collectSkillCoverage reads the interval split. A Master skill-group role that differs from the runtime role is
// listed as a conflict for review; it never changes attribution.
func collectSkillCoverage(statistics DamageStatisticsSnapshot, masters *gameMaster) skillCoverage {
	var coverage skillCoverage
	distinct := map[uint64]bool{}
	unnamed := map[uint64]bool{}
	stillUnnamed := map[uint64]bool{}
	derived := map[string]bool{}
	for _, role := range statistics.Roles {
		if role.OutgoingEvents == 0 {
			continue
		}
		coverage.Roles++
		coverage.RoleEvents += role.OutgoingEvents
		coverage.RoleDamage += role.DamageDealt
		coverage.MissingSkillEvents += role.UnassignedSkill.MissingSkillEvents
		coverage.OverflowEvents += role.UnassignedSkill.OverflowEvents
		if role.UnassignedSkill.OverflowEvents == 0 {
			coverage.MissingSkillDamage += role.UnassignedSkill.DamageDealt
		}
		// A trial role reads its skills through its parent role, as the view's skill projection does.
		staticRole := masters.staticRoleID(role.RoleID)
		for _, skill := range role.Skills {
			distinct[skill.SkillID] = true
			coverage.SkillEvents += skill.OutgoingEvents
			coverage.SkillDamage += skill.DamageDealt
			record, ok := masters.damageSkill(skill.SkillID)
			if ok && record.Name != "" && record.NameStatus == string(resolutionVerified) {
				coverage.NamedSkillEvents += skill.OutgoingEvents
				coverage.NamedSkillDamage += skill.DamageDealt
			} else {
				unnamed[skill.SkillID] = true
				if _, status, source, sourceID := masters.skillRowName(staticRole, skill); status == skillNameStatusDerived {
					coverage.DerivedSkillEvents += skill.OutgoingEvents
					coverage.DerivedSkillDamage += skill.DamageDealt
					derived[fmt.Sprintf("role %d skill %d %s %d", role.RoleID, skill.SkillID, source, sourceID)] = true
				} else {
					stillUnnamed[skill.SkillID] = true
				}
			}
			if ok && record.RoleID != 0 && record.RoleID != staticRole {
				coverage.MasterRoleConflicts = append(coverage.MasterRoleConflicts, fmt.Sprintf("role %d skill %d master role %d", role.RoleID, skill.SkillID, record.RoleID))
			}
		}
	}
	if vehicle := statistics.Vehicle; vehicle != nil {
		coverage.VehicleEvents = vehicle.OutgoingEvents
		for _, skill := range vehicle.Skills {
			coverage.VehicleSkillEvents += skill.OutgoingEvents
		}
	}
	coverage.DistinctSkills = len(distinct)
	for id := range unnamed {
		coverage.UnnamedSkillIDs = append(coverage.UnnamedSkillIDs, id)
	}
	sort.Slice(coverage.UnnamedSkillIDs, func(i, j int) bool { return coverage.UnnamedSkillIDs[i] < coverage.UnnamedSkillIDs[j] })
	for id := range stillUnnamed {
		coverage.StillUnnamedSkillIDs = append(coverage.StillUnnamedSkillIDs, id)
	}
	sort.Slice(coverage.StillUnnamedSkillIDs, func(i, j int) bool { return coverage.StillUnnamedSkillIDs[i] < coverage.StillUnnamedSkillIDs[j] })
	for key := range derived {
		coverage.DerivedSkills = append(coverage.DerivedSkills, key)
	}
	sort.Strings(coverage.DerivedSkills)
	return coverage
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
			// The payload size is a measurement; process-local capture-session numbers inside it may differ in width.
			viewBytes, skillBytes := first.ViewSnapshotBytes, first.ViewSnapshotSkillBytes
			first.ViewSnapshotBytes, second.ViewSnapshotBytes, first.ViewSnapshotSkillBytes, second.ViewSnapshotSkillBytes = 0, 0, 0, 0
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("two replays of the same capture produced different results")
			}
			assertTimelinesConserved(t, first)
			assertEncounterTotalsConserved(t, first.Statistics)
			assertEncountersDoNotOverlap(t, first.Statistics)
			assertRoleTotalsReconcile(t, first.Statistics)
			assertConfirmedEndsAtSignals(t, first)
			first.ViewSnapshotBytes, first.ViewSnapshotSkillBytes = viewBytes, skillBytes
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
		p.dev.encounterOut = &diagnostics
		p.dev.combatEventsJSONL = &rawEvents
		p.damageStatistics.recordEncounterTransitions(true)
	}, func(index int, p *processor) {
		if frame := p.currentFrameTime; frame.After(lastFrame) {
			if active := p.damageStatistics.encounters.current; active != nil && active.state == EncounterActive && !lastFrame.IsZero() {
				maxGap = max(maxGap, frame.Sub(lastFrame))
			}
			lastFrame = frame
		}
		if publishEvery > 0 && index%publishEvery == 0 {
			// Every published view must account for each tracked effect exactly once (see ViewEffectCounts).
			view := p.viewSnapshot()
			if err := view.Effects.Counts.accountingError(len(view.Effects.Active)); err != nil {
				t.Fatalf("effect view counts at packet %d: %v", index, err)
			}
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
	finalView := p.viewSnapshot()
	view, err := json.Marshal(finalView)
	if err != nil {
		t.Fatal(err)
	}
	withoutSkills, err := json.Marshal(viewWithoutSkills(finalView))
	if err != nil {
		t.Fatal(err)
	}
	return encounterReplayResult{SkillCoverage: collectSkillCoverage(statistics, masters), ViewSnapshotBytes: len(view), ViewSnapshotSkillBytes: len(view) - len(withoutSkills), Name: capture.name, Timelines: timelines, MaxFrameGapInEncounterSeconds: maxGap.Seconds(), UDPPackets: stats.udpPackets, Statistics: statistics, Signals: signals, Unassigned: unassigned, Evidence: evidence, VehicleRecords: vehicleRecords, VehicleEvidence: vehicleEvidence, RawFactsSHA256: hex.EncodeToString(factsHash.Sum(nil)), RawFactsCount: factsCount, Pairing: p.rpcPairingSnapshot()}
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
