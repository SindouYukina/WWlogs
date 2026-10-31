package capture

// damageRPCSchemaRecord is one schema-decoded DamageExecuteRequest or
// DamageExecuteResponse payload. RPCID comes from the wrapper that carries the
// payload, so typed damage events can be joined to it exactly.
type damageRPCSchemaRecord struct {
	SchemaVersion int             `json:"schemaVersion"`
	FrameOrdinal  uint64          `json:"frameOrdinal"`
	SessionNumber int             `json:"sessionNumber"`
	Direction     string          `json:"direction"`
	CommandID     uint16          `json:"commandId"`
	RPCID         uint64          `json:"rpcId"`
	Message       string          `json:"message"`
	Decoded       *decodedMessage `json:"decoded"`
}

type damageRPCItem struct {
	RPCID   uint64
	Payload []byte
}
