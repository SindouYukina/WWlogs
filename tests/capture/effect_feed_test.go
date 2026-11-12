package capture

import (
	"testing"
	"time"
)

// These tests drive the real CombatReceivePackNotify decoder with synthetic
// protobuf bytes. They are SYNTHETIC evidence: the byte layout follows the
// decoder's field table, not a captured game packet.

func effectPackBody(commonEntityID uint64, innerField uint64, payload []byte) []byte {
	notify := appendBytesField(nil, fieldCombatNotifyDataCombatCommon, appendVarintField(nil, fieldCombatCommonEntityID, commonEntityID)) // CombatCommon.EntityId
	notify = appendBytesField(notify, innerField, payload)
	return appendBytesField(nil, fieldCombatReceivePackNotifyData, appendBytesField(nil, fieldCombatReceiveDataCombatNotifyData, notify))
}

func effectFloatField(dst []byte, number uint64, value float32) []byte {
	return appendFixed32TestField(dst, number, mathFloat32bits(value))
}

func effectApplyPayload(handle, effectID, target, instigator uint64, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, fieldApplyGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyID, effectID)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyEntityID, target)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyInstigatorID, instigator)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyStackCount, stack)
	payload = effectFloatField(payload, fieldApplyGameplayEffectNotifyDuration, duration)
	return effectFloatField(payload, fieldApplyGameplayEffectNotifyLeftDuration, remaining)
}

func effectStackPayload(handle, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, fieldBuffStackCountNotifyHandleID, handle)
	payload = appendVarintField(payload, fieldBuffStackCountNotifyNewStackCount, stack)
	payload = effectFloatField(payload, fieldBuffStackCountNotifyDuration, duration)
	return effectFloatField(payload, fieldBuffStackCountNotifyLeftDuration, remaining)
}

func effectDurationPayload(handle uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, fieldBuffDurationNotifyHandleID, handle)
	payload = effectFloatField(payload, fieldBuffDurationNotifyDuration, duration)
	return effectFloatField(payload, fieldBuffDurationNotifyLeftDuration, remaining)
}

func effectRemovePayload(handle, target, instigator uint64) []byte {
	payload := appendVarintField(nil, fieldRemoveGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, fieldRemoveGameplayEffectNotifyEntityID, target)
	return appendVarintField(payload, fieldRemoveGameplayEffectNotifyInstigatorID, instigator)
}

func deliverEffectMessage(t *testing.T, p *processor, seconds float64, body []byte) {
	t.Helper()
	frame := gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in", timestamp: effectAt(seconds)}
	if err := p.handleCombatReceivePackNotify(frame, body); err != nil {
		t.Fatalf("handle CombatReceivePackNotify: %v", err)
	}
}

func effectQueryFor(p *processor, seconds float64) CaptureQueryTime {
	return CaptureQueryTime{CaptureSessionID: p.effects.captureSessionID, Timestamp: effectAt(seconds)}
}

func TestEffectFeedWithoutTrackerDoesNotPanic(t *testing.T) {
	p := &processor{sessionNumber: 1, entities: newEntityResolver()}
	frame := gameFrame{commandID: cmdCombatReceivePackNotify, direction: "in", timestamp: time.Unix(1, 0)}
	if err := p.handleCombatReceivePackNotify(frame, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 5, 5))); err != nil {
		t.Fatalf("handle without tracker: %v", err)
	}
	if got := p.effectSnapshotAtLastFrame(); len(got.Effects) != 0 {
		t.Fatalf("snapshot without tracker = %+v", got)
	}
}

func effectSnapshotResponse(handle, buffID, instigator, stack uint64, duration, remaining float32) []byte {
	record := appendVarintField(nil, fieldFightBuffInformationHandleID, handle)
	record = appendVarintField(record, fieldFightBuffInformationBuffID, buffID)
	record = appendVarintField(record, fieldFightBuffInformationStackCount, stack)
	record = appendVarintField(record, fieldFightBuffInformationInstigatorID, instigator)
	record = effectFloatField(record, fieldFightBuffInformationDuration, duration)
	record = effectFloatField(record, fieldFightBuffInformationLeftDuration, remaining)
	components := appendBytesField(nil, fieldEntityComponentPbFightBuffComponent, appendBytesField(nil, fieldFightBuffComponentPbFightBuffInfos, record))
	return appendBytesField(nil, fieldEntityActiveResponseComponentPbs, components)
}

func deliverEffectSnapshot(t *testing.T, p *processor, token uint16, holder uint64, seconds float64, response []byte) {
	t.Helper()
	if err := p.handleActiveRequest(token, appendVarintField(nil, fieldEntityActiveRequestEntityID, holder)); err != nil {
		t.Fatal(err)
	}
	frame := gameFrame{commandID: cmdEntityActiveResponse, direction: "in", token: token, timestamp: effectAt(seconds)}
	if err := p.handleActiveResponse(frame, response); err != nil {
		t.Fatal(err)
	}
}

func TestEffectTrackerRefreshUnknownRelationsResolvesEachEntityOnce(t *testing.T) {
	tracker := newTestEffectTracker()
	unknown := func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationUnknown, EffectRelationUnknown
	}
	for handle := uint64(1); handle <= 5; handle++ {
		tracker.ObserveApply(testEffectApply(handle, 0, unknown))
	}
	tracker.ObserveApply(testEffectApply(6, 0, func(e *EffectApplyEvent) {
		e.HolderRelation, e.SourceRelation = EffectRelationLocalRole, EffectRelationUnknown
	}))
	calls := map[uint64]int{}
	tracker.RefreshUnknownRelations(func(entityID uint64) EffectEntityRelation {
		calls[entityID]++
		if entityID == 20 {
			return EffectRelationLocalRole
		}
		return EffectRelationUnknown
	})
	if calls[10] != 1 || calls[20] != 1 {
		t.Fatalf("resolve calls = %v, want one per distinct entity", calls)
	}
	for _, effect := range tracker.Snapshot().Effects {
		if effect.SourceRelation != EffectRelationLocalRole {
			t.Fatalf("source relation = %s, want local_role", effect.SourceRelation)
		}
		if effect.HandleID == 6 && effect.HolderRelation != EffectRelationLocalRole {
			t.Fatalf("known holder relation changed: %s", effect.HolderRelation)
		}
	}
	tracker.RefreshUnknownRelations(nil)
}
