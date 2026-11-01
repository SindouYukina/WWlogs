package capture

import (
	"testing"
	"time"
)

// These tests drive the real CombatReceivePackNotify decoder with synthetic
// protobuf bytes. They are SYNTHETIC evidence: the byte layout follows the
// decoder's field table, not a captured game packet.

func effectPackBody(commonEntityID uint64, innerField uint64, payload []byte) []byte {
	notify := appendBytesField(nil, 1, appendVarintField(nil, 5, commonEntityID)) // CombatCommon.EntityId
	notify = appendBytesField(notify, innerField, payload)
	return appendBytesField(nil, 5, appendBytesField(nil, 2, notify))
}

func effectFloatField(dst []byte, number uint64, value float32) []byte {
	return appendFixed32TestField(dst, number, mathFloat32bits(value))
}

func effectApplyPayload(handle, effectID, target, instigator uint64, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, 2, handle)
	payload = appendVarintField(payload, 11, effectID)
	payload = appendVarintField(payload, 7, target)
	payload = appendVarintField(payload, 8, instigator)
	payload = appendVarintField(payload, 1, stack)
	payload = effectFloatField(payload, 15, duration)
	return effectFloatField(payload, 12, remaining)
}

func effectStackPayload(handle, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, 9, handle)
	payload = appendVarintField(payload, 4, stack)
	payload = effectFloatField(payload, 7, duration)
	return effectFloatField(payload, 11, remaining)
}

func effectDurationPayload(handle uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, 12, handle)
	payload = effectFloatField(payload, 2, duration)
	return effectFloatField(payload, 5, remaining)
}

func effectRemovePayload(handle, target, instigator uint64) []byte {
	payload := appendVarintField(nil, 13, handle)
	payload = appendVarintField(payload, 5, target)
	return appendVarintField(payload, 14, instigator)
}

func deliverEffectMessage(t *testing.T, p *processor, seconds float64, body []byte) {
	t.Helper()
	frame := gameFrame{commandID: 25706, direction: "in", timestamp: effectAt(seconds)}
	if err := p.handleCombatReceivePackNotify(frame, body); err != nil {
		t.Fatalf("handle CombatReceivePackNotify: %v", err)
	}
}

func effectQueryFor(p *processor, seconds float64) CaptureQueryTime {
	return CaptureQueryTime{CaptureSessionID: p.effects.captureSessionID, Timestamp: effectAt(seconds)}
}

func TestEffectFeedWithoutTrackerDoesNotPanic(t *testing.T) {
	p := &processor{sessionNumber: 1, entities: newEntityResolver()}
	frame := gameFrame{commandID: 25706, direction: "in", timestamp: time.Unix(1, 0)}
	if err := p.handleCombatReceivePackNotify(frame, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 5, 5))); err != nil {
		t.Fatalf("handle without tracker: %v", err)
	}
	if got := p.effectSnapshotAtLastFrame(); len(got.Effects) != 0 {
		t.Fatalf("snapshot without tracker = %+v", got)
	}
}

func effectSnapshotResponse(handle, buffID, instigator, stack uint64, duration, remaining float32) []byte {
	record := appendVarintField(nil, 1, handle)
	record = appendVarintField(record, 2, buffID)
	record = appendVarintField(record, 4, stack)
	record = appendVarintField(record, 5, instigator)
	record = effectFloatField(record, 8, duration)
	record = effectFloatField(record, 9, remaining)
	components := appendBytesField(nil, 15, appendBytesField(nil, 1, record))
	return appendBytesField(nil, 11, components)
}

func deliverEffectSnapshot(t *testing.T, p *processor, token uint16, holder uint64, seconds float64, response []byte) {
	t.Helper()
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, holder)); err != nil {
		t.Fatal(err)
	}
	frame := gameFrame{commandID: 26702, direction: "in", token: token, timestamp: effectAt(seconds)}
	if err := p.handleActiveResponse(frame, response); err != nil {
		t.Fatal(err)
	}
}
