package capture

import (
	"encoding/json"
	"regexp"
	"testing"
)

// The server answers a DamageExecuteRequest inside a CombatReceivePackNotify that comes either embedded in a
// CombatSendPackResponse or standalone. These tests hold the product to one rule: the container never changes what a
// response means. A response is paired with its request by RequestId alone, counted once, and classified by the same
// direction and ownership rules wherever it came from.

type responseContainer int

const (
	inSendPackResponse responseContainer = iota
	inStandaloneNotify
)

// damageResponseData is one CombatReceiveData item: CombatResponseData{RequestId, DamageExecuteResponse{Damage}}.
func damageResponseData(rpcID uint64, amount int32) []byte {
	lis := appendVarintField(nil, FieldDamageExecuteResponseErrorCode, 0)
	lis = appendVarintField(lis, FieldDamageExecuteResponseImmuneType, 0)
	lis = appendVarintField(lis, FieldDamageExecuteResponseDamage, uint64(uint32(amount)))
	response := appendVarintField(nil, FieldCombatResponseDataRequestID, rpcID)
	response = appendBytesField(response, FieldCombatResponseDataDamageExecuteResponse, lis)
	return appendBytesField(nil, FieldCombatReceiveDataCombatResponseData, response)
}

// receivePackBody is a CombatReceivePackNotify body with the given CombatReceiveData items.
func receivePackBody(items ...[]byte) []byte {
	var body []byte
	for _, item := range items {
		body = appendBytesField(body, FieldCombatReceivePackNotifyData, item)
	}
	return body
}

// deliverReceivePack hands one frame of the chosen container to the dispatcher handleFrame uses for both.
func deliverReceivePack(t *testing.T, p *Processor, seconds float64, container responseContainer, items ...[]byte) {
	t.Helper()
	pairingFrame(p, seconds)
	notify := receivePackBody(items...)
	frame := GameFrame{Direction: "in", CommandID: CmdCombatReceivePackNotify, Timestamp: pairingAt(seconds)}
	body := notify
	if container == inSendPackResponse {
		frame.CommandID = CmdCombatSendPackResponse
		body = appendBytesField(nil, FieldCombatSendPackResponseReceivePackNotify, notify)
	}
	if err := p.HandleCombatReceivePacks(frame, body); err != nil {
		t.Fatal(err)
	}
}

func sendDamageRequestFrom(p *Processor, seconds float64, rpcID, attacker, target uint64) {
	pairingFrame(p, seconds)
	p.HandleDamageRequests(GameFrame{Direction: "out", CommandID: CmdCombatSendPackRequest, Timestamp: pairingAt(seconds)}, damageRequestBody(rpcID, attacker, target))
}

// newContainerTestProcessor has two local role entities (100 and 101), two monsters (200 and 201) and nothing for 999.
func newContainerTestProcessor() *Processor {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508)
	p.ObserveLocalRoleEntity(101)
	addTestRuntimeEntity(p, 201, func(entity *RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9002, true
	})
	return p
}

var processLocalIDs = regexp.MustCompile(`"captureSessionId":\d+|"\d+-(\d+-\d+)"`)

// containerStatistics is the statistics snapshot without the process-local capture session number.
func containerStatistics(t *testing.T, p *Processor) string {
	t.Helper()
	encoded, err := json.Marshal(p.DamageStatistics.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return processLocalIDs.ReplaceAllString(string(encoded), `"capture-$1"`)
}

type containerStep struct {
	seconds          float64
	request          bool
	rpcID            uint64
	attacker, target uint64
	amount           int32
}

// containerScenario covers both directions, a monster-on-monster hit, several actors and targets, an unknown actor, a
// response that arrives before its request, and several responses in one pack.
var containerScenario = []containerStep{
	{seconds: 1, request: true, rpcID: 1, attacker: 100, target: 200},
	{seconds: 1.1, rpcID: 1, amount: 1000},                              // local role on a monster
	{seconds: 1.2, request: true, rpcID: 2, attacker: 200, target: 100}, // a monster on the local player
	{seconds: 1.3, rpcID: 2, amount: 40},
	{seconds: 1.4, request: true, rpcID: 3, attacker: 200, target: 201}, // monster on monster: not the local player's
	{seconds: 1.5, rpcID: 3, amount: 77},
	{seconds: 1.6, request: true, rpcID: 4, attacker: 101, target: 201}, // a second actor and a second target
	{seconds: 1.7, rpcID: 4, amount: 2500},
	{seconds: 1.8, request: true, rpcID: 5, attacker: 999, target: 200}, // an attacker no message named
	{seconds: 1.9, rpcID: 5, amount: 300},
	{seconds: 2.0, rpcID: 6, amount: 650}, // before its request
	{seconds: 2.4, request: true, rpcID: 6, attacker: 100, target: 201},
}

func runContainerScenario(t *testing.T, containerOf func(rpcID uint64) responseContainer) *Processor {
	t.Helper()
	p := newContainerTestProcessor()
	for _, step := range containerScenario {
		if step.request {
			sendDamageRequestFrom(p, step.seconds, step.rpcID, step.attacker, step.target)
			continue
		}
		deliverReceivePack(t, p, step.seconds, containerOf(step.rpcID), damageResponseData(step.rpcID, step.amount))
	}
	return p
}

// The same requests and responses give the same statistics in either container, or in a mix of both.
func TestDamageResponseContainerDoesNotChangeStatistics(t *testing.T) {
	embedded := runContainerScenario(t, func(uint64) responseContainer { return inSendPackResponse })
	standalone := runContainerScenario(t, func(uint64) responseContainer { return inStandaloneNotify })
	mixed := runContainerScenario(t, func(rpcID uint64) responseContainer { return responseContainer(rpcID % 2) })
	want := containerStatistics(t, embedded)
	if got := containerStatistics(t, standalone); got != want {
		t.Fatalf("standalone responses changed the statistics:\n got %s\nwant %s", got, want)
	}
	if got := containerStatistics(t, mixed); got != want {
		t.Fatalf("mixed containers changed the statistics:\n got %s\nwant %s", got, want)
	}
	got := standalone.DamageStatistics.Snapshot()
	// Dealt: 1000 + 2500 + 650 from the local roles; taken: 40. The unknown attacker's 300 stays unattributed, apart
	// from confirmed local damage, and the monster-on-monster 77 is in no total.
	if got.DamageDealt != 4150 || got.OutgoingEvents != 3 || got.DamageTaken != 40 || got.IncomingEvents != 1 ||
		got.UnattributedOutgoingDamage != 300 || got.UnattributedOutgoingEvents != 1 {
		t.Fatalf("standalone totals = dealt %d/%d taken %d/%d unattributed %d/%d", got.DamageDealt, got.OutgoingEvents, got.DamageTaken, got.IncomingEvents, got.UnattributedOutgoingDamage, got.UnattributedOutgoingEvents)
	}
	pairing := standalone.RPCPairingSnapshot()
	if pairing.Requests != 6 || pairing.Responses != 6 || pairing.StandaloneResponses != 6 || pairing.Paired != 6 || pairing.PairedOutOfOrder != 1 || pairing.PendingRequests != 0 {
		t.Fatalf("standalone pairing = %+v", pairing)
	}
	if mixedPairing := mixed.RPCPairingSnapshot(); mixedPairing.StandaloneResponses != 3 || mixedPairing.Responses != 6 {
		t.Fatalf("mixed pairing = %+v", mixedPairing)
	}
}

// A standalone response is the only answer to its request: without it the request stays pending and nothing is counted.
func TestStandaloneNotifyResponseIsCounted(t *testing.T) {
	p := newContainerTestProcessor()
	sendDamageRequestFrom(p, 1, 7, 100, 200)
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 0 || p.RPCPairingSnapshot().PendingRequests != 1 {
		t.Fatalf("a request alone was counted: %+v", got)
	}
	deliverReceivePack(t, p, 1.4, inStandaloneNotify, damageResponseData(7, 71224))
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 71224 || got.OutgoingEvents != 1 {
		t.Fatalf("standalone response = %+v", got)
	}
	if pairing := p.RPCPairingSnapshot(); pairing.PendingRequests != 0 || pairing.Paired != 1 || pairing.StandaloneResponses != 1 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// One RequestId is one result. A copy in the other container, a retransmitted frame, or a second response with other
// content never adds a second result; copies are counted as duplicates, other content as a conflict.
func TestDamageResponseCopiesAcrossContainersCountOnce(t *testing.T) {
	for _, order := range [][2]responseContainer{{inSendPackResponse, inStandaloneNotify}, {inStandaloneNotify, inSendPackResponse}, {inStandaloneNotify, inStandaloneNotify}} {
		p := newContainerTestProcessor()
		sendDamageRequestFrom(p, 1, 8, 100, 200)
		deliverReceivePack(t, p, 1.1, order[0], damageResponseData(8, 5000))
		deliverReceivePack(t, p, 1.2, order[1], damageResponseData(8, 5000))
		deliverReceivePack(t, p, 1.3, order[1], damageResponseData(8, 6000))
		if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 5000 || got.OutgoingEvents != 1 || got.DuplicateEvents != 0 {
			t.Fatalf("%v: copies changed the totals: %+v", order, got)
		}
		if pairing := p.RPCPairingSnapshot(); pairing.DuplicateResponses != 1 || pairing.ConflictingResponses != 1 || pairing.Paired != 1 {
			t.Fatalf("%v: pairing = %+v", order, pairing)
		}
	}
}

// Copies of a held response, the same bytes or not, wait as one: the first copy is paired once its request arrives.
func TestHeldStandaloneResponseCopies(t *testing.T) {
	p := newContainerTestProcessor()
	deliverReceivePack(t, p, 1, inStandaloneNotify, damageResponseData(9, 300))
	deliverReceivePack(t, p, 1.1, inSendPackResponse, damageResponseData(9, 300))
	deliverReceivePack(t, p, 1.2, inStandaloneNotify, damageResponseData(9, 301))
	sendDamageRequestFrom(p, 1.5, 9, 100, 200)
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 300 || got.OutgoingEvents != 1 {
		t.Fatalf("held copies = %+v", got)
	}
	if pairing := p.RPCPairingSnapshot(); pairing.DuplicateResponses != 1 || pairing.ConflictingResponses != 1 || pairing.PairedOutOfOrder != 1 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// A standalone response whose request never arrives (lost in a capture gap, or sent before the capture began) stays
// unpaired: it is neither counted nor turned into a zero-damage event.
func TestUnpairedStandaloneResponseIsNotCounted(t *testing.T) {
	p := newContainerTestProcessor()
	deliverReceivePack(t, p, 1, inStandaloneNotify, damageResponseData(10, 900))
	deliverReceivePack(t, p, 1+damageResponseHoldWindow.Seconds()+0.5, inStandaloneNotify)
	pairing := p.RPCPairingSnapshot()
	if pairing.UnpairedExpired != 1 || pairing.HeldResponses != 0 || pairing.Paired != 0 {
		t.Fatalf("pairing = %+v", pairing)
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 0 || got.OutgoingEvents != 0 {
		t.Fatalf("an unpaired response was counted: %+v", got)
	}
}

// RequestIds restart with a protocol session: a standalone response held from the previous session is discarded, never
// paired with the new session's request of the same RequestId.
func TestStandaloneResponseNeverPairsAcrossSessions(t *testing.T) {
	p := newContainerTestProcessor()
	deliverReceivePack(t, p, 1, inStandaloneNotify, damageResponseData(11, 900))
	p.discardHeldResponses(&p.pairing.sessionChange)
	p.sessionGeneration++
	sendDamageRequestFrom(p, 1.5, 11, 100, 200)
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("a response of the previous session was paired: %+v", got)
	}
	deliverReceivePack(t, p, 1.6, inStandaloneNotify, damageResponseData(11, 120))
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 120 || got.OutgoingEvents != 1 {
		t.Fatalf("the new session's own response = %+v", got)
	}
	if pairing := p.RPCPairingSnapshot(); pairing.UnpairedSessionChange != 1 || pairing.Paired != 1 {
		t.Fatalf("pairing = %+v", pairing)
	}
}

// A standalone notify may carry responses and notifications together; both are read, responses first.
func TestStandaloneNotifyCarriesResponsesAndNotifications(t *testing.T) {
	p := newContainerTestProcessor()
	p.SessionNumber = 1
	p.observeSceneJoin(singlePlayerJoinScene(77), pairingAt(0.5))
	sendDamageRequestFrom(p, 1, 12, 100, 200)
	sendDamageRequestFrom(p, 1, 13, 101, 201)
	// battleStateReceivePack is a whole notify body with one Data item; the two response items follow it in the same body.
	pairingFrame(p, 1.2)
	body := append(battleStateReceivePack(true, 77), receivePackBody(damageResponseData(12, 800), damageResponseData(13, 450))...)
	if err := p.HandleCombatReceivePacks(GameFrame{Direction: "in", CommandID: CmdCombatReceivePackNotify, Timestamp: pairingAt(1.2)}, body); err != nil {
		t.Fatal(err)
	}
	got := p.DamageStatistics.Snapshot()
	if got.DamageDealt != 1250 || got.OutgoingEvents != 2 {
		t.Fatalf("responses of a mixed pack = %+v", got)
	}
	if got.Encounters.BattleState != "in_battle" {
		t.Fatalf("the notification of a mixed pack was not read: %+v", got.Encounters)
	}
}

// A malformed response item is counted as a parse error and skipped; the other items of the pack still count.
func TestMalformedStandaloneResponseItemIsSkipped(t *testing.T) {
	p := newContainerTestProcessor()
	sendDamageRequestFrom(p, 1, 14, 100, 200)
	broken := appendBytesField(nil, FieldCombatReceiveDataCombatResponseData, []byte{0x0a, 0x05, 0x01})
	before := p.Stats.ParseErrors
	deliverReceivePack(t, p, 1.2, inStandaloneNotify, broken, damageResponseData(14, 64))
	if p.Stats.ParseErrors != before+1 {
		t.Fatalf("parse errors = %d, want %d", p.Stats.ParseErrors, before+1)
	}
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 64 {
		t.Fatalf("the valid item was lost: %+v", got)
	}
}
