//go:build wwdev

package capture

import ()

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

// damageRPCPayloads extracts (rpcId, payload) pairs from a damage RPC frame: CombatSendPackRequest.Data[].Request
// (CombatRequestData) for requests, and for responses Data[].CombatResponseData of every CombatReceivePackNotify the frame
// carries, embedded in a CombatSendPackResponse or standalone. The paths are the ones handleDamageRequests and
// handleCombatReceivePacks read.
func damageRPCPayloads(commandID uint16, body []byte) ([]damageRPCItem, error) {
	if commandID != CmdCombatSendPackRequest {
		bodies, err := ReceivePackNotifyBodies(commandID, body)
		if err != nil {
			return nil, err
		}
		var items []damageRPCItem
		for _, notify := range bodies {
			responses, _ := ReceivePackDamageResponses(notify)
			for _, response := range responses {
				items = append(items, damageRPCItem{RPCID: response.RPCID, Payload: response.Payload})
			}
		}
		return items, nil
	}
	outer, err := ParseProtoFields(body)
	if err != nil {
		return nil, err
	}
	var items []damageRPCItem
	for _, field := range outer {
		if field.Number != FieldCombatSendPackRequestData || field.Wire != 2 {
			continue
		}
		envelopes, err := ParseProtoFields(field.Data)
		if err != nil {
			return nil, err
		}
		for _, envelope := range envelopes {
			if envelope.Number != FieldCombatSendDataRequest || envelope.Wire != 2 {
				continue
			}
			fields, err := ParseProtoFields(envelope.Data)
			if err != nil {
				continue
			}
			// An absent RequestId is the proto3 default 0, as in handleDamageRequests.
			var rpcID uint64
			var payload []byte
			for _, f := range fields {
				if f.Number == FieldCombatRequestDataRequestID && f.Wire == 0 {
					rpcID = f.Value
				}
				if f.Number == FieldCombatRequestDataDamageExecuteRequest && f.Wire == 2 {
					payload = f.Data
				}
			}
			if payload != nil {
				items = append(items, damageRPCItem{RPCID: rpcID, Payload: payload})
			}
		}
	}
	return items, nil
}

// ArchiveDamageRPCSchema writes schema decodes of damage RPC payloads. It is
// read-only with respect to the typed pipeline and does nothing without a schema
// and an output writer.
func (p *Processor) ArchiveDamageRPCSchema(frame GameFrame, plain []byte) {
	if p.Dev.protocolSchema == nil || p.Dev.schemaDecodeOut == nil {
		return
	}
	var message string
	switch frame.CommandID {
	case CmdCombatSendPackRequest:
		message = "DamageExecuteRequest"
	case CmdCombatSendPackResponse, CmdCombatReceivePackNotify:
		message = "DamageExecuteResponse"
	default:
		return
	}
	items, err := damageRPCPayloads(frame.CommandID, plain)
	if err != nil {
		p.Dev.schemaDecodeErrors++
		return
	}
	for _, item := range items {
		decoded, err := decodeSchemaMessage(p.Dev.protocolSchema, message, item.Payload, 0)
		if err != nil {
			p.Dev.schemaDecodeErrors++
			continue
		}
		record := damageRPCSchemaRecord{
			SchemaVersion: schemaDecodeSchemaVersion,
			FrameOrdinal:  p.Dev.frameOrdinal - 1,
			SessionNumber: p.SessionNumber,
			Direction:     frame.Direction,
			CommandID:     frame.CommandID,
			RPCID:         item.RPCID,
			Message:       message,
			Decoded:       decoded,
		}
		writeResearchRecord(p.Dev.schemaDecodeOut, record, &p.Dev.schemaDecodeErrors)
	}
}
