package capture

import (
	"regexp"
	"strings"
)

// Current game version only.
//
// WWlogs decodes the protocol of the current official game version and no other. The command and field numbers the
// decoders read are that version's own wire numbers (protocol_numbers.go, generated from the shipped client's
// Protocol.js and NetDefine.js); nothing renumbers a frame at run time. A game update that renumbers the protocol
// replaces those numbers and currentGameVersion together; it never adds a second set.
//
// Every game version so far renumbered its commands and fields, so the numbers of one version read as other fields in
// another. A protocol session is therefore decoded only after it has reported the current game version right after its
// handshake (LoginRequest build or CheckClientPush). Any other version, an unreadable version report, and every frame
// before the report are not decoded and are reported as such; nothing falls back to another numbering or guesses one.

// maxPendingProtocolFrames is how many frames a protocol session may carry before it has reported a client version. In
// every archived session the LoginRequest is the first frame after the handshake, so a session still unidentified after
// this many frames comes from a client whose version report WWlogs cannot read: it is unsupported.
const maxPendingProtocolFrames = 64

type protocolState uint8

const (
	protocolNone        protocolState = iota // no handshake in this capture yet: nothing is decryptable
	protocolPending                          // handshake seen; the client version is not reported yet
	protocolCurrent                          // the session reported the current game version
	protocolUnsupported                      // another game version, or no readable version report
)

// Protocol status values reported in ViewSessionHealth.
const (
	ProtocolStatusNone        = "none"
	ProtocolStatusPending     = "pending"
	ProtocolStatusSupported   = "supported"
	ProtocolStatusUnsupported = "unsupported"
)

// protocolCounters count, over the whole capture, the frames that were not decoded because of the protocol state.
type protocolCounters struct {
	pendingFrames     uint64
	unsupportedFrames uint64
}

// protocolSession is the wire protocol of the current protocol session. Its counters cover the whole capture.
type protocolSession struct {
	state       protocolState
	gameVersion string // major.minor the session reported; empty when not reported
	// sessionPendingFrames counts this protocol session's frames before its version report (maxPendingProtocolFrames).
	sessionPendingFrames uint64
	counters             protocolCounters
}

// CurrentGameVersion is the major.minor game version this build decodes.
func CurrentGameVersion() string {
	return currentGameVersion
}

// CurrentProtocol names the client protocol this build's numbers come from; records that keep what decoded them store it.
func CurrentProtocol() string {
	return currentProtocolClient + " client protocol"
}

// protocolSchemaBasis is the evidence text for a message the decoders read: the current client protocol's numbers,
// applied only after the protocol session reported the current game version.
func protocolSchemaBasis() string {
	return CurrentProtocol() + "; decoded only in a session that reported game version " + currentGameVersion
}

// beginSession starts the protocol of a new protocol session: unknown until the session reports its client version.
func (s *protocolSession) beginSession() {
	s.state, s.gameVersion, s.sessionPendingFrames = protocolPending, "", 0
}

// observeVersion decides the protocol session from the version it reported. Only the first report of a session counts.
func (s *protocolSession) observeVersion(gameVersion string) {
	if s.state != protocolPending || gameVersion == "" {
		return
	}
	s.gameVersion = gameVersion
	if gameVersion == currentGameVersion {
		s.state = protocolCurrent
	} else {
		s.state = protocolUnsupported
	}
}

// readable reports whether a frame of this command can be decoded in the protocol session's current state. The version
// reports themselves are always read, because they decide the session.
func (s *protocolSession) readable(command uint16) bool {
	return command == cmdLoginRequest || command == cmdCheckClientPush || s.state == protocolCurrent
}

// decodes is readable for the frame being handled, and counts the frames the session cannot decode for its state.
func (s *protocolSession) decodes(command uint16) bool {
	if s.readable(command) {
		return true
	}
	switch s.state {
	case protocolPending:
		s.counters.pendingFrames++
		s.sessionPendingFrames++
		if s.sessionPendingFrames >= maxPendingProtocolFrames {
			s.state = protocolUnsupported
		}
	case protocolUnsupported:
		s.counters.unsupportedFrames++
	}
	return false
}

// status is the non-sensitive protocol view for ViewSessionHealth.
func (s *protocolSession) status() (string, string) {
	switch s.state {
	case protocolPending:
		return ProtocolStatusPending, ""
	case protocolCurrent:
		return ProtocolStatusSupported, s.gameVersion
	case protocolUnsupported:
		return ProtocolStatusUnsupported, s.gameVersion
	default:
		return ProtocolStatusNone, ""
	}
}

var gameVersionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.[0-9]+$`)

// gameVersionOf returns major.minor of the first version-shaped token: "OS_Windows_Product_3.8.0_9244589_3.8.9_9436312"
// and "3.8.0" both give "3.8". The first version in a LoginRequest build and in a CheckClientPush is the game version
// the installed client was built for; later ones are resource versions.
func gameVersionOf(text string) string {
	for _, token := range strings.Split(text, "_") {
		if match := gameVersionPattern.FindStringSubmatch(token); match != nil {
			return match[1] + "." + match[2]
		}
	}
	return ""
}
