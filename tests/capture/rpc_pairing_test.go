package capture

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

var pairingTestBase = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func pairingAt(seconds float64) time.Time {
	return pairingTestBase.Add(time.Duration(seconds * float64(time.Second)))
}

// pairingFrame advances the processor clock the way handleFrame does for one decoded frame.
func pairingFrame(p *processor, seconds float64) {
	p.currentFrameTime = pairingAt(seconds)
	p.observePairingClock(p.currentFrameTime)
}

func damageRequestBody(rpcID, attackerID, targetID uint64) []byte {
	u3n := appendVarintField(nil, fieldDamageExecuteRequestDamageID, 70010001)      // DamageId
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestTargetEntityID, targetID) // TargetEntityId
	u3n = appendVarintField(u3n, fieldDamageExecuteRequestAttackerEntityID, attackerID)
	request := appendVarintField(nil, fieldCombatRequestDataRequestID, rpcID)
	request = appendBytesField(request, fieldCombatRequestDataDamageExecuteRequest, u3n)
	return appendBytesField(nil, fieldCombatSendPackRequestData, appendBytesField(nil, fieldCombatSendDataRequest, request))
}

func damageResponseBody(rpcID uint64, amount int32) []byte {
	lis := appendVarintField(nil, fieldDamageExecuteResponseErrorCode, 0)
	lis = appendVarintField(lis, fieldDamageExecuteResponseImmuneType, 0)
	lis = appendVarintField(lis, fieldDamageExecuteResponseDamage, uint64(uint32(amount)))
	response := appendVarintField(nil, fieldCombatResponseDataRequestID, rpcID)
	response = appendBytesField(response, fieldCombatResponseDataDamageExecuteResponse, lis)
	item := appendBytesField(nil, fieldCombatReceiveDataCombatResponseData, response)
	return appendBytesField(nil, fieldCombatSendPackResponseReceivePackNotify, appendBytesField(nil, fieldCombatReceivePackNotifyData, item))
}

// newPairingTestProcessor has one local role entity (100) and one monster (200).
func newPairingTestProcessor() *processor {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.entities = newEntityResolver()
	p.masters = &gameMaster{damage: map[uint64]damageMasterRecord{
		70010001: {ID: 70010001, CalculateType: 0, SourceHash: strings.Repeat("a", 64), SourceTable: "damage"},
	}}
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	p.observeLocalRoleEntity(100)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9001, true
	})
	return p
}

func sendDamageRequest(p *processor, seconds float64, rpcID uint64) {
	pairingFrame(p, seconds)
	p.handleDamageRequests(gameFrame{direction: "out", commandID: cmdCombatSendPackRequest, timestamp: pairingAt(seconds)}, damageRequestBody(rpcID, 100, 200))
}

func sendDamageResponse(p *processor, seconds float64, rpcID uint64, amount int32) {
	pairingFrame(p, seconds)
	if err := p.handleCombatReceivePacks(gameFrame{direction: "in", commandID: cmdCombatSendPackResponse, timestamp: pairingAt(seconds)}, damageResponseBody(rpcID, amount)); err != nil {
		panic(err)
	}
}

func TestDamageResponseBeforeRequestPairsAtResponseTime(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageResponse(p, 1, 42, 500)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 || got.OutgoingEvents != 0 {
		t.Fatalf("a response without its request was counted: %+v", got)
	}
	if got := p.rpcPairingSnapshot(); got.HeldResponses != 1 || got.Paired != 0 {
		t.Fatalf("response was not held for its request: %+v", got)
	}
	sendDamageRequest(p, 2.5, 42)
	got := p.damageStatistics.Snapshot()
	if got.DamageDealt != 500 || got.OutgoingEvents != 1 {
		t.Fatalf("out-of-order pair was not counted exactly once: %+v", got)
	}
	encounter := got.Encounters.Current
	if encounter == nil || !encounter.FirstActivityAt.Equal(pairingAt(1)) {
		t.Fatalf("out-of-order pair must use the response frame time, got %+v", encounter)
	}
	pairing := p.rpcPairingSnapshot()
	if pairing.Paired != 1 || pairing.PairedOutOfOrder != 1 || pairing.HeldResponses != 0 || pairing.PendingRequests != 0 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
	if pairing.MaxOutOfOrderWaitMs != 1500 {
		t.Fatalf("max out-of-order wait = %.1f ms, want 1500", pairing.MaxOutOfOrderWaitMs)
	}
	if !p.currentFrameTime.Equal(pairingAt(2.5)) {
		t.Fatalf("processor clock was not restored after the late pair: %s", p.currentFrameTime)
	}
}

func TestInOrderPairIsUnchangedByHolding(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 7)
	sendDamageResponse(p, 1.2, 7, 300)
	got := p.damageStatistics.Snapshot()
	if got.DamageDealt != 300 || got.OutgoingEvents != 1 {
		t.Fatalf("in-order pair = %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.Paired != 1 || pairing.PairedOutOfOrder != 0 || pairing.HeldResponses != 0 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
}

func TestDuplicateResponsesNeverCountTwice(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageResponse(p, 1, 9, 100)
	sendDamageResponse(p, 1.1, 9, 100) // retransmitted while held
	sendDamageRequest(p, 1.5, 9)
	sendDamageResponse(p, 1.6, 9, 100) // retransmitted after the pair
	sendDamageRequest(p, 1.7, 9)       // retransmitted request after the pair: nothing is held for it
	got := p.damageStatistics.Snapshot()
	if got.DamageDealt != 100 || got.OutgoingEvents != 1 {
		t.Fatalf("duplicates changed the totals: %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.DuplicateResponses != 2 || pairing.Paired != 1 || pairing.PairedOutOfOrder != 1 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
}

func TestHeldResponseExpiresAndLateRequestStaysUnpaired(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageResponse(p, 1, 11, 100)
	// Another frame later than the hold window ends the hold.
	sendDamageResponse(p, 1+damageResponseHoldWindow.Seconds()+0.5, 12, 50)
	sendDamageRequest(p, 1+damageResponseHoldWindow.Seconds()+0.6, 11)
	got := p.damageStatistics.Snapshot()
	if got.DamageDealt != 0 {
		t.Fatalf("an expired response was paired with a late request: %+v", got)
	}
	pairing := p.rpcPairingSnapshot()
	if pairing.UnpairedExpired != 1 || pairing.Paired != 0 || pairing.PendingRequests != 1 || pairing.HeldResponses != 1 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
	if p.stats.unpairedResponses != 1 {
		t.Fatalf("unpaired responses = %d, want 1", p.stats.unpairedResponses)
	}
}

func TestHeldResponsesWithinWindowPairInAnyOrder(t *testing.T) {
	p := newPairingTestProcessor()
	for i, rpcID := range []uint64{21, 22, 23} {
		sendDamageResponse(p, 1+float64(i)*0.1, rpcID, int32(10*(i+1)))
	}
	for i, rpcID := range []uint64{23, 21, 22} {
		sendDamageRequest(p, 3+float64(i)*0.1, rpcID)
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 60 || got.OutgoingEvents != 3 {
		t.Fatalf("reordered pairs = %+v", got)
	}
}

func TestHeldResponsesAreBoundedByCount(t *testing.T) {
	p := newPairingTestProcessor()
	pairingFrame(p, 1)
	for rpcID := uint64(1); rpcID <= maxHeldDamageResponses+1; rpcID++ {
		p.handleOneDamageResult(rpcID, appendVarintField(nil, fieldDamageExecuteResponseDamage, 1))
	}
	pairing := p.rpcPairingSnapshot()
	if pairing.HeldResponses != maxHeldDamageResponses || pairing.UnpairedEvicted != 1 {
		t.Fatalf("held responses were not bounded: %+v", pairing)
	}
	sendDamageRequest(p, 1.5, 1) // the evicted, oldest response
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("an evicted response was paired: %+v", got)
	}
}

func TestHeldResponsesNeverPairAcrossProtocolSessions(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageResponse(p, 1, 31, 100)
	// The session change path discards every hold before RPC ids restart.
	p.discardHeldResponses(&p.pairing.sessionChange)
	p.sessionGeneration++
	sendDamageRequest(p, 1.5, 31)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("a response of the previous session was paired: %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.UnpairedSessionChange != 1 || pairing.Paired != 0 {
		t.Fatalf("pairing counters = %+v", pairing)
	}

	// Even a hold that survived a generation change is refused.
	sendDamageResponse(p, 2, 32, 100)
	p.sessionGeneration++
	sendDamageRequest(p, 2.5, 32)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("a hold from another generation was paired: %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.UnpairedSessionChange != 2 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
}

func TestHeldResponsesAreUnpairedAtCaptureEnd(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageResponse(p, 1, 41, 100)
	sendDamageResponse(p, 1.1, 42, 100)
	p.finishCapture()
	pairing := p.rpcPairingSnapshot()
	if pairing.UnpairedCaptureEnd != 2 || pairing.HeldResponses != 0 || p.stats.unpairedResponses != 2 {
		t.Fatalf("pairing counters at capture end = %+v unpaired=%d", pairing, p.stats.unpairedResponses)
	}
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("held responses were counted at capture end: %+v", got)
	}
}

func TestInvalidHeldResponseStaysInvalid(t *testing.T) {
	p := newPairingTestProcessor()
	pairingFrame(p, 1)
	immune := appendVarintField(nil, fieldDamageExecuteResponseImmuneType, 1)
	immune = appendVarintField(immune, fieldDamageExecuteResponseDamage, 100)
	p.handleOneDamageResult(51, immune)
	sendDamageRequest(p, 1.5, 51)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("an immune response became damage: %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.InvalidResponses != 1 || pairing.Paired != 1 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
}

func entityRemoveBody(ids ...uint64) []byte {
	body := appendVarintField(nil, fieldEntityRemoveNotifyIsRemove, 1)
	for _, id := range ids {
		body = appendBytesField(body, fieldEntityRemoveNotifyRemoveInfos, appendVarintField(nil, fieldEntityRemoveInfoEntityID, id))
	}
	return body
}

func removeEntities(t *testing.T, p *processor, seconds float64, ids ...uint64) {
	t.Helper()
	pairingFrame(p, seconds)
	if err := p.handleEntityRemoveNotify(entityRemoveBody(ids...)); err != nil {
		t.Fatal(err)
	}
}

// The server sends the response to a killing blow and the EntityRemoveNotify of its target in one datagram. When that
// datagram is decoded before the request (the outbound stream waited behind a gap), the pair must still be the in-order
// one: confirmed local damage, not unattributed damage. Observed in LIVE_20261103_122337, LIVE_20261103_165511 and
// LIVE_20261107_201525 under the cross-direction perturbation.
func TestHeldPairSeesATargetRemovedAfterItsResponse(t *testing.T) {
	inOrder := newPairingTestProcessor()
	sendDamageRequest(inOrder, 1, 42)
	sendDamageResponse(inOrder, 1.01, 42, 500)
	removeEntities(t, inOrder, 1.01, 200)

	held := newPairingTestProcessor()
	sendDamageResponse(held, 1.01, 42, 500)
	removeEntities(t, held, 1.01, 200)
	sendDamageRequest(held, 1.02, 42)

	want, got := inOrder.damageStatistics.Snapshot(), held.damageStatistics.Snapshot()
	if want.DamageDealt != 500 || want.UnattributedOutgoingDamage != 0 {
		t.Fatalf("in-order reference = %+v", want)
	}
	if got.DamageDealt != want.DamageDealt || got.UnattributedOutgoingDamage != want.UnattributedOutgoingDamage || got.OutgoingEvents != want.OutgoingEvents {
		t.Fatalf("held pair after its target's removal: dealt %d unattributed %d, want dealt %d unattributed %d",
			got.DamageDealt, got.UnattributedOutgoingDamage, want.DamageDealt, want.UnattributedOutgoingDamage)
	}
	// The rewind lasts for the pair only: the target is removed again, and nothing is kept once no response is held.
	if _, present := held.entities.entities[200]; present {
		t.Fatal("the removed target came back after the pair")
	}
	if entity, status := held.entities.resolve(200); status != resolutionUnknown && entity.entityID == 200 {
		t.Fatalf("the removed target still resolves: %+v", entity)
	}
	if pairing := held.rpcPairingSnapshot(); pairing.RewoundEntities != 1 || pairing.PairedOutOfOrder != 1 || len(held.pairing.removals) != 0 {
		t.Fatalf("pairing = %+v, removals kept = %d", pairing, len(held.pairing.removals))
	}
}

func TestHeldPairKeepsARemovalThatPrecededItsResponse(t *testing.T) {
	// In order the target is already gone when the response arrives, so the damage stays unattributed; a held response
	// that arrived after the removal must not bring the target back.
	inOrder := newPairingTestProcessor()
	sendDamageRequest(inOrder, 1, 42)
	removeEntities(t, inOrder, 1.005, 200)
	sendDamageResponse(inOrder, 1.01, 42, 500)

	held := newPairingTestProcessor()
	sendDamageResponse(held, 1.0, 41, 100) // held, so the removal below is remembered
	removeEntities(t, held, 1.005, 200)
	sendDamageResponse(held, 1.01, 42, 500)
	sendDamageRequest(held, 1.02, 42)

	want, got := inOrder.damageStatistics.Snapshot(), held.damageStatistics.Snapshot()
	if want.DamageDealt != 0 || want.UnattributedOutgoingDamage != 500 {
		t.Fatalf("in-order reference = %+v", want)
	}
	if got.DamageDealt != 0 || got.UnattributedOutgoingDamage != 500 {
		t.Fatalf("a held response that followed the removal was attributed: %+v", got)
	}
	if pairing := held.rpcPairingSnapshot(); pairing.RewoundEntities != 0 || pairing.HeldResponses != 1 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

func TestRewindRestoresAReusedEntityID(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageResponse(p, 1.01, 42, 500)
	removeEntities(t, p, 1.01, 200)
	// The ID is reused by a new entity before the delayed request arrives.
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
		entity.monsterID, entity.hasMonsterID = 9002, true
	})
	sendDamageRequest(p, 1.02, 42)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 500 {
		t.Fatalf("held pair = %+v", got)
	}
	if entity := p.entities.entities[200]; entity.monsterID != 9002 {
		t.Fatalf("the reused entity was not restored after the pair: %+v", entity)
	}
}

func TestRemovalsAreNotKeptWithoutHeldResponses(t *testing.T) {
	p := newPairingTestProcessor()
	removeEntities(t, p, 1, 200)
	if len(p.pairing.removals) != 0 {
		t.Fatalf("removals kept without a held response: %d", len(p.pairing.removals))
	}
	// A held response that expires releases the removals it kept.
	sendDamageResponse(p, 2, 42, 500)
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypeMonster, true
	})
	removeEntities(t, p, 2.1, 300)
	if len(p.pairing.removals) != 1 {
		t.Fatalf("removal during a hold was not kept: %d", len(p.pairing.removals))
	}
	pairingFrame(p, 2+damageResponseHoldWindow.Seconds()+1)
	p.expireHeldResponses()
	if len(p.pairing.removals) != 0 || p.rpcPairingSnapshot().UnpairedExpired != 1 {
		t.Fatalf("expired hold kept its removals: %d, %+v", len(p.pairing.removals), p.rpcPairingSnapshot())
	}
}

func TestRewindKeepsSourceRoleEvidenceFromTheDelayedRequest(t *testing.T) {
	before := entityAttributionState{sourceRole: sourceRoleEvidence{roleID: 1409, skillIDs: map[uint64]struct{}{1: {}}}, hasSourceRole: true}
	later := entityAttributionState{sourceRole: sourceRoleEvidence{roleID: 1409, skillIDs: map[uint64]struct{}{2: {}}}, hasSourceRole: true}
	merged := before.withLaterSourceRoles(later)
	if len(merged.sourceRole.skillIDs) != 2 || merged.sourceRole.conflict {
		t.Fatalf("merged evidence = %+v", merged.sourceRole)
	}
	if len(before.sourceRole.skillIDs) != 1 || len(later.sourceRole.skillIDs) != 1 {
		t.Fatal("merging changed its inputs")
	}
	other := entityAttributionState{sourceRole: sourceRoleEvidence{roleID: 1205, skillIDs: map[uint64]struct{}{3: {}}}, hasSourceRole: true}
	if !before.withLaterSourceRoles(other).sourceRole.conflict {
		t.Fatal("evidence for two roles was not marked as a conflict")
	}
}
