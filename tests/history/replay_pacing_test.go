package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// TestHistoryReplayPacingCollector replays every listed capture at every listed speed, each into a fresh Combat History
// file, through the host session path with the recorder's own backpressure, and checks that the speed changes nothing that
// is stored: the stored content (historyContent: every table, keyed by the engine's own keys, without row ids, host clock
// times or session identities) is byte-identical at every speed, and no speed loses a journal entry. A speed of 0 replays as
// fast as possible, so the replay runs exactly as fast as the history stores it.
//
// It needs the embedded key, so it runs in a test binary built by scripts/embed-key.go (-test-binary ./internal/history).
// WWLOGS_HISTORY_PACING_PCAPS lists "name|path" lines, WWLOGS_HISTORY_PACING_FACTORS the speeds ("0,20,1"),
// WWLOGS_HISTORY_PACING_MASTER_DB is the Master, and WWLOGS_HISTORY_PACING_OUT receives the results.
func TestHistoryReplayPacingCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_HISTORY_PACING_OUT")
	if out == "" {
		t.Skip("WWLOGS_HISTORY_PACING_OUT is not set")
	}
	listPath, masterPath, factorList := os.Getenv("WWLOGS_HISTORY_PACING_PCAPS"), os.Getenv("WWLOGS_HISTORY_PACING_MASTER_DB"), os.Getenv("WWLOGS_HISTORY_PACING_FACTORS")
	if listPath == "" || masterPath == "" || factorList == "" {
		t.Fatal("WWLOGS_HISTORY_PACING_PCAPS, WWLOGS_HISTORY_PACING_MASTER_DB and WWLOGS_HISTORY_PACING_FACTORS are required")
	}
	var factors []float64
	for _, field := range strings.Split(factorList, ",") {
		factor, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil || factor < 0 {
			t.Fatalf("pacing %q: %v", field, err)
		}
		factors = append(factors, factor)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	masters, err := capture.LoadMasterData(masterPath, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	defer masters.Close()
	type run struct {
		Pacing           float64 `json:"pacing"`
		Seconds          float64 `json:"seconds"`
		Content          string  `json:"content"`
		Encounters       int     `json:"encounters"`
		Finished         int     `json:"finished"`
		Results          int     `json:"results"`
		EffectChanges    int     `json:"effectChanges"`
		DamageDealt      int64   `json:"damageDealt"`
		Lost             uint64  `json:"lost"`
		CompleteFinished int     `json:"completeFinished"`
	}
	results := map[string][]run{}
	for _, item := range readCaptureList(t, listPath) {
		t.Run(item.name, func(t *testing.T) {
			digest, err := fileDigestHex(item.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, pacing := range factors {
				path := filepath.Join(out, fmt.Sprintf("%s-pacing-%g.db", item.name, pacing))
				for _, leftover := range []string{path, path + "-wal", path + "-shm", keyPath(path), keyRecordPath(path), path + ".lock"} {
					_ = os.Remove(leftover)
				}
				store, err := Open(Options{Path: path, MasterSHA256: masters.SHA256()})
				if err != nil {
					t.Fatal(err)
				}
				recorder, err := store.BeginSession(SessionStart{Source: SourceReplay, SourceSHA256: digest})
				if err != nil {
					t.Fatal(err)
				}
				session, err := capture.OpenSession(capture.SessionConfig{LocalIPv4: "192.168.1.2", ReplayPath: item.path, ReplayPacing: pacing, History: recorder}, masters)
				if err != nil {
					t.Fatal(err)
				}
				began := time.Now()
				result, err := session.Run(context.Background(), nil)
				if err != nil || result.StopReason != capture.StopReasonReplayEnd {
					t.Fatalf("pacing %g: replay ended %q (%v)", pacing, result.StopReason, err)
				}
				if err := recorder.Finish(context.Background(), result.StopReason); err != nil {
					t.Fatalf("pacing %g: %v", pacing, err)
				}
				r := run{Pacing: pacing, Seconds: time.Since(began).Seconds(), Lost: recorder.Health().Lost}
				r.Content = historyContent(t, store)
				ctx := context.Background()
				if err := store.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(state = 'finished'), 0), coalesce(sum(state = 'finished' AND complete = 1), 0),
					coalesce(sum(CASE WHEN state = 'finished' THEN damage_dealt END), 0) FROM encounter`).Scan(&r.Encounters, &r.Finished, &r.CompleteFinished, &r.DamageDealt); err != nil {
					t.Fatal(err)
				}
				r.Results, r.EffectChanges = countRows(t, store, "combat_event"), countRows(t, store, "effect_event")
				assertIntegrity(t, store)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				if r.Lost != 0 || r.CompleteFinished != r.Finished {
					t.Fatalf("pacing %g lost %d entries; %d of %d finished encounters complete", pacing, r.Lost, r.CompleteFinished, r.Finished)
				}
				results[item.name] = append(results[item.name], r)
				t.Logf("pacing %g: %.1f s, %d encounters (%d finished), %d results, %d effect changes, damage %d, content %s", pacing, r.Seconds, r.Encounters, r.Finished,
					r.Results, r.EffectChanges, r.DamageDealt, r.Content[:16])
			}
			for _, r := range results[item.name][1:] {
				if r.Content != results[item.name][0].Content {
					t.Fatalf("pacing %g stored different content than pacing %g", r.Pacing, results[item.name][0].Pacing)
				}
			}
		})
	}
	writeJSON(t, filepath.Join(out, "pacing.json"), results)
}

// historyContent digests everything a history stores about the engine's work, in the engine's own keys: no row id, no host
// clock time and no session identity, which differ between two files holding the same replay.
func historyContent(t *testing.T, store *Store) string {
	t.Helper()
	encounterKey := "(SELECT e.statistics_interval || '-' || e.sequence FROM encounter e WHERE e.id = %s)"
	queries := []string{
		`SELECT source, source_sha256, status, start_reason, stop_reason, journal_dropped, queue_dropped, write_failed, effects_rejected, app_version,
			app_build, app_commit, app_dirty, app_profile, master_sha256 FROM capture_session ORDER BY id`,
		`SELECT protocol_session, game_version FROM protocol_session ORDER BY capture_session_id, protocol_session`,
		`SELECT loss_id, direction, from_ns, to_ns, failed FROM capture_gap ORDER BY capture_session_id, loss_id`,
		`SELECT statistics_interval, sequence, protocol_session, state, outcome, complete, incomplete_reason, checkpoint_at_ns, started_at_ns, start_basis,
			start_confirmed, battle_entered_at_ns,
			ended_at_ns, end_basis, end_decided_at_ns, first_activity_at_ns, last_activity_at_ns, reentries, local_date, utc_offset_s, damage_dealt, damage_taken,
			outgoing_events, incoming_events, unattributed_outgoing_damage, unattributed_incoming_damage, unattributed_outgoing_events, unattributed_incoming_events,
			integrity_status, transport_losses, unrecovered_losses, context_downgraded_events, first_loss_at_ns, dps_status, dps_reason, dps_value, dps_damage,
			dps_duration_ns, dps_start_ns, dps_end_ns, dps_confidence, peak_status, peak_reason, peak_value, peak_damage, peak_duration_ns, peak_start_ns, peak_end_ns,
			peak_confidence, healing_status, healing_reason, healing_effective, healing_reported, healing_overheal, healing_events, healing_unknown_effective,
			healing_unknown_events, unassigned_damage_dealt, unassigned_outgoing_events, unassigned_damage_taken, unassigned_incoming_events, unassigned_overflow_events,
			scene_instance_id, enemies_overflow FROM encounter ORDER BY statistics_interval, sequence`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, risk FROM encounter_risk ORDER BY 1, 2`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, ordinal, kind, player_id, role_id, vehicle_kind, vehicle_evidence, damage_dealt, outgoing_events,
			direct_damage, summon_damage, damage_taken, incoming_events, unassigned_damage, unassigned_events, unassigned_missing_skill_events,
			unassigned_overflow_events, unassigned_damage_ids_incomplete FROM encounter_contributor ORDER BY 1, 2`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, contributor_ordinal, skill_id, damage_dealt, outgoing_events, damage_ids_incomplete
			FROM encounter_skill ORDER BY 1, 2, 3`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, contributor_ordinal, skill_id, kind, value FROM encounter_skill_evidence ORDER BY 1, 2, 3, 4, 5`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, player_id, role_id, effective, reported, events FROM encounter_healer ORDER BY 1, 2, 3`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, anchor_ns, scale_s FROM encounter_timeline ORDER BY 1`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, position, kind, player_id, role_id, vehicle_kind FROM encounter_timeline_contributor ORDER BY 1, 2`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, bucket, position, damage FROM encounter_timeline_bucket ORDER BY 1, 2, 3`,
		`SELECT ` + fmt.Sprintf(encounterKey, "encounter_id") + `, ordinal, monster_id, template_id, identity_conflict, damage_taken, hits_taken, damage_dealt, hits_dealt,
			first_seen_ns, last_seen_ns FROM encounter_enemy ORDER BY 1, 2`,
		`SELECT sequence, ` + fmt.Sprintf(encounterKey, "encounter_id") + `, protocol_session, rpc_id, observed_at_ns, kind, flow, confidence, evidence, amount,
			applied_healing, healing_inconsistent, damage_id, skill_id, source_entity_id, source_generation, source_relation, source_player_id, source_role_id,
			source_role_basis, target_entity_id, target_generation, target_relation, target_player_id, target_role_id, target_role_basis, vehicle_kind,
			context_downgrade, source_monster_id, source_template_id, target_monster_id, target_template_id FROM combat_event ORDER BY capture_session_id, sequence`,
		`SELECT instance, protocol_session, holder_entity_id, holder_generation, effect_id, definition_linked, source_entity_id, holder_relation, source_relation,
			holder_player_id, holder_role_id, holder_role_basis, holder_monster_id, holder_template_id, source_player_id, source_role_id, source_role_basis,
			source_monster_id, source_template_id, origin, first_observed_ns, start_recorded FROM effect_instance ORDER BY capture_session_id, instance`,
		`SELECT instance, sequence, after_combat_sequence, observed_at_ns, change_kind, observation, evidence, reason, stack, duration_s, remaining_s, basis_ns,
			time_reason FROM effect_event ORDER BY capture_session_id, sequence`,
	}
	hash := sha256.New()
	for _, query := range queries {
		rows, err := store.db.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatalf("%v: %s", err, query)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := make([]sql.RawBytes, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				if value == nil {
					hash.Write([]byte{0})
					continue
				}
				fmt.Fprintf(hash, "%d:%s", len(value), value)
			}
			hash.Write([]byte{'\n'})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		hash.Write([]byte("--\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
