package capture

import "fmt"

// A CombatReceivePackNotify reaches the client in two containers: standalone, as its own command, and embedded, as the
// ReceivePackNotify field of a CombatSendPackResponse. Both are the same message. Its Data items carry the server's
// responses to the client's combat requests (CombatResponseData, keyed by the request's RequestId) next to server
// notifications (CombatNotifyData), and the server picks the container per response: in the Global 3.8.9 captures most
// DamageExecuteResponses come in a CombatSendPackResponse and some, answering the same kind of request, in a standalone
// notify; no response came in both (research/runs/2026-11-13/v0.7.0-damage-completeness). Every reader therefore takes
// the bodies of both containers from receivePackNotifyBodies and reads them alike; which container carried a response
// is packaging, not part of the result.

// ReceivePackNotifyBodies returns the CombatReceivePackNotify bodies of one frame: the body itself for a standalone
// notify, every ReceivePackNotify field of a CombatSendPackResponse, and nothing for other commands.
func ReceivePackNotifyBodies(commandID uint16, body []byte) ([][]byte, error) {
	switch commandID {
	case CmdCombatReceivePackNotify:
		return [][]byte{body}, nil
	case CmdCombatSendPackResponse:
		fields, err := ParseProtoFields(body)
		if err != nil {
			return nil, fmt.Errorf("parse CombatSendPackResponse: %w", err)
		}
		var payloads [][]byte
		for _, field := range fields {
			if field.Number != FieldCombatSendPackResponseReceivePackNotify {
				continue
			}
			if field.Wire != 2 {
				return nil, fmt.Errorf("parse CombatSendPackResponse.ReceivePackNotify: unexpected wire type %d", field.Wire)
			}
			payloads = append(payloads, field.Data)
		}
		return payloads, nil
	default:
		return nil, nil
	}
}

// combatDamageResponse is one DamageExecuteResponse of a CombatReceivePackNotify, with the RequestId of the client request
// it answers.
type combatDamageResponse struct {
	RPCID   uint64
	Payload []byte
}

// ReceivePackDamageResponses returns the DamageExecuteResponses one CombatReceivePackNotify body carries in
// Data[].CombatResponseData, in wire order, and how many nested messages it skipped as malformed. An item without a
// RequestId or without a DamageExecuteResponse answers another kind of combat request and is not returned.
func ReceivePackDamageResponses(body []byte) ([]combatDamageResponse, int) {
	batches, err := ParseProtoFields(body)
	if err != nil {
		return nil, 1
	}
	var responses []combatDamageResponse
	malformed := 0
	for _, batch := range batches {
		if batch.Number != FieldCombatReceivePackNotifyData || batch.Wire != 2 { // CombatReceivePackNotify.Data
			continue
		}
		items, err := ParseProtoFields(batch.Data)
		if err != nil {
			malformed++
			continue
		}
		for _, item := range items {
			if item.Number != FieldCombatReceiveDataCombatResponseData || item.Wire != 2 { // CombatReceiveData.CombatResponseData
				continue
			}
			fields, err := ParseProtoFields(item.Data)
			if err != nil {
				malformed++
				continue
			}
			// An absent RequestId is the proto3 default 0: the client's counter sends it once per wrap (combatRequestIDSpace).
			var response combatDamageResponse
			for _, field := range fields {
				if field.Number == FieldCombatResponseDataRequestID && field.Wire == 0 {
					response.RPCID = field.Value
				}
				if field.Number == FieldCombatResponseDataDamageExecuteResponse && field.Wire == 2 {
					response.Payload = field.Data
				}
			}
			if response.Payload != nil {
				responses = append(responses, response)
			}
		}
	}
	return responses, malformed
}

// HandleCombatReceivePacks reads the CombatReceivePackNotify bodies of one frame of either container: first every damage
// response of every body, then every notification, so a frame is read in the same order whichever container the server
// used. A response is paired and deduplicated by its RequestId alone (handleOneDamageResult), never by its container; a
// server-executed DamageExecuteNotify by the frame's sequence and its position among the frame's notifications
// (damage_notify.go).
func (p *Processor) HandleCombatReceivePacks(frame GameFrame, plain []byte) error {
	bodies, err := ReceivePackNotifyBodies(frame.CommandID, plain)
	if err != nil {
		return err
	}
	p.expireHeldResponses()
	p.frameNotifyOrdinal = 0
	for _, body := range bodies {
		responses, malformed := ReceivePackDamageResponses(body)
		p.Stats.ParseErrors += malformed
		for _, response := range responses {
			p.Stats.CandidateResponses++
			if frame.CommandID == CmdCombatReceivePackNotify {
				p.Stats.standaloneNotifyResponses++
			}
			if p.Census != nil {
				p.Census.ObserveInnerEntry("Server->Client DamageExecuteResponse")
			}
			p.HandleOneDamageResult(response.RPCID, response.Payload)
		}
	}
	for _, body := range bodies {
		if err := p.HandleCombatReceivePackNotify(frame, body); err != nil {
			return err
		}
	}
	return nil
}
