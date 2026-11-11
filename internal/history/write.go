package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"wwlogs/internal/capture"
)

// sessionWriter applies journal entries of one session inside one transaction. staged collects encounter row ids created
// (or removed, as 0) in the transaction; the recorder keeps them only once the transaction committed.
type sessionWriter struct {
	tx       *sql.Tx
	recorder *Recorder
	staged   map[string]int64
}

func (w *sessionWriter) apply(ctx context.Context, entry capture.HistoryEntry) error {
	switch entry.Kind {
	case capture.HistoryEventAccepted:
		return w.insertEvent(ctx, entry.Sequence, entry.Event)
	case capture.HistoryEventAssigned:
		id, found, err := w.encounterRow(ctx, entry.Encounter)
		if err != nil || !found {
			return err
		}
		result, err := w.tx.ExecContext(ctx, "UPDATE combat_event SET encounter_id = ? WHERE capture_session_id = ? AND sequence = ?", id, w.recorder.sessionID, int64(entry.Sequence))
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err == nil && changed == 0 {
			// The result itself never reached storage, so the encounter's stored results are not all of its results.
			_, err = w.tx.ExecContext(ctx, "UPDATE encounter SET complete = 0 WHERE id = ?", id)
			return err
		}
		return nil
	case capture.HistoryEncounterOpened:
		if entry.Opened == nil {
			return errors.New("opened entry without its opening")
		}
		_, err := w.insertOpening(ctx, entry.Encounter, *entry.Opened, true)
		return err
	case capture.HistoryEncounterActive:
		id, found, err := w.encounterRow(ctx, entry.Encounter)
		if err != nil || !found {
			return err
		}
		_, err = w.tx.ExecContext(ctx, "UPDATE encounter SET state = 'open' WHERE id = ? AND state = 'pending'", id)
		return err
	case capture.HistoryEncounterFinished:
		if entry.Finished == nil {
			return errors.New("finished entry without its encounter")
		}
		return w.writeFinished(ctx, entry.Encounter, entry.Finished)
	case capture.HistoryEncounterDiscarded:
		id, found, err := w.encounterRow(ctx, entry.Encounter)
		if err != nil || !found {
			return err
		}
		// Assigned results go back outside every encounter through the foreign key (ON DELETE SET NULL).
		if _, err := w.tx.ExecContext(ctx, "DELETE FROM encounter WHERE id = ?", id); err != nil {
			return err
		}
		w.staged[entry.Encounter] = 0
		return nil
	case capture.HistoryEncounterIntegrity:
		if entry.Integrity == nil {
			return errors.New("integrity entry without its integrity")
		}
		id, found, err := w.encounterRow(ctx, entry.Encounter)
		if err != nil || !found {
			return err
		}
		return w.writeIntegrity(ctx, id, *entry.Integrity)
	case capture.HistoryLossBegan:
		loss := entry.Loss
		if loss == nil {
			return errors.New("loss entry without its loss")
		}
		_, err := w.tx.ExecContext(ctx, "INSERT INTO capture_gap (capture_session_id, loss_id, direction, from_ns) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING",
			w.recorder.sessionID, int64(loss.ID), loss.Direction, loss.From.UnixNano())
		return err
	case capture.HistoryLossEnded:
		if entry.Loss == nil {
			return errors.New("loss entry without its loss")
		}
		_, err := w.tx.ExecContext(ctx, "UPDATE capture_gap SET to_ns = ? WHERE capture_session_id = ? AND loss_id = ?", unixNanos(entry.Loss.To), w.recorder.sessionID, int64(entry.Loss.ID))
		return err
	case capture.HistoryLossFailed:
		if entry.Loss == nil {
			return errors.New("loss entry without its loss")
		}
		_, err := w.tx.ExecContext(ctx, "UPDATE capture_gap SET failed = 1 WHERE capture_session_id = ? AND loss_id = ?", w.recorder.sessionID, int64(entry.Loss.ID))
		return err
	}
	return fmt.Errorf("unknown history entry %q", entry.Kind)
}

// encounterRow finds the row of an engine encounter key in this session.
func (w *sessionWriter) encounterRow(ctx context.Context, key string) (int64, bool, error) {
	if id, ok := w.staged[key]; ok {
		return id, id != 0, nil
	}
	if id, ok := w.recorder.encounters[key]; ok {
		return id, true, nil
	}
	interval, sequence, ok := parseEncounterKey(key)
	if !ok {
		return 0, false, fmt.Errorf("malformed encounter key %q", key)
	}
	var id int64
	err := w.tx.QueryRowContext(ctx, "SELECT id FROM encounter WHERE capture_session_id = ? AND statistics_interval = ? AND sequence = ?",
		w.recorder.sessionID, int64(interval), int64(sequence)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	w.staged[key] = id
	return id, true, nil
}

// insertOpening records an encounter as it opened. complete is false when the row is created only because its finished
// entry arrived without the opening, which means the opening was lost.
func (w *sessionWriter) insertOpening(ctx context.Context, key string, opening capture.HistoryEncounterOpening, complete bool) (int64, error) {
	date, offset := w.recorder.store.localDate(opening.StartedAt)
	result, err := w.tx.ExecContext(ctx, `INSERT INTO encounter (capture_session_id, statistics_interval, sequence, protocol_session, state, outcome, complete,
		started_at_ns, start_basis, local_date, utc_offset_s, scene_instance_id, scene_recorded)
		VALUES (?, ?, ?, ?, 'pending', 'OPEN', ?, ?, ?, ?, ?, ?, 1)`,
		w.recorder.sessionID, int64(opening.StatisticsInterval), int64(opening.Sequence), opening.ProtocolSession, boolInt(complete),
		opening.StartedAt.UnixNano(), opening.StartBasis, date, offset, sceneValue(opening.Scene))
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	w.staged[key] = id
	return id, nil
}

func sceneValue(scene capture.SceneContext) any {
	if !scene.Known {
		return nil
	}
	return int64(scene.InstanceID)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// nullableRole splits an optional role into its three columns.
func nullableRole(role *capture.CombatRole) (any, any, any) {
	if role == nil {
		return nil, nil, nil
	}
	return int64(role.PlayerID), int64(role.RoleID), role.Basis
}

func actorIDs(actor *capture.CombatActorType) (int64, int64) {
	if actor == nil {
		return 0, 0
	}
	return int64(actor.MonsterID), int64(actor.TemplateID)
}

// insertEvent stores one accepted combat result. A response key already stored for the session is ignored: the engine
// accepted it once, and replaying a journal never stores it twice.
func (w *sessionWriter) insertEvent(ctx context.Context, sequence uint64, event *capture.CombatEventRecord) error {
	if event == nil {
		return errors.New("accepted entry without its event")
	}
	if event.Kind != capture.CombatEventDamage && event.Kind != capture.CombatEventHealing {
		return fmt.Errorf("unexpected event kind %q", event.Kind)
	}
	sourcePlayer, sourceRole, sourceBasis := nullableRole(event.SourceRole)
	targetPlayer, targetRole, targetBasis := nullableRole(event.TargetRole)
	sourceMonster, sourceTemplate := actorIDs(event.SourceActor)
	targetMonster, targetTemplate := actorIDs(event.TargetActor)
	_, err := w.tx.ExecContext(ctx, `INSERT INTO combat_event (capture_session_id, sequence, protocol_session, rpc_id, observed_at_ns, kind, flow, confidence, evidence,
		amount, applied_healing, healing_inconsistent, damage_id, skill_id,
		source_entity_id, source_generation, source_relation, source_player_id, source_role_id, source_role_basis,
		target_entity_id, target_generation, target_relation, target_player_id, target_role_id, target_role_basis,
		vehicle_kind, context_downgrade, source_monster_id, source_template_id, target_monster_id, target_template_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		w.recorder.sessionID, int64(sequence), event.Key.ProtocolSession, int64(event.Key.RPCID), unixNanos(event.ObservedAt), string(event.Kind),
		string(event.Flow), string(event.Confidence), event.Evidence, event.Amount, event.AppliedHealing, boolInt(event.HealingInconsistent),
		int64(event.DamageID), int64(event.SkillID),
		int64(event.Source.EntityID), int64(event.Source.EntityGeneration), event.Source.Relation, sourcePlayer, sourceRole, sourceBasis,
		int64(event.Target.EntityID), int64(event.Target.EntityGeneration), event.Target.Relation, targetPlayer, targetRole, targetBasis,
		event.VehicleKind, event.ContextDowngrade, sourceMonster, sourceTemplate, targetMonster, targetTemplate)
	return err
}

// writeFinished stores a finished encounter's boundaries, results and splits.
func (w *sessionWriter) writeFinished(ctx context.Context, key string, finished *capture.HistoryEncounter) error {
	snapshot := finished.Snapshot
	id, found, err := w.encounterRow(ctx, key)
	if err != nil {
		return err
	}
	if !found {
		// The opening never reached storage, so the row starts out incomplete.
		id, err = w.insertOpening(ctx, key, capture.HistoryEncounterOpening{
			ProtocolSession: snapshot.ProtocolSession, StatisticsInterval: snapshot.StatisticsIntervalID, Sequence: snapshot.Sequence,
			StartedAt: snapshot.StartedAt, StartBasis: snapshot.StartBasis, Scene: finished.Scene,
		}, false)
		if err != nil {
			return err
		}
	}
	date, offset := w.recorder.store.localDate(snapshot.StartedAt)
	totals, integrity := snapshot.Totals, snapshot.Integrity
	dps, peak, healing, unassigned := snapshot.EncounterDPS, snapshot.PeakDPS, snapshot.Healing, snapshot.UnassignedRole
	_, err = w.tx.ExecContext(ctx, `UPDATE encounter SET state = 'finished', outcome = ?, started_at_ns = ?, start_basis = ?, start_confirmed = ?,
		battle_entered_at_ns = ?, ended_at_ns = ?, end_basis = ?, end_decided_at_ns = ?, first_activity_at_ns = ?, last_activity_at_ns = ?, reentries = ?,
		local_date = ?, utc_offset_s = ?, protocol_session = ?,
		damage_dealt = ?, damage_taken = ?, outgoing_events = ?, incoming_events = ?,
		unattributed_outgoing_damage = ?, unattributed_incoming_damage = ?, unattributed_outgoing_events = ?, unattributed_incoming_events = ?,
		integrity_status = ?, transport_losses = ?, unrecovered_losses = ?, context_downgraded_events = ?, first_loss_at_ns = ?,
		dps_status = ?, dps_reason = ?, dps_value = ?, dps_damage = ?, dps_duration_ns = ?, dps_start_ns = ?, dps_end_ns = ?, dps_confidence = ?,
		peak_status = ?, peak_reason = ?, peak_value = ?, peak_damage = ?, peak_duration_ns = ?, peak_start_ns = ?, peak_end_ns = ?, peak_confidence = ?,
		healing_status = ?, healing_reason = ?, healing_effective = ?, healing_reported = ?, healing_overheal = ?, healing_events = ?,
		healing_unknown_effective = ?, healing_unknown_events = ?,
		unassigned_damage_dealt = ?, unassigned_outgoing_events = ?, unassigned_damage_taken = ?, unassigned_incoming_events = ?, unassigned_overflow_events = ?,
		scene_instance_id = ?, scene_recorded = 1, enemies_recorded = 1, enemies_overflow = ?
		WHERE id = ?`,
		string(snapshot.Outcome), snapshot.StartedAt.UnixNano(), snapshot.StartBasis, boolInt(snapshot.StartConfirmed),
		unixNanos(snapshot.BattleEntered), unixNanos(snapshot.EndedAt), snapshot.EndBasis, unixNanos(snapshot.EndDecidedAt),
		unixNanos(snapshot.FirstActivityAt), unixNanos(snapshot.LastActivityAt), int64(snapshot.Reentries),
		date, offset, snapshot.ProtocolSession,
		totals.DamageDealt, totals.DamageTaken, int64(totals.OutgoingEvents), int64(totals.IncomingEvents),
		totals.UnattributedOutgoingDamage, totals.UnattributedIncomingDamage, int64(totals.UnattributedOutgoingEvents), int64(totals.UnattributedIncomingEvents),
		string(integrity.Status), integrity.TransportLosses, integrity.UnrecoveredLosses, int64(integrity.ContextDowngradedEvents), unixNanos(integrity.FirstLossAt),
		string(dps.Status), dps.Reason, dps.Value, dps.Damage, int64(dps.Duration), unixNanos(dps.StartObservedAt), unixNanos(dps.EndObservedAt), dps.Confidence,
		string(peak.Status), peak.Reason, peak.Value, peak.Damage, int64(peak.Duration), unixNanos(peak.StartObservedAt), unixNanos(peak.EndObservedAt), peak.Confidence,
		string(healing.Status), healing.Reason, healing.Effective, healing.Reported, healing.Overheal, int64(healing.Events),
		healing.UnknownHealerEffective, int64(healing.UnknownHealerEvents),
		unassigned.DamageDealt, int64(unassigned.OutgoingEvents), unassigned.DamageTaken, int64(unassigned.IncomingEvents), int64(unassigned.OverflowEvents),
		sceneValue(finished.Scene), int64(finished.EnemiesOverflow), id)
	if err != nil {
		return err
	}
	for _, statement := range []string{"DELETE FROM encounter_risk WHERE encounter_id = ?", "DELETE FROM encounter_contributor WHERE encounter_id = ?",
		"DELETE FROM encounter_healer WHERE encounter_id = ?", "DELETE FROM encounter_timeline WHERE encounter_id = ?", "DELETE FROM encounter_enemy WHERE encounter_id = ?"} {
		if _, err := w.tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	if err := w.insertRisks(ctx, id, integrity.Risks); err != nil {
		return err
	}
	ordinal := 0
	for _, role := range snapshot.Roles {
		if err := w.insertContributor(ctx, id, ordinal, "role", role.PlayerID, role.RoleID, "", "", role.DamageDealt, role.OutgoingEvents,
			role.DirectDamage, role.SummonDamage, role.DamageTaken, role.IncomingEvents, role.Skills, role.UnassignedSkill); err != nil {
			return err
		}
		ordinal++
	}
	if vehicle := snapshot.Vehicle; vehicle != nil {
		if err := w.insertContributor(ctx, id, ordinal, "vehicle", 0, 0, vehicle.Kind, vehicle.Evidence, vehicle.DamageDealt, vehicle.OutgoingEvents,
			0, 0, 0, 0, vehicle.Skills, vehicle.UnassignedSkill); err != nil {
			return err
		}
	}
	for _, healer := range healing.ByHealer {
		if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_healer (encounter_id, player_id, role_id, effective, reported, events) VALUES (?, ?, ?, ?, ?, ?)",
			id, int64(healer.PlayerID), int64(healer.RoleID), healer.Effective, healer.Reported, int64(healer.Events)); err != nil {
			return err
		}
	}
	if err := w.insertTimeline(ctx, id, finished.Timeline); err != nil {
		return err
	}
	for _, enemy := range finished.Enemies {
		if _, err := w.tx.ExecContext(ctx, `INSERT INTO encounter_enemy (encounter_id, ordinal, monster_id, template_id, identity_conflict,
			damage_taken, hits_taken, damage_dealt, hits_dealt, first_seen_ns, last_seen_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, enemy.Ordinal, int64(enemy.MonsterID), int64(enemy.TemplateID), boolInt(enemy.IdentityConflict),
			enemy.DamageTaken, int64(enemy.HitsTaken), enemy.DamageDealt, int64(enemy.HitsDealt), unixNanos(enemy.FirstSeenAt), unixNanos(enemy.LastSeenAt)); err != nil {
			return err
		}
	}
	return nil
}

func (w *sessionWriter) insertRisks(ctx context.Context, id int64, risks []string) error {
	for _, risk := range risks {
		if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_risk (encounter_id, risk) VALUES (?, ?) ON CONFLICT DO NOTHING", id, risk); err != nil {
			return err
		}
	}
	return nil
}

func (w *sessionWriter) insertContributor(ctx context.Context, id int64, ordinal int, kind string, playerID, roleID uint64, vehicleKind, vehicleEvidence string,
	damage int64, events uint64, direct, summon, taken int64, incoming uint64, skills []capture.SkillDamageSnapshot, unassigned capture.UnassignedSkillTotals) error {
	if _, err := w.tx.ExecContext(ctx, `INSERT INTO encounter_contributor (encounter_id, ordinal, kind, player_id, role_id, vehicle_kind, vehicle_evidence,
		damage_dealt, outgoing_events, direct_damage, summon_damage, damage_taken, incoming_events,
		unassigned_damage, unassigned_events, unassigned_missing_skill_events, unassigned_overflow_events, unassigned_damage_ids_incomplete)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, ordinal, kind, int64(playerID), int64(roleID), vehicleKind, vehicleEvidence, damage, int64(events), direct, summon, taken, int64(incoming),
		unassigned.DamageDealt, int64(unassigned.OutgoingEvents), int64(unassigned.MissingSkillEvents), int64(unassigned.OverflowEvents),
		boolInt(unassigned.DamageIDsIncomplete)); err != nil {
		return err
	}
	evidence := func(skillID uint64, kind string, values []uint64) error {
		for _, value := range values {
			if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_skill_evidence (encounter_id, contributor_ordinal, skill_id, kind, value) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING",
				id, ordinal, int64(skillID), kind, int64(value)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, skill := range skills {
		if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_skill (encounter_id, contributor_ordinal, skill_id, damage_dealt, outgoing_events, damage_ids_incomplete) VALUES (?, ?, ?, ?, ?, ?)",
			id, ordinal, int64(skill.SkillID), skill.DamageDealt, int64(skill.OutgoingEvents), boolInt(skill.DamageIDsIncomplete)); err != nil {
			return err
		}
		if err := evidence(skill.SkillID, "damage_id", skill.DamageIDs); err != nil {
			return err
		}
		if err := evidence(skill.SkillID, "echo_vision", skill.EchoVisions); err != nil {
			return err
		}
	}
	if err := evidence(0, "damage_id", unassigned.DamageIDs); err != nil {
		return err
	}
	return evidence(0, "echo_vision", unassigned.EchoVisions)
}

func (w *sessionWriter) insertTimeline(ctx context.Context, id int64, timeline capture.StoredTimeline) error {
	if timeline.ScaleSeconds < 1 {
		return nil
	}
	if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_timeline (encounter_id, anchor_ns, scale_s) VALUES (?, ?, ?)", id, timeline.Anchor.UnixNano(), timeline.ScaleSeconds); err != nil {
		return err
	}
	for position, contributor := range timeline.Contributors {
		if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_timeline_contributor (encounter_id, position, kind, player_id, role_id, vehicle_kind) VALUES (?, ?, ?, ?, ?, ?)",
			id, position, contributor.Kind, int64(contributor.PlayerID), int64(contributor.RoleID), contributor.VehicleKind); err != nil {
			return err
		}
	}
	for _, bucket := range timeline.Buckets {
		if _, err := w.tx.ExecContext(ctx, "INSERT INTO encounter_timeline_bucket (encounter_id, bucket, position, damage) VALUES (?, ?, ?, ?)",
			id, bucket.Index, bucket.Contributor, bucket.Damage); err != nil {
			return err
		}
	}
	return nil
}

// writeIntegrity replaces a finished encounter's integrity facts. Its stored results stay as the engine computed them.
func (w *sessionWriter) writeIntegrity(ctx context.Context, id int64, integrity capture.DataIntegrity) error {
	if _, err := w.tx.ExecContext(ctx, "UPDATE encounter SET integrity_status = ?, transport_losses = ?, unrecovered_losses = ?, context_downgraded_events = ?, first_loss_at_ns = ? WHERE id = ?",
		string(integrity.Status), integrity.TransportLosses, integrity.UnrecoveredLosses, int64(integrity.ContextDowngradedEvents), unixNanos(integrity.FirstLossAt), id); err != nil {
		return err
	}
	if _, err := w.tx.ExecContext(ctx, "DELETE FROM encounter_risk WHERE encounter_id = ?", id); err != nil {
		return err
	}
	return w.insertRisks(ctx, id, integrity.Risks)
}

// applyTaint marks the encounters whose entries were lost as incomplete and returns the keys that have no row yet.
func (w *sessionWriter) applyTaint(ctx context.Context, t taint) ([]string, error) {
	if t.all {
		_, err := w.tx.ExecContext(ctx, "UPDATE encounter SET complete = 0 WHERE capture_session_id = ?", w.recorder.sessionID)
		return nil, err
	}
	var unresolved []string
	for _, key := range t.keys {
		id, found, err := w.encounterRow(ctx, key)
		if err != nil {
			return nil, err
		}
		if !found {
			unresolved = append(unresolved, key)
			continue
		}
		if _, err := w.tx.ExecContext(ctx, "UPDATE encounter SET complete = 0 WHERE id = ?", id); err != nil {
			return nil, err
		}
	}
	return unresolved, nil
}
