package capture

import "fmt"

func receivePackNotifyBodies(commandID uint16, body []byte) ([][]byte, error) {
	switch commandID {
	case cmdCombatReceivePackNotify:
		return [][]byte{body}, nil
	case cmdCombatSendPackResponse:
		fields, err := parseProtoFields(body)
		if err != nil {
			return nil, fmt.Errorf("parse CombatSendPackResponse: %w", err)
		}
		var payloads [][]byte
		for _, field := range fields {
			if field.number != fieldCombatSendPackResponseReceivePackNotify {
				continue
			}
			if field.wire != 2 {
				return nil, fmt.Errorf("parse CombatSendPackResponse.ReceivePackNotify: unexpected wire type %d", field.wire)
			}
			payloads = append(payloads, field.data)
		}
		return payloads, nil
	default:
		return nil, nil
	}
}

func (p *processor) handleEmbeddedCombatReceivePackNotify(frame gameFrame, body []byte) error {
	if frame.commandID != cmdCombatSendPackResponse {
		return nil
	}
	payloads, err := receivePackNotifyBodies(frame.commandID, body)
	if err != nil {
		return err
	}
	for _, payload := range payloads {
		if err := p.handleCombatReceivePackNotify(frame, payload); err != nil {
			return err
		}
	}
	return nil
}
