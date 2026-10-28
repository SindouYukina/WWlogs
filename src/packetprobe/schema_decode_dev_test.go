//go:build wwdev

package main

import (
	"strings"
	"testing"
)

// Command 103 carries account and device identifiers. Schema decoding must not
// write it even when the schema names its message.
func TestSchemaDecodeExcludesLoginIdentifiers(t *testing.T) {
	schema := &protocolSchema{Messages: map[string]schemaMessage{
		"LoginRequest": {Fields: []schemaField{{Number: 2, Name: "Account", schemaType: schemaType{Kind: "string"}}}},
	}}
	var out strings.Builder
	p := &processor{protocolSchema: schema, schemaDecodeOut: &out}
	p.archiveSchemaDecode(gameFrame{commandID: 103, direction: "out", body: []byte{0x12, 0x03, 'a', 'b', 'c'}}, []byte{0x12, 0x03, 'a', 'b', 'c'})
	if out.Len() != 0 {
		t.Fatalf("command 103 wrote %d bytes of schema output", out.Len())
	}
	if p.schemaDecodeSkipped != 1 {
		t.Fatalf("skipped = %d, want 1", p.schemaDecodeSkipped)
	}
}

func TestSchemaMessageNameDropsCensusAnnotation(t *testing.T) {
	cases := map[string]string{
		"EntityAddNotify (candidate)":     "EntityAddNotify",
		"CombatRequest (outer transport)": "CombatRequest",
		"LoginRequest":                    "LoginRequest",
	}
	for in, want := range cases {
		if got := schemaMessageName(in); got != want {
			t.Fatalf("schemaMessageName(%q) = %q, want %q", in, got, want)
		}
	}
}
