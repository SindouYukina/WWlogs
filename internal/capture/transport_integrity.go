package capture

import (
	"sort"
	"time"
)

// Transport health, context validity, and loss bookkeeping. Three questions stay separate:
//
//   - Transport health: is data being delivered now, and has any been lost? (TransportHealthSnapshot)
//   - Context validity: after a loss, can the runtime state that attribution depends on still be trusted?
//     (ContextValiditySnapshot) Recovering frame decoding does not answer this.
//   - Data integrity: are the statistics of an encounter or interval complete? (DataIntegrity, in combat_statistics.go
//     and encounter.go) A recovered transport never restores the integrity of data already affected.
//
// Everything here is driven from the capture goroutine and is not safe for concurrent use.

// TransportState is the delivery state of one direction or of the whole capture.
type TransportState string

const (
	// TransportHealthy: no gap is open and no data has been lost in this capture.
	TransportHealthy TransportState = "HEALTHY"
	// TransportGapDetected: a sequence is missing and later data waits for a retransmission.
	TransportGapDetected TransportState = "GAP_DETECTED"
	// TransportRecovering: data was skipped or a frame failed, and the stream is searching for the next valid frame.
	TransportRecovering TransportState = "RECOVERING"
	// TransportRecovered: decoding continues after an earlier loss; the lost data stays lost.
	TransportRecovered TransportState = "RECOVERED"
	// TransportUnrecoverable: a recovery search exhausted its scan budget or the capture ended before a valid frame
	// was found. The search continues if data keeps arriving.
	TransportUnrecoverable TransportState = "UNRECOVERABLE"
)

func transportStateRank(state TransportState) int {
	switch state {
	case TransportUnrecoverable:
		return 4
	case TransportRecovering:
		return 3
	case TransportGapDetected:
		return 2
	case TransportRecovered:
		return 1
	default:
		return 0
	}
}

// TransportDirectionHealth reports one direction ("in" server to client, "out" client to server).
type TransportDirectionHealth struct {
	Direction       string         `json:"direction"`
	State           TransportState `json:"state"`
	Streams         int            `json:"streams"`
	OpenGaps        int            `json:"openGaps"`
	PendingSegments int            `json:"pendingSegments"`
	PendingBytes    int            `json:"pendingBytes"`
	OpenGapSince    time.Time      `json:"openGapSince,omitempty"`
	// Losses counts recovery episodes: one or more skipped gaps or framing failures before a valid frame was found.
	Losses          uint64    `json:"losses"`
	SkippedSegments uint64    `json:"skippedSegments"`
	DiscardedBytes  uint64    `json:"discardedBytes"`
	Recoveries      uint64    `json:"recoveries"`
	Unrecovered     uint64    `json:"unrecovered"`
	LastLossAt      time.Time `json:"lastLossAt,omitempty"`
	LastLossReason  string    `json:"lastLossReason,omitempty"`
	LastRecoveredAt time.Time `json:"lastRecoveredAt,omitempty"`
	// EstimatedLostFrames sums the frames lost across recoveries whose outbound sequence evidence was consistent;
	// recoveries without that evidence are counted in LostFramesUnknown.
	EstimatedLostFrames uint64 `json:"estimatedLostFrames"`
	LostFramesUnknown   uint64 `json:"lostFramesUnknown"`
}

// TransportHealthSnapshot is the transport view of the whole capture.
type TransportHealthSnapshot struct {
	State      TransportState             `json:"state"`
	Directions []TransportDirectionHealth `json:"directions"`
}

type transportLossDetails struct {
	DiscardedBytes      int
	SkippedSegments     int
	SequenceEvidence    string
	EstimatedLostFrames int
	Recovered           bool
	Unrecoverable       bool
}

type transportLossEpisode struct {
	id        uint64
	direction string
	from      time.Time
	reason    string
	ended     bool
	failed    bool
}

type transportDirectionCounters struct {
	losses, skippedSegments, discardedBytes, recoveries, unrecovered uint64
	estimatedLostFrames, lostFramesUnknown                           uint64
	lastLossAt, lastRecoveredAt                                      time.Time
	lastLossReason                                                   string
}

type transportMonitor struct {
	nextLossID uint64
	episodes   map[uint64]*transportLossEpisode
	directions map[string]*transportDirectionCounters
}

func (m *transportMonitor) counters(direction string) *transportDirectionCounters {
	if m.directions == nil {
		m.directions = make(map[string]*transportDirectionCounters)
	}
	counters, ok := m.directions[direction]
	if !ok {
		counters = &transportDirectionCounters{}
		m.directions[direction] = counters
	}
	return counters
}

// maxTransportLossEpisodes bounds the open-episode table; a stream holds at most one open episode, and streams are
// bounded by maxKCPStreams, so the table cannot exceed it in normal operation.
const maxTransportLossEpisodes = 4 * maxKCPStreams

// beginTransportLoss records that data of direction was lost after from and returns the episode ID. It marks the
// context that depends on that direction suspect and tells statistics and effect tracking.
func (p *processor) beginTransportLoss(direction string, from, detectedAt time.Time, reason string, skippedSegments int) uint64 {
	m := &p.transport
	m.nextLossID++
	id := m.nextLossID
	if m.episodes == nil {
		m.episodes = make(map[uint64]*transportLossEpisode)
	}
	if len(m.episodes) >= maxTransportLossEpisodes {
		for _, oldID := range sortedEpisodeIDs(m.episodes) {
			if m.episodes[oldID].ended || m.episodes[oldID].failed {
				delete(m.episodes, oldID)
				break
			}
		}
	}
	m.episodes[id] = &transportLossEpisode{id: id, direction: direction, from: from, reason: reason}
	counters := m.counters(direction)
	counters.losses++
	counters.skippedSegments += uint64(skippedSegments)
	counters.lastLossAt, counters.lastLossReason = detectedAt, reason
	p.context.invalidate(direction, from, reason)
	if direction == "in" && p.damageStatistics != nil {
		p.damageStatistics.setBattleStateUnverified(true)
	}
	if p.damageStatistics != nil {
		p.damageStatistics.beginTransportLoss(id, direction, from)
		p.flushEncounterTransitions()
	}
	if p.effects != nil {
		p.effects.MarkTransportLoss(from)
	}
	return id
}

func sortedEpisodeIDs(episodes map[uint64]*transportLossEpisode) []uint64 {
	ids := make([]uint64, 0, len(episodes))
	for id := range episodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// extendTransportLoss adds further skipped segments to an episode that has not recovered yet.
func (p *processor) extendTransportLoss(id uint64, skippedSegments int) {
	if episode, ok := p.transport.episodes[id]; ok {
		p.transport.counters(episode.direction).skippedSegments += uint64(skippedSegments)
	}
}

// endTransportLoss records that the stream found a valid frame at to. The data between the episode's start and to
// stays lost; only the transport state changes.
func (p *processor) endTransportLoss(id uint64, direction string, to time.Time, details transportLossDetails) {
	counters := p.transport.counters(direction)
	counters.recoveries++
	counters.discardedBytes += uint64(details.DiscardedBytes)
	counters.lastRecoveredAt = to
	if details.SequenceEvidence == sequenceEvidenceConsistent && details.EstimatedLostFrames >= 0 {
		counters.estimatedLostFrames += uint64(details.EstimatedLostFrames)
	} else {
		counters.lostFramesUnknown++
	}
	if episode, ok := p.transport.episodes[id]; ok {
		episode.ended = true
	}
	p.context.observeLossEnd(direction, to)
	if p.damageStatistics != nil {
		p.damageStatistics.endTransportLoss(id, to)
		p.flushEncounterTransitions()
	}
}

// failTransportLoss records that an episode could not find a frame within its budget or before the capture ended.
func (p *processor) failTransportLoss(id uint64, direction string) {
	episode, ok := p.transport.episodes[id]
	if ok && episode.failed {
		return
	}
	if ok {
		episode.failed = true
	}
	p.transport.counters(direction).unrecovered++
	if p.damageStatistics != nil {
		p.damageStatistics.failTransportLoss(id)
		p.flushEncounterTransitions()
	}
}

// observeTransportGap tells statistics that a stream's data after since is delayed (open) or no longer delayed.
func (p *processor) observeTransportGap(streamKey, direction string, since time.Time, open bool) {
	if p.damageStatistics == nil {
		return
	}
	if open {
		p.damageStatistics.setTransportGap(streamKey, since)
	} else {
		p.damageStatistics.setTransportGap(streamKey, time.Time{})
	}
}

// transportHealthSnapshot combines the live stream state with the episode counters.
func (p *processor) transportHealthSnapshot() TransportHealthSnapshot {
	byDirection := map[string]*TransportDirectionHealth{
		"in":  {Direction: "in", State: TransportHealthy},
		"out": {Direction: "out", State: TransportHealthy},
	}
	for _, state := range p.kcpStreams {
		health, ok := byDirection[state.direction]
		if !ok {
			continue
		}
		health.Streams++
		health.PendingSegments += len(state.pending)
		health.PendingBytes += state.pendingBytes
		current := TransportHealthy
		switch {
		case state.recovery.active && state.recovery.failed:
			current = TransportUnrecoverable
		case state.recovery.active:
			current = TransportRecovering
		case state.gapOpen:
			current = TransportGapDetected
		}
		if state.gapOpen {
			health.OpenGaps++
			if health.OpenGapSince.IsZero() || state.gapSince.Before(health.OpenGapSince) {
				health.OpenGapSince = state.gapSince
			}
		}
		if transportStateRank(current) > transportStateRank(health.State) {
			health.State = current
		}
	}
	snapshot := TransportHealthSnapshot{State: TransportHealthy}
	for _, direction := range []string{"in", "out"} {
		health := byDirection[direction]
		if counters, ok := p.transport.directions[direction]; ok {
			health.Losses, health.SkippedSegments, health.DiscardedBytes = counters.losses, counters.skippedSegments, counters.discardedBytes
			health.Recoveries, health.Unrecovered = counters.recoveries, counters.unrecovered
			health.LastLossAt, health.LastLossReason, health.LastRecoveredAt = counters.lastLossAt, counters.lastLossReason, counters.lastRecoveredAt
			health.EstimatedLostFrames, health.LostFramesUnknown = counters.estimatedLostFrames, counters.lostFramesUnknown
			if health.State == TransportHealthy && counters.losses > 0 {
				health.State = TransportRecovered
			}
		}
		if transportStateRank(health.State) > transportStateRank(snapshot.State) {
			snapshot.State = health.State
		}
		snapshot.Directions = append(snapshot.Directions, *health)
	}
	return snapshot
}

// ContextState is the validity of one piece of runtime context.
type ContextState string

const (
	ContextValid ContextState = "VALID"
	// ContextSuspect: data this context depends on was lost, and no evidence since then re-established it.
	ContextSuspect ContextState = "SUSPECT"
)

// Context components. Attribution gates statistics; the others are reported so consumers know what may be stale.
const (
	// contextAttribution: the scene and the local-player anchor (PlayerSceneGroup mapping and role entities) that
	// classify damage as local. Inbound loss can hide a scene change or a team change.
	contextAttribution = "local_player_attribution"
	// contextEntities: entity add/remove, ownership, and summoner chains, all from inbound notifies.
	contextEntities = "entity_registry"
	// contextBattleState: the in-battle state from inbound battle-state signals.
	contextBattleState = "battle_state"
	// contextEffects: gameplay-effect instances, from inbound notifies and outbound client reports.
	contextEffects = "effects"
)

// Revalidation bases.
const (
	contextBasisSceneJoin       = "scene_join"
	contextBasisSessionStart    = "protocol_session_start"
	contextBasisLocalRoleReport = "local_role_report_matches_scene_group"
	contextBasisBattleSignal    = "battle_state_signal"
)

// ContextComponentSnapshot is one context component.
type ContextComponentSnapshot struct {
	Component     string       `json:"component"`
	State         ContextState `json:"state"`
	Reason        string       `json:"reason,omitempty"`
	SuspectSince  time.Time    `json:"suspectSince,omitempty"`
	RevalidatedAt time.Time    `json:"revalidatedAt,omitempty"`
	Basis         string       `json:"basis,omitempty"`
	Invalidations uint64       `json:"invalidations"`
}

// ContextValiditySnapshot reports context validity. DowngradedEvents counts statistics events that would have been
// confirmed local but were kept unattributed because local-player attribution was suspect.
type ContextValiditySnapshot struct {
	State            ContextState               `json:"state"`
	Components       []ContextComponentSnapshot `json:"components"`
	DowngradedEvents uint64                     `json:"downgradedEvents"`
}

type contextComponent struct {
	suspect       bool
	since         time.Time
	reason        string
	revalidatedAt time.Time
	basis         string
	invalidations uint64
}

func (c *contextComponent) invalidate(at time.Time, reason string) {
	if !c.suspect {
		c.since = at
	}
	c.suspect, c.reason = true, reason
	c.invalidations++
}

func (c *contextComponent) revalidate(at time.Time, basis string) {
	if !c.suspect {
		return
	}
	c.suspect, c.revalidatedAt, c.basis = false, at, basis
}

// contextValidity tracks which runtime context a loss may have made stale. Revalidation of inbound-derived context
// needs evidence observed after the inbound loss ended, while no inbound loss is ongoing.
type contextValidity struct {
	attribution, entities, battleState, effects contextComponent
	ongoingInbound                              int
	lastInboundLossEnd                          time.Time
	downgradedEvents                            uint64
}

var contextComponentOrder = []string{contextAttribution, contextEntities, contextBattleState, contextEffects}

func (c *contextValidity) component(name string) *contextComponent {
	switch name {
	case contextAttribution:
		return &c.attribution
	case contextEntities:
		return &c.entities
	case contextBattleState:
		return &c.battleState
	default:
		return &c.effects
	}
}

// invalidate marks the context that depends on direction. Outbound loss leaves attribution intact: the client's role
// reports describe its current role, and a missed report does not change which entities are local.
func (c *contextValidity) invalidate(direction string, at time.Time, reason string) {
	if direction == "in" {
		c.ongoingInbound++
		c.attribution.invalidate(at, reason)
		c.entities.invalidate(at, reason)
		c.battleState.invalidate(at, reason)
	}
	c.effects.invalidate(at, reason)
}

func (c *contextValidity) observeLossEnd(direction string, to time.Time) {
	if direction != "in" {
		return
	}
	if c.ongoingInbound > 0 {
		c.ongoingInbound--
	}
	if to.After(c.lastInboundLossEnd) {
		c.lastInboundLossEnd = to
	}
}

// inboundEvidenceUsable reports whether evidence observed at at can re-establish inbound-derived context.
func (c *contextValidity) inboundEvidenceUsable(at time.Time) bool {
	return c.ongoingInbound == 0 && !at.IsZero() && at.After(c.lastInboundLossEnd)
}

// resetForScene revalidates everything at a scene join or protocol session start: both rebuild the runtime context.
func (c *contextValidity) resetForScene(at time.Time, basis string) {
	for _, name := range contextComponentOrder {
		component := c.component(name)
		if name == contextEffects || c.inboundEvidenceUsable(at) || basis == contextBasisSessionStart {
			component.revalidate(at, basis)
		}
	}
	if basis == contextBasisSessionStart {
		c.ongoingInbound = 0
	}
}

func (c *contextValidity) snapshot() ContextValiditySnapshot {
	snapshot := ContextValiditySnapshot{State: ContextValid, DowngradedEvents: c.downgradedEvents}
	for _, name := range contextComponentOrder {
		component := c.component(name)
		entry := ContextComponentSnapshot{
			Component: name, State: ContextValid, RevalidatedAt: component.revalidatedAt, Basis: component.basis,
			Invalidations: component.invalidations,
		}
		if component.suspect {
			entry.State, entry.Reason, entry.SuspectSince = ContextSuspect, component.reason, component.since
			snapshot.State = ContextSuspect
		}
		snapshot.Components = append(snapshot.Components, entry)
	}
	return snapshot
}

// observeContextSceneJoin and observeContextSessionStart rebuild context at scene and session boundaries.
func (p *processor) observeContextSceneJoin(at time.Time) {
	p.context.resetForScene(at, contextBasisSceneJoin)
	if p.damageStatistics != nil && !p.context.battleState.suspect {
		p.damageStatistics.setBattleStateUnverified(false)
	}
}

func (p *processor) observeContextSessionStart(at time.Time) {
	p.context.resetForScene(at, contextBasisSessionStart)
	if p.damageStatistics != nil {
		p.damageStatistics.setBattleStateUnverified(false)
	}
}

// observeContextBattleSignal re-establishes the battle state from a local battle-state signal after the loss.
func (p *processor) observeContextBattleSignal(at time.Time) {
	if !p.context.battleState.suspect || !p.context.inboundEvidenceUsable(at) {
		return
	}
	p.context.battleState.revalidate(at, contextBasisBattleSignal)
	if p.damageStatistics != nil {
		p.damageStatistics.setBattleStateUnverified(false)
	}
}

// observeContextLocalRole re-establishes local-player attribution when, after an inbound loss ended, the client
// reports a current role entity that the registry still maps through PlayerSceneGroup to the anchored local player.
// That shows the scene and the team mapping survived the loss.
func (p *processor) observeContextLocalRole(entityID uint64, at time.Time) {
	if !p.context.attribution.suspect || !p.context.inboundEvidenceUsable(at) || p.entities == nil || p.playerRelevance == nil {
		return
	}
	entity, status := p.entities.resolve(entityID)
	if status == resolutionUnknown || entity.entityID != entityID || !p.playerRelevance.isLocalPlayerRoleEntity(entity) {
		return
	}
	p.context.attribution.revalidate(at, contextBasisLocalRoleReport)
}

// Attribution downgrade reason carried on statistics records.
const contextDowngradeAttribution = "local_player_attribution_suspect"

// gateAttribution keeps an event unattributed while local-player attribution is suspect: a local relation built
// before the loss may no longer be true, and confirmed output must not include it.
func (p *processor) gateAttribution(record CombatEventRecord) CombatEventRecord {
	if !p.context.attribution.suspect {
		return record
	}
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		record.Flow = CombatFlowUnattributedOutgoing
	case CombatFlowLocalIncoming:
		record.Flow = CombatFlowUnattributedIncoming
	default:
		return record
	}
	record.Confidence = record.Flow.confidence()
	record.ContextDowngrade = contextDowngradeAttribution
	p.context.downgradedEvents++
	return record
}

// DataIntegritySnapshot is the read-only trust view for consumers such as the public console and a future UI.
type DataIntegritySnapshot struct {
	Capture    CaptureLayerHealth      `json:"capture"`
	Transport  TransportHealthSnapshot `json:"transport"`
	Context    ContextValiditySnapshot `json:"context"`
	Statistics DataIntegrity           `json:"statistics"`
}

func (p *processor) dataIntegritySnapshot(statistics DamageStatisticsSnapshot) DataIntegritySnapshot {
	return DataIntegritySnapshot{
		Capture: p.captureLayerHealth(), Transport: p.transportHealthSnapshot(), Context: p.context.snapshot(),
		Statistics: statistics.Integrity,
	}
}
