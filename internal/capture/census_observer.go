package capture

import "time"

// commandCensusObserver receives what the frame parser and the frame dispatcher learn about every frame: framing
// failures, each frame's command, and how far the frame was decoded. The Development command census
// (-command-census-out) implements it; a processor without a census (every Public run, and every Development run
// without the flag) skips each report. Nothing it returns reaches a core decision: FailedCRC returns the census's own
// index of the observation, which the parser hands back with the next valid frame boundary.
type commandCensusObserver interface {
	Malformed(reason string, timestamp time.Time, offset int, declaredLength uint16, available int)
	FailedCRC(observation crcFailureObservation) int
	SetNextValidFrame(index int, next frameBoundaryObservation)
	ObserveFrame(frame gameFrame, session int) censusFrameObserver
	ObserveSession(session int)
	ObserveInnerEntry(name string)
	ObserveCombatNotifyField(fieldNumber uint64)
}

// censusFrameObserver is the census entry of one frame's command: how far the dispatcher got with the frame. A frame
// that no decoder reads is the census's to inspect (InspectUndecoded); the core never decrypts it.
type censusFrameObserver interface {
	Fail(reason string)
	Decrypted()
	Decoded()
	DescribeCurrentProtocol()
	ObserveKnownBodyWire(plain []byte)
	InspectUndecoded(frame gameFrame, decodable bool)
}

type crcFailureObservation struct {
	Direction      string                    `json:"direction"`
	Timestamp      time.Time                 `json:"timestamp"`
	Offset         int                       `json:"applicationOffset"`
	MessageType    byte                      `json:"messageType"`
	Sequence       uint32                    `json:"sequence"`
	Token          uint16                    `json:"token,omitempty"`
	CommandID      uint16                    `json:"commandId"`
	DeclaredLength uint16                    `json:"declaredLength"`
	FrameLength    int                       `json:"frameLength"`
	BodyLength     int                       `json:"bodyLength"`
	ReceivedCRC    uint32                    `json:"receivedCRC32"`
	ComputedCRC    uint32                    `json:"computedCRC32"`
	RemainingBytes int                       `json:"remainingApplicationBytes"`
	PreviousValid  *frameBoundaryObservation `json:"previousValidFrame,omitempty"`
}

type frameBoundaryObservation struct {
	Offset      int    `json:"applicationOffset"`
	MessageType byte   `json:"messageType"`
	Sequence    uint32 `json:"sequence"`
	Token       uint16 `json:"token,omitempty"`
	CommandID   uint16 `json:"commandId"`
	FrameLength int    `json:"frameLength"`
}
