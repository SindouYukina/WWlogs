package main

import (
	"time"
)

const partReferenceSchemaVersion = 1

type partReferenceRecord struct {
	SchemaVersion         int                    `json:"schemaVersion"`
	EventType             string                 `json:"eventType"`
	FrameOrdinal          uint64                 `json:"frameOrdinal"`
	SessionNumber         int                    `json:"sessionNumber"`
	Timestamp             time.Time              `json:"timestamp"`
	Direction             string                 `json:"direction"`
	CommandID             uint16                 `json:"commandId"`
	RPCID                 uint64                 `json:"rpcId"`
	FieldPath             string                 `json:"fieldPath"`
	FieldNumber           uint64                 `json:"fieldNumber"`
	TargetEntityID        *uint64                `json:"targetEntityId,omitempty"`
	PartID                *int32                 `json:"partId,omitempty"`
	HasPartID             bool                   `json:"hasPartId"`
	Status                partReferenceStatus    `json:"status"`
	PartInformation       *partInformationRecord `json:"partInformation,omitempty"`
	PartInformationSource string                 `json:"partInformationSource,omitempty"`
	Evidence              string                 `json:"evidence"`
}

type partInformationRecord struct {
	PartID    int32    `json:"partId"`
	PartTag   *int32   `json:"partTag,omitempty"`
	LifeMax   *float32 `json:"lifeMax,omitempty"`
	Activated *bool    `json:"activated,omitempty"`
}
