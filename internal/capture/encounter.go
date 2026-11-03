package capture

import (
	"fmt"
	"math"
	"time"
)

// EncounterState is the lifecycle position of the encounter tracker or of one encounter.
type EncounterState string

const (
	EncounterIdle            EncounterState = "IDLE"
	EncounterActive          EncounterState = "ACTIVE"
	EncounterEndingCandidate EncounterState = "ENDING_CANDIDATE"
	EncounterFinished        EncounterState = "FINISHED"
)

// EncounterOutcome states how well an encounter's boundaries are supported. OPEN means not finished yet.
type EncounterOutcome string

const (
	EncounterOutcomeOpen EncounterOutcome = "OPEN"
	// EncounterOutcomeConfirmed: the server's local battle-state signal opened and closed the encounter.
	EncounterOutcomeConfirmed EncounterOutcome = "CONFIRMED"
	// EncounterOutcomeInferred: at least one boundary comes from observed activity rather than the signal.
	EncounterOutcomeInferred EncounterOutcome = "INFERRED"
	// EncounterOutcomeInterrupted: a scene change, protocol session change, or statistics reset cut it off.
	EncounterOutcomeInterrupted EncounterOutcome = "INTERRUPTED"
	// EncounterOutcomeIncomplete: the capture ended while the encounter was still active.
	EncounterOutcomeIncomplete EncounterOutcome = "INCOMPLETE"
)

// Boundary bases recorded on every encounter so each start and end can be explained.
const (
	encounterStartBattleState   = "battle_state_enter"
	encounterStartFirstActivity = "first_confirmed_activity"
	encounterEndBattleState     = "battle_state_exit"
	encounterEndInactivity      = "inactivity_timeout"
	encounterEndSceneTransition = "scene_transition"
	encounterEndSessionChange   = "protocol_session_change"
	encounterEndStatisticsReset = "statistics_reset"
	encounterEndCaptureEnd      = "capture_end"
)

const (
	// encounterReentryGrace merges a battle exit followed closely by a new enter into one encounter. Captured wave
	// transitions inside instances re-entered after 0.21-0.30 s; distinct open-world fights were 1.86 s apart or more.
	encounterReentryGrace = time.Second
	// encounterPreRollWindow lets hits observed shortly before the enter signal join the encounter they started.
	// Captured first hits preceded the signal by up to 0.28 s.
	encounterPreRollWindow = 2 * time.Second
	// encounterInactivityTimeout ends an encounter only when no battle-state signal governs it. The end time is the
	// last confirmed activity, never the moment the timeout fired.
	encounterInactivityTimeout = 30 * time.Second
	maxCompletedEncounters     = 64
	maxEncounterPreludeEvents  = 256
)

// EncounterTotals partitions the same statistics events by encounter. It is not a separate accumulator: every
// value here comes from an event the interval totals also counted.
type EncounterTotals struct {
	DamageDealt                int64  `json:"damageDealt"`
	DamageTaken                int64  `json:"damageTaken"`
	OutgoingEvents             uint64 `json:"outgoingEvents"`
	IncomingEvents             uint64 `json:"incomingEvents"`
	UnattributedOutgoingDamage int64  `json:"unattributedOutgoingDamage"`
	UnattributedIncomingDamage int64  `json:"unattributedIncomingDamage"`
	UnattributedOutgoingEvents uint64 `json:"unattributedOutgoingEvents"`
	UnattributedIncomingEvents uint64 `json:"unattributedIncomingEvents"`
}

func (t *EncounterTotals) merge(other EncounterTotals) {
	t.DamageDealt += other.DamageDealt
	t.DamageTaken += other.DamageTaken
	t.OutgoingEvents += other.OutgoingEvents
	t.IncomingEvents += other.IncomingEvents
	t.UnattributedOutgoingDamage += other.UnattributedOutgoingDamage
	t.UnattributedIncomingDamage += other.UnattributedIncomingDamage
	t.UnattributedOutgoingEvents += other.UnattributedOutgoingEvents
	t.UnattributedIncomingEvents += other.UnattributedIncomingEvents
}

func (t *EncounterTotals) add(record CombatEventRecord) {
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		t.DamageDealt += record.Amount
		t.OutgoingEvents++
	case CombatFlowLocalIncoming:
		t.DamageTaken += record.Amount
		t.IncomingEvents++
	case CombatFlowUnattributedOutgoing:
		t.UnattributedOutgoingDamage += record.Amount
		t.UnattributedOutgoingEvents++
	case CombatFlowUnattributedIncoming:
		t.UnattributedIncomingDamage += record.Amount
		t.UnattributedIncomingEvents++
	}
}

func (t EncounterTotals) confirmedActivity() bool {
	return t.OutgoingEvents+t.IncomingEvents > 0
}

// EncounterSnapshot is one encounter with its boundaries, the evidence for each, and its Encounter DPS.
type EncounterSnapshot struct {
	EncounterID          string           `json:"encounterId"`
	CaptureSessionID     uint64           `json:"captureSessionId"`
	StatisticsIntervalID uint64           `json:"statisticsIntervalId"`
	Sequence             uint64           `json:"sequence"`
	ProtocolSession      int              `json:"protocolSession"`
	State                EncounterState   `json:"state"`
	Outcome              EncounterOutcome `json:"outcome"`
	// StartedAt and EndedAt are capture-observed times. They bound the observable encounter; they are not claimed
	// to equal the game's internal combat start or end.
	StartedAt      time.Time `json:"startedAt"`
	StartBasis     string    `json:"startBasis"`
	StartConfirmed bool      `json:"startConfirmed"`
	BattleEntered  time.Time `json:"battleStateEnteredAt,omitempty"`
	EndedAt        time.Time `json:"endedAt,omitempty"`
	EndBasis       string    `json:"endBasis,omitempty"`
	// EndDecidedAt is when the end became final, for example when the re-entry grace or inactivity timeout expired.
	// It is never used as the end time itself.
	EndDecidedAt    time.Time       `json:"endDecidedAt,omitempty"`
	FirstActivityAt time.Time       `json:"firstActivityAt,omitempty"`
	LastActivityAt  time.Time       `json:"lastActivityAt,omitempty"`
	Reentries       uint64          `json:"reentries"`
	Totals          EncounterTotals `json:"totals"`
	EncounterDPS    DPSResult       `json:"encounterDps"`
}

// EncounterTrackerSnapshot is the encounter view of one statistics interval.
type EncounterTrackerSnapshot struct {
	State       EncounterState      `json:"state"`
	BattleState string              `json:"battleState"`
	Current     *EncounterSnapshot  `json:"current,omitempty"`
	Completed   []EncounterSnapshot `json:"completed"`
	// CompletedDropped counts finished encounters evicted from the bounded Completed list; their totals are kept.
	CompletedDropped       uint64          `json:"completedDropped"`
	CompletedDroppedTotals EncounterTotals `json:"completedDroppedTotals"`
	// DiscardedEmpty counts battle-state intervals that ended with no confirmed local damage dealt or taken.
	DiscardedEmpty     uint64          `json:"discardedEmpty"`
	OutsideEncounters  EncounterTotals `json:"outsideEncounters"`
	EventsWithoutTime  uint64          `json:"eventsWithoutTime"`
	BattleStateSignals uint64          `json:"battleStateSignals"`
	// UnanchoredBattleStateSignals were rejected because their PlayerId was not proven to be the local player.
	UnanchoredBattleStateSignals uint64 `json:"unanchoredBattleStateSignals"`
}

// EncounterTransition is one lifecycle change, reported to development diagnostics.
type EncounterTransition struct {
	Kind        string             `json:"kind"`
	EncounterID string             `json:"encounterId"`
	At          time.Time          `json:"at"`
	State       EncounterState     `json:"state"`
	Basis       string             `json:"basis,omitempty"`
	Encounter   *EncounterSnapshot `json:"encounter,omitempty"`
}

type encounterRecord struct {
	sequence        uint64
	protocolSession int
	state           EncounterState
	startAt         time.Time
	startBasis      string
	startConfirmed  bool
	battleEntered   time.Time
	endAt           time.Time
	endBasis        string
	endDecidedAt    time.Time
	firstActivity   time.Time
	lastActivity    time.Time
	reentries       uint64
	totals          EncounterTotals
}

// encounterTracker owns the encounter lifecycle of one statistics interval. It is driven only by capture-observed
// times, so a replay produces the same segmentation regardless of replay speed. It is not safe for concurrent use;
// DamageStatistics calls it under its own lock.
type encounterTracker struct {
	captureSessionID uint64
	intervalID       uint64
	nextSequence     uint64
	current          *encounterRecord
	completed        []EncounterSnapshot
	completedDropped uint64
	droppedTotals    EncounterTotals
	discardedEmpty   uint64
	// prelude holds events seen out of battle while the battle-state signal governs boundaries.
	prelude           []CombatEventRecord
	outside           EncounterTotals
	eventsWithoutTime uint64
	signalKnown       bool
	inBattle          bool
	highWater         time.Time
	// boundaryFloor is the end of the latest encounter that ended in this interval (or the interval's start).
	// No later encounter may include an event at or before it, so two encounters never share a time span even
	// when a response arrives late or out of order.
	boundaryFloor time.Time
	// boundaryFloorSession is the protocol session the floor belongs to; 0 applies it to every session.
	boundaryFloorSession int
	battleStateSignals   uint64
	// unanchoredBattleStateSignals counts signals rejected because their PlayerId was not proven local.
	unanchoredBattleStateSignals uint64
	recordTransitions            bool
	transitions                  []EncounterTransition
}

func newEncounterTracker(captureSessionID, intervalID uint64) *encounterTracker {
	return &encounterTracker{captureSessionID: captureSessionID, intervalID: intervalID}
}

// nextInterval returns the tracker for a new statistics interval. The game's battle state and the knowledge that
// the signal exists carry over; encounters, totals, and the prelude belong to the old interval and do not.
func (t *encounterTracker) nextInterval(intervalID uint64) *encounterTracker {
	next := newEncounterTracker(t.captureSessionID, intervalID)
	next.signalKnown, next.inBattle, next.highWater = t.signalKnown, t.inBattle, t.highWater
	// Events observed before the reset belong to the old interval and cannot start or extend an encounter here.
	next.boundaryFloor = t.highWater
	next.recordTransitions = t.recordTransitions
	return next
}

func (t *encounterTracker) encounterID(sequence uint64) string {
	return fmt.Sprintf("%d-%d-%d", t.captureSessionID, t.intervalID, sequence)
}

func (t *encounterTracker) takeTransitions() []EncounterTransition {
	transitions := t.transitions
	t.transitions = nil
	return transitions
}

func (t *encounterTracker) emit(kind string, record *encounterRecord, at time.Time, basis string, detail *EncounterSnapshot) {
	if !t.recordTransitions {
		return
	}
	t.transitions = append(t.transitions, EncounterTransition{
		Kind: kind, EncounterID: t.encounterID(record.sequence), At: at, State: record.state, Basis: basis, Encounter: detail,
	})
}

// observeTime advances the capture clock. Backward times are ignored for timers so a late frame from another
// stream can never reopen or extend a decision already taken.
func (t *encounterTracker) observeTime(at time.Time) {
	if at.IsZero() || !at.After(t.highWater) {
		return
	}
	t.highWater = at
	if t.current != nil {
		switch {
		case t.current.state == EncounterEndingCandidate && at.Sub(t.current.endAt) > encounterReentryGrace:
			t.finish(at, "")
		case t.current.state == EncounterActive && !t.inBattle && t.current.battleEntered.IsZero() &&
			at.Sub(t.current.lastActivity) > encounterInactivityTimeout:
			t.current.endAt, t.current.endBasis = t.current.lastActivity, encounterEndInactivity
			t.finish(at, "")
		}
	}
	t.expirePrelude(at)
}

// expirePrelude moves prelude events that can no longer join an encounter to the outside totals.
func (t *encounterTracker) expirePrelude(now time.Time) {
	if t.current != nil && t.current.state == EncounterEndingCandidate {
		return
	}
	kept := t.prelude[:0]
	for _, record := range t.prelude {
		if now.Sub(record.ObservedAt) > encounterPreRollWindow {
			t.outside.add(record)
			continue
		}
		kept = append(kept, record)
	}
	t.prelude = kept
}

func countsTowardEncounters(flow CombatFlow) bool {
	switch flow {
	case CombatFlowLocalOutgoing, CombatFlowLocalIncoming, CombatFlowUnattributedOutgoing, CombatFlowUnattributedIncoming:
		return true
	}
	return false
}

func isConfirmedActivity(flow CombatFlow) bool {
	return flow == CombatFlowLocalOutgoing || flow == CombatFlowLocalIncoming
}

// Event assignments reported to development diagnostics.
const (
	encounterAssignmentNotCounted = "not_counted"
	encounterAssignmentPrelude    = "prelude"
	encounterAssignmentOutside    = "outside"
	encounterAssignmentNoTime     = "outside_without_time"
)

// observeEvent assigns one statistics event to the open encounter, the prelude, or the outside totals, and
// returns where it went. A prelude event can still join an encounter later.
func (t *encounterTracker) observeEvent(record CombatEventRecord) string {
	if !countsTowardEncounters(record.Flow) {
		return encounterAssignmentNotCounted
	}
	if record.ObservedAt.IsZero() {
		t.eventsWithoutTime++
		t.outside.add(record)
		return encounterAssignmentNoTime
	}
	t.observeTime(record.ObservedAt)
	if t.current != nil && t.current.protocolSession != record.Key.ProtocolSession {
		t.interrupt(record.ObservedAt, encounterEndSessionChange)
	}
	confirmed := isConfirmedActivity(record.Flow)
	if t.belowBoundaryFloor(record) {
		// The event belongs to a span an earlier encounter (or interval) already closed.
		t.outside.add(record)
		return encounterAssignmentOutside
	}
	if t.current != nil && (t.current.state == EncounterActive || !record.ObservedAt.After(t.current.endAt)) {
		if record.ObservedAt.Before(t.current.startAt) {
			if t.current.startAt.Sub(record.ObservedAt) > encounterPreRollWindow {
				t.outside.add(record)
				return encounterAssignmentOutside
			}
			if confirmed {
				t.current.startAt, t.current.startBasis = record.ObservedAt, encounterStartFirstActivity
			}
		}
		t.addToCurrent(record)
		return t.encounterID(t.current.sequence)
	}
	if t.current != nil || (t.signalKnown && !t.inBattle) {
		// After an exit (ENDING_CANDIDATE) or out of battle, an event waits for a re-entry or a new enter.
		t.addPrelude(record)
		return encounterAssignmentPrelude
	}
	if !confirmed {
		t.outside.add(record)
		return encounterAssignmentOutside
	}
	t.open(record.ObservedAt, record.Key.ProtocolSession, encounterStartFirstActivity, false)
	t.addToCurrent(record)
	return t.encounterID(t.current.sequence)
}

// belowBoundaryFloor reports whether an event falls at or before the end of an earlier encounter of its own
// protocol session. Events of a newer session are not compared with an older session's floor.
func (t *encounterTracker) belowBoundaryFloor(record CombatEventRecord) bool {
	if t.boundaryFloor.IsZero() || (t.boundaryFloorSession != 0 && t.boundaryFloorSession != record.Key.ProtocolSession) {
		return false
	}
	return !record.ObservedAt.After(t.boundaryFloor)
}

func (t *encounterTracker) addPrelude(record CombatEventRecord) {
	if len(t.prelude) >= maxEncounterPreludeEvents {
		t.outside.add(t.prelude[0])
		t.prelude = t.prelude[1:]
	}
	t.prelude = append(t.prelude, record)
}

func (t *encounterTracker) addToCurrent(record CombatEventRecord) {
	t.current.totals.add(record)
	if !isConfirmedActivity(record.Flow) {
		return
	}
	if t.current.firstActivity.IsZero() || record.ObservedAt.Before(t.current.firstActivity) {
		t.current.firstActivity = record.ObservedAt
	}
	if record.ObservedAt.After(t.current.lastActivity) {
		t.current.lastActivity = record.ObservedAt
	}
}

func (t *encounterTracker) open(at time.Time, protocolSession int, basis string, confirmed bool) {
	t.nextSequence++
	t.current = &encounterRecord{
		sequence: t.nextSequence, protocolSession: protocolSession, state: EncounterActive,
		startAt: at, startBasis: basis, startConfirmed: confirmed,
	}
	if basis == encounterStartBattleState {
		t.current.battleEntered = at
	}
	t.emit("opened", t.current, at, basis, nil)
}

// absorbPrelude moves prelude events into the open encounter. Confirmed activity within the pre-roll window
// before the start moves the start back to that activity; older events go to the outside totals.
func (t *encounterTracker) absorbPrelude() {
	for _, record := range t.prelude {
		if t.current.startAt.Sub(record.ObservedAt) > encounterPreRollWindow || record.Key.ProtocolSession != t.current.protocolSession ||
			t.belowBoundaryFloor(record) {
			t.outside.add(record)
			continue
		}
		if record.ObservedAt.Before(t.current.startAt) && isConfirmedActivity(record.Flow) {
			t.current.startAt, t.current.startBasis = record.ObservedAt, encounterStartFirstActivity
		}
		t.addToCurrent(record)
	}
	t.prelude = nil
}

// observeBattleState applies one local battle-state signal.
func (t *encounterTracker) observeBattleState(inBattle bool, at time.Time, protocolSession int) {
	if at.IsZero() {
		return
	}
	t.observeTime(at)
	t.signalKnown = true
	t.battleStateSignals++
	if t.current != nil && t.current.protocolSession != protocolSession {
		t.interrupt(at, encounterEndSessionChange)
	}
	if !inBattle {
		t.inBattle = false
		if t.current != nil && t.current.state == EncounterActive {
			t.current.state = EncounterEndingCandidate
			t.current.endAt, t.current.endBasis = at, encounterEndBattleState
			t.emit("ending_candidate", t.current, at, encounterEndBattleState, nil)
		}
		return
	}
	t.inBattle = true
	if t.current != nil {
		switch {
		case t.current.state == EncounterEndingCandidate:
			// observeTime has already finished the encounter if the grace window had passed.
			t.current.state = EncounterActive
			t.current.endAt, t.current.endBasis = time.Time{}, ""
			t.current.reentries++
			t.emit("resumed", t.current, at, encounterStartBattleState, nil)
			t.absorbPrelude()
			return
		case !t.current.battleEntered.IsZero():
			return
		case !t.current.firstActivity.Before(at.Add(-encounterPreRollWindow)):
			// The activity that opened this encounter is the same fight the signal now confirms.
			t.current.battleEntered, t.current.startConfirmed = at, true
			if at.Before(t.current.startAt) {
				t.current.startAt, t.current.startBasis = at, encounterStartBattleState
			}
			return
		default:
			t.current.endAt, t.current.endBasis = t.current.lastActivity, encounterEndInactivity
			t.finish(at, "")
		}
	}
	t.open(at, protocolSession, encounterStartBattleState, true)
	t.absorbPrelude()
}

// observeSceneTransition and observeSessionChange end whatever is open. The battle state is unknown afterwards
// until the server signals again, which counts as out of battle for boundary decisions.
func (t *encounterTracker) observeSceneTransition(at time.Time) {
	t.observeTime(at)
	t.interrupt(at, encounterEndSceneTransition)
}

func (t *encounterTracker) observeSessionChange(at time.Time) {
	t.observeTime(at)
	t.interrupt(at, encounterEndSessionChange)
}

func (t *encounterTracker) interrupt(at time.Time, basis string) {
	t.inBattle = false
	if t.current != nil {
		if t.current.state == EncounterActive {
			end := at
			if end.IsZero() || end.Before(t.current.lastActivity) {
				end = t.current.lastActivity
			}
			t.current.endAt, t.current.endBasis = end, basis
		}
		t.finish(at, basis)
	}
	t.flushPrelude()
}

// finishCapture closes the tracker at the end of a capture or replay.
func (t *encounterTracker) finishCapture() {
	at := t.highWater
	if t.current != nil {
		if t.current.state == EncounterActive {
			end := at
			if end.Before(t.current.lastActivity) {
				end = t.current.lastActivity
			}
			t.current.endAt, t.current.endBasis = end, encounterEndCaptureEnd
		}
		t.finish(at, encounterEndCaptureEnd)
	}
	t.flushPrelude()
}

func (t *encounterTracker) flushPrelude() {
	for _, record := range t.prelude {
		t.outside.add(record)
	}
	t.prelude = nil
}

// finish moves the open encounter to FINISHED and decides its outcome. interruptBasis is empty for a normal end.
func (t *encounterTracker) finish(decidedAt time.Time, interruptBasis string) {
	record := t.current
	t.current = nil
	wasCandidate := record.state == EncounterEndingCandidate
	record.state = EncounterFinished
	record.endDecidedAt = decidedAt
	if record.endAt.After(t.boundaryFloor) {
		t.boundaryFloor, t.boundaryFloorSession = record.endAt, record.protocolSession
	}
	var outcome EncounterOutcome
	switch {
	case wasCandidate && record.startConfirmed:
		outcome = EncounterOutcomeConfirmed
	case wasCandidate:
		outcome = EncounterOutcomeInferred
	case interruptBasis == encounterEndCaptureEnd:
		outcome = EncounterOutcomeIncomplete
	case interruptBasis != "":
		outcome = EncounterOutcomeInterrupted
	default:
		outcome = EncounterOutcomeInferred
	}
	if !record.totals.confirmedActivity() {
		// Unattributed events seen during an empty battle interval stay counted, outside any encounter.
		t.outside.merge(record.totals)
		t.discardedEmpty++
		t.emit("discarded", record, decidedAt, record.endBasis, nil)
		return
	}
	snapshot := t.snapshotOf(record, outcome, time.Time{})
	if len(t.completed) >= maxCompletedEncounters {
		t.droppedTotals.merge(t.completed[0].Totals)
		t.completed = t.completed[1:]
		t.completedDropped++
	}
	t.completed = append(t.completed, snapshot)
	detail := snapshot
	t.emit("finished", record, decidedAt, record.endBasis, &detail)
}

// snapshotOf renders one encounter. For an open encounter, query is the capture time the live DPS is evaluated at.
func (t *encounterTracker) snapshotOf(record *encounterRecord, outcome EncounterOutcome, query time.Time) EncounterSnapshot {
	snapshot := EncounterSnapshot{
		EncounterID: t.encounterID(record.sequence), CaptureSessionID: t.captureSessionID, StatisticsIntervalID: t.intervalID,
		Sequence: record.sequence, ProtocolSession: record.protocolSession, State: record.state, Outcome: outcome,
		StartedAt: record.startAt, StartBasis: record.startBasis, StartConfirmed: record.startConfirmed,
		BattleEntered: record.battleEntered, EndedAt: record.endAt, EndBasis: record.endBasis, EndDecidedAt: record.endDecidedAt,
		FirstActivityAt: record.firstActivity, LastActivityAt: record.lastActivity, Reentries: record.reentries, Totals: record.totals,
	}
	switch record.state {
	case EncounterActive:
		// A query older than the newest observed capture time cannot shorten a live encounter: the damage already
		// counted was observed by then, so the end is never placed before it.
		if query.Before(t.highWater) {
			query = t.highWater
		}
		snapshot.EncounterDPS = encounterDPS(record, query, "live")
	case EncounterEndingCandidate:
		snapshot.EncounterDPS = encounterDPS(record, record.endAt, "ending_candidate")
	default:
		snapshot.EncounterDPS = encounterDPS(record, record.endAt, outcomeConfidence(outcome))
	}
	return snapshot
}

func outcomeConfidence(outcome EncounterOutcome) string {
	switch outcome {
	case EncounterOutcomeConfirmed:
		return "confirmed"
	case EncounterOutcomeInferred:
		return "inferred"
	case EncounterOutcomeInterrupted:
		return "interrupted"
	case EncounterOutcomeIncomplete:
		return "incomplete"
	}
	return "unknown"
}

// encounterDPS divides confirmed local damage dealt by the real elapsed time of the encounter. Animations, role
// switches, official time stops, and quiet periods inside the encounter stay in the denominator by definition.
func encounterDPS(record *encounterRecord, end time.Time, confidence string) DPSResult {
	result := DPSResult{
		Damage: record.totals.DamageDealt, StartObservedAt: record.startAt, EndObservedAt: end, Confidence: confidence,
	}
	if record.startAt.IsZero() || end.IsZero() {
		result.Status, result.Reason = DPSStatusUnavailable, "encounter_time_unavailable"
		return result
	}
	result.Duration = end.Sub(record.startAt)
	if result.Duration <= 0 {
		result.Status, result.Reason = DPSStatusInvalidDuration, "non_positive_encounter_duration"
		return result
	}
	value := float64(result.Damage) / result.Duration.Seconds()
	if math.IsNaN(value) || math.IsInf(value, 0) {
		result.Status, result.Reason = DPSStatusUnavailable, "non_finite_result"
		return result
	}
	result.Status, result.Value = DPSStatusAvailable, value
	return result
}

// snapshot renders the tracker. query is the capture time for live values; zero uses the newest observed time.
func (t *encounterTracker) snapshot(query time.Time) EncounterTrackerSnapshot {
	if query.IsZero() {
		query = t.highWater
	}
	snapshot := EncounterTrackerSnapshot{
		State: EncounterIdle, BattleState: "unknown",
		Completed:        append([]EncounterSnapshot(nil), t.completed...),
		CompletedDropped: t.completedDropped, CompletedDroppedTotals: t.droppedTotals, DiscardedEmpty: t.discardedEmpty,
		OutsideEncounters: t.outside, EventsWithoutTime: t.eventsWithoutTime, BattleStateSignals: t.battleStateSignals,
		UnanchoredBattleStateSignals: t.unanchoredBattleStateSignals,
	}
	if snapshot.Completed == nil {
		snapshot.Completed = []EncounterSnapshot{}
	}
	if t.signalKnown {
		snapshot.BattleState = "out_of_battle"
		if t.inBattle {
			snapshot.BattleState = "in_battle"
		}
	}
	for _, record := range t.prelude {
		snapshot.OutsideEncounters.add(record)
	}
	if t.current != nil {
		current := t.snapshotOf(t.current, EncounterOutcomeOpen, query)
		snapshot.Current, snapshot.State = &current, t.current.state
	}
	return snapshot
}

// headlineDPS is the Encounter DPS shown at the top level: the open encounter's live value, otherwise the most
// recent finished encounter's final value.
func (s EncounterTrackerSnapshot) headlineDPS() DPSResult {
	if s.Current != nil {
		return s.Current.EncounterDPS
	}
	if n := len(s.Completed); n > 0 {
		return s.Completed[n-1].EncounterDPS
	}
	return DPSResult{Status: DPSStatusUnavailable, Reason: "no_encounter_observed"}
}

// The DamageStatistics methods below drive the encounter tracker under the statistics lock, so encounter
// assignment and interval totals always describe the same set of events.

func (s *DamageStatistics) observeCaptureTime(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.observeTime(at)
}

func (s *DamageStatistics) observeBattleState(inBattle bool, at time.Time, protocolSession int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.observeBattleState(inBattle, at, protocolSession)
}

func (s *DamageStatistics) observeUnanchoredBattleState() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.unanchoredBattleStateSignals++
}

func (s *DamageStatistics) observeSceneTransition(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.observeSceneTransition(at)
}

func (s *DamageStatistics) observeSessionChange(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.observeSessionChange(at)
}

func (s *DamageStatistics) finishCapture() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.finishCapture()
}

// recordEncounterTransitions turns transition recording on for development diagnostics. It stays off in public
// builds, where nothing drains the list.
func (s *DamageStatistics) recordEncounterTransitions(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.recordTransitions = enabled
	if !enabled {
		s.encounters.transitions = nil
	}
}

func (s *DamageStatistics) takeEncounterTransitions() []EncounterTransition {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encounters.takeTransitions()
}
