package devcapture

import (
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

func TestFsmTransitionIsNamedOnlyWhenEveryCandidateMachineAgrees(t *testing.T) {
	master := fsmFixture()

	agree := resolveFsmTransition(master, 1, 2, 2, nil, nil)
	if agree.Status != capture.ResolutionCorrelated || agree.MachineCount != 2 || agree.FromName != "idle" || agree.ToName != "idle" {
		t.Fatalf("both machines name state 2 the same: %+v", agree)
	}
	differ := resolveFsmTransition(master, 1, 2, 3, nil, nil)
	if differ.Status != capture.ResolutionUnknown || differ.MachineCount != 2 || differ.FromName != "" || differ.ToName != "" || !strings.Contains(differ.Evidence, "differently") {
		t.Fatalf("machines that disagree on a state name must not name it: %+v", differ)
	}
	none := resolveFsmTransition(master, 1, 2, 77, nil, nil)
	if none.Status != capture.ResolutionUnknown || none.MachineCount != 0 {
		t.Fatalf("a state that no machine holds under the FsmId must stay unknown: %+v", none)
	}
	if resolveFsmTransition((*capture.GameMaster)(nil), 1, 2, 3, nil, nil) != nil || resolveFsmTransition((&capture.GameMaster{}), 1, 2, 3, nil, nil) != nil {
		t.Fatal("without state-machine nodes there is nothing to resolve")
	}
}

func TestFsmDescendantsFollowReferencesIntoOtherMachines(t *testing.T) {
	fsm := &capture.FSMMaster{
		MachinesByNode: map[int64][]string{10: {"M"}, 20: {"COMMON"}, 21: {"COMMON"}},
		Names:          map[capture.FSMNodeKey]string{{Machine: "M", UUID: 10}: "wrapper", {Machine: "COMMON", UUID: 20}: "main", {Machine: "COMMON", UUID: 21}: "idle"},
		Children:       map[capture.FSMNodeKey][]int64{{Machine: "COMMON", UUID: 20}: {21}},
		References:     map[capture.FSMNodeKey]int64{{Machine: "M", UUID: 10}: 20},
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
	master.Locale = "en-US"
	master.FSM.Labels = map[string]map[string]capture.LocalizedSkillLabel{
		"idle": {"zh-CN": {Text: "idle", Locale: "zh-CN", Kind: "developer-original"}, "en-US": {Text: "Idle", Locale: "en-US", Kind: "unofficial-translation"}},
		"root": {"zh-CN": {Text: "root", Locale: "zh-CN", Kind: "developer-original"}},
	}
	if got := fsmLabel(master, "idle"); got == nil || got.Text != "Idle" || got.Locale != "en-US" || got.Kind != "unofficial-translation" {
		t.Fatalf("the master locale text must win: %+v", got)
	}
	if got := fsmLabel(master, "root"); got == nil || got.Text != "root" || got.Locale != "zh-CN" || got.Kind != "developer-original" {
		t.Fatalf("a name without a translation must fall back to the original: %+v", got)
	}
	if fsmLabel(master, "fight") != nil || fsmLabel(master, "") != nil || fsmLabel((*capture.GameMaster)(nil), "idle") != nil {
		t.Fatal("an unlabelled or empty name must give no label")
	}
}

func TestResolveAttributeNamesOnlyWhenKeyAndProtocolNameAgree(t *testing.T) {
	schema := &protocolSchema{Enums: map[string]schemaEnum{"EAttributeType": {Members: map[string]int64{"LifeMax": 2, "CdReduse": 12, "Atk": 7, "Odd": 99}}}}
	master := &capture.GameMaster{PropertyNames: map[int64]capture.PropertyName{
		2:  {Key: "LifeMax", Name: "生命"},
		12: {Key: "CDReduse", Name: "冷卻縮減"},
		7:  {Key: "Defense", Name: "防禦"},
		99: {Key: "Odd"},
	}}

	if got := resolveCombatStateAttribute(master, schema, 2, "EAttributeType"); got.Status != capture.ResolutionCorrelated || got.Name != "生命" {
		t.Fatalf("matching key must name the attribute: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 12, "EAttributeType"); got.Status != capture.ResolutionCorrelated || got.Name != "冷卻縮減" {
		t.Fatalf("a case-only difference must still match: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 7, "EAttributeType"); got.Status != capture.ResolutionUnknown || got.Name != "" || got.PropertyKey != "Defense" {
		t.Fatalf("a different key must not be named: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 99, "EAttributeType"); got.Status != capture.ResolutionUnknown || got.Name != "" {
		t.Fatalf("a row without a localized name must stay unnamed: %+v", got)
	}
	if got := resolveCombatStateAttribute(master, schema, 5000, "EAttributeType"); got.Status != capture.ResolutionUnknown || got.PropertyKey != "" {
		t.Fatalf("an unknown id must stay unknown: %+v", got)
	}
	if got := resolveCombatStateAttribute((*capture.GameMaster)(nil), schema, 2, "EAttributeType"); got.Status != capture.ResolutionUnknown || got.ProtocolName != "LifeMax" {
		t.Fatalf("without a master the protocol name is still kept: %+v", got)
	}
}
