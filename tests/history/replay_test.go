package history

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// TestHistoryReplayCollector replays every listed capture through the desktop host's session path (capture.OpenSession and
// Run, as CaptureService runs them) into one Combat History file, and checks that history repeats the engine exactly:
//
//   - keeping history changes nothing: the final view of every capture is identical with and without it;
//   - every encounter the engine finished is stored once, as the engine finished it (ApplyEncounterIntegrity of the journaled
//     result with its last journaled integrity), and the live view of it equals the history projection of it;
//   - every accepted combat result is stored once; from the stored results alone, each finished encounter's totals, contributor
//     split, skill split and healing are recomputed and equal the stored engine results;
//   - the stored timeline draws exactly the live timeline; the heatmap counts are the listed encounters by local date;
//   - every effect lifecycle change the effect tracker journaled is stored once, in the tracker's order, with its instance;
//     a removal is always an observed message and an end of tracking is never stored as a removal;
//   - after reopening the file every answer is the same, and replaying the same file again stores nothing.
//
// It needs the embedded key, so it runs in a test binary built by scripts/embed-key.go (-test-binary ./internal/history), in
// either build profile. WWLOGS_HISTORY_REPLAY_PCAPS lists "name|path" lines; WWLOGS_HISTORY_REPLAY_MASTER_DB is the Master;
// WWLOGS_HISTORY_REPLAY_OUT receives one summary per capture and the history file itself.
func TestHistoryReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_HISTORY_REPLAY_OUT")
	if out == "" {
		t.Skip("WWLOGS_HISTORY_REPLAY_OUT is not set")
	}
	listPath, masterPath := os.Getenv("WWLOGS_HISTORY_REPLAY_PCAPS"), os.Getenv("WWLOGS_HISTORY_REPLAY_MASTER_DB")
	if listPath == "" || masterPath == "" {
		t.Fatal("WWLOGS_HISTORY_REPLAY_PCAPS and WWLOGS_HISTORY_REPLAY_MASTER_DB are required")
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	masters, err := capture.LoadMasterData(masterPath, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	defer masters.Close()
	historyPath := filepath.Join(out, "combat-history.db")
	_ = os.Remove(historyPath)
	store, err := Open(Options{Path: historyPath, MasterSHA256: masters.SHA256()})
	if err != nil {
		t.Fatal(err)
	}
	captures := readCaptureList(t, listPath)
	totals := replayTotals{}
	for _, item := range captures {
		t.Run(item.name, func(t *testing.T) {
			summary := replayIntoHistory(t, store, masters, item)
			totals.add(summary)
			writeJSON(t, filepath.Join(out, item.name+".json"), summary)
		})
	}
	checkHeatmap(t, store)
	snapshotBefore := historyDigest(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(Options{Path: historyPath, MasterSHA256: masters.SHA256()})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status := reopened.Status(); status.State != StateReady || status.RecoveredSessions != 0 || status.RecoveredEncounters != 0 {
		t.Fatalf("reopened status = %+v", status)
	}
	if after := historyDigest(t, reopened); after != snapshotBefore {
		t.Fatal("the history reads differently after reopening the file")
	}
	for _, item := range captures {
		digest, err := fileDigestHex(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.BeginSession(SessionStart{Source: SourceReplay, SourceSHA256: digest, Level: Level()}); !errors.Is(err, ErrDuplicateSource) {
			t.Fatalf("%s: a second replay of the same file was accepted: %v", item.name, err)
		}
	}
	assertIntegrity(t, reopened)
	totals.Captures = len(captures)
	writeJSON(t, filepath.Join(out, "summary.json"), totals)
	t.Logf("history replay: %d captures (%d byte-identical copies not stored again), %d encounters (%d finished, %d discarded by the engine), %d accepted results stored, %d recomputed, %d effect changes of %d instances %v",
		totals.Captures, totals.Duplicates, totals.ListedEncounters, totals.FinishedEncounters, totals.Discarded, totals.StoredEvents, totals.RecomputedEncounters,
		totals.EffectChanges, totals.EffectInstances, totals.EffectKinds)
}

type captureItem struct{ name, path string }

func readCaptureList(t *testing.T, path string) []captureItem {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var items []captureItem
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" {
			continue
		}
		name, capturePath, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("malformed capture list line %q", line)
		}
		items = append(items, captureItem{name: name, path: capturePath})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return items
}

// teeSink stores the journal through the recorder and keeps a copy for comparison.
type teeSink struct {
	recorder *Recorder
	entries  []capture.HistoryEntry
	dropped  uint64
}

func (s *teeSink) Level() capture.HistoryLevel { return s.recorder.Level() }

func (s *teeSink) Submit(entries []capture.HistoryEntry, dropped uint64) {
	s.entries = append(s.entries, entries...)
	s.dropped += dropped
	s.recorder.Submit(entries, dropped)
}

type replaySummary struct {
	Name string `json:"name"`
	// DuplicateOf marks a byte-identical copy of a capture already stored; it was not stored again.
	DuplicateOf          bool           `json:"duplicateOf,omitempty"`
	Intervals            int            `json:"intervals"`
	FinishedEncounters   int            `json:"finishedEncounters"`
	Discarded            int            `json:"discarded"`
	ListedEncounters     int            `json:"listedEncounters"`
	Outcomes             map[string]int `json:"outcomes"`
	Integrity            map[string]int `json:"integrity"`
	AcceptedResults      int            `json:"acceptedResults"`
	StoredEvents         int            `json:"storedEvents"`
	LiveViewsCompared    int            `json:"liveViewsCompared"`
	RecomputedEncounters int            `json:"recomputedEncounters"`
	TimelinesCompared    int            `json:"timelinesCompared"`
	ScenesKnown          int            `json:"scenesKnown"`
	EnemyInstances       int            `json:"enemyInstances"`
	EnemiesWithMonster   int            `json:"enemiesWithMonster"`
	DamageDealt          int64          `json:"damageDealt"`
	EffectChanges        int            `json:"effectChanges"`
	EffectInstances      int            `json:"effectInstances"`
	EffectKinds          map[string]int `json:"effectKinds"`
}

type replayTotals struct {
	Captures             int            `json:"captures"`
	Duplicates           int            `json:"duplicates"`
	FinishedEncounters   int            `json:"finishedEncounters"`
	Discarded            int            `json:"discarded"`
	ListedEncounters     int            `json:"listedEncounters"`
	StoredEvents         int            `json:"storedEvents"`
	LiveViewsCompared    int            `json:"liveViewsCompared"`
	RecomputedEncounters int            `json:"recomputedEncounters"`
	TimelinesCompared    int            `json:"timelinesCompared"`
	ScenesKnown          int            `json:"scenesKnown"`
	EnemyInstances       int            `json:"enemyInstances"`
	EnemiesWithMonster   int            `json:"enemiesWithMonster"`
	DamageDealt          int64          `json:"damageDealt"`
	EffectChanges        int            `json:"effectChanges"`
	EffectInstances      int            `json:"effectInstances"`
	EffectKinds          map[string]int `json:"effectKinds"`
}

func (t *replayTotals) add(s replaySummary) {
	if t.EffectKinds == nil {
		t.EffectKinds = map[string]int{}
	}
	for kind, count := range s.EffectKinds {
		t.EffectKinds[kind] += count
	}
	t.EffectChanges += s.EffectChanges
	t.EffectInstances += s.EffectInstances
	if s.DuplicateOf {
		t.Duplicates++
	}
	t.FinishedEncounters += s.FinishedEncounters
	t.Discarded += s.Discarded
	t.ListedEncounters += s.ListedEncounters
	t.StoredEvents += s.StoredEvents
	t.LiveViewsCompared += s.LiveViewsCompared
	t.RecomputedEncounters += s.RecomputedEncounters
	t.TimelinesCompared += s.TimelinesCompared
	t.ScenesKnown += s.ScenesKnown
	t.EnemyInstances += s.EnemyInstances
	t.EnemiesWithMonster += s.EnemiesWithMonster
	t.DamageDealt += s.DamageDealt
}

// runCapture replays one capture through the host session path and returns the last view of every statistics interval and
// the final view.
func runCapture(t *testing.T, masters *capture.MasterData, path string, sink capture.HistorySink) (map[uint64]capture.ViewSnapshot, capture.ViewSnapshot) {
	t.Helper()
	session, err := capture.OpenSession(capture.SessionConfig{LocalIPv4: "192.168.1.2", ReplayPath: path, PublishInterval: capture.MinPublishInterval, History: sink}, masters)
	if err != nil {
		t.Fatal(err)
	}
	intervals := map[uint64]capture.ViewSnapshot{}
	var final capture.ViewSnapshot
	if _, err := session.Run(context.Background(), func(view capture.ViewSnapshot) {
		intervals[view.StatisticsIntervalID] = view
		if view.Final {
			final = view
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !final.Final {
		t.Fatal("the session published no final view")
	}
	return intervals, final
}

func replayIntoHistory(t *testing.T, store *Store, masters *capture.MasterData, item captureItem) replaySummary {
	summary := replaySummary{Name: item.name, Outcomes: map[string]int{}, Integrity: map[string]int{}, EffectKinds: map[string]int{}}
	digest, err := fileDigestHex(item.path)
	if err != nil {
		t.Fatal(err)
	}
	// Without history first: the reference the history run must not change.
	_, plain := runCapture(t, masters, item.path, nil)
	recorder, err := store.BeginSession(SessionStart{Source: SourceReplay, SourceSHA256: digest, Level: Level()})
	if errors.Is(err, ErrDuplicateSource) {
		// A byte-identical copy of a capture already replayed: it is not stored again.
		var sessions int
		if err := store.db.QueryRowContext(context.Background(), "SELECT count(*) FROM capture_session WHERE source_sha256 = ?", digest).Scan(&sessions); err != nil || sessions != 1 {
			t.Fatalf("a duplicate capture is stored %d times (%v)", sessions, err)
		}
		summary.DuplicateOf = true
		return summary
	}
	if err != nil {
		t.Fatal(err)
	}
	tee := &teeSink{recorder: recorder}
	intervals, final := runCapture(t, masters, item.path, tee)
	if err := recorder.Finish(context.Background(), capture.StopReasonReplayEnd); err != nil {
		t.Fatal(err)
	}
	if tee.dropped != 0 {
		t.Fatalf("the engine journal refused %d entries", tee.dropped)
	}
	if !reflect.DeepEqual(comparableView(final), comparableView(plain)) {
		t.Fatal("keeping history changed the final view")
	}
	summary.Intervals = len(intervals)

	// The engine's own decisions, from the journal copy.
	finished := map[string]*capture.HistoryEncounter{}
	integrity := map[string]capture.DataIntegrity{}
	accepted := 0
	for _, entry := range tee.entries {
		switch entry.Kind {
		case capture.HistoryEncounterFinished:
			if finished[entry.Encounter] != nil {
				t.Fatalf("encounter %s finished twice", entry.Encounter)
			}
			finished[entry.Encounter] = entry.Finished
			integrity[entry.Encounter] = entry.Finished.Snapshot.Integrity
		case capture.HistoryEncounterIntegrity:
			integrity[entry.Encounter] = *entry.Integrity
		case capture.HistoryEncounterDiscarded:
			summary.Discarded++
		case capture.HistoryEventAccepted:
			accepted++
		}
	}
	summary.FinishedEncounters, summary.AcceptedResults = len(finished), accepted
	// The views count finished encounters independently of the journal. The final view is exact; an earlier interval's last
	// view may predate the encounter its protocol session change finished, so it can only count fewer.
	perInterval := map[uint64]uint64{}
	for key := range finished {
		interval, _, _ := parseEncounterKey(key)
		perInterval[interval]++
	}
	for interval, view := range intervals {
		if view.Final && view.Encounters.CompletedTotal != perInterval[interval] || view.Encounters.CompletedTotal > perInterval[interval] {
			t.Fatalf("interval %d: the view counts %d finished encounters, the journal %d", interval, view.Encounters.CompletedTotal, perInterval[interval])
		}
	}

	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, "SELECT id, statistics_interval, sequence, state FROM encounter WHERE capture_session_id = ? ORDER BY id", recorder.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]int64{}
	for rows.Next() {
		var id, interval, sequence int64
		var state string
		if err := rows.Scan(&id, &interval, &sequence, &state); err != nil {
			t.Fatal(err)
		}
		if state != "finished" {
			t.Fatalf("encounter %d-%d is %s after the capture ended", interval, sequence, state)
		}
		stored[fmt.Sprintf("%d-%d", interval, sequence)] = id
	}
	rows.Close()
	if len(stored) != len(finished) {
		t.Fatalf("stored %d encounters, the engine finished %d", len(stored), len(finished))
	}
	summary.ListedEncounters = len(stored)
	for key, engine := range finished {
		id, ok := stored[key]
		if !ok {
			t.Fatalf("encounter %s is not stored", key)
		}
		detail, err := store.Encounter(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !detail.Summary.Complete {
			t.Fatalf("encounter %s is marked incomplete", key)
		}
		want := capture.ApplyEncounterIntegrity(engine.Snapshot, integrity[key])
		got := *detail.Snapshot
		want.EncounterID, got.EncounterID, want.CaptureSessionID = "", "", 0
		if !reflect.DeepEqual(normalizeSnapshot(got), normalizeSnapshot(want)) {
			t.Fatalf("encounter %s is not stored as the engine finished it:\n got %+v\nwant %+v", key, got, want)
		}
		if !reflect.DeepEqual(*detail.Timeline, engine.Timeline) && !(len(detail.Timeline.Buckets) == 0 && len(engine.Timeline.Buckets) == 0) {
			t.Fatalf("encounter %s timeline is not stored as kept", key)
		}
		summary.Outcomes[string(want.Outcome)]++
		summary.Integrity[string(want.Integrity.Status)]++
		summary.DamageDealt += want.Totals.DamageDealt
		if detail.Summary.Scene.Known {
			summary.ScenesKnown++
		}
		summary.EnemyInstances += len(detail.Enemies)
		for _, enemy := range detail.Enemies {
			if enemy.MonsterID != 0 {
				summary.EnemiesWithMonster++
			}
		}
		if !reflect.DeepEqual(detail.Enemies, normalizeEnemies(engine.Enemies)) {
			t.Fatalf("encounter %s enemies differ:\n got %+v\nwant %+v", key, detail.Enemies, engine.Enemies)
		}
		recompute(t, store, id, *detail.Snapshot)
		summary.RecomputedEncounters++
	}
	// The live view of each encounter equals its history projection.
	for interval, view := range intervals {
		for _, live := range view.Encounters.Recent {
			key := fmt.Sprintf("%d-%d", interval, live.Sequence)
			id, ok := stored[key]
			if !ok {
				t.Fatalf("live encounter %s is not stored", key)
			}
			detail, err := store.Encounter(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			projected := masters.ProjectEncounter(*detail.Snapshot)
			projected.EncounterID, live.EncounterID = "", ""
			if !reflect.DeepEqual(projected, live) {
				t.Fatalf("the history view of %s differs from the live view:\nhistory %+v\n   live %+v", key, projected, live)
			}
			summary.LiveViewsCompared++
		}
		if timeline := view.Timeline; timeline != nil && view.Encounters.Current == nil && len(view.Encounters.Recent) > 0 {
			last := view.Encounters.Recent[len(view.Encounters.Recent)-1]
			detail, err := store.Encounter(ctx, stored[fmt.Sprintf("%d-%d", interval, last.Sequence)])
			if err != nil {
				t.Fatal(err)
			}
			rendered, valid := detail.Timeline.RenderForView(timeline.EncounterID, detail.Snapshot.StartedAt, detail.Snapshot.EndedAt)
			if !valid || !reflect.DeepEqual(rendered, *timeline) {
				t.Fatalf("the history timeline of %d-%d differs from the live one", interval, last.Sequence)
			}
			summary.TimelinesCompared++
		}
	}
	var events, keyed, distinct int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*), count(CASE WHEN rpc_id != 0 THEN 1 END),
		(SELECT count(*) FROM (SELECT DISTINCT protocol_session, kind, rpc_id FROM combat_event WHERE capture_session_id = ? AND rpc_id != 0))
		FROM combat_event WHERE capture_session_id = ?`, recorder.SessionID(), recorder.SessionID()).Scan(&events, &keyed, &distinct); err != nil {
		t.Fatal(err)
	}
	if events != accepted || keyed != distinct {
		t.Fatalf("stored %d results of %d accepted; %d keyed, %d distinct keys", events, accepted, keyed, distinct)
	}
	summary.StoredEvents = events
	compareEffects(t, store, recorder.SessionID(), tee.entries, &summary)
	return summary
}

// compareEffects checks that the stored effect lifecycle is the journal's: one row per journaled change, in the tracker's
// order, with the instance, change and observation the tracker gave it, and only observed messages stored as removals.
func compareEffects(t *testing.T, store *Store, span int64, entries []capture.HistoryEntry, summary *replaySummary) {
	t.Helper()
	var journaled []*capture.HistoryEffect
	instances := map[uint64]struct{}{}
	spans := 0
	for _, entry := range entries {
		switch entry.Kind {
		case capture.HistoryRecordingStarted:
			spans++
		case capture.HistoryEffectChanged:
			if entry.Effect.Sequence != uint64(len(journaled)+1) {
				t.Fatalf("effect change %d has sequence %d", len(journaled)+1, entry.Effect.Sequence)
			}
			journaled = append(journaled, entry.Effect)
			instances[entry.Effect.Instance] = struct{}{}
		}
	}
	if spans != 1 {
		t.Fatalf("a replay recorded from its start has %d spans", spans)
	}
	rows, err := store.db.QueryContext(context.Background(), "SELECT sequence, instance, after_combat_sequence, change_kind, observation, evidence, reason FROM effect_event WHERE capture_session_id = ? ORDER BY sequence", span)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := 0
	for rows.Next() {
		var sequence, instance, after int64
		var change, observation, evidence, reason string
		if err := rows.Scan(&sequence, &instance, &after, &change, &observation, &evidence, &reason); err != nil {
			t.Fatal(err)
		}
		if stored >= len(journaled) {
			t.Fatalf("stored more effect changes than the tracker journaled")
		}
		want := journaled[stored]
		if uint64(sequence) != want.Sequence || uint64(instance) != want.Instance || uint64(after) != want.AfterCombat || change != string(want.Change) ||
			observation != want.Observation || evidence != string(want.Evidence) || reason != want.Reason {
			t.Fatalf("effect change %d stored as %d/%d/%d/%s/%s/%s/%s, journaled %+v", stored+1, sequence, instance, after, change, observation, evidence, reason, *want)
		}
		switch change {
		case string(capture.EffectChangeRemove):
			if observation != capture.EffectObserved || (evidence != string(capture.EffectEvidenceServerNotify) && evidence != string(capture.EffectEvidenceClientReport)) {
				t.Fatalf("a removal that is not an observed message: %s/%s", observation, evidence)
			}
		case string(capture.EffectChangeUntracked):
			if observation != capture.EffectUntracked {
				t.Fatalf("an end of tracking stored as %s", observation)
			}
		case string(capture.EffectChangeHolderRemoved), string(capture.EffectChangeSuperseded):
			if observation != capture.EffectDerived {
				t.Fatalf("a derived end stored as %s", observation)
			}
		}
		summary.EffectKinds[change]++
		stored++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if stored != len(journaled) {
		t.Fatalf("stored %d effect changes of %d journaled", stored, len(journaled))
	}
	var storedInstances int
	if err := store.db.QueryRowContext(context.Background(), "SELECT count(*) FROM effect_instance WHERE capture_session_id = ?", span).Scan(&storedInstances); err != nil || storedInstances != len(instances) {
		t.Fatalf("stored %d effect instances of %d journaled (%v)", storedInstances, len(instances), err)
	}
	summary.EffectChanges, summary.EffectInstances = stored, storedInstances
}

// normalizeSnapshot maps nil and empty slices to one form; storage cannot tell them apart and neither can a view.
func normalizeSnapshot(s capture.EncounterSnapshot) capture.EncounterSnapshot {
	if len(s.Integrity.Risks) == 0 {
		s.Integrity.Risks = []string{}
	}
	if len(s.Healing.ByHealer) == 0 {
		s.Healing.ByHealer = []capture.HealerTotals{}
	}
	if len(s.Roles) == 0 {
		s.Roles = []capture.RoleDamageSnapshot{}
	}
	s.Roles = append([]capture.RoleDamageSnapshot(nil), s.Roles...)
	for i := range s.Roles {
		s.Roles[i].Skills = normalizeSkills(s.Roles[i].Skills)
		s.Roles[i].UnassignedSkill = normalizeUnassigned(s.Roles[i].UnassignedSkill)
	}
	if s.Vehicle != nil {
		vehicle := *s.Vehicle
		vehicle.Skills = normalizeSkills(vehicle.Skills)
		vehicle.UnassignedSkill = normalizeUnassigned(vehicle.UnassignedSkill)
		s.Vehicle = &vehicle
	}
	return s
}

func normalizeSkills(skills []capture.SkillDamageSnapshot) []capture.SkillDamageSnapshot {
	out := make([]capture.SkillDamageSnapshot, 0, len(skills))
	for _, skill := range skills {
		if len(skill.DamageIDs) == 0 {
			skill.DamageIDs = nil
		}
		if len(skill.EchoVisions) == 0 {
			skill.EchoVisions = nil
		}
		out = append(out, skill)
	}
	return out
}

func normalizeUnassigned(row capture.UnassignedSkillTotals) capture.UnassignedSkillTotals {
	if len(row.DamageIDs) == 0 {
		row.DamageIDs = nil
	}
	if len(row.EchoVisions) == 0 {
		row.EchoVisions = nil
	}
	return row
}

// normalizeEnemies gives the engine's enemies the statuses storage derives from the IDs it keeps.
func normalizeEnemies(enemies []capture.EncounterEnemySnapshot) []capture.EncounterEnemySnapshot {
	out := make([]capture.EncounterEnemySnapshot, 0, len(enemies))
	for _, enemy := range enemies {
		enemy.MonsterStatus, enemy.TemplateStatus = linkStatus(enemy.MonsterID), linkStatus(enemy.TemplateID)
		out = append(out, enemy)
	}
	return out
}

// recompute derives one finished encounter's totals and splits from its stored combat results with the engine's crediting
// rules (role_aggregation.go, skill_breakdown.go, healing.go) and compares them with the stored engine results.
func recompute(t *testing.T, store *Store, id int64, snapshot capture.EncounterSnapshot) {
	t.Helper()
	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, `SELECT kind, flow, amount, applied_healing, skill_id, vehicle_kind, context_downgrade,
		source_player_id, source_role_id, source_role_basis, target_player_id, target_role_id FROM combat_event WHERE encounter_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type role struct{ player, role int64 }
	totals := capture.EncounterTotals{}
	dealt, direct, summon, taken := map[role]int64{}, map[role]int64{}, map[role]int64{}, map[role]int64{}
	skills := map[role]map[int64]int64{}
	var vehicle, unassignedDealt, unassignedTaken, healingEffective, healingReported int64
	for rows.Next() {
		var kind, flow, vehicleKind, downgrade string
		var amount, applied, skill int64
		var sourcePlayer, sourceRole, targetPlayer, targetRole *int64
		var basis *string
		if err := rows.Scan(&kind, &flow, &amount, &applied, &skill, &vehicleKind, &downgrade, &sourcePlayer, &sourceRole, &basis, &targetPlayer, &targetRole); err != nil {
			t.Fatal(err)
		}
		if kind == string(capture.CombatEventHealing) {
			healingEffective += applied
			healingReported += amount
			continue
		}
		switch capture.CombatFlow(flow) {
		case capture.CombatFlowLocalOutgoing:
			totals.DamageDealt += amount
			totals.OutgoingEvents++
			switch {
			case vehicleKind != "":
				vehicle += amount
			case sourceRole != nil:
				key := role{*sourcePlayer, *sourceRole}
				dealt[key] += amount
				if basis != nil && *basis == "summoner_chain" {
					summon[key] += amount
				} else {
					direct[key] += amount
				}
				if skills[key] == nil {
					skills[key] = map[int64]int64{}
				}
				skills[key][skill] += amount
			default:
				unassignedDealt += amount
			}
		case capture.CombatFlowLocalIncoming:
			totals.DamageTaken += amount
			totals.IncomingEvents++
			if targetRole != nil {
				taken[role{*targetPlayer, *targetRole}] += amount
			} else {
				unassignedTaken += amount
			}
		case capture.CombatFlowUnattributedOutgoing:
			totals.UnattributedOutgoingDamage += amount
			totals.UnattributedOutgoingEvents++
		case capture.CombatFlowUnattributedIncoming:
			totals.UnattributedIncomingDamage += amount
			totals.UnattributedIncomingEvents++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if totals != snapshot.Totals {
		t.Fatalf("encounter %d: recomputed totals %+v, stored %+v", id, totals, snapshot.Totals)
	}
	if unassignedDealt != snapshot.UnassignedRole.DamageDealt || unassignedTaken != snapshot.UnassignedRole.DamageTaken {
		t.Fatalf("encounter %d: recomputed unassigned %d/%d, stored %+v", id, unassignedDealt, unassignedTaken, snapshot.UnassignedRole)
	}
	if snapshot.Vehicle != nil && vehicle != snapshot.Vehicle.DamageDealt || snapshot.Vehicle == nil && vehicle != 0 {
		t.Fatalf("encounter %d: recomputed vehicle damage %d", id, vehicle)
	}
	for _, stored := range snapshot.Roles {
		key := role{int64(stored.PlayerID), int64(stored.RoleID)}
		if dealt[key] != stored.DamageDealt || direct[key] != stored.DirectDamage || summon[key] != stored.SummonDamage || taken[key] != stored.DamageTaken {
			t.Fatalf("encounter %d role %d: recomputed %d/%d/%d/%d, stored %+v", id, stored.RoleID, dealt[key], direct[key], summon[key], taken[key], stored)
		}
		sum := stored.UnassignedSkill.DamageDealt
		for _, skill := range stored.Skills {
			if skills[key][int64(skill.SkillID)] != skill.DamageDealt {
				t.Fatalf("encounter %d role %d skill %d: recomputed %d, stored %d", id, stored.RoleID, skill.SkillID, skills[key][int64(skill.SkillID)], skill.DamageDealt)
			}
			sum += skill.DamageDealt
		}
		if skills[key][0] > stored.UnassignedSkill.DamageDealt || sum != stored.DamageDealt {
			t.Fatalf("encounter %d role %d: the skill split does not add up", id, stored.RoleID)
		}
	}
	if healingEffective != snapshot.Healing.Effective || healingReported != snapshot.Healing.Reported {
		t.Fatalf("encounter %d: recomputed healing %d/%d, stored %d/%d", id, healingEffective, healingReported, snapshot.Healing.Effective, snapshot.Healing.Reported)
	}
}

// checkHeatmap checks that every year's day counts are the listed encounters of those dates.
func checkHeatmap(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	var listed int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM encounter WHERE state IN "+listedStates).Scan(&listed); err != nil {
		t.Fatal(err)
	}
	first, err := store.Year(ctx, time.Now().Year())
	if err != nil {
		t.Fatal(err)
	}
	if listed == 0 {
		return
	}
	counted := 0
	for year := first.FirstYear; year <= first.LastYear; year++ {
		view, err := store.Year(ctx, year)
		if err != nil {
			t.Fatal(err)
		}
		days := 0
		for _, day := range view.Days {
			page, err := store.Day(ctx, day.Date, 0, MaxDayPage)
			if err != nil || page.Total != day.Encounters {
				t.Fatalf("%s: the heatmap counts %d, the day lists %d (%v)", day.Date, day.Encounters, page.Total, err)
			}
			for _, encounter := range page.Encounters {
				if encounter.LocalDate != day.Date {
					t.Fatalf("%s lists an encounter of %s", day.Date, encounter.LocalDate)
				}
			}
			days += day.Encounters
		}
		if days != view.Total {
			t.Fatalf("%d: days add up to %d, total %d", year, days, view.Total)
		}
		counted += view.Total
	}
	if counted != listed {
		t.Fatalf("the heatmap counts %d encounters, the history lists %d", counted, listed)
	}
}

// historyDigest hashes what the History view can read: every year, every day page and every encounter detail.
func historyDigest(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	hash := sha256.New()
	first, err := store.Year(ctx, time.Now().Year())
	if err != nil {
		t.Fatal(err)
	}
	for year := first.FirstYear; year != 0 && year <= first.LastYear; year++ {
		view, err := store.Year(ctx, year)
		if err != nil {
			t.Fatal(err)
		}
		writeHash(t, hash, view)
		for _, day := range view.Days {
			page, err := store.Day(ctx, day.Date, 0, MaxDayPage)
			if err != nil {
				t.Fatal(err)
			}
			writeHash(t, hash, page)
			for _, encounter := range page.Encounters {
				detail, err := store.Encounter(ctx, encounter.ID)
				if err != nil {
					t.Fatal(err)
				}
				writeHash(t, hash, struct {
					Detail   EncounterDetail
					Snapshot *capture.EncounterSnapshot
					Timeline *capture.StoredTimeline
					Enemies  []capture.EncounterEnemySnapshot
				}{detail, detail.Snapshot, detail.Timeline, detail.Enemies})
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeHash(t *testing.T, w io.Writer, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileDigestHex(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// comparableView drops what differs between two runs of the same capture for reasons other than analysis: the process-local
// capture session number (also inside encounter IDs) and the timing of the capture loop itself.
func comparableView(v capture.ViewSnapshot) capture.ViewSnapshot {
	v.CaptureSessionID = 0
	v.Health.Capture = capture.ViewCaptureHealth{}
	v.Health.Session.FirstFrameAgeSeconds, v.Health.Session.FrameIdleSeconds = nil, nil
	strip := func(id string) string {
		if _, rest, ok := strings.Cut(id, "-"); ok {
			return rest
		}
		return id
	}
	if v.Encounters.Current != nil {
		current := *v.Encounters.Current
		current.EncounterID = strip(current.EncounterID)
		v.Encounters.Current = &current
	}
	recent := append([]capture.ViewEncounter(nil), v.Encounters.Recent...)
	for i := range recent {
		recent[i].EncounterID = strip(recent[i].EncounterID)
	}
	v.Encounters.Recent = recent
	if v.Timeline != nil {
		timeline := *v.Timeline
		timeline.EncounterID = strip(timeline.EncounterID)
		v.Timeline = &timeline
	}
	return v
}
