//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"os"
)

// protocolSchema is optional, versioned field metadata used by schema-aware
// analysis. Callers provide it with -protocol-schema; it is not embedded in the
// executable. Its numbers must be those of the current game version, because
// frames are read with their wire numbers; GameVersion says which version that is.
// Schema facts describe one client build and do not prove that the same bytes
// carry that meaning in a capture build.
type protocolSchema struct {
	SourceSHA256 string                   `json:"sourceSha256"`
	ClientLabel  string                   `json:"clientLabel"`
	GameVersion  string                   `json:"gameVersion"`
	Messages     map[string]schemaMessage `json:"messages"`
	Enums        map[string]schemaEnum    `json:"enums"`
}

type schemaMessage struct {
	Namespace string        `json:"namespace"`
	Fields    []schemaField `json:"fields"`
}

type schemaField struct {
	Number uint64 `json:"number"`
	Name   string `json:"name"`
	schemaType
}

// schemaType mirrors the kinds written by the extractor. Kind values are
// int32, int64, uint32, uint64, bool, float, double, string, bytes, enum,
// class, repeated, or map.
type schemaType struct {
	Kind    string      `json:"kind"`
	Type    string      `json:"type,omitempty"`
	Element *schemaType `json:"element,omitempty"`
	Key     *schemaType `json:"key,omitempty"`
	Value   *schemaType `json:"value,omitempty"`
}

// schemaEnum holds the member values of one protobuf enum from the schema.
type schemaEnum struct {
	Members map[string]int64 `json:"members"`
}

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
	// A schema of another game version numbers the same names differently; reading frames with it would misdecode them.
	if schema.GameVersion != currentGameVersion {
		return nil, fmt.Errorf("protocol schema %s describes game version %q; this build decodes %q", path, schema.GameVersion, currentGameVersion)
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
