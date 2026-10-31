//go:build wwdev

package capture

import (
	"testing"
)

func TestResolveAttributeNamesOnlyWhenKeyAndProtocolNameAgree(t *testing.T) {
	schema := &protocolSchema{Enums: map[string]schemaEnum{"EAttributeType": {Members: map[string]int64{"LifeMax": 2, "CdReduse": 12, "Atk": 7, "Odd": 99}}}}
	master := &gameMaster{propertyNames: map[int64]propertyName{
		2:  {Key: "LifeMax", Name: "生命"},
		12: {Key: "CDReduse", Name: "冷卻縮減"},
		7:  {Key: "Defense", Name: "防禦"},
		99: {Key: "Odd"},
	}}

	if got := master.resolveAttribute(schema, 2, "EAttributeType"); got.Status != resolutionCorrelated || got.Name != "生命" {
		t.Fatalf("matching key must name the attribute: %+v", got)
	}
	if got := master.resolveAttribute(schema, 12, "EAttributeType"); got.Status != resolutionCorrelated || got.Name != "冷卻縮減" {
		t.Fatalf("a case-only difference must still match: %+v", got)
	}
	if got := master.resolveAttribute(schema, 7, "EAttributeType"); got.Status != resolutionUnknown || got.Name != "" || got.PropertyKey != "Defense" {
		t.Fatalf("a different key must not be named: %+v", got)
	}
	if got := master.resolveAttribute(schema, 99, "EAttributeType"); got.Status != resolutionUnknown || got.Name != "" {
		t.Fatalf("a row without a localized name must stay unnamed: %+v", got)
	}
	if got := master.resolveAttribute(schema, 5000, "EAttributeType"); got.Status != resolutionUnknown || got.PropertyKey != "" {
		t.Fatalf("an unknown id must stay unknown: %+v", got)
	}
	if got := (*gameMaster)(nil).resolveAttribute(schema, 2, "EAttributeType"); got.Status != resolutionUnknown || got.ProtocolName != "LifeMax" {
		t.Fatalf("without a master the protocol name is still kept: %+v", got)
	}
}
