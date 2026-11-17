package capture

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const maxCombatStatisticsEntities = 8192

// maxStatisticsAppliedKeys bounds the idempotency set; it equals maxConsumedRPCIDs.
const maxStatisticsAppliedKeys = maxConsumedRPCIDs

// maxRollingDamageEvents prevents unusually dense captures from consuming unbounded memory.
const maxRollingDamageEvents = 32768

const (
	rollingFiveSecondWindow = 5 * time.Second
	rollingTenSecondWindow  = 10 * time.Second
)

var captureSessionSequence atomic.Uint64

type combatStatisticsKey struct {
	sessionNumber    int
	entityID         uint64
	entityGeneration uint64
}

// PlayerEntityDamageSnapshot stores confirmed damage totals for one EntityId within a session.
type PlayerEntityDamageSnapshot struct {
	SessionNumber    int    `json:"sessionNumber"`
	EntityID         uint64 `json:"entityId"`
	EntityGeneration uint64 `json:"entityGeneration"`
	DamageDealt      int64  `json:"damageDealt"`
	DamageTaken      int64  `json:"damageTaken"`
	OutgoingEvents   uint64 `json:"outgoingEvents"`
	IncomingEvents   uint64 `json:"incomingEvents"`
	// These are capture-frame observation times, not authoritative in-game event times.
	FirstObserved time.Time `json:"firstObserved,omitempty"`
	LastObserved  time.Time `json:"lastObserved,omitempty"`
}

// DPSStatus identifies whether a DPS result is supported by its required data.
type DPSStatus string

const (
	DPSStatusAvailable        DPSStatus = "available"
	DPSStatusInsufficientData DPSStatus = "insufficient_data"
	DPSStatusUnavailable      DPSStatus = "unavailable"
	DPSStatusInvalidDuration  DPSStatus = "invalid_duration"
	// DPSStatusLowerBound: Value is computed, but events in its range may have been lost; the true value is not lower.
	DPSStatusLowerBound DPSStatus = "lower_bound"
	// DPSStatusUnreliable: a boundary or the extent of a loss is unknown, so no Value is reported.
	DPSStatusUnreliable DPSStatus = "unreliable"
)

// DPSResult reports a calculation and the evidence state that supports it.
type DPSResult struct {
	Status          DPSStatus     `json:"status"`
	Reason          string        `json:"reason,omitempty"`
	Damage          int64         `json:"damage"`
	Duration        time.Duration `json:"duration"`
	Value           float64       `json:"value"`
	StartObservedAt time.Time     `json:"startObservedAt,omitempty"`
	EndObservedAt   time.Time     `json:"endObservedAt,omitempty"`
	// Confidence is set on Encounter DPS only: live, ending_candidate, confirmed, inferred, interrupted, or
	// incomplete. Only confirmed and inferred describe a finished encounter with an observed end.
	Confidence string `json:"confidence,omitempty"`
}

// CaptureQueryTime binds an explicit timestamp from a capture clock domain to its capture session.
type CaptureQueryTime struct {
	CaptureSessionID uint64
	Timestamp        time.Time
}

// DamageStatisticsSnapshot separates confirmed totals from unattributed candidates.
type DamageStatisticsSnapshot struct {
	// CaptureSessionID is process-local and stays fixed for this statistics owner.
	CaptureSessionID uint64 `json:"captureSessionId"`
	// StatisticsIntervalID starts at 1 and advances whenever Reset begins a new interval.
	StatisticsIntervalID       uint64 `json:"statisticsIntervalId"`
	DamageDealt                int64  `json:"damageDealt"`
	DamageTaken                int64  `json:"damageTaken"`
	OutgoingEvents             uint64 `json:"outgoingEvents"`
	IncomingEvents             uint64 `json:"incomingEvents"`
	UnattributedOutgoingDamage int64  `json:"unattributedOutgoingDamage"`
	UnattributedIncomingDamage int64  `json:"unattributedIncomingDamage"`
	UnattributedOutgoingEvents uint64 `json:"unattributedOutgoingEvents"`
	UnattributedIncomingEvents uint64 `json:"unattributedIncomingEvents"`
	UngroupedDamageDealt       int64  `json:"ungroupedDamageDealt"`
	UngroupedDamageTaken       int64  `json:"ungroupedDamageTaken"`
	UngroupedOutgoingEvents    uint64 `json:"ungroupedOutgoingEvents"`
	UngroupedIncomingEvents    uint64 `json:"ungroupedIncomingEvents"`
	// DuplicateEvents counts records rejected in this interval because their response key was already applied.
	DuplicateEvents uint64                       `json:"duplicateEvents"`
	ByPlayerEntity  []PlayerEntityDamageSnapshot `json:"byPlayerEntity"`
	EncounterDPS    DPSResult                    `json:"encounterDps"`
	GameTimeDPS     DPSResult                    `json:"gameTimeDps"`
	DamageSpanDPS   DPSResult                    `json:"damageSpanDps"`
	Rolling5sDPS    DPSResult                    `json:"rolling5sDps"`
	Rolling10sDPS   DPSResult                    `json:"rolling10sDps"`
	// Encounters partitions this interval's events into encounters; EncounterDPS above is its headline value.
	Encounters EncounterTrackerSnapshot `json:"encounters"`
	// Integrity is the integrity of this interval's totals (the same value as Encounters.Integrity).
	Integrity DataIntegrity `json:"integrity"`
	// Roles and Vehicle split confirmed local damage by contributor; UnassignedRole retains unsupported sources.
	// Together they add up to DamageDealt and DamageTaken exactly (role_aggregation.go).
	Roles          []RoleDamageSnapshot   `json:"roles"`
	UnassignedRole UnassignedRoleTotals   `json:"unassignedRole"`
	Vehicle        *VehicleDamageSnapshot `json:"vehicle,omitempty"`
	// Healing is the local team's healing of this interval (healing.go). HealingNotLocalEvents counts healing results
	// that were not the local team's (another source or target, or held back by the attribution gate),
	// HealingDuplicateEvents repeated healing results, and HealingInconsistentEvents healing results that lowered HP.
	Healing                   HealingTotals `json:"healing"`
	HealingNotLocalEvents     uint64        `json:"healingNotLocalEvents"`
	HealingDuplicateEvents    uint64        `json:"healingDuplicateEvents"`
	HealingInconsistentEvents uint64        `json:"healingInconsistentEvents"`
}

type rollingDamageEvent struct {
	observedAt time.Time
	damage     int64
}

// DamageStatistics aggregates player-related damage confirmed by server responses.
type DamageStatistics struct {
	mu sync.RWMutex

	CaptureSessionID           uint64
	statisticsIntervalID       uint64
	entities                   map[combatStatisticsKey]PlayerEntityDamageSnapshot
	damageDealt                int64
	damageTaken                int64
	outgoingEvents             uint64
	incomingEvents             uint64
	unattributedOutgoingDamage int64
	unattributedIncomingDamage int64
	unattributedOutgoingEvents uint64
	unattributedIncomingEvents uint64
	ungroupedDamageDealt       int64
	ungroupedDamageTaken       int64
	ungroupedOutgoingEvents    uint64
	ungroupedIncomingEvents    uint64
	firstOutgoingObserved      time.Time
	lastOutgoingObserved       time.Time
	outgoingMissingTime        bool
	outgoingSessionNumber      int
	hasOutgoingSession         bool
	multipleOutgoingSessions   bool
	rollingEvents              []rollingDamageEvent
	rollingHighWater           time.Time
	rollingRetentionStart      time.Time
	rollingSessionNumber       int
	hasRollingSession          bool
	rollingUnavailableReason   string
	duplicateEvents            uint64
	// appliedKeys outlives Reset: a repeated response must not re-enter a later interval either.
	appliedKeys     map[CombatEventKey]struct{}
	appliedKeyOrder []CombatEventKey
	// Encounters is replaced, not cleared, on Reset so encounter IDs carry the new interval.
	Encounters *encounterTracker
	// lastDowngradeAt is the capture time of the newest event kept unattributed by the attribution context gate.
	lastDowngradeAt time.Time
	roles           roleAggregate
	// healing is the local team's healing of this interval; appliedHealingKeys is kept apart from appliedKeys, so
	// healing never changes which damage keys the bounded set remembers.
	healing                healingAccumulator
	healingNotLocal        uint64
	healingDuplicates      uint64
	appliedHealingKeys     map[CombatEventKey]struct{}
	appliedHealingKeyOrder []CombatEventKey
	// history is the Combat History journal (history_journal.go); it outlives Reset like appliedKeys, and the processor
	// shares it with its effect tracker.
	history *historyJournal
}

// NewDamageStatistics creates an empty damage statistics accumulator.
func NewDamageStatistics() *DamageStatistics {
	captureSessionID := captureSessionSequence.Add(1)
	statistics := &DamageStatistics{
		CaptureSessionID:     captureSessionID,
		statisticsIntervalID: 1,
		entities:             make(map[combatStatisticsKey]PlayerEntityDamageSnapshot),
		Encounters:           newEncounterTracker(captureSessionID, 1),
		history:              &historyJournal{},
	}
	statistics.Encounters.history = statistics.history
	return statistics
}

// Snapshot returns an independent copy of the current statistics.
func (s *DamageStatistics) Snapshot() DamageStatisticsSnapshot {
	if s == nil {
		return emptyDamageStatisticsSnapshot()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

// SnapshotAt returns statistics and rolling DPS for an explicit timestamp from this capture session. The timestamp
// must use the same clock domain as the capture events. A query reads only: any number of readers may query at any
// times, in any order, without affecting each other or later queries.
func (s *DamageStatistics) SnapshotAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if s == nil {
		return emptyDamageStatisticsSnapshot()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotAtLocked(query)
}

func (s *DamageStatistics) snapshotAtLocked(query CaptureQueryTime) DamageStatisticsSnapshot {
	snapshot := s.snapshotLocked()
	if query.CaptureSessionID == s.CaptureSessionID && !query.Timestamp.IsZero() {
		snapshot.Encounters = s.Encounters.snapshot(query.Timestamp)
		snapshot.EncounterDPS = snapshot.Encounters.headlineDPS()
		snapshot.Integrity = snapshot.Encounters.Integrity
	}
	reason := s.validateRollingQueryLocked(query)
	if reason != "" {
		snapshot.Rolling5sDPS = unavailableRollingDPS(reason)
		snapshot.Rolling10sDPS = unavailableRollingDPS(reason)
		return snapshot
	}
	snapshot.Rolling5sDPS = s.rollingDPSLocked(query.Timestamp, rollingFiveSecondWindow)
	snapshot.Rolling10sDPS = s.rollingDPSLocked(query.Timestamp, rollingTenSecondWindow)
	return snapshot
}

func (s *DamageStatistics) validateRollingQueryLocked(query CaptureQueryTime) string {
	switch {
	case query.CaptureSessionID == 0 || query.CaptureSessionID != s.CaptureSessionID:
		return "capture_session_mismatch"
	case query.Timestamp.IsZero():
		return "query_time_unavailable"
	case s.rollingUnavailableReason != "":
		return s.rollingUnavailableReason
	default:
		return ""
	}
}

func (s *DamageStatistics) rollingDPSLocked(queryTime time.Time, window time.Duration) DPSResult {
	result := DPSResult{Duration: window, StartObservedAt: queryTime.Add(-window), EndObservedAt: queryTime}
	start := result.StartObservedAt
	if !s.rollingRetentionStart.IsZero() && start.Before(s.rollingRetentionStart) {
		// Events before the retention start were discarded as newer ones arrived; this window can no longer be summed.
		return unavailableRollingDPS("query_before_retained_window")
	}
	var total int64
	for _, event := range s.rollingEvents {
		if !event.observedAt.After(start) || event.observedAt.After(queryTime) {
			continue
		}
		if event.damage > math.MaxInt64-total {
			return unavailableRollingDPS("damage_sum_overflow")
		}
		total += event.damage
	}
	result.Damage = total
	result.Value = float64(total) / window.Seconds()
	if math.IsNaN(result.Value) || math.IsInf(result.Value, 0) {
		return unavailableRollingDPS("non_finite_result")
	}
	result.Status = DPSStatusAvailable
	// The window has a fixed length, so missing events only lower the sum: the value is a lower bound.
	if reason := s.windowIntegrityLocked(start, queryTime); reason != "" {
		result.Status, result.Reason = DPSStatusLowerBound, reason
	}
	return result
}

func unavailableRollingDPS(reason string) DPSResult {
	return DPSResult{Status: DPSStatusUnavailable, Reason: reason}
}

func (s *DamageStatistics) snapshotLocked() DamageStatisticsSnapshot {
	snapshot := DamageStatisticsSnapshot{
		CaptureSessionID: s.CaptureSessionID, StatisticsIntervalID: s.statisticsIntervalID,
		DamageDealt: s.damageDealt, DamageTaken: s.damageTaken,
		OutgoingEvents: s.outgoingEvents, IncomingEvents: s.incomingEvents,
		UnattributedOutgoingDamage: s.unattributedOutgoingDamage, UnattributedIncomingDamage: s.unattributedIncomingDamage,
		UnattributedOutgoingEvents: s.unattributedOutgoingEvents, UnattributedIncomingEvents: s.unattributedIncomingEvents,
		UngroupedDamageDealt: s.ungroupedDamageDealt, UngroupedDamageTaken: s.ungroupedDamageTaken,
		UngroupedOutgoingEvents: s.ungroupedOutgoingEvents, UngroupedIncomingEvents: s.ungroupedIncomingEvents,
		DuplicateEvents: s.duplicateEvents,
		ByPlayerEntity:  make([]PlayerEntityDamageSnapshot, 0, len(s.entities)),
		Healing:         s.healing.result(), HealingNotLocalEvents: s.healingNotLocal, HealingDuplicateEvents: s.healingDuplicates,
		HealingInconsistentEvents: s.healing.inconsistent,
	}
	for _, entity := range s.entities {
		snapshot.ByPlayerEntity = append(snapshot.ByPlayerEntity, entity)
	}
	snapshot.Encounters = s.Encounters.snapshot(time.Time{})
	snapshot.EncounterDPS = snapshot.Encounters.headlineDPS()
	snapshot.Integrity = snapshot.Encounters.Integrity
	snapshot.Roles, snapshot.UnassignedRole = s.roles.snapshot()
	snapshot.Vehicle = s.roles.vehicleSnapshot()
	snapshot.GameTimeDPS = DPSResult{Status: DPSStatusUnavailable, Reason: "official_timer_duration_unavailable"}
	snapshot.DamageSpanDPS = s.damageSpanDPSLocked()
	snapshot.Rolling5sDPS = unavailableRollingDPS("query_time_required")
	snapshot.Rolling10sDPS = unavailableRollingDPS("query_time_required")
	sort.Slice(snapshot.ByPlayerEntity, func(i, j int) bool {
		if snapshot.ByPlayerEntity[i].SessionNumber != snapshot.ByPlayerEntity[j].SessionNumber {
			return snapshot.ByPlayerEntity[i].SessionNumber < snapshot.ByPlayerEntity[j].SessionNumber
		}
		if snapshot.ByPlayerEntity[i].EntityID != snapshot.ByPlayerEntity[j].EntityID {
			return snapshot.ByPlayerEntity[i].EntityID < snapshot.ByPlayerEntity[j].EntityID
		}
		return snapshot.ByPlayerEntity[i].EntityGeneration < snapshot.ByPlayerEntity[j].EntityGeneration
	})
	return snapshot
}

func (s *DamageStatistics) snapshotAndReset() DamageStatisticsSnapshot {
	if s == nil {
		return emptyDamageStatisticsSnapshot()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.snapshotLocked()
	s.resetLocked()
	return snapshot
}

func (s *DamageStatistics) snapshotAndResetAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if s == nil {
		return emptyDamageStatisticsSnapshot()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.snapshotAtLocked(query)
	s.resetLocked()
	return snapshot
}

// Reset clears all totals and per-entity state.
func (s *DamageStatistics) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

func (s *DamageStatistics) resetLocked() {
	s.entities = make(map[combatStatisticsKey]PlayerEntityDamageSnapshot)
	s.statisticsIntervalID++
	s.damageDealt = 0
	s.damageTaken = 0
	s.outgoingEvents = 0
	s.incomingEvents = 0
	s.unattributedOutgoingDamage = 0
	s.unattributedIncomingDamage = 0
	s.unattributedOutgoingEvents = 0
	s.unattributedIncomingEvents = 0
	s.ungroupedDamageDealt = 0
	s.ungroupedDamageTaken = 0
	s.ungroupedOutgoingEvents = 0
	s.ungroupedIncomingEvents = 0
	s.firstOutgoingObserved = time.Time{}
	s.lastOutgoingObserved = time.Time{}
	s.outgoingMissingTime = false
	s.outgoingSessionNumber = 0
	s.hasOutgoingSession = false
	s.multipleOutgoingSessions = false
	s.rollingEvents = nil
	s.rollingHighWater = time.Time{}
	s.rollingRetentionStart = time.Time{}
	s.rollingSessionNumber = 0
	s.hasRollingSession = false
	s.rollingUnavailableReason = ""
	s.duplicateEvents = 0
	s.lastDowngradeAt = time.Time{}
	s.roles = roleAggregate{}
	s.healing = healingAccumulator{}
	s.healingNotLocal = 0
	s.healingDuplicates = 0
	s.Encounters = s.Encounters.nextInterval(s.statisticsIntervalID)
}

func (p *Processor) DamageStatisticsSnapshot() DamageStatisticsSnapshot {
	if p == nil || p.DamageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.DamageStatistics.Snapshot()
}

func emptyDamageStatisticsSnapshot() DamageStatisticsSnapshot {
	return DamageStatisticsSnapshot{
		ByPlayerEntity: []PlayerEntityDamageSnapshot{},
		EncounterDPS:   DPSResult{Status: DPSStatusUnavailable, Reason: "no_encounter_observed"},
		Encounters:     EncounterTrackerSnapshot{State: EncounterIdle, BattleState: "unknown", Completed: []EncounterSnapshot{}, Integrity: intactIntegrity()},
		Integrity:      intactIntegrity(),
		Roles:          []RoleDamageSnapshot{},
		GameTimeDPS:    DPSResult{Status: DPSStatusUnavailable, Reason: "official_timer_duration_unavailable"},
		DamageSpanDPS:  DPSResult{Status: DPSStatusInsufficientData, Reason: "no_confirmed_outgoing_damage"},
		Rolling5sDPS:   unavailableRollingDPS("query_time_required"),
		Rolling10sDPS:  unavailableRollingDPS("query_time_required"),
	}
}

func (p *Processor) DamageStatisticsSnapshotAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if p == nil || p.DamageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.DamageStatistics.SnapshotAt(query)
}

func (p *Processor) resetDamageStatistics() {
	if p != nil && p.DamageStatistics != nil {
		p.DamageStatistics.Reset()
	}
}

func (p *Processor) SnapshotAndResetDamageStatistics() DamageStatisticsSnapshot {
	if p == nil || p.DamageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.DamageStatistics.snapshotAndReset()
}

func (p *Processor) SnapshotAndResetDamageStatisticsAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if p == nil || p.DamageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.DamageStatistics.snapshotAndResetAt(query)
}

// addDamageEvent receives only events that passed response validation and RPC deduplication.
func (s *DamageStatistics) addDamageEvent(event CombatEvent, sourceRelation, targetRelation PlayerRelation) {
	record, ok := normalizeCombatEvent(event, sourceRelation, targetRelation)
	if !ok {
		return
	}
	s.addCombatEvent(record)
}

// Statistics-level assignments for records that never reach the encounter tracker.
const (
	StatisticsAssignmentDuplicate  = "duplicate"
	StatisticsAssignmentNotCounted = "not_counted"
)

// addCombatEvent applies one normalized record and returns its encounter assignment. Damage enters the damage totals
// and the encounters; the local team's healing enters only the healing totals (addHealingLocked). A record whose key
// was already applied is counted as a duplicate and ignored.
func (s *DamageStatistics) addCombatEvent(record CombatEventRecord) string {
	if s == nil || record.Amount <= 0 || (record.Kind != CombatEventDamage && record.Kind != CombatEventHealing) {
		return StatisticsAssignmentNotCounted
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.Kind == CombatEventHealing {
		return s.addHealingLocked(record)
	}
	if s.entities == nil {
		s.entities = make(map[combatStatisticsKey]PlayerEntityDamageSnapshot)
	}
	if record.Key.hasIdentity() {
		if _, seen := s.appliedKeys[record.Key]; seen {
			s.duplicateEvents++
			return StatisticsAssignmentDuplicate
		}
		s.rememberAppliedKeyLocked(record.Key)
	}
	s.history.accept(&record)
	if record.ContextDowngrade != "" {
		s.Encounters.contextDowngraded++
		if record.ObservedAt.After(s.lastDowngradeAt) {
			s.lastDowngradeAt = record.ObservedAt
		}
	}
	s.roles.add(record)
	session := record.Key.ProtocolSession
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		s.damageDealt += record.Amount
		s.outgoingEvents++
		s.addOutgoingDamageTime(session, record.ObservedAt)
		s.addRollingDamageEventLocked(session, record.ObservedAt, record.Amount)
		s.addEntityDamage(session, record.Source.EntityID, record.Source.EntityGeneration, record.Amount, true, record.ObservedAt)
	case CombatFlowLocalIncoming:
		s.damageTaken += record.Amount
		s.incomingEvents++
		s.addEntityDamage(session, record.Target.EntityID, record.Target.EntityGeneration, record.Amount, false, record.ObservedAt)
	case CombatFlowUnattributedOutgoing:
		s.unattributedOutgoingDamage += record.Amount
		s.unattributedOutgoingEvents++
	case CombatFlowUnattributedIncoming:
		s.unattributedIncomingDamage += record.Amount
		s.unattributedIncomingEvents++
	}
	return s.Encounters.observeEvent(record)
}

// rememberAppliedKeyLocked keeps a bounded FIFO of applied keys. The bound matches the RPC consumed set, so an
// evicted key could also be re-admitted there; both layers then agree on what counts as a repeat.
func (s *DamageStatistics) rememberAppliedKeyLocked(key CombatEventKey) {
	rememberBoundedKey(&s.appliedKeys, &s.appliedKeyOrder, key)
}

func rememberBoundedKey(keys *map[CombatEventKey]struct{}, order *[]CombatEventKey, key CombatEventKey) {
	if *keys == nil {
		*keys = make(map[CombatEventKey]struct{})
	}
	(*keys)[key] = struct{}{}
	*order = append(*order, key)
	for len(*keys) > maxStatisticsAppliedKeys && len(*order) > 0 {
		delete(*keys, (*order)[0])
		*order = (*order)[1:]
	}
}

// Statistics-level assignment for a healing record counted as the local team's healing.
const statisticsAssignmentHealing = "healing"

// addHealingLocked applies one healing record. Only the local team's healing is counted; it is kept for encounter
// association without touching encounter boundaries, the clock, or any damage total.
func (s *DamageStatistics) addHealingLocked(record CombatEventRecord) string {
	if record.Key.hasIdentity() {
		if _, seen := s.appliedHealingKeys[record.Key]; seen {
			s.healingDuplicates++
			return StatisticsAssignmentDuplicate
		}
		rememberBoundedKey(&s.appliedHealingKeys, &s.appliedHealingKeyOrder, record.Key)
	}
	s.history.accept(&record)
	if !isLocalHealing(record) {
		s.healingNotLocal++
		return StatisticsAssignmentNotCounted
	}
	s.healing.add(healingEventOf(record))
	s.Encounters.observeHealing(record)
	return statisticsAssignmentHealing
}

func (s *DamageStatistics) addRollingDamageEventLocked(sessionNumber int, observedAt time.Time, damage int64) {
	if observedAt.IsZero() {
		s.rollingUnavailableReason = "outgoing_event_missing_observed_time"
		s.rollingEvents = nil
		return
	}
	if !s.hasRollingSession {
		s.rollingSessionNumber = sessionNumber
		s.hasRollingSession = true
	} else if s.rollingSessionNumber != sessionNumber {
		s.rollingUnavailableReason = "multiple_sessions_require_separate_statistics"
		s.rollingEvents = nil
		return
	}
	if s.rollingUnavailableReason != "" {
		return
	}
	if !s.rollingRetentionStart.IsZero() && observedAt.Before(s.rollingRetentionStart) {
		return
	}
	if len(s.rollingEvents) >= maxRollingDamageEvents {
		s.rollingUnavailableReason = "event_capacity_exceeded"
		s.rollingEvents = nil
		return
	}
	i := sort.Search(len(s.rollingEvents), func(i int) bool {
		return s.rollingEvents[i].observedAt.After(observedAt)
	})
	s.rollingEvents = append(s.rollingEvents, rollingDamageEvent{})
	copy(s.rollingEvents[i+1:], s.rollingEvents[i:])
	s.rollingEvents[i] = rollingDamageEvent{observedAt: observedAt, damage: damage}
	if observedAt.After(s.rollingHighWater) {
		s.rollingHighWater = observedAt
	}
	s.advanceRollingRetentionLocked(s.rollingHighWater)
}

func (s *DamageStatistics) advanceRollingRetentionLocked(reference time.Time) {
	if reference.After(s.rollingHighWater) {
		s.rollingHighWater = reference
	}
	if reference.After(s.rollingRetentionStart.Add(rollingTenSecondWindow)) || s.rollingRetentionStart.IsZero() {
		s.rollingRetentionStart = reference.Add(-rollingTenSecondWindow)
	}
	cutoff := s.rollingRetentionStart
	firstRetained := sort.Search(len(s.rollingEvents), func(i int) bool {
		return !s.rollingEvents[i].observedAt.Before(cutoff)
	})
	if firstRetained > 0 {
		copy(s.rollingEvents, s.rollingEvents[firstRetained:])
		s.rollingEvents = s.rollingEvents[:len(s.rollingEvents)-firstRetained]
	}
}

func (s *DamageStatistics) addOutgoingDamageTime(sessionNumber int, observedAt time.Time) {
	if !s.hasOutgoingSession {
		s.outgoingSessionNumber = sessionNumber
		s.hasOutgoingSession = true
	} else if s.outgoingSessionNumber != sessionNumber {
		s.multipleOutgoingSessions = true
	}
	if observedAt.IsZero() {
		s.outgoingMissingTime = true
		return
	}
	if s.firstOutgoingObserved.IsZero() || observedAt.Before(s.firstOutgoingObserved) {
		s.firstOutgoingObserved = observedAt
	}
	if s.lastOutgoingObserved.IsZero() || observedAt.After(s.lastOutgoingObserved) {
		s.lastOutgoingObserved = observedAt
	}
}

func (s *DamageStatistics) damageSpanDPSLocked() DPSResult {
	result := DPSResult{Damage: s.damageDealt}
	switch {
	case s.outgoingEvents == 0:
		result.Status, result.Reason = DPSStatusInsufficientData, "no_confirmed_outgoing_damage"
	case s.multipleOutgoingSessions:
		result.Status, result.Reason = DPSStatusUnavailable, "multiple_sessions_require_separate_statistics"
	case s.outgoingMissingTime:
		result.Status, result.Reason = DPSStatusUnavailable, "outgoing_event_missing_observed_time"
	case s.outgoingEvents < 2:
		result.Status, result.Reason = DPSStatusInsufficientData, "requires_multiple_damage_events"
	default:
		result.StartObservedAt = s.firstOutgoingObserved
		result.EndObservedAt = s.lastOutgoingObserved
		if result.StartObservedAt.IsZero() || result.EndObservedAt.IsZero() {
			result.Status, result.Reason = DPSStatusUnavailable, "outgoing_event_missing_observed_time"
			return result
		}
		result.Duration = result.EndObservedAt.Sub(result.StartObservedAt)
		if result.Duration <= 0 {
			result.Status, result.Reason = DPSStatusInvalidDuration, "non_positive_damage_span"
			return result
		}
		value := float64(result.Damage) / result.Duration.Seconds()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			result.Status, result.Reason = DPSStatusUnavailable, "non_finite_result"
			return result
		}
		result.Status, result.Value = DPSStatusAvailable, value
		s.applySpanIntegrityLocked(&result)
	}
	return result
}

// applySpanIntegrityLocked qualifies Damage-span DPS. A loss strictly inside the observed span only removes events, so
// the value is a lower bound. A loss that reaches either end may have removed the true first or last event, which
// would change the span itself, so the value is withheld.
func (s *DamageStatistics) applySpanIntegrityLocked(result *DPSResult) {
	t := s.Encounters
	inside, outside := false, false
	for _, id := range t.lossOrder {
		loss, ok := t.losses[id]
		if !ok {
			continue
		}
		if loss.ended && loss.from.After(result.StartObservedAt) && loss.to.Before(result.EndObservedAt) {
			inside = true
		} else {
			outside = true
		}
	}
	switch {
	case outside || t.lossesEvicted:
		result.Status, result.Reason, result.Value = DPSStatusUnreliable, "transport_loss_at_span_boundary", 0
	case inside:
		result.Status, result.Reason = DPSStatusLowerBound, "transport_loss_in_span"
	case !s.lastDowngradeAt.IsZero():
		result.Status, result.Reason = DPSStatusLowerBound, "attribution_context_suspect_in_span"
	}
}

func (s *DamageStatistics) addEntityDamage(sessionNumber int, entityID, entityGeneration uint64, amount int64, outgoing bool, observedAt time.Time) {
	key := combatStatisticsKey{sessionNumber: sessionNumber, entityID: entityID, entityGeneration: entityGeneration}
	entity, exists := s.entities[key]
	if !exists && len(s.entities) >= maxCombatStatisticsEntities {
		if outgoing {
			s.ungroupedDamageDealt += amount
			s.ungroupedOutgoingEvents++
		} else {
			s.ungroupedDamageTaken += amount
			s.ungroupedIncomingEvents++
		}
		return
	}
	if !exists {
		entity = PlayerEntityDamageSnapshot{SessionNumber: sessionNumber, EntityID: entityID, EntityGeneration: entityGeneration}
	}
	if outgoing {
		entity.DamageDealt += amount
		entity.OutgoingEvents++
	} else {
		entity.DamageTaken += amount
		entity.IncomingEvents++
	}
	if !observedAt.IsZero() {
		if entity.FirstObserved.IsZero() || observedAt.Before(entity.FirstObserved) {
			entity.FirstObserved = observedAt
		}
		if entity.LastObserved.IsZero() || observedAt.After(entity.LastObserved) {
			entity.LastObserved = observedAt
		}
	}
	s.entities[key] = entity
}
