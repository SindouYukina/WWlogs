//go:build wwdev

package main

import (
	"encoding/json"
)

// damageRPCPayloads extracts (rpcId, payload) pairs from a damage RPC wrapper.
// Request wrappers (19470) carry one item per field 3. Response wrappers (20332)
// carry batches in field 5, each with items in field 3. Field numbers follow the
// typed pipeline in handleDamageRequests and handleDamageResponses.
func damageRPCPayloads(commandID uint16, body []byte) ([]damageRPCItem, error) {
	outer, err := parseProtoFields(body)
	if err != nil {
		return nil, err
	}
	var items []damageRPCItem
	for _, field := range outer {
		if field.number != 14 || field.wire != 2 {
			continue
		}
		var containers [][]byte
		if commandID == 19470 {
			envelopes, err := parseProtoFields(field.data)
			if err != nil {
				return nil, err
			}
			for _, envelope := range envelopes {
				if envelope.number == 3 && envelope.wire == 2 {
					containers = append(containers, envelope.data)
				}
			}
		} else {
			batches, err := parseProtoFields(field.data)
			if err != nil {
				return nil, err
			}
			for _, batch := range batches {
				if batch.number != 5 || batch.wire != 2 {
					continue
				}
				entries, err := parseProtoFields(batch.data)
				if err != nil {
					return nil, err
				}
				for _, entry := range entries {
					if entry.number == 3 && entry.wire == 2 {
						containers = append(containers, entry.data)
					}
				}
			}
		}
		for _, container := range containers {
			fields, err := parseProtoFields(container)
			if err != nil {
				continue
			}
			var rpcID uint64
			var hasRPC bool
			var payload []byte
			for _, f := range fields {
				if f.number == 2 && f.wire == 0 {
					rpcID, hasRPC = f.value, true
				}
				if f.number == 5 && f.wire == 2 {
					payload = f.data
				}
			}
			if hasRPC && payload != nil {
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
	case 19470:
		message = "DamageExecuteRequest"
	case 20332:
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
