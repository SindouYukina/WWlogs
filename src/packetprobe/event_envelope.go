package main

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
