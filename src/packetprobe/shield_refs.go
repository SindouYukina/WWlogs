package main

const shieldRefSchemaVersion = 1

// shieldConfigRefRecord links one typed ShieldUpdateInfo.ConfigId to the normalized shield table.
type shieldConfigRefRecord struct {
	SchemaVersion          int              `json:"schemaVersion"`
	FrameOrdinal           uint64           `json:"frameOrdinal"`
	SessionNumber          int              `json:"sessionNumber"`
	Timestamp              string           `json:"timestamp"`
	FrameToken             uint16           `json:"frameToken"`
	OuterCommandID         uint16           `json:"outerCommandId"`
	Direction              string           `json:"direction"`
	FieldPath              string           `json:"fieldPath"`
	ShieldIndex            int              `json:"shieldIndex"`
	Handle                 *int64           `json:"handle,omitempty"`
	ConfigID               *int64           `json:"configId,omitempty"`
	ShieldValue            *int64           `json:"shieldValue,omitempty"`
	UpdateType             *int64           `json:"updateType,omitempty"`
	ShieldDefinitionStatus resolutionStatus `json:"shieldDefinitionStatus"`
	ShieldDefinitionTable  string           `json:"shieldDefinitionTable,omitempty"`
	ShieldDefinitionID     *uint64          `json:"shieldDefinitionId,omitempty"`
	Evidence               string           `json:"evidence"`
}
