package capture

import (
	"testing"
	"time"
)

// These tests drive the real CombatReceivePackNotify decoder with synthetic
// protobuf bytes. They are SYNTHETIC evidence: the byte layout follows the
// decoder's field table, not a captured game packet.

func effectPackBody(commonEntityID uint64, innerField uint64, payload []byte) []byte {
	notify := appendBytesField(nil, FieldCombatNotifyDataCombatCommon, appendVarintField(nil, FieldCombatCommonEntityID, commonEntityID)) // CombatCommon.EntityId
	notify = appendBytesField(notify, innerField, payload)
	return appendBytesField(nil, FieldCombatReceivePackNotifyData, appendBytesField(nil, FieldCombatReceiveDataCombatNotifyData, notify))
}

func effectFloatField(dst []byte, number uint64, value float32) []byte {
	return appendFixed32TestField(dst, number, mathFloat32bits(value))
}

func effectApplyPayload(handle, effectID, target, instigator uint64, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, FieldApplyGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyID, effectID)
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyEntityID, target)
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyInstigatorID, instigator)
	payload = appendVarintField(payload, FieldApplyGameplayEffectNotifyStackCount, stack)
	payload = effectFloatField(payload, FieldApplyGameplayEffectNotifyDuration, duration)
	return effectFloatField(payload, FieldApplyGameplayEffectNotifyLeftDuration, remaining)
}

func effectStackPayload(handle, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, FieldBuffStackCountNotifyHandleID, handle)
	payload = appendVarintField(payload, FieldBuffStackCountNotifyNewStackCount, stack)
	payload = effectFloatField(payload, FieldBuffStackCountNotifyDuration, duration)
	return effectFloatField(payload, FieldBuffStackCountNotifyLeftDuration, remaining)
}

func effectDurationPayload(handle uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, FieldBuffDurationNotifyHandleID, handle)
	payload = effectFloatField(payload, FieldBuffDurationNotifyDuration, duration)
	return effectFloatField(payload, FieldBuffDurationNotifyLeftDuration, remaining)
}

func effectRemovePayload(handle, target, instigator uint64) []byte {
	payload := appendVarintField(nil, FieldRemoveGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, FieldRemoveGameplayEffectNotifyEntityID, target)
	return appendVarintField(payload, FieldRemoveGameplayEffectNotifyInstigatorID, instigator)
}

func deliverEffectMessage(t *testing.T, p *Processor, seconds float64, body []byte) {
	t.Helper()
	frame := GameFrame{CommandID: CmdCombatReceivePackNotify, Direction: "in", Timestamp: effectAt(seconds)}
	if err := p.HandleCombatReceivePackNotify(frame, body); err != nil {
		t.Fatalf("handle CombatReceivePackNotify: %v", err)
	}
}

func effectQueryFor(p *Processor, seconds float64) CaptureQueryTime {
	return CaptureQueryTime{CaptureSessionID: p.Effects.CaptureSessionID, Timestamp: effectAt(seconds)}
}

func TestEffectFeedWithoutTrackerDoesNotPanic(t *testing.T) {
	p := &Processor{SessionNumber: 1, Entities: NewEntityResolver()}
	frame := GameFrame{CommandID: CmdCombatReceivePackNotify, Direction: "in", Timestamp: time.Unix(1, 0)}
	if err := p.HandleCombatReceivePackNotify(frame, effectPackBody(200, FieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 5, 5))); err != nil {
		t.Fatalf("handle without tracker: %v", err)
	}
	if got := p.EffectSnapshotAtLastFrame(); len(got.Effects) != 0 {
		t.Fatalf("snapshot without tracker = %+v", got)
	}
}

func effectSnapshotResponse(handle, buffID, instigator, stack uint64, duration, remaining float32) []byte {
	record := appendVarintField(nil, FieldFightBuffInformationHandleID, handle)
	record = appendVarintField(record, FieldFightBuffInformationBuffID, buffID)
	record = appendVarintField(record, FieldFightBuffInformationStackCount, stack)
	record = appendVarintField(record, FieldFightBuffInformationInstigatorID, instigator)
	record = effectFloatField(record, FieldFightBuffInformationDuration, duration)
	record = effectFloatField(record, FieldFightBuffInformationLeftDuration, remaining)
	components := appendBytesField(nil, FieldEntityComponentPbFightBuffComponent, appendBytesField(nil, FieldFightBuffComponentPbFightBuffInfos, record))
	return appendBytesField(nil, FieldEntityActiveResponseComponentPbs, components)
}

func deliverEffectSnapshot(t *testing.T, p *Processor, token uint16, holder uint64, seconds float64, response []byte) {
	t.Helper()
	if err := p.HandleActiveRequest(token, appendVarintField(nil, FieldEntityActiveRequestEntityID, holder)); err != nil {
		t.Fatal(err)
	}
	frame := GameFrame{CommandID: CmdEntityActiveResponse, Direction: "in", Token: token, Timestamp: effectAt(seconds)}
	if err := p.HandleActiveResponse(frame, response); err != nil {
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
