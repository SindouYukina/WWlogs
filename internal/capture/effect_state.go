package capture

import (
	"math"
	"sort"
	"sync"
	"time"
)

// Bounds keep a long-running capture finite. Reaching maxEffectInstances is
// reported through EffectSnapshot.Status instead of silently dropping state.
const (
	maxEffectInstances     = 8192
	maxEffectEndedRecords  = 128
	maxEffectTombstones    = 512
	maxEffectExcludedKeys  = 4096
	maxEffectDurationValue = 1e7 // seconds; larger server values are treated as invalid
	// effectOverdueRetention is how long an instance may stay past its estimated expiry,
	// waiting for the server REMOVE, before it is reported as estimated_expired. Observed
	// REMOVE messages arrived from 0.4 s before to 66.8 s after the estimate, so this is a
	// memory-safety bound and not a game rule.
	effectOverdueRetention = 120 * time.Second
)

// EffectEntityRelation says what the capture can establish about the entity that
// holds or applied an effect. It reuses the player-relation evidence that damage
// attribution uses; UNKNOWN is never promoted to a player.
type EffectEntityRelation string

const (
	// EffectRelationLocalRole is a role entity of the local player's team.
	EffectRelationLocalRole EffectEntityRelation = "local_role"
	// EffectRelationLocalOwned is a non-role entity whose confirmed owner chain ends at the local team.
	EffectRelationLocalOwned EffectEntityRelation = "local_owned"
	// EffectRelationNonPlayer is a closed-chain runtime entity known as a monster or template.
	// Hostility is not separately verified.
	EffectRelationNonPlayer EffectEntityRelation = "non_player"
	EffectRelationUnknown   EffectEntityRelation = "unknown"
)

// EffectLifecycle separates what the server confirmed from what was estimated.
type EffectLifecycle string

const (
	EffectLifecycleActive EffectLifecycle = "active"
	// EffectLifecycleConfirmedRemoved means a server-side REMOVE for the same handle was observed.
	EffectLifecycleConfirmedRemoved EffectLifecycle = "confirmed_removed"
	// EffectLifecycleEstimatedExpired means the estimated expiry passed by more than
	// effectOverdueRetention without a REMOVE; the server never confirmed it.
	EffectLifecycleEstimatedExpired EffectLifecycle = "estimated_expired"
	// EffectLifecycleHolderRemoved means the holder entity was removed; the effect removal itself is inferred.
	EffectLifecycleHolderRemoved EffectLifecycle = "holder_removed"
	// EffectLifecycleReportedRemoved means the local client reported an early removal
	// (RemoveGameplayEffectPush with IsPrematureRemoval) for the same handle. The client
	// owns its team's effects, but the server does not acknowledge the report.
	EffectLifecycleReportedRemoved EffectLifecycle = "reported_removed"
	// EffectLifecycleReportedExpired means the local client reported a removal without
	// IsPrematureRemoval, which every observed natural expiry used.
	EffectLifecycleReportedExpired EffectLifecycle = "reported_expired"
)

// EffectEvidence names the message family that reported an effect instance. The
// server confirms its own notifies and snapshots; the local client reports the
// effects it applies to the entities it controls in outbound combat pushes, and
// the server does not echo those reports back.
type EffectEvidence string

const (
	EffectEvidenceServerNotify   EffectEvidence = "server_notify"
	EffectEvidenceServerSnapshot EffectEvidence = "server_snapshot"
	EffectEvidenceClientReport   EffectEvidence = "client_report"
)

// EffectRemainingStatus qualifies RemainingSeconds.
type EffectRemainingStatus string

const (
	// EffectRemainingEstimated is the last server-provided remaining time minus packet-observed elapsed time.
	EffectRemainingEstimated EffectRemainingStatus = "estimated"
	// EffectRemainingOverdue means the estimate reached zero and the REMOVE has not been observed.
	EffectRemainingOverdue EffectRemainingStatus = "overdue"
	// EffectRemainingUnknown means the server gave no usable finite duration; Reason says why.
	EffectRemainingUnknown EffectRemainingStatus = "unknown"
	// EffectRemainingUnavailable means this query cannot produce an estimate; Reason says why.
	EffectRemainingUnavailable EffectRemainingStatus = "unavailable"
)

// EffectOrigin records how the tracker first learned about an instance.
type EffectOrigin string

const (
	// EffectOriginApply is a server APPLY message, so the application time is known.
	EffectOriginApply EffectOrigin = "apply"
	// EffectOriginSnapshot is a FightBuffInformation row from an entity-active dump: the effect
	// existed at the dump time, but when it was applied is unknown.
	EffectOriginSnapshot EffectOrigin = "snapshot"
)

// EffectPolarity is the buff/debuff classification. No verified protocol field or
// Master relation gives it, so every instance stays unclassified.
type EffectPolarity string

const EffectPolarityUnclassified EffectPolarity = "unclassified"

// EffectSnapshotStatus tells a consumer whether the tracked set is complete.
type EffectSnapshotStatus string

const (
	EffectSnapshotComplete EffectSnapshotStatus = "complete"
	// EffectSnapshotDegraded means state was dropped (see Reason); absent effects may still be active.
	EffectSnapshotDegraded EffectSnapshotStatus = "degraded"
)

const (
	effectReasonNoServerDuration     = "no_server_duration"
	effectReasonDurationAbsent       = "duration_absent"
	effectReasonZeroDuration         = "zero_duration"
	effectReasonInvalidDuration      = "invalid_duration"
	effectReasonObservedTimeMissing  = "observed_time_missing"
	effectReasonQueryTimeRequired    = "query_time_required"
	effectReasonQueryTimeUnavailable = "query_time_unavailable"
	effectReasonSessionMismatch      = "capture_session_mismatch"
	effectReasonQueryBeforeUpdate    = "query_before_observation"
	effectReasonCapacityExceeded     = "capacity_exceeded"
)

// EffectStaticInfo is the static Master definition of an effect id. It describes the
// definition, not the live instance, and none of it is an official display name.
type EffectStaticInfo struct {
	DurationPolicy        *int64   `json:"durationPolicy,omitempty"`
	StaticDurationSeconds *float64 `json:"staticDurationSeconds,omitempty"`
	StackLimit            *uint64  `json:"stackLimit,omitempty"`
	// DeveloperTags are GameplayTag paths of the definition: simplified-Chinese developer
	// text, unofficial, and in part hashed segments that no table can read.
	DeveloperTags []string `json:"developerTags,omitempty"`
	// Term is the official glossary text of an element-ailment effect. Its link to the effect is
	// correlated, never verified, because it joins on the GameplayTag rather than on an engine key.
	Term *EffectTermInfo `json:"term,omitempty"`
}

// EffectTermInfo is the localized glossary term tied to an effect definition.
type EffectTermInfo struct {
	TermID      uint64 `json:"termId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
}

// EffectSemanticStatus reports how much of an effect's meaning is resolved. No state
// here means the official name is known: no table keys a name by effect id.
type EffectSemanticStatus string

const (
	// EffectSemanticDefinitionUnresolved means the effect id has no exact static row.
	EffectSemanticDefinitionUnresolved EffectSemanticStatus = "definition_unresolved"
	// EffectSemanticNameUnresolved means the static row is linked and its parameters are
	// available, but no official name or description is tied to the id.
	EffectSemanticNameUnresolved EffectSemanticStatus = "definition_linked_name_unresolved"
	// EffectSemanticNameCorrelated means a glossary term is tied to the definition by its
	// GameplayTag. The name is real game text, but the link is correlated, not verified.
	EffectSemanticNameCorrelated EffectSemanticStatus = "definition_linked_name_correlated"
)

// EffectApplyEvent is a gameplay-effect APPLY on one holder, or (with Origin
// snapshot) a snapshot row that reports an already existing instance.
type EffectApplyEvent struct {
	// Origin defaults to EffectOriginApply.
	Origin EffectOrigin
	// Evidence defaults to server_notify for an apply and server_snapshot for a snapshot.
	Evidence         EffectEvidence
	SessionNumber    int
	HolderEntityID   uint64
	HolderGeneration uint64
	HandleID         uint64
	EffectID         uint64
	HasEffectID      bool
	// DefinitionLinked reports an exact static buff row for EffectID.
	DefinitionLinked bool
	// Static carries the definition parameters of that row, when linked.
	Static *EffectStaticInfo
	// CombatClass and CombatBasis classify the definition; empty means unknown.
	CombatClass EffectCombatClass
	CombatBasis string
	// HolderMonster and SourceMonster report ends resolved as monsters, for the runtime combat rules.
	HolderMonster  bool
	SourceMonster  bool
	SourceEntityID uint64
	HasSource      bool
	HolderRelation EffectEntityRelation
	SourceRelation EffectEntityRelation
	StackCount     uint64
	HasStackCount  bool
	// DurationSeconds and RemainingSeconds are the raw server floats; nil means the field was absent.
	DurationSeconds  *float64
	RemainingSeconds *float64
	ObservedAt       time.Time
}

// EffectUpdateEvent is a duration or stack-count message addressed to an existing handle.
type EffectUpdateEvent struct {
	SessionNumber    int
	HolderEntityID   uint64
	HolderGeneration uint64
	HandleID         uint64
	StackCount       uint64
	HasStackCount    bool
	DurationSeconds  *float64
	RemainingSeconds *float64
	ObservedAt       time.Time
}

// EffectRemoveEvent is a gameplay-effect REMOVE for one handle.
type EffectRemoveEvent struct {
	SessionNumber    int
	HolderEntityID   uint64
	HolderGeneration uint64
	HandleID         uint64
	ObservedAt       time.Time
	// Evidence defaults to server_notify. A client_report removal ends the instance as
	// reported_removed when Premature is true and as reported_expired otherwise.
	Evidence  EffectEvidence
	Premature bool
}

// EffectCounters report how input was handled. A non-zero counter next to a
// small active set is the signal that a capture missed lifecycle messages.
type EffectCounters struct {
	Applies                uint64 `json:"applies"`
	DuplicateApplies       uint64 `json:"duplicateApplies"`
	SnapshotInstances      uint64 `json:"snapshotInstances"`
	DuplicateSnapshots     uint64 `json:"duplicateSnapshots"`
	SnapshotsSkipped       uint64 `json:"snapshotsSkipped"`
	ConflictingApplies     uint64 `json:"conflictingApplies"`
	StaleApplies           uint64 `json:"staleApplies"`
	InstantApplies         uint64 `json:"instantApplies"`
	Updates                uint64 `json:"updates"`
	UpdatesWithoutApply    uint64 `json:"updatesWithoutApply"`
	StaleUpdates           uint64 `json:"staleUpdates"`
	InvalidTimeValues      uint64 `json:"invalidTimeValues"`
	Removes                uint64 `json:"removes"`
	RemovesWithoutInstance uint64 `json:"removesWithoutInstance"`
	StaleRemoves           uint64 `json:"staleRemoves"`
	HolderRemovals         uint64 `json:"holderRemovals"`
	BoundaryDrops          uint64 `json:"boundaryDrops"`
	ExcludedUnrelated      uint64 `json:"excludedUnrelated"`
	// LateMessages counts update and remove messages that arrive for an instance already ended by holder removal.
	LateMessages uint64 `json:"lateMessages"`
	// ExcludedMessages counts update and remove messages addressed to an instance that was excluded as unrelated.
	ExcludedMessages          uint64 `json:"excludedMessages"`
	SkippedUncorrelatedHolder uint64 `json:"skippedUncorrelatedHolder"`
	StaleSessionEvents        uint64 `json:"staleSessionEvents"`
	CapacityRejected          uint64 `json:"capacityRejected"`
	PrunedExpired             uint64 `json:"prunedExpired"`
}

// EffectInstanceSnapshot is one tracked effect instance at the query time.
type EffectInstanceSnapshot struct {
	SessionNumber    int                  `json:"sessionNumber"`
	HolderEntityID   uint64               `json:"holderEntityId"`
	HolderGeneration uint64               `json:"holderGeneration"`
	HandleID         uint64               `json:"handleId"`
	HolderRelation   EffectEntityRelation `json:"holderRelation"`
	// EffectID is the Id of the apply message (or the BuffId of a snapshot row); it is the only stable identity and the display fallback.
	EffectID *uint64 `json:"effectId,omitempty"`
	// DefinitionStatus is correlated when the effect id has an exact row in the static buff table.
	// It says nothing about the effect's meaning, name or polarity.
	DefinitionStatus string `json:"definitionStatus"`
	// SemanticStatus is never complete: the official name is unresolved for every effect.
	SemanticStatus EffectSemanticStatus `json:"semanticStatus"`
	// Static holds the linked definition's parameters; it is a copy and may be nil.
	Static         *EffectStaticInfo    `json:"static,omitempty"`
	SourceEntityID *uint64              `json:"sourceEntityId,omitempty"`
	SourceRelation EffectEntityRelation `json:"sourceRelation"`
	Polarity       EffectPolarity       `json:"polarity"`
	StackCount     *uint64              `json:"stackCount,omitempty"`
	Lifecycle      EffectLifecycle      `json:"lifecycle"`
	Origin         EffectOrigin         `json:"origin"`
	// Evidence is the message family that created the instance; a server REMOVE or a client report may end it later.
	Evidence EffectEvidence `json:"evidence"`
	// CombatClass says whether the effect is evidently part of combat; CombatBasis names the evidence.
	// unknown means no evidence either way and is never a statement that the effect is harmless.
	CombatClass     EffectCombatClass `json:"combatClass"`
	CombatBasis     string            `json:"combatBasis"`
	FirstObservedAt time.Time         `json:"firstObservedAt"`
	// AppliedAt is set only for origin apply; a snapshot-origin instance has an unknown application time.
	AppliedAt     *time.Time `json:"appliedAt,omitempty"`
	LastUpdatedAt time.Time  `json:"lastUpdatedAt"`
	// ServerDurationSeconds is the most recent finite server duration.
	ServerDurationSeconds *float64              `json:"serverDurationSeconds,omitempty"`
	RemainingStatus       EffectRemainingStatus `json:"remainingStatus"`
	RemainingReason       string                `json:"remainingReason,omitempty"`
	RemainingSeconds      *float64              `json:"remainingSeconds,omitempty"`
	ExpectedExpiry        *time.Time            `json:"expectedExpiry,omitempty"`
}

// EffectEndedSnapshot records a bounded history entry for an instance that left the active set.
type EffectEndedSnapshot struct {
	EffectInstanceSnapshot
	EndedAt time.Time `json:"endedAt"`
	// ExpiryDeltaSeconds is EndedAt minus the estimated expiry, when a finite estimate existed.
	ExpiryDeltaSeconds *float64 `json:"expiryDeltaSeconds,omitempty"`
}

// EffectSnapshot is an independent copy of the tracker state.
type EffectSnapshot struct {
	CaptureSessionID     uint64               `json:"captureSessionId"`
	StatisticsIntervalID uint64               `json:"statisticsIntervalId"`
	QueryTime            *time.Time           `json:"queryTime,omitempty"`
	Status               EffectSnapshotStatus `json:"status"`
	Reason               string               `json:"reason,omitempty"`
	// Effects holds active and estimated_expired instances, ordered by session, holder, generation, handle.
	Effects     []EffectInstanceSnapshot `json:"effects"`
	RecentEnded []EffectEndedSnapshot    `json:"recentEnded"`
	Counters    EffectCounters           `json:"counters"`
}

type effectKey struct {
	session    int
	holder     uint64
	generation uint64
	handle     uint64
}

type effectInstance struct {
	key              effectKey
	effectID         uint64
	hasEffectID      bool
	definitionLinked bool
	static           *EffectStaticInfo
	source           uint64
	hasSource        bool
	holderRelation   EffectEntityRelation
	sourceRelation   EffectEntityRelation
	stack            uint64
	hasStack         bool
	origin           EffectOrigin
	evidence         EffectEvidence
	combatClass      EffectCombatClass
	combatBasis      string
	holderMonster    bool
	sourceMonster    bool
	firstObservedAt  time.Time
	lastUpdatedAt    time.Time
	hasDuration      bool
	duration         float64
	timeKnown        bool
	basisRemaining   float64
	basisAt          time.Time
	unknownReason    string
	insertionOrdinal uint64
}

// EffectTracker keeps the live set of server-confirmed gameplay-effect
// instances. Time is the packet-observed capture clock; queries pass an explicit
// timestamp from the same clock, so tests need no sleeping and a snapshot with
// no new packets still reports a countdown.
type EffectTracker struct {
	mu sync.RWMutex

	captureSessionID     uint64
	statisticsIntervalID uint64

	session          int
	hasSession       bool
	instances        map[effectKey]*effectInstance
	ended            []EffectEndedSnapshot
	tombstones       map[effectKey]time.Time
	tombstoneOrder   []effectKey
	excluded         map[effectKey]struct{}
	excludedOrder    []effectKey
	counters         EffectCounters
	highWater        time.Time
	capacityExceeded bool
	ordinal          uint64
}

// NewEffectTracker creates an empty tracker bound to a capture session ID.
func NewEffectTracker(captureSessionID uint64) *EffectTracker {
	return &EffectTracker{
		captureSessionID:     captureSessionID,
		statisticsIntervalID: 1,
		instances:            make(map[effectKey]*effectInstance),
		tombstones:           make(map[effectKey]time.Time),
	}
}

// validEffectHandle reports whether a handle can identify an instance. The server
// sends -2 (as uint64) with effects that have no duration lifecycle.
func validEffectHandle(handle uint64) bool {
	return int64(handle) >= 0
}

// timeBasis is the interpretation of one server duration/remaining pair.
type timeBasis struct {
	finite    bool
	remaining float64
	reason    string
	invalid   bool
}

func classifyEffectTime(duration, remaining *float64) (timeBasis, float64, bool) {
	var hasDuration bool
	var durationValue float64
	if duration != nil && !math.IsNaN(*duration) && !math.IsInf(*duration, 0) && *duration > 0 && *duration <= maxEffectDurationValue {
		hasDuration, durationValue = true, *duration
	}
	candidate := remaining
	if candidate == nil {
		candidate = duration
	}
	if candidate == nil {
		return timeBasis{reason: effectReasonDurationAbsent}, durationValue, hasDuration
	}
	value := *candidate
	switch {
	case math.IsNaN(value) || math.IsInf(value, 0) || value < -1 || value > maxEffectDurationValue:
		return timeBasis{invalid: true, reason: effectReasonInvalidDuration}, durationValue, hasDuration
	case value == -1:
		return timeBasis{reason: effectReasonNoServerDuration}, durationValue, hasDuration
	case value == 0:
		return timeBasis{reason: effectReasonZeroDuration}, durationValue, hasDuration
	default:
		return timeBasis{finite: true, remaining: value}, durationValue, hasDuration
	}
}

func (info *EffectStaticInfo) clone() *EffectStaticInfo {
	if info == nil {
		return nil
	}
	copied := &EffectStaticInfo{DeveloperTags: append([]string(nil), info.DeveloperTags...)}
	if info.Term != nil {
		term := *info.Term
		copied.Term = &term
	}
	if info.DurationPolicy != nil {
		value := *info.DurationPolicy
		copied.DurationPolicy = &value
	}
	if info.StaticDurationSeconds != nil {
		value := *info.StaticDurationSeconds
		copied.StaticDurationSeconds = &value
	}
	if info.StackLimit != nil {
		value := *info.StackLimit
		copied.StackLimit = &value
	}
	return copied
}

// ObserveApply records a server APPLY. A repeated APPLY for an active handle is
// treated as a refresh of that instance, never as a second instance.
func (t *EffectTracker) ObserveApply(event EffectApplyEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	origin := event.Origin
	if origin == "" {
		origin = EffectOriginApply
	}
	evidence := event.Evidence
	if evidence == "" {
		evidence = EffectEvidenceServerNotify
		if origin == EffectOriginSnapshot {
			evidence = EffectEvidenceServerSnapshot
		}
	}
	if !t.acceptSessionLocked(event.SessionNumber) {
		return
	}
	if !validEffectHandle(event.HandleID) {
		t.counters.InstantApplies++
		return
	}
	key := effectKey{event.SessionNumber, event.HolderEntityID, event.HolderGeneration, event.HandleID}
	if event.HolderRelation == EffectRelationNonPlayer && event.SourceRelation == EffectRelationNonPlayer {
		t.counters.ExcludedUnrelated++
		t.addExcludedLocked(key)
		return
	}
	delete(t.excluded, key)
	if removedAt, ok := t.tombstones[key]; ok && !event.ObservedAt.IsZero() && !event.ObservedAt.After(removedAt) {
		t.counters.StaleApplies++
		return
	}
	t.advanceHighWaterLocked(event.ObservedAt)
	basis, duration, hasDuration := classifyEffectTime(event.DurationSeconds, event.RemainingSeconds)
	if existing, active := t.instances[key]; active {
		if (existing.hasEffectID && event.HasEffectID && existing.effectID != event.EffectID) || (event.HasSource && existing.hasSource && existing.source != event.SourceEntityID) {
			// The handle now names a different effect or source: an earlier REMOVE was missed.
			t.counters.ConflictingApplies++
			delete(t.instances, key)
		} else {
			if origin == EffectOriginSnapshot {
				t.counters.DuplicateSnapshots++
			} else {
				t.counters.DuplicateApplies++
				existing.origin = EffectOriginApply
				existing.evidence = evidence
			}
			t.updateLocked(existing, event.ObservedAt, event.StackCount, event.HasStackCount, basis, duration, hasDuration, true)
			return
		}
	}
	if len(t.instances) >= maxEffectInstances {
		t.pruneExpiredLocked()
		if len(t.instances) >= maxEffectInstances {
			t.counters.CapacityRejected++
			t.capacityExceeded = true
			return
		}
	}
	t.ordinal++
	instance := &effectInstance{
		key: key, effectID: event.EffectID, hasEffectID: event.HasEffectID, definitionLinked: event.DefinitionLinked, static: event.Static,
		source: event.SourceEntityID, hasSource: event.HasSource,
		holderRelation: normalizeEffectRelation(event.HolderRelation), sourceRelation: normalizeEffectRelation(event.SourceRelation),
		stack: event.StackCount, hasStack: event.HasStackCount,
		origin: origin, evidence: evidence, combatClass: event.CombatClass, combatBasis: event.CombatBasis,
		holderMonster: event.HolderMonster, sourceMonster: event.SourceMonster,
		firstObservedAt: event.ObservedAt, lastUpdatedAt: event.ObservedAt,
		insertionOrdinal: t.ordinal,
	}
	t.setBasisLocked(instance, event.ObservedAt, basis, duration, hasDuration)
	t.instances[key] = instance
	delete(t.tombstones, key)
	if origin == EffectOriginSnapshot {
		t.counters.SnapshotInstances++
	} else {
		t.counters.Applies++
	}
}

// ObserveSnapshot records an effect instance reported by an entity-active
// snapshot. It uses the APPLY rules, with origin snapshot and its own counters.
func (t *EffectTracker) ObserveSnapshot(event EffectApplyEvent) {
	event.Origin = EffectOriginSnapshot
	t.ObserveApply(event)
}

// NoteSkippedSnapshot counts a snapshot row that cannot name its holder or handle.
func (t *EffectTracker) NoteSkippedSnapshot() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.counters.SnapshotsSkipped++
	t.mu.Unlock()
}

// ObserveUpdate applies a duration or stack-count message to an existing handle.
// The message cannot create an instance: it carries no effect Id or source.
func (t *EffectTracker) ObserveUpdate(event EffectUpdateEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.acceptSessionLocked(event.SessionNumber) {
		return
	}
	key := effectKey{event.SessionNumber, event.HolderEntityID, event.HolderGeneration, event.HandleID}
	instance, active := t.instances[key]
	if !active || !validEffectHandle(event.HandleID) {
		if _, excluded := t.excluded[key]; excluded {
			t.counters.ExcludedMessages++
		} else if t.endedByHolderRemovalLocked(key) >= 0 {
			t.counters.LateMessages++
		} else {
			t.counters.UpdatesWithoutApply++
		}
		return
	}
	t.advanceHighWaterLocked(event.ObservedAt)
	t.counters.Updates++
	basis, duration, hasDuration := classifyEffectTime(event.DurationSeconds, event.RemainingSeconds)
	// A message with no time fields keeps the earlier estimate.
	timeFields := event.DurationSeconds != nil || event.RemainingSeconds != nil
	t.updateLocked(instance, event.ObservedAt, event.StackCount, event.HasStackCount, basis, duration, hasDuration, timeFields)
}

// ObserveRemove records a server REMOVE. A REMOVE with no tracked instance is
// remembered briefly so that an APPLY that arrives later but is older is rejected.
func (t *EffectTracker) ObserveRemove(event EffectRemoveEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.acceptSessionLocked(event.SessionNumber) {
		return
	}
	key := effectKey{event.SessionNumber, event.HolderEntityID, event.HolderGeneration, event.HandleID}
	instance, active := t.instances[key]
	if !active {
		if _, excluded := t.excluded[key]; excluded {
			t.counters.ExcludedMessages++
			delete(t.excluded, key)
			return
		}
		if i := t.endedByHolderRemovalLocked(key); i >= 0 {
			// The REMOVE arrives after the server removed the holder entity.
			t.counters.LateMessages++
			t.ended[i].Lifecycle = removeLifecycle(event)
			t.ended[i].EndedAt = event.ObservedAt
			t.ended[i].ExpiryDeltaSeconds = nil
			return
		}
		t.counters.RemovesWithoutInstance++
		if validEffectHandle(event.HandleID) && !event.ObservedAt.IsZero() {
			t.addTombstoneLocked(key, event.ObservedAt)
		}
		return
	}
	if !event.ObservedAt.IsZero() && !instance.firstObservedAt.IsZero() && event.ObservedAt.Before(instance.firstObservedAt) {
		// The REMOVE belongs to an earlier instance of a reused handle.
		t.counters.StaleRemoves++
		return
	}
	t.advanceHighWaterLocked(event.ObservedAt)
	t.counters.Removes++
	t.endLocked(instance, removeLifecycle(event), event.ObservedAt)
	if !event.ObservedAt.IsZero() {
		t.addTombstoneLocked(key, event.ObservedAt)
	}
}

// removeLifecycle keeps a server-confirmed removal apart from a client report.
func removeLifecycle(event EffectRemoveEvent) EffectLifecycle {
	switch {
	case event.Evidence != EffectEvidenceClientReport:
		return EffectLifecycleConfirmedRemoved
	case event.Premature:
		return EffectLifecycleReportedRemoved
	default:
		return EffectLifecycleReportedExpired
	}
}

// ObserveHolderRemoved ends every instance held by an entity that the server removed.
func (t *EffectTracker) ObserveHolderRemoved(sessionNumber int, holderEntityID uint64, observedAt time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.acceptSessionLocked(sessionNumber) {
		return
	}
	t.advanceHighWaterLocked(observedAt)
	for _, instance := range t.sortedInstancesLocked() {
		if instance.key.holder != holderEntityID {
			continue
		}
		t.counters.HolderRemovals++
		t.endLocked(instance, EffectLifecycleHolderRemoved, observedAt)
	}
}

// RefreshRelations upgrades UNKNOWN holder and source relations of one holder's
// instances once the entity evidence improves. A known relation is never
// downgraded. resolve runs while the tracker lock is held and must not call the tracker.
func (t *EffectTracker) RefreshRelations(sessionNumber int, holderEntityID uint64, resolve func(entityID uint64) EffectEntityRelation) {
	if t == nil || resolve == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, instance := range t.instances {
		if instance.key.session != sessionNumber || instance.key.holder != holderEntityID {
			continue
		}
		if instance.holderRelation == EffectRelationUnknown {
			instance.holderRelation = normalizeEffectRelation(resolve(instance.key.holder))
		}
		if instance.hasSource && instance.sourceRelation == EffectRelationUnknown {
			instance.sourceRelation = normalizeEffectRelation(resolve(instance.source))
		}
	}
}

// RefreshUnknownRelations upgrades every UNKNOWN holder or source relation, resolving
// each distinct entity once. A known relation is never downgraded. resolve runs
// while the tracker lock is held and must not call the tracker.
func (t *EffectTracker) RefreshUnknownRelations(resolve func(entityID uint64) EffectEntityRelation) {
	if t == nil || resolve == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	resolved := make(map[uint64]EffectEntityRelation)
	lookup := func(entityID uint64) EffectEntityRelation {
		relation, done := resolved[entityID]
		if !done {
			relation = normalizeEffectRelation(resolve(entityID))
			resolved[entityID] = relation
		}
		return relation
	}
	for _, instance := range t.instances {
		if instance.holderRelation == EffectRelationUnknown {
			instance.holderRelation = lookup(instance.key.holder)
		}
		if instance.hasSource && instance.sourceRelation == EffectRelationUnknown {
			instance.sourceRelation = lookup(instance.source)
		}
	}
}

// NoteSkippedUncorrelatedHolder counts an APPLY whose holder could not be established.
func (t *EffectTracker) NoteSkippedUncorrelatedHolder() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.counters.SkippedUncorrelatedHolder++
	t.mu.Unlock()
}

// EndLiveState drops every live instance at a scene or session boundary, where
// runtime entity and handle IDs stop being comparable. Counters and the
// statistics interval are unchanged.
func (t *EffectTracker) EndLiveState() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counters.BoundaryDrops += uint64(len(t.instances))
	t.clearTrackedLocked()
}

// Snapshot returns the tracked set without remaining-time estimates.
func (t *EffectTracker) Snapshot() EffectSnapshot {
	if t == nil {
		return emptyEffectSnapshot()
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked(CaptureQueryTime{}, effectReasonQueryTimeRequired)
}

// SnapshotAt returns the tracked set with remaining-time estimates evaluated at
// query.Timestamp, which must use the capture clock of this tracker's session.
func (t *EffectTracker) SnapshotAt(query CaptureQueryTime) EffectSnapshot {
	if t == nil {
		return emptyEffectSnapshot()
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked(query, t.queryRejectionLocked(query))
}

// SnapshotAndResetAt snapshots and starts a new statistics interval atomically.
func (t *EffectTracker) SnapshotAndResetAt(query CaptureQueryTime) EffectSnapshot {
	if t == nil {
		return emptyEffectSnapshot()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snapshot := t.snapshotLocked(query, t.queryRejectionLocked(query))
	t.resetLocked()
	return snapshot
}

// Reset starts a new statistics interval and drops all state of the old one.
// Effects still running in the game are untracked until the server updates them.
func (t *EffectTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resetLocked()
}

func (t *EffectTracker) resetLocked() {
	t.statisticsIntervalID++
	t.clearTrackedLocked()
	t.ended = nil
	t.counters = EffectCounters{}
	t.highWater = time.Time{}
	t.ordinal = 0
}

func emptyEffectSnapshot() EffectSnapshot {
	return EffectSnapshot{Status: EffectSnapshotComplete, Effects: []EffectInstanceSnapshot{}, RecentEnded: []EffectEndedSnapshot{}}
}

func normalizeEffectRelation(relation EffectEntityRelation) EffectEntityRelation {
	switch relation {
	case EffectRelationLocalRole, EffectRelationLocalOwned, EffectRelationNonPlayer:
		return relation
	default:
		return EffectRelationUnknown
	}
}

// acceptSessionLocked keeps a newer session and rejects an older one: handle and
// entity IDs of different sessions are not comparable.
func (t *EffectTracker) acceptSessionLocked(session int) bool {
	if !t.hasSession {
		t.session, t.hasSession = session, true
		return true
	}
	switch {
	case session == t.session:
		return true
	case session > t.session:
		t.counters.BoundaryDrops += uint64(len(t.instances))
		t.clearTrackedLocked()
		t.highWater = time.Time{}
		t.session = session
		return true
	default:
		t.counters.StaleSessionEvents++
		return false
	}
}

func (t *EffectTracker) advanceHighWaterLocked(at time.Time) {
	if at.After(t.highWater) {
		t.highWater = at
	}
}

func (t *EffectTracker) setBasisLocked(instance *effectInstance, at time.Time, basis timeBasis, duration float64, hasDuration bool) {
	instance.hasDuration, instance.duration = hasDuration, duration
	switch {
	case basis.finite && at.IsZero():
		instance.timeKnown, instance.unknownReason = false, effectReasonObservedTimeMissing
	case basis.finite:
		instance.timeKnown, instance.basisRemaining, instance.basisAt, instance.unknownReason = true, basis.remaining, at, ""
	default:
		instance.timeKnown, instance.unknownReason = false, basis.reason
		if basis.invalid {
			t.counters.InvalidTimeValues++
		}
	}
}

// updateLocked merges one duration/stack/refresh observation into an instance.
// An observation older than the last accepted one changes nothing but a counter.
func (t *EffectTracker) updateLocked(instance *effectInstance, at time.Time, stack uint64, hasStack bool, basis timeBasis, duration float64, hasDuration, timeFields bool) {
	if !at.IsZero() && !instance.lastUpdatedAt.IsZero() && at.Before(instance.lastUpdatedAt) {
		t.counters.StaleUpdates++
		return
	}
	if hasStack {
		instance.stack, instance.hasStack = stack, true
	}
	if !at.IsZero() {
		instance.lastUpdatedAt = at
	}
	if !timeFields {
		return
	}
	if basis.invalid {
		// Keep the earlier estimate: an unreadable value is not a statement about the duration.
		t.counters.InvalidTimeValues++
		return
	}
	t.setBasisLocked(instance, at, basis, duration, hasDuration)
}

func (t *EffectTracker) addTombstoneLocked(key effectKey, at time.Time) {
	if _, exists := t.tombstones[key]; !exists {
		t.tombstoneOrder = append(t.tombstoneOrder, key)
	}
	t.tombstones[key] = at
	for len(t.tombstoneOrder) > maxEffectTombstones {
		delete(t.tombstones, t.tombstoneOrder[0])
		t.tombstoneOrder = t.tombstoneOrder[1:]
	}
}

// endedByHolderRemovalLocked returns the index of the newest holder_removed record for key, or -1.
func (t *EffectTracker) endedByHolderRemovalLocked(key effectKey) int {
	for i := len(t.ended) - 1; i >= 0; i-- {
		record := t.ended[i]
		if record.Lifecycle == EffectLifecycleHolderRemoved && record.SessionNumber == key.session &&
			record.HolderEntityID == key.holder && record.HolderGeneration == key.generation && record.HandleID == key.handle {
			return i
		}
	}
	return -1
}

func (t *EffectTracker) addExcludedLocked(key effectKey) {
	if t.excluded == nil {
		t.excluded = make(map[effectKey]struct{})
	}
	if _, exists := t.excluded[key]; !exists {
		t.excludedOrder = append(t.excludedOrder, key)
	}
	t.excluded[key] = struct{}{}
	for len(t.excludedOrder) > maxEffectExcludedKeys {
		delete(t.excluded, t.excludedOrder[0])
		t.excludedOrder = t.excludedOrder[1:]
	}
}

func (t *EffectTracker) clearTrackedLocked() {
	t.instances = make(map[effectKey]*effectInstance)
	t.tombstones = make(map[effectKey]time.Time)
	t.tombstoneOrder = nil
	t.excluded = nil
	t.excludedOrder = nil
	t.capacityExceeded = false
}

func (t *EffectTracker) endLocked(instance *effectInstance, lifecycle EffectLifecycle, at time.Time) {
	delete(t.instances, instance.key)
	record := EffectEndedSnapshot{EffectInstanceSnapshot: t.instanceSnapshotLocked(instance, CaptureQueryTime{}, effectReasonQueryTimeRequired), EndedAt: at}
	record.Lifecycle = lifecycle
	record.RemainingStatus, record.RemainingSeconds = EffectRemainingUnavailable, nil
	record.RemainingReason = "ended"
	if instance.timeKnown && !at.IsZero() {
		delta := at.Sub(instance.basisAt).Seconds() - instance.basisRemaining
		if !math.IsNaN(delta) && !math.IsInf(delta, 0) {
			record.ExpiryDeltaSeconds = &delta
		}
	}
	t.ended = append(t.ended, record)
	if len(t.ended) > maxEffectEndedRecords {
		t.ended = append([]EffectEndedSnapshot(nil), t.ended[len(t.ended)-maxEffectEndedRecords:]...)
	}
}

// pruneExpiredLocked frees instances whose estimated expiry is far behind the newest observed time.
func (t *EffectTracker) pruneExpiredLocked() {
	if t.highWater.IsZero() {
		return
	}
	for key, instance := range t.instances {
		if !instance.timeKnown {
			continue
		}
		expiry := instance.basisAt.Add(secondsToDuration(instance.basisRemaining))
		if t.highWater.Sub(expiry) > effectOverdueRetention {
			delete(t.instances, key)
			t.counters.PrunedExpired++
		}
	}
}

func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func (t *EffectTracker) queryRejectionLocked(query CaptureQueryTime) string {
	switch {
	case query.CaptureSessionID == 0 || query.CaptureSessionID != t.captureSessionID:
		return effectReasonSessionMismatch
	case query.Timestamp.IsZero():
		return effectReasonQueryTimeUnavailable
	default:
		return ""
	}
}

func (t *EffectTracker) sortedInstancesLocked() []*effectInstance {
	instances := make([]*effectInstance, 0, len(t.instances))
	for _, instance := range t.instances {
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool {
		a, b := instances[i].key, instances[j].key
		switch {
		case a.session != b.session:
			return a.session < b.session
		case a.holder != b.holder:
			return a.holder < b.holder
		case a.generation != b.generation:
			return a.generation < b.generation
		default:
			return a.handle < b.handle
		}
	})
	return instances
}

func (t *EffectTracker) snapshotLocked(query CaptureQueryTime, rejection string) EffectSnapshot {
	snapshot := EffectSnapshot{
		CaptureSessionID: t.captureSessionID, StatisticsIntervalID: t.statisticsIntervalID,
		Status: EffectSnapshotComplete, Counters: t.counters,
		Effects:     make([]EffectInstanceSnapshot, 0, len(t.instances)),
		RecentEnded: append([]EffectEndedSnapshot{}, t.ended...),
	}
	if rejection == "" {
		queryTime := query.Timestamp
		snapshot.QueryTime = &queryTime
	}
	if t.capacityExceeded {
		snapshot.Status, snapshot.Reason = EffectSnapshotDegraded, effectReasonCapacityExceeded
	}
	for _, instance := range t.sortedInstancesLocked() {
		snapshot.Effects = append(snapshot.Effects, t.instanceSnapshotLocked(instance, query, rejection))
	}
	return snapshot
}

func (t *EffectTracker) instanceSnapshotLocked(instance *effectInstance, query CaptureQueryTime, rejection string) EffectInstanceSnapshot {
	snapshot := EffectInstanceSnapshot{
		SessionNumber: instance.key.session, HolderEntityID: instance.key.holder,
		HolderGeneration: instance.key.generation, HandleID: instance.key.handle,
		HolderRelation: instance.holderRelation, SourceRelation: instance.sourceRelation,
		DefinitionStatus: string(resolutionUnknown), SemanticStatus: EffectSemanticDefinitionUnresolved,
		Polarity: EffectPolarityUnclassified, Lifecycle: EffectLifecycleActive,
		Origin: instance.origin, Evidence: instance.evidence, FirstObservedAt: instance.firstObservedAt, LastUpdatedAt: instance.lastUpdatedAt,
	}
	if instance.origin == EffectOriginApply {
		appliedAt := instance.firstObservedAt
		snapshot.AppliedAt = &appliedAt
	}
	if instance.hasEffectID {
		id := instance.effectID
		snapshot.EffectID = &id
		if instance.definitionLinked {
			snapshot.DefinitionStatus = string(resolutionCorrelated)
			snapshot.SemanticStatus = EffectSemanticNameUnresolved
			snapshot.Static = instance.static.clone()
			if snapshot.Static != nil && snapshot.Static.Term != nil {
				snapshot.SemanticStatus = EffectSemanticNameCorrelated
			}
		}
	}
	if instance.hasSource {
		id := instance.source
		snapshot.SourceEntityID = &id
	}
	class, basis := instance.combatClass, instance.combatBasis
	if class == "" {
		class, basis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
	}
	snapshot.CombatClass, snapshot.CombatBasis = classifyEffectInstance(class, basis, effectParties{
		holder: instance.holderRelation, source: instance.sourceRelation,
		holderMonster: instance.holderMonster, sourceMonster: instance.sourceMonster,
	})
	if instance.hasStack {
		count := instance.stack
		snapshot.StackCount = &count
	}
	if instance.hasDuration {
		duration := instance.duration
		snapshot.ServerDurationSeconds = &duration
	}
	switch {
	case !instance.timeKnown:
		snapshot.RemainingStatus, snapshot.RemainingReason = EffectRemainingUnknown, instance.unknownReason
		return snapshot
	case rejection != "":
		snapshot.RemainingStatus, snapshot.RemainingReason = EffectRemainingUnavailable, rejection
	case query.Timestamp.Before(instance.basisAt):
		snapshot.RemainingStatus, snapshot.RemainingReason = EffectRemainingUnavailable, effectReasonQueryBeforeUpdate
	default:
		remaining := instance.basisRemaining - query.Timestamp.Sub(instance.basisAt).Seconds()
		if math.IsNaN(remaining) || math.IsInf(remaining, 0) {
			snapshot.RemainingStatus, snapshot.RemainingReason = EffectRemainingUnavailable, effectReasonInvalidDuration
			break
		}
		if remaining > 0 {
			snapshot.RemainingStatus, snapshot.RemainingSeconds = EffectRemainingEstimated, &remaining
			break
		}
		zero := 0.0
		snapshot.RemainingStatus, snapshot.RemainingSeconds = EffectRemainingOverdue, &zero
		if -remaining > effectOverdueRetention.Seconds() {
			snapshot.Lifecycle = EffectLifecycleEstimatedExpired
		}
	}
	expiry := instance.basisAt.Add(secondsToDuration(instance.basisRemaining))
	snapshot.ExpectedExpiry = &expiry
	return snapshot
}
