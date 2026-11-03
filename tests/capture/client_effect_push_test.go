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
		body = appendVarintField(body, 1, common.preMessageID)
	}
	if common.messageID != 0 {
		body = appendVarintField(body, 2, common.messageID)
	}
	if common.entityID != 0 {
		body = appendVarintField(body, 5, common.entityID)
	}
	return body
}

// clientPushBody wraps one CombatPushData entry in CombatSendPackRequest.Data[].Push.
func clientPushBody(common testCombatCommon, pushField uint64, payload []byte) []byte {
	push := appendBytesField(nil, 1, clientCommonBody(common))
	push = appendBytesField(push, pushField, payload)
	return appendBytesField(nil, 14, appendBytesField(nil, 2, push))
}

func clientApplyPayload(handle, effectID, instigator, stack uint64, duration float32) []byte {
	payload := appendBytesField(nil, 2, []byte("test reason"))
	payload = appendVarintField(payload, 3, handle)
	payload = appendVarintField(payload, 4, stack)
	payload = appendVarintField(payload, 5, effectID)
	payload = effectFloatField(payload, 8, duration)
	return appendVarintField(payload, 10, instigator)
}

func clientStackPayload(handle, instigator, stack uint64, duration float32, notRefreshDuration bool) []byte {
	payload := appendVarintField(nil, 2, handle)
	if notRefreshDuration {
		payload = appendVarintField(payload, 4, 1)
	}
	payload = appendVarintField(payload, 12, instigator)
	payload = effectFloatField(payload, 13, duration)
	return appendVarintField(payload, 14, stack)
}

func clientRemovePayload(handle, entity uint64, premature bool) []byte {
	payload := appendVarintField(nil, 14, handle)
	payload = appendVarintField(payload, 15, entity)
	if premature {
		payload = appendVarintField(payload, 10, 1)
	}
	return payload
}

func deliverClientPush(t *testing.T, p *processor, seconds float64, body []byte) {
	t.Helper()
	frame := gameFrame{commandID: 19470, direction: "out", timestamp: effectAt(seconds)}
	if err := p.handleClientEffectPushes(frame, body); err != nil {
		t.Fatalf("handle client effect push: %v", err)
	}
}

// newClientEffectTestProcessor has local team roles 100, 101 and 102 of player 7 (FightRoleInfos),
// with 100 reported as the current entity, monsters 200 and 201, and no diagnostic outputs.
func newClientEffectTestProcessor() *processor {
	p := newProcessor(nil, io.Discard, io.Discard, nil)
	p.sessionNumber = 1
	addTestPlayerRoleGroupEntity(p, 100, 7, 1312)
	addTestPlayerRoleGroupEntity(p, 101, 7, 1311)
	addTestPlayerRoleGroupEntity(p, 102, 7, 1505)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 201, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9002, true })
	p.observeLocalRoleEntity(100)
	return p
}

func clientQuery(p *processor, seconds float64) CaptureQueryTime {
	return CaptureQueryTime{CaptureSessionID: p.effects.captureSessionID, Timestamp: effectAt(seconds)}
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
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, 9, clientApplyPayload(268436765, 1312010093, 102, 1, 12)))
	effect := onlyEffect(t, p.effects.SnapshotAt(clientQuery(p, 5)))
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
	if counters := p.effects.Snapshot().Counters; counters.Applies != 1 || counters.SkippedUncorrelatedHolder != 0 {
		t.Fatalf("counters = %+v", counters)
	}
}

func TestClientStackReportRestartsDurationUnlessNotRefreshDuration(t *testing.T) {
	p := newClientEffectTestProcessor()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436729, 1505701002, 102, 1, 1.1)))
	deliverClientPush(t, p, 0.9, clientPushBody(common, 37, clientStackPayload(268436729, 102, 1, 1.1, false)))
	effect := onlyEffect(t, p.effects.SnapshotAt(clientQuery(p, 1.5)))
	requireNear(t, "remaining after refresh", effect.RemainingSeconds, 0.5)
	deliverClientPush(t, p, 1.8, clientPushBody(common, 37, clientStackPayload(268436729, 102, 2, 1.1, true)))
	effect = onlyEffect(t, p.effects.SnapshotAt(clientQuery(p, 2.1)))
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
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436766, 1312010033, 100, 1, 12)))
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436707, 1312010092, 100, 1, -1)))
	if effect := p.effects.SnapshotAt(clientQuery(p, 1)).Effects[0]; effect.RemainingStatus != EffectRemainingUnknown || effect.RemainingReason != effectReasonNoServerDuration {
		t.Fatalf("a duration of -1 must not count down: %+v", effect)
	}
	deliverClientPush(t, p, 3, clientPushBody(common, 10, clientRemovePayload(268436707, 100, true)))
	if ended := lastEnded(t, p.effects.Snapshot()); ended.Lifecycle != EffectLifecycleReportedRemoved || ended.HandleID != 268436707 {
		t.Fatalf("early removal = %+v", ended)
	}
	deliverClientPush(t, p, 12.01, clientPushBody(common, 10, clientRemovePayload(268436766, 100, false)))
	ended := lastEnded(t, p.effects.Snapshot())
	if ended.Lifecycle != EffectLifecycleReportedExpired || ended.Evidence != EffectEvidenceClientReport {
		t.Fatalf("expiry = %+v", ended)
	}
	requireNear(t, "expiry delta", ended.ExpiryDeltaSeconds, 0.01)
	if len(p.effects.Snapshot().Effects) != 0 {
		t.Fatal("removed effects are still active")
	}
}

func TestClientRemoveReportEndsServerSnapshotInstance(t *testing.T) {
	p := newClientEffectTestProcessor()
	deliverEffectSnapshot(t, p, 1, 100, 0, effectSnapshotResponse(166, 800100008341, 100, 1, -1, -1))
	deliverClientPush(t, p, 2, clientPushBody(testCombatCommon{entityID: 100}, 10, clientRemovePayload(166, 100, true)))
	ended := lastEnded(t, p.effects.Snapshot())
	if ended.Lifecycle != EffectLifecycleReportedRemoved || ended.Evidence != EffectEvidenceServerSnapshot || ended.Origin != EffectOriginSnapshot {
		t.Fatalf("ended = %+v", ended)
	}
}

func TestClientReportsKeepSameEffectOnEachHolderSeparate(t *testing.T) {
	p := newClientEffectTestProcessor()
	for i, holder := range []uint64{100, 101, 102} {
		deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: holder}, 9, clientApplyPayload(268436729+uint64(i), 1505701002, 102, 1, 1.1)))
	}
	if effects := p.effects.SnapshotAt(clientQuery(p, 0.5)).Effects; len(effects) != 3 {
		t.Fatalf("effects = %d, want one per holder", len(effects))
	}
}

func TestClientReportsWithoutEstablishedHolderAreSkipped(t *testing.T) {
	p := newClientEffectTestProcessor()
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{}, 9, clientApplyPayload(268436700, 42, 100, 1, 5)))
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, 9, clientApplyPayload(268436701, 42, 100, 1, 5)))
	// A remove push whose own EntityId disagrees with CombatCommon names no holder.
	deliverClientPush(t, p, 1, clientPushBody(testCombatCommon{entityID: 100}, 10, clientRemovePayload(268436701, 101, true)))
	snapshot := p.effects.SnapshotAt(clientQuery(p, 1))
	if len(snapshot.Effects) != 1 || snapshot.Counters.SkippedUncorrelatedHolder != 2 {
		t.Fatalf("effects = %d counters = %+v", len(snapshot.Effects), snapshot.Counters)
	}
}

func TestClientReportsHandleDuplicatesAndOutOfOrderMessages(t *testing.T) {
	p := newClientEffectTestProcessor()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436718, 800100008344, 100, 1, 10)))
	deliverClientPush(t, p, 1, clientPushBody(common, 9, clientApplyPayload(268436718, 800100008344, 100, 1, 10)))
	effect := onlyEffect(t, p.effects.SnapshotAt(clientQuery(p, 2)))
	requireNear(t, "remaining after duplicate apply", effect.RemainingSeconds, 9)
	deliverClientPush(t, p, 5, clientPushBody(common, 10, clientRemovePayload(268436718, 100, false)))
	// A late copy of an older apply must not resurrect the removed instance.
	deliverClientPush(t, p, 4, clientPushBody(common, 9, clientApplyPayload(268436718, 800100008344, 100, 1, 10)))
	// A stack push for a handle that was never applied creates nothing.
	deliverClientPush(t, p, 6, clientPushBody(common, 37, clientStackPayload(268436999, 100, 1, 10, false)))
	snapshot := p.effects.SnapshotAt(clientQuery(p, 6))
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
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436718, 42, 100, 1, 10)))
	p.sessionNumber = 2
	deliverClientPush(t, p, 1, clientPushBody(common, 37, clientStackPayload(268436718, 100, 2, 10, false)))
	snapshot := p.effects.SnapshotAt(clientQuery(p, 1))
	if len(snapshot.Effects) != 0 || snapshot.Counters.BoundaryDrops != 1 || snapshot.Counters.UpdatesWithoutApply != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestClientReportsKeepTeamRelationAcrossRoleSwitch(t *testing.T) {
	p := newClientEffectTestProcessor()
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, 9, clientApplyPayload(268436701, 2002202311, 102, 5, 30)))
	p.observeLocalRoleEntity(101)
	effect := onlyEffect(t, p.effects.SnapshotAt(clientQuery(p, 1)))
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
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 200}, 9, clientApplyPayload(268436800, 1101005002, 100, 1, 1)))
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 201}, 9, clientApplyPayload(268436801, 70000003, 201, 1, -1)))
	snapshot := p.effects.SnapshotAt(clientQuery(p, 0.5))
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
	before := p.damageStatistics.Snapshot()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436766, 1312010033, 100, 1, 12)))
	deliverClientPush(t, p, 1, clientPushBody(common, 37, clientStackPayload(268436766, 100, 1, 12, false)))
	deliverClientPush(t, p, 12, clientPushBody(common, 10, clientRemovePayload(268436766, 100, false)))
	after := p.damageStatistics.Snapshot()
	if before.DamageDealt != after.DamageDealt || before.DamageTaken != after.DamageTaken || before.OutgoingEvents != after.OutgoingEvents ||
		before.IncomingEvents != after.IncomingEvents || before.StatisticsIntervalID != after.StatisticsIntervalID {
		t.Fatalf("damage statistics changed: %+v -> %+v", before, after)
	}
}

func TestClientEffectPushRejectsMalformedFields(t *testing.T) {
	p := newClientEffectTestProcessor()
	frame := gameFrame{commandID: 19470, direction: "out", timestamp: effectAt(0)}
	// Handle must be a varint; a length-delimited value is a decode error, not a zero handle.
	payload := appendBytesField(nil, 3, []byte{1})
	if err := p.handleClientEffectPushes(frame, clientPushBody(testCombatCommon{entityID: 100}, 9, payload)); err == nil {
		t.Fatal("malformed ApplyGameplayEffectPush was accepted")
	}
	if len(p.effects.Snapshot().Effects) != 0 {
		t.Fatal("malformed push created state")
	}
}

func TestServerBuffRequestLinksToClientResponseByPreMessageID(t *testing.T) {
	p := newClientEffectTestProcessor()
	request := appendVarintField(nil, 4, 800100008343)
	request = appendVarintField(request, 15, 100)
	notify := appendBytesField(nil, 1, clientCommonBody(testCombatCommon{entityID: 100, messageID: 1}))
	notify = appendBytesField(notify, 53, request)
	deliverEffectMessage(t, p, 0, appendBytesField(nil, 5, appendBytesField(nil, 2, notify)))
	if len(p.effects.Snapshot().Effects) != 0 {
		t.Fatal("a server request created effect state")
	}

	response := appendVarintField(nil, 5, 1)
	response = appendVarintField(response, 11, 268436712)
	row, err := decodeClientEffectPush(gameFrame{commandID: 19470, direction: "out"}, 4, response, combatCommonContext{entityID: u64(100), preMessageID: u64(1)})
	if err != nil {
		t.Fatal(err)
	}
	row.SessionNumber = 1
	p.linkServerBuffRequest(&row)
	if row.RequestLinkStatus != resolutionCorrelated || row.RequestLinkedID == nil || *row.RequestLinkedID != 800100008343 || row.HandleID == nil || *row.HandleID != 268436712 {
		t.Fatalf("linked response = %+v", row)
	}

	other := row
	other.RequestLinkedID, other.RequestLinkStatus = nil, ""
	other.CombatCommonEntityID = u64(101)
	p.linkServerBuffRequest(&other)
	if other.RequestLinkStatus != resolutionUnknown || other.RequestLinkedID != nil {
		t.Fatalf("response on another entity was linked: %+v", other)
	}
	wrongKind := row
	wrongKind.EventKind, wrongKind.RequestLinkedID = "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE", nil
	p.linkServerBuffRequest(&wrongKind)
	if wrongKind.RequestLinkStatus != resolutionUnknown || wrongKind.RequestLinkedID != nil {
		t.Fatalf("response of another kind was linked: %+v", wrongKind)
	}
}
