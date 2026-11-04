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
	u3n := appendVarintField(nil, 9, 70010001) // DamageId
	u3n = appendVarintField(u3n, 10, targetID) // TargetEntityId
	u3n = appendVarintField(u3n, 13, attackerID)
	request := appendVarintField(nil, 2, rpcID)
	request = appendBytesField(request, 5, u3n)
	return appendBytesField(nil, 14, appendBytesField(nil, 3, request))
}

func damageResponseBody(rpcID uint64, amount int32) []byte {
	lis := appendVarintField(nil, 4, 0)
	lis = appendVarintField(lis, 5, 0)
	lis = appendVarintField(lis, 10, uint64(uint32(amount)))
	response := appendVarintField(nil, 2, rpcID)
	response = appendBytesField(response, 5, lis)
	item := appendBytesField(nil, 3, response)
	return appendBytesField(nil, 14, appendBytesField(nil, 5, item))
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
	p.handleDamageRequests(gameFrame{direction: "out", commandID: 19470, timestamp: pairingAt(seconds)}, damageRequestBody(rpcID, 100, 200))
}

func sendDamageResponse(p *processor, seconds float64, rpcID uint64, amount int32) {
	pairingFrame(p, seconds)
	p.handleDamageResponses(damageResponseBody(rpcID, amount))
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
		p.handleOneDamageResult(rpcID, appendVarintField(nil, 10, 1))
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
	immune := appendVarintField(nil, 5, 1)
	immune = appendVarintField(immune, 10, 100)
	p.handleOneDamageResult(51, immune)
	sendDamageRequest(p, 1.5, 51)
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("an immune response became damage: %+v", got)
	}
	if pairing := p.rpcPairingSnapshot(); pairing.InvalidResponses != 1 || pairing.Paired != 1 {
		t.Fatalf("pairing counters = %+v", pairing)
	}
}

func TestRPCPairingLineReportsCounters(t *testing.T) {
	line := rpcPairingLine(RPCPairingSnapshot{Requests: 3, Paired: 2, PairedOutOfOrder: 1, UnpairedExpired: 1})
	for _, want := range []string{"damage_rpc_pairing ", "requests=3", "paired=2", "paired_out_of_order=1", "unpaired_expired=1"} {
		if !bytes.Contains([]byte(line), []byte(want)) {
			t.Fatalf("pairing line %q is missing %q", line, want)
		}
	}
}
