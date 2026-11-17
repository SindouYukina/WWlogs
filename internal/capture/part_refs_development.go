//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
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

func (p *processor) writePartReference(frame gameFrame, rpcID uint64, request pendingDamageRequest) {
	if p.dev.partRefOut == nil {
		return
	}
	ordinal := uint64(0)
	if p.dev.frameOrdinal > 0 {
		ordinal = p.dev.frameOrdinal - 1
	}
	reference := runtimePartReference{
		status:   partReferenceUnknown,
		evidence: "DamageExecuteRequest.PartId field is absent; no value or PartInformation row was inferred",
	}
	if request.hasPartID {
		targetEntityID := uint64(0)
		if request.hasTargetEntityID {
			targetEntityID = request.targetEntityID
		}
		reference = p.entities.resolvePartReference(targetEntityID, request.partID)
	}
	row := partReferenceRecord{
		SchemaVersion: partReferenceSchemaVersion,
		EventType:     "DAMAGE_REQUEST_PART_REFERENCE",
		FrameOrdinal:  ordinal,
		SessionNumber: p.sessionNumber,
		Timestamp:     frame.timestamp,
		Direction:     frame.direction,
		CommandID:     frame.commandID,
		RPCID:         rpcID,
		FieldPath:     "DamageExecuteRequest.PartId",
		FieldNumber:   fieldDamageExecuteRequestPartID,
		TargetEntityID: optionalUint64(
			request.targetEntityID,
			request.hasTargetEntityID,
		),
		PartID:    optionalInt32(request.partID, request.hasPartID),
		HasPartID: request.hasPartID,
		Status:    reference.status,
		Evidence:  reference.evidence,
	}
	if reference.information != nil {
		information := reference.information
		row.PartInformation = &partInformationRecord{
			PartID:    information.partID,
			PartTag:   optionalInt32(information.partTag, information.hasPartTag),
			LifeMax:   optionalFloat32(information.lifeMax, information.hasLifeMax),
			Activated: optionalBool(information.activated, information.hasActivated),
		}
		row.PartInformationSource = reference.informationFrom
	}
	line, err := json.Marshal(row)
	if err != nil {
		p.dev.partRefErrors++
		fmt.Fprintf(p.diagnostic, "part_reference_encode_error=%v\n", err)
		return
	}
	if _, err := p.dev.partRefOut.Write(append(line, '\n')); err != nil {
		p.dev.partRefErrors++
		fmt.Fprintf(p.diagnostic, "part_reference_write_error=%v\n", err)
	}
}

func optionalFloat32(value float32, present bool) *float32 {
	if !present {
		return nil
	}
	return &value
}

func optionalBool(value bool, present bool) *bool {
	if !present {
		return nil
	}
	return &value
}
