package capture

import (
	"io"
	"testing"
)

// These tests drive the outbound CombatSendPackRequest effect decoder with synthetic protobuf
// bytes. They are SYNTHETIC evidence: the layout follows the Aki.Protocol field table that the
// decoder implements, and the scenarios mirror shapes seen in a local live capture, but no byte
// here was captured from the game.

type testCombatCommon struct {
	entityID, messageID, preMessageID uint64
}

func clientCommonBody(common testCombatCommon) []byte {
	body := []byte(nil)
	if common.preMessageID != 0 {
		body = appendVarintField(body, FieldCombatCommonPreMessageID, common.preMessageID)
	}
	if common.messageID != 0 {
		body = appendVarintField(body, FieldCombatCommonMessageID, common.messageID)
	}
	if common.entityID != 0 {
		body = appendVarintField(body, FieldCombatCommonEntityID, common.entityID)
	}
	return body
}

// clientPushBody wraps one CombatPushData entry in CombatSendPackRequest.Data[].Push.
func clientPushBody(common testCombatCommon, pushField uint64, payload []byte) []byte {
	push := appendBytesField(nil, FieldCombatPushDataCombatCommon, clientCommonBody(common))
	push = appendBytesField(push, pushField, payload)
	return appendBytesField(nil, FieldCombatSendPackRequestData, appendBytesField(nil, FieldCombatSendDataPush, push))
}

func clientApplyPayload(handle, effectID, instigator, stack uint64, duration float32) []byte {
	payload := appendBytesField(nil, FieldApplyGameplayEffectPushReason, []byte("test reason"))
	payload = appendVarintField(payload, FieldApplyGameplayEffectPushHandle, handle)
	payload = appendVarintField(payload, FieldApplyGameplayEffectPushStackCount, stack)
	payload = appendVarintField(payload, FieldApplyGameplayEffectPushID, effectID)
	payload = effectFloatField(payload, FieldApplyGameplayEffectPushDuration, duration)
	return appendVarintField(payload, FieldApplyGameplayEffectPushInstigatorID, instigator)
}

func clientStackPayload(handle, instigator, stack uint64, duration float32, notRefreshDuration bool) []byte {
	payload := appendVarintField(nil, FieldBuffStackCountPushHandleID, handle)
	if notRefreshDuration {
		payload = appendVarintField(payload, FieldBuffStackCountPushNotRefreshDuration, 1)
	}
	payload = appendVarintField(payload, FieldBuffStackCountPushInstigatorID, instigator)
	payload = effectFloatField(payload, FieldBuffStackCountPushDuration, duration)
	return appendVarintField(payload, FieldBuffStackCountPushNewStackCount, stack)
}

func clientRemovePayload(handle, entity uint64, premature bool) []byte {
	payload := appendVarintField(nil, FieldRemoveGameplayEffectPushHandle, handle)
	payload = appendVarintField(payload, FieldRemoveGameplayEffectPushEntityID, entity)
	if premature {
		payload = appendVarintField(payload, FieldRemoveGameplayEffectPushIsPrematureRemoval, 1)
	}
	return payload
}

func deliverClientPush(t *testing.T, p *Processor, seconds float64, body []byte) {
	t.Helper()
	frame := GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out", Timestamp: effectAt(seconds)}
	if err := p.HandleClientEffectPushes(frame, body); err != nil {
		t.Fatalf("handle client effect push: %v", err)
	}
}

// newClientEffectTestProcessor has local team roles 100, 101 and 102 of player 7 (FightRoleInfos),
// with 100 reported as the current entity, monsters 200 and 201, and no diagnostic outputs.
func newClientEffectTestProcessor() *Processor {
	p := NewProcessor(nil, io.Discard)
	p.SessionNumber = 1
	addTestPlayerRoleGroupEntity(p, 100, 7, 1312)
	addTestPlayerRoleGroupEntity(p, 101, 7, 1311)
	addTestPlayerRoleGroupEntity(p, 102, 7, 1505)
	monster := func(id uint64) func(*RuntimeEntity) {
		return func(entity *RuntimeEntity) {
			entity.MonsterID, entity.HasMonsterID = id, true
			entity.EntityTypeCode, entity.HasEntityType = EntityTypeMonster, true
		}
	}
	addTestRuntimeEntity(p, 200, monster(9001))
	addTestRuntimeEntity(p, 201, monster(9002))
	p.ObserveLocalRoleEntity(100)
	return p
}

func clientQuery(p *Processor, seconds float64) CaptureQueryTime {
	return CaptureQueryTime{CaptureSessionID: p.Effects.CaptureSessionID, Timestamp: effectAt(seconds)}
}

func lastEnded(t *testing.T, snapshot EffectSnapshot) EffectEndedSnapshot {
	t.Helper()
	if len(snapshot.RecentEnded) == 0 {
		t.Fatal("no ended effect")
	}
	return snapshot.RecentEnded[len(snapshot.RecentEnded)-1]
}

func TestClientApplyReportTracksTeamEffectWithClientEvidence(t *testing.T) {
	p := newClientEffectTestProcessor()
	// Role 102 shares a 12-second effect to role 100; the report is addressed to the holder.
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436765, 1312010093, 102, 1, 12)))
	effect := onlyEffect(t, p.Effects.SnapshotAt(clientQuery(p, 5)))
	if effect.Evidence != EffectEvidenceClientReport || effect.Origin != EffectOriginApply || effect.Lifecycle != EffectLifecycleActive {
		t.Fatalf("effect = %+v", effect)
	}
	if effect.HolderEntityID != 100 || effect.HolderRelation != EffectRelationLocalRole {
		t.Fatalf("holder = %d %s", effect.HolderEntityID, effect.HolderRelation)
	}
	if effect.SourceEntityID == nil || *effect.SourceEntityID != 102 || effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("source = %v %s", effect.SourceEntityID, effect.SourceRelation)
	}
	if effect.EffectID == nil || *effect.EffectID != 1312010093 || effect.RemainingStatus != EffectRemainingEstimated {
		t.Fatalf("effect id/remaining = %+v", effect)
	}
	requireNear(t, "remaining", effect.RemainingSeconds, 7)
	if counters := p.Effects.Snapshot().Counters; counters.Applies != 1 || counters.SkippedUncorrelatedHolder != 0 {
		t.Fatalf("counters = %+v", counters)
	}
}

func TestClientStackReportRestartsDurationUnlessNotRefreshDuration(t *testing.T) {
	p := newClientEffectTestProcessor()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436729, 1505701002, 102, 1, 1.1)))
	deliverClientPush(t, p, 0.9, clientPushBody(common, FieldCombatPushDataBuffStackCountPush, clientStackPayload(268436729, 102, 1, 1.1, false)))
	effect := onlyEffect(t, p.Effects.SnapshotAt(clientQuery(p, 1.5)))
	requireNear(t, "remaining after refresh", effect.RemainingSeconds, 0.5)
	deliverClientPush(t, p, 1.8, clientPushBody(common, FieldCombatPushDataBuffStackCountPush, clientStackPayload(268436729, 102, 2, 1.1, true)))
	effect = onlyEffect(t, p.Effects.SnapshotAt(clientQuery(p, 2.1)))
	if effect.StackCount == nil || *effect.StackCount != 2 {
		t.Fatalf("stack = %v, want 2", effect.StackCount)
	}
	// NotRefreshDuration keeps the estimate from the refresh at 0.9 s, which ran out at 2.0 s.
	if effect.RemainingStatus != EffectRemainingOverdue {
		t.Fatalf("remaining status = %s, want overdue", effect.RemainingStatus)
	}
}

func TestClientRemoveReportSeparatesExpiryFromEarlyRemoval(t *testing.T) {
	p := newClientEffectTestProcessor()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436766, 1312010033, 100, 1, 12)))
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436707, 1312010092, 100, 1, -1)))
	if effect := p.Effects.SnapshotAt(clientQuery(p, 1)).Effects[0]; effect.RemainingStatus != EffectRemainingUnknown || effect.RemainingReason != effectReasonNoServerDuration {
		t.Fatalf("a duration of -1 must not count down: %+v", effect)
	}
	deliverClientPush(t, p, 3, clientPushBody(common, FieldCombatPushDataRemoveGameplayEffectPush, clientRemovePayload(268436707, 100, true)))
	if ended := lastEnded(t, p.Effects.Snapshot()); ended.Lifecycle != EffectLifecycleReportedRemoved || ended.HandleID != 268436707 {
		t.Fatalf("early removal = %+v", ended)
	}
	deliverClientPush(t, p, 12.01, clientPushBody(common, FieldCombatPushDataRemoveGameplayEffectPush, clientRemovePayload(268436766, 100, false)))
	ended := lastEnded(t, p.Effects.Snapshot())
	if ended.Lifecycle != EffectLifecycleReportedExpired || ended.Evidence != EffectEvidenceClientReport {
		t.Fatalf("expiry = %+v", ended)
	}
	requireNear(t, "expiry delta", ended.ExpiryDeltaSeconds, 0.01)
	if len(p.Effects.Snapshot().Effects) != 0 {
		t.Fatal("removed effects are still active")
	}
}

func TestClientRemoveReportEndsServerSnapshotInstance(t *testing.T) {
	p := newClientEffectTestProcessor()
	deliverEffectSnapshot(t, p, 1, 100, 0, effectSnapshotResponse(166, 800100008341, 100, 1, -1, -1))
	deliverClientPush(t, p, 2, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataRemoveGameplayEffectPush, clientRemovePayload(166, 100, true)))
	ended := lastEnded(t, p.Effects.Snapshot())
	if ended.Lifecycle != EffectLifecycleReportedRemoved || ended.Evidence != EffectEvidenceServerSnapshot || ended.Origin != EffectOriginSnapshot {
		t.Fatalf("ended = %+v", ended)
	}
}

func TestClientReportsKeepSameEffectOnEachHolderSeparate(t *testing.T) {
	p := newClientEffectTestProcessor()
	for i, holder := range []uint64{100, 101, 102} {
		deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: holder}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436729+uint64(i), 1505701002, 102, 1, 1.1)))
	}
	if effects := p.Effects.SnapshotAt(clientQuery(p, 0.5)).Effects; len(effects) != 3 {
		t.Fatalf("effects = %d, want one per holder", len(effects))
	}
}

func TestClientReportsWithoutEstablishedHolderAreSkipped(t *testing.T) {
	p := newClientEffectTestProcessor()
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436700, 42, 100, 1, 5)))
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436701, 42, 100, 1, 5)))
	// A remove push whose own EntityId disagrees with CombatCommon names no holder.
	deliverClientPush(t, p, 1, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataRemoveGameplayEffectPush, clientRemovePayload(268436701, 101, true)))
	snapshot := p.Effects.SnapshotAt(clientQuery(p, 1))
	if len(snapshot.Effects) != 1 || snapshot.Counters.SkippedUncorrelatedHolder != 2 {
		t.Fatalf("effects = %d counters = %+v", len(snapshot.Effects), snapshot.Counters)
	}
}

func TestClientReportsHandleDuplicatesAndOutOfOrderMessages(t *testing.T) {
	p := newClientEffectTestProcessor()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436718, 800100008344, 100, 1, 10)))
	deliverClientPush(t, p, 1, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436718, 800100008344, 100, 1, 10)))
	effect := onlyEffect(t, p.Effects.SnapshotAt(clientQuery(p, 2)))
	requireNear(t, "remaining after duplicate apply", effect.RemainingSeconds, 9)
	deliverClientPush(t, p, 5, clientPushBody(common, FieldCombatPushDataRemoveGameplayEffectPush, clientRemovePayload(268436718, 100, false)))
	// A late copy of an older apply must not resurrect the removed instance.
	deliverClientPush(t, p, 4, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436718, 800100008344, 100, 1, 10)))
	// A stack push for a handle that was never applied creates nothing.
	deliverClientPush(t, p, 6, clientPushBody(common, FieldCombatPushDataBuffStackCountPush, clientStackPayload(268436999, 100, 1, 10, false)))
	snapshot := p.Effects.SnapshotAt(clientQuery(p, 6))
	if len(snapshot.Effects) != 0 {
		t.Fatalf("effects = %+v", snapshot.Effects)
	}
	c := snapshot.Counters
	if c.Applies != 1 || c.DuplicateApplies != 1 || c.StaleApplies != 1 || c.UpdatesWithoutApply != 1 || c.Removes != 1 {
		t.Fatalf("counters = %+v", c)
	}
}

func TestClientReportsAreIsolatedBySession(t *testing.T) {
	p := newClientEffectTestProcessor()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436718, 42, 100, 1, 10)))
	p.SessionNumber = 2
	deliverClientPush(t, p, 1, clientPushBody(common, FieldCombatPushDataBuffStackCountPush, clientStackPayload(268436718, 100, 2, 10, false)))
	snapshot := p.Effects.SnapshotAt(clientQuery(p, 1))
	if len(snapshot.Effects) != 0 || snapshot.Counters.BoundaryDrops != 1 || snapshot.Counters.UpdatesWithoutApply != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestClientReportsKeepTeamRelationAcrossRoleSwitch(t *testing.T) {
	p := newClientEffectTestProcessor()
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436701, 2002202311, 102, 5, 30)))
	p.ObserveLocalRoleEntity(101)
	effect := onlyEffect(t, p.Effects.SnapshotAt(clientQuery(p, 1)))
	if effect.HolderRelation != EffectRelationLocalRole || effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("relations after role switch = %s/%s", effect.HolderRelation, effect.SourceRelation)
	}
	if effect.StackCount == nil || *effect.StackCount != 5 {
		t.Fatalf("stack = %v", effect.StackCount)
	}
}

func TestClientReportsFollowPlayerRelationFiltering(t *testing.T) {
	p := newClientEffectTestProcessor()
	// A local role debuffing a monster is kept; a monster effect on a monster is not player-related.
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 200}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436800, 1101005002, 100, 1, 1)))
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 201}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436801, 70000003, 201, 1, -1)))
	snapshot := p.Effects.SnapshotAt(clientQuery(p, 0.5))
	effect := onlyEffect(t, snapshot)
	if effect.HolderEntityID != 200 || effect.HolderRelation != EffectRelationNonPlayer || effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("effect = %+v", effect)
	}
	if snapshot.Counters.ExcludedUnrelated != 1 {
		t.Fatalf("counters = %+v", snapshot.Counters)
	}
}

func TestClientEffectReportsLeaveDamageStatisticsUntouched(t *testing.T) {
	p := newClientEffectTestProcessor()
	before := p.DamageStatistics.Snapshot()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436766, 1312010033, 100, 1, 12)))
	deliverClientPush(t, p, 1, clientPushBody(common, FieldCombatPushDataBuffStackCountPush, clientStackPayload(268436766, 100, 1, 12, false)))
	deliverClientPush(t, p, 12, clientPushBody(common, FieldCombatPushDataRemoveGameplayEffectPush, clientRemovePayload(268436766, 100, false)))
	after := p.DamageStatistics.Snapshot()
	if before.DamageDealt != after.DamageDealt || before.DamageTaken != after.DamageTaken || before.OutgoingEvents != after.OutgoingEvents ||
		before.IncomingEvents != after.IncomingEvents || before.StatisticsIntervalID != after.StatisticsIntervalID {
		t.Fatalf("damage statistics changed: %+v -> %+v", before, after)
	}
}

func TestClientEffectPushRejectsMalformedFields(t *testing.T) {
	p := newClientEffectTestProcessor()
	frame := GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out", Timestamp: effectAt(0)}
	// Handle must be a varint; a length-delimited value is a decode error, not a zero handle.
	payload := appendBytesField(nil, FieldApplyGameplayEffectPushHandle, []byte{1})
	if err := p.HandleClientEffectPushes(frame, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataApplyGameplayEffectPush, payload)); err == nil {
		t.Fatal("malformed ApplyGameplayEffectPush was accepted")
	}
	if len(p.Effects.Snapshot().Effects) != 0 {
		t.Fatal("malformed push created state")
	}
}

func TestServerBuffRequestLinksToClientResponseByPreMessageID(t *testing.T) {
	p := newClientEffectTestProcessor()
	request := appendVarintField(nil, FieldApplyBuffS2cRequestNotifyID, 800100008343)
	request = appendVarintField(request, FieldApplyBuffS2cRequestNotifyInstigatorID, 100)
	notify := appendBytesField(nil, FieldCombatNotifyDataCombatCommon, clientCommonBody(testCombatCommon{entityID: 100, messageID: 1}))
	notify = appendBytesField(notify, FieldCombatNotifyDataApplyBuffS2CRequestNotify, request)
	deliverEffectMessage(t, p, 0, appendBytesField(nil, FieldCombatReceivePackNotifyData, appendBytesField(nil, FieldCombatReceiveDataCombatNotifyData, notify)))
	if len(p.Effects.Snapshot().Effects) != 0 {
		t.Fatal("a server request created effect state")
	}

	response := appendVarintField(nil, fieldApplyBuffS2cResponsePushIsActive, 1)
	response = appendVarintField(response, fieldApplyBuffS2cResponsePushHandle, 268436712)
	row, err := decodeClientEffectPush(GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out"}, fieldCombatPushDataApplyBuffS2CResponsePush, response, combatCommonContext{entityID: u64(100), preMessageID: u64(1)})
	if err != nil {
		t.Fatal(err)
	}
	row.SessionNumber = 1
	p.linkServerBuffRequest(&row)
	if row.RequestLinkStatus != ResolutionCorrelated || row.RequestLinkedID == nil || *row.RequestLinkedID != 800100008343 || row.HandleID == nil || *row.HandleID != 268436712 {
		t.Fatalf("linked response = %+v", row)
	}

	other := row
	other.RequestLinkedID, other.RequestLinkStatus = nil, ""
	other.CombatCommonEntityID = u64(101)
	p.linkServerBuffRequest(&other)
	if other.RequestLinkStatus != ResolutionUnknown || other.RequestLinkedID != nil {
		t.Fatalf("response on another entity was linked: %+v", other)
	}
	wrongKind := row
	wrongKind.EventKind, wrongKind.RequestLinkedID = "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE", nil
	p.linkServerBuffRequest(&wrongKind)
	if wrongKind.RequestLinkStatus != ResolutionUnknown || wrongKind.RequestLinkedID != nil {
		t.Fatalf("response of another kind was linked: %+v", wrongKind)
	}
}
