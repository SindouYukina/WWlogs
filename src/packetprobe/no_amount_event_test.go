package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeJSONLLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}
