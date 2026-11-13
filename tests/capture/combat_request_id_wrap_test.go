package capture

import (
	"testing"
)

// The client numbers every combat request with a counter that runs 1..32767 and then 0 again (CombatNet.GenerateRpcId),
// so a long protocol session uses one RequestId for several requests: the Build 31 LIVE capture wrapped once, after
// 46,399 combat requests, and 675 damage RequestIds were used a second time. These tests hold the pairing to the rule that
// a RequestId names one request from that request until a later request takes it again, and that every result keeps a
// key of its own.

// combatRequestBody is a CombatSendPackRequest with one RequestData that is no damage request; it only advances the
// client's RequestId counter.
func combatRequestBody(rpcID uint64) []byte {
	request := appendVarintField(nil, fieldCombatRequestDataRequestID, rpcID)
	return appendBytesField(nil, fieldCombatSendPackRequestData, appendBytesField(nil, fieldCombatSendDataRequest, request))
}

func sendCombatRequest(p *processor, seconds float64, rpcID uint64) {
	pairingFrame(p, seconds)
	p.handleDamageRequests(gameFrame{direction: "out", commandID: cmdCombatSendPackRequest, timestamp: pairingAt(seconds)}, combatRequestBody(rpcID))
}

// wrapCounter sends the last RequestIds before the wrap, then the first ones after it, as the client does.
func wrapCounter(p *processor, seconds float64) {
	sendCombatRequest(p, seconds, 32766)
	sendCombatRequest(p, seconds, 32767)
	sendCombatRequest(p, seconds, 0) // proto3 omits the 0, as on the wire
	sendCombatRequest(p, seconds, 1)
}

// damageRequestWithoutID is a damage request whose RequestId is the proto3 default 0, so the field is absent.
func damageRequestWithoutID(attackerID, targetID uint64) []byte {
	u3n := appendVarintField(nil, fieldDamageExecuteRequestDamageID, 70010001)
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestTargetEntityID, targetID)
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestAttackerEntityID, attackerID)
	request := appendBytesField(nil, fieldCombatRequestDataDamageExecuteRequest, u3n)
	return appendBytesField(nil, fieldCombatSendPackRequestData, appendBytesField(nil, fieldCombatSendDataRequest, request))
}

// damageResponseWithoutID answers it: CombatResponseData without RequestId.
func damageResponseWithoutID(amount int32) []byte {
	lis := appendVarintField(nil, fieldDamageExecuteResponseDamage, uint64(uint32(amount)))
	response := appendBytesField(nil, fieldCombatResponseDataDamageExecuteResponse, lis)
	item := appendBytesField(nil, fieldCombatReceiveDataCombatResponseData, response)
	return appendBytesField(nil, fieldCombatSendPackResponseReceivePackNotify, appendBytesField(nil, fieldCombatReceivePackNotifyData, item))
}

func TestCombatRequestCounterEpochs(t *testing.T) {
	var counter combatRequestCounter
	for _, step := range []struct{ id, epoch uint64 }{{30000, 0}, {30002, 0}, {30001, 0}, {32767, 0}, {0, 1}, {1, 1}, {16000, 1}, {32767, 1}, {3, 2}} {
		if got := counter.observe(step.id); got != step.epoch {
			t.Fatalf("RequestId %d: epoch %d, want %d", step.id, got, step.epoch)
		}
	}
	if got := extendedRequestID(7, 0); got != 7 {
		t.Fatalf("before the first wrap the key is the RequestId: %d", got)
	}
	if got := extendedRequestID(7, 2); got != 2*32768+7 {
		t.Fatalf("extended id = %d", got)
	}
}

// A request that reuses a completed RequestId after the wrap is paired with its own response, and both results count.
func TestReusedRequestIDAfterWrapCountsBothResults(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 7)
	sendDamageResponse(p, 1.1, 7, 100)
	wrapCounter(p, 600)
	sendDamageRequest(p, 600.5, 7)
	sendDamageResponse(p, 600.6, 7, 250)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 350 || got.OutgoingEvents != 2 || got.DuplicateEvents != 0 {
		t.Fatalf("statistics = %+v", got)
	}
	pairing := p.rpcPairingSnapshot()
	if pairing.Paired != 2 || pairing.ConflictingResponses != 0 || pairing.DuplicateResponses != 0 || pairing.PendingRequests != 0 || pairing.ReusedRequestIDs != 1 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// After the wrap, the response of a reused RequestId can arrive before its request (the outbound stream waits behind a
// gap). It is held for its request like any other early response instead of being taken for a copy of the old result.
func TestReusedRequestIDResponseBeforeItsRequest(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 9)
	sendDamageResponse(p, 1.1, 9, 300)
	wrapCounter(p, 600)
	sendDamageResponse(p, 600.5, 9, 400)
	sendDamageRequest(p, 600.9, 9)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 700 || got.OutgoingEvents != 2 {
		t.Fatalf("statistics = %+v", got)
	}
	pairing := p.rpcPairingSnapshot()
	if pairing.Paired != 2 || pairing.PairedOutOfOrder != 1 || pairing.ConflictingResponses != 0 || pairing.ReusedRequestIDs != 1 || pairing.HeldResponses != 0 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// A copy that arrives right after its response is still a copy, the same bytes or not: one RequestId, one result.
func TestCopyWithinWindowStaysOneResult(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 11)
	sendDamageResponse(p, 1.1, 11, 500)
	sendDamageResponse(p, 1.2, 11, 500)
	sendDamageResponse(p, 1.3, 11, 501)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 500 || got.OutgoingEvents != 1 {
		t.Fatalf("statistics = %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.DuplicateResponses != 1 || pairing.ConflictingResponses != 1 || pairing.ReusedRequestIDs != 0 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// A response with the RequestId of one processed long ago, whose request never comes, is a response without its request:
// unpaired, never counted, never taken for a copy.
func TestLateResponseOfACompletedRequestIDWithoutRequest(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 12)
	sendDamageResponse(p, 1.1, 12, 500)
	sendDamageResponse(p, 600, 12, 900)
	sendCombatRequest(p, 600+damageResponseHoldWindow.Seconds()+1, 40)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 500 || got.OutgoingEvents != 1 {
		t.Fatalf("statistics = %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.UnpairedExpired != 1 || pairing.ConflictingResponses != 0 || pairing.HeldResponses != 0 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// The RequestId after 32767 is 0, which proto3 leaves off the wire in both the request and its response.
func TestRequestIDZeroAfterWrapIsPaired(t *testing.T) {
	p := newPairingTestProcessor()
	sendCombatRequest(p, 1, 32766)
	sendCombatRequest(p, 1, 32767)
	pairingFrame(p, 1.5)
	p.handleDamageRequests(gameFrame{direction: "out", commandID: cmdCombatSendPackRequest, timestamp: pairingAt(1.5)}, damageRequestWithoutID(100, 200))
	pairingFrame(p, 1.6)
	if err := p.handleCombatReceivePacks(gameFrame{direction: "in", commandID: cmdCombatSendPackResponse, timestamp: pairingAt(1.6)}, damageResponseWithoutID(640)); err != nil {
		t.Fatal(err)
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 640 || got.OutgoingEvents != 1 {
		t.Fatalf("statistics = %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.Paired != 1 || pairing.PendingRequests != 0 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// An unanswered request whose RequestId a later request takes stays unanswered, counted as superseded; the later
// request is paired with the response that follows it.
func TestUnansweredRequestIsSupersededByReuse(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 13)
	wrapCounter(p, 600)
	sendDamageRequest(p, 600.5, 13)
	sendDamageResponse(p, 600.6, 13, 80)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 80 || got.OutgoingEvents != 1 {
		t.Fatalf("statistics = %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.SupersededRequests != 1 || pairing.Paired != 1 || pairing.PendingRequests != 0 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// The normalized record of each result carries a key of its own: the first use keeps its RequestId, the reuse the
// RequestId continued past the wrap, so a statistics owner or a history never takes the second for a repeat.
func TestReusedRequestIDResultsKeepDistinctKeys(t *testing.T) {
	event := combatEvent{
		SessionNumber: 1, RPCID: 21, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 10, Timestamp: pairingAt(1), DamageID: 70010001,
	}
	first, ok := normalizeCombatEvent(event, playerRelationLocal, playerRelationUnrelated)
	if !ok || first.Key != (CombatEventKey{ProtocolSession: 1, RPCID: 21}) {
		t.Fatalf("first use: ok=%v key=%+v", ok, first.Key)
	}
	event.RequestEpoch = 1
	reused, ok := normalizeCombatEvent(event, playerRelationLocal, playerRelationUnrelated)
	if !ok || reused.Key != (CombatEventKey{ProtocolSession: 1, RPCID: 32768 + 21}) {
		t.Fatalf("reuse: ok=%v key=%+v", ok, reused.Key)
	}
}
