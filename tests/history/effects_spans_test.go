package history

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// createSchemaTwo writes a file as a schema 2 build did, labelled with the stale v0.6.0 registry value of that build line.
func createSchemaTwo(t *testing.T, path string) {
	t.Helper()
	createSchemaOne(t, path)
	db, err := sql.Open("sqlite", plainDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{path: path, db: db, now: time.Now, build: testBuild, location: time.UTC}
	if err := store.applyMigration(context.Background(), migrations[1]); err != nil {
		t.Fatalf("schema 2: %v", err)
	}
	for _, statement := range []string{
		`UPDATE capture_session SET app_version = 'v0.6.0' WHERE id = 1`,
		`INSERT INTO capture_session (id, session_uid, source, status, opened_at_ns, utc_offset_s, zone_name, history_level, app_version, app_build, app_commit, app_dirty, app_profile, protocol_schema, master_sha256)
			VALUES (2, 'test-binary', 'replay', 'closed', 1700000100000000000, 0, 'UTC', 'encounters', 'UNAVAILABLE', 0, 'c', 'unknown', 'development', 'p', 'm')`,
		`UPDATE encounter SET scene_instance_id = 2117, scene_recorded = 1, enemies_recorded = 1 WHERE id = 1`,
		`INSERT INTO encounter_enemy (encounter_id, ordinal, template_id, damage_taken, hits_taken, damage_dealt, hits_dealt) VALUES (1, 0, 34000, 500, 5, 0, 0)`,
		`INSERT INTO combat_event (capture_session_id, sequence, encounter_id, protocol_session, rpc_id, observed_at_ns, kind, flow, confidence, evidence, amount,
			source_entity_id, source_generation, source_relation, target_entity_id, target_generation, target_relation)
			VALUES (1, 1, 1, 1, 77, 1700000001000000000, 'damage', 'local_outgoing', 'confirmed', 'rpc', 500, 1, 0, 'LOCAL', 2, 0, 'UNRELATED')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("schema 2 row: %v", err)
		}
	}
}

func TestSchemaTwoIsUpgradedWithoutRaisingItsCoverage(t *testing.T) {
	path := testPath(t)
	createSchemaTwo(t, path)
	store := openTestStore(t, path, nil)
	if status := store.Status(); status.State != StateReady || status.SchemaVersion != 3 {
		t.Fatalf("status = %+v", status)
	}
	if backups, _ := filepath.Glob(path + ".schema-2-*.bak"); len(backups) != 1 {
		t.Fatalf("pre-migration copies = %v", backups)
	}
	ctx := context.Background()
	day, err := store.Day(ctx, "2023-11-15", 0, 10, mustZone(t, "Asia/Tokyo"))
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	old := day.Encounters[0]
	if !old.Coverage.CombatEvents || old.Coverage.EffectEvents || old.Coverage.SkillCasts || old.CheckpointAt != nil || old.IncompleteReason != "" ||
		!old.Scene.Known || old.Scene.InstanceID != 2117 || old.MainEnemy == nil || old.DPS.Value != 50 {
		t.Fatalf("upgraded row = %+v", old)
	}
	detail, err := store.Encounter(ctx, old.ID, time.UTC)
	if err != nil || detail.Session.Coverage.EffectEvents || detail.Effects != (EffectWindow{}) {
		t.Fatalf("an old encounter claims effect data: %+v %+v (%v)", detail.Session.Coverage, detail.Effects, err)
	}
	// The v0.6.0 label of a v0.7.0-line build is corrected and kept; a label that is not that stale value is left alone.
	if detail.Session.AppVersion != "v0.7.0" || detail.Session.RecordedAppVersion != "v0.6.0" || detail.Session.StartReason != "capture_start" {
		t.Fatalf("session = %+v", detail.Session)
	}
	var version string
	var recorded sql.NullString
	var events int
	if err := store.db.QueryRowContext(ctx, "SELECT app_version, recorded_app_version, combat_events_recorded FROM capture_session WHERE id = 2").Scan(&version, &recorded, &events); err != nil ||
		version != "UNAVAILABLE" || recorded.Valid || events != 0 {
		t.Fatalf("session 2 = %q %v %d (%v)", version, recorded, events, err)
	}
	var stored int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM combat_event WHERE encounter_id = 1").Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("combat events = %d (%v)", stored, err)
	}
	assertIntegrity(t, store)
}

func TestSchemaOneIsUpgradedToTheLatestInOneStep(t *testing.T) {
	path := testPath(t)
	createSchemaOne(t, path)
	store := openTestStore(t, path, nil)
	var versions []int
	rows, err := store.db.Query("SELECT version FROM schema_migration ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var version int
		_ = rows.Scan(&version)
		versions = append(versions, version)
	}
	rows.Close()
	if !reflect.DeepEqual(versions, []int{1, 2, 3}) {
		t.Fatalf("migrations = %v", versions)
	}
	assertIntegrity(t, store)
}

// effectChange builds one journaled effect change of instance on holder 10 from source 20.
func effectChange(sequence, instance, afterCombat uint64, change capture.EffectChange, observation string, at time.Time, mutate func(*capture.HistoryEffect)) capture.HistoryEntry {
	effect := capture.HistoryEffect{
		Instance: instance, Sequence: sequence, AfterCombat: afterCombat, Change: change, Observation: observation, Evidence: capture.EffectEvidenceServerNotify,
		ObservedAt: at, SessionNumber: 1, HolderEntityID: 10, EffectID: 100, HasEffectID: true, SourceEntityID: 20, HasSource: true,
		HolderRelation: capture.EffectRelationUnknown, SourceRelation: capture.EffectRelationLocalRole, Origin: capture.EffectOriginApply,
		FirstObservedAt: at, Stack: 1, HasStack: true, Duration: 8, HasDuration: true, TimeKnown: true, Remaining: 8, BasisAt: at,
	}
	if mutate != nil {
		mutate(&effect)
	}
	return capture.HistoryEntry{Kind: capture.HistoryEffectChanged, Sequence: sequence, Effect: &effect}
}

func TestEffectLifecycleIsStoredInOrderWithItsInstance(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC).Local()
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	encounter := finishedEncounter(1, 1, start, 20, 400, capture.IntegrityIntact)
	effects := []capture.HistoryEntry{
		effectChange(1, 1, 0, capture.EffectChangeApply, capture.EffectObserved, at(1), nil),
		// The holder's relation becomes known later: the instance is upgraded, never downgraded.
		effectChange(2, 1, 1, capture.EffectChangeStack, capture.EffectObserved, at(2), func(e *capture.HistoryEffect) {
			e.Stack, e.HolderRelation = 2, capture.EffectRelationLocalRole
			e.HolderActor = &capture.EffectActorIdentity{Role: &capture.CombatRole{PlayerID: 7, RoleID: 1505, Basis: "role_entity"}}
		}),
		effectChange(3, 1, 1, capture.EffectChangeStack, capture.EffectObserved, at(2), func(e *capture.HistoryEffect) { e.Stack = 2 }),
		effectChange(4, 1, 2, capture.EffectChangeRemove, capture.EffectObserved, at(5), func(e *capture.HistoryEffect) {
			e.Reason, e.HolderRelation = string(capture.EffectLifecycleConfirmedRemoved), capture.EffectRelationUnknown
		}),
		// An instance first stored by a later change started before the span.
		effectChange(5, 2, 2, capture.EffectChangeUntracked, capture.EffectUntracked, at(30), func(e *capture.HistoryEffect) {
			e.Reason, e.Evidence, e.FirstObservedAt, e.HasEffectID = capture.EffectUntrackedSceneChange, "", at(-100), false
		}),
	}
	recorder := record(t, store, liveStart, append(encounter, effects...))
	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, "SELECT sequence, instance, change_kind, observation, stack, after_combat_sequence FROM effect_event WHERE capture_session_id = ? ORDER BY sequence", recorder.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		sequence, instance  int
		change, observation string
		stack               sql.NullInt64
		after               int
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.sequence, &r.instance, &r.change, &r.observation, &r.stack, &r.after); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 5 || got[1].change != "stack" || got[2].change != "stack" || got[3].change != "remove" || got[3].after != 2 || got[4].observation != "untracked" {
		t.Fatalf("stored changes = %+v", got)
	}
	var relation string
	var role sql.NullInt64
	var started, startRecorded int
	var effectID sql.NullInt64
	if err := store.db.QueryRowContext(ctx, "SELECT holder_relation, holder_role_id, start_recorded FROM effect_instance WHERE capture_session_id = ? AND instance = 1", recorder.SessionID()).
		Scan(&relation, &role, &started); err != nil || relation != "local_role" || role.Int64 != 1505 || started != 1 {
		t.Fatalf("instance 1 = %s %v %d (%v)", relation, role, started, err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT start_recorded, effect_id FROM effect_instance WHERE capture_session_id = ? AND instance = 2", recorder.SessionID()).
		Scan(&startRecorded, &effectID); err != nil || startRecorded != 0 || effectID.Valid {
		t.Fatalf("instance 2 = %d %v (%v)", startRecorded, effectID, err)
	}
	day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 || !day.Encounters[0].Coverage.EffectEvents || day.Encounters[0].Coverage.SkillCasts {
		t.Fatalf("day = %+v (%v)", day.Encounters, err)
	}
	detail, err := store.Encounter(ctx, day.Encounters[0].ID, time.UTC)
	if err != nil || detail.Effects != (EffectWindow{Changes: 4, Instances: 1, Observed: 4}) {
		t.Fatalf("effects during the encounter = %+v (%v)", detail.Effects, err)
	}
	assertIntegrity(t, store)
}

func TestRecordingSpansCloseAndOpenRows(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC).Local()
	recorder, err := store.BeginSession(SessionStart{Source: SourceLive, Level: capture.HistoryEvents, Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	if recorder.SessionID() != 0 {
		t.Fatal("a paused capture created a session row")
	}
	first := finishedEncounter(1, 1, start, 10, 100, capture.IntegrityIntact)
	second := finishedEncounter(1, 2, start.Add(time.Minute), 10, 200, capture.IntegrityIntact)
	enabled := capture.HistoryEntry{Kind: capture.HistoryRecordingStarted, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStartEnabled}}
	stopped := capture.HistoryEntry{Kind: capture.HistoryRecordingStopped, Recording: &capture.HistoryRecordingChange{Reason: capture.HistoryStopDisabled, Open: []string{"1-2"}}}
	recorder.Submit([]capture.HistoryEntry{enabled}, 0)
	recorder.Submit(first, 0)
	// The second encounter is in progress when recording stops: it is kept unfinished, never finished.
	recorder.Submit(second[:2], 0)
	recorder.Submit([]capture.HistoryEntry{stopped}, 0)
	// Between spans only a recorded encounter's later integrity change is stored.
	degraded := capture.DataIntegrity{Status: capture.IntegrityDegraded, Risks: []string{"combat_results_possibly_lost"}, TransportLosses: 1}
	recorder.Submit([]capture.HistoryEntry{{Kind: capture.HistoryEncounterIntegrity, Encounter: "1-1", Integrity: &degraded}, second[2]}, 0)
	recorder.Submit([]capture.HistoryEntry{enabled}, 0)
	recorder.Submit(finishedEncounter(1, 3, start.Add(2*time.Minute), 10, 300, capture.IntegrityIntact), 0)
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, "SELECT id, status, start_reason, stop_reason FROM capture_session ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var spans []string
	for rows.Next() {
		var id int64
		var status, startReason string
		var stopReason sql.NullString
		_ = rows.Scan(&id, &status, &startReason, &stopReason)
		spans = append(spans, status+"/"+startReason+"/"+stopReason.String)
	}
	rows.Close()
	if !reflect.DeepEqual(spans, []string{"closed/recording_enabled/recording_disabled", "closed/recording_enabled/requested"}) {
		t.Fatalf("spans = %v", spans)
	}
	day, err := store.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 3 {
		t.Fatalf("day = %+v (%v)", day.Encounters, err)
	}
	cut := day.Encounters[1]
	if cut.State != "abandoned" || cut.Complete || cut.IncompleteReason != "recording_disabled" || day.Encounters[0].Integrity != "DEGRADED" ||
		day.Encounters[0].State != "finished" || day.Encounters[2].State != "finished" || !day.Encounters[2].Complete {
		t.Fatalf("encounters = %+v", day.Encounters)
	}
	assertIntegrity(t, store)
}

func TestCheckpointSurvivesAnAbnormalExitAsUnfinished(t *testing.T) {
	path := testPath(t)
	store := openTestStore(t, path, nil)
	start := time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC).Local()
	entries := finishedEncounter(1, 1, start, 20, 800, capture.IntegrityIntact)
	checkpoint := *entries[2].Finished
	checkpoint.Snapshot.State, checkpoint.Snapshot.Outcome, checkpoint.Snapshot.EndedAt, checkpoint.Snapshot.EndBasis = capture.EncounterActive, capture.EncounterOutcomeOpen, time.Time{}, ""
	checkpoint.CheckpointAt = start.Add(12 * time.Second)
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatal(err)
	}
	recorder.Submit(append(entries[:2:2], capture.HistoryEntry{Kind: capture.HistoryEncounterCheckpoint, Encounter: "1-1", Finished: &checkpoint}), 0)
	// The application ends without finishing the session: storage drains, then the file is closed as an exit would leave it.
	waitQueued(t, recorder)
	if err := store.closeHandles(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path, nil)
	if status := reopened.Status(); status.RecoveredSessions != 1 || status.RecoveredEncounters != 1 {
		t.Fatalf("status = %+v", status)
	}
	ctx := context.Background()
	day, err := reopened.Day(ctx, start.Format("2006-01-02"), 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	summary := day.Encounters[0]
	if summary.State != "abandoned" || summary.Complete || summary.IncompleteReason != "application_exit" || summary.CheckpointAt == nil ||
		!summary.CheckpointAt.Equal(start.Add(12*time.Second)) || summary.DPS.Status != capture.DPSStatusUnavailable {
		t.Fatalf("summary = %+v", summary)
	}
	detail, err := reopened.Encounter(ctx, summary.ID, time.UTC)
	if err != nil || detail.Snapshot == nil || detail.Snapshot.State != capture.EncounterActive || detail.Snapshot.Outcome != capture.EncounterOutcomeOpen ||
		detail.Snapshot.Totals.DamageDealt != 800 {
		t.Fatalf("detail = %+v (%v)", detail.Snapshot, err)
	}
	assertIntegrity(t, reopened)
}

func TestFinishedEntryReplacesTheCheckpoint(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 11, 11, 4, 0, 0, 0, time.UTC).Local()
	entries := finishedEncounter(1, 1, start, 20, 900, capture.IntegrityIntact)
	checkpoint := *entries[2].Finished
	checkpoint.Snapshot.Outcome, checkpoint.CheckpointAt = capture.EncounterOutcomeOpen, start.Add(5*time.Second)
	checkpoint.Snapshot.Totals.DamageDealt = 10
	record(t, store, liveStart, entries[:2], []capture.HistoryEntry{{Kind: capture.HistoryEncounterCheckpoint, Encounter: "1-1", Finished: &checkpoint}}, entries[2:])
	var checkpointAt sql.NullInt64
	var damage int64
	var state string
	if err := store.db.QueryRow("SELECT checkpoint_at_ns, damage_dealt, state FROM encounter").Scan(&checkpointAt, &damage, &state); err != nil ||
		checkpointAt.Valid || damage != 900 || state != "finished" {
		t.Fatalf("encounter = %v %d %s (%v)", checkpointAt, damage, state, err)
	}
}

// waitQueued waits until the recorder's writer stored everything submitted so far.
func waitQueued(t *testing.T, recorder *Recorder) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for recorder.queued.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the writer did not drain")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
