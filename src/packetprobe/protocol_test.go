package main

import (
	"bytes"
	"strings"
	"testing"
)

func appendVarintField(dst []byte, number, value uint64) []byte {
	dst = append(dst, byte(number<<3))
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func appendBytesField(dst []byte, number uint64, value []byte) []byte {
	dst = append(dst, byte(number<<3|2), byte(len(value)))
	return append(dst, value...)
}

func TestDamageRequestUsesSchemaEntityAndSkillFields(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	u3n := appendVarintField(nil, 9, 70010001)    // DamageId
	u3n = appendVarintField(u3n, 10, 902)         // TargetEntityId
	u3n = appendVarintField(u3n, 12, 2)           // SkillLevel
	u3n = appendVarintField(u3n, 13, 25320)       // AttackerEntityId
	context := appendVarintField(nil, 4, 1312011) // SkillId
	context = appendVarintField(context, 5, 77)   // SkillMessageId
	u3n = appendBytesField(u3n, 15, context)      // DamageContext
	request := appendVarintField(nil, 2, 42)      // RPC ID
	request = appendBytesField(request, 5, u3n)
	variant := appendBytesField(nil, 3, request)
	outer := appendBytesField(nil, 14, variant)

	p.handleDamageRequests(outer)

	got, ok := p.pending[42]
	if !ok {
		t.Fatal("damage request was not retained")
	}
	if !got.hasAttackerEntityID || got.attackerEntityID != 25320 {
		t.Fatalf("attacker entity ID = %d (present=%t), want 25320", got.attackerEntityID, got.hasAttackerEntityID)
	}
	if !got.hasTargetEntityID || got.targetEntityID != 902 {
		t.Fatalf("target entity ID = %d (present=%t), want 902", got.targetEntityID, got.hasTargetEntityID)
	}
	if got.skillLevel != 2 || got.skillID != 1312011 || got.skillMessageID != 77 || got.damageID != 70010001 {
		t.Fatalf("request context = %+v, want skill level/id/message and damage ID to match schema fields", got)
	}
}

func TestDamageResponseLabelsEntityAndKeepsCharacterUnknown(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.pending[42] = pendingDamageRequest{attackerEntityID: 25320, hasAttackerEntityID: true}
	response := appendVarintField(nil, 4, 0)     // success
	response = appendVarintField(response, 5, 0) // not immune
	response = appendVarintField(response, 10, 9001)

	p.handleOneDamageResult(42, response)
	p.handleOneDamageResult(42, response)

	if got, want := stdout.String(), "Unknown [Char: ?] [Entity: 25320] 9001\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if p.stats.printed != 1 || p.stats.duplicateResponses != 1 || p.stats.sumAbsDamage != 9001 {
		t.Fatalf("unexpected stats: printed=%d duplicates=%d sum=%d", p.stats.printed, p.stats.duplicateResponses, p.stats.sumAbsDamage)
	}
	if !strings.Contains(stdout.String(), "Entity: 25320") || strings.Contains(stdout.String(), "Char: 25320") {
		t.Fatalf("entity was mislabeled or unverified character identity was presented as resolved: %q", stdout.String())
	}
}

func TestDamageResponseWithoutSourceUsesUnknown(t *testing.T) {
	var stdout, diagnostic bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.pending[7] = pendingDamageRequest{}
	response := appendVarintField(nil, 4, 0)
	response = appendVarintField(response, 5, 0)
	response = appendVarintField(response, 10, 33)

	p.handleOneDamageResult(7, response)

	if got, want := stdout.String(), "Unknown [Char: ?] [Entity: ?] 33\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if p.stats.missingAttackerEntityID != 1 {
		t.Fatalf("missingAttackerEntityID = %d, want 1", p.stats.missingAttackerEntityID)
	}
}
