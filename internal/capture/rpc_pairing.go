package capture

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"time"
)

// Cross-direction DamageExecute pairing.
//
// Requests travel client to server (outbound) and responses server to client (inbound), on two independent KCP
// streams. When the outbound stream waits behind a gap (up to kcpGapSkipTimeout) the inbound responses keep arriving,
// so a response can reach the decoder before its own request. Pairing is by the RPC id of the protocol session, the
// same key the in-order path uses; arrival time is never used to match.
//
// A response without a pending request is held for damageResponseHoldWindow of capture time. If its request arrives in
// that window the pair is processed then, with the response's own frame time, as an in-order pair would be.
// Otherwise the response stays unpaired: its request was lost, was sent before the capture began, or never existed;
// nothing is estimated from it. Held responses are bounded in count and bytes, discarded at a protocol session
// change, and counted unpaired at the end of the capture. Requests keep their existing bounded pending table.
//
// Removals during a hold. The server sends the response to a killing blow and the EntityRemoveNotify of its target in
// the same datagram. In order, the pair is evaluated before the removal; a held pair is evaluated when its request
// arrives, after the removal, and its target would no longer resolve, turning confirmed damage into unattributed
// damage. While responses are held, the state an entity had just before an EntityRemoveNotify is therefore kept, and a
// held pair is evaluated with the entities removed after its response rewound to that state; the current state is put
// back right after. Only removals are rewound: other changes between a response and its delayed request, such as a
// scene join, still apply.

const (
	// damageResponseHoldWindow covers a request delayed by an outbound gap: the gap is skipped after kcpGapSkipTimeout
	// and its waiting segments are delivered at once, so a request trails its response by about that much at most.
	// Twice the timeout leaves margin for the datagram that triggers the skip.
	damageResponseHoldWindow   = 2 * kcpGapSkipTimeout
	maxHeldDamageResponses     = 4096
	maxHeldDamageResponseBytes = 4 << 20
)

type heldDamageResponse struct {
	payload    []byte
	frameTime  time.Time
	generation uint64
	sequence   uint64
}

type heldResponseRef struct {
	rpcID    uint64
	sequence uint64
}

// entityAttributionState is what a damage pair reads about one entity: its runtime record, its lifecycle, whether it is
// a local role entity, and its correlated source role.
type entityAttributionState struct {
	entity        runtimeEntity
	present       bool
	lifecycle     entityLifecycle
	hasLifecycle  bool
	roleEntity    bool
	sourceRole    sourceRoleEvidence
	hasSourceRole bool
}

// heldRemoval is the state of an entity just before an EntityRemoveNotify removed it while responses were held.
type heldRemoval struct {
	entityID uint64
	// afterHold is the hold sequence at the removal: held responses up to it arrived before the removal.
	afterHold uint64
	before    entityAttributionState
}

// RPCPairingSnapshot reports DamageExecute pairing for consumers and diagnostics.
type RPCPairingSnapshot struct {
	Requests  int `json:"requests"`
	Responses int `json:"responses"`
	// StandaloneResponses of Responses came in a standalone CombatReceivePackNotify, the rest in a CombatSendPackResponse.
	StandaloneResponses int `json:"standaloneResponses"`
	// Paired counts responses processed with their request; PairedOutOfOrder of them arrived before the request.
	Paired           uint64 `json:"paired"`
	PairedOutOfOrder uint64 `json:"pairedOutOfOrder"`
	// MaxOutOfOrderWaitMs is the longest wait of a held response for its request, on the capture clock high-water mark.
	MaxOutOfOrderWaitMs float64 `json:"maxOutOfOrderWaitMs"`
	// Unpaired responses never met their request, split by why the hold ended.
	UnpairedExpired       uint64 `json:"unpairedExpired"`
	UnpairedEvicted       uint64 `json:"unpairedEvicted"`
	UnpairedSessionChange uint64 `json:"unpairedSessionChange"`
	UnpairedCaptureEnd    uint64 `json:"unpairedCaptureEnd"`
	// RewoundEntities counts entities a held pair saw in their state before a removal that followed its response.
	RewoundEntities    uint64 `json:"rewoundEntities"`
	DuplicateResponses int    `json:"duplicateResponses"`
	// ConflictingResponses repeat the RequestId of a response already taken with other content; only the first counts.
	ConflictingResponses int `json:"conflictingResponses"`
	InvalidResponses     int `json:"invalidResponses"`
	PendingRequests      int `json:"pendingRequests"`
	EvictedRequests      int `json:"evictedRequests"`
	HeldResponses        int `json:"heldResponses"`
}

type rpcPairingState struct {
	held           map[uint64]heldDamageResponse
	order          []heldResponseRef
	head           int
	bytes          int
	sequence       uint64
	frameHighWater time.Time
	paired         uint64
	outOfOrder     uint64
	maxWait        time.Duration
	expired        uint64
	evicted        uint64
	sessionChange  uint64
	captureEnd     uint64
	// removals keeps entities removed while responses were held, oldest first, bounded like the holds.
	removals []heldRemoval
	rewound  uint64
}

// observePairingClock advances the capture clock the hold window runs on.
func (p *processor) observePairingClock(at time.Time) {
	if at.After(p.pairing.frameHighWater) {
		p.pairing.frameHighWater = at
	}
}

// holdDamageResponse keeps a response whose request has not been seen yet.
func (p *processor) holdDamageResponse(rpcID uint64, payload []byte) {
	state := &p.pairing
	if held, exists := state.held[rpcID]; exists {
		if bytes.Equal(held.payload, payload) {
			p.stats.duplicateResponses++
		} else {
			p.stats.conflictingResponses++
		}
		return
	}
	if state.held == nil {
		state.held = make(map[uint64]heldDamageResponse)
	}
	state.sequence++
	state.held[rpcID] = heldDamageResponse{
		payload: append([]byte(nil), payload...), frameTime: p.currentFrameTime, generation: p.sessionGeneration, sequence: state.sequence,
	}
	state.order = append(state.order, heldResponseRef{rpcID: rpcID, sequence: state.sequence})
	state.bytes += len(payload)
	for len(state.held) > maxHeldDamageResponses || state.bytes > maxHeldDamageResponseBytes {
		if !p.dropOldestHeldResponse(&state.evicted) {
			break
		}
	}
}

// dropOldestHeldResponse ends the oldest hold as unpaired and counts it under reason.
func (p *processor) dropOldestHeldResponse(reason *uint64) bool {
	state := &p.pairing
	for state.head < len(state.order) {
		ref := state.order[state.head]
		state.head++
		held, ok := state.held[ref.rpcID]
		if !ok || held.sequence != ref.sequence {
			continue
		}
		delete(state.held, ref.rpcID)
		state.bytes -= len(held.payload)
		*reason++
		p.stats.unpairedResponses++
		p.compactHeldOrder()
		p.pruneRemovals()
		return true
	}
	return false
}

func (p *processor) compactHeldOrder() {
	state := &p.pairing
	if state.head < 1024 || state.head*2 < len(state.order) {
		return
	}
	state.order = append([]heldResponseRef(nil), state.order[state.head:]...)
	state.head = 0
}

// expireHeldResponses ends holds older than the hold window on the capture clock.
func (p *processor) expireHeldResponses() {
	state := &p.pairing
	for state.head < len(state.order) {
		ref := state.order[state.head]
		held, ok := state.held[ref.rpcID]
		if ok && held.sequence == ref.sequence && state.frameHighWater.Sub(held.frameTime) <= damageResponseHoldWindow {
			return
		}
		if !ok || held.sequence != ref.sequence {
			state.head++
			continue
		}
		p.dropOldestHeldResponse(&state.expired)
	}
}

// pairHeldResponse processes a held response once its request has been registered. The pair is evaluated at the
// response's own frame time, so the event is the one an in-order arrival would have produced.
func (p *processor) pairHeldResponse(rpcID uint64) {
	state := &p.pairing
	held, ok := state.held[rpcID]
	if !ok {
		return
	}
	delete(state.held, rpcID)
	state.bytes -= len(held.payload)
	if held.generation != p.sessionGeneration {
		// Cannot happen while holds are discarded at session changes; never pair across sessions.
		state.sessionChange++
		p.stats.unpairedResponses++
		return
	}
	state.outOfOrder++
	// Measured on the capture clock: a request released from a gap keeps its own, earlier arrival time.
	if wait := state.frameHighWater.Sub(held.frameTime); wait > state.maxWait {
		state.maxWait = wait
	}
	saved := p.currentFrameTime
	p.currentFrameTime = held.frameTime
	restore := p.rewindRemovals(held.sequence)
	p.handleOneDamageResult(rpcID, held.payload)
	restore()
	p.currentFrameTime = saved
	p.pruneRemovals()
}

// rememberRemovalsForHeldResponses keeps, while any response is held, the state of the entities an EntityRemoveNotify is
// about to remove. Without held responses nothing is kept and nothing is parsed twice.
func (p *processor) rememberRemovalsForHeldResponses(body []byte) {
	state := &p.pairing
	if len(state.held) == 0 {
		return
	}
	// A malformed notify is reported by the removal itself; the IDs read before the fault are still removed there.
	ids, _ := entityRemoveNotifyIDs(body)
	for _, id := range ids {
		state.removals = append(state.removals, heldRemoval{entityID: id, afterHold: state.sequence, before: p.entityAttributionState(id)})
	}
	if over := len(state.removals) - maxHeldDamageResponses; over > 0 {
		state.removals = append([]heldRemoval(nil), state.removals[over:]...)
	}
}

// rewindRemovals puts the entities removed after the held response with this sequence back into their state before the
// first such removal, and returns the function that restores the current state.
func (p *processor) rewindRemovals(sequence uint64) func() {
	state := &p.pairing
	type rewound struct {
		entityID uint64
		current  entityAttributionState
	}
	var undo []rewound
	for _, removal := range state.removals {
		if removal.afterHold < sequence || slices.ContainsFunc(undo, func(r rewound) bool { return r.entityID == removal.entityID }) {
			continue
		}
		current := p.entityAttributionState(removal.entityID)
		undo = append(undo, rewound{entityID: removal.entityID, current: current})
		p.setEntityAttributionState(removal.entityID, removal.before.withLaterSourceRoles(current))
		state.rewound++
	}
	return func() {
		for i := len(undo) - 1; i >= 0; i-- {
			if !undo[i].current.present {
				// The entity is still gone: settle what the pair credited to it, as its removal did.
				p.settleRemovedSource(undo[i].entityID)
			}
			p.setEntityAttributionState(undo[i].entityID, undo[i].current)
		}
	}
}

// pruneRemovals forgets removals that no held response predates.
func (p *processor) pruneRemovals() {
	state := &p.pairing
	if len(state.removals) == 0 {
		return
	}
	if len(state.held) == 0 {
		state.removals = nil
		return
	}
	oldest := uint64(math.MaxUint64)
	for _, held := range state.held {
		oldest = min(oldest, held.sequence)
	}
	state.removals = slices.DeleteFunc(state.removals, func(r heldRemoval) bool { return r.afterHold < oldest })
}

func (p *processor) entityAttributionState(id uint64) entityAttributionState {
	var s entityAttributionState
	s.entity, s.present = p.entities.entities[id]
	s.lifecycle, s.hasLifecycle = p.entities.lifecycles[id]
	s.roleEntity = p.playerRelevance.isRoleEntity(id)
	s.sourceRole, s.hasSourceRole = p.sourceRoles.bySource[id]
	return s
}

// withLaterSourceRoles adds the source role evidence observed since the removal, such as the skill of the delayed
// request itself, which an in-order pair would have seen. The result has its own skill set; neither input is changed.
func (s entityAttributionState) withLaterSourceRoles(later entityAttributionState) entityAttributionState {
	if !later.hasSourceRole {
		return s
	}
	if !s.hasSourceRole {
		s.sourceRole, s.hasSourceRole = later.sourceRole, true
		return s
	}
	before, after := s.sourceRole, later.sourceRole
	merged := sourceRoleEvidence{
		roleID: before.roleID, skillIDs: make(map[uint64]struct{}, len(before.skillIDs)+len(after.skillIDs)),
		conflict: before.conflict || after.conflict || before.roleID != after.roleID, lastSeen: max(before.lastSeen, after.lastSeen),
	}
	for id := range before.skillIDs {
		merged.skillIDs[id] = struct{}{}
	}
	for id := range after.skillIDs {
		merged.skillIDs[id] = struct{}{}
	}
	s.sourceRole = merged
	return s
}

// setEntityAttributionState writes the maps directly: a temporary rewind must not evict other entries or reorder them.
func (p *processor) setEntityAttributionState(id uint64, s entityAttributionState) {
	if s.present {
		p.entities.entities[id] = s.entity
	} else {
		delete(p.entities.entities, id)
	}
	if s.hasLifecycle {
		p.entities.lifecycles[id] = s.lifecycle
	} else {
		delete(p.entities.lifecycles, id)
	}
	if p.playerRelevance != nil {
		if s.roleEntity {
			p.playerRelevance.roleEntities[id] = struct{}{}
		} else {
			delete(p.playerRelevance.roleEntities, id)
		}
	}
	if s.hasSourceRole {
		p.sourceRoles.bySource[id] = s.sourceRole
	} else {
		delete(p.sourceRoles.bySource, id)
	}
}

// settleRemovedSource credits or drops the development summary credits held for a removed source entity.
func (p *processor) settleRemovedSource(id uint64) {
	if roleID, correlated := p.sourceRoles.correlatedRole(id); correlated {
		p.backfillEncounterSummary(id, roleID)
	}
	p.removePendingSummaryCredits(id)
}

// discardHeldResponses ends every hold, for a protocol session change or the end of the capture.
func (p *processor) discardHeldResponses(reason *uint64) {
	state := &p.pairing
	*reason += uint64(len(state.held))
	p.stats.unpairedResponses += len(state.held)
	state.held, state.order, state.head, state.bytes, state.removals = nil, nil, 0, 0, nil
}

func (p *processor) rpcPairingSnapshot() RPCPairingSnapshot {
	state := p.pairing
	return RPCPairingSnapshot{
		Requests: p.stats.damageRequests, Responses: p.stats.candidateResponses, StandaloneResponses: p.stats.standaloneNotifyResponses,
		Paired: state.paired, PairedOutOfOrder: state.outOfOrder, MaxOutOfOrderWaitMs: float64(state.maxWait) / float64(time.Millisecond),
		UnpairedExpired: state.expired, UnpairedEvicted: state.evicted, UnpairedSessionChange: state.sessionChange, UnpairedCaptureEnd: state.captureEnd,
		RewoundEntities: state.rewound, DuplicateResponses: p.stats.duplicateResponses, ConflictingResponses: p.stats.conflictingResponses,
		InvalidResponses: p.stats.invalidResponses, PendingRequests: len(p.pending), EvictedRequests: p.stats.evictedPending, HeldResponses: len(state.held),
	}
}

// rpcPairingLine formats the pairing counters for development diagnostics.
func rpcPairingLine(s RPCPairingSnapshot) string {
	return fmt.Sprintf("damage_rpc_pairing requests=%d responses=%d standalone_responses=%d paired=%d paired_out_of_order=%d max_out_of_order_wait_ms=%.1f unpaired_expired=%d unpaired_evicted=%d unpaired_session_change=%d unpaired_capture_end=%d rewound_entities=%d duplicate_responses=%d conflicting_responses=%d invalid_responses=%d pending_requests=%d evicted_requests=%d held_responses=%d",
		s.Requests, s.Responses, s.StandaloneResponses, s.Paired, s.PairedOutOfOrder, s.MaxOutOfOrderWaitMs, s.UnpairedExpired, s.UnpairedEvicted,
		s.UnpairedSessionChange, s.UnpairedCaptureEnd, s.RewoundEntities, s.DuplicateResponses, s.ConflictingResponses, s.InvalidResponses, s.PendingRequests, s.EvictedRequests, s.HeldResponses)
}
