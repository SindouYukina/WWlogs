package capture

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const maxCombatStatisticsEntities = 8192

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
	StatisticsIntervalID       uint64                       `json:"statisticsIntervalId"`
	DamageDealt                int64                        `json:"damageDealt"`
	DamageTaken                int64                        `json:"damageTaken"`
	OutgoingEvents             uint64                       `json:"outgoingEvents"`
	IncomingEvents             uint64                       `json:"incomingEvents"`
	UnattributedOutgoingDamage int64                        `json:"unattributedOutgoingDamage"`
	UnattributedIncomingDamage int64                        `json:"unattributedIncomingDamage"`
	UnattributedOutgoingEvents uint64                       `json:"unattributedOutgoingEvents"`
	UnattributedIncomingEvents uint64                       `json:"unattributedIncomingEvents"`
	UngroupedDamageDealt       int64                        `json:"ungroupedDamageDealt"`
	UngroupedDamageTaken       int64                        `json:"ungroupedDamageTaken"`
	UngroupedOutgoingEvents    uint64                       `json:"ungroupedOutgoingEvents"`
	UngroupedIncomingEvents    uint64                       `json:"ungroupedIncomingEvents"`
	ByPlayerEntity             []PlayerEntityDamageSnapshot `json:"byPlayerEntity"`
	EncounterDPS               DPSResult                    `json:"encounterDps"`
	GameTimeDPS                DPSResult                    `json:"gameTimeDps"`
	DamageSpanDPS              DPSResult                    `json:"damageSpanDps"`
	Rolling5sDPS               DPSResult                    `json:"rolling5sDps"`
	Rolling10sDPS              DPSResult                    `json:"rolling10sDps"`
}

type rollingDamageEvent struct {
	observedAt time.Time
	damage     int64
}

// DamageStatistics aggregates player-related damage confirmed by server responses.
type DamageStatistics struct {
	mu sync.RWMutex

	captureSessionID           uint64
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
	rollingLastQuery           time.Time
	rollingSessionNumber       int
	hasRollingSession          bool
	rollingUnavailableReason   string
}

// NewDamageStatistics creates an empty damage statistics accumulator.
func NewDamageStatistics() *DamageStatistics {
	return &DamageStatistics{
		captureSessionID:     captureSessionSequence.Add(1),
		statisticsIntervalID: 1,
		entities:             make(map[combatStatisticsKey]PlayerEntityDamageSnapshot),
	}
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

// SnapshotAt returns statistics and rolling DPS for an explicit timestamp from this capture session.
// The timestamp must use the same clock domain as the capture events; backwards queries are unavailable.
func (s *DamageStatistics) SnapshotAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if s == nil {
		return emptyDamageStatisticsSnapshot()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotAtLocked(query)
}

func (s *DamageStatistics) snapshotAtLocked(query CaptureQueryTime) DamageStatisticsSnapshot {
	snapshot := s.snapshotLocked()
	reason := s.validateRollingQueryLocked(query)
	if reason != "" {
		snapshot.Rolling5sDPS = unavailableRollingDPS(reason)
		snapshot.Rolling10sDPS = unavailableRollingDPS(reason)
		return snapshot
	}
	s.rollingLastQuery = query.Timestamp
	s.advanceRollingRetentionLocked(query.Timestamp)
	snapshot.Rolling5sDPS = s.rollingDPSLocked(query.Timestamp, rollingFiveSecondWindow)
	snapshot.Rolling10sDPS = s.rollingDPSLocked(query.Timestamp, rollingTenSecondWindow)
	return snapshot
}

func (s *DamageStatistics) validateRollingQueryLocked(query CaptureQueryTime) string {
	switch {
	case query.CaptureSessionID == 0 || query.CaptureSessionID != s.captureSessionID:
		return "capture_session_mismatch"
	case query.Timestamp.IsZero():
		return "query_time_unavailable"
	case s.rollingUnavailableReason != "":
		return s.rollingUnavailableReason
	case !s.rollingLastQuery.IsZero() && query.Timestamp.Before(s.rollingLastQuery):
		return "query_time_moved_backwards"
	case !s.rollingRetentionStart.IsZero() && query.Timestamp.Before(s.rollingRetentionStart):
		return "query_before_retained_window"
	default:
		return ""
	}
}

func (s *DamageStatistics) rollingDPSLocked(queryTime time.Time, window time.Duration) DPSResult {
	result := DPSResult{Duration: window, StartObservedAt: queryTime.Add(-window), EndObservedAt: queryTime}
	start := result.StartObservedAt
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
	return result
}

func unavailableRollingDPS(reason string) DPSResult {
	return DPSResult{Status: DPSStatusUnavailable, Reason: reason}
}

func (s *DamageStatistics) snapshotLocked() DamageStatisticsSnapshot {
	snapshot := DamageStatisticsSnapshot{
		CaptureSessionID: s.captureSessionID, StatisticsIntervalID: s.statisticsIntervalID,
		DamageDealt: s.damageDealt, DamageTaken: s.damageTaken,
		OutgoingEvents: s.outgoingEvents, IncomingEvents: s.incomingEvents,
		UnattributedOutgoingDamage: s.unattributedOutgoingDamage, UnattributedIncomingDamage: s.unattributedIncomingDamage,
		UnattributedOutgoingEvents: s.unattributedOutgoingEvents, UnattributedIncomingEvents: s.unattributedIncomingEvents,
		UngroupedDamageDealt: s.ungroupedDamageDealt, UngroupedDamageTaken: s.ungroupedDamageTaken,
		UngroupedOutgoingEvents: s.ungroupedOutgoingEvents, UngroupedIncomingEvents: s.ungroupedIncomingEvents,
		ByPlayerEntity: make([]PlayerEntityDamageSnapshot, 0, len(s.entities)),
	}
	for _, entity := range s.entities {
		snapshot.ByPlayerEntity = append(snapshot.ByPlayerEntity, entity)
	}
	snapshot.EncounterDPS = DPSResult{Status: DPSStatusUnavailable, Reason: "encounter_boundaries_unavailable"}
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
	s.rollingLastQuery = time.Time{}
	s.rollingSessionNumber = 0
	s.hasRollingSession = false
	s.rollingUnavailableReason = ""
}

func (p *processor) damageStatisticsSnapshot() DamageStatisticsSnapshot {
	if p == nil || p.damageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.damageStatistics.Snapshot()
}

func emptyDamageStatisticsSnapshot() DamageStatisticsSnapshot {
	return DamageStatisticsSnapshot{
		ByPlayerEntity: []PlayerEntityDamageSnapshot{},
		EncounterDPS:   DPSResult{Status: DPSStatusUnavailable, Reason: "encounter_boundaries_unavailable"},
		GameTimeDPS:    DPSResult{Status: DPSStatusUnavailable, Reason: "official_timer_duration_unavailable"},
		DamageSpanDPS:  DPSResult{Status: DPSStatusInsufficientData, Reason: "no_confirmed_outgoing_damage"},
		Rolling5sDPS:   unavailableRollingDPS("query_time_required"),
		Rolling10sDPS:  unavailableRollingDPS("query_time_required"),
	}
}

func (p *processor) damageStatisticsSnapshotAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if p == nil || p.damageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.damageStatistics.SnapshotAt(query)
}

func (p *processor) resetDamageStatistics() {
	if p != nil && p.damageStatistics != nil {
		p.damageStatistics.Reset()
	}
}

func (p *processor) snapshotAndResetDamageStatistics() DamageStatisticsSnapshot {
	if p == nil || p.damageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.damageStatistics.snapshotAndReset()
}

func (p *processor) snapshotAndResetDamageStatisticsAt(query CaptureQueryTime) DamageStatisticsSnapshot {
	if p == nil || p.damageStatistics == nil {
		return emptyDamageStatisticsSnapshot()
	}
	return p.damageStatistics.snapshotAndResetAt(query)
}

// addDamageEvent receives only events that passed response validation and RPC deduplication.
func (s *DamageStatistics) addDamageEvent(event combatEvent, sourceRelation, targetRelation playerRelation) {
	if s == nil || event.EventTypeCandidate != "DAMAGE_CANDIDATE" || event.Damage <= 0 ||
		!event.HasSourceEntity || event.SourceEntityID == 0 || !event.HasTargetEntity || event.TargetEntityID == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entities == nil {
		s.entities = make(map[combatStatisticsKey]PlayerEntityDamageSnapshot)
	}
	switch {
	case sourceRelation == playerRelationLocal && targetRelation == playerRelationUnrelated:
		s.damageDealt += event.Damage
		s.outgoingEvents++
		s.addOutgoingDamageTime(event.SessionNumber, event.Timestamp)
		s.addRollingDamageEventLocked(event.SessionNumber, event.Timestamp, event.Damage)
		s.addEntityDamage(event.SessionNumber, event.SourceEntityID, event.Source.Lifecycle.ReusedAfterRemove, event.Damage, true, event.Timestamp)
	case sourceRelation == playerRelationUnrelated && targetRelation == playerRelationLocal:
		s.damageTaken += event.Damage
		s.incomingEvents++
		s.addEntityDamage(event.SessionNumber, event.TargetEntityID, event.Target.Lifecycle.ReusedAfterRemove, event.Damage, false, event.Timestamp)
	case sourceRelation == playerRelationLocal && targetRelation == playerRelationUnknown:
		s.unattributedOutgoingDamage += event.Damage
		s.unattributedOutgoingEvents++
	case sourceRelation == playerRelationUnknown && targetRelation == playerRelationLocal:
		s.unattributedIncomingDamage += event.Damage
		s.unattributedIncomingEvents++
	case sourceRelation != playerRelationUnrelated && targetRelation == playerRelationUnrelated:
		s.unattributedOutgoingDamage += event.Damage
		s.unattributedOutgoingEvents++
	case sourceRelation == playerRelationUnrelated && targetRelation != playerRelationUnrelated:
		s.unattributedIncomingDamage += event.Damage
		s.unattributedIncomingEvents++
	}
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
	}
	return result
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
