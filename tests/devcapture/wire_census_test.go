package devcapture

import (
	"testing"

	"wwlogs/internal/capture"
)

func TestTopLevelFieldCountsUseWireFactsOnly(t *testing.T) {
	// field 1 varint 150, field 2 length-delimited "ab", field 5 fixed32.
	body := []byte{0x08, 0x96, 0x01, 0x12, 0x02, 'a', 'b', 0x2d, 1, 2, 3, 4}
	fields, err := capture.ParseProtoFields(body)
	if err != nil {
		t.Fatalf("parseProtoFields: %v", err)
	}
	var entry commandCensusEntry
	entry.observeTopLevelFields(fields)
	want := map[string]int{"1/w0": 1, "2/w2": 1, "5/w5": 1}
	if len(entry.TopLevelFields) != len(want) {
		t.Fatalf("keys = %v, want %v", entry.TopLevelFields, want)
	}
	for key, count := range want {
		if entry.TopLevelFields[key] != count {
			t.Fatalf("%s = %d, want %d", key, entry.TopLevelFields[key], count)
		}
	}
}

func TestTopLevelFieldCountsAreBounded(t *testing.T) {
	var fields []capture.ProtoField
	for number := uint64(1); number <= maxTopLevelFieldKeys+6; number++ {
		fields = append(fields, capture.ProtoField{Number: number, Wire: 0})
	}
	var entry commandCensusEntry
	entry.observeTopLevelFields(fields)
	if len(entry.TopLevelFields) != maxTopLevelFieldKeys {
		t.Fatalf("distinct keys = %d, want %d", len(entry.TopLevelFields), maxTopLevelFieldKeys)
	}
	if entry.TopLevelFieldOverflow != 6 {
		t.Fatalf("overflow = %d, want 6", entry.TopLevelFieldOverflow)
	}
}
