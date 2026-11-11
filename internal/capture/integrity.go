package capture

import (
	"sort"
	"time"
)

// Data integrity of statistics. An encounter's lifecycle (State, Outcome) says how its boundaries were decided; its
// integrity says whether the data inside those boundaries is complete. A loss marks every encounter whose span it
// touches and is never cleared by a later recovery: the lost events stay lost and nothing estimates them.

// IntegrityStatus is the trust level of an encounter or a statistics interval.
type IntegrityStatus string

const (
	// IntegrityIntact: no transport loss or context downgrade touched the data.
	IntegrityIntact IntegrityStatus = "INTACT"
	// IntegrityDegraded: events may be missing, but the boundaries are intact; totals and DPS are lower bounds.
	IntegrityDegraded IntegrityStatus = "DEGRADED"
	// IntegrityUnreliable: a boundary may have been lost or the extent of the loss is unknown; DPS is not computed.
	IntegrityUnreliable IntegrityStatus = "UNRELIABLE"
)

// Integrity risks. Inbound data carries combat results (damage and healing), server effect notifies, and the
// battle-state signals that bound encounters; outbound data carries combat requests and client effect reports.
const (
	riskCombatRequestsLost      = "combat_requests_possibly_lost"
	riskClientEffectReportsLost = "client_effect_reports_possibly_lost"
	riskCombatResultsLost       = "combat_results_possibly_lost"
	riskServerEffectsLost       = "server_effects_possibly_lost"
	riskBoundarySignalsLost     = "boundary_signals_possibly_lost"
	riskTransportUnrecovered    = "transport_unrecovered"
	riskLossRecordEvicted       = "transport_loss_record_evicted"
	riskBattleStateUnverified   = "battle_state_unverified_at_start"
	riskAttributionSuspect      = "local_player_attribution_suspect"
	riskTransportGapOpen        = "transport_gap_open"
)

func riskMakesUnreliable(risk string) bool {
	switch risk {
	case riskBoundarySignalsLost, riskTransportUnrecovered, riskLossRecordEvicted, riskBattleStateUnverified:
		return true
	}
	return false
}

// DataIntegrity is the integrity of one encounter or of one statistics interval.
type DataIntegrity struct {
	Status                  IntegrityStatus `json:"status"`
	Risks                   []string        `json:"risks"`
	TransportLosses         int             `json:"transportLosses"`
	UnrecoveredLosses       int             `json:"unrecoveredLosses"`
	ContextDowngradedEvents uint64          `json:"contextDowngradedEvents"`
	FirstLossAt             time.Time       `json:"firstLossAt,omitempty"`
}

func intactIntegrity() DataIntegrity {
	return DataIntegrity{Status: IntegrityIntact, Risks: []string{}}
}

// statisticsLoss is one transport loss episode as statistics see it: data of direction after from was lost until to.
type statisticsLoss struct {
	id        uint64
	direction string
	from, to  time.Time
	ended     bool
	failed    bool
}

func (l *statisticsLoss) risks() []string {
	var risks []string
	if l.direction == "in" {
		risks = append(risks, riskCombatResultsLost, riskServerEffectsLost, riskBoundarySignalsLost)
	} else {
		risks = append(risks, riskCombatRequestsLost, riskClientEffectReportsLost)
	}
	if l.failed {
		risks = append(risks, riskTransportUnrecovered)
	}
	return risks
}

// overlaps reports whether the loss can have removed events observed between start and end (end zero: still open).
func (l *statisticsLoss) overlaps(start, end time.Time) bool {
	if !end.IsZero() && !l.from.Before(end) {
		return false
	}
	return !l.ended || start.IsZero() || l.to.After(start)
}

// encounterIntegrityState is what one encounter remembers about its integrity.
type encounterIntegrityState struct {
	lossIDs               []uint64
	lossRefsOverflow      bool
	battleStateUnverified bool
	contextDowngraded     uint64
}

const (
	maxEncounterLossRefs      = 32
	maxTrackedTransportLosses = 256
)

func (s *encounterIntegrityState) addLoss(id uint64) {
	for _, existing := range s.lossIDs {
		if existing == id {
			return
		}
	}
	if len(s.lossIDs) >= maxEncounterLossRefs {
		s.lossRefsOverflow = true
		return
	}
	s.lossIDs = append(s.lossIDs, id)
}

func (s *encounterIntegrityState) removeLoss(id uint64) {
	for i, existing := range s.lossIDs {
		if existing == id {
			s.lossIDs = append(s.lossIDs[:i], s.lossIDs[i+1:]...)
			return
		}
	}
}

// integrityFrom builds the reported integrity from the loss table and extra risks.
func (t *encounterTracker) integrityFrom(state encounterIntegrityState, extra ...string) DataIntegrity {
	integrity := intactIntegrity()
	risks := map[string]bool{}
	for _, id := range state.lossIDs {
		loss, ok := t.losses[id]
		if !ok {
			risks[riskLossRecordEvicted] = true
			continue
		}
		integrity.TransportLosses++
		if loss.failed {
			integrity.UnrecoveredLosses++
		}
		if integrity.FirstLossAt.IsZero() || loss.from.Before(integrity.FirstLossAt) {
			integrity.FirstLossAt = loss.from
		}
		for _, risk := range loss.risks() {
			risks[risk] = true
		}
	}
	if state.lossRefsOverflow {
		risks[riskLossRecordEvicted] = true
	}
	if state.battleStateUnverified {
		risks[riskBattleStateUnverified] = true
	}
	if state.contextDowngraded > 0 {
		risks[riskAttributionSuspect] = true
		integrity.ContextDowngradedEvents = state.contextDowngraded
	}
	for _, risk := range extra {
		risks[risk] = true
	}
	for risk := range risks {
		integrity.Risks = append(integrity.Risks, risk)
		if riskMakesUnreliable(risk) {
			integrity.Status = IntegrityUnreliable
		} else if integrity.Status == IntegrityIntact {
			integrity.Status = IntegrityDegraded
		}
	}
	sort.Strings(integrity.Risks)
	return integrity
}

// applyIntegrityToDPS keeps a computed DPS only as a lower bound when data may be missing, and withholds it when a
// boundary or the extent of the loss is unknown. Damage, duration, and times stay visible in both cases.
func applyIntegrityToDPS(result DPSResult, integrity DataIntegrity) DPSResult {
	if result.Status != DPSStatusAvailable {
		return result
	}
	switch integrity.Status {
	case IntegrityUnreliable:
		result.Status, result.Reason, result.Value = DPSStatusUnreliable, "data_integrity_unreliable", 0
	case IntegrityDegraded:
		result.Status, result.Reason = DPSStatusLowerBound, "data_integrity_degraded"
	}
	return result
}

// beginLoss records a loss and marks every encounter of this interval whose span it touches.
func (t *encounterTracker) beginLoss(id uint64, direction string, from time.Time) {
	if t.losses == nil {
		t.losses = make(map[uint64]*statisticsLoss)
	}
	if len(t.losses) >= maxTrackedTransportLosses {
		t.evictOldestEndedLoss()
	}
	t.losses[id] = &statisticsLoss{id: id, direction: direction, from: from}
	t.lossOrder = append(t.lossOrder, id)
	if t.current != nil && (t.current.state == EncounterActive || from.Before(t.current.endAt)) {
		t.current.integrity.addLoss(id)
	}
	for i := range t.completed {
		if from.Before(t.completed[i].EndedAt) {
			t.completedIntegrity[i].addLoss(id)
		}
	}
}

func (t *encounterTracker) evictOldestEndedLoss() {
	for i, id := range t.lossOrder {
		if loss, ok := t.losses[id]; !ok || loss.ended {
			t.lossesEvicted = true
			delete(t.losses, id)
			t.lossOrder = append(t.lossOrder[:i], t.lossOrder[i+1:]...)
			return
		}
	}
}

// endLoss records where the loss ended. An encounter that started at or after that point only received data from
// after the loss and is released from it.
func (t *encounterTracker) endLoss(id uint64, to time.Time) {
	loss, ok := t.losses[id]
	if !ok {
		return
	}
	loss.to, loss.ended = to, true
	if t.current != nil && !t.current.startAt.Before(to) {
		t.current.integrity.removeLoss(id)
	}
	for i := range t.completed {
		if !t.completed[i].StartedAt.Before(to) {
			t.completedIntegrity[i].removeLoss(id)
		}
	}
}

func (t *encounterTracker) failLoss(id uint64) {
	if loss, ok := t.losses[id]; ok {
		loss.failed = true
	}
}

// ongoingLossIDs lists losses that have not recovered; data is still being lost while they last.
func (t *encounterTracker) ongoingLossIDs() []uint64 {
	var ids []uint64
	for _, id := range t.lossOrder {
		if loss, ok := t.losses[id]; ok && !loss.ended {
			ids = append(ids, id)
		}
	}
	return ids
}

// gapOpenAt reports whether any stream's data at or after a point before at is still waiting behind a gap.
func (t *encounterTracker) gapOpenAt(at time.Time) bool {
	for _, since := range t.openGaps {
		if at.IsZero() || !since.After(at) {
			return true
		}
	}
	return false
}

// intervalIntegrity is the integrity of the interval totals. They are sums of observed events, so any loss or
// downgrade makes them lower bounds; an unrecovered loss leaves the extent unknown.
func (t *encounterTracker) intervalIntegrity() DataIntegrity {
	integrity := intactIntegrity()
	risks := map[string]bool{}
	for _, id := range t.lossOrder {
		loss, ok := t.losses[id]
		if !ok {
			continue
		}
		integrity.TransportLosses++
		if integrity.FirstLossAt.IsZero() || loss.from.Before(integrity.FirstLossAt) {
			integrity.FirstLossAt = loss.from
		}
		for _, risk := range loss.risks() {
			risks[risk] = true
		}
		if loss.failed {
			integrity.UnrecoveredLosses++
		}
	}
	if t.lossesEvicted {
		risks[riskLossRecordEvicted] = true
	}
	if t.contextDowngraded > 0 {
		risks[riskAttributionSuspect] = true
		integrity.ContextDowngradedEvents = t.contextDowngraded
	}
	for risk := range risks {
		integrity.Risks = append(integrity.Risks, risk)
		if risk == riskTransportUnrecovered || risk == riskLossRecordEvicted {
			integrity.Status = IntegrityUnreliable
		} else if integrity.Status == IntegrityIntact {
			integrity.Status = IntegrityDegraded
		}
	}
	sort.Strings(integrity.Risks)
	return integrity
}

// The DamageStatistics methods below feed transport and context events to the tracker under the statistics lock.

func (s *DamageStatistics) beginTransportLoss(id uint64, direction string, from time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.beginLoss(id, direction, from)
	s.history.add(HistoryEntry{Kind: HistoryLossBegan, Loss: &HistoryLoss{ID: id, Direction: direction, From: from}})
	s.encounters.journalCompletedIntegrity()
}

func (s *DamageStatistics) endTransportLoss(id uint64, to time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.endLoss(id, to)
	s.history.add(HistoryEntry{Kind: HistoryLossEnded, Loss: &HistoryLoss{ID: id, To: to}})
	s.encounters.journalCompletedIntegrity()
}

func (s *DamageStatistics) failTransportLoss(id uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.failLoss(id)
	s.history.add(HistoryEntry{Kind: HistoryLossFailed, Loss: &HistoryLoss{ID: id}})
	s.encounters.journalCompletedIntegrity()
}

// setTransportGap records that a stream's data after since is delayed behind a gap; a zero since clears it.
func (s *DamageStatistics) setTransportGap(streamKey string, since time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if since.IsZero() {
		delete(s.encounters.openGaps, streamKey)
		return
	}
	if s.encounters.openGaps == nil {
		s.encounters.openGaps = make(map[string]time.Time)
	}
	s.encounters.openGaps[streamKey] = since
}

// setBattleStateUnverified records whether the battle state may be stale after an inbound loss. An encounter that
// opens while it is set, without a battle-state signal, has an unverified start.
func (s *DamageStatistics) setBattleStateUnverified(unverified bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encounters.battleStateUnverified = unverified
}

// windowIntegrityLocked reports why a capture-time window (start, end] may be missing events, or "".
func (s *DamageStatistics) windowIntegrityLocked(start, end time.Time) string {
	t := s.encounters
	for _, id := range t.lossOrder {
		if loss, ok := t.losses[id]; ok && loss.overlaps(start, end) {
			return "transport_loss_in_window"
		}
	}
	if t.lossesEvicted {
		return "transport_loss_record_evicted"
	}
	if t.gapOpenAt(end) {
		return "transport_gap_open"
	}
	if !s.lastDowngradeAt.IsZero() && s.lastDowngradeAt.After(start) {
		return "attribution_context_suspect_in_window"
	}
	return ""
}
