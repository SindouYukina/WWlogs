package capture

import (
	"fmt"
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
// that window the pair is processed then, with the response's own frame time, exactly as an in-order pair would be.
// Otherwise the response stays unpaired: its request was lost, was sent before the capture began, or never existed;
// nothing is estimated from it. Held responses are bounded in count and bytes, discarded at a protocol session
// change, and counted unpaired at the end of the capture. Requests keep their existing bounded pending table.

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

// RPCPairingSnapshot reports DamageExecute pairing for consumers and diagnostics.
type RPCPairingSnapshot struct {
	Requests  int `json:"requests"`
	Responses int `json:"responses"`
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
	DuplicateResponses    int    `json:"duplicateResponses"`
	InvalidResponses      int    `json:"invalidResponses"`
	PendingRequests       int    `json:"pendingRequests"`
	EvictedRequests       int    `json:"evictedRequests"`
	HeldResponses         int    `json:"heldResponses"`
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
	if _, exists := state.held[rpcID]; exists {
		p.stats.duplicateResponses++
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
	p.handleOneDamageResult(rpcID, held.payload)
	p.currentFrameTime = saved
}

// discardHeldResponses ends every hold, for a protocol session change or the end of the capture.
func (p *processor) discardHeldResponses(reason *uint64) {
	state := &p.pairing
	*reason += uint64(len(state.held))
	p.stats.unpairedResponses += len(state.held)
	state.held, state.order, state.head, state.bytes = nil, nil, 0, 0
}

func (p *processor) rpcPairingSnapshot() RPCPairingSnapshot {
	state := p.pairing
	return RPCPairingSnapshot{
		Requests: p.stats.damageRequests, Responses: p.stats.candidateResponses,
		Paired: state.paired, PairedOutOfOrder: state.outOfOrder, MaxOutOfOrderWaitMs: float64(state.maxWait) / float64(time.Millisecond),
		UnpairedExpired: state.expired, UnpairedEvicted: state.evicted, UnpairedSessionChange: state.sessionChange, UnpairedCaptureEnd: state.captureEnd,
		DuplicateResponses: p.stats.duplicateResponses, InvalidResponses: p.stats.invalidResponses,
		PendingRequests: len(p.pending), EvictedRequests: p.stats.evictedPending, HeldResponses: len(state.held),
	}
}

// rpcPairingLine formats the pairing counters for development diagnostics.
func rpcPairingLine(s RPCPairingSnapshot) string {
	return fmt.Sprintf("damage_rpc_pairing requests=%d responses=%d paired=%d paired_out_of_order=%d max_out_of_order_wait_ms=%.1f unpaired_expired=%d unpaired_evicted=%d unpaired_session_change=%d unpaired_capture_end=%d duplicate_responses=%d invalid_responses=%d pending_requests=%d evicted_requests=%d held_responses=%d",
		s.Requests, s.Responses, s.Paired, s.PairedOutOfOrder, s.MaxOutOfOrderWaitMs, s.UnpairedExpired, s.UnpairedEvicted,
		s.UnpairedSessionChange, s.UnpairedCaptureEnd, s.DuplicateResponses, s.InvalidResponses, s.PendingRequests, s.EvictedRequests, s.HeldResponses)
}
