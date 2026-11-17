//go:build wwdev

package capture

import (
	"strings"
	"testing"
)

func TestFsmTransitionIsNamedOnlyWhenEveryCandidateMachineAgrees(t *testing.T) {
	master := fsmFixture()

	agree := resolveFsmTransition(master, 1, 2, 2, nil, nil)
	if agree.Status != resolutionCorrelated || agree.MachineCount != 2 || agree.FromName != "idle" || agree.ToName != "idle" {
		t.Fatalf("both machines name state 2 the same: %+v", agree)
	}
	differ := resolveFsmTransition(master, 1, 2, 3, nil, nil)
	if differ.Status != resolutionUnknown || differ.MachineCount != 2 || differ.FromName != "" || differ.ToName != "" || !strings.Contains(differ.Evidence, "differently") {
		t.Fatalf("machines that disagree on a state name must not name it: %+v", differ)
	}
	none := resolveFsmTransition(master, 1, 2, 77, nil, nil)
	if none.Status != resolutionUnknown || none.MachineCount != 0 {
		t.Fatalf("a state that no machine holds under the FsmId must stay unknown: %+v", none)
	}
	if resolveFsmTransition((*gameMaster)(nil), 1, 2, 3, nil, nil) != nil || resolveFsmTransition((&gameMaster{}), 1, 2, 3, nil, nil) != nil {
		t.Fatal("without state-machine nodes there is nothing to resolve")
	}
}

func TestFsmDescendantsFollowReferencesIntoOtherMachines(t *testing.T) {
	fsm := &fsmMaster{
		machinesByNode: map[int64][]string{10: {"M"}, 20: {"COMMON"}, 21: {"COMMON"}},
		names:          map[fsmNodeKey]string{{"M", 10}: "wrapper", {"COMMON", 20}: "main", {"COMMON", 21}: "idle"},
		children:       map[fsmNodeKey][]int64{{"COMMON", 20}: {21}},
		references:     map[fsmNodeKey]int64{{"M", 10}: 20},
	}
	below := fsmDescendants(fsm, "M", 10)
	if _, ok := below[20]; !ok {
		t.Fatalf("the referenced node must be reachable: %v", below)
	}
	if _, ok := below[21]; !ok {
		t.Fatalf("children of the referenced node must be reachable: %v", below)
	}
}

func TestFsmLabelUsesTheMasterLocaleAndFallsBackToTheOriginal(t *testing.T) {
	master := fsmFixture()
	master.locale = "en-US"
	master.fsm.labels = map[string]map[string]localizedSkillLabel{
		"idle": {"zh-CN": {Text: "idle", Locale: "zh-CN", Kind: "developer-original"}, "en-US": {Text: "Idle", Locale: "en-US", Kind: "unofficial-translation"}},
		"root": {"zh-CN": {Text: "root", Locale: "zh-CN", Kind: "developer-original"}},
	}
	if got := fsmLabel(master, "idle"); got == nil || got.Text != "Idle" || got.Locale != "en-US" || got.Kind != "unofficial-translation" {
		t.Fatalf("the master locale text must win: %+v", got)
	}
	if got := fsmLabel(master, "root"); got == nil || got.Text != "root" || got.Locale != "zh-CN" || got.Kind != "developer-original" {
		t.Fatalf("a name without a translation must fall back to the original: %+v", got)
	}
	if fsmLabel(master, "fight") != nil || fsmLabel(master, "") != nil || fsmLabel((*gameMaster)(nil), "idle") != nil {
		t.Fatal("an unlabelled or empty name must give no label")
	}
}

func TestResolveAttributeNamesOnlyWhenKeyAndProtocolNameAgree(t *testing.T) {
	schema := &protocolSchema{Enums: map[string]schemaEnum{"EAttributeType": {Members: map[string]int64{"LifeMax": 2, "CdReduse": 12, "Atk": 7, "Odd": 99}}}}
	master := &gameMaster{propertyNames: map[int64]propertyName{
		2:  {Key: "LifeMax", Name: "生命"},
		12: {Key: "CDReduse", Name: "冷卻縮減"},
		7:  {Key: "Defense", Name: "防禦"},
		99: {Key: "Odd"},
	}}

	if got := resolveCombatStateAttribute(master, schema, 2, "EAttributeType"); got.Status != resolutionCorrelated || got.Name != "生命" {
		t.Fatalf("matching key must name the attribute: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 12, "EAttributeType"); got.Status != resolutionCorrelated || got.Name != "冷卻縮減" {
		t.Fatalf("a case-only difference must still match: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 7, "EAttributeType"); got.Status != resolutionUnknown || got.Name != "" || got.PropertyKey != "Defense" {
		t.Fatalf("a different key must not be named: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 99, "EAttributeType"); got.Status != resolutionUnknown || got.Name != "" {
		t.Fatalf("a row without a localized name must stay unnamed: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 5000, "EAttributeType"); got.Status != resolutionUnknown || got.PropertyKey != "" {
		t.Fatalf("an unknown id must stay unknown: %+v", got)
	}
	if got := resolveCombatStateAttribute((*gameMaster)(nil), schema, 2, "EAttributeType"); got.Status != resolutionUnknown || got.ProtocolName != "LifeMax" {
		t.Fatalf("without a master the protocol name is still kept: %+v", got)
	}
}
