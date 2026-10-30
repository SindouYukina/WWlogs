package main

import (
	"strings"
	"testing"
)

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

	agree := master.resolveFsmTransition(1, 2, 2)
	if agree.Status != resolutionCorrelated || agree.MachineCount != 2 || agree.FromName != "idle" || agree.ToName != "idle" {
		t.Fatalf("both machines name state 2 the same: %+v", agree)
	}
	differ := master.resolveFsmTransition(1, 2, 3)
	if differ.Status != resolutionUnknown || differ.MachineCount != 2 || differ.FromName != "" || differ.ToName != "" || !strings.Contains(differ.Evidence, "differently") {
		t.Fatalf("machines that disagree on a state name must not name it: %+v", differ)
	}
	none := master.resolveFsmTransition(1, 2, 77)
	if none.Status != resolutionUnknown || none.MachineCount != 0 {
		t.Fatalf("a state that no machine holds under the FsmId must stay unknown: %+v", none)
	}
	if (*gameMaster)(nil).resolveFsmTransition(1, 2, 3) != nil || (&gameMaster{}).resolveFsmTransition(1, 2, 3) != nil {
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
