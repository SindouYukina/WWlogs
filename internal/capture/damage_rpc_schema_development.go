//go:build wwdev

package capture

import (
	"encoding/json"
)

// damageRPCPayloads extracts (rpcId, payload) pairs from a damage RPC frame: CombatSendPackRequest.Data[].Request
// (CombatRequestData) for requests, and for responses Data[].CombatResponseData of every CombatReceivePackNotify the frame
// carries, embedded in a CombatSendPackResponse or standalone. The paths are the ones handleDamageRequests and
// handleCombatReceivePacks read.
func damageRPCPayloads(commandID uint16, body []byte) ([]damageRPCItem, error) {
	if commandID != cmdCombatSendPackRequest {
		bodies, err := receivePackNotifyBodies(commandID, body)
		if err != nil {
			return nil, err
		}
		var items []damageRPCItem
		for _, notify := range bodies {
			responses, _ := receivePackDamageResponses(notify)
			for _, response := range responses {
				items = append(items, damageRPCItem{RPCID: response.rpcID, Payload: response.payload})
			}
		}
		return items, nil
	}
	outer, err := parseProtoFields(body)
	if err != nil {
		return nil, err
	}
	var items []damageRPCItem
	for _, field := range outer {
		if field.number != fieldCombatSendPackRequestData || field.wire != 2 {
			continue
		}
		envelopes, err := parseProtoFields(field.data)
		if err != nil {
			return nil, err
		}
		for _, envelope := range envelopes {
			if envelope.number != fieldCombatSendDataRequest || envelope.wire != 2 {
				continue
			}
			fields, err := parseProtoFields(envelope.data)
			if err != nil {
				continue
			}
			// An absent RequestId is the proto3 default 0, as in handleDamageRequests.
			var rpcID uint64
			var payload []byte
			for _, f := range fields {
				if f.number == fieldCombatRequestDataRequestID && f.wire == 0 {
					rpcID = f.value
				}
				if f.number == fieldCombatRequestDataDamageExecuteRequest && f.wire == 2 {
					payload = f.data
				}
			}
			if payload != nil {
				items = append(items, damageRPCItem{RPCID: rpcID, Payload: payload})
			}
		}
	}
	return items, nil
}

// archiveDamageRPCSchema writes schema decodes of damage RPC payloads. It is
// read-only with respect to the typed pipeline and does nothing without a schema
// and an output writer.
func (p *processor) archiveDamageRPCSchema(frame gameFrame, plain []byte) {
	if p.protocolSchema == nil || p.schemaDecodeOut == nil {
		return
	}
	var message string
	switch frame.commandID {
	case cmdCombatSendPackRequest:
		message = "DamageExecuteRequest"
	case cmdCombatSendPackResponse, cmdCombatReceivePackNotify:
		message = "DamageExecuteResponse"
	default:
		return
	}
	items, err := damageRPCPayloads(frame.commandID, plain)
	if err != nil {
		p.schemaDecodeErrors++
		return
	}
	for _, item := range items {
		decoded, err := decodeSchemaMessage(p.protocolSchema, message, item.Payload, 0)
		if err != nil {
			p.schemaDecodeErrors++
			continue
		}
		record := damageRPCSchemaRecord{
			SchemaVersion: schemaDecodeSchemaVersion,
			FrameOrdinal:  p.frameOrdinal - 1,
			SessionNumber: p.sessionNumber,
			Direction:     frame.direction,
			CommandID:     frame.commandID,
			RPCID:         item.RPCID,
			Message:       message,
			Decoded:       decoded,
		}
		line, err := json.Marshal(record)
		if err != nil {
			p.schemaDecodeErrors++
			continue
		}
		if _, err := p.schemaDecodeOut.Write(append(line, '\n')); err != nil {
			p.schemaDecodeErrors++
		}
	}
}
