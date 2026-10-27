package main

// protocolSchema is the field schema extracted offline from the local Windows
// Aki.Protocol metadata schema note.
// private client-derived data and is loaded only when -protocol-schema is set.
// Schema facts are client-version evidence: they say how a field is typed in
// that build, not that the same bytes carry that meaning in a capture build.
type protocolSchema struct {
	SourceSHA256 string                   `json:"sourceSha256"`
	ClientLabel  string                   `json:"clientLabel"`
	Messages     map[string]schemaMessage `json:"messages"`
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
