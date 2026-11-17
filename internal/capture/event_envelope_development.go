//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// eventEnvelope identifies a combat event independently of its timestamp. The
// identifier is built from session, emitting frame ordinal and emission order
// inside that frame, so the same PCAP and flags reproduce the same ids.
//
// EmitFrameOrdinal is the frame being processed when the event is written. It
// is the response frame for direct damage results, but a backfilled event may
// be written during a later frame, so consumers must not treat it as the source
// frame of that event.
type eventEnvelope struct {
	EventID          string `json:"eventId"`
	EmitFrameOrdinal uint64 `json:"emitFrameOrdinal"`
	EmitOrderInFrame int    `json:"emitOrderInFrame"`
}

// auxEventOrder is the per-frame counter of one auxiliary event namespace.
type auxEventOrder struct {
	frame uint64
	order int
}

// nextEventEnvelope returns the envelope for the next event written in the
// current frame. The order counter resets whenever the frame ordinal changes.
func (p *Processor) nextEventEnvelope() eventEnvelope {
	var frame uint64
	if p.Dev.frameOrdinal > 0 {
		frame = p.Dev.frameOrdinal - 1
	}
	if p.Dev.eventOrderFrame != frame {
		p.Dev.eventOrderFrame = frame
		p.Dev.eventOrder = 0
	}
	order := p.Dev.eventOrder
	p.Dev.eventOrder++
	return eventEnvelope{
		EventID:          fmt.Sprintf("s%d-f%d-e%d", p.SessionNumber, frame, order),
		EmitFrameOrdinal: frame,
		EmitOrderInFrame: order,
	}
}

// nextAuxEventEnvelope numbers events that are not part of the primary event sequence in a namespace of their own
// (one letter per event type: "x" for hits without an amount, "p" for pushed damage), so adding an event type never
// shifts the ids of events consumers already key on.
func (p *Processor) nextAuxEventEnvelope(kind string) eventEnvelope {
	var frame uint64
	if p.Dev.frameOrdinal > 0 {
		frame = p.Dev.frameOrdinal - 1
	}
	if p.Dev.auxEventOrders == nil {
		p.Dev.auxEventOrders = make(map[string]*auxEventOrder)
	}
	counter := p.Dev.auxEventOrders[kind]
	if counter == nil {
		counter = &auxEventOrder{}
		p.Dev.auxEventOrders[kind] = counter
	}
	if counter.frame != frame {
		counter.frame = frame
		counter.order = 0
	}
	order := counter.order
	counter.order++
	return eventEnvelope{
		EventID:          fmt.Sprintf("s%d-f%d-%s%d", p.SessionNumber, frame, kind, order),
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
