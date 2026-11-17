//go:build wwdev

package capture

import (
	"encoding/json"
	"testing"
)

const testSchemaJSON = `{
  "messages": {
    "Outer": {"fields": [
      {"number": 5, "name": "Data", "kind": "repeated", "element": {"kind": "class", "type": "Inner"}},
      {"number": 1, "name": "Count", "kind": "int32"}
    ]},
    "Inner": {"fields": [
      {"number": 1, "name": "StackCount", "kind": "int32"},
      {"number": 2, "name": "Handle", "kind": "int32"},
      {"number": 7, "name": "EntityId", "kind": "int64"},
      {"number": 9, "name": "Kind", "kind": "enum", "type": "E"},
      {"number": 15, "name": "Duration", "kind": "float"},
      {"number": 16, "name": "Tags", "kind": "map"}
    ]}
  }
}`

func testSchema(t *testing.T) *protocolSchema {
	t.Helper()
	var schema protocolSchema
	if err := json.Unmarshal([]byte(testSchemaJSON), &schema); err != nil {
		t.Fatalf("unmarshal test schema: %v", err)
	}
	return &schema
}
