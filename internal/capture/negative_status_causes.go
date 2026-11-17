package capture

import (
	"time"
)

// Settlement causes of negative statuses without a period, the overflow that spreads such a settlement to other enemies,
// and the status instances the effect tracker leaves out (research/runs/2026-11-14/v0.7.0-negative-status-causal-closure).
//
// Evidence (Global 3.8.0 client JS and decoded ConfigDB, four Global 3.8.9 captures read through the generic schema):
//   - The server reports every application to a status it holds on a monster: the creation (ApplyGameplayEffectNotify
//     with the creator's InstigatorId) and each later stack application (BuffStackCountNotify with the applier's
//     InstigatorId), also an application at the stack limit, which keeps the count. In the captures such count-keeping
//     reports number over 10,000, every one at the Buff's limit and naming an applier; on a shared (StackingType 2)
//     instance another local role's applications are reported under that role's id (420 times) while the instance keeps
//     its creator as owner.
//   - The server's removal of a monster's status (RemoveGameplayEffectNotify) names nobody: no InstigatorId in any of the
//     2,000+ removals of the captures (expiry, early removal, death).
//   - The server reports a cause before its effect: an instance's creation before its on-creation execution (170 of
//     170), an InvokePeriod before the execution it forced (663 of 663), an execution before its echo.
//   - A status without a period settles on an application (official glossary): Glacio Chafe on every application, Fusion
//     Burst when an application overflows the stack limit. The client's overflow is an application while the count is at
//     the limit (BaseBuffComponent.OnBuffStackIncreased: limit <= old and limit <= new), and bClearStackOnOverflow then
//     removes the instance. Fusion Burst's overflow damages its holder and the enemies around it (glossary 850011; its
//     Buff's third parameter, 300, is the radius in the unit of the client's own range effects, such as
//     PeriodAddBuffToAdjacentEntity -> GetEntitiesInRangeWithLocation). Only the server executes it: the client's
//     AbnormalFire.OnExecute is empty and FormulaType 9 is not in the client's formula table.
//
// Rules:
//   - The cause of a result of a status without a period is an application the server reported in the same server moment,
//     at most statusTriggerCarry before the result. An application at the stack limit of an instance that clears on
//     overflow is the overflow: its instigator set the settlement off, whoever owns the instance or applied the other
//     stacks.
//   - Such an overflow can reach other enemies (statusSpreadsOnOverflow): a result of the same DamageId on any enemy of
//     the moment may be its result, also on an enemy without an instance of the status. The overflow is not used up by one
//     result, because one settlement has many results.
//   - A removal at the stack limit without a reported overflowing application leaves the actor unknown; server frames
//     missing in the moment, or since an instance's creation when no application of the moment explains a result, leave
//     the cause unproven.
//   - An instance the effect tracker leaves out (holder and source both unrelated to every player) is never the local
//     team's, and while it can have caused a result it keeps that result out of the local totals.

// statusCauseKind is what one server report says happened to a status instance.
type statusCauseKind uint8

const (
	// statusCauseCreated is the creation of an instance: its creator's application.
	statusCauseCreated statusCauseKind = iota + 1
	// statusCauseStacked is a stack report that raised the count: an application.
	statusCauseStacked
	// statusCauseAtLimit is a stack report that kept the count: an application at the stack limit, which overflows an
	// instance that clears on overflow.
	statusCauseAtLimit
	// statusCauseRemoved is the server's removal of the instance; it names nobody.
	statusCauseRemoved
)

// maxStatusCauses bounds the remembered causes. Each lives at most statusTriggerDoubt, and only statuses without a period
// are recorded, so a full team on many enemies stays far below the bound.
const maxStatusCauses = 1024

// maxExcludedStatuses bounds the remembered status instances the effect tracker leaves out.
const maxExcludedStatuses = 256

// statusCause is one server report about an instance of a status without a period.
type statusCause struct {
	key      effectKey
	effectID uint64
	at       time.Time
	kind     statusCauseKind
	actor    uint64
	hasActor bool
	// stack is the count after the report; for a removal, the count the instance had.
	stack uint64
	// holderEnemy is set when the holder is unrelated to every player.
	holderEnemy bool
	// local and enemy classify the instance's source as statusCandidate does; excluded marks an instance the effect tracker
	// leaves out (holder and source both unrelated to every player).
	local, enemy, excluded bool
}

// statusCauseLog keeps the causes of the last statusTriggerDoubt in arrival order.
type statusCauseLog struct {
	entries []statusCause
	dropped uint64
}

func (l *statusCauseLog) add(cause statusCause) {
	l.prune(cause.at)
	if len(l.entries) >= maxStatusCauses {
		l.entries = l.entries[1:]
		l.dropped++
	}
	l.entries = append(l.entries, cause)
}

func (l *statusCauseLog) prune(at time.Time) {
	kept := l.entries[:0]
	for _, entry := range l.entries {
		if !at.IsZero() && at.Sub(entry.at) > statusTriggerDoubt {
			continue
		}
		kept = append(kept, entry)
	}
	for i := len(kept); i < len(l.entries); i++ {
		l.entries[i] = statusCause{}
	}
	l.entries = kept
}

// excludedStatus is a negative-status instance the effect tracker leaves out because its holder and its source are both
// unrelated to every player: an enemy's or an NPC's status on an enemy.
type excludedStatus struct {
	effectID uint64
	stack    uint64
	// expires is the estimated end of the instance, zero without a finite duration; removedAt is the server's removal,
	// zero while the instance lives. A removed instance can still have executed a result statusEndedCarry later.
	expires, removedAt time.Time
}

// excludedStatusIndex holds the excluded instances by key, oldest first for the bound.
type excludedStatusIndex struct {
	byKey map[effectKey]*excludedStatus
	order []effectKey
}

func (x *excludedStatusIndex) put(key effectKey, status *excludedStatus) {
	if x.byKey == nil {
		x.byKey = make(map[effectKey]*excludedStatus)
	}
	if _, exists := x.byKey[key]; !exists {
		x.order = append(x.order, key)
	}
	x.byKey[key] = status
	for len(x.order) > maxExcludedStatuses {
		delete(x.byKey, x.order[0])
		x.order = x.order[1:]
	}
}

// markRemoved keeps a removed instance for statusEndedCarry: an overflow clears the instance before its result.
func (x *excludedStatusIndex) markRemoved(key effectKey, at time.Time) {
	if status, exists := x.byKey[key]; exists && status.removedAt.IsZero() {
		status.removedAt = at
	}
}

// statusInstanceInfo is what the settlement causes need of an instance the effect tracker keeps.
type statusInstanceInfo struct {
	effectID     uint64
	stack        uint64
	hasStack     bool
	local, enemy bool
}

// statusInstanceInfo returns the effect id, current count and source relation of a tracked instance; false when the
// tracker keeps no instance with an effect id under key.
func (t *EffectTracker) statusInstanceInfo(key effectKey) (statusInstanceInfo, bool) {
	if t == nil {
		return statusInstanceInfo{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	instance, ok := t.instances[key]
	if !ok || !instance.hasEffectID {
		return statusInstanceInfo{}, false
	}
	candidate := statusCandidateOf(instance)
	return statusInstanceInfo{effectID: instance.effectID, stack: instance.stack, hasStack: instance.hasStack, local: candidate.local, enemy: candidate.enemy}, true
}

// statusSpreadsOnOverflow reports a status Buff whose settlement can reach enemies other than its holder: it has no
// period and its overflow clears the instance (the settlement of Fusion Burst).
func (m *GameMaster) statusSpreadsOnOverflow(effectID uint64) bool {
	settlement := m.negativeStatusSettlementOf(effectID)
	return !settlement.Periodic && settlement.ClearsOnOverflow
}

// damageSpreadsOnOverflow reports a negative-status DamageId executed by a status Buff that spreads on overflow.
func (m *GameMaster) damageSpreadsOnOverflow(damageID uint64) bool {
	if m == nil {
		return false
	}
	for effectID, executed := range m.NegativeStatusExecutors {
		if executed == damageID && m.statusSpreadsOnOverflow(effectID) {
			return true
		}
	}
	return false
}

// atOrAboveStackLimit reports a count at or above the Buff's stack limit in the Master, the lowest limit an instance can
// have (a stack-limit modifier only raises it). False without a positive limit.
func (m *GameMaster) atOrAboveStackLimit(effectID, stack uint64) bool {
	if m == nil {
		return false
	}
	static, ok := m.buffStatic(effectID)
	return ok && static.HasStackLimit && static.StackLimit > 0 && stack >= static.StackLimit
}

// observeStatusCause records what one server lifecycle row says about a negative-status instance, before the effect
// tracker takes the row: the applications and removals of a status without a period, and the instances the tracker leaves
// out. knownHolderRelation and sourceRelation are the relations observeEffectLifecycle computed on apply; a nil
// knownHolderRelation is resolved only for a status instance.
func (p *Processor) observeStatusCause(row BuffLifecycleAuditRow, holder uint64, knownHolderRelation *EffectEntityRelation, sourceRelation EffectEntityRelation) {
	if p == nil || p.Masters == nil || row.HandleID == nil || !ValidEffectHandle(*row.HandleID) {
		return
	}
	key := effectKey{row.SessionNumber, holder, p.effectHolderGeneration(holder), *row.HandleID}
	holderEnemy := func() bool {
		if knownHolderRelation != nil {
			return *knownHolderRelation == EffectRelationNonPlayer
		}
		return p.EffectEntityRelation(holder) == EffectRelationNonPlayer
	}
	switch row.EventKind {
	case "GAMEPLAY_EFFECT_APPLY":
		if row.ID == nil {
			return
		}
		effectID := *row.ID
		if _, executes := p.Masters.negativeStatusExecutorDamage(effectID); !executes {
			return
		}
		var stack uint64 = 1
		if row.StackCount != nil {
			stack = *row.StackCount
		}
		holderRelation := EffectRelationUnknown
		if knownHolderRelation != nil {
			holderRelation = *knownHolderRelation
		}
		if holderRelation == EffectRelationNonPlayer && sourceRelation == EffectRelationNonPlayer {
			status := &excludedStatus{effectID: effectID, stack: stack}
			if row.DurationSeconds != nil && *row.DurationSeconds > 0 && !row.Timestamp.IsZero() {
				status.expires = row.Timestamp.Add(secondsToDuration(float64(*row.DurationSeconds)))
			}
			p.excludedStatuses.put(key, status)
			p.NotifyStats.ExcludedStatusInstances++
			p.addStatusCause(statusCause{key: key, effectID: effectID, at: row.Timestamp, kind: statusCauseCreated, stack: stack,
				holderEnemy: true, enemy: true, excluded: true}, row.InstigatorID)
			return
		}
		cause := statusCause{key: key, effectID: effectID, at: row.Timestamp, kind: statusCauseCreated, stack: stack, holderEnemy: holderEnemy()}
		switch sourceRelation {
		case EffectRelationLocalRole, EffectRelationLocalOwned, EffectRelationLocalPlayer:
			cause.local = true
		case EffectRelationNonPlayer:
			cause.enemy = true
		}
		if info, tracked := p.Effects.statusInstanceInfo(key); tracked && info.effectID == effectID && info.hasStack {
			// A repeated APPLY of a live handle is a further application to the same instance.
			cause.kind = applicationKind(info.stack, stack)
			if cause.kind == 0 {
				return
			}
		}
		p.addStatusCause(cause, row.InstigatorID)
	case "BUFF_STACK_COUNT_CHANGE":
		if row.StackCount == nil {
			return
		}
		next := *row.StackCount
		if excluded, ok := p.excludedStatuses.byKey[key]; ok && excluded.removedAt.IsZero() {
			kind := applicationKind(excluded.stack, next)
			excluded.stack = next
			if row.RemainingDuration != nil && *row.RemainingDuration > 0 && !row.Timestamp.IsZero() {
				excluded.expires = row.Timestamp.Add(secondsToDuration(float64(*row.RemainingDuration)))
			}
			if kind != 0 {
				p.addStatusCause(statusCause{key: key, effectID: excluded.effectID, at: row.Timestamp, kind: kind, stack: next, holderEnemy: true, enemy: true, excluded: true}, row.InstigatorID)
			}
			return
		}
		info, tracked := p.Effects.statusInstanceInfo(key)
		if !tracked || !info.hasStack {
			return
		}
		if _, executes := p.Masters.negativeStatusExecutorDamage(info.effectID); !executes {
			return
		}
		kind := applicationKind(info.stack, next)
		if kind == 0 {
			return
		}
		if p.Masters.negativeStatusSettlementOf(info.effectID).Periodic {
			return
		}
		p.addStatusCause(statusCause{key: key, effectID: info.effectID, at: row.Timestamp, kind: kind, stack: next,
			holderEnemy: holderEnemy(), local: info.local, enemy: info.enemy}, row.InstigatorID)
	case "GAMEPLAY_EFFECT_REMOVE":
		if excluded, ok := p.excludedStatuses.byKey[key]; ok {
			p.excludedStatuses.markRemoved(key, row.Timestamp)
			p.addStatusCause(statusCause{key: key, effectID: excluded.effectID, at: row.Timestamp, kind: statusCauseRemoved, stack: excluded.stack, holderEnemy: true, enemy: true, excluded: true}, nil)
			return
		}
		info, tracked := p.Effects.statusInstanceInfo(key)
		if !tracked {
			return
		}
		if _, executes := p.Masters.negativeStatusExecutorDamage(info.effectID); !executes {
			return
		}
		if p.Masters.negativeStatusSettlementOf(info.effectID).Periodic {
			return
		}
		p.addStatusCause(statusCause{key: key, effectID: info.effectID, at: row.Timestamp, kind: statusCauseRemoved, stack: info.stack,
			holderEnemy: holderEnemy(), local: info.local, enemy: info.enemy}, nil)
	}
}

// applicationKind classifies a stack report from the count before it: a raise is an application, a kept count an
// application at the stack limit; a decrease applies nothing (zero).
func applicationKind(before, after uint64) statusCauseKind {
	switch {
	case after > before:
		return statusCauseStacked
	case after == before && after > 0:
		return statusCauseAtLimit
	default:
		return 0
	}
}

// addStatusCause keeps a cause of a status without a period; the causes of a timed status settle nothing.
func (p *Processor) addStatusCause(cause statusCause, instigator *uint64) {
	if p.Masters.negativeStatusSettlementOf(cause.effectID).Periodic {
		return
	}
	if instigator != nil && *instigator != 0 {
		cause.actor, cause.hasActor = *instigator, true
	}
	p.statusCauses.add(cause)
}

// excludedStatusExecutes reports an excluded instance on target that can have executed a result of damageID at at: alive,
// or removed by the server at most statusEndedCarry before.
func (p *Processor) excludedStatusExecutes(target, damageID uint64, at time.Time) bool {
	generation := p.effectHolderGeneration(target)
	for key, status := range p.excludedStatuses.byKey {
		if key.session != p.SessionNumber || key.holder != target || key.generation != generation || !p.Masters.negativeStatusExecutes(status.effectID, damageID) {
			continue
		}
		if !status.removedAt.IsZero() && at.Sub(status.removedAt) > statusEndedCarry {
			continue
		}
		if status.removedAt.IsZero() && !status.expires.IsZero() && !at.IsZero() && at.Sub(status.expires) > negativeStatusLiveSlack {
			continue
		}
		return true
	}
	return false
}

// spreadCandidate is an instance on another enemy whose overflow in the moment of a result can have spread the result.
type spreadCandidate struct {
	key      effectKey
	effectID uint64
	// actors are the instigators of the overflowing applications; unknown is set when one names nobody, or when the server
	// removed the instance at its stack limit in the moment without reporting the application that overflowed it.
	actors  map[uint64]struct{}
	unknown bool
	// local, enemy and excluded classify the instance's source.
	local, enemy, excluded bool
	removedAtLimit         bool
}

// spreadCandidates lists the instances on enemies other than target whose overflow, at most statusTriggerCarry before at,
// can have spread a result of damageID onto target, ordered by their first report.
func (p *Processor) spreadCandidates(target, damageID uint64, at time.Time) []spreadCandidate {
	if p == nil || !p.Masters.damageSpreadsOnOverflow(damageID) {
		return nil
	}
	var found []spreadCandidate
	index := map[effectKey]int{}
	for _, cause := range p.statusCauses.entries {
		if cause.key.session != p.SessionNumber || cause.key.holder == target || !cause.holderEnemy || cause.at.After(at) || at.Sub(cause.at) > statusTriggerCarry {
			continue
		}
		if !p.Masters.negativeStatusExecutes(cause.effectID, damageID) || !p.Masters.statusSpreadsOnOverflow(cause.effectID) {
			continue
		}
		atLimit := cause.kind == statusCauseAtLimit
		removedAtLimit := cause.kind == statusCauseRemoved && p.Masters.atOrAboveStackLimit(cause.effectID, cause.stack)
		if !atLimit && !removedAtLimit {
			continue
		}
		i, seen := index[cause.key]
		if !seen {
			i = len(found)
			index[cause.key] = i
			found = append(found, spreadCandidate{key: cause.key, effectID: cause.effectID, actors: map[uint64]struct{}{}, local: cause.local, enemy: cause.enemy, excluded: cause.excluded})
		}
		candidate := &found[i]
		if atLimit {
			if cause.hasActor {
				candidate.actors[cause.actor] = struct{}{}
			} else {
				candidate.unknown = true
			}
		} else {
			candidate.removedAtLimit = true
		}
	}
	for i := range found {
		if found[i].removedAtLimit && len(found[i].actors) == 0 {
			found[i].unknown = true
		}
	}
	return found
}

// appliedSettlementActors returns the entities whose application can have caused a result of candidate, an instance of a
// status without a period, at at:
//   - the instigators of the applications at the stack limit the server reported in the moment, when there are any (the
//     overflow sets the settlement off);
//   - no entity, with the reason, when the instance clears on overflow and the server removed it at its stack limit in
//     the moment without reporting the overflowing application;
//   - otherwise the instigators of its applications in the moment;
//   - otherwise, when no application of the moment explains the result, every applier of the instance, provided no server
//     frame is missing since its creation (one may have carried another applier's application).
func (p *Processor) appliedSettlementActors(candidate statusCandidate, at time.Time) statusActors {
	actors := statusActors{entities: map[uint64]struct{}{}, applied: true}
	settlement := p.Masters.negativeStatusSettlementOf(candidate.effectID)
	var atLimit, applied []statusCause
	removedAtLimit := false
	for _, cause := range p.statusCauses.entries {
		if cause.key != candidate.key || cause.at.After(at) || at.Sub(cause.at) > statusTriggerCarry {
			continue
		}
		switch cause.kind {
		case statusCauseAtLimit:
			atLimit = append(atLimit, cause)
		case statusCauseCreated, statusCauseStacked:
			applied = append(applied, cause)
		case statusCauseRemoved:
			if settlement.ClearsOnOverflow && p.Masters.atOrAboveStackLimit(cause.effectID, cause.stack) {
				removedAtLimit = true
			}
		}
	}
	switch {
	case len(atLimit) > 0:
		applied = atLimit
	case removedAtLimit:
		actors.reason = "the server removed the status instance at its stack limit without reporting the application that overflowed it"
		return actors
	}
	if len(applied) > 0 {
		for _, cause := range applied {
			if !cause.hasActor {
				actors.reason = "an application of the status instance in the moment of the result names no instigator"
				return actors
			}
			actors.entities[cause.actor] = struct{}{}
		}
		return actors
	}
	if !candidate.hasSource {
		actors.reason = "a status instance that can have executed the result names no instigator"
		return actors
	}
	if candidate.manyStackInstigators {
		actors.reason = "too many entities applied the status instance"
		return actors
	}
	if p.serverStreamIncomplete(candidate.createdAt, at) {
		actors.reason = "server frames are missing since the status instance was created: an application of another entity may be lost"
		return actors
	}
	actors.entities[candidate.source] = struct{}{}
	for _, applier := range candidate.stackInstigators {
		actors.entities[applier] = struct{}{}
	}
	return actors
}
