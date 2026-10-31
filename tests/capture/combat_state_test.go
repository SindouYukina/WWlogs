package capture

import (
	"strings"
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

func fsmFixture() *gameMaster {
	fsm := &fsmMaster{
		machinesByNode: map[int64][]string{1: {"A", "B"}, 2: {"A", "B"}, 3: {"A", "B"}, 9: {"C"}},
		names: map[fsmNodeKey]string{
			{"A", 1}: "root", {"A", 2}: "idle", {"A", 3}: "fight",
			{"B", 1}: "root", {"B", 2}: "idle", {"B", 3}: "attack",
			{"C", 9}: "lonely",
		},
		children:   map[fsmNodeKey][]int64{{"A", 1}: {2, 3}, {"B", 1}: {2, 3}},
		references: map[fsmNodeKey]int64{},
	}
	return &gameMaster{fsm: fsm}
}

func TestFsmTransitionIsNamedOnlyWhenEveryCandidateMachineAgrees(t *testing.T) {
	master := fsmFixture()

	agree := master.resolveFsmTransition(1, 2, 2, nil, nil)
	if agree.Status != resolutionCorrelated || agree.MachineCount != 2 || agree.FromName != "idle" || agree.ToName != "idle" {
		t.Fatalf("both machines name state 2 the same: %+v", agree)
	}
	differ := master.resolveFsmTransition(1, 2, 3, nil, nil)
	if differ.Status != resolutionUnknown || differ.MachineCount != 2 || differ.FromName != "" || differ.ToName != "" || !strings.Contains(differ.Evidence, "differently") {
		t.Fatalf("machines that disagree on a state name must not name it: %+v", differ)
	}
	none := master.resolveFsmTransition(1, 2, 77, nil, nil)
	if none.Status != resolutionUnknown || none.MachineCount != 0 {
		t.Fatalf("a state that no machine holds under the FsmId must stay unknown: %+v", none)
	}
	if (*gameMaster)(nil).resolveFsmTransition(1, 2, 3, nil, nil) != nil || (&gameMaster{}).resolveFsmTransition(1, 2, 3, nil, nil) != nil {
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
	below := fsm.descendants("M", 10)
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
	if got := master.fsmLabel("idle"); got == nil || got.Text != "Idle" || got.Locale != "en-US" || got.Kind != "unofficial-translation" {
		t.Fatalf("the master locale text must win: %+v", got)
	}
	if got := master.fsmLabel("root"); got == nil || got.Text != "root" || got.Locale != "zh-CN" || got.Kind != "developer-original" {
		t.Fatalf("a name without a translation must fall back to the original: %+v", got)
	}
	if master.fsmLabel("fight") != nil || master.fsmLabel("") != nil || (*gameMaster)(nil).fsmLabel("idle") != nil {
		t.Fatal("an unlabelled or empty name must give no label")
	}
}

func TestActiveResponseFsmComponentOnlyAddsTheHashes(t *testing.T) {
	resolver := newEntityResolver()
	resolver.entities[77] = runtimeEntity{entityID: 77, componentSourceMessageID: 5, componentSourceFieldPath: "kept"}
	component := appendVarintField(nil, 2, uint64(uint32(0x59d9c441)))
	component = appendVarintField(component, 3, uint64(uint32(0x4a3b2c1d)))
	if err := resolver.applyActiveResponseFsmComponents(77, []protoField{{number: 28, wire: 2, data: component}}); err != nil {
		t.Fatal(err)
	}
	got := resolver.entities[77]
	if !got.hasFsmHashCode || got.fsmHashCode != 0x59d9c441 || !got.hasFsmCommonHashCode || got.fsmCommonHashCode != 0x4a3b2c1d {
		t.Fatalf("hashes not recorded: %+v", got)
	}
	if got.componentSourceMessageID != 5 || got.componentSourceFieldPath != "kept" {
		t.Fatalf("identity provenance must stay untouched: %+v", got)
	}
	if err := resolver.applyActiveResponseFsmComponents(999, []protoField{{number: 28, wire: 2, data: component}}); err != nil {
		t.Fatal(err)
	}
	if _, created := resolver.entities[999]; created {
		t.Fatal("an unknown entity must not be created from an FSM component")
	}
}
