package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/buildinfo"
	"wwlogs/internal/capture"
)

// plainDSN opens path as plaintext SQLite through modernc.org/sqlite, in WAL mode: a file the history must never touch.
func plainDSN(path string) string {
	absolute, _ := filepath.Abs(path)
	uriPath := filepath.ToSlash(absolute)
	if len(uriPath) >= 2 && uriPath[1] == ':' {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String() + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
}

var testBuild = buildinfo.Identity{Version: "v0.7.0", BuildNumber: 0, Commit: "0123456789abcdef0123456789abcdef01234567", Dirty: "false", Profile: buildinfo.ProfilePublic}

func openTestStore(t *testing.T, path string, location *time.Location) *Store {
	t.Helper()
	if location == nil {
		location = time.UTC
	}
	store, err := Open(Options{Path: path, Location: location, Build: &testBuild, MasterSHA256: "test-master"})
	if err != nil {
		t.Fatalf("open history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "history", "combat-history.db")
}

// finishedEncounter builds the journal entries the engine produces for one finished encounter.
func finishedEncounter(interval, sequence uint64, start time.Time, seconds int, damage int64, integrity capture.IntegrityStatus) []capture.HistoryEntry {
	key := strconv.FormatUint(interval, 10) + "-" + strconv.FormatUint(sequence, 10)
	// Capture times reach the engine in the host's local representation; stored times come back the same way.
	start = start.Local()
	end := start.Add(time.Duration(seconds) * time.Second)
	risks := []string{}
	if integrity == capture.IntegrityDegraded {
		risks = []string{"combat_results_possibly_lost"}
	}
	snapshot := capture.EncounterSnapshot{
		StatisticsIntervalID: interval, Sequence: sequence, ProtocolSession: 1, State: capture.EncounterFinished, Outcome: capture.EncounterOutcomeConfirmed,
		StartedAt: start, StartBasis: "battle_state_enter", StartConfirmed: true, BattleEntered: start, EndedAt: end, EndBasis: "battle_state_exit",
		EndDecidedAt: end.Add(time.Second), FirstActivityAt: start, LastActivityAt: end,
		Totals:    capture.EncounterTotals{DamageDealt: damage, DamageTaken: 120, OutgoingEvents: 3, IncomingEvents: 1, UnattributedOutgoingDamage: 7, UnattributedOutgoingEvents: 1},
		Integrity: capture.DataIntegrity{Status: integrity, Risks: risks},
		EncounterDPS: capture.DPSResult{Status: capture.DPSStatusAvailable, Damage: damage, Duration: end.Sub(start), Value: float64(damage) / end.Sub(start).Seconds(),
			StartObservedAt: start, EndObservedAt: end, Confidence: "confirmed"},
		PeakDPS: capture.DPSResult{Status: capture.DPSStatusAvailable, Damage: damage, Duration: time.Second, Value: float64(damage), StartObservedAt: start, EndObservedAt: start.Add(time.Second), Confidence: "confirmed"},
		Healing: capture.HealingResult{Status: capture.DPSStatusAvailable, HealingTotals: capture.HealingTotals{Effective: 40, Reported: 90, Overheal: 50, Events: 2,
			ByHealer: []capture.HealerTotals{{PlayerID: 7, RoleID: 1505, Effective: 40, Reported: 90, Events: 2}}}},
		Roles: []capture.RoleDamageSnapshot{{PlayerID: 7, RoleID: 1505, DamageDealt: damage - 10, OutgoingEvents: 2, DirectDamage: damage - 10, DamageTaken: 120, IncomingEvents: 1,
			Skills:          []capture.SkillDamageSnapshot{{SkillID: 150501, DamageDealt: damage - 10, OutgoingEvents: 2, DamageIDs: []uint64{11, 12}, EchoVisions: []uint64{390080005}}},
			UnassignedSkill: capture.UnassignedSkillTotals{}}},
		UnassignedRole: capture.UnassignedRoleTotals{DamageDealt: 10, OutgoingEvents: 1},
	}
	stored := capture.StoredTimeline{Anchor: start, ScaleSeconds: 1,
		Contributors: []capture.TimelineContributor{{Kind: "role", PlayerID: 7, RoleID: 1505}, {Kind: "unassigned"}},
		Buckets:      []capture.StoredTimelineBucket{{Index: 0, Contributor: 0, Damage: damage - 10}, {Index: 0, Contributor: 1, Damage: 10}}}
	return []capture.HistoryEntry{
		{Kind: capture.HistoryEncounterOpened, Encounter: key, Opened: &capture.HistoryEncounterOpening{ProtocolSession: 1, StatisticsInterval: interval, Sequence: sequence, StartedAt: start, StartBasis: "battle_state_enter", Scene: capture.SceneContext{InstanceID: 2117, Known: true}}},
		{Kind: capture.HistoryEncounterActive, Encounter: key},
		{Kind: capture.HistoryEncounterFinished, Encounter: key, Finished: &capture.HistoryEncounter{Snapshot: snapshot, Timeline: stored, Scene: capture.SceneContext{InstanceID: 2117, Known: true},
			Enemies: []capture.EncounterEnemySnapshot{
				{Ordinal: 0, MonsterID: 340000040, MonsterStatus: "CORRELATED", DamageTaken: damage - 30, HitsTaken: 2, DamageDealt: 120, HitsDealt: 1, FirstSeenAt: start, LastSeenAt: end},
				{Ordinal: 1, MonsterID: 340000040, MonsterStatus: "CORRELATED", DamageTaken: 20, HitsTaken: 1, FirstSeenAt: start, LastSeenAt: start},
			}}},
	}
}

// protocolOne names protocol session 1 with the game version this build decodes, as the journal does before anything of the
// session and again at every span start.
var protocolOne = capture.HistoryEntry{Kind: capture.HistoryProtocolConfirmed, Protocol: &capture.HistoryProtocolSession{Number: 1, GameVersion: capture.CurrentGameVersion()}}

// begin starts a recorder whose first span names protocol session 1, as a capture's journal does.
func begin(t *testing.T, store *Store, start SessionStart) *Recorder {
	t.Helper()
	recorder, err := store.BeginSession(start)
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if start.Paused {
		return recorder
	}
	if start.Source == SourceReplay {
		if err := recorder.SubmitWait(context.Background(), []capture.HistoryEntry{protocolOne}, 0); err != nil {
			t.Fatal(err)
		}
	} else {
		recorder.Submit([]capture.HistoryEntry{protocolOne}, 0)
	}
	return recorder
}

func record(t *testing.T, store *Store, start SessionStart, batches ...[]capture.HistoryEntry) *Recorder {
	t.Helper()
	recorder := begin(t, store, start)
	for _, batch := range batches {
		recorder.Submit(batch, 0)
	}
	if err := recorder.Finish(context.Background(), capture.StopReasonReplayEnd); err != nil {
		t.Fatalf("finish session: %v", err)
	}
	return recorder
}

var liveStart = SessionStart{Source: SourceLive}

func TestOpenCreatesLatestSchema(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	status := store.Status()
	if status.State != StateReady || status.SchemaVersion != schemaVersion || !status.Encrypted || status.Encryption != "aes-256-xts" || status.KeyProtection != "dpapi-user" {
		t.Fatalf("status = %+v", status)
	}
	var mode string
	var foreignKeys, applicationIDValue, version int
	ctx := context.Background()
	for query, target := range map[string]any{"PRAGMA journal_mode": &mode, "PRAGMA foreign_keys": &foreignKeys, "PRAGMA application_id": &applicationIDValue, "PRAGMA user_version": &version} {
		if err := store.db.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if mode != "wal" || foreignKeys != 1 || applicationIDValue != applicationID || version != schemaVersion {
		t.Fatalf("journal_mode=%s foreign_keys=%d application_id=%d user_version=%d", mode, foreignKeys, applicationIDValue, version)
	}
	assertIntegrity(t, store)
	// A new file is the schema itself, created in one step: its tables and nothing else.
	tables, err := tableNames(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"capture_gap", "capture_session", "combat_event", "effect_event", "effect_instance", "encounter", "encounter_contributor", "encounter_enemy",
		"encounter_healer", "encounter_risk", "encounter_skill", "encounter_skill_evidence", "encounter_timeline", "encounter_timeline_bucket",
		"encounter_timeline_contributor", "protocol_session"}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("tables = %v\nwant %v", tables, want)
	}
}

// tableNames lists the tables of a history in name order.
func tableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func assertIntegrity(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	var check string
	if err := store.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q (%v)", check, err)
	}
	rows, err := store.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check reported a violation")
	}
}

func TestFinishedEncounterRoundTripsAndSurvivesReopen(t *testing.T) {
	path := testPath(t)
	start := time.Date(2026, 11, 10, 14, 30, 0, 123456789, time.UTC)
	entries := finishedEncounter(1, 1, start, 20, 1000, capture.IntegrityIntact)
	store := openTestStore(t, path, nil)
	record(t, store, liveStart, entries)
	want := entries[2].Finished
	check := func(store *Store) {
		t.Helper()
		ctx := context.Background()
		year, err := store.Year(ctx, 2026, time.UTC)
		if err != nil || year.Total != 1 || len(year.Days) != 1 || year.Days[0] != (DayCount{Date: "2026-11-10", Encounters: 1}) || year.FirstYear != 2026 || year.LastYear != 2026 {
			t.Fatalf("year = %+v (%v)", year, err)
		}
		day, err := store.Day(ctx, "2026-11-10", 0, 50, time.UTC)
		if err != nil || day.Total != 1 || len(day.Encounters) != 1 {
			t.Fatalf("day = %+v (%v)", day, err)
		}
		summary := day.Encounters[0]
		if summary.State != "finished" || !summary.Complete || summary.DPS.Status != capture.DPSStatusAvailable || summary.DPS.Value != 50 ||
			summary.DurationSeconds == nil || *summary.DurationSeconds != 20 || !summary.Scene.Known || summary.Scene.InstanceID != 2117 || summary.GameVersion != capture.CurrentGameVersion() ||
			summary.MainEnemy == nil || summary.MainEnemy.MonsterID != 340000040 || summary.MainEnemy.SameType != 2 || summary.Enemies != 2 || !summary.StartedAt.Equal(start) {
			t.Fatalf("summary = %+v main=%+v", summary, summary.MainEnemy)
		}
		detail, err := store.Encounter(ctx, summary.ID, time.UTC)
		if err != nil || detail.Snapshot == nil || detail.Timeline == nil {
			t.Fatalf("detail = %+v (%v)", detail, err)
		}
		expected := capture.ApplyEncounterIntegrity(want.Snapshot, want.Snapshot.Integrity)
		got := *detail.Snapshot
		got.EncounterID, expected.EncounterID = "", ""
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("stored snapshot differs:\n got %+v\nwant %+v", got, expected)
		}
		if !reflect.DeepEqual(*detail.Timeline, want.Timeline) {
			t.Fatalf("stored timeline = %+v, want %+v", *detail.Timeline, want.Timeline)
		}
		if detail.Session.Source != SourceLive || detail.Session.Status != "closed" || detail.Session.AppVersion != "0.7.0" || detail.Session.AppBuild != 0 || detail.Session.AppCommit != testBuild.Commit {
			t.Fatalf("session = %+v", detail.Session)
		}
	}
	check(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path, nil)
	if status := reopened.Status(); status.RecoveredSessions != 0 || status.RecoveredEncounters != 0 {
		t.Fatalf("a closed session was recovered: %+v", status)
	}
	check(reopened)
	assertIntegrity(t, reopened)
}

func TestLaterIntegrityChangesTheShownDPS(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	entries := finishedEncounter(1, 1, start, 10, 500, capture.IntegrityIntact)
	degraded := capture.DataIntegrity{Status: capture.IntegrityDegraded, Risks: []string{"combat_results_possibly_lost", "server_effects_possibly_lost"}, TransportLosses: 1, FirstLossAt: start.Add(2 * time.Second)}
	entries = append(entries, capture.HistoryEntry{Kind: capture.HistoryEncounterIntegrity, Encounter: "1-1", Integrity: &degraded})
	record(t, store, liveStart, entries)
	day, err := store.Day(context.Background(), "2026-03-01", 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	if got := day.Encounters[0]; got.Integrity != "DEGRADED" || got.DPS.Status != capture.DPSStatusLowerBound || got.DPS.Value != 50 {
		t.Fatalf("summary after the late loss = %+v", got)
	}
	detail, err := store.Encounter(context.Background(), day.Encounters[0].ID, time.UTC)
	if err != nil || detail.Snapshot.Integrity.Status != capture.IntegrityDegraded || len(detail.Snapshot.Integrity.Risks) != 2 ||
		detail.Snapshot.EncounterDPS.Status != capture.DPSStatusLowerBound || detail.Snapshot.Healing.Status != capture.DPSStatusLowerBound {
		t.Fatalf("detail after the late loss = %+v (%v)", detail.Snapshot, err)
	}
}

func TestDiscardedEncounterLeavesItsEventsOutside(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	at := time.Date(2026, 5, 5, 5, 5, 5, 0, time.UTC)
	event := &capture.CombatEventRecord{Kind: capture.CombatEventDamage, Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: 99}, ObservedAt: at, Evidence: "damage_rpc_response",
		Source: capture.CombatEndpoint{EntityID: 5, Relation: "UNKNOWN"}, Target: capture.CombatEndpoint{EntityID: 6, Relation: "UNRELATED"},
		Flow: capture.CombatFlowUnattributedOutgoing, Confidence: capture.AttributionUnattributed, Amount: 30}
	recorder := record(t, store, liveStart, []capture.HistoryEntry{
		{Kind: capture.HistoryEncounterOpened, Encounter: "1-1", Opened: &capture.HistoryEncounterOpening{ProtocolSession: 1, StatisticsInterval: 1, Sequence: 1, StartedAt: at, StartBasis: "battle_state_enter"}},
		{Kind: capture.HistoryEventAccepted, Sequence: 1, Event: event},
		{Kind: capture.HistoryEventAssigned, Sequence: 1, Encounter: "1-1"},
	}, []capture.HistoryEntry{{Kind: capture.HistoryEncounterDiscarded, Encounter: "1-1"}})
	var encounters, events int
	var assigned sql.NullInt64
	ctx := context.Background()
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM encounter").Scan(&encounters); err != nil || encounters != 0 {
		t.Fatalf("encounters = %d (%v)", encounters, err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT count(*), max(encounter_id) FROM combat_event WHERE capture_session_id = ?", recorder.SessionID()).Scan(&events, &assigned); err != nil || events != 1 || assigned.Valid {
		t.Fatalf("events = %d assigned = %v (%v)", events, assigned, err)
	}
}

func TestRepeatedResponseIsStoredOnce(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	at := time.Date(2026, 5, 5, 5, 5, 5, 0, time.UTC)
	event := func(sequence uint64) capture.HistoryEntry {
		return capture.HistoryEntry{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: &capture.CombatEventRecord{Kind: capture.CombatEventDamage,
			Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: 4242}, ObservedAt: at, Evidence: "damage_rpc_response", Flow: capture.CombatFlowLocalOutgoing,
			Confidence: capture.AttributionConfirmed, Amount: 10, Source: capture.CombatEndpoint{EntityID: 1, Relation: "LOCAL"}, Target: capture.CombatEndpoint{EntityID: 2, Relation: "UNRELATED"}}}
	}
	recorder := record(t, store, liveStart, []capture.HistoryEntry{event(1)}, []capture.HistoryEntry{event(2)})
	var events int
	if err := store.db.QueryRowContext(context.Background(), "SELECT count(*) FROM combat_event WHERE capture_session_id = ?", recorder.SessionID()).Scan(&events); err != nil || events != 1 {
		t.Fatalf("stored %d copies of one response (%v)", events, err)
	}
}

func TestAbnormalExitLeavesNoFinishedLookingEncounter(t *testing.T) {
	path := testPath(t)
	store := openTestStore(t, path, nil)
	start := time.Date(2026, 7, 1, 23, 0, 0, 0, time.UTC)
	recorder := begin(t, store, liveStart)
	opened := finishedEncounter(1, 1, start, 5, 100, capture.IntegrityIntact)[:2]
	pending := capture.HistoryEntry{Kind: capture.HistoryEncounterOpened, Encounter: "1-2", Opened: &capture.HistoryEncounterOpening{ProtocolSession: 1, StatisticsInterval: 1, Sequence: 2, StartedAt: start.Add(time.Minute), StartBasis: "battle_state_enter"}}
	recorder.Submit(append(opened, pending), 0)
	// Let the writer store the batch, then end the process the hard way: the file is released without Finish.
	deadline := time.Now().Add(5 * time.Second)
	for recorder.queued.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(writeLinger + 100*time.Millisecond)
	if err := store.closeHandles(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path, nil)
	status := reopened.Status()
	if status.State != StateReady || status.RecoveredSessions != 1 || status.RecoveredEncounters != 1 {
		t.Fatalf("status after the abnormal exit = %+v", status)
	}
	day, err := reopened.Day(context.Background(), "2026-07-01", 0, 10, time.UTC)
	if err != nil || day.Total != 1 {
		t.Fatalf("day = %+v (%v)", day, err)
	}
	got := day.Encounters[0]
	if got.State != "abandoned" || got.Complete || got.DPS.Status != capture.DPSStatusUnavailable || got.DurationSeconds != nil || got.Enemies != 0 {
		t.Fatalf("abandoned encounter = %+v", got)
	}
	var sessionStatus string
	if err := reopened.db.QueryRowContext(context.Background(), "SELECT status FROM capture_session").Scan(&sessionStatus); err != nil || sessionStatus != "abandoned" {
		t.Fatalf("session status = %q (%v)", sessionStatus, err)
	}
}

func TestFullQueueIsCountedAndMarksTheEncounterIncomplete(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	entries := finishedEncounter(1, 1, start, 10, 300, capture.IntegrityIntact)
	recorder := begin(t, store, liveStart)
	recorder.Submit(entries[:2], 0)
	// One drain larger than the queue bound cannot be kept: it is counted, never silently dropped.
	flood := make([]capture.HistoryEntry, maxQueuedEntries+1)
	for i := range flood {
		flood[i] = capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: uint64(i + 1), Encounter: "1-1"}
	}
	recorder.Submit(flood, 0)
	recorder.Submit(entries[2:], 0)
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	var queueDropped int
	if err := store.db.QueryRowContext(context.Background(), "SELECT queue_dropped FROM capture_session WHERE id = ?", recorder.SessionID()).Scan(&queueDropped); err != nil || queueDropped != len(flood) {
		t.Fatalf("queue_dropped = %d (%v)", queueDropped, err)
	}
	day, err := store.Day(context.Background(), "2026-08-08", 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 || day.Encounters[0].Complete || day.Encounters[0].State != "finished" {
		t.Fatalf("day = %+v (%v)", day, err)
	}
}

func TestJournalBoundMarksEverySessionEncounterIncomplete(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	recorder := begin(t, store, liveStart)
	recorder.Submit(finishedEncounter(1, 1, time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC), 10, 300, capture.IntegrityIntact), 3)
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	var dropped, complete int
	if err := store.db.QueryRowContext(context.Background(), "SELECT s.journal_dropped, e.complete FROM capture_session s JOIN encounter e ON e.capture_session_id = s.id").Scan(&dropped, &complete); err != nil || dropped != 3 || complete != 0 {
		t.Fatalf("journal_dropped = %d complete = %d (%v)", dropped, complete, err)
	}
}

func TestReplayedFileIsStoredOnce(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	digest := sha256.Sum256([]byte("capture"))
	start := SessionStart{Source: SourceReplay, SourceSHA256: hex.EncodeToString(digest[:])}
	record(t, store, start, finishedEncounter(1, 1, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 10, 100, capture.IntegrityIntact))
	if _, err := store.BeginSession(start); !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("second replay of the same file: %v", err)
	}
	if _, err := store.BeginSession(liveStart); err != nil {
		t.Fatalf("a live session is never a duplicate: %v", err)
	}
}

func TestRecordedDateFollowsTheStartWhereItWasRecorded(t *testing.T) {
	tokyo := time.FixedZone("UTC+9", 9*60*60)
	newYork := time.FixedZone("UTC-5", -5*60*60)
	// 15:30 UTC is 00:30 the next day in UTC+9 and 10:30 the same day in UTC-5. The recorded date and offset are those of the
	// host that recorded it; a view in UTC still lists the encounter on its UTC date.
	start := time.Date(2024, 2, 28, 15, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		location *time.Location
		date     string
		offset   int
	}{{tokyo, "2024-02-29", 9 * 60 * 60}, {newYork, "2024-02-28", -5 * 60 * 60}, {time.UTC, "2024-02-28", 0}} {
		store := openTestStore(t, testPath(t), test.location)
		record(t, store, liveStart, finishedEncounter(1, 1, start, 45*60, 900, capture.IntegrityIntact))
		day, err := store.Day(context.Background(), "2024-02-28", 0, 10, time.UTC)
		if err != nil || len(day.Encounters) != 1 {
			t.Fatalf("%s: day = %+v (%v)", test.location, day, err)
		}
		summary := day.Encounters[0]
		if summary.Recorded.Date != test.date || summary.Recorded.OffsetSeconds != test.offset || summary.LocalDate != "2024-02-28" || summary.Start.Clock != "15:30:00" {
			t.Fatalf("%s: summary = %+v", test.location, summary)
		}
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return zone
}

// zoneDates reads where every encounter of the history is listed in zone: the heatmap of each year with an encounter, and
// each date's list, which must hold exactly the encounters the heatmap counted there.
func zoneDates(t *testing.T, store *Store, zone *time.Location) map[int64]EncounterSummary {
	t.Helper()
	ctx := context.Background()
	first, err := store.Year(ctx, 0, zone)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[int64]EncounterSummary{}
	for year := first.FirstYear; year != 0 && year <= first.LastYear; year++ {
		view, err := store.Year(ctx, year, zone)
		if err != nil {
			t.Fatal(err)
		}
		sum := 0
		for _, day := range view.Days {
			page, err := store.Day(ctx, day.Date, 0, MaxDayPage, zone)
			if err != nil || page.Total != day.Encounters || len(page.Encounters) != day.Encounters {
				t.Fatalf("%s %s: the heatmap counts %d, the day lists %d of %d (%v)", zone, day.Date, day.Encounters, len(page.Encounters), page.Total, err)
			}
			for _, summary := range page.Encounters {
				if summary.LocalDate != day.Date || summary.Start.Date != day.Date || !strings.HasPrefix(day.Date, strconv.Itoa(year)) {
					t.Fatalf("%s %s lists an encounter of %s", zone, day.Date, summary.LocalDate)
				}
				if _, again := listed[summary.ID]; again {
					t.Fatalf("%s: encounter %d is listed twice", zone, summary.ID)
				}
				detail, err := store.Encounter(ctx, summary.ID, zone)
				if err != nil || detail.Summary.LocalDate != day.Date || detail.Summary.Start != summary.Start {
					t.Fatalf("%s: the detail of %d says %+v (%v)", zone, summary.ID, detail.Summary.Start, err)
				}
				listed[summary.ID] = summary
			}
			sum += day.Encounters
		}
		if sum != view.Total {
			t.Fatalf("%s %d: days add up to %d, total %d", zone, year, sum, view.Total)
		}
	}
	return listed
}

// TestDatesFollowTheViewZoneNotTheRecordingZone records encounters in Tokyo and reads them in other zones: each zone lists
// every encounter exactly once, on the date its UTC start has there, across midnight, a year end, a leap day and both DST
// changes of Los Angeles, and reading never changes a stored instant.
func TestDatesFollowTheViewZoneNotTheRecordingZone(t *testing.T) {
	tokyo, losAngeles, london := mustZone(t, "Asia/Tokyo"), mustZone(t, "America/Los_Angeles"), mustZone(t, "Europe/London")
	path := testPath(t)
	store := openTestStore(t, path, tokyo)
	starts := []time.Time{
		time.Date(2026, 11, 3, 18, 30, 0, 0, time.UTC), // Tokyo 11-04 03:30, Los Angeles 11-03 10:30
		time.Date(2024, 2, 28, 15, 30, 0, 0, time.UTC), // Tokyo 02-29 00:30 (leap day), Los Angeles 02-28 07:30
		time.Date(2024, 2, 29, 23, 0, 0, 0, time.UTC),  // UTC 02-29 23:00, Tokyo 03-01 08:00, Los Angeles 02-29 15:00
		time.Date(2025, 12, 31, 15, 0, 0, 0, time.UTC), // Tokyo 2026-01-01 00:00, Los Angeles 2025-12-31 07:00, UTC 2025-12-31
		time.Date(2026, 3, 8, 9, 59, 59, 0, time.UTC),  // Los Angeles 01:59:59 PST, one second before the spring change
		time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),   // Los Angeles 03:00:00 PDT
		time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC),  // Los Angeles 01:30 PDT, the first of the repeated hour
		time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC),  // Los Angeles 01:30 PST, the second
		time.Date(2026, 11, 2, 7, 59, 0, 0, time.UTC),  // Los Angeles 11-01 23:59 PST, the last minute of a 25-hour day
	}
	var entries []capture.HistoryEntry
	for i, start := range starts {
		entries = append(entries, finishedEncounter(1, uint64(i+1), start, 20*60, int64(1000+i), capture.IntegrityIntact)...)
	}
	record(t, store, liveStart, entries)
	stored := func() string {
		rows, err := store.db.Query("SELECT id, started_at_ns, ended_at_ns, local_date, utc_offset_s FROM encounter ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		hash := sha256.New()
		for rows.Next() {
			var id, started, ended, offset int64
			var date string
			if err := rows.Scan(&id, &started, &ended, &date, &offset); err != nil {
				t.Fatal(err)
			}
			_, _ = hash.Write([]byte(strconv.FormatInt(id, 10) + "|" + strconv.FormatInt(started, 10) + "|" + strconv.FormatInt(ended, 10) + "|" + date + "|" + strconv.FormatInt(offset, 10) + "\n"))
		}
		return hex.EncodeToString(hash.Sum(nil))
	}
	before := stored()
	want := map[*time.Location][]string{
		tokyo:      {"2026-11-04 03:30:00", "2024-02-29 00:30:00", "2024-03-01 08:00:00", "2026-01-01 00:00:00", "2026-03-08 18:59:59", "2026-03-08 19:00:00", "2026-11-01 17:30:00", "2026-11-01 18:30:00", "2026-11-02 16:59:00"},
		losAngeles: {"2026-11-03 10:30:00", "2024-02-28 07:30:00", "2024-02-29 15:00:00", "2025-12-31 07:00:00", "2026-03-08 01:59:59", "2026-03-08 03:00:00", "2026-11-01 01:30:00", "2026-11-01 01:30:00", "2026-11-01 23:59:00"},
		time.UTC:   {"2026-11-03 18:30:00", "2024-02-28 15:30:00", "2024-02-29 23:00:00", "2025-12-31 15:00:00", "2026-03-08 09:59:59", "2026-03-08 10:00:00", "2026-11-01 08:30:00", "2026-11-01 09:30:00", "2026-11-02 07:59:00"},
		london:     {"2026-11-03 18:30:00", "2024-02-28 15:30:00", "2024-02-29 23:00:00", "2025-12-31 15:00:00", "2026-03-08 09:59:59", "2026-03-08 10:00:00", "2026-11-01 08:30:00", "2026-11-01 09:30:00", "2026-11-02 07:59:00"},
	}
	wantOffsets := map[*time.Location][]int{
		losAngeles: {-8 * 3600, -8 * 3600, -8 * 3600, -8 * 3600, -8 * 3600, -7 * 3600, -7 * 3600, -8 * 3600, -8 * 3600},
	}
	for zone, local := range want {
		listed := zoneDates(t, store, zone)
		if len(listed) != len(starts) {
			t.Fatalf("%s lists %d of %d encounters", zone, len(listed), len(starts))
		}
		byStart := map[int64]EncounterSummary{}
		for _, summary := range listed {
			byStart[summary.StartedAt.UnixNano()] = summary
		}
		for i, start := range starts {
			summary, ok := byStart[start.UnixNano()]
			if !ok {
				t.Fatalf("%s does not list the encounter of %s", zone, start)
			}
			if got := summary.Start.Date + " " + summary.Start.Clock; got != local[i] {
				t.Errorf("%s: %s is shown at %s, want %s", zone, start, got, local[i])
			}
			if offsets := wantOffsets[zone]; offsets != nil && summary.Start.OffsetSeconds != offsets[i] {
				t.Errorf("%s: %s has offset %d, want %d", zone, start, summary.Start.OffsetSeconds, offsets[i])
			}
			if summary.Recorded.OffsetSeconds != 9*3600 || summary.End == nil || summary.EndedAt.Sub(summary.StartedAt) != 20*time.Minute {
				t.Errorf("%s: %s recorded %+v, end %+v", zone, start, summary.Recorded, summary.End)
			}
		}
	}
	// The repeated hour of the autumn change keeps both encounters on 2026-11-01 in Los Angeles, 25 hours long.
	if day, err := store.Day(context.Background(), "2026-11-01", 0, MaxDayPage, losAngeles); err != nil || day.Total != 3 {
		t.Fatalf("the 25-hour day lists %d (%v)", day.Total, err)
	}
	// The year 2025 of Tokyo has nothing: its last encounter starts on 2026-01-01 there.
	if year, err := store.Year(context.Background(), 2025, tokyo); err != nil || year.Total != 0 {
		t.Fatalf("Tokyo 2025 = %+v (%v)", year, err)
	}
	if year, err := store.Year(context.Background(), 2025, losAngeles); err != nil || year.Total != 1 || year.Days[0].Date != "2025-12-31" {
		t.Fatalf("Los Angeles 2025 = %+v (%v)", year, err)
	}
	if after := stored(); after != before {
		t.Fatal("reading the history in other zones changed stored instants")
	}
}

// TestFarthestZonesStillMeet reads encounters recorded at UTC+14 in UTC-12 and the other way round: the recorded date of
// each is two calendar days away from where the view lists it, which the query's recorded-date margin must still reach.
func TestFarthestZonesStillMeet(t *testing.T) {
	kiritimati, bakerIsland := mustZone(t, "Pacific/Kiritimati"), mustZone(t, "Etc/GMT+12")
	for _, test := range []struct{ recorded, viewed *time.Location }{{kiritimati, bakerIsland}, {bakerIsland, kiritimati}} {
		store := openTestStore(t, testPath(t), test.recorded)
		// 10:30 UTC is 00:30 the next day at UTC+14 and 22:30 the day before at UTC-12.
		start := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
		record(t, store, liveStart, finishedEncounter(1, 1, start, 60, 500, capture.IntegrityIntact), finishedEncounter(1, 2, start.Add(-23*time.Hour), 60, 600, capture.IntegrityIntact))
		listed := zoneDates(t, store, test.viewed)
		if len(listed) != 2 {
			t.Fatalf("recorded in %s, viewed in %s: %d listed", test.recorded, test.viewed, len(listed))
		}
		for _, summary := range listed {
			if want := summary.StartedAt.In(test.viewed).Format(dateLayout); summary.LocalDate != want {
				t.Fatalf("listed on %s, want %s", summary.LocalDate, want)
			}
		}
	}
}

// TestZoneDaysFollowTheirOwnLength reads a zone whose clocks skip midnight: on the first day of Chilean summer time 00:00
// to 01:00 never happens, so the day starts at 01:00 and is 23 hours long, and the day before is complete.
func TestZoneDaysFollowTheirOwnLength(t *testing.T) {
	santiago := mustZone(t, "America/Santiago")
	store := openTestStore(t, testPath(t), nil)
	// 2026-09-06 00:00 -04 does not exist: clocks move to 01:00 -03 (03:59:59 UTC is still 23:59:59 on 09-05).
	before, after := time.Date(2026, 9, 6, 3, 59, 59, 0, time.UTC), time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	record(t, store, liveStart, finishedEncounter(1, 1, before, 60, 500, capture.IntegrityIntact), finishedEncounter(1, 2, after, 60, 600, capture.IntegrityIntact))
	ctx := context.Background()
	first, err := store.Day(ctx, "2026-09-05", 0, MaxDayPage, santiago)
	if err != nil || first.Total != 1 || first.Encounters[0].Start.Clock != "23:59:59" {
		t.Fatalf("2026-09-05 = %+v (%v)", first, err)
	}
	second, err := store.Day(ctx, "2026-09-06", 0, MaxDayPage, santiago)
	if err != nil || second.Total != 1 || second.Encounters[0].Start.Clock != "01:00:00" || second.Encounters[0].Start.OffsetSeconds != -3*3600 {
		t.Fatalf("2026-09-06 = %+v (%v)", second, err)
	}
}

func TestViewZonesResolveByIANANameAndFallBackToLocal(t *testing.T) {
	for _, test := range []struct {
		name     string
		id       string
		fallback bool
	}{
		{"Asia/Tokyo", "Asia/Tokyo", false}, {"UTC", "UTC", false}, {"America/Los_Angeles", "America/Los_Angeles", false},
		{"Local", LocalZoneID, false}, {"", LocalZoneID, true}, {"Not/AZone", LocalZoneID, true}, {"../../etc/passwd", LocalZoneID, true},
		{strings.Repeat("A", maxZoneName+1), LocalZoneID, true},
	} {
		zone := ResolveViewZone(test.name)
		if zone.ID != test.id || zone.Fallback != test.fallback || zone.Location == nil {
			t.Errorf("%q resolved to %+v", test.name, zone)
		}
		if test.fallback && zone.Location != time.Local {
			t.Errorf("%q fell back to %s, not Local", test.name, zone.Location)
		}
	}
	if ResolveViewZone("Asia/Tokyo").Location != ResolveViewZone("Asia/Tokyo").Location {
		t.Error("a loaded zone is not reused")
	}
}

func TestYearZeroIsTheCurrentYearOfTheZone(t *testing.T) {
	now := time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC) // 2027-01-01 05:00 in Tokyo
	store, err := Open(Options{Path: testPath(t), Location: time.UTC, Now: func() time.Time { return now }, Build: &testBuild})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if view, err := store.Year(context.Background(), 0, mustZone(t, "Asia/Tokyo")); err != nil || view.Year != 2027 || view.Today != "2027-01-01" {
		t.Fatalf("Tokyo = %d %s (%v)", view.Year, view.Today, err)
	}
	if view, err := store.Year(context.Background(), 0, time.UTC); err != nil || view.Year != 2026 || view.Today != "2026-12-31" {
		t.Fatalf("UTC = %d %s (%v)", view.Year, view.Today, err)
	}
}

func TestBusyDayIsPagedInStartOrder(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var entries []capture.HistoryEntry
	const count = MaxDayPage + 37
	for i := count; i >= 1; i-- {
		// Journal order is not start order: the list must still be ordered by start.
		entries = append(entries, finishedEncounter(1, uint64(i), base.Add(time.Duration(i)*time.Minute), 30, int64(100+i), capture.IntegrityIntact)...)
	}
	record(t, store, liveStart, entries)
	ctx := context.Background()
	year, err := store.Year(ctx, 2026, time.UTC)
	if err != nil || year.Total != count {
		t.Fatalf("year total = %d (%v)", year.Total, err)
	}
	first, err := store.Day(ctx, "2026-06-01", 0, 0, time.UTC)
	if err != nil || first.Total != count || len(first.Encounters) != MaxDayPage {
		t.Fatalf("first page = %d of %d (%v)", len(first.Encounters), first.Total, err)
	}
	second, err := store.Day(ctx, "2026-06-01", MaxDayPage, 0, time.UTC)
	if err != nil || len(second.Encounters) != count-MaxDayPage {
		t.Fatalf("second page = %d (%v)", len(second.Encounters), err)
	}
	all := append(first.Encounters, second.Encounters...)
	for i := 1; i < len(all); i++ {
		if !all[i].StartedAt.After(all[i-1].StartedAt) {
			t.Fatalf("row %d starts %s, not after %s", i, all[i].StartedAt, all[i-1].StartedAt)
		}
	}
	if empty, err := store.Day(ctx, "2026-06-02", 0, 0, time.UTC); err != nil || empty.Total != 0 || len(empty.Encounters) != 0 {
		t.Fatalf("an empty date = %+v (%v)", empty, err)
	}
	if _, err := store.Day(ctx, "2026-02-30", 0, 0, time.UTC); err == nil {
		t.Fatal("an impossible date was accepted")
	}
}

func TestEmptyHistoryHasNoYears(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	year, err := store.Year(context.Background(), 2026, time.UTC)
	if err != nil || year.Total != 0 || len(year.Days) != 0 || year.FirstYear != 0 || year.LastYear != 0 {
		t.Fatalf("empty year = %+v (%v)", year, err)
	}
}

func TestNewerSchemaIsNeverWritten(t *testing.T) {
	path := testPath(t)
	store := openTestStore(t, path, nil)
	record(t, store, liveStart, finishedEncounter(1, 1, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), 10, 100, capture.IntegrityIntact))
	if _, err := store.db.Exec("PRAGMA user_version=" + strconv.Itoa(schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := fileDigest(t, path)
	newer, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if err == nil {
		t.Fatal("a newer schema opened without an error")
	}
	if status := newer.Status(); status.State == StateReady || status.Reason != ReasonNewerSchema {
		t.Fatalf("status = %+v", status)
	}
	if _, err := newer.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("a newer schema accepted a session: %v", err)
	}
	if err := newer.Close(); err != nil {
		t.Fatal(err)
	}
	if after := fileDigest(t, path); after != before {
		t.Fatal("the newer file was modified")
	}
}

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestForeignFileIsNotTouched(t *testing.T) {
	path := testPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", plainDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE other (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := fileDigest(t, path)
	store, err := Open(Options{Path: path, Build: &testBuild})
	if err == nil || store.Status().Reason != ReasonNotHistory {
		t.Fatalf("foreign file: %v %+v", err, store.Status())
	}
	store.Close()
	if fileDigest(t, path) != before {
		t.Fatal("a foreign file was modified")
	}
}

// TestUnencryptedHistoryIsNotTouched places a plaintext SQLite file that calls itself a WWlogs history at the history path:
// the history is never stored unencrypted, so such a file is no history of this build and stays exactly as it was.
func TestUnencryptedHistoryIsNotTouched(t *testing.T) {
	path := testPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", plainDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"PRAGMA application_id=" + strconv.Itoa(applicationID), schemaDDL, "PRAGMA user_version=" + strconv.Itoa(schemaVersion)} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	before := fileDigest(t, path)
	store, err := Open(Options{Path: path, Build: &testBuild})
	if err == nil || store.Status().State != StateUnavailable || store.Status().Reason != ReasonNotHistory {
		t.Fatalf("plaintext history: %v %+v", err, store.Status())
	}
	if _, err := store.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("a plaintext file accepted a session: %v", err)
	}
	store.Close()
	if fileDigest(t, path) != before {
		t.Fatal("a plaintext file was modified")
	}
	if _, err := os.Stat(path + ".key"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a key was made for a plaintext file: %v", err)
	}
}

// TestOtherLayoutIsNotTouched opens encrypted files with the history's key that are not this build's layout: another
// application's file, and a WWlogs history of an older layout. Neither is read, migrated or written.
func TestOtherLayoutIsNotTouched(t *testing.T) {
	for name, identity := range map[string][2]int{"another application": {0x57574C48, 3}, "an older layout": {applicationID, 0}} {
		t.Run(name, func(t *testing.T) {
			path := testPath(t)
			store := openTestStore(t, path, nil)
			record(t, store, liveStart, finishedEncounter(1, 1, time.Date(2026, 2, 2, 2, 2, 2, 0, time.UTC), 10, 100, capture.IntegrityIntact))
			if _, err := store.db.Exec("PRAGMA application_id=" + strconv.Itoa(identity[0]) + "; PRAGMA user_version=" + strconv.Itoa(identity[1])); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before := fileDigest(t, path)
			other, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
			if err == nil || other.Status().State != StateUnavailable || other.Status().Reason != ReasonNotHistory {
				t.Fatalf("%s: %v %+v", name, err, other.Status())
			}
			if _, err := other.Year(context.Background(), 2026, time.UTC); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("%s was read: %v", name, err)
			}
			if err := other.Close(); err != nil {
				t.Fatal(err)
			}
			if fileDigest(t, path) != before {
				t.Fatalf("%s was modified", name)
			}
		})
	}
}

func TestSecondProcessOnlyReads(t *testing.T) {
	path := testPath(t)
	first := openTestStore(t, path, nil)
	record(t, first, liveStart, finishedEncounter(1, 1, time.Date(2026, 4, 4, 4, 4, 4, 0, time.UTC), 10, 100, capture.IntegrityIntact))
	second, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if !errors.Is(err, errLocked) {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	if status := second.Status(); status.State != StateReadOnly || status.Reason != ReasonLocked {
		t.Fatalf("second status = %+v", status)
	}
	if _, err := second.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("the second process recorded: %v", err)
	}
	if year, err := second.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("the second process cannot read: %+v (%v)", year, err)
	}
}

func TestLostActivityKeepsAPendingEncounterAsUnfinished(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)
	entries := finishedEncounter(1, 1, start, 10, 300, capture.IntegrityIntact)
	recorder := begin(t, store, liveStart)
	recorder.Submit(entries[:1], 0)
	// The activity and the end of the fight are lost in one refused drain.
	lost := append(make([]capture.HistoryEntry, maxQueuedEntries), entries[1:]...)
	for i := 0; i < maxQueuedEntries; i++ {
		lost[i] = capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: uint64(i + 1), Encounter: "1-1"}
	}
	recorder.Submit(lost, 0)
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	day, err := store.Day(context.Background(), "2026-09-09", 0, 10, time.UTC)
	if err != nil || day.Total != 1 || day.Encounters[0].State != "abandoned" || day.Encounters[0].Complete {
		t.Fatalf("an encounter whose entries were lost was removed or shown complete: %+v (%v)", day, err)
	}
}

func TestAssignmentOfALostResultMarksTheEncounterIncomplete(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	entries := finishedEncounter(1, 1, start, 10, 300, capture.IntegrityIntact)
	// The engine accepted result 1 and assigned it to the encounter, but the accepted entry never reached storage.
	assigned := capture.HistoryEntry{Kind: capture.HistoryEventAssigned, Sequence: 1, Encounter: "1-1"}
	record(t, store, liveStart, []capture.HistoryEntry{entries[0], assigned, entries[1], entries[2]})
	day, err := store.Day(context.Background(), "2026-09-10", 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 || day.Encounters[0].Complete {
		t.Fatalf("day = %+v (%v)", day, err)
	}
}

// Two distinct results the engine accepted under one key (a RequestId wrap the counter could not see, for example) cannot
// both be stored under the history's unique key. The second is not stored, and its assignment marks the encounter
// incomplete, so a record with a missing result never reads as complete.
func TestResultsSharingOneKeyLeaveTheEncounterIncomplete(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	start := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	entries := finishedEncounter(1, 1, start, 10, 300, capture.IntegrityIntact)
	result := func(sequence uint64, amount int64) []capture.HistoryEntry {
		event := &capture.CombatEventRecord{Kind: capture.CombatEventDamage, Key: capture.CombatEventKey{ProtocolSession: 1, RPCID: 32700}, ObservedAt: start.Add(time.Second),
			Evidence: "damage_rpc_response", Flow: capture.CombatFlowLocalOutgoing, Confidence: capture.AttributionConfirmed, Amount: amount,
			Source: capture.CombatEndpoint{EntityID: 1, Relation: "LOCAL"}, Target: capture.CombatEndpoint{EntityID: 2, Relation: "UNRELATED"}}
		return []capture.HistoryEntry{{Kind: capture.HistoryEventAccepted, Sequence: sequence, Event: event}, {Kind: capture.HistoryEventAssigned, Sequence: sequence, Encounter: "1-1"}}
	}
	batch := append([]capture.HistoryEntry{entries[0], entries[1]}, result(1, 140)...)
	batch = append(batch, result(2, 160)...)
	recorder := record(t, store, liveStart, append(batch, entries[2]))
	var stored int
	if err := store.db.QueryRowContext(context.Background(), "SELECT count(*) FROM combat_event WHERE capture_session_id = ?", recorder.SessionID()).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored %d results under one key (%v)", stored, err)
	}
	day, err := store.Day(context.Background(), "2026-09-11", 0, 10, time.UTC)
	if err != nil || len(day.Encounters) != 1 || day.Encounters[0].Complete || day.Encounters[0].IncompleteReason != incompleteEntriesLost {
		t.Fatalf("day = %+v (%v)", day, err)
	}
}

// inSession moves a test encounter into protocol session number.
func inSession(entries []capture.HistoryEntry, number int) []capture.HistoryEntry {
	entries[0].Opened.ProtocolSession = number
	entries[2].Finished.Snapshot.ProtocolSession = number
	return entries
}

func protocolEntry(number int, version string) capture.HistoryEntry {
	return capture.HistoryEntry{Kind: capture.HistoryProtocolConfirmed, Protocol: &capture.HistoryProtocolSession{Number: number, GameVersion: version}}
}

// TestEachEncounterKeepsTheGameVersionOfItsProtocolSession records two protocol sessions of one capture, as a relogin after a
// game update would: every encounter shows the major.minor its own session reported, never another session's, and the
// stored version is exactly what the journal named (no patch number, no guess).
func TestEachEncounterKeepsTheGameVersionOfItsProtocolSession(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	day := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	record(t, store, liveStart,
		finishedEncounter(1, 1, day, 10, 100, capture.IntegrityIntact),
		[]capture.HistoryEntry{protocolEntry(2, "9.1")},
		inSession(finishedEncounter(2, 1, day.Add(time.Hour), 10, 200, capture.IntegrityIntact), 2))
	ctx := context.Background()
	list, err := store.Day(ctx, "2026-11-13", 0, 10, time.UTC)
	if err != nil || len(list.Encounters) != 2 {
		t.Fatalf("day = %+v (%v)", list, err)
	}
	if first, second := list.Encounters[0], list.Encounters[1]; first.GameVersion != capture.CurrentGameVersion() || second.GameVersion != "9.1" {
		t.Fatalf("game versions = %q, %q", first.GameVersion, second.GameVersion)
	}
	detail, err := store.Encounter(ctx, list.Encounters[1].ID, time.UTC)
	if err != nil || detail.Summary.GameVersion != "9.1" {
		t.Fatalf("detail game version = %q (%v)", detail.Summary.GameVersion, err)
	}
	var sessions int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM protocol_session").Scan(&sessions); err != nil || sessions != 2 {
		t.Fatalf("%d protocol sessions (%v)", sessions, err)
	}
	assertIntegrity(t, store)
}

// TestNothingIsStoredUnderAnUnnamedOrRenamedProtocolSession hands the writer an encounter of a protocol session the span
// never named, and a second, different version for a named one. Neither is stored under a guessed version: their
// transactions fail, what they held is counted lost, and the refused announcement is not carried again, so the writer goes
// on; the span that lost it shows no encounter as complete.
func TestNothingIsStoredUnderAnUnnamedOrRenamedProtocolSession(t *testing.T) {
	store := openTestStore(t, testPath(t), nil)
	day := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	recorder := begin(t, store, liveStart)
	recorder.Submit(inSession(finishedEncounter(3, 1, day, 10, 100, capture.IntegrityIntact), 3), 0)
	waitQueued(t, recorder)
	recorder.Submit([]capture.HistoryEntry{protocolEntry(1, "9.9")}, 0)
	waitQueued(t, recorder)
	if health := recorder.Health(); health.Lost != 4 || health.Stopped {
		t.Fatalf("health = %+v", health)
	}
	recorder.Submit(finishedEncounter(1, 1, day.Add(time.Hour), 10, 200, capture.IntegrityIntact), 0)
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	var version string
	var encounters, complete int
	if err := store.db.QueryRow("SELECT p.game_version, count(*), sum(e.complete) FROM encounter e JOIN protocol_session p ON p.capture_session_id = e.capture_session_id AND p.protocol_session = e.protocol_session").
		Scan(&version, &encounters, &complete); err != nil || encounters != 1 || version != capture.CurrentGameVersion() || complete != 0 {
		t.Fatalf("stored %d encounters at %q, %d complete (%v)", encounters, version, complete, err)
	}
	assertIntegrity(t, store)
}

// TestAnalyserIdentityIsStoredWithoutGuessing records spans with an official, an unofficial and an unidentified build: the
// product version is MAJOR.MINOR.PATCH, the Build Number only an official one, and what a build did not carry stays NULL.
func TestAnalyserIdentityIsStoredWithoutGuessing(t *testing.T) {
	for name, test := range map[string]struct {
		build           buildinfo.Identity
		version, commit sql.NullString
		number          sql.NullInt64
	}{
		"official": {buildinfo.Identity{Version: "v0.7.0", BuildNumber: 31, Commit: testBuild.Commit, Dirty: "false", Profile: buildinfo.ProfilePublic},
			sql.NullString{String: "0.7.0", Valid: true}, sql.NullString{String: testBuild.Commit, Valid: true}, sql.NullInt64{Int64: 31, Valid: true}},
		"unofficial": {buildinfo.Identity{Version: "v0.7.0", Commit: testBuild.Commit, Dirty: "true", Profile: buildinfo.ProfileDevelopment},
			sql.NullString{String: "0.7.0", Valid: true}, sql.NullString{String: testBuild.Commit, Valid: true}, sql.NullInt64{}},
		"unidentified": {buildinfo.Identity{Version: buildinfo.Unavailable, Commit: buildinfo.Unavailable, Dirty: "unknown", Profile: buildinfo.ProfileDevelopment},
			sql.NullString{}, sql.NullString{}, sql.NullInt64{}},
	} {
		t.Run(name, func(t *testing.T) {
			build := test.build
			store, err := Open(Options{Path: testPath(t), Location: time.UTC, Build: &build, MasterSHA256: "test-master"})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			record(t, store, liveStart, finishedEncounter(1, 1, time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC), 10, 100, capture.IntegrityIntact))
			var version, commit sql.NullString
			var number sql.NullInt64
			var profile string
			if err := store.db.QueryRow("SELECT app_version, app_build, app_commit, app_profile FROM capture_session").Scan(&version, &number, &commit, &profile); err != nil {
				t.Fatal(err)
			}
			if version != test.version || number != test.number || commit != test.commit || profile != string(build.Profile) {
				t.Fatalf("stored %v %v %v %s", version, number, commit, profile)
			}
		})
	}
}
