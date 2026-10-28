package main

// hpStateSchemaVersion identifies the layout of hpStateRecord.
const hpStateSchemaVersion = 1

// Attribute types that carry HP state in the Windows 3.7.1 schema. The values
// come from EAttributeType: LifeMax is 2 and Life is 3.
const (
	attributeTypeLifeMax = 2
	attributeTypeLife    = 3
)

// hpStateRecord is one observed HP attribute value on one entity. A proto3
// field that is absent carries the default zero, so both values are always
// present in the record. BaseValue is the field the observed build writes the
// Life value into; CurrentValue is kept as observed.
type hpStateRecord struct {
	SchemaVersion   int    `json:"schemaVersion"`
	FrameOrdinal    uint64 `json:"frameOrdinal"`
	SessionNumber   int    `json:"sessionNumber"`
	Timestamp       string `json:"timestamp"`
	EntityID        uint64 `json:"entityId"`
	AttributeType   int64  `json:"attributeType"`
	AttributeName   string `json:"attributeName,omitempty"`
	BaseValue       int64  `json:"baseValue"`
	CurrentValue    int64  `json:"currentValue"`
	EventOrderInMsg int    `json:"orderInMessage"`
}
