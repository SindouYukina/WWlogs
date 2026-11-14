package capture

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Role credit of negative-status damage (Aero Erosion, Spectro Frazzle and the other statuses of the server-only
// negative-status formula).
//
// The server executes a negative status on an enemy and names the scene's combat entity as attacker, so the damage result
// itself names no character (damage_notify.go). Research on the Global 3.8 client and four 3.8.9 captures
// (research/runs/2026-11-14/v0.7.0-causal-damage-attribution, corrected and generalized by
// research/runs/2026-11-14/v0.7.0-anomaly-attribution-verification) established what causes each execution:
//
//   - A status on an enemy is one shared instance (StackingType 2: BaseBuffComponent.GetStackableBuff returns the holder's
//     instance whoever adds a stack). Its InstigatorId is set once, when the instance is created (ActiveBuff.AU), and never
//     changes; every later stack application reports its own instigator in BuffStackCountNotify. The creator is the owner.
//   - A status with a period (buff.period > 0: Aero Erosion, Electro Flare, Spectro Frazzle) executes on its own timer
//     and, when buff.b_execute_periodic_effect_on_application is set, once right after its creation. Both executions are
//     the instance's own: the executing context is the instance and its owner.
//   - A status without a period (Glacio Chafe, Fusion Burst, Havoc Bane) has no timer: per the client's glossary its
//     damage, when it has any, is caused by an application (Glacio Chafe on every application, Fusion Burst when an
//     application overflows the stack limit, which also clears the instance: buff.b_clear_stack_on_overflow). The server
//     does not say which application caused a result, so every applier of the instance is a candidate.
//   - An InvokePeriod Buff (extra effect type 67) that a role applies to the holder forces an immediate execution of the
//     named statuses present at that moment (InvokePeriod.OnExecute -> GetBuffById -> ApplyPeriodExecution) without
//     touching their timers. The server reports it as an instant ApplyGameplayEffectNotify (no handle) with its own
//     InstigatorId, before the forced result. A status created after the InvokePeriod was not forced by it: in the captures
//     its single execution after creation is the on-creation one.
//
// Product rule (owner policy): a forced execution is credited to the role whose InvokePeriod forced it, any other execution
// to the role that caused it by the status's own settlement: the owner of a timed status, the applier of a status without
// a period. A result is credited only when every actor the evidence leaves possible is the same entity, and that entity
// resolves to a local role or a summon of one (localRoleForEntity); otherwise it stays the local team's damage but no
// role's (UnassignedRoleTotals.NegativeStatusDamage), with the reason. Proximity in time alone never credits a role:
//   - a trigger names the instances it can force when it is applied, and explains at most one execution of each;
//   - InvokePeriods of different roles pending for the same instance make its forced executions ambiguous;
//   - a result is the instance's own only when the server stream before it is complete (no missing sequenced server
//     frame, which could have carried a forcing InvokePeriod) and no InvokePeriod that could still explain it is open;
//   - an instance removed by the server just before the result can still be the executing one (an overflow clears it).
// The executing entity, the scene combat entity, stays the record's source.

const (
	// roleBasisNegativeStatusTrigger credits a forced negative-status execution to the role whose InvokePeriod forced it.
	roleBasisNegativeStatusTrigger = "negative_status_trigger"
	// roleBasisNegativeStatusOwner credits a negative-status execution to the role that owns, and alone applied, the
	// executing status instance.
	roleBasisNegativeStatusOwner = "negative_status_owner"
	// RoleBasisNegativeStatusTrigger and RoleBasisNegativeStatusOwner are the stored forms of the two credit bases.
	RoleBasisNegativeStatusTrigger = roleBasisNegativeStatusTrigger
	RoleBasisNegativeStatusOwner   = roleBasisNegativeStatusOwner
)

// invokePeriodExtraEffect is the client's ExtraEffectDefine type of InvokePeriod (getBuffExecutionClass(67)).
const invokePeriodExtraEffect = 67

// statusTriggerCarry is how long after an InvokePeriod its forced result may arrive and still be explained by it. In the
// captures a forced result followed its InvokePeriod after at most 0.053 s (663 forced executions); the bound searches
// three times that. It is a search bound, not evidence: the trigger also has to name the instance and have found it alive.
const statusTriggerCarry = 150 * time.Millisecond

// statusTriggerDoubt is how long an InvokePeriod that found an instance alive but forced no result yet keeps doubt on
// that instance's executions: a late forced result could not be told from the instance's own. Every such InvokePeriod of
// the captures was answered within 0.053 s, so the doubt only matters for a delayed server.
const statusTriggerDoubt = time.Second

// statusEndedCarry is how long after its removal by the server a status instance can still be the executing one of a
// result: an overflow that clears the instance (b_clear_stack_on_overflow) and the execution that removes the last stack
// can be reported before their result.
const statusEndedCarry = 150 * time.Millisecond

// maxStatusTriggers bounds the remembered triggers. Each lives at most statusTriggerDoubt; a burst of a full team on many
// enemies stays far below this, so the bound only stops pathological input.
const maxStatusTriggers = 256

// maxStatusStackInstigators bounds the distinct stack instigators kept per status instance; a status applied by more
// entities than this credits nobody.
const maxStatusStackInstigators = 8

// maxServerSequenceGaps bounds the remembered gaps of the server's frame sequence.
const maxServerSequenceGaps = 16

// Settlement kinds of a negative-status result, as the development diagnostics name them.
const (
	statusSettlementForced       = "forced"
	statusSettlementOwn          = "own"
	statusSettlementApplied      = "applied"
	statusSettlementUndetermined = "undetermined"
)

// negativeStatusSettlement is how an executing status Buff settles, from its Buff row.
type negativeStatusSettlement struct {
	// known is set when the Buff row has a period column; without it the status is taken as one without a period, the
	// stricter rule.
	known bool
	// periodic is buff.period > 0: the status executes on its own timer.
	periodic bool
	// executesOnCreation is buff.b_execute_periodic_effect_on_application.
	executesOnCreation bool
	// clearsOnOverflow is buff.b_clear_stack_on_overflow: an application past the stack limit removes the instance.
	clearsOnOverflow bool
}

// negativeStatusSettlementOf returns how a status Buff settles.
func (m *gameMaster) negativeStatusSettlementOf(effectID uint64) negativeStatusSettlement {
	if m == nil {
		return negativeStatusSettlement{}
	}
	return m.negativeStatusSettlements[effectID]
}

// NegativeStatusSettlement is the product's reading of how a status Buff executes a negative-status DamageId, for the
// version readiness gate: whether the product takes damageID as negative-status damage executed by effectID, and how the
// status settles (Known is false when the Buff row gave no period).
type NegativeStatusSettlement struct {
	Executes, Known, Periodic, ExecutesOnCreation, ClearsOnOverflow, Forcible bool
}

// NegativeStatusSettlement returns the product's reading of status Buff effectID executing damageID.
func (m *MasterData) NegativeStatusSettlement(effectID, damageID uint64) NegativeStatusSettlement {
	if m == nil || m.masters == nil {
		return NegativeStatusSettlement{}
	}
	settlement := m.masters.negativeStatusSettlementOf(effectID)
	return NegativeStatusSettlement{
		Executes: m.masters.isNegativeStatusDamage(damageID) && m.masters.negativeStatusExecutes(effectID, damageID),
		Known:    settlement.known, Periodic: settlement.periodic, ExecutesOnCreation: settlement.executesOnCreation,
		ClearsOnOverflow: settlement.clearsOnOverflow, Forcible: m.masters.statusCanBeForced(effectID),
	}
}

// negativeStatusTerm names the status behind a negative-status DamageId.
type negativeStatusTerm struct {
	termID uint64
	name   string
	// iconBuff is the lowest executing status Buff that carries the term; the status row shows its Buff icon.
	iconBuff uint64
}

// statusTrigger is one InvokePeriod the server applied on a holder.
type statusTrigger struct {
	session            int
	holder, generation uint64
	at                 time.Time
	effectID           uint64
	source             uint64
	hasSource          bool
	// eligible lists the status instances (tracker ordinals) the InvokePeriod found alive on the holder when the server
	// applied it: the only ones it can have forced.
	eligible []uint64
	// used lists the eligible instances whose forced execution this trigger already explained.
	used []uint64
	// ambiguous lists the eligible instances for which this trigger was pending together with an InvokePeriod of another
	// instigator: which of them forced an execution of the instance cannot be told.
	ambiguous []uint64
}

// statusTriggerLog keeps the InvokePeriod triggers of the last statusTriggerDoubt in arrival order.
type statusTriggerLog struct {
	entries []statusTrigger
	dropped uint64
}

// add remembers a trigger and forgets those too old to matter now.
func (l *statusTriggerLog) add(trigger statusTrigger) {
	l.prune(trigger.at)
	if len(l.entries) >= maxStatusTriggers {
		l.entries = l.entries[1:]
		l.dropped++
	}
	l.entries = append(l.entries, trigger)
}

func (l *statusTriggerLog) prune(at time.Time) {
	kept := l.entries[:0]
	for _, entry := range l.entries {
		if !at.IsZero() && at.Sub(entry.at) > statusTriggerDoubt {
			continue
		}
		kept = append(kept, entry)
	}
	for i := len(kept); i < len(l.entries); i++ {
		l.entries[i] = statusTrigger{}
	}
	l.entries = kept
}

// serverSequenceGap is a stretch of the current protocol session in which sequenced server frames are missing: from the
// last frame before the gap to the first after it.
type serverSequenceGap struct {
	session  int
	from, to time.Time
}

// serverSequenceState follows the sequence of the server's frames. Every sequenced frame the server sends advances it by
// one (95,033 of 95,033 sequenced inbound frames of the four Global 3.8.9 captures); frames of sequence 0 carry none.
type serverSequenceState struct {
	session int
	last    uint32
	lastAt  time.Time
	has     bool
	gaps    []serverSequenceGap
	// total counts the gaps of the run, for diagnostics.
	total int
}

// observeServerSequence notes one server frame's sequence and records a gap when sequenced frames are missing before it.
func (p *processor) observeServerSequence(frame gameFrame) {
	if p == nil || frame.direction != "in" || frame.sequence == 0 {
		return
	}
	state := &p.serverSequence
	if !state.has || state.session != p.sessionNumber {
		state.session, state.last, state.lastAt, state.has = p.sessionNumber, frame.sequence, frame.timestamp, true
		return
	}
	if frame.sequence != state.last+1 {
		gap := serverSequenceGap{session: p.sessionNumber, from: state.lastAt, to: frame.timestamp}
		if len(state.gaps) >= maxServerSequenceGaps {
			state.gaps = state.gaps[1:]
		}
		state.gaps = append(state.gaps, gap)
		state.total++
	}
	state.last, state.lastAt = frame.sequence, frame.timestamp
}

// serverStreamIncomplete reports whether sequenced server frames of the current protocol session are missing between from
// and to: a frame the server sent in that span may be lost.
func (p *processor) serverStreamIncomplete(from, to time.Time) bool {
	if p == nil {
		return false
	}
	for _, gap := range p.serverSequence.gaps {
		if gap.session == p.sessionNumber && !gap.to.Before(from) && !gap.from.After(to) {
			return true
		}
	}
	return false
}

// statusCandidate is one status instance that can have executed a negative-status result: alive at the result, or removed
// by the server within statusEndedCarry before it.
type statusCandidate struct {
	ordinal   uint64
	handle    uint64
	effectID  uint64
	source    uint64
	hasSource bool
	// local is set when the instance's source is the local team (a role, an entity it owns, or the local player); enemy
	// when it is an entity unrelated to any player (an enemy). An instance with neither names an instigator the processor
	// cannot place, or none.
	local, enemy bool
	// stackInstigators are the distinct InstigatorIds of the instance's stack applications; manyStackInstigators is set
	// when more were seen than the tracker keeps.
	stackInstigators     []uint64
	manyStackInstigators bool
}

// negativeStatusExecution is what the server's own messages say about one negative-status result: which instance
// executed, how it settled, and whose action that was.
type negativeStatusExecution struct {
	// CreditEntity is the runtime entity the result is credited to (trigger or owner); zero when none qualifies.
	CreditEntity uint64
	// CreditBasis is roleBasisNegativeStatusTrigger or roleBasisNegativeStatusOwner, empty without a credit entity.
	CreditBasis string
	// Settlement is statusSettlementForced, Own, Applied or Undetermined.
	Settlement string
	// StatusEffectID is the executing status Buff, when one instance can be named.
	StatusEffectID uint64
	// TriggerEffectID is the InvokePeriod Buff that forced the execution; zero for the instance's own execution.
	TriggerEffectID uint64
	// Owner is the executing instance's InstigatorId; zero when the candidate instances disagree or name none.
	Owner uint64
	// Candidates is the number of distinct entities the evidence leaves as the cause; one for a credited result.
	Candidates int
	// Reason says why the result has no credit entity; empty with one.
	Reason string
}

// recordStatusTrigger remembers an instant InvokePeriod the server applied, with the named status instances it found alive.
func (p *processor) recordStatusTrigger(row buffLifecycleAuditRow, holder uint64) {
	if p == nil || row.ID == nil || row.HandleID == nil || validEffectHandle(*row.HandleID) {
		return
	}
	named := p.masters.invokePeriodStatuses(*row.ID)
	if len(named) == 0 {
		return
	}
	generation := p.effectHolderGeneration(holder)
	trigger := statusTrigger{session: row.SessionNumber, holder: holder, generation: generation, at: row.Timestamp, effectID: *row.ID}
	if row.InstigatorID != nil && *row.InstigatorID != 0 {
		trigger.source, trigger.hasSource = *row.InstigatorID, true
	}
	for _, candidate := range p.effects.statusCandidates(row.SessionNumber, holder, generation, func(effectID uint64) bool { return containsID(named, effectID) }, row.Timestamp, negativeStatusLiveSlack, 0) {
		trigger.eligible = insertSortedID(trigger.eligible, candidate.ordinal)
	}
	p.statusTriggers.add(trigger)
	p.notifyStats.statusTriggers++
	if len(trigger.eligible) == 0 {
		p.notifyStats.statusTriggersForcingNothing++
	}
}

// negativeStatusCandidates lists the instances on target that can have executed a result of damageID at at.
func (p *processor) negativeStatusCandidates(target, damageID uint64, at time.Time) []statusCandidate {
	executes := func(effectID uint64) bool { return p.masters.negativeStatusExecutes(effectID, damageID) }
	return p.effects.statusCandidates(p.sessionNumber, target, p.effectHolderGeneration(target), executes, at, negativeStatusLiveSlack, statusEndedCarry)
}

// statusActors is what an instance's own settlement leaves as the possible cause of a result: the entities, or the reason
// none can be named.
type statusActors struct {
	entities map[uint64]struct{}
	reason   string
	// periodic is set for a status with its own timer.
	periodic bool
}

// ownSettlementActors returns the entities that can have caused an execution of candidate that no pending InvokePeriod
// explains: the owner of a timed status, every applier of a status without a period, and the instigator of any InvokePeriod
// that found the instance alive, forced nothing yet and could still explain a late result.
func (p *processor) ownSettlementActors(target, generation uint64, candidate statusCandidate, at time.Time) statusActors {
	settlement := p.masters.negativeStatusSettlementOf(candidate.effectID)
	actors := statusActors{entities: map[uint64]struct{}{}, periodic: settlement.periodic}
	if !candidate.hasSource {
		actors.reason = "a status instance that can have executed the result names no instigator"
		return actors
	}
	actors.entities[candidate.source] = struct{}{}
	if !settlement.periodic {
		if candidate.manyStackInstigators {
			actors.reason = "too many entities applied the status instance"
			return actors
		}
		for _, applier := range candidate.stackInstigators {
			actors.entities[applier] = struct{}{}
		}
	}
	for i := range p.statusTriggers.entries {
		trigger := &p.statusTriggers.entries[i]
		if trigger.session != p.sessionNumber || trigger.holder != target || trigger.generation != generation || at.Before(trigger.at) ||
			!containsID(trigger.eligible, candidate.ordinal) || containsID(trigger.used, candidate.ordinal) {
			continue
		}
		if !trigger.hasSource {
			actors.reason = "an InvokePeriod that names no instigator may have forced the execution"
			return actors
		}
		actors.entities[trigger.source] = struct{}{}
	}
	return actors
}

// pendingStatusTriggers returns, oldest first, the InvokePeriods applied on target within the carry window that found
// the instance alive and have not explained an execution of it yet.
func (p *processor) pendingStatusTriggers(target, generation, ordinal uint64, at time.Time) []*statusTrigger {
	var pending []*statusTrigger
	for i := range p.statusTriggers.entries {
		trigger := &p.statusTriggers.entries[i]
		if trigger.session != p.sessionNumber || trigger.holder != target || trigger.generation != generation || at.Before(trigger.at) ||
			at.Sub(trigger.at) > statusTriggerCarry || !containsID(trigger.eligible, ordinal) || containsID(trigger.used, ordinal) {
			continue
		}
		pending = append(pending, trigger)
	}
	return pending
}

// matchNegativeStatusExecution explains one negative-status result on target from its candidate instances. It runs for
// every such result, counted or not, so a trigger explains at most one execution of each instance it names.
func (p *processor) matchNegativeStatusExecution(target uint64, candidates []statusCandidate, at time.Time) negativeStatusExecution {
	if len(candidates) == 0 {
		return negativeStatusExecution{Settlement: statusSettlementUndetermined, Reason: "no live status instance on the target executes this DamageId"}
	}
	generation := p.effectHolderGeneration(target)
	p.statusTriggers.prune(at)
	// A forced execution: an InvokePeriod applied within the carry window found the instance alive and has not explained
	// an execution of it yet. The oldest such trigger is used up; all pending ones must agree on the role, and so must the
	// own settlement of every other instance that can have executed this result instead.
	for i, candidate := range candidates {
		pending := p.pendingStatusTriggers(target, generation, candidate.ordinal, at)
		if len(pending) == 0 {
			continue
		}
		chosen := pending[0]
		chosen.used = insertSortedID(chosen.used, candidate.ordinal)
		execution := negativeStatusExecution{Settlement: statusSettlementForced, StatusEffectID: candidate.effectID, TriggerEffectID: chosen.effectID}
		if candidate.hasSource {
			execution.Owner = candidate.source
		}
		actors := map[uint64]struct{}{}
		anonymous := false
		for _, trigger := range pending {
			if trigger.hasSource {
				actors[trigger.source] = struct{}{}
			} else {
				anonymous = true
			}
		}
		execution.Candidates = len(actors)
		if len(actors) > 1 || (anonymous && len(actors) > 0) {
			for _, trigger := range pending {
				trigger.ambiguous = insertSortedID(trigger.ambiguous, candidate.ordinal)
			}
			execution.Reason = "InvokePeriods of different instigators were pending for the status instance at once"
			return execution
		}
		if containsID(chosen.ambiguous, candidate.ordinal) {
			execution.Reason = "InvokePeriods of different instigators were pending for the status instance at once"
			return execution
		}
		if !chosen.hasSource {
			execution.Reason = "the InvokePeriod that forced the execution names no instigator"
			return execution
		}
		for j, other := range candidates {
			if j == i {
				continue
			}
			own := p.ownSettlementActors(target, generation, other, at)
			if own.reason != "" || len(own.entities) != 1 || !containsKey(own.entities, chosen.source) {
				execution.Candidates++
				execution.Reason = "another status instance that can have executed the result instead has another owner or applier"
				return execution
			}
		}
		execution.CreditEntity, execution.CreditBasis = chosen.source, roleBasisNegativeStatusTrigger
		return execution
	}
	// The instances' own settlement.
	execution := negativeStatusExecution{Settlement: statusSettlementOwn}
	entities := map[uint64]struct{}{}
	owners := map[uint64]struct{}{}
	forcible, periodicOnly := false, true
	for _, candidate := range candidates {
		own := p.ownSettlementActors(target, generation, candidate, at)
		if own.reason != "" {
			execution.Settlement, execution.Reason = statusSettlementUndetermined, own.reason
			return execution
		}
		if !own.periodic {
			periodicOnly = false
		}
		if p.masters.statusCanBeForced(candidate.effectID) {
			forcible = true
		}
		for entity := range own.entities {
			entities[entity] = struct{}{}
		}
		owners[candidate.source] = struct{}{}
	}
	if !periodicOnly {
		execution.Settlement = statusSettlementApplied
	}
	execution.Candidates = len(entities)
	if len(owners) == 1 {
		execution.Owner = candidates[0].source
		if allSameEffect(candidates) {
			execution.StatusEffectID = candidates[0].effectID
		}
	}
	if forcible && p.serverStreamIncomplete(at.Add(-statusTriggerDoubt), at) {
		execution.Settlement, execution.Reason = statusSettlementUndetermined, "server frames before the result are missing: an InvokePeriod that forced it may be lost"
		return execution
	}
	if len(entities) != 1 {
		switch {
		case len(owners) > 1:
			execution.StatusEffectID = 0
			execution.Reason = "the status instances that can have executed the result have different owners"
		case !periodicOnly && len(entities) > len(owners):
			execution.Reason = "several entities applied the status instance and the server does not name the application that caused the result"
		default:
			execution.Reason = "an InvokePeriod of another instigator that found the status instance alive may have forced the result"
		}
		return execution
	}
	for entity := range entities {
		execution.CreditEntity = entity
	}
	execution.CreditBasis = roleBasisNegativeStatusOwner
	return execution
}

func containsKey(set map[uint64]struct{}, key uint64) bool {
	_, ok := set[key]
	return ok
}

func allSameEffect(candidates []statusCandidate) bool {
	for _, candidate := range candidates {
		if candidate.effectID != candidates[0].effectID {
			return false
		}
	}
	return true
}

// negativeStatusRole resolves the credit entity of a local negative-status result to its local role, with the credit basis
// in place of the entity's own role basis. Nil when the entity is no local role or summon of one.
func (p *processor) negativeStatusRole(execution negativeStatusExecution) *CombatRole {
	if execution.CreditEntity == 0 {
		return nil
	}
	role := p.localRoleForEntity(execution.CreditEntity, entityIdentity{})
	if role == nil {
		return nil
	}
	credited := *role
	credited.Basis = execution.CreditBasis
	return &credited
}

// isNegativeStatusRoleBasis reports a role credit given through negativeStatusExecution.
func isNegativeStatusRoleBasis(basis string) bool {
	return basis == roleBasisNegativeStatusTrigger || basis == roleBasisNegativeStatusOwner
}

// IsNegativeStatusRoleBasis reports a stored role basis that credits a negative-status result to a role; Combat History
// reads the status split of its stored results with it.
func IsNegativeStatusRoleBasis(basis string) bool {
	return isNegativeStatusRoleBasis(basis)
}

// statusCandidates lists, ordered by handle, the instances on one holder whose effect id satisfies executes: the live ones
// (an instance past its estimated expiry by more than slack is no longer live) and, with endedCarry > 0, those the server
// removed within endedCarry before at.
func (t *EffectTracker) statusCandidates(session int, holder, generation uint64, executes func(uint64) bool, at time.Time, slack, endedCarry time.Duration) []statusCandidate {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var candidates []statusCandidate
	for key, instance := range t.instances {
		if key.session != session || key.holder != holder || key.generation != generation || !instance.hasEffectID || !executes(instance.effectID) {
			continue
		}
		if instance.timeKnown && !at.IsZero() && t.overdueLocked(instance, at) > slack.Seconds() {
			continue
		}
		candidates = append(candidates, statusCandidateOf(instance))
	}
	if endedCarry > 0 && !at.IsZero() {
		for i := len(t.ended) - 1; i >= 0; i-- {
			record := t.ended[i]
			if record.EndedAt.IsZero() || at.Sub(record.EndedAt) > endedCarry {
				break
			}
			instance := record.instance
			if instance == nil || record.Lifecycle != EffectLifecycleConfirmedRemoved || record.EndedAt.After(at) {
				continue
			}
			key := instance.key
			if key.session != session || key.holder != holder || key.generation != generation || !instance.hasEffectID || !executes(instance.effectID) {
				continue
			}
			candidates = append(candidates, statusCandidateOf(instance))
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].handle != candidates[j].handle {
			return candidates[i].handle < candidates[j].handle
		}
		return candidates[i].ordinal < candidates[j].ordinal
	})
	return candidates
}

func statusCandidateOf(instance *effectInstance) statusCandidate {
	candidate := statusCandidate{
		ordinal: instance.insertionOrdinal, handle: instance.key.handle, effectID: instance.effectID, source: instance.source, hasSource: instance.hasSource,
		stackInstigators: append([]uint64(nil), instance.stackInstigators...), manyStackInstigators: instance.manyStackInstigators,
	}
	switch instance.sourceRelation {
	case EffectRelationLocalRole, EffectRelationLocalOwned, EffectRelationLocalPlayer:
		candidate.local = instance.hasSource
	case EffectRelationNonPlayer:
		candidate.enemy = instance.hasSource
	}
	return candidate
}

// noteStackInstigator adds the InstigatorId of one stack application to the instance.
func (instance *effectInstance) noteStackInstigator(instigator uint64) {
	if containsID(instance.stackInstigators, instigator) {
		return
	}
	if len(instance.stackInstigators) >= maxStatusStackInstigators {
		instance.manyStackInstigators = true
		return
	}
	instance.stackInstigators = insertSortedID(instance.stackInstigators, instigator)
}

// insertSortedID adds id to a sorted ID list once.
func insertSortedID(ids []uint64, id uint64) []uint64 {
	index := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	if index < len(ids) && ids[index] == id {
		return ids
	}
	ids = append(ids, 0)
	copy(ids[index+1:], ids[index:])
	ids[index] = id
	return ids
}

// invokePeriodStatuses returns the status Buffs an InvokePeriod Buff forces, sorted; nil for any other Buff.
func (m *gameMaster) invokePeriodStatuses(effectID uint64) []uint64 {
	if m == nil {
		return nil
	}
	return m.invokePeriodTargets[effectID]
}

// statusCanBeForced reports whether some InvokePeriod Buff of the Master names the status Buff statusID.
func (m *gameMaster) statusCanBeForced(statusID uint64) bool {
	if m == nil {
		return false
	}
	for _, statuses := range m.invokePeriodTargets {
		if containsID(statuses, statusID) {
			return true
		}
	}
	return false
}

// NegativeStatusTerm returns the glossary name of the status behind a negative-status DamageId and the Buff whose icon
// shows it, or false when the DamageId is no negative status or its status Buffs name no single term.
func (m *gameMaster) negativeStatusTerm(damageID uint64) (negativeStatusTerm, bool) {
	if m == nil {
		return negativeStatusTerm{}, false
	}
	term, ok := m.negativeStatusTerms[damageID]
	return term, ok
}

// loadSQLiteNegativeStatusTriggers reads the InvokePeriod Buffs (buff.extra_effect_id 67) and the status Buffs their first
// extra-effect parameter names ("10041000#10051000"), keeping only names that are negative-status executors. It also names
// each negative-status DamageId by the glossary term its executing Buffs share. Both tables are optional.
func loadSQLiteNegativeStatusTriggers(db *sql.DB, master *gameMaster) error {
	defer nameNegativeStatuses(master)
	for _, table := range []string{"buff", "buff_extra_effect_parameters"} {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
	}
	rows, err := db.Query(`SELECT CAST(b.id AS TEXT), CAST(p.value AS TEXT) FROM buff b JOIN buff_extra_effect_parameters p
		ON CAST(p.owner_id AS TEXT) = CAST(b.id AS TEXT) AND p.ordinal = 0 WHERE CAST(b.extra_effect_id AS TEXT) = ?`, fmt.Sprint(invokePeriodExtraEffect))
	if err != nil {
		return fmt.Errorf("query InvokePeriod Buffs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawBuff, rawValue string
		if err := rows.Scan(&rawBuff, &rawValue); err != nil {
			return fmt.Errorf("read InvokePeriod Buff: %w", err)
		}
		buffID, err := parseMasterID(rawBuff, "buff.id")
		if err != nil {
			return err
		}
		var statuses []uint64
		for _, part := range strings.Split(rawValue, "#") {
			status, err := parseMasterID(strings.TrimSpace(part), "buff_extra_effect_parameters.value")
			if err != nil {
				continue // not a Buff id: InvokePeriod reads every part with Number(), a non-number names nothing
			}
			if _, executes := master.negativeStatusExecutors[status]; executes {
				statuses = insertSortedID(statuses, status)
			}
		}
		if len(statuses) > 0 {
			master.invokePeriodTargets[buffID] = statuses
		}
	}
	return rows.Err()
}

// nameNegativeStatuses names each negative-status DamageId by the one glossary term its executing status Buffs carry.
func nameNegativeStatuses(master *gameMaster) {
	type candidate struct {
		terms map[uint64]string
		icon  uint64
	}
	byDamage := map[uint64]*candidate{}
	for buffID, damageID := range master.negativeStatusExecutors {
		term, ok := master.buffTerms[buffID]
		if !ok || term.name == "" {
			continue
		}
		found := byDamage[damageID]
		if found == nil {
			found = &candidate{terms: map[uint64]string{}}
			byDamage[damageID] = found
		}
		found.terms[term.termID] = term.name
		if found.icon == 0 || buffID < found.icon {
			found.icon = buffID
		}
	}
	for damageID, found := range byDamage {
		if len(found.terms) != 1 {
			continue
		}
		for termID, name := range found.terms {
			master.negativeStatusTerms[damageID] = negativeStatusTerm{termID: termID, name: name, iconBuff: found.icon}
		}
	}
}
