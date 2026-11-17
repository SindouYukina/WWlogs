package capture

import (
	"testing"
)

func fsmFixture() *GameMaster {
	fsm := &FSMMaster{
		MachinesByNode: map[int64][]string{1: {"A", "B"}, 2: {"A", "B"}, 3: {"A", "B"}, 9: {"C"}},
		Names: map[FSMNodeKey]string{
			{"A", 1}: "root", {"A", 2}: "idle", {"A", 3}: "fight",
			{"B", 1}: "root", {"B", 2}: "idle", {"B", 3}: "attack",
			{"C", 9}: "lonely",
		},
		Children:   map[FSMNodeKey][]int64{{"A", 1}: {2, 3}, {"B", 1}: {2, 3}},
		References: map[FSMNodeKey]int64{},
	}
	return &GameMaster{FSM: fsm}
}

func TestActiveResponseFsmComponentOnlyAddsTheHashes(t *testing.T) {
	resolver := NewEntityResolver()
	resolver.Entities[77] = RuntimeEntity{EntityID: 77, ComponentSourceMessageID: 5, ComponentSourceFieldPath: "kept"}
	component := appendVarintField(nil, fieldEntityFsmComponentPbHashCode, uint64(uint32(0x59d9c441)))
	component = appendVarintField(component, fieldEntityFsmComponentPbCommonHashCode, uint64(uint32(0x4a3b2c1d)))
	if err := resolver.applyActiveResponseFsmComponents(77, []ProtoField{{Number: fieldEntityComponentPbEntityFsmComponentPb, Wire: 2, Data: component}}); err != nil {
		t.Fatal(err)
	}
	got := resolver.Entities[77]
	if !got.HasFsmHashCode || got.FSMHashCode != 0x59d9c441 || !got.HasFsmCommonHashCode || got.FSMCommonHashCode != 0x4a3b2c1d {
		t.Fatalf("hashes not recorded: %+v", got)
	}
	if got.ComponentSourceMessageID != 5 || got.ComponentSourceFieldPath != "kept" {
		t.Fatalf("identity provenance must stay untouched: %+v", got)
	}
	if err := resolver.applyActiveResponseFsmComponents(999, []ProtoField{{Number: fieldEntityComponentPbEntityFsmComponentPb, Wire: 2, Data: component}}); err != nil {
		t.Fatal(err)
	}
	if _, created := resolver.Entities[999]; created {
		t.Fatal("an unknown entity must not be created from an FSM component")
	}
}
