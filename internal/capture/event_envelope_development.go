//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// nextEventEnvelope returns the envelope for the next event written in the
// current frame. The order counter resets whenever the frame ordinal changes.
func (p *processor) nextEventEnvelope() eventEnvelope {
	var frame uint64
	if p.dev.frameOrdinal > 0 {
		frame = p.dev.frameOrdinal - 1
	}
	if p.dev.eventOrderFrame != frame {
		p.dev.eventOrderFrame = frame
		p.dev.eventOrder = 0
	}
	order := p.dev.eventOrder
	p.dev.eventOrder++
	return eventEnvelope{
		EventID:          fmt.Sprintf("s%d-f%d-e%d", p.sessionNumber, frame, order),
		EmitFrameOrdinal: frame,
		EmitOrderInFrame: order,
	}
}

// nextAuxEventEnvelope numbers events that are not part of the primary event sequence in a namespace of their own
// (one letter per event type: "x" for hits without an amount, "p" for pushed damage), so adding an event type never
// shifts the ids of events consumers already key on.
func (p *processor) nextAuxEventEnvelope(kind string) eventEnvelope {
	var frame uint64
	if p.dev.frameOrdinal > 0 {
		frame = p.dev.frameOrdinal - 1
	}
	if p.dev.auxEventOrders == nil {
		p.dev.auxEventOrders = make(map[string]*auxEventOrder)
	}
	counter := p.dev.auxEventOrders[kind]
	if counter == nil {
		counter = &auxEventOrder{}
		p.dev.auxEventOrders[kind] = counter
	}
	if counter.frame != frame {
		counter.frame = frame
		counter.order = 0
	}
	order := counter.order
	counter.order++
	return eventEnvelope{
		EventID:          fmt.Sprintf("s%d-f%d-%s%d", p.sessionNumber, frame, kind, order),
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
