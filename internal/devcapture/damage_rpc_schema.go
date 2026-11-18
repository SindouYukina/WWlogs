package devcapture

import (
	"wwlogs/internal/capture"
)

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
	if commandID != capture.CmdCombatSendPackRequest {
		bodies, err := capture.ReceivePackNotifyBodies(commandID, body)
		if err != nil {
			return nil, err
		}
		var items []damageRPCItem
		for _, notify := range bodies {
			responses, _ := capture.ReceivePackDamageResponses(notify)
			for _, response := range responses {
				items = append(items, damageRPCItem{RPCID: response.RPCID, Payload: response.Payload})
			}
		}
		return items, nil
	}
	outer, err := capture.ParseProtoFields(body)
	if err != nil {
		return nil, err
	}
	var items []damageRPCItem
	for _, field := range outer {
		if field.Number != capture.FieldCombatSendPackRequestData || field.Wire != 2 {
			continue
		}
		envelopes, err := capture.ParseProtoFields(field.Data)
		if err != nil {
			return nil, err
		}
		for _, envelope := range envelopes {
			if envelope.Number != capture.FieldCombatSendDataRequest || envelope.Wire != 2 {
				continue
			}
			fields, err := capture.ParseProtoFields(envelope.Data)
			if err != nil {
				continue
			}
			// An absent RequestId is the proto3 default 0, as in handleDamageRequests.
			var rpcID uint64
			var payload []byte
			for _, f := range fields {
				if f.Number == capture.FieldCombatRequestDataRequestID && f.Wire == 0 {
					rpcID = f.Value
				}
				if f.Number == capture.FieldCombatRequestDataDamageExecuteRequest && f.Wire == 2 {
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
func (p *processor) ArchiveDamageRPCSchema(frame capture.GameFrame, plain []byte) {
	if p.dev.protocolSchema == nil || p.dev.schemaDecodeOut == nil {
		return
	}
	var message string
	switch frame.CommandID {
	case capture.CmdCombatSendPackRequest:
		message = "DamageExecuteRequest"
	case capture.CmdCombatSendPackResponse, capture.CmdCombatReceivePackNotify:
		message = "DamageExecuteResponse"
	default:
		return
	}
	items, err := damageRPCPayloads(frame.CommandID, plain)
	if err != nil {
		p.dev.schemaDecodeErrors++
		return
	}
	for _, item := range items {
		decoded, err := decodeSchemaMessage(p.dev.protocolSchema, message, item.Payload, 0)
		if err != nil {
			p.dev.schemaDecodeErrors++
			continue
		}
		record := damageRPCSchemaRecord{
			SchemaVersion: schemaDecodeSchemaVersion,
			FrameOrdinal:  p.dev.frameOrdinal - 1,
			SessionNumber: p.SessionNumber,
			Direction:     frame.Direction,
			CommandID:     frame.CommandID,
			RPCID:         item.RPCID,
			Message:       message,
			Decoded:       decoded,
		}
		writeResearchRecord(p.dev.schemaDecodeOut, record, &p.dev.schemaDecodeErrors)
	}
}
