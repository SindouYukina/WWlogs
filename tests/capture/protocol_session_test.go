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
		"ProtoKeyResponse":        {CmdProtoKeyResponse, 112},
		"LoginRequest":            {CmdLoginRequest, 103},
		"CheckClientPush":         {CmdCheckClientPush, 116},
		"CombatSendPackRequest":   {CmdCombatSendPackRequest, 26497},
		"CombatSendPackResponse":  {CmdCombatSendPackResponse, 21536},
		"CombatReceivePackNotify": {CmdCombatReceivePackNotify, 29973},
		"EntityAddNotify":         {CmdEntityAddNotify, 20302},
		"EntityRemoveNotify":      {CmdEntityRemoveNotify, 17927},
		"JoinSceneNotify":         {CmdJoinSceneNotify, 20597},
		"BasicInfoNotify":         {CmdBasicInfoNotify, 21993},
		"EntityActiveRequest":     {CmdEntityActiveRequest, 26157},
		"EntityActiveResponse":    {CmdEntityActiveResponse, 19734},
		// CombatSendPackRequest.Data moved from 14 (Global 3.7) to 11.
		"CombatSendPackRequest.Data":               {FieldCombatSendPackRequestData, 11},
		"CombatSendPackResponse.ReceivePackNotify": {FieldCombatSendPackResponseReceivePackNotify, 1},
		"DamageExecuteRequest.AttackerEntityId":    {FieldDamageExecuteRequestAttackerEntityID, 5},
		"DamageExecuteRequest.TargetEntityId":      {FieldDamageExecuteRequestTargetEntityID, 1},
		"DamageExecuteResponse.Damage":             {FieldDamageExecuteResponseDamage, 9},
		"LoginRequest.ClientBasicInfo":             {FieldLoginRequestClientBasicInfo, 8},
		"ClientBasicInfo.ClientVersion":            {FieldClientBasicInfoClientVersion, 19},
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
	var session ProtocolSession
	if session.decodes(CmdCombatSendPackRequest) {
		t.Fatal("a frame before any handshake was decoded")
	}
	session.BeginSession()
	if session.decodes(CmdCombatSendPackRequest) {
		t.Fatal("a session decoded combat before reporting its version")
	}
	if !session.decodes(CmdLoginRequest) || !session.decodes(CmdCheckClientPush) {
		t.Fatal("the version reports themselves must stay readable")
	}
	session.ObserveVersion("3.8")
	session.ObserveVersion("3.7")
	if status, version := session.Status(); status != ProtocolStatusSupported || version != "3.8" {
		t.Fatalf("status %s %s, want the first report to decide", status, version)
	}
	if !session.decodes(CmdCombatSendPackRequest) {
		t.Fatal("the current protocol's combat command was not decoded")
	}
	for _, other := range []string{"3.7", "3.9"} {
		session.BeginSession()
		session.ObserveVersion(other)
		if status, version := session.Status(); status != ProtocolStatusUnsupported || version != other {
			t.Fatalf("%s: status %s %s, want unsupported", other, status, version)
		}
		if session.decodes(CmdCombatSendPackRequest) || session.Readable(CmdCombatSendPackResponse) {
			t.Fatalf("%s: a frame of another game version was decoded", other)
		}
	}
	session.BeginSession()
	for range maxPendingProtocolFrames {
		session.decodes(CmdCombatSendPackRequest)
	}
	if status, _ := session.Status(); status != ProtocolStatusUnsupported {
		t.Fatalf("a session that never reports a version stays %s", status)
	}
	if pending, unsupported := session.Counters.PendingFrames, session.Counters.UnsupportedFrames; pending != maxPendingProtocolFrames+1 || unsupported != 2 {
		t.Fatalf("protocol counters: pending %d unsupported %d", pending, unsupported)
	}
}

// A fight sent in the current protocol is decoded end to end: pairing, attribution and the encounter.
func TestCurrentProtocolSessionDecodesTheFight(t *testing.T) {
	p, _ := initialProcessor(t)
	sendCurrentProtocolFight(t, p)
	s := p.DamageStatisticsSnapshot()
	if s.DamageDealt != 750 || s.OutgoingEvents != 2 || len(s.Roles) != 1 || s.Encounters.Current == nil || s.Encounters.Current.Totals.DamageDealt != 750 {
		t.Fatalf("current-protocol session: dealt %d events %d roles %d", s.DamageDealt, s.OutgoingEvents, len(s.Roles))
	}
	if status, version := p.Protocol.Status(); status != ProtocolStatusSupported || version != "3.8" || p.Protocol.Counters.PendingFrames != 0 || p.Protocol.Counters.UnsupportedFrames != 0 {
		t.Fatalf("protocol session: %s %s, pending %d unsupported %d", status, version, p.Protocol.Counters.PendingFrames, p.Protocol.Counters.UnsupportedFrames)
	}
}

// sendCurrentProtocolFight sends a two-hit fight of the current protocol after the handshake of p.
func sendCurrentProtocolFight(t *testing.T, p *Processor) {
	t.Helper()
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 77), 0.1)
	initialEncryptedFrame(t, p, CmdJoinSceneNotify, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 200, func(e *RuntimeEntity) { e.EntityTypeCode, e.HasEntityType = EntityTypeMonster, true })
	initialEncryptedFrame(t, p, CmdCombatReceivePackNotify, "in", battleStateReceivePack(true, 77), 1)
	initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1)
	initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2)
	initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(2, 100, 200), 2.1)
	initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(2, 250), 3)
}

// Evidence names the protocol a message was read with, the current client's, and the version gate that admitted it.
func TestEvidenceNamesTheCurrentProtocol(t *testing.T) {
	if text := ProtocolSchemaBasis(); !strings.Contains(text, CurrentProtocol()) {
		t.Fatalf("message schema basis %q does not name the current client protocol", text)
	}
	if !strings.HasSuffix(ProtocolSchemaBasis(), "game version "+CurrentGameVersion()) {
		t.Fatalf("message schema basis %q does not name the version gate", ProtocolSchemaBasis())
	}
}

// A session of any other game version is never decoded, even when its frames happen to parse under the current numbers.
func TestOtherGameVersionsAreNeverDecoded(t *testing.T) {
	for _, build := range otherVersionBuilds {
		p, _ := initialProcessorWithBuild(t, build)
		sendOtherVersionFight(t, p)
		s := p.DamageStatisticsSnapshot()
		if s.DamageDealt != 0 || s.UnattributedOutgoingDamage != 0 || p.Stats.DamageRequests != 0 || p.Stats.CandidateResponses != 0 {
			t.Fatalf("%s: a session of another game version produced combat data", build)
		}
		want := gameVersionOf(build)
		if health := p.sessionHealthSnapshot(); health.Protocol.Status != ProtocolStatusUnsupported || health.Protocol.GameVersion != want {
			t.Fatalf("%s: session health protocol %+v", build, health.Protocol)
		}
	}
}

// otherVersionBuilds are client builds of other game versions, whose frames parse under the current numbers.
var otherVersionBuilds = []string{"OS_Windows_Product_3.7.0_9052414_3.7.18_9350105", "OS_Windows_Product_3.9.0_9999999_3.9.1_9999999"}

// sendOtherVersionFight sends a one-hit fight after the handshake of p.
func sendOtherVersionFight(t *testing.T, p *Processor) {
	t.Helper()
	for _, frame := range []struct {
		command   uint16
		direction string
		body      []byte
		seconds   float64
	}{
		{CmdJoinSceneNotify, "in", initialJoinBody(), 0.2},
		{CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1},
		{CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2},
	} {
		initialEncryptedFrame(t, p, frame.command, frame.direction, frame.body, frame.seconds)
	}
}
