package capture

import "testing"

func localOutgoingEvent(p *processor, rpcID uint64, amount int64) combatEvent {
	event := testDamageEvent(p, 100, 200)
	event.EventTypeCandidate = "DAMAGE_CANDIDATE"
	event.Damage = amount
	event.RPCID = rpcID
	return event
}

func TestSuspectAttributionKeepsUnreferencedEntityUnattributed(t *testing.T) {
	p := newPairingTestProcessor()
	p.context.invalidate("in", pairingAt(1), "test_gap")
	pairingFrame(p, 2)
	p.writeDamageEvent(localOutgoingEvent(p, 1, 100))
	got := p.damageStatistics.Snapshot()
	if got.DamageDealt != 0 || got.UnattributedOutgoingDamage != 100 {
		t.Fatalf("an unreferenced local entity was kept local while attribution is suspect: %+v", got)
	}
	if context := p.context.snapshot(); context.DowngradedEvents != 1 || context.RevalidatedEvents != 0 {
		t.Fatalf("context counters = %+v", context)
	}
}

func TestClientReferenceAfterLossRevalidatesThatEntityOnly(t *testing.T) {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508)
	p.observeLocalRoleEntity(101)
	p.context.invalidate("in", pairingAt(1), "test_gap")
	// The client names entity 100 in its own request after the loss began.
	sendDamageRequest(p, 2, 5)
	if context := p.context.snapshot(); context.ClientReferencedEntities != 2 {
		t.Fatalf("client references = %d, want attacker and target", context.ClientReferencedEntities)
	}
	p.writeDamageEvent(localOutgoingEvent(p, 6, 100))
	other := testDamageEvent(p, 101, 200)
	other.EventTypeCandidate, other.Damage, other.RPCID = "DAMAGE_CANDIDATE", 40, 7
	p.writeDamageEvent(other)
	got := p.damageStatistics.Snapshot()
	if got.DamageDealt != 100 || got.UnattributedOutgoingDamage != 40 {
		t.Fatalf("revalidation must cover exactly the referenced entity: %+v", got)
	}
	context := p.context.snapshot()
	if context.RevalidatedEvents != 1 || context.DowngradedEvents != 1 || context.State != ContextSuspect {
		t.Fatalf("context = %+v; per-entity revalidation must not clear the global suspect state", context)
	}
}

func TestReferenceBeforeLossDoesNotRevalidate(t *testing.T) {
	p := newPairingTestProcessor()
	sendDamageRequest(p, 1, 5) // attribution is valid: nothing is recorded
	p.context.invalidate("in", pairingAt(2), "test_gap")
	pairingFrame(p, 3)
	p.writeDamageEvent(localOutgoingEvent(p, 6, 100))
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 || got.UnattributedOutgoingDamage != 100 {
		t.Fatalf("a reference made before the loss revalidated the entity: %+v", got)
	}

	// A reference during one loss is not evidence about the next loss.
	sendDamageRequest(p, 4, 7)
	p.context.invalidate("in", pairingAt(5), "test_gap_2")
	pairingFrame(p, 6)
	p.writeDamageEvent(localOutgoingEvent(p, 8, 100))
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 || got.UnattributedOutgoingDamage != 200 {
		t.Fatalf("a reference from an earlier loss survived a new loss: %+v", got)
	}
}

func TestSceneJoinClearsClientReferences(t *testing.T) {
	p := newPairingTestProcessor()
	p.context.invalidate("in", pairingAt(1), "test_gap")
	sendDamageRequest(p, 2, 5)
	p.context.ongoingInbound = 1 // the loss is still open, so the scene join cannot revalidate attribution
	p.context.resetForScene(pairingAt(3), contextBasisSceneJoin)
	if context := p.context.snapshot(); context.ClientReferencedEntities != 0 || !p.context.attribution.suspect {
		t.Fatalf("scene join kept references or cleared suspicion: %+v", context)
	}
	pairingFrame(p, 4)
	p.writeDamageEvent(localOutgoingEvent(p, 6, 100))
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 0 {
		t.Fatalf("a reference to a previous scene's entity revalidated it: %+v", got)
	}
}

func TestClientReferenceRevalidatesIncomingTarget(t *testing.T) {
	p := newPairingTestProcessor()
	p.context.invalidate("in", pairingAt(1), "test_gap")
	sendDamageRequest(p, 2, 5) // names 100 as attacker
	incoming := testDamageEvent(p, 200, 100)
	incoming.EventTypeCandidate, incoming.Damage, incoming.RPCID = "DAMAGE_CANDIDATE", 70, 9
	p.writeDamageEvent(incoming)
	if got := p.damageStatistics.Snapshot(); got.DamageTaken != 70 || got.UnattributedIncomingDamage != 0 {
		t.Fatalf("incoming damage to a referenced local entity = %+v", got)
	}
}

func TestClientReferencesAreBounded(t *testing.T) {
	var c contextValidity
	c.attribution.invalidate(pairingAt(1), "test_gap")
	for id := uint64(1); id <= maxClientReferencedEntities+10; id++ {
		c.observeClientReference(id)
	}
	if len(c.clientReferenced) != maxClientReferencedEntities || c.revalidatesEntity(1) || !c.revalidatesEntity(maxClientReferencedEntities+10) {
		t.Fatalf("reference set is not a bounded FIFO: size=%d", len(c.clientReferenced))
	}
}
