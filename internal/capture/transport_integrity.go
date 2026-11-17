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
const maxTransportLossEpisodes = 4 * MaxKCPStreams

// beginTransportLoss records that data of direction was lost after from and returns the episode ID. It marks the
// context that depends on that direction suspect and tells statistics and effect tracking.
func (p *Processor) beginTransportLoss(direction string, from, detectedAt time.Time, reason string, skippedSegments int) uint64 {
	id := p.openLossEpisode(direction, from, detectedAt, reason, skippedSegments)
	p.Context.invalidate(direction, from, reason)
	if direction == "in" && p.DamageStatistics != nil {
		p.DamageStatistics.setBattleStateUnverified(true)
	}
	if p.DamageStatistics != nil {
		p.DamageStatistics.beginTransportLoss(id, direction, from)
		if p.observer != nil {
			p.observer.FlushEncounterTransitions()
		}
	}
	if p.Effects != nil {
		p.Effects.MarkTransportLoss(from)
	}
	return id
}

// openLossEpisode records a new loss episode of direction and counts it.
func (p *Processor) openLossEpisode(direction string, from, detectedAt time.Time, reason string, skippedSegments int) uint64 {
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
	return id
}

// supersedeStreamLoss records what a retired stream loses when a new protocol session supersedes its connection (see
// retireSupersededStreams). An open loss episode, or data still waiting behind a gap, is lost from the previous session
// and never recovered; the episode ends at the handshake, so it can mark only encounters before it. The new session's
// context is not invalidated and no effect is touched: both were rebuilt at the handshake and depend on nothing the old
// connection carried.
func (p *Processor) supersedeStreamLoss(state *Stream, at time.Time) {
	id := state.Recovery.lossID
	if !state.Recovery.Active {
		from := state.lastDeliveredAt
		if from.IsZero() || from.After(at) {
			from = at
		}
		id = p.openLossEpisode(state.direction, from, at, kcpLossSuperseded, len(state.Pending))
		if p.DamageStatistics != nil {
			p.DamageStatistics.beginTransportLoss(id, state.direction, from)
		}
	}
	state.Recovery.Active = false
	p.failTransportLoss(id, state.direction)
	if episode, ok := p.transport.episodes[id]; ok {
		episode.ended = true
	}
	if p.DamageStatistics != nil {
		p.DamageStatistics.endTransportLoss(id, at)
		if p.observer != nil {
			p.observer.FlushEncounterTransitions()
		}
	}
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
func (p *Processor) extendTransportLoss(id uint64, skippedSegments int) {
	if episode, ok := p.transport.episodes[id]; ok {
		p.transport.counters(episode.direction).skippedSegments += uint64(skippedSegments)
	}
}

// endTransportLoss records that the stream found a valid frame at to. The data between the episode's start and to
// stays lost; only the transport state changes.
func (p *Processor) endTransportLoss(id uint64, direction string, to time.Time, details transportLossDetails) {
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
	p.Context.observeLossEnd(direction, to)
	if p.DamageStatistics != nil {
		p.DamageStatistics.endTransportLoss(id, to)
		if p.observer != nil {
			p.observer.FlushEncounterTransitions()
		}
	}
}

// failTransportLoss records that an episode could not find a frame within its budget or before the capture ended.
func (p *Processor) failTransportLoss(id uint64, direction string) {
	episode, ok := p.transport.episodes[id]
	if ok && episode.failed {
		return
	}
	if ok {
		episode.failed = true
	}
	p.transport.counters(direction).unrecovered++
	if p.DamageStatistics != nil {
		p.DamageStatistics.failTransportLoss(id)
		if p.observer != nil {
			p.observer.FlushEncounterTransitions()
		}
	}
}

// observeTransportGap tells statistics that a stream's data after since is delayed (open) or no longer delayed.
func (p *Processor) observeTransportGap(streamKey, direction string, since time.Time, open bool) {
	if p.DamageStatistics == nil {
		return
	}
	if open {
		p.DamageStatistics.setTransportGap(streamKey, since)
	} else {
		p.DamageStatistics.setTransportGap(streamKey, time.Time{})
	}
}

// TransportHealthSnapshot combines the live stream state with the episode counters.
func (p *Processor) TransportHealthSnapshot() TransportHealthSnapshot {
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
		health.PendingSegments += len(state.Pending)
		health.PendingBytes += state.PendingBytes
		current := TransportHealthy
		switch {
		case state.Recovery.Active && state.Recovery.failed:
			current = TransportUnrecoverable
		case state.Recovery.Active:
			current = TransportRecovering
		case state.GapOpen:
			current = TransportGapDetected
		}
		if state.GapOpen {
			health.OpenGaps++
			if health.OpenGapSince.IsZero() || state.GapSince.Before(health.OpenGapSince) {
				health.OpenGapSince = state.GapSince
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
	// RevalidatedEvents counts local events kept local while attribution was suspect, because the client referenced
	// their local entity after the loss began; ClientReferencedEntities is the size of that evidence set.
	RevalidatedEvents        uint64 `json:"revalidatedEvents"`
	ClientReferencedEntities int    `json:"clientReferencedEntities"`
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
	// clientReferenced holds the entities the local client named in its own DamageExecuteRequests since the latest
	// inbound loss began. See revalidatesEntity.
	clientReferenced  map[uint64]struct{}
	referencedOrder   []uint64
	revalidatedEvents uint64
}

// maxClientReferencedEntities bounds the per-entity revalidation set; the oldest reference is forgotten first, which
// only makes the gate more conservative.
const maxClientReferencedEntities = 8192

// observeClientReference records that the client referenced an entity while attribution is suspect.
func (c *contextValidity) observeClientReference(entityIDs ...uint64) {
	if !c.attribution.suspect {
		return
	}
	if c.clientReferenced == nil {
		c.clientReferenced = make(map[uint64]struct{})
	}
	for _, id := range entityIDs {
		if id == 0 {
			continue
		}
		if _, ok := c.clientReferenced[id]; ok {
			continue
		}
		c.clientReferenced[id] = struct{}{}
		c.referencedOrder = append(c.referencedOrder, id)
		for len(c.referencedOrder) > maxClientReferencedEntities {
			delete(c.clientReferenced, c.referencedOrder[0])
			c.referencedOrder = c.referencedOrder[1:]
		}
	}
}

// revalidatesEntity reports whether the client has referenced entityID since the latest inbound loss began.
//
// Evidence (research note transport-recovery-backlog, Phase 2B): in 21 archived captures (42 protocol sessions, 73
// scene joins, 3,858 entity adds) no EntityId was ever reused for a different entity inside a protocol session: 65 IDs
// came back after being absent, all with the same configuration and kind, and no local role EntityId ever came back.
// A client request naming an EntityId after the loss therefore names the same entity the registry knew before it, and
// a local classification built on that entity still holds. Without such a reference the event stays unattributed.
func (c *contextValidity) revalidatesEntity(entityID uint64) bool {
	_, ok := c.clientReferenced[entityID]
	return ok
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
		// References made before this loss are not evidence about it.
		c.clientReferenced, c.referencedOrder = nil, nil
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
	// A new scene assigns new EntityIds; references to the old ones prove nothing about it.
	c.clientReferenced, c.referencedOrder = nil, nil
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

func (c *contextValidity) Snapshot() ContextValiditySnapshot {
	snapshot := ContextValiditySnapshot{State: ContextValid, DowngradedEvents: c.downgradedEvents,
		RevalidatedEvents: c.revalidatedEvents, ClientReferencedEntities: len(c.clientReferenced)}
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
func (p *Processor) observeContextSceneJoin(at time.Time) {
	p.Context.resetForScene(at, contextBasisSceneJoin)
	if p.DamageStatistics != nil && !p.Context.battleState.suspect {
		p.DamageStatistics.setBattleStateUnverified(false)
	}
}

func (p *Processor) observeContextSessionStart(at time.Time) {
	p.Context.resetForScene(at, contextBasisSessionStart)
	if p.DamageStatistics != nil {
		p.DamageStatistics.setBattleStateUnverified(false)
	}
}

// observeContextBattleSignal re-establishes the battle state from a local battle-state signal after the loss.
func (p *Processor) observeContextBattleSignal(at time.Time) {
	if !p.Context.battleState.suspect || !p.Context.inboundEvidenceUsable(at) {
		return
	}
	p.Context.battleState.revalidate(at, contextBasisBattleSignal)
	if p.DamageStatistics != nil {
		p.DamageStatistics.setBattleStateUnverified(false)
	}
}

// observeContextLocalRole re-establishes local-player attribution when, after an inbound loss ended, the client
// reports a current role entity that the registry still maps through PlayerSceneGroup to the anchored local player.
// That shows the scene and the team mapping survived the loss.
func (p *Processor) observeContextLocalRole(entityID uint64, at time.Time) {
	if !p.Context.attribution.suspect || !p.Context.inboundEvidenceUsable(at) || p.Entities == nil || p.PlayerRelevance == nil {
		return
	}
	entity, status := p.Entities.Resolve(entityID)
	if status == ResolutionUnknown || entity.EntityID != entityID || !p.PlayerRelevance.isLocalPlayerRoleEntity(entity) {
		return
	}
	p.Context.attribution.revalidate(at, contextBasisLocalRoleReport)
}

// Attribution downgrade reason carried on statistics records.
const contextDowngradeAttribution = "local_player_attribution_suspect"

// gateAttribution keeps an event unattributed while local-player attribution is suspect: a local relation built
// before the loss may no longer be true, and confirmed output must not include it.
func (p *Processor) gateAttribution(record CombatEventRecord) CombatEventRecord {
	if !p.Context.attribution.suspect {
		return record
	}
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		if p.Context.revalidatesEntity(record.Source.EntityID) {
			p.Context.revalidatedEvents++
			return record
		}
		record.Flow = CombatFlowUnattributedOutgoing
	case CombatFlowLocalIncoming:
		if p.Context.revalidatesEntity(record.Target.EntityID) {
			p.Context.revalidatedEvents++
			return record
		}
		record.Flow = CombatFlowUnattributedIncoming
	default:
		return record
	}
	record.Confidence = record.Flow.confidence()
	record.ContextDowngrade = contextDowngradeAttribution
	p.Context.downgradedEvents++
	return record
}

// DataIntegritySnapshot is the read-only trust view for consumers such as the public console and a future UI.
type DataIntegritySnapshot struct {
	Capture    CaptureLayerHealth      `json:"capture"`
	Transport  TransportHealthSnapshot `json:"transport"`
	Context    ContextValiditySnapshot `json:"context"`
	Statistics DataIntegrity           `json:"statistics"`
}

func (p *Processor) dataIntegritySnapshot(statistics DamageStatisticsSnapshot) DataIntegritySnapshot {
	return DataIntegritySnapshot{
		Capture: p.CaptureLayerHealth(), Transport: p.TransportHealthSnapshot(), Context: p.Context.Snapshot(),
		Statistics: statistics.Integrity,
	}
}
