package capture

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"testing"
	"time"
)

func initialHandshake(t *testing.T, p *Processor, key *rsa.PrivateKey, at time.Time) {
	t.Helper()
	initialHandshakeWithBuild(t, p, key, at, currentTestBuild)
}

// initialHandshakeWithBuild exchanges a session key and, like a real client right after it, reports build in a
// LoginRequest; the report decides whether the protocol session is decoded.
func initialHandshakeWithBuild(t *testing.T, p *Processor, key *rsa.PrivateKey, at time.Time, build string) {
	t.Helper()
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	body := appendBytesField(appendVarintField(nil, FieldProtoKeyResponseType, 2), FieldProtoKeyResponseKey, ciphertext)
	p.HandleFrame(GameFrame{CommandID: CmdProtoKeyResponse, Direction: "in", Timestamp: at, Body: body})
	initialEncryptedFrame(t, p, CmdLoginRequest, "out", loginRequestBody(build), 0)
}

// loginRequestBody encodes LoginRequest.ClientBasicInfo with only its build string (ClientVersion).
func loginRequestBody(build string) []byte {
	return appendBytesField(nil, FieldLoginRequestClientBasicInfo, appendBytesField(nil, FieldClientBasicInfoClientVersion, []byte(build)))
}

// initialEncryptedFrame encrypts body under the session key and sends it as the client does, with the given command.
func initialEncryptedFrame(t *testing.T, p *Processor, command uint16, direction string, body []byte, seconds float64) {
	t.Helper()
	const sequence = 1
	plain := append([]byte{}, body...)
	if len(plain) > 0 {
		plain[sequence%len(plain)] ^= p.Session[sequence&31]
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(p.Session)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, len(plain))
	for i := 0; i < len(plain); i += aes.BlockSize {
		block.Encrypt(encoded[i:i+aes.BlockSize], plain[i:i+aes.BlockSize])
	}
	p.HandleFrame(GameFrame{CommandID: command, Direction: direction, Sequence: sequence, Timestamp: pairingAt(seconds), Body: encoded})
}

func initialJoinBody() []byte {
	player := appendVarintField(nil, FieldScenePlayerInformationPlayerID, 77)
	role := appendVarintField(appendVarintField(nil, FieldSceneRoleInformationRoleID, 1409), FieldSceneRoleInformationEntityID, 100)
	player = appendBytesField(player, FieldScenePlayerInformationPlayerSceneGroupData, appendBytesField(nil, FieldPlayerSceneGroupDataFightRoleInfos, role))
	scene := appendBytesField(nil, FieldSceneInformationPlayerInfos, player)
	return appendBytesField(nil, FieldJoinSceneNotifySceneInfo, scene)
}

func initialProcessor(t *testing.T) (*Processor, *rsa.PrivateKey) {
	t.Helper()
	return initialProcessorWithBuild(t, currentTestBuild)
}

func initialProcessorWithBuild(t *testing.T, build string) (*Processor, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := newPairingTestProcessor()
	p.Diagnostic = io.Discard
	p.RSA = &StaticRSA{E: key.E, N: key.N, D: key.D}
	p.IntervalPerProtocolSession = true
	initialHandshakeWithBuild(t, p, key, pairingAt(0), build)
	return p, key
}

func TestFirstEncounterCountsBeforePeriodicRoleReport(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "identity_before_scene", false: "identity_after_scene"}[before], func(t *testing.T) {
			p, _ := initialProcessor(t)
			captureID := p.DamageStatistics.CaptureSessionID
			identity := appendVarintField(nil, FieldBasicInfoNotifyID, 77)
			if before {
				initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", identity, 0.1)
			}
			initialEncryptedFrame(t, p, CmdJoinSceneNotify, "in", initialJoinBody(), 0.2)
			if !before {
				initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", identity, 0.3)
			}
			addTestRuntimeEntity(p, 200, func(e *RuntimeEntity) { e.EntityTypeCode, e.HasEntityType = EntityTypeMonster, true })
			initialEncryptedFrame(t, p, CmdCombatReceivePackNotify, "in", battleStateReceivePack(true, 77), 1)
			initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1)
			initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2)
			first := p.DamageStatisticsSnapshot()
			if first.DamageDealt != 500 || first.OutgoingEvents != 1 || len(first.Roles) != 1 || first.Encounters.Current == nil || first.Encounters.Current.Totals.DamageDealt != 500 {
				t.Fatalf("first encounter lost paired damage: confirmed=%d unattributed=%d roles=%d state=%s", first.DamageDealt, first.UnattributedOutgoingDamage, len(first.Roles), first.Encounters.State)
			}
			if first.CaptureSessionID != captureID || first.StatisticsIntervalID != 1 || p.SessionNumber != 1 || p.Stats.PlayerRoleReports != 0 {
				t.Fatal("initial session or interval changed")
			}
			view := p.ViewSnapshot()
			if view.Statistics.DamageDealt != 500 || len(view.Roles) != 1 || view.Encounters.Current.DPS.Value <= 0 {
				t.Fatal("first damage did not reach the shared UI snapshot")
			}
			initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2.1)
			initialEncryptedFrame(t, p, CmdCombatReceivePackNotify, "in", battleStateReceivePack(false, 77), 3)
			p.observeEncounterClock(pairingAt(5))
			initialEncryptedFrame(t, p, CmdCombatReceivePackNotify, "in", battleStateReceivePack(true, 77), 6)
			initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(2, 100, 200), 6.1)
			initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(2, 333), 7)
			addTestPlayerRoleGroupEntity(p, 101, 88, 1508)
			initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(3, 101, 200), 7.1)
			initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(3, 999), 8)
			final := p.DamageStatisticsSnapshot()
			if final.DamageDealt != 833 || final.OutgoingEvents != 2 || final.UnattributedOutgoingDamage != 999 || len(final.Encounters.Completed) != 1 {
				t.Fatal("later encounter, deduplication, or other-player isolation changed")
			}
			assertEncounterTotalsConserved(t, final)
			assertRoleTotalsReconcile(t, final)
		})
	}
}

func TestBasicInfoIdentityRejectsMissingInvalidAndConflictingEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      []byte
		direction string
		want      bool
	}{
		{"missing", appendVarintField(nil, fieldBasicInfoNotifyBirthday, 77), "in", false},
		{"zero", appendVarintField(nil, FieldBasicInfoNotifyID, 0), "in", false},
		{"wrong_wire", appendBytesField(nil, FieldBasicInfoNotifyID, []byte("77")), "in", false},
		{"out_of_range", appendVarintField(nil, FieldBasicInfoNotifyID, 1<<32), "in", false},
		{"conflicting_fields", appendVarintField(appendVarintField(nil, FieldBasicInfoNotifyID, 77), FieldBasicInfoNotifyID, 88), "in", false},
		{"wrong_direction", appendVarintField(nil, FieldBasicInfoNotifyID, 77), "out", false},
		{"repeated_same_identity", appendVarintField(appendVarintField(nil, FieldBasicInfoNotifyID, 77), FieldBasicInfoNotifyID, 77), "in", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, _ := initialProcessor(t)
			initialEncryptedFrame(t, p, CmdBasicInfoNotify, test.direction, test.body, 0.1)
			if p.PlayerRelevance.IsLocalPlayerID(77) != test.want {
				t.Fatal("unproven identity was accepted or explicit identity was lost")
			}
		})
	}
	p, _ := initialProcessor(t)
	p.Context.attribution.suspect = true
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 77), 0.1)
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 88), 0.2)
	if !p.PlayerRelevance.IsLocalPlayerID(77) || p.PlayerRelevance.IsLocalPlayerID(88) || !p.Context.attribution.suspect {
		t.Fatal("identity conflict expanded ownership or repaired lost scene context")
	}
	p.PlayerRelevance.Reset()
	p.SessionNumber = 0
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 77), 0.3)
	if len(p.PlayerRelevance.LocalPlayerIDs) != 0 {
		t.Fatal("identity before the protocol session was accepted")
	}
}

func TestInitialIdentityNeverCrossesProtocolSession(t *testing.T) {
	p, key := initialProcessor(t)
	captureID := p.DamageStatistics.CaptureSessionID
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 77), 0.1)
	initialEncryptedFrame(t, p, CmdJoinSceneNotify, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 200, func(e *RuntimeEntity) { e.EntityTypeCode, e.HasEntityType = EntityTypeMonster, true })
	initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1)
	initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2)
	initialHandshake(t, p, key, pairingAt(10))
	if p.SessionNumber != 2 || p.DamageStatistics.CaptureSessionID != captureID || p.DamageStatistics.statisticsIntervalID != 2 || len(p.PlayerRelevance.LocalPlayerIDs) != 0 || len(p.Entities.Entities) != 0 || p.DamageStatisticsSnapshot().DamageDealt != 0 {
		t.Fatal("handshake retained identity or statistics across intervals")
	}
	addTestPlayerRoleGroupEntity(p, 100, 88, 1508)
	addTestRuntimeEntity(p, 200, func(e *RuntimeEntity) { e.EntityTypeCode, e.HasEntityType = EntityTypeMonster, true })
	initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 11)
	initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(1, 200), 12)
	if s := p.DamageStatisticsSnapshot(); s.DamageDealt != 0 || s.UnattributedOutgoingDamage != 200 {
		t.Fatal("reused entity/RPC inherited prior ownership")
	}
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 88), 13)
	initialEncryptedFrame(t, p, CmdCombatSendPackRequest, "out", damageRequestBody(2, 100, 200), 14)
	initialEncryptedFrame(t, p, CmdCombatSendPackResponse, "in", damageResponseBody(2, 300), 15)
	s := p.DamageStatisticsSnapshot()
	if s.DamageDealt != 300 || s.OutgoingEvents != 1 || s.UnattributedOutgoingDamage != 200 {
		t.Fatal("new interval duplicated or retroactively forced earlier unknown damage")
	}
	assertRoleTotalsReconcile(t, s)
}

func TestVehicleFirstAttackUsesExplicitInitialPlayerIdentity(t *testing.T) {
	p, _ := initialProcessor(t)
	initialEncryptedFrame(t, p, CmdBasicInfoNotify, "in", appendVarintField(nil, FieldBasicInfoNotifyID, 77), 0.1)
	initialEncryptedFrame(t, p, CmdJoinSceneNotify, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 300, func(e *RuntimeEntity) {
		e.PlayerID, e.HasPlayerID = 77, true
		e.EntityTypeCode, e.HasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(e *RuntimeEntity) { e.EntityTypeCode, e.HasEntityType = EntityTypeMonster, true })
	event := testDamageEvent(p, 300, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.DamageContributorCandidate = &DamageContributorCandidate{Kind: VehicleKindMotorcycle, ActorType: "Vehicle", EntityType: "BattleVehicle", Status: ResolutionCorrelated}
	p.WriteDamageEvent(event)
	s := p.DamageStatisticsSnapshot()
	if s.DamageDealt != 321 || s.OutgoingEvents != 1 || s.UnassignedRole.DamageDealt != 0 || s.Vehicle == nil || s.Vehicle.DamageDealt != 321 || len(s.Roles) != 0 || p.Stats.PlayerRoleReports != 0 {
		t.Fatal("initial vehicle damage was lost or forced into a character role")
	}
}
