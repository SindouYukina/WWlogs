//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"os"
)

func loadProtocolSchema(path string) (*protocolSchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read protocol schema: %w", err)
	}
	var schema protocolSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parse protocol schema: %w", err)
	}
	if len(schema.Messages) == 0 {
		return nil, fmt.Errorf("protocol schema %s has no messages", path)
	}
	return &schema, nil
}

// fieldIndex returns a number-to-field lookup for one message. It is built on
// demand; the schema is small enough that per-message maps are cheap.
func (m schemaMessage) fieldIndex() map[uint64]schemaField {
	index := make(map[uint64]schemaField, len(m.Fields))
	for _, field := range m.Fields {
		index[field.Number] = field
	}
	return index
}

// enumName returns the member name for a value, or false when the schema has no
// such member. Callers keep the raw number in that case.
func (s *protocolSchema) enumName(enum string, value int64) (string, bool) {
	definition, ok := s.Enums[enum]
	if !ok {
		return "", false
	}
	for name, member := range definition.Members {
		if member == value {
			return name, true
		}
	}
	return "", false
}
