package capture

const roleConfigRecordSchemaVersion = 1

// roleConfigRecord preserves the observed RoleId and records its namespace-specific master relation.
type roleConfigRecord struct {
	SchemaVersion           int     `json:"schemaVersion"`
	FrameOrdinal            uint64  `json:"frameOrdinal"`
	SessionNumber           int     `json:"sessionNumber"`
	Direction               string  `json:"direction"`
	CommandID               uint16  `json:"commandId"`
	ConfigIndex             int     `json:"configIndex"`
	IDNamespace             string  `json:"idNamespace"`
	RawRoleID               *int64  `json:"rawRoleId,omitempty"`
	CanonicalRoleID         *uint64 `json:"canonicalRoleId,omitempty"`
	CanonicalRoleName       string  `json:"canonicalRoleName,omitempty"`
	CanonicalRoleNameStatus string  `json:"canonicalRoleNameStatus"`
	Language                string  `json:"language,omitempty"`
	Status                  string  `json:"status"`
	Basis                   string  `json:"basis"`
	Evidence                string  `json:"evidence"`
}

type roleConfigRoleResolution struct {
	CanonicalRoleID uint64
	Character       masterRecord
	Status          string
	Basis           string
}
