//go:build wwdev

package main

import (
	"encoding/json"
	"fmt"
)

func (p *processor) writePartReference(frame gameFrame, rpcID uint64, request pendingDamageRequest) {
	if p.partRefOut == nil {
		return
	}
	ordinal := uint64(0)
	if p.frameOrdinal > 0 {
		ordinal = p.frameOrdinal - 1
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
		FieldNumber:   5,
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
		p.partRefErrors++
		fmt.Fprintf(p.diagnostic, "part_reference_encode_error=%v\n", err)
		return
	}
	if _, err := p.partRefOut.Write(append(line, '\n')); err != nil {
		p.partRefErrors++
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
