package capture

// protocolSchema is optional, versioned field metadata used by schema-aware
// analysis. Callers provide it with -protocol-schema; it is not embedded in the
// executable. Schema facts describe one client build and do not prove that the
// same bytes carry that meaning in a capture build.
type protocolSchema struct {
	SourceSHA256 string                   `json:"sourceSha256"`
	ClientLabel  string                   `json:"clientLabel"`
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
