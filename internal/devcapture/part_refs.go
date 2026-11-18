package devcapture

import (
	"encoding/json"
	"fmt"
	"time"

	"wwlogs/internal/capture"
)

type runtimePartReference struct {
	partID          int32
	status          partReferenceStatus
	information     *capture.RuntimePartInformation
	informationFrom string
	evidence        string
}

type partReferenceStatus string

const (
	partReferenceCorrelated partReferenceStatus = "CORRELATED"
	partReferenceNoPart     partReferenceStatus = "NO_PART"
	partReferenceUnknown    partReferenceStatus = "UNKNOWN"
)

func resolvePartReference(r *capture.EntityResolver, entityID uint64, partID int32) runtimePartReference {
	result := runtimePartReference{partID: partID, status: partReferenceUnknown}
	if partID == -1 {
		result.status = partReferenceNoPart
		result.evidence = "DamageExecuteRequest.PartId=-1 is the observed no-part sentinel; raw value preserved and no PartInformation lookup attempted"
		return result
	}
	if partID <= 0 {
		result.evidence = "Non-positive DamageExecuteRequest.PartId is preserved without assigning a PartInformation row"
		return result
	}
	entity, ok := r.Entities[entityID]
	if !ok || !entity.HasPartComponent {
		result.evidence = "No current PartComponent observation is available for the exact target entity in this session"
		return result
	}
	information, ok := entity.PartInformation[partID]
	if !ok {
		result.evidence = "Current target PartComponent has no PartLifeInfos row with this exact PartId"
		return result
	}
	if information.Conflicted {
		result.evidence = "Current target PartComponent has conflicting PartLifeInfos rows for this PartId"
		return result
	}
	result.status = partReferenceCorrelated
	result.information = &information
	result.informationFrom = entity.PartComponentSource
	result.evidence = "DamageExecuteRequest.PartId exactly matches PartComponent.PartLifeInfos.PartId observed for the same TargetEntityId before this request; this is a session-scoped runtime relation, not a static Master key"
	return result
}

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

func (p *processor) WritePartReference(frame capture.GameFrame, rpcID uint64, request capture.PendingDamageRequest) {
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
	if request.HasPartID {
		targetEntityID := uint64(0)
		if request.HasTargetEntityID {
			targetEntityID = request.TargetEntityID
		}
		reference = resolvePartReference(p.Entities, targetEntityID, request.PartID)
	}
	row := partReferenceRecord{
		SchemaVersion: partReferenceSchemaVersion,
		EventType:     "DAMAGE_REQUEST_PART_REFERENCE",
		FrameOrdinal:  ordinal,
		SessionNumber: p.SessionNumber,
		Timestamp:     frame.Timestamp,
		Direction:     frame.Direction,
		CommandID:     frame.CommandID,
		RPCID:         rpcID,
		FieldPath:     "DamageExecuteRequest.PartId",
		FieldNumber:   capture.FieldDamageExecuteRequestPartID,
		TargetEntityID: optionalUint64(
			request.TargetEntityID,
			request.HasTargetEntityID,
		),
		PartID:    optionalInt32(request.PartID, request.HasPartID),
		HasPartID: request.HasPartID,
		Status:    reference.status,
		Evidence:  reference.evidence,
	}
	if reference.information != nil {
		information := reference.information
		row.PartInformation = &partInformationRecord{
			PartID:    information.PartID,
			PartTag:   optionalInt32(information.PartTag, information.HasPartTag),
			LifeMax:   optionalFloat32(information.LifeMax, information.HasLifeMax),
			Activated: optionalBool(information.Activated, information.HasActivated),
		}
		row.PartInformationSource = reference.informationFrom
	}
	line, err := json.Marshal(row)
	if err != nil {
		p.dev.partRefErrors++
		fmt.Fprintf(p.Diagnostic, "part_reference_encode_error=%v\n", err)
		return
	}
	if _, err := p.dev.partRefOut.Write(append(line, '\n')); err != nil {
		p.dev.partRefErrors++
		fmt.Fprintf(p.Diagnostic, "part_reference_write_error=%v\n", err)
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
