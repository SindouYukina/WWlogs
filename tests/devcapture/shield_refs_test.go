package devcapture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

func TestObserveShieldReferencesUsesOnlyTheTypedConfigIDNamespace(t *testing.T) {
	var out bytes.Buffer
	p := observed(&processor{Processor: &capture.Processor{Masters: &capture.GameMaster{ShieldDefinitions: map[uint64]struct{}{1409404003: {}, 1508990003: {}}, BuffDefinitions: map[uint64]struct{}{1409404003: {}}}, SessionNumber: 2}, dev: processorDiagnostics{
		shieldRefOut: &out,
		frameOrdinal: 9,
	}})
	shieldNotify := &decodedMessage{Type: "ShieldUpdateNotify", Fields: []decodedField{
		messageField("Shields", &decodedMessage{Type: "ShieldUpdateInfo", Fields: []decodedField{scalarField("Handle", 31), scalarField("ConfigId", 1409404003), scalarField("ShieldValue", 720), scalarField("UpdateType", 1)}}),
		messageField("Shields", &decodedMessage{Type: "ShieldUpdateInfo", Fields: []decodedField{scalarField("ConfigId", 1508990003), scalarField("ShieldValue", 400)}}),
		messageField("Shields", &decodedMessage{Type: "ShieldUpdateInfo", Fields: []decodedField{scalarField("ConfigId", 1312010090)}}),
	}}
	data := &decodedMessage{Type: "CombatReceivePackNotifyData", Fields: []decodedField{
		messageField("CombatNotifyData", &decodedMessage{Type: "CombatNotifyData", Fields: []decodedField{messageField("ShieldUpdateNotify", shieldNotify)}}),
		messageField("Unrelated", &decodedMessage{Type: "Unrelated", Fields: []decodedField{scalarField("ConfigId", 1409404003)}}),
	}}
	p.observeShieldReferences(capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify, Token: 99}, data, "in")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three typed ShieldUpdateInfo records, got %d: %q", len(lines), out.String())
	}
	var first, second, missing shieldConfigRefRecord
	for index, target := range []*shieldConfigRefRecord{&first, &second, &missing} {
		if err := json.Unmarshal([]byte(lines[index]), target); err != nil {
			t.Fatalf("decode record %d: %v", index, err)
		}
	}
	if first.ConfigID == nil || *first.ConfigID != 1409404003 || first.ShieldDefinitionStatus != capture.ResolutionCorrelated || first.ShieldDefinitionTable != "shield" || first.ShieldDefinitionID == nil || *first.ShieldDefinitionID != 1409404003 {
		t.Fatalf("first exact shield link = %+v", first)
	}
	if first.ShieldValue == nil || *first.ShieldValue != 720 || first.Handle == nil || *first.Handle != 31 || first.UpdateType == nil || *first.UpdateType != 1 {
		t.Fatalf("raw ShieldUpdateInfo values were not preserved: %+v", first)
	}
	if first.FieldPath != "Data.CombatNotifyData.ShieldUpdateNotify.Shields[0].ConfigId" || first.OuterCommandID != capture.CmdCombatReceivePackNotify || first.FrameToken != 99 || first.Direction != "in" {
		t.Fatalf("typed reference context missing: %+v", first)
	}
	if second.ConfigID == nil || *second.ConfigID != 1508990003 || second.ShieldDefinitionStatus != capture.ResolutionCorrelated {
		t.Fatalf("second exact shield link = %+v", second)
	}
	if missing.ConfigID == nil || *missing.ConfigID != 1312010090 || missing.ShieldDefinitionStatus != capture.ResolutionUnknown || missing.ShieldDefinitionTable != "" {
		t.Fatalf("unmatched ConfigId must remain unresolved: %+v", missing)
	}
}
