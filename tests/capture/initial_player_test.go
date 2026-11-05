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

func initialHandshake(t *testing.T, p *processor, key *rsa.PrivateKey, at time.Time) {
	t.Helper()
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	body := appendBytesField(appendVarintField(nil, 2, 2), 3, ciphertext)
	p.handleFrame(gameFrame{commandID: 112, direction: "in", timestamp: at, body: body})
}

func initialEncryptedFrame(t *testing.T, p *processor, command uint16, direction string, body []byte, seconds float64) {
	t.Helper()
	const sequence = 1
	plain := append([]byte{}, body...)
	if len(plain) > 0 {
		plain[sequence%len(plain)] ^= p.session[sequence&31]
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(p.session)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, len(plain))
	for i := 0; i < len(plain); i += aes.BlockSize {
		block.Encrypt(encoded[i:i+aes.BlockSize], plain[i:i+aes.BlockSize])
	}
	p.handleFrame(gameFrame{commandID: command, direction: direction, sequence: sequence, timestamp: pairingAt(seconds), body: encoded})
}

func initialJoinBody() []byte {
	player := appendVarintField(nil, 1, 77)
	role := appendVarintField(appendVarintField(nil, 1, 1409), 2, 100)
	player = appendBytesField(player, 11, appendBytesField(nil, 2, role))
	scene := appendBytesField(nil, 4, player)
	return appendBytesField(nil, 5, scene)
}

func initialProcessor(t *testing.T) (*processor, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := newPairingTestProcessor()
	p.stdout, p.diagnostic = io.Discard, io.Discard
	p.rsa = &staticRSA{e: key.E, n: key.N, d: key.D}
	p.intervalPerProtocolSession = true
	initialHandshake(t, p, key, pairingAt(0))
	return p, key
}

func TestFirstEncounterCountsBeforePeriodicRoleReport(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "identity_before_scene", false: "identity_after_scene"}[before], func(t *testing.T) {
			p, _ := initialProcessor(t)
			captureID := p.damageStatistics.captureSessionID
			identity := appendVarintField(nil, 15, 77)
			if before {
				initialEncryptedFrame(t, p, 15380, "in", identity, 0.1)
			}
			initialEncryptedFrame(t, p, 18559, "in", initialJoinBody(), 0.2)
			if !before {
				initialEncryptedFrame(t, p, 15380, "in", identity, 0.3)
			}
			addTestRuntimeEntity(p, 200, func(e *runtimeEntity) { e.entityTypeCode, e.hasEntityType = entityTypeMonster, true })
			initialEncryptedFrame(t, p, 25706, "in", battleStateReceivePack(true, 77), 1)
			initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(1, 100, 200), 1.1)
			initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(1, 500), 2)
			first := p.damageStatisticsSnapshot()
			if first.DamageDealt != 500 || first.OutgoingEvents != 1 || len(first.Roles) != 1 || first.Encounters.Current == nil || first.Encounters.Current.Totals.DamageDealt != 500 {
				t.Fatalf("first encounter lost paired damage: confirmed=%d unattributed=%d roles=%d state=%s", first.DamageDealt, first.UnattributedOutgoingDamage, len(first.Roles), first.Encounters.State)
			}
			if first.CaptureSessionID != captureID || first.StatisticsIntervalID != 1 || p.sessionNumber != 1 || p.stats.playerRoleReports != 0 {
				t.Fatal("initial session or interval changed")
			}
			view := p.viewSnapshot()
			if view.Statistics.DamageDealt != 500 || len(view.Roles) != 1 || view.Encounters.Current.DPS.Value <= 0 {
				t.Fatal("first damage did not reach the shared UI snapshot")
			}
			initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(1, 500), 2.1)
			initialEncryptedFrame(t, p, 25706, "in", battleStateReceivePack(false, 77), 3)
			p.observeEncounterClock(pairingAt(5))
			initialEncryptedFrame(t, p, 25706, "in", battleStateReceivePack(true, 77), 6)
			initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(2, 100, 200), 6.1)
			initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(2, 333), 7)
			addTestPlayerRoleGroupEntity(p, 101, 88, 1508)
			initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(3, 101, 200), 7.1)
			initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(3, 999), 8)
			final := p.damageStatisticsSnapshot()
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
		{"missing", appendVarintField(nil, 1, 77), "in", false},
		{"zero", appendVarintField(nil, 15, 0), "in", false},
		{"wrong_wire", appendBytesField(nil, 15, []byte("77")), "in", false},
		{"out_of_range", appendVarintField(nil, 15, 1<<32), "in", false},
		{"conflicting_fields", appendVarintField(appendVarintField(nil, 15, 77), 15, 88), "in", false},
		{"wrong_direction", appendVarintField(nil, 15, 77), "out", false},
		{"repeated_same_identity", appendVarintField(appendVarintField(nil, 15, 77), 15, 77), "in", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, _ := initialProcessor(t)
			initialEncryptedFrame(t, p, 15380, test.direction, test.body, 0.1)
			if p.playerRelevance.isLocalPlayerID(77) != test.want {
				t.Fatal("unproven identity was accepted or explicit identity was lost")
			}
		})
	}
	p, _ := initialProcessor(t)
	p.context.attribution.suspect = true
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 77), 0.1)
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 88), 0.2)
	if !p.playerRelevance.isLocalPlayerID(77) || p.playerRelevance.isLocalPlayerID(88) || !p.context.attribution.suspect {
		t.Fatal("identity conflict expanded ownership or repaired lost scene context")
	}
	p.playerRelevance.reset()
	p.sessionNumber = 0
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 77), 0.3)
	if len(p.playerRelevance.localPlayerIDs) != 0 {
		t.Fatal("identity before the protocol session was accepted")
	}
}

func TestInitialIdentityNeverCrossesProtocolSession(t *testing.T) {
	p, key := initialProcessor(t)
	captureID := p.damageStatistics.captureSessionID
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 77), 0.1)
	initialEncryptedFrame(t, p, 18559, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 200, func(e *runtimeEntity) { e.entityTypeCode, e.hasEntityType = entityTypeMonster, true })
	initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(1, 100, 200), 1)
	initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(1, 500), 2)
	initialHandshake(t, p, key, pairingAt(10))
	if p.sessionNumber != 2 || p.damageStatistics.captureSessionID != captureID || p.damageStatistics.statisticsIntervalID != 2 || len(p.playerRelevance.localPlayerIDs) != 0 || len(p.entities.entities) != 0 || p.damageStatisticsSnapshot().DamageDealt != 0 {
		t.Fatal("handshake retained identity or statistics across intervals")
	}
	addTestPlayerRoleGroupEntity(p, 100, 88, 1508)
	addTestRuntimeEntity(p, 200, func(e *runtimeEntity) { e.entityTypeCode, e.hasEntityType = entityTypeMonster, true })
	initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(1, 100, 200), 11)
	initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(1, 200), 12)
	if s := p.damageStatisticsSnapshot(); s.DamageDealt != 0 || s.UnattributedOutgoingDamage != 200 {
		t.Fatal("reused entity/RPC inherited prior ownership")
	}
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 88), 13)
	initialEncryptedFrame(t, p, 19470, "out", damageRequestBody(2, 100, 200), 14)
	initialEncryptedFrame(t, p, 20332, "in", damageResponseBody(2, 300), 15)
	s := p.damageStatisticsSnapshot()
	if s.DamageDealt != 300 || s.OutgoingEvents != 1 || s.UnattributedOutgoingDamage != 200 {
		t.Fatal("new interval duplicated or retroactively forced earlier unknown damage")
	}
	assertRoleTotalsReconcile(t, s)
}

func TestVehicleFirstAttackUsesExplicitInitialPlayerIdentity(t *testing.T) {
	p, _ := initialProcessor(t)
	initialEncryptedFrame(t, p, 15380, "in", appendVarintField(nil, 15, 77), 0.1)
	initialEncryptedFrame(t, p, 18559, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 300, func(e *runtimeEntity) {
		e.playerID, e.hasPlayerID = 77, true
		e.entityTypeCode, e.hasEntityType = 10, true
	})
	addTestRuntimeEntity(p, 200, func(e *runtimeEntity) { e.entityTypeCode, e.hasEntityType = entityTypeMonster, true })
	event := testDamageEvent(p, 300, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = 321
	event.DamageContributorCandidate = &damageContributorCandidate{ActorType: "Vehicle", Status: resolutionCorrelated}
	p.writeDamageEvent(event)
	s := p.damageStatisticsSnapshot()
	if s.DamageDealt != 321 || s.OutgoingEvents != 1 || s.UnassignedRole.DamageDealt != 321 || len(s.Roles) != 0 || p.stats.playerRoleReports != 0 {
		t.Fatal("initial vehicle damage was lost or forced into a character role")
	}
}
