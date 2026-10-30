//go:build wwdev

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// nextEventEnvelope returns the envelope for the next event written in the
// current frame. The order counter resets whenever the frame ordinal changes.
func (p *processor) nextEventEnvelope() eventEnvelope {
	var frame uint64
	if p.frameOrdinal > 0 {
		frame = p.frameOrdinal - 1
	}
	if p.eventOrderFrame != frame {
		p.eventOrderFrame = frame
		p.eventOrder = 0
	}
	order := p.eventOrder
	p.eventOrder++
	return eventEnvelope{
		EventID:          fmt.Sprintf("s%d-f%d-e%d", p.sessionNumber, frame, order),
		EmitFrameOrdinal: frame,
		EmitOrderInFrame: order,
	}
}

// nextAuxEventEnvelope numbers events that are not part of the primary event sequence in a separate namespace
// ("x"), so adding such an event type never shifts the ids of the events consumers already key on.
func (p *processor) nextAuxEventEnvelope() eventEnvelope {
	var frame uint64
	if p.frameOrdinal > 0 {
		frame = p.frameOrdinal - 1
	}
	if p.auxEventOrderFrame != frame {
		p.auxEventOrderFrame = frame
		p.auxEventOrder = 0
	}
	order := p.auxEventOrder
	p.auxEventOrder++
	return eventEnvelope{
		EventID:          fmt.Sprintf("s%d-f%d-x%d", p.sessionNumber, frame, order),
		EmitFrameOrdinal: frame,
		EmitOrderInFrame: order,
	}
}

// prependEnvelope merges the envelope fields in front of an already encoded JSON
// object. Encoding the envelope separately keeps every event type's own struct
// unchanged, so the Buff snapshot and lifecycle files stay byte-compatible.
func prependEnvelope(envelope eventEnvelope, event any) ([]byte, error) {
	head, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(body) <= 2 {
		return append(head, '\n'), nil
	}
	var line bytes.Buffer
	line.Write(head[:len(head)-1])
	line.WriteByte(',')
	line.Write(body[1:])
	line.WriteByte('\n')
	return line.Bytes(), nil
}
