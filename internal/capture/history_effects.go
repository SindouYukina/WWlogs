package capture

import "time"

// Effect lifecycles in the Combat History journal.
//
// The effect tracker (effect_state.go) journals each lifecycle change it accepts, under its own lock and in the order it
// accepts them, into the statistics' journal (history_journal.go). An entry repeats the tracker's decision together with the
// instance's state after it; it never decides anything, so the tracker's instances, counters and snapshots are identical
// whether history is on or off. A change the tracker rejects (stale, excluded, without a tracked instance) is not journaled.
// An instance that stops being tracked without an observed end (a scene or protocol session boundary, a statistics reset, a
// long overdue estimate) is journaled as untracked, never as removed, and an estimated expiry is never journaled at all.

// EffectChange names one journaled effect lifecycle change.
type EffectChange string

const (
	// EffectChangeApply is a server APPLY, or the local client's apply report, that started a tracked instance.
	EffectChangeApply EffectChange = "apply"
	// EffectChangeSnapshot is an instance first seen in an entity-active snapshot: it was already active then.
	EffectChangeSnapshot EffectChange = "snapshot"
	// EffectChangeRefresh is an APPLY or snapshot repeated for the active instance; stack and time are what it restated.
	EffectChangeRefresh EffectChange = "refresh"
	// EffectChangeStack is a stack-count message (BUFF_STACK_COUNT_CHANGE or the client's stack report) for the instance.
	EffectChangeStack EffectChange = "stack"
	// EffectChangeDuration is a duration message (BUFF_DURATION_CHANGE) for the instance.
	EffectChangeDuration EffectChange = "duration"
	// EffectChangeRemove is a REMOVE: the server's (Reason confirmed_removed) or the client's report (reported_removed or
	// reported_expired).
	EffectChangeRemove EffectChange = "remove"
	// EffectChangeHolderRemoved ends every instance of a holder entity the server removed (EntityRemoveNotify).
	EffectChangeHolderRemoved EffectChange = "holder_removed"
	// EffectChangeSuperseded ends an instance whose handle a later APPLY reused for another effect or source, so its REMOVE
	// was missed; the later APPLY's time bounds the end from above.
	EffectChangeSuperseded EffectChange = "superseded"
	// EffectChangeUntracked is an instance the tracker stopped following without an observed end (Reason).
	EffectChangeUntracked EffectChange = "untracked"
	// EffectChangeInstant is a server APPLY of an effect without a duration lifecycle (no handle): it has no later change.
	EffectChangeInstant EffectChange = "instant"
)

// How a journaled change is known.
const (
	// EffectObserved is a change a decoded message stated.
	EffectObserved = "observed"
	// EffectDerived is an end that follows from another observed fact: the holder's removal or the handle's reuse.
	EffectDerived = "derived"
	// EffectUntracked is the end of tracking: nothing is known about the effect in the game afterwards.
	EffectUntracked = "untracked"
)

// Reasons an instance stops being tracked without an observed end.
const (
	EffectUntrackedSceneChange   = "scene_change"
	EffectUntrackedSessionChange = "protocol_session_change"
	EffectUntrackedReset         = "statistics_reset"
	// EffectUntrackedOverdue frees an instance whose estimated expiry passed long ago (effectOverdueRetention) at the
	// capacity bound. The estimate is not an observation, so the instance is untracked, not removed.
	EffectUntrackedOverdue = "estimate_overdue"
	// EffectSupersededHandleReused is the reason of EffectChangeSuperseded.
	EffectSupersededHandleReused = "handle_reused"
)

// EffectActorIdentity is the runtime identity evidence of an effect holder or source at the time its instance was first
// seen, resolved with the same rules as combat events: the local role (if any) and the static monster or template type.
type EffectActorIdentity struct {
	Role *CombatRole
	Type *CombatActorType
}

func (a *EffectActorIdentity) clone() *EffectActorIdentity {
	if a == nil {
		return nil
	}
	copied := &EffectActorIdentity{}
	if a.Role != nil {
		role := *a.Role
		copied.Role = &role
	}
	if a.Type != nil {
		actor := *a.Type
		copied.Type = &actor
	}
	return copied
}

// merge keeps the known parts of a and fills the unknown ones from other.
func (a *EffectActorIdentity) merge(other *EffectActorIdentity) *EffectActorIdentity {
	switch {
	case other == nil:
		return a
	case a == nil:
		return other.clone()
	}
	if a.Role == nil && other.Role != nil {
		role := *other.Role
		a.Role = &role
	}
	if other.Type != nil && (a.Type == nil || (a.Type.MonsterID == 0 && a.Type.TemplateID == 0)) {
		actor := *other.Type
		a.Type = &actor
	}
	return a
}

// HistoryEffect is one journaled effect lifecycle change and the instance's state after it. Instance numbers the tracked
// instance within the capture session (handles are reused, instances are not); Sequence numbers the changes. AfterCombat is
// the number of combat results journaled before the change, which places it in the combat event order.
type HistoryEffect struct {
	Instance    uint64
	Sequence    uint64
	AfterCombat uint64
	Change      EffectChange
	Observation string
	// Evidence is the message evidence of an observed change (server_notify, server_snapshot, client_report), or of the fact
	// a derived end follows from; empty for untracked.
	Evidence EffectEvidence
	// Reason is the lifecycle of a remove, the reason of an untracked or superseded end, and empty otherwise.
	Reason     string
	ObservedAt time.Time

	SessionNumber    int
	HolderEntityID   uint64
	HolderGeneration uint64
	HandleID         uint64
	EffectID         uint64
	HasEffectID      bool
	DefinitionLinked bool
	SourceEntityID   uint64
	HasSource        bool
	HolderRelation   EffectEntityRelation
	SourceRelation   EffectEntityRelation
	HolderActor      *EffectActorIdentity
	SourceActor      *EffectActorIdentity
	Origin           EffectOrigin
	FirstObservedAt  time.Time

	Stack    uint64
	HasStack bool
	// Duration is the full duration the server last stated; Remaining is the remaining time it stated at BasisAt, when
	// TimeKnown. TimeReason says why the time is unknown. An expiry derived from them is an estimate, never a removal.
	Duration    float64
	HasDuration bool
	TimeKnown   bool
	Remaining   float64
	BasisAt     time.Time
	TimeReason  string
}

// journalEffect numbers and journals one effect change; instance is the instance's own number, assigned on its first
// journaled change. It returns without journaling unless the journal records events now.
func (j *historyJournal) journalEffect(instance *uint64, effect HistoryEffect) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.eventsLocked() {
		return
	}
	if *instance == 0 {
		j.effectInstances++
		*instance = j.effectInstances
	}
	j.effectSequence++
	effect.Instance, effect.Sequence, effect.AfterCombat = *instance, j.effectSequence, j.sequence
	j.addLocked(HistoryEntry{Kind: HistoryEffectChanged, Sequence: effect.Sequence, Effect: &effect})
}

// effectRejected journals an APPLY the tracker refused at its capacity bound.
func (j *historyJournal) effectRejected() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.eventsLocked() {
		j.addLocked(HistoryEntry{Kind: HistoryEffectRejected})
	}
}

// newJournaledEffectTracker creates the effect tracker of a processor, journaling into its statistics' history.
func newJournaledEffectTracker(statistics *DamageStatistics) *EffectTracker {
	tracker := NewEffectTracker(statistics.captureSessionID)
	tracker.history = statistics.history
	return tracker
}

// effectActor resolves the history-only identity of an effect holder or source with the rules combat events use. It runs on
// the processing goroutine, the owner of the entity resolver.
func (p *processor) effectActor(entityID uint64) *EffectActorIdentity {
	if p == nil || p.entities == nil || entityID == 0 {
		return nil
	}
	identity := resolveEntityIdentity(p.entities, p.masters, entityID)
	return &EffectActorIdentity{Role: p.localRoleForEntity(entityID, identity), Type: combatActorTypeOf(identity)}
}

// withHistoryActors adds the history-only identities to an APPLY or snapshot while Combat History records effects.
func (p *processor) withHistoryActors(event *EffectApplyEvent) {
	if !p.effects.recordsEffects() {
		return
	}
	event.HolderActor = p.effectActor(event.HolderEntityID)
	if event.HasSource {
		event.SourceActor = p.effectActor(event.SourceEntityID)
	}
}

// recordsEffects reports whether journaled effect changes are kept now, so callers skip resolving history-only identity.
func (t *EffectTracker) recordsEffects() bool {
	return t != nil && t.history.events()
}

// journalLocked journals one accepted change of instance with the instance's state after it. It runs under the tracker lock.
func (t *EffectTracker) journalLocked(instance *effectInstance, change EffectChange, observation, reason string, evidence EffectEvidence, at time.Time) {
	if t.history == nil {
		return
	}
	t.history.journalEffect(&instance.historyNumber, HistoryEffect{
		Change: change, Observation: observation, Evidence: evidence, Reason: reason, ObservedAt: at,
		SessionNumber: instance.key.session, HolderEntityID: instance.key.holder, HolderGeneration: instance.key.generation, HandleID: instance.key.handle,
		EffectID: instance.effectID, HasEffectID: instance.hasEffectID, DefinitionLinked: instance.definitionLinked,
		SourceEntityID: instance.source, HasSource: instance.hasSource,
		HolderRelation: instance.holderRelation, SourceRelation: instance.sourceRelation,
		HolderActor: instance.holderActor.clone(), SourceActor: instance.sourceActor.clone(),
		Origin: instance.origin, FirstObservedAt: instance.firstObservedAt,
		Stack: instance.stack, HasStack: instance.hasStack, Duration: instance.duration, HasDuration: instance.hasDuration,
		TimeKnown: instance.timeKnown, Remaining: instance.basisRemaining, BasisAt: instance.basisAt, TimeReason: instance.unknownReason,
	})
}

// untrackAllLocked journals every tracked instance as untracked for reason, before the tracker forgets them.
func (t *EffectTracker) untrackAllLocked(reason string) {
	if !t.recordsEffects() {
		return
	}
	for _, instance := range t.sortedInstancesLocked() {
		t.journalLocked(instance, EffectChangeUntracked, EffectUntracked, reason, "", t.highWater)
	}
}

// journalInstantLocked journals an APPLY of an effect without a duration lifecycle. Such an effect has no instance in the
// tracker; it gets an instance number of its own in the history. An effect between two non-player entities is skipped by
// the same rule that keeps such instances out of the tracker.
func (t *EffectTracker) journalInstantLocked(event EffectApplyEvent, evidence EffectEvidence) {
	if (event.HolderRelation == EffectRelationNonPlayer && event.SourceRelation == EffectRelationNonPlayer) || !t.recordsEffects() {
		return
	}
	instance := &effectInstance{
		key: effectKey{event.SessionNumber, event.HolderEntityID, event.HolderGeneration, event.HandleID}, effectID: event.EffectID, hasEffectID: event.HasEffectID,
		definitionLinked: event.DefinitionLinked, source: event.SourceEntityID, hasSource: event.HasSource,
		holderRelation: normalizeEffectRelation(event.HolderRelation), sourceRelation: normalizeEffectRelation(event.SourceRelation),
		stack: event.StackCount, hasStack: event.HasStackCount, origin: EffectOriginApply, evidence: evidence,
		holderActor: event.HolderActor, sourceActor: event.SourceActor, firstObservedAt: event.ObservedAt,
	}
	basis, duration, hasDuration := classifyEffectTime(event.DurationSeconds, event.RemainingSeconds)
	instance.hasDuration, instance.duration = hasDuration, duration
	instance.unknownReason = basis.reason
	t.journalLocked(instance, EffectChangeInstant, EffectObserved, "", evidence, event.ObservedAt)
}
