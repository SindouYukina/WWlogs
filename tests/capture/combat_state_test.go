package capture

import (
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

func TestActiveResponseFsmComponentOnlyAddsTheHashes(t *testing.T) {
	resolver := newEntityResolver()
	resolver.entities[77] = runtimeEntity{entityID: 77, componentSourceMessageID: 5, componentSourceFieldPath: "kept"}
	component := appendVarintField(nil, fieldEntityFsmComponentPbHashCode, uint64(uint32(0x59d9c441)))
	component = appendVarintField(component, fieldEntityFsmComponentPbCommonHashCode, uint64(uint32(0x4a3b2c1d)))
	if err := resolver.applyActiveResponseFsmComponents(77, []protoField{{number: fieldEntityComponentPbEntityFsmComponentPb, wire: 2, data: component}}); err != nil {
		t.Fatal(err)
	}
	got := resolver.entities[77]
	if !got.hasFsmHashCode || got.fsmHashCode != 0x59d9c441 || !got.hasFsmCommonHashCode || got.fsmCommonHashCode != 0x4a3b2c1d {
		t.Fatalf("hashes not recorded: %+v", got)
	}
	if got.componentSourceMessageID != 5 || got.componentSourceFieldPath != "kept" {
		t.Fatalf("identity provenance must stay untouched: %+v", got)
	}
	if err := resolver.applyActiveResponseFsmComponents(999, []protoField{{number: fieldEntityComponentPbEntityFsmComponentPb, wire: 2, data: component}}); err != nil {
		t.Fatal(err)
	}
	if _, created := resolver.entities[999]; created {
		t.Fatal("an unknown entity must not be created from an FSM component")
	}
}
