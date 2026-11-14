package capture

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Server-executed damage: DamageExecuteNotify.
//
// The client asks the server to settle the damage it computes itself (a bullet, a client-run Buff execution) with a
// DamageExecuteRequest and reads the result from the DamageExecuteResponse of that RequestId. Damage the server executes
// on its own reaches the client as a DamageExecuteNotify inside a CombatNotifyData, with no request and no RequestId. The
// Global 3.8 client (BaseDamageComponent.js, 3.8.0 base, not replaced by the 3.8.9 hotfix) listens for it as the
// CombatNotifyData field EFn ("服务端伤害广播", the server's damage broadcast): OnDamageExecuteNotify finds the target by
// TargetEntityId, ProcessRemoteDamage takes the attacker from AttackerEntityId and the damage row from DamageId, and both
// paths then run the same uqr step, which emits CharDamage on the attacker and CharBeDamage on the target. The game's own
// per-entity damage statistics (PhantomArenaBattleDamageStatisticsItem, CharacterStatisticsComponent) add up those events,
// so the client counts a notified result exactly like a response, credited to the entity the server names as attacker.
// Neither path changes HP: Life arrives in AttributeChangedNotify.
//
// Accounting rules (research/runs/2026-11-14/v0.7.0-damage-execute-notify-client-re):
//   - A notified result enters the same normalized record, statistics, encounter and Combat History path as a response,
//     with the same admission rule (Damage present and non-zero, ImmuneType 0, an attacker and a target) and the same
//     DamageId CalculateType classification. Every endpoint relation, role credit and the vehicle rule are the response's.
//   - A notified result is one server message, so it is counted once: its key is the protocol session, the server frame's
//     sequence number and the result's position among the frame's notified results (notifyEventKey). The captures had no
//     notified result that repeated a counted response: no equal Life step, and no DamageId of a notified result ever
//     appeared in a client request.
//   - Negative-status damage (Aero Erosion, Spectro Frazzle and the other AbnormalDamageConfig statuses): the server
//     names the scene's combat entity (SceneInformation.SceneEntityId) as attacker, and no character. It is the local
//     team's damage when the target is an enemy, the scene has no player but the local one, and every live status
//     instance on the target that executes this DamageId was applied by the local team (negativeStatusOwnership).
//     Otherwise its source stays as resolved, which keeps it out of the local totals. A local result is then credited to
//     the role whose InvokePeriod forced the execution, or to the role that owns the executing status instance
//     (negative_status_credit.go); without such a role it stays in the unassigned bucket.
//   - The notification has no CombatCommon Originator, MessageId or PreMessageId (0 in every capture), so nothing in it
//     names a player: local ownership comes only from the entity evidence above.

// combatEvidenceDamageExecuteNotify marks a record built from a server-executed DamageExecuteNotify.
const combatEvidenceDamageExecuteNotify = "damage_execute_notify"

// CombatEvidenceNegativeStatusNotify marks a notified negative-status result credited to the local team through the
// status instances on its target; the record's source stays the scene combat entity the server named. Combat History keeps
// it in combat_event.evidence and derives UnassignedRoleTotals.NegativeStatusDamage from it.
const CombatEvidenceNegativeStatusNotify = "damage_execute_notify_negative_status"

const combatEvidenceNegativeStatusNotify = CombatEvidenceNegativeStatusNotify

// negativeStatusLiveSlack is how long past its estimated expiry a timed status instance still counts as live for a
// notified tick: the server's last tick and its REMOVE arrive around the estimated end.
const negativeStatusLiveSlack = time.Second

// notifyEventKey numbers a notified result inside its protocol session: the server frame's sequence (strictly
// increasing for combat frames in every capture, never zero) and the result's position in that frame. A frame without
// a sequence gives no key, and such a result is counted without deduplication, like a response without a RequestId.
func notifyEventKey(sequence uint32, ordinal int) uint64 {
	if sequence == 0 || ordinal < 0 || ordinal > 0xffff {
		return 0
	}
	return uint64(sequence)<<16 | uint64(ordinal)
}

// damageExecuteNotify is one decoded DamageExecuteNotify (protocol_numbers.go, EFn).
type damageExecuteNotify struct {
	attacker, target       uint64
	hasAttacker, hasTarget bool
	damageID               uint64
	hasDamageID            bool
	damage                 int32
	hasDamage              bool
	changeLife             int32
	hasChangeLife          bool
	immuneType             int32
	partID                 int32
	hasPartID              bool
	skillLevel             uint64
	context                damageContext
}

// damageContext holds the DamageContext of a notified result; no notified result of the captures carried one.
type damageContext struct {
	sourceType, skillID, skillMessageID uint64
	hasSourceType                       bool
	bulletID                            int64
	hasBulletID                         bool
}

func parseDamageExecuteNotify(body []byte) (damageExecuteNotify, error) {
	var notify damageExecuteNotify
	fields, err := parseProtoFields(body)
	if err != nil {
		return notify, fmt.Errorf("parse DamageExecuteNotify: %w", err)
	}
	for _, field := range fields {
		if field.number == fieldDamageExecuteNotifyDamageContext && field.wire == 2 {
			context, err := parseDamageContext(field.data)
			if err != nil {
				return notify, err
			}
			notify.context = context
			continue
		}
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case fieldDamageExecuteNotifyAttackerEntityID:
			notify.attacker, notify.hasAttacker = field.value, true
		case fieldDamageExecuteNotifyTargetEntityID:
			notify.target, notify.hasTarget = field.value, true
		case fieldDamageExecuteNotifyDamageID:
			notify.damageID, notify.hasDamageID = field.value, true
		case fieldDamageExecuteNotifyDamage:
			notify.damage, notify.hasDamage = int32(uint32(field.value)), true
		case fieldDamageExecuteNotifyChangeLife:
			notify.changeLife, notify.hasChangeLife = int32(uint32(field.value)), true
		case fieldDamageExecuteNotifyImmuneType:
			notify.immuneType = int32(uint32(field.value))
		case fieldDamageExecuteNotifyPartID:
			notify.partID, notify.hasPartID = int32(uint32(field.value)), true
		case fieldDamageExecuteNotifySkillLevel:
			notify.skillLevel = field.value
		}
	}
	return notify, nil
}

func parseDamageContext(body []byte) (damageContext, error) {
	var context damageContext
	fields, err := parseProtoFields(body)
	if err != nil {
		return context, fmt.Errorf("parse DamageContext: %w", err)
	}
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case fieldDamageContextSourceType:
			context.sourceType, context.hasSourceType = field.value, true
		case fieldDamageContextSkillID:
			context.skillID = field.value
		case fieldDamageContextSkillMessageID:
			context.skillMessageID = field.value
		case fieldDamageContextBulletID:
			context.bulletID, context.hasBulletID = int64(field.value), true
		}
	}
	return context, nil
}

// damageNotifyStats counts notified results for diagnostics; the statistics own what was counted.
type damageNotifyStats struct {
	notifies            int
	noAmount            int // no Damage, Damage 0 or a nonzero ImmuneType: nothing to count
	malformed           int
	keyless             int // counted without a deduplication key
	negativeStatus      int // negative-status results seen
	negativeStatusLocal int // of those, credited to the local team
	creditedTrigger     int // of those, credited to the role whose InvokePeriod forced the execution
	creditedOwner       int // of those, credited to the role that owns the executing instance
	uncredited          int // of those, credited to no role
	statusTriggers      int // InvokePeriod Buffs the server applied on a holder
	// statusTriggersForcingNothing counts InvokePeriods that found no named status alive on the holder.
	statusTriggersForcingNothing int
	// settledByApplication counts local results of statuses without a period (caused by an application).
	settledByApplication int
	// settledAcrossTargets counts local results an overflow on another enemy can have spread.
	settledAcrossTargets int
	// instantStatusExecutions counts instant status Buffs the server applied (they execute their status by themselves).
	instantStatusExecutions int
	// excludedStatusInstances counts status instances whose holder and source are unrelated to every player.
	excludedStatusInstances int
}

// handleDamageExecuteNotify reads one DamageExecuteNotify of a CombatNotifyData; ordinal is its position among the
// notified results of the frame.
func (p *processor) handleDamageExecuteNotify(frame gameFrame, body []byte, ordinal int) {
	p.notifyStats.notifies++
	notify, err := parseDamageExecuteNotify(body)
	if err != nil {
		p.notifyStats.malformed++
		p.stats.parseErrors++
		return
	}
	// Every negative-status execution the scene combat entity reports uses up the trigger that forced it, also one that
	// deals nothing, so a trigger never explains a later execution of the same instance.
	negativeStatus := notify.hasDamageID && p.masters.isNegativeStatusDamage(notify.damageID)
	var execution negativeStatusExecution
	var candidates []statusCandidate
	var spread []spreadCandidate
	if negativeStatus && notify.hasAttacker && notify.hasTarget && notify.attacker == p.sceneCombatEntity() && notify.attacker != 0 {
		candidates = p.negativeStatusCandidates(notify.target, notify.damageID, frame.timestamp)
		p.statusCauses.prune(frame.timestamp)
		spread = p.spreadCandidates(notify.target, notify.damageID, frame.timestamp)
		execution = p.matchNegativeStatusExecution(notify.target, notify.damageID, candidates, spread, frame.timestamp)
	}
	if !notify.hasDamage || notify.damage == 0 || notify.immuneType != 0 {
		p.notifyStats.noAmount++
		return
	}
	value := int64(notify.damage)
	if value < 0 {
		value = -value
	}
	request := pendingDamageRequest{
		attackerEntityID: notify.attacker, hasAttackerEntityID: notify.hasAttacker,
		targetEntityID: notify.target, hasTargetEntityID: notify.hasTarget,
		partID: notify.partID, hasPartID: notify.hasPartID, skillLevel: notify.skillLevel,
		damageID: notify.damageID, hasDamageID: notify.hasDamageID,
		skillID: notify.context.skillID, skillMessageID: notify.context.skillMessageID,
		damageSourceType: notify.context.sourceType, hasDamageSourceType: notify.context.hasSourceType,
		bulletID: notify.context.bulletID, hasBulletID: notify.context.hasBulletID,
	}
	event := p.buildDamageEvent(0, request, notify.damage, notify.changeLife, notify.hasChangeLife, value)
	event.Notify = &notifiedResult{Key: notifyEventKey(frame.sequence, ordinal)}
	if event.Notify.Key == 0 {
		p.notifyStats.keyless++
	}
	if negativeStatus {
		p.notifyStats.negativeStatus++
		event.Notify.NegativeStatus = true
		event.Notify.NegativeStatusLocal, event.Notify.NegativeStatusBasis = p.negativeStatusOwnership(event, notify.damageID, candidates, spread, execution, frame.timestamp)
		if event.Notify.NegativeStatusLocal {
			p.notifyStats.negativeStatusLocal++
			event.Notify.Execution = execution
			if role := p.negativeStatusRole(execution); role != nil {
				event.Notify.Credit = role
				if role.Basis == roleBasisNegativeStatusTrigger {
					p.notifyStats.creditedTrigger++
				} else {
					p.notifyStats.creditedOwner++
				}
			} else {
				p.notifyStats.uncredited++
				if execution.Reason == "" {
					event.Notify.Execution.Reason = "the credited entity is no local role or summon of one"
				}
			}
			switch execution.Settlement {
			case statusSettlementApplied:
				p.notifyStats.settledByApplication++
			case statusSettlementSpread:
				p.notifyStats.settledByApplication++
				p.notifyStats.settledAcrossTargets++
			}
		}
	}
	p.stats.decodedMatchedEvents++
	p.stats.decodedCombatResultAbs += value
	if p.writeDamageEvent(event) {
		p.observeEncounterSummary(event)
	}
}

// notifiedResult marks a combatEvent read from a DamageExecuteNotify.
type notifiedResult struct {
	// Key is notifyEventKey; zero when the frame had no sequence.
	Key uint64
	// NegativeStatus is set for a DamageId named by AbnormalDamageConfig; NegativeStatusLocal when negativeStatusOwnership
	// credited it to the local team, with the reason either way in NegativeStatusBasis.
	NegativeStatus      bool
	NegativeStatusLocal bool
	NegativeStatusBasis string
	// Execution is what the server's messages say caused a local negative-status result (negative_status_credit.go), and
	// Credit the local role it is credited to; nil leaves it in the unassigned bucket.
	Execution negativeStatusExecution
	Credit    *CombatRole
}

// negativeStatusOwnership decides whether a notified negative-status result is the local team's damage. The server names
// no character for it: its attacker is the scene's combat entity, and the client credits it to that entity too. The
// damage belongs to the team whose status the server executes, so it is the local team's when
//   - the attacker is the current scene's combat entity (SceneInformation.SceneEntityId), never a role or summon,
//   - the target is an enemy (UNRELATED to the local player),
//   - the scene lists no player but the local one (with another player in the scene a status could be theirs),
//   - no status instance applied by an entity unrelated to every player can have executed it (on the target, or by an
//     overflow on another enemy of the moment), and no instant status Buff of the moment names someone outside the team,
//   - at least one possible cause exists: a status instance on the target that can have executed this DamageId (alive, or
//     removed by the server just before: negativeStatusCandidates), an overflow on another enemy of the moment that can
//     have spread it (spreadCandidates), or an instant status Buff of the moment, and
//   - every such instance was applied by the local team, or the result was credited to a local role whose action caused
//     it (execution: forced, instant, applied or spread) and no such instance was applied by an enemy: the instance names
//     no instigator the processor can place, but the action that caused this result is the local team's.
func (p *processor) negativeStatusOwnership(event combatEvent, damageID uint64, candidates []statusCandidate, spread []spreadCandidate, execution negativeStatusExecution, at time.Time) (bool, string) {
	if scene := p.sceneCombatEntity(); scene == 0 || event.SourceEntityID != scene {
		return false, "attacker is not the scene's combat entity"
	}
	if p.playerRelationForEntity(event.TargetEntityID, event.Target) != playerRelationUnrelated {
		return false, "target is not an enemy"
	}
	if len(p.scenePlayerIDs) != 1 || p.playerRelevance == nil || !p.playerRelevance.isLocalPlayerID(p.scenePlayerIDs[0]) {
		return false, "the scene does not list the local player alone"
	}
	if p.excludedStatusExecutes(event.TargetEntityID, damageID, at) {
		return false, "a status instance on the target that executes this DamageId was applied by an entity unrelated to every player"
	}
	if len(candidates) == 0 && len(spread) == 0 && !execution.Instant {
		return false, "no status instance on the target or around it can have executed this DamageId"
	}
	if execution.InstantNotLocal {
		return false, "an instant status Buff of the moment names an instigator outside the local team"
	}
	unplaced := false
	for _, candidate := range candidates {
		switch {
		case candidate.local:
		case candidate.enemy:
			return false, "a live status instance on the target that executes this DamageId was not applied by the local team"
		default:
			unplaced = true
		}
	}
	for _, candidate := range spread {
		switch {
		case candidate.excluded || candidate.enemy:
			return false, "an overflow of a status the local team did not apply can have spread the result from another enemy"
		case candidate.local:
		default:
			unplaced = true
		}
	}
	if !unplaced {
		switch {
		case len(candidates) == 0 && len(spread) > 0:
			return true, "the result is on an enemy without the status, and every overflow of the moment that can have spread it is of a status the local team applied"
		case len(candidates) == 0:
			return true, "a local role's instant status Buff executed the result"
		case len(spread) > 0:
			return true, "every status instance on the target and every overflow of the moment that can have executed this DamageId was applied by the local team"
		default:
			return true, "every live status instance on the target that executes this DamageId was applied by the local team"
		}
	}
	if execution.CreditBasis == roleBasisNegativeStatusTrigger && p.localRoleForEntity(execution.CreditEntity, entityIdentity{}) != nil {
		return true, "a local role's action caused the execution of a status instance whose instigator is unknown"
	}
	return false, "a status instance that can have executed this DamageId was not applied by the local team"
}

// sceneCombatEntity is the current scene's combat entity, or 0 when the scene named none.
func (p *processor) sceneCombatEntity() uint64 {
	if p == nil || p.entities == nil || !p.entities.hasSceneCombatEntityID {
		return 0
	}
	return p.entities.sceneCombatEntityID
}

// isNegativeStatusDamage reports a DamageId named by an Abnormal<id> column of AbnormalDamageConfig.
func (m *gameMaster) isNegativeStatusDamage(damageID uint64) bool {
	if m == nil || damageID == 0 {
		return false
	}
	_, ok := m.abnormalDamageIDs[damageID]
	return ok
}

// negativeStatusExecutes reports whether the Buff effectID is a status whose extra effect is the negative-status type that
// executes damageID. The client's ExtraEffectDefine maps extra effect types 1001-1006 to the Abnormal* status classes, whose
// periodic execution is empty on the client; the server executes them with the DamageId of the same number, whose damage
// row reads the AbnormalDamageConfig column Abnormal<id>. In the captures every such notified result had a live instance of
// a Buff of that type on its target (CORRELATED: the relation is the shared number, not a stated reference).
func (m *gameMaster) negativeStatusExecutes(effectID, damageID uint64) bool {
	if m == nil {
		return false
	}
	executed, ok := m.negativeStatusExecutors[effectID]
	return ok && executed == damageID
}

// negativeStatusFormula is the damage FormulaType of the negative statuses: the client's formula table has no entry for it
// (CharacterDamageCalculations, "unexpected formula type"), so only the server computes such damage.
const negativeStatusFormula = 9

// loadSQLiteNegativeStatusDamage reads the negative-status DamageIds, the Buffs whose extra effect type is one of them, and
// how each such Buff settles. AbnormalDamageConfig names 1001-1006 through its Abnormal<id> columns; the damage rows of
// FormulaType 9 add the DamageIds it has no column for (Global 3.8: 1007, executed by the Glacio Chafe variant 10011100
// that replaces 10011000 for some appliers, buff.replace_buff_ids).
func loadSQLiteNegativeStatusDamage(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "abnormaldamageconfig")
	if err != nil {
		return err
	}
	if exists {
		rows, err := db.Query(`PRAGMA table_info("abnormaldamageconfig")`)
		if err != nil {
			return fmt.Errorf("inspect abnormaldamageconfig columns: %w", err)
		}
		for rows.Next() {
			var ordinal, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return fmt.Errorf("read abnormaldamageconfig column: %w", err)
			}
			if match := abnormalColumn.FindStringSubmatch(name); match != nil {
				id, err := strconv.ParseUint(match[1], 10, 64)
				if err != nil {
					rows.Close()
					return fmt.Errorf("abnormaldamageconfig column %q: %w", name, err)
				}
				master.abnormalDamageIDs[id] = struct{}{}
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if present, err := masterColumnExists(db, "damage", "formula_type"); err != nil {
		return err
	} else if present {
		rows, err := db.Query(`SELECT CAST(id AS TEXT) FROM damage WHERE CAST(formula_type AS TEXT) = ?`, strconv.Itoa(negativeStatusFormula))
		if err != nil {
			return fmt.Errorf("query negative-status damage rows: %w", err)
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return fmt.Errorf("read negative-status damage row: %w", err)
			}
			id, err := parseMasterID(raw, "damage.id")
			if err != nil {
				rows.Close()
				return err
			}
			master.abnormalDamageIDs[id] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if len(master.abnormalDamageIDs) == 0 {
		return nil
	}
	present, err := masterColumnExists(db, "buff", "extra_effect_id")
	if err != nil || !present {
		return err
	}
	// The settlement columns are optional: a status without them settles by the strictest rule (negativeStatusSettlement).
	columns := []string{"CAST(id AS TEXT)", "CAST(extra_effect_id AS TEXT)"}
	for _, column := range []string{"period", "b_execute_periodic_effect_on_application", "b_clear_stack_on_overflow", "duration_policy"} {
		present, err := masterColumnExists(db, "buff", column)
		if err != nil {
			return err
		}
		if present {
			columns = append(columns, "CAST("+column+" AS TEXT)")
		} else {
			columns = append(columns, "NULL")
		}
	}
	ids := make([]string, 0, len(master.abnormalDamageIDs))
	for id := range master.abnormalDamageIDs {
		ids = append(ids, strconv.FormatUint(id, 10))
	}
	executors, err := db.Query("SELECT " + strings.Join(columns, ", ") + " FROM buff WHERE CAST(extra_effect_id AS TEXT) IN ('" + strings.Join(ids, "','") + "')")
	if err != nil {
		return fmt.Errorf("query negative-status Buffs: %w", err)
	}
	defer executors.Close()
	for executors.Next() {
		var rawBuff, rawType string
		var period, onApplication, clearOnOverflow, durationPolicy sql.NullString
		if err := executors.Scan(&rawBuff, &rawType, &period, &onApplication, &clearOnOverflow, &durationPolicy); err != nil {
			return fmt.Errorf("read negative-status Buff: %w", err)
		}
		buffID, err := parseMasterID(rawBuff, "buff.id")
		if err != nil {
			return err
		}
		statusType, err := parseMasterID(rawType, "buff.extra_effect_id")
		if err != nil {
			return err
		}
		master.negativeStatusExecutors[buffID] = statusType
		settlement, err := parseNegativeStatusSettlement(period, onApplication, clearOnOverflow, durationPolicy)
		if err != nil {
			return fmt.Errorf("negative-status Buff %d: %w", buffID, err)
		}
		master.negativeStatusSettlements[buffID] = settlement
	}
	return executors.Err()
}

// parseNegativeStatusSettlement reads buff.period, buff.b_execute_periodic_effect_on_application,
// buff.b_clear_stack_on_overflow and buff.duration_policy of an executing status Buff; any of them may be absent.
func parseNegativeStatusSettlement(period, onApplication, clearOnOverflow, durationPolicy sql.NullString) (negativeStatusSettlement, error) {
	var settlement negativeStatusSettlement
	if period.Valid && strings.TrimSpace(period.String) != "" {
		value, err := strconv.ParseFloat(strings.TrimSpace(period.String), 64)
		if err != nil {
			return settlement, fmt.Errorf("buff.period %q: %w", period.String, err)
		}
		settlement.known = true
		settlement.periodic = value > 0
	}
	settlement.executesOnCreation = onApplication.Valid && strings.TrimSpace(onApplication.String) == "1"
	settlement.clearsOnOverflow = clearOnOverflow.Valid && strings.TrimSpace(clearOnOverflow.String) == "1"
	settlement.instant = durationPolicy.Valid && strings.TrimSpace(durationPolicy.String) == "0"
	return settlement, nil
}

// damageNotifyLine formats the notified-result counters for development diagnostics.
func damageNotifyLine(s damageNotifyStats, sequenceGaps int) string {
	return fmt.Sprintf("damage_notify notifies=%d no_amount=%d malformed=%d keyless=%d negative_status=%d negative_status_local=%d credited_trigger=%d credited_owner=%d uncredited=%d status_triggers=%d status_triggers_forcing_nothing=%d settled_by_application=%d settled_across_targets=%d instant_status_executions=%d excluded_status_instances=%d server_sequence_gaps=%d",
		s.notifies, s.noAmount, s.malformed, s.keyless, s.negativeStatus, s.negativeStatusLocal, s.creditedTrigger, s.creditedOwner, s.uncredited, s.statusTriggers,
		s.statusTriggersForcingNothing, s.settledByApplication, s.settledAcrossTargets, s.instantStatusExecutions, s.excludedStatusInstances, sequenceGaps)
}
