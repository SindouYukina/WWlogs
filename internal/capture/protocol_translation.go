package capture

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Current game version only.
//
// WWlogs decodes the protocol of the current official game version and no other. The decoders read one fixed internal
// numbering of commands and fields; it is their own vocabulary (it was taken from Global 3.7 when they were written), not
// a protocol WWlogs accepts on the wire. Every frame of the current game version is translated into it by one generated
// table, protocol_translation_<version>.json, built offline from the shipped client's own Protocol.js and NetDefine.js:
// each game version renumbers commands and fields but keeps the client's message and property names, so the table maps
// numbers by name. A translated body is re-encoded with internal field numbers; fields the decoders have no number for
// are dropped, because no decoder reads them. A game update replaces the table; it never adds a second one.
//
// A protocol session is decoded only after it has reported the current game version right after its handshake
// (LoginRequest build or CheckClientPush). Any other version, an unreadable version report, and every frame before the
// report are not decoded and are reported as such; nothing falls back to an older numbering or guesses one.

//go:embed protocol_translation_3.8.json
var currentProtocolTable []byte

// internalNumbering names the numbering the decoders read; a translation table must target it.
const internalNumbering = "global-3.7"

// Commands the current protocol shares with the internal numbering: the key exchange and the two version reports, which
// must be readable before the session's version is known. Loading the table checks that each maps to itself.
const (
	protoKeyResponseCommand = 112
	loginRequestCommand     = 103
	checkClientPushCommand  = 116
)

// maxPendingProtocolFrames is how many frames a protocol session may carry before it has reported a client version. In
// every archived session the LoginRequest is the first frame after the handshake, so a session still unidentified after
// this many frames comes from a client whose version report WWlogs cannot read: it is unsupported.
const maxPendingProtocolFrames = 64

// maxTranslationDepth bounds nested message recursion; the deepest path the decoders reach is far shallower.
const maxTranslationDepth = 48

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

type translatedField struct {
	internal uint64 // 0: the decoders have no number for this field and it is dropped
	wire     uint64
	packed   bool               // a repeated numeric scalar, which may also arrive length-delimited
	mapEntry bool               // a map entry whose value, entry field 2, is message
	message  *translatedMessage // nested message, or the map value message
}

type translatedMessage struct {
	name   string
	fields map[uint64]translatedField
}

type translatedCommand struct {
	internal uint16
	message  *translatedMessage
}

// protocolTranslation maps one game version's wire numbering to the internal numbering.
type protocolTranslation struct {
	gameVersion string
	commands    map[uint16]translatedCommand // by the wire command number
}

// protocolCounters is evidence that the table fits the traffic. A field number the table does not know, or a wire type
// that differs from the table, means the session does not speak the protocol the table describes.
type protocolCounters struct {
	translatedFrames  uint64
	unknownFields     uint64
	wireMismatches    uint64
	droppedNewFields  uint64
	translationErrors uint64
	pendingFrames     uint64
	unsupportedFrames uint64
	// mismatches names the message, field number and wire type of each unknown field or wire mismatch, bounded by
	// maxProtocolMismatchKinds; it holds no values.
	mismatches map[string]uint64
}

// maxProtocolMismatchKinds bounds the distinct mismatch kinds kept for diagnostics; later kinds are only counted.
const maxProtocolMismatchKinds = 64

func (c *protocolCounters) observeMismatch(kind string, message *translatedMessage, number, wire uint64) {
	if c.mismatches == nil {
		c.mismatches = make(map[string]uint64)
	}
	key := fmt.Sprintf("%s %s.%d wire=%d", kind, message.name, number, wire)
	if _, known := c.mismatches[key]; known || len(c.mismatches) < maxProtocolMismatchKinds {
		c.mismatches[key]++
	}
}

// protocolSession is the wire protocol of the current protocol session. Its counters cover the whole capture.
type protocolSession struct {
	state       protocolState
	gameVersion string // major.minor the session reported; empty when not reported
	// sessionPendingFrames counts this protocol session's frames before its version report (maxPendingProtocolFrames).
	sessionPendingFrames uint64
	counters             protocolCounters
}

// protocolReport is the Development census and diagnostic view of protocol normalization over a capture.
type protocolReport struct {
	Status            string `json:"status"`
	GameVersion       string `json:"gameVersion,omitempty"`
	TranslatedFrames  uint64 `json:"translatedFrames"`
	UnknownFields     uint64 `json:"unknownFields"`
	WireMismatches    uint64 `json:"wireMismatches"`
	DroppedNewFields  uint64 `json:"droppedNewFields"`
	TranslationErrors uint64 `json:"translationErrors"`
	PendingFrames     uint64 `json:"pendingFrames"`
	UnsupportedFrames uint64 `json:"unsupportedFrames"`
	// Mismatches counts each kind of unknown field or wire mismatch by message, field number and wire type.
	Mismatches map[string]uint64 `json:"mismatches,omitempty"`
}

func (s *protocolSession) report() protocolReport {
	status, version := s.status()
	c := s.counters
	report := protocolReport{Status: status, GameVersion: version, TranslatedFrames: c.translatedFrames, UnknownFields: c.unknownFields,
		WireMismatches: c.wireMismatches, DroppedNewFields: c.droppedNewFields, TranslationErrors: c.translationErrors,
		PendingFrames: c.pendingFrames, UnsupportedFrames: c.unsupportedFrames}
	if len(c.mismatches) > 0 {
		report.Mismatches = make(map[string]uint64, len(c.mismatches))
		for key, count := range c.mismatches {
			report.Mismatches[key] = count
		}
	}
	return report
}

// line formats the report for Development diagnostics. It carries counts and the reported major.minor only.
func (r protocolReport) line() string {
	version := r.GameVersion
	if version == "" {
		version = "none"
	}
	return fmt.Sprintf("protocol_normalization status=%s game_version=%s translated_frames=%d unknown_fields=%d wire_mismatches=%d dropped_new_fields=%d translation_errors=%d pending_frames=%d unsupported_frames=%d",
		r.Status, version, r.TranslatedFrames, r.UnknownFields, r.WireMismatches, r.DroppedNewFields, r.TranslationErrors, r.PendingFrames, r.UnsupportedFrames)
}

type protocolTableFile struct {
	Format            string                         `json:"format"`
	GameVersion       string                         `json:"gameVersion"`
	InternalNumbering string                         `json:"internalNumbering"`
	Commands          [][3]json.RawMessage           `json:"commands"`
	Messages          map[string][][]json.RawMessage `json:"messages"`
}

// currentProtocol is the embedded table, loaded once; a table that fails validation is a build defect and panics.
var currentProtocol = sync.OnceValue(func() *protocolTranslation {
	translation, err := loadProtocolTranslation(currentProtocolTable)
	if err != nil {
		panic(fmt.Sprintf("embedded protocol translation is invalid: %v", err))
	}
	return translation
})

// CurrentGameVersion is the major.minor game version this build decodes.
func CurrentGameVersion() string {
	return currentProtocol().gameVersion
}

func loadProtocolTranslation(data []byte) (*protocolTranslation, error) {
	var file protocolTableFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Format != "wwlogs-protocol-translation/2" || file.InternalNumbering != internalNumbering || !gameVersionPattern.MatchString(file.GameVersion+".0") {
		return nil, fmt.Errorf("unexpected table header %q %q %q", file.Format, file.GameVersion, file.InternalNumbering)
	}
	messages := make(map[string]*translatedMessage, len(file.Messages))
	for name := range file.Messages {
		messages[name] = &translatedMessage{name: name, fields: make(map[uint64]translatedField)}
	}
	for name, rows := range file.Messages {
		message := messages[name]
		internalSeen := make(map[uint64]bool)
		for _, row := range rows {
			if len(row) != 5 {
				return nil, fmt.Errorf("%s: field row has %d values", name, len(row))
			}
			var wireNumber, internal, wire, flags uint64
			var nested string
			for index, target := range []any{&wireNumber, &internal, &wire, &flags, &nested} {
				if err := json.Unmarshal(row[index], target); err != nil {
					return nil, fmt.Errorf("%s: field row: %w", name, err)
				}
			}
			if wireNumber == 0 || wire > 5 || wire == 3 || wire == 4 {
				return nil, fmt.Errorf("%s: invalid field %d wire %d", name, wireNumber, wire)
			}
			if _, duplicate := message.fields[wireNumber]; duplicate {
				return nil, fmt.Errorf("%s: field %d listed twice", name, wireNumber)
			}
			if internal != 0 {
				if internalSeen[internal] {
					return nil, fmt.Errorf("%s: internal field %d assigned twice", name, internal)
				}
				internalSeen[internal] = true
			}
			field := translatedField{internal: internal, wire: wire, packed: flags&1 != 0, mapEntry: flags&2 != 0}
			if nested != "" {
				field.message = messages[nested]
				if field.message == nil {
					return nil, fmt.Errorf("%s: field %d names unknown message %s", name, wireNumber, nested)
				}
				if wire != 2 {
					return nil, fmt.Errorf("%s: message field %d is not length-delimited", name, wireNumber)
				}
			}
			message.fields[wireNumber] = field
		}
	}
	translation := &protocolTranslation{gameVersion: file.GameVersion, commands: make(map[uint16]translatedCommand, len(file.Commands))}
	internalSeen := make(map[uint16]bool)
	for _, row := range file.Commands {
		var wireCommand, internal uint16
		var name string
		for index, target := range []any{&wireCommand, &internal, &name} {
			if err := json.Unmarshal(row[index], target); err != nil {
				return nil, fmt.Errorf("command row: %w", err)
			}
		}
		message := messages[name]
		if message == nil {
			return nil, fmt.Errorf("command %d names unknown message %s", wireCommand, name)
		}
		if _, duplicate := translation.commands[wireCommand]; duplicate || internalSeen[internal] {
			return nil, fmt.Errorf("command %d -> %d is not one to one", wireCommand, internal)
		}
		internalSeen[internal] = true
		translation.commands[wireCommand] = translatedCommand{internal: internal, message: message}
	}
	for _, shared := range []uint16{protoKeyResponseCommand, loginRequestCommand, checkClientPushCommand} {
		if command, ok := translation.commands[shared]; !ok || command.internal != shared {
			return nil, fmt.Errorf("version report command %d does not map to itself", shared)
		}
	}
	return translation, nil
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
	if gameVersion == currentProtocol().gameVersion {
		s.state = protocolCurrent
	} else {
		s.state = protocolUnsupported
	}
}

// internalCommand is the internal command a wire command reads as in this protocol session, or 0 when the session
// cannot decode it: no current-version report yet, another version, or a command the table does not cover. The
// version reports themselves always read, because they decide the session.
func (s *protocolSession) internalCommand(wire uint16) (uint16, *translatedMessage) {
	if wire == loginRequestCommand || wire == checkClientPushCommand {
		return wire, nil
	}
	if s.state != protocolCurrent {
		return 0, nil
	}
	if command, ok := currentProtocol().commands[wire]; ok {
		return command.internal, command.message
	}
	return 0, nil
}

// command resolves a frame like internalCommand and counts frames the session could not decode for its version state.
func (s *protocolSession) command(wire uint16) (uint16, *translatedMessage) {
	internal, message := s.internalCommand(wire)
	if internal != 0 {
		return internal, message
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
	return 0, nil
}

// undecodableReason is the census reason for a frame command returned 0 for.
func (s *protocolSession) undecodableReason() string {
	switch s.state {
	case protocolNone:
		return protocolNoHandshakeReason
	case protocolPending:
		return protocolPendingFailureReason
	case protocolUnsupported:
		return protocolUnsupportedFailureReason
	default:
		return protocolUntranslatedReason
	}
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

// translate re-encodes body, a message of type message, with internal field numbers.
func (s *protocolSession) translate(message *translatedMessage, body []byte) ([]byte, error) {
	out, err := translateMessage(message, body, 0, &s.counters)
	if err != nil {
		s.counters.translationErrors++
		return nil, err
	}
	s.counters.translatedFrames++
	return out, nil
}

var errTranslationDepth = errors.New("protocol translation exceeds the nesting bound")

func translateMessage(message *translatedMessage, data []byte, depth int, counters *protocolCounters) ([]byte, error) {
	if depth > maxTranslationDepth {
		return nil, errTranslationDepth
	}
	out := make([]byte, 0, len(data)+8)
	for offset := 0; offset < len(data); {
		tag, n := binary.Uvarint(data[offset:])
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid Protobuf tag")
		}
		offset += n
		number, wire := tag>>3, tag&7
		valueStart := offset
		var payload []byte
		switch wire {
		case 0:
			_, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return nil, errors.New("invalid Protobuf varint")
			}
			offset += n
		case 1:
			if len(data)-offset < 8 {
				return nil, errors.New("truncated Protobuf fixed64")
			}
			offset += 8
		case 2:
			length, n := binary.Uvarint(data[offset:])
			if n <= 0 || length > uint64(len(data)-offset-n) {
				return nil, errors.New("invalid Protobuf byte length")
			}
			payload = data[offset+n : offset+n+int(length)]
			offset += n + int(length)
		case 5:
			if len(data)-offset < 4 {
				return nil, errors.New("truncated Protobuf fixed32")
			}
			offset += 4
		default:
			return nil, fmt.Errorf("unsupported Protobuf wire type %d", wire)
		}
		field, known := message.fields[number]
		switch {
		case !known:
			counters.unknownFields++
			counters.observeMismatch("unknown", message, number, wire)
			continue
		case wire != field.wire && !(field.packed && wire == 2):
			counters.wireMismatches++
			counters.observeMismatch("wire", message, number, wire)
			continue
		case field.internal == 0:
			counters.droppedNewFields++
			continue
		}
		if field.message == nil || wire != 2 {
			out = binary.AppendUvarint(out, field.internal<<3|wire)
			out = append(out, data[valueStart:offset]...)
			continue
		}
		var nested []byte
		var err error
		if field.mapEntry {
			nested, err = translateMapEntry(field.message, payload, depth+1, counters)
		} else {
			nested, err = translateMessage(field.message, payload, depth+1, counters)
		}
		if err != nil {
			return nil, err
		}
		out = binary.AppendUvarint(out, field.internal<<3|2)
		out = binary.AppendUvarint(out, uint64(len(nested)))
		out = append(out, nested...)
	}
	return out, nil
}

// translateMapEntry translates the value of one map entry; the key (field 1) and the entry numbering never change.
func translateMapEntry(value *translatedMessage, entry []byte, depth int, counters *protocolCounters) ([]byte, error) {
	fields, err := parseProtoFields(entry)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(entry)+8)
	for _, field := range fields {
		if field.number == 2 && field.wire == 2 {
			nested, err := translateMessage(value, field.data, depth, counters)
			if err != nil {
				return nil, err
			}
			out = binary.AppendUvarint(out, 2<<3|2)
			out = binary.AppendUvarint(out, uint64(len(nested)))
			out = append(out, nested...)
			continue
		}
		out = appendProtoField(out, field)
	}
	return out, nil
}

func appendProtoField(out []byte, field protoField) []byte {
	out = binary.AppendUvarint(out, field.number<<3|field.wire)
	switch field.wire {
	case 0:
		out = binary.AppendUvarint(out, field.value)
	case 2:
		out = binary.AppendUvarint(out, uint64(len(field.data)))
		out = append(out, field.data...)
	default:
		out = append(out, field.data...)
	}
	return out
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
