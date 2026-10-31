package capture

import (
	"time"

	"wwlogs/internal/versioninfo"
)

var combatNotifyAuditSchemaBasis = versioninfo.WindowsSchemaLabel() + " static schema; capture-build compatibility unverified"

var combatNotifyAuditNames = map[uint64]string{
	4:  "DamageExecuteNotify",
	5:  "ApplyGameplayEffectNotify",
	6:  "RemoveGameplayEffectNotify",
	22: "AttributeChangedNotify",
	29: "ActivateBuffNotify",
	38: "ChangeStateNotify",
	53: "ApplyBuffS2CRequestNotify",
	58: "RecoverPropChangedNotify",
	59: "RemoveBuffByIdS2CRequestNotify",
	87: "SquadPropChangedNotify",
}

type combatNotifyFieldShape struct {
	Number    uint64 `json:"fieldNumber"`
	WireType  uint64 `json:"wireType"`
	Count     int    `json:"count"`
	ByteCount int    `json:"byteCount,omitempty"`
}

type combatNotifyNumericValue struct {
	FieldNumber uint64   `json:"fieldNumber"`
	WireType    uint64   `json:"wireType"`
	Unsigned    *uint64  `json:"unsignedValue,omitempty"`
	Float       *float32 `json:"float32Value,omitempty"`
}

type combatNotifyNestedRecord struct {
	FieldNumber uint64                     `json:"fieldNumber"`
	SchemaName  string                     `json:"schemaName,omitempty"`
	FieldShapes []combatNotifyFieldShape   `json:"fieldShapes"`
	Values      []combatNotifyNumericValue `json:"values,omitempty"`
}

type combatNotifyAuditRow struct {
	SchemaVersion       int                        `json:"schemaVersion"`
	EventType           string                     `json:"eventType"`
	SessionNumber       int                        `json:"sessionNumber"`
	Timestamp           time.Time                  `json:"timestamp"`
	OuterCommandID      uint16                     `json:"outerCommandId"`
	Direction           string                     `json:"direction"`
	InnerFieldNumber    uint64                     `json:"innerFieldNumber"`
	InnerMessageName    string                     `json:"innerMessageName,omitempty"`
	SchemaBasis         string                     `json:"schemaBasis"`
	CombatCommonEntity  *uint64                    `json:"combatCommonEntityId,omitempty"`
	CombatCommonSource  *uint64                    `json:"combatCommonOriginatorId,omitempty"`
	CombatCommonMessage *uint64                    `json:"combatCommonMessageId,omitempty"`
	FieldShapes         []combatNotifyFieldShape   `json:"fieldShapes"`
	NestedRecords       []combatNotifyNestedRecord `json:"nestedRecords,omitempty"`
	DecodeStatus        string                     `json:"decodeStatus"`
}
