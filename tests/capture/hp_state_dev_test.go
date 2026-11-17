//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHPStateKeepsLifeAndLifeMaxOnly(t *testing.T) {
	var out strings.Builder
	p := &processor{
		sessionNumber: 2,
		dev: processorDiagnostics{
			protocolSchema: hpTestSchema(),
			hpStateOut:     &out,
			frameOrdinal:   1,
		},
	}
	frame := gameFrame{commandID: cmdCombatReceivePackNotify, timestamp: time.Date(2026, 10, 28, 9, 44, 28, 0, time.UTC)}
	decoded := wrapData(notifyWithEntity(52082,
		attributeMessage(3, 484076, false),
		attributeMessage(2, 500000, true),
		attributeMessage(60, 999, true),
	))
	p.archiveHPState(frame, decoded)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("records = %d, want 2 (Life and LifeMax)", len(lines))
	}
	var life hpStateRecord
	if err := json.Unmarshal([]byte(lines[0]), &life); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if life.EntityID != 52082 || life.AttributeType != 3 || life.AttributeName != "Life" || life.BaseValue != 484076 || life.CurrentValue != 0 {
		t.Fatalf("life record = %+v", life)
	}
	if life.FrameOrdinal != 0 || life.SessionNumber != 2 {
		t.Fatalf("life metadata = %+v", life)
	}
}

func TestHPStateSkipsNotifyWithoutEntity(t *testing.T) {
	var out strings.Builder
	p := &processor{dev: processorDiagnostics{protocolSchema: hpTestSchema(), hpStateOut: &out}}
	notify := decodedField{Number: fieldCombatReceiveDataCombatNotifyData, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Type: "CombatNotifyData", Fields: []decodedField{
		{Number: fieldCombatNotifyDataAttributeChangedNotify, Name: "AttributeChangedNotify", Kind: "class", Message: &decodedMessage{Fields: []decodedField{attributeMessage(3, 10, true)}}},
	}}}
	p.archiveHPState(gameFrame{}, wrapData(notify))
	if out.Len() != 0 {
		t.Fatalf("record written without CombatCommon entity: %q", out.String())
	}
}
