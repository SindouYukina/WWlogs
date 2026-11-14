package capture

import (
	"strings"
	"testing"
)

// currentTestBuild is a LoginRequest build of the current game version, as the 2026-11-12 Global 3.8 capture reported it.
const currentTestBuild = "OS_Windows_Product_3.8.0_9244589_3.8.9_9436312"

// The decoders read the current game version's own numbers. These are the ones the 2026-11-12 Global 3.8 capture carried
// on the wire; a regenerated protocol_numbers.go that changes them for the same game version needs new captured
// evidence, not a quiet update of this test.
func TestCurrentProtocolNumbersMatchTheCapturedWire(t *testing.T) {
	if CurrentGameVersion() != "3.8" || currentGameVersion != gameVersionOf(currentTestBuild) {
		t.Fatalf("current game version %q does not match the captured build %q", CurrentGameVersion(), currentTestBuild)
	}
	for name, pair := range map[string][2]uint64{
		"ProtoKeyResponse":        {cmdProtoKeyResponse, 112},
		"LoginRequest":            {cmdLoginRequest, 103},
		"CheckClientPush":         {cmdCheckClientPush, 116},
		"CombatSendPackRequest":   {cmdCombatSendPackRequest, 26497},
		"CombatSendPackResponse":  {cmdCombatSendPackResponse, 21536},
		"CombatReceivePackNotify": {cmdCombatReceivePackNotify, 29973},
		"EntityAddNotify":         {cmdEntityAddNotify, 20302},
		"EntityRemoveNotify":      {cmdEntityRemoveNotify, 17927},
		"JoinSceneNotify":         {cmdJoinSceneNotify, 20597},
		"BasicInfoNotify":         {cmdBasicInfoNotify, 21993},
		"EntityActiveRequest":     {cmdEntityActiveRequest, 26157},
		"EntityActiveResponse":    {cmdEntityActiveResponse, 19734},
		// CombatSendPackRequest.Data moved from 14 (Global 3.7) to 11.
		"CombatSendPackRequest.Data":               {fieldCombatSendPackRequestData, 11},
		"CombatSendPackResponse.ReceivePackNotify": {fieldCombatSendPackResponseReceivePackNotify, 1},
		"DamageExecuteRequest.AttackerEntityId":    {fieldDamageExecuteRequestAttackerEntityID, 5},
		"DamageExecuteRequest.TargetEntityId":      {fieldDamageExecuteRequestTargetEntityID, 1},
		"DamageExecuteResponse.Damage":             {fieldDamageExecuteResponseDamage, 9},
		"LoginRequest.ClientBasicInfo":             {fieldLoginRequestClientBasicInfo, 8},
		"ClientBasicInfo.ClientVersion":            {fieldClientBasicInfoClientVersion, 19},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, the capture carried %d", name, pair[0], pair[1])
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

// Only a session that reported the current game version is decoded; nothing falls back to another numbering.
func TestProtocolSessionDecodesOnlyTheCurrentVersion(t *testing.T) {
	var session protocolSession
	if session.decodes(cmdCombatSendPackRequest) {
		t.Fatal("a frame before any handshake was decoded")
	}
	session.beginSession()
	if session.decodes(cmdCombatSendPackRequest) {
		t.Fatal("a session decoded combat before reporting its version")
	}
	if !session.decodes(cmdLoginRequest) || !session.decodes(cmdCheckClientPush) {
		t.Fatal("the version reports themselves must stay readable")
	}
	session.observeVersion("3.8")
	session.observeVersion("3.7")
	if status, version := session.status(); status != ProtocolStatusSupported || version != "3.8" {
		t.Fatalf("status %s %s, want the first report to decide", status, version)
	}
	if !session.decodes(cmdCombatSendPackRequest) {
		t.Fatal("the current protocol's combat command was not decoded")
	}
	for _, other := range []string{"3.7", "3.9"} {
		session.beginSession()
		session.observeVersion(other)
		if status, version := session.status(); status != ProtocolStatusUnsupported || version != other {
			t.Fatalf("%s: status %s %s, want unsupported", other, status, version)
		}
		if session.decodes(cmdCombatSendPackRequest) || session.readable(cmdCombatSendPackResponse) {
			t.Fatalf("%s: a frame of another game version was decoded", other)
		}
	}
	session.beginSession()
	for range maxPendingProtocolFrames {
		session.decodes(cmdCombatSendPackRequest)
	}
	if status, _ := session.status(); status != ProtocolStatusUnsupported {
		t.Fatalf("a session that never reports a version stays %s", status)
	}
	if report := session.report(); report.PendingFrames != maxPendingProtocolFrames+1 || report.UnsupportedFrames != 2 {
		t.Fatalf("protocol report %+v", report)
	}
}

// A fight sent in the current protocol is decoded end to end: pairing, attribution and the encounter.
func TestCurrentProtocolSessionDecodesTheFight(t *testing.T) {
	p, _ := initialProcessor(t)
	p.census = newCommandCensus()
	initialEncryptedFrame(t, p, cmdBasicInfoNotify, "in", appendVarintField(nil, fieldBasicInfoNotifyID, 77), 0.1)
	initialEncryptedFrame(t, p, cmdJoinSceneNotify, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 200, func(e *runtimeEntity) { e.entityTypeCode, e.hasEntityType = entityTypeMonster, true })
	initialEncryptedFrame(t, p, cmdCombatReceivePackNotify, "in", battleStateReceivePack(true, 77), 1)
	initialEncryptedFrame(t, p, cmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1)
	initialEncryptedFrame(t, p, cmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2)
	initialEncryptedFrame(t, p, cmdCombatSendPackRequest, "out", damageRequestBody(2, 100, 200), 2.1)
	initialEncryptedFrame(t, p, cmdCombatSendPackResponse, "in", damageResponseBody(2, 250), 3)
	s := p.damageStatisticsSnapshot()
	if s.DamageDealt != 750 || s.OutgoingEvents != 2 || len(s.Roles) != 1 || s.Encounters.Current == nil || s.Encounters.Current.Totals.DamageDealt != 750 {
		t.Fatalf("current-protocol session: dealt %d events %d roles %d", s.DamageDealt, s.OutgoingEvents, len(s.Roles))
	}
	if report := p.protocol.report(); report.Status != ProtocolStatusSupported || report.GameVersion != "3.8" || report.PendingFrames != 0 || report.UnsupportedFrames != 0 {
		t.Fatalf("protocol report %+v", report)
	}
	if entry := p.census.entries[censusKey{CommandID: cmdJoinSceneNotify, Direction: "in"}]; entry == nil || entry.Name != "JoinSceneNotify" || entry.SchemaBasis != censusSchemaBasis() {
		t.Fatalf("a current-protocol frame was not named from the current protocol: %+v", entry)
	}
}

// Evidence names the protocol a message was read with, the current client's, and the version gate that admitted it.
func TestEvidenceNamesTheCurrentProtocol(t *testing.T) {
	for name, text := range map[string]string{"message schema basis": protocolSchemaBasis(), "census schema basis": censusSchemaBasis(), "combat notify audit": combatNotifyAuditSchemaBasis} {
		if !strings.Contains(text, CurrentProtocol()) {
			t.Fatalf("%s %q does not name the current client protocol", name, text)
		}
	}
	if !strings.HasSuffix(protocolSchemaBasis(), "game version "+CurrentGameVersion()) {
		t.Fatalf("message schema basis %q does not name the version gate", protocolSchemaBasis())
	}
}

// A session of any other game version is never decoded, even when its frames happen to parse under the current numbers.
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
			{cmdJoinSceneNotify, "in", initialJoinBody(), 0.2},
			{cmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1},
			{cmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2},
		} {
			initialEncryptedFrame(t, p, frame.command, frame.direction, frame.body, frame.seconds)
		}
		s := p.damageStatisticsSnapshot()
		if s.DamageDealt != 0 || s.UnattributedOutgoingDamage != 0 || p.stats.damageRequests != 0 || p.stats.candidateResponses != 0 {
			t.Fatalf("%s: a session of another game version produced combat data", build)
		}
		want := gameVersionOf(build)
		if health := p.sessionHealthSnapshot(); health.Protocol.Status != ProtocolStatusUnsupported || health.Protocol.GameVersion != want {
			t.Fatalf("%s: session health protocol %+v", build, health.Protocol)
		}
		if got := p.census.entry(cmdCombatSendPackRequest, "out").FailureReasons[protocolUnsupportedFailureReason]; got != 1 {
			t.Fatalf("%s: census reason = %d, want 1", build, got)
		}
		// The same number means another message in another version: the census must not name it from the current one.
		for _, key := range []censusKey{{CommandID: cmdJoinSceneNotify, Direction: "in"}, {CommandID: cmdCombatSendPackRequest, Direction: "out"}, {CommandID: cmdCombatSendPackResponse, Direction: "in"}} {
			if entry := p.census.entries[key]; entry == nil || entry.Name != "" || entry.SchemaBasis != "" {
				t.Fatalf("%s: a frame of another game version took a current-protocol name: %+v", build, entry)
			}
		}
	}
}
