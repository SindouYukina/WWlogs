package capture

import (
	"bytes"
	"testing"
)

// currentTestBuild is a LoginRequest build of the current game version, as the 2026-11-12 Global 3.8 capture reported it.
const currentTestBuild = "OS_Windows_Product_3.8.0_9244589_3.8.9_9436312"

// The embedded table must load, keep the version reports as they are, and carry the renumbering that the 2026-11-12
// Global 3.8 capture confirmed on the wire (research/runs/2026-11-12/v0.7.0-3.8-compatibility).
func TestEmbeddedProtocolTranslationLoads(t *testing.T) {
	translation := currentProtocol()
	if translation == nil || translation.gameVersion != "3.8" || CurrentGameVersion() != "3.8" {
		t.Fatal("the current protocol table is not the Global 3.8 one")
	}
	for wire, internal := range map[uint16]uint16{
		112: 112, 103: 103, 116: 116,
		26497: 19470, // CombatSendPackRequest
		21536: 20332, // CombatSendPackResponse
		29973: 25706, // CombatReceivePackNotify
		20302: 25185, // EntityAddNotify
		17927: 17769, // EntityRemoveNotify
		20597: 18559, // JoinSceneNotify
		21993: 15380, // BasicInfoNotify
		26157: 28481, // EntityActiveRequest
		19734: 26702, // EntityActiveResponse
	} {
		command, ok := translation.commands[wire]
		if !ok || command.internal != internal {
			t.Fatalf("wire command %d -> %+v, want internal %d", wire, command, internal)
		}
	}
	// CombatSendPackRequest moved its Data field from 14 to 11 and HostPlayerId from 8 to 10.
	root := translation.commands[26497].message
	if root.fields[11].internal != 14 || root.fields[10].internal != 8 || root.fields[11].message == nil {
		t.Fatalf("CombatSendPackRequest fields = %+v", root.fields)
	}
}

func TestProtocolTableRejectsInconsistentRows(t *testing.T) {
	const header = `"format":"wwlogs-protocol-translation/2","gameVersion":"9.9","internalNumbering":"global-3.7"`
	for name, table := range map[string]string{
		"unknown nested message": `{` + header + `,"commands":[[103,103,"A"],[112,112,"A"],[116,116,"A"]],"messages":{"A":[[1,1,2,0,"B"]]}}`,
		"duplicate internal":     `{` + header + `,"commands":[[103,103,"A"],[112,112,"A"],[116,116,"A"]],"messages":{"A":[[1,1,0,0,""],[2,1,0,0,""]]}}`,
		"version report moved":   `{` + header + `,"commands":[[104,103,"A"],[112,112,"A"],[116,116,"A"]],"messages":{"A":[]}}`,
		"other numbering":        `{"format":"wwlogs-protocol-translation/2","gameVersion":"9.9","internalNumbering":"global-3.6","commands":[],"messages":{}}`,
		"older format":           `{"format":"wwlogs-protocol-translation/1","gameVersion":"9.9","internalNumbering":"global-3.7","commands":[],"messages":{}}`,
	} {
		if _, err := loadProtocolTranslation([]byte(table)); err == nil {
			t.Fatalf("%s: table was accepted", name)
		}
	}
}

func TestGameVersionComesFromTheFirstVersionToken(t *testing.T) {
	for text, want := range map[string]string{
		"OS_Windows_Product_3.8.0_9244589_3.8.9_9436312":  "3.8",
		"OS_Windows_Product_3.7.0_9052414_3.7.16_9131583": "3.7",
		"3.10.2":   "3.10",
		"Product":  "",
		"3.8":      "",
		"3.8.0-rc": "",
	} {
		if got := gameVersionOf(text); got != want {
			t.Fatalf("gameVersionOf(%q) = %q, want %q", text, got, want)
		}
	}
}

// Only a session that reported the current game version is decoded; nothing falls back to the internal numbering.
func TestProtocolSessionDecodesOnlyTheCurrentVersion(t *testing.T) {
	var session protocolSession
	if internal, _ := session.command(26497); internal != 0 {
		t.Fatal("a frame before any handshake was decoded")
	}
	session.beginSession()
	if internal, _ := session.command(26497); internal != 0 {
		t.Fatal("a session decoded combat before reporting its version")
	}
	if internal, _ := session.command(loginRequestCommand); internal != loginRequestCommand {
		t.Fatal("the version report itself must stay readable")
	}
	session.observeVersion("3.8")
	session.observeVersion("3.7")
	if status, version := session.status(); status != ProtocolStatusSupported || version != "3.8" {
		t.Fatalf("status %s %s, want the first report to decide", status, version)
	}
	if internal, message := session.command(26497); internal != 19470 || message == nil {
		t.Fatal("the current protocol's combat command was not translated")
	}
	if internal, _ := session.command(19470); internal != 0 {
		t.Fatal("an internal command number was accepted on the wire")
	}
	for _, other := range []string{"3.7", "3.9"} {
		session.beginSession()
		session.observeVersion(other)
		if status, version := session.status(); status != ProtocolStatusUnsupported || version != other {
			t.Fatalf("%s: status %s %s, want unsupported", other, status, version)
		}
		if internal, _ := session.command(26497); internal != 0 {
			t.Fatalf("%s: a frame of another game version was decoded", other)
		}
		if internal, _ := session.command(19470); internal != 0 {
			t.Fatalf("%s: the internal numbering was used as a fallback", other)
		}
	}
	session.beginSession()
	for range maxPendingProtocolFrames {
		session.command(26497)
	}
	if status, _ := session.status(); status != ProtocolStatusUnsupported {
		t.Fatalf("a session that never reports a version stays %s", status)
	}
}

// reverseTranslate encodes a message written in the internal numbering in the current wire numbering, the inverse of
// translateMessage, so tests can write bodies in the decoders' vocabulary and send what the client would send.
func reverseTranslate(t *testing.T, message *translatedMessage, data []byte) []byte {
	t.Helper()
	inverse := make(map[uint64]uint64)
	for wire, field := range message.fields {
		if field.internal != 0 {
			inverse[field.internal] = wire
		}
	}
	fields, err := parseProtoFields(data)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, field := range fields {
		wireNumber, ok := inverse[field.number]
		if !ok {
			t.Fatalf("%s: internal field %d has no current counterpart", message.name, field.number)
		}
		definition := message.fields[wireNumber]
		if definition.message != nil && field.wire == 2 {
			if definition.mapEntry {
				entry, err := parseProtoFields(field.data)
				if err != nil {
					t.Fatal(err)
				}
				var rebuilt []byte
				for _, part := range entry {
					if part.number == 2 && part.wire == 2 {
						part.data = reverseTranslate(t, definition.message, part.data)
					}
					rebuilt = appendProtoField(rebuilt, part)
				}
				field.data = rebuilt
			} else {
				field.data = reverseTranslate(t, definition.message, field.data)
			}
		}
		field.number = wireNumber
		out = appendProtoField(out, field)
	}
	return out
}

// wireCommandFor is the current wire command and root message of an internal command.
func wireCommandFor(t *testing.T, internal uint16) (uint16, *translatedMessage) {
	t.Helper()
	for wire, command := range currentProtocol().commands {
		if command.internal == internal {
			return wire, command.message
		}
	}
	t.Fatalf("internal command %d is not in the current protocol table", internal)
	return 0, nil
}

func TestTranslationRoundTripsInternalBodies(t *testing.T) {
	for internal, body := range map[uint16][]byte{
		19470: damageRequestBody(7, 100, 200),
		20332: damageResponseBody(7, -321),
		18559: initialJoinBody(),
		25706: battleStateReceivePack(true, 77),
		15380: appendVarintField(nil, 15, 77),
	} {
		_, message := wireCommandFor(t, internal)
		wire := reverseTranslate(t, message, body)
		var session protocolSession
		got, err := session.translate(message, wire)
		if err != nil {
			t.Fatalf("command %d: %v", internal, err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("command %d: translation does not restore the internal body", internal)
		}
		if session.counters.unknownFields != 0 || session.counters.wireMismatches != 0 || session.counters.translatedFrames != 1 {
			t.Fatalf("command %d: counters %+v", internal, session.counters)
		}
	}
}

func TestTranslationDropsWhatTheDecodersCannotRead(t *testing.T) {
	_, message := wireCommandFor(t, 19470)
	var newOnly uint64
	for wire, field := range message.fields {
		if field.internal == 0 {
			newOnly = wire
		}
	}
	body := appendVarintField(nil, 10, 5)        // HostPlayerId, internal 8
	body = appendBytesField(body, 10, []byte{1}) // the same field with a wire type the schema does not declare
	body = appendVarintField(body, 2000, 9)      // a number the 3.8 schema does not know
	if newOnly != 0 {
		body = appendVarintField(body, newOnly, 1) // a field the decoders have no number for
	}
	var session protocolSession
	got, err := session.translate(message, body)
	if err != nil {
		t.Fatal(err)
	}
	if want := appendVarintField(nil, 8, 5); !bytes.Equal(got, want) {
		t.Fatalf("translated body %x, want only HostPlayerId as internal field 8", got)
	}
	if session.counters.wireMismatches != 1 || session.counters.unknownFields != 1 || len(session.counters.mismatches) != 2 {
		t.Fatalf("counters %+v", session.counters)
	}
}

func TestTranslationRejectsMalformedAndUnboundedBodies(t *testing.T) {
	_, message := wireCommandFor(t, 19470)
	var session protocolSession
	if _, err := session.translate(message, []byte{0x5a, 0x05, 0x01}); err == nil {
		t.Fatal("a truncated length-delimited field was accepted")
	}
	// A self-nesting message deeper than the bound is refused instead of recursing without limit.
	loop := &translatedMessage{name: "loop", fields: map[uint64]translatedField{}}
	loop.fields[1] = translatedField{internal: 1, wire: 2, message: loop}
	var nested []byte
	for range maxTranslationDepth + 2 {
		nested = appendBytesField(nil, 1, nested)
	}
	if _, err := translateMessage(loop, nested, 0, &protocolCounters{}); err == nil {
		t.Fatal("nesting beyond the bound was accepted")
	}
	if session.counters.translationErrors != 1 {
		t.Fatalf("translation errors %d, want 1", session.counters.translationErrors)
	}
}

// A fight sent in the current wire protocol is decoded end to end: pairing, attribution and the encounter.
func TestCurrentProtocolSessionDecodesTheFight(t *testing.T) {
	p, _ := initialProcessor(t)
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 77), 0.1)
	initialEncryptedFrame(t, p, 18559, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 200, func(e *runtimeEntity) { e.entityTypeCode, e.hasEntityType = entityTypeMonster, true })
	initialEncryptedFrame(t, p, 25706, "in", battleStateReceivePack(true, 77), 1)
	initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(1, 100, 200), 1.1)
	initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(1, 500), 2)
	initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(2, 100, 200), 2.1)
	initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(2, 250), 3)
	s := p.damageStatisticsSnapshot()
	if s.DamageDealt != 750 || s.OutgoingEvents != 2 || len(s.Roles) != 1 || s.Encounters.Current == nil || s.Encounters.Current.Totals.DamageDealt != 750 {
		t.Fatalf("current-protocol session: dealt %d events %d roles %d", s.DamageDealt, s.OutgoingEvents, len(s.Roles))
	}
	if report := p.protocol.report(); report.Status != ProtocolStatusSupported || report.GameVersion != "3.8" || report.TranslatedFrames != 7 || report.UnknownFields != 0 || report.WireMismatches != 0 {
		t.Fatalf("protocol report %+v", report)
	}
}

// A session of any other game version is never decoded with a guessed numbering. 3.7 sends exactly the internal
// numbering on the wire, so this is also the proof that no older protocol is accepted as a fallback.
func TestOtherGameVersionsAreNeverDecoded(t *testing.T) {
	for _, build := range []string{"OS_Windows_Product_3.7.0_9052414_3.7.18_9350105", "OS_Windows_Product_3.9.0_9999999_3.9.1_9999999"} {
		p, _ := initialProcessorWithBuild(t, build)
		p.census = newCommandCensus()
		for _, frame := range []struct {
			command   uint16
			direction string
			body      []byte
			seconds   float64
		}{
			{18559, "in", initialJoinBody(), 0.2},
			{19470, "out", damageRequestBody(1, 100, 200), 1.1},
			{20332, "in", damageResponseBody(1, 500), 2},
		} {
			initialRawEncryptedFrame(t, p, frame.command, frame.direction, frame.body, frame.seconds)
		}
		s := p.damageStatisticsSnapshot()
		if s.DamageDealt != 0 || s.UnattributedOutgoingDamage != 0 || p.stats.damageRequests != 0 || p.stats.candidateResponses != 0 {
			t.Fatalf("%s: a session of another game version produced combat data", build)
		}
		want := gameVersionOf(build)
		if health := p.sessionHealthSnapshot(); health.Protocol.Status != ProtocolStatusUnsupported || health.Protocol.GameVersion != want {
			t.Fatalf("%s: session health protocol %+v", build, health.Protocol)
		}
		if got := p.census.entry(19470, "out").FailureReasons[protocolUnsupportedFailureReason]; got != 1 {
			t.Fatalf("%s: census reason = %d, want 1", build, got)
		}
	}
}
