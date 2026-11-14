//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEffectFeedLeavesLifecycleJSONLUnchanged(t *testing.T) {
	var withTracker, withoutTracker []byte
	run := func(disable bool) []byte {
		p := newEffectTestProcessor()
		var lifecycle bytes.Buffer
		p.buffLifecycle = &lifecycle
		if disable {
			p.effects = nil
		}
		deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
		deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataBuffStackCountNotify, effectStackPayload(1, 2, 30, 30)))
		deliverEffectMessage(t, p, 2, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(1, 200, 100)))
		return lifecycle.Bytes()
	}
	withTracker, withoutTracker = run(false), run(true)
	if len(withTracker) == 0 || string(withTracker) != string(withoutTracker) {
		t.Fatalf("lifecycle JSONL differs with the tracker enabled:\n%s\n---\n%s", withTracker, withoutTracker)
	}
}

func TestEffectFeedWritesFinalStateAsOneJSONLine(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	p.currentFrameTime = effectAt(10)
	var out bytes.Buffer
	if err := p.writeEffectState(&out); err != nil {
		t.Fatalf("write effect state: %v", err)
	}
	if bytes.Count(out.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("effect state is not a single line: %q", out.String())
	}
	var snapshot EffectSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode effect state: %v", err)
	}
	effect := onlyEffect(t, snapshot)
	requireNear(t, "remaining at the last frame time", effect.RemainingSeconds, 20)
	if snapshot.QueryTime == nil || !snapshot.QueryTime.Equal(effectAt(10)) {
		t.Fatalf("query time = %v, want the last frame time", snapshot.QueryTime)
	}
}

func TestEffectSemanticConsoleFieldsAreLabelledAndNeverNameTheEffect(t *testing.T) {
	duration, limit := 14.8, uint64(3)
	readable := &EffectStaticInfo{StaticDurationSeconds: &duration, StackLimit: &limit, DeveloperTags: []string{"战斗状态.异常状态.基础元素异常.风蚀效应"}}
	got := formatEffectSemanticFields(10041000, readable)
	for _, want := range []string{"effectId=10041000", "semantic=definition_linked_name_unresolved", "staticDuration_s=14.800", "staticStackLimit=3", "devTag[internal]=战斗状态.异常状态.基础元素异常.风蚀效应"} {
		if !strings.Contains(got, want) {
			t.Errorf("console fields %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "name=") {
		t.Errorf("a name field was produced without a name source: %q", got)
	}
	hashed := formatEffectSemanticFields(1508300103, &EffectStaticInfo{DeveloperTags: []string{"角色.dc777977.cf4dcd3d.7315298d.5941a58c"}})
	if !strings.Contains(hashed, "devTag[internal]=unreadable(1)") || strings.Contains(hashed, "dc777977") {
		t.Errorf("hashed tag was shown as readable: %q", hashed)
	}
	if bare := formatEffectSemanticFields(70051, &EffectStaticInfo{}); strings.Contains(bare, "devTag") || strings.Contains(bare, "staticDuration") {
		t.Errorf("absent parameters were invented: %q", bare)
	}
}

func TestEffectSemanticConsoleFieldsFollowApplyAndHandleLink(t *testing.T) {
	p := newEffectTestProcessor()
	p.masters = &gameMaster{buffDefinitions: map[uint64]struct{}{10041000: {}}}
	apply := uint64(10041000)
	link := uint64(10041000)
	if got := p.effectSemanticConsoleFields(buffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY", ID: &apply}); !strings.Contains(got, "effectId=10041000") || !strings.Contains(got, "definition_linked_name_unresolved") {
		t.Errorf("apply row fields = %q", got)
	}
	if got := p.effectSemanticConsoleFields(buffLifecycleAuditRow{EventKind: "BUFF_STACK_COUNT_CHANGE", HandleLinkedID: &link, HandleLinkedStatus: resolutionCorrelated}); !strings.Contains(got, "effectId=10041000") {
		t.Errorf("handle-linked row fields = %q", got)
	}
	// An unconfirmed handle link must not attribute the update to an effect id.
	if got := p.effectSemanticConsoleFields(buffLifecycleAuditRow{EventKind: "BUFF_STACK_COUNT_CHANGE", HandleLinkedID: &link, HandleLinkedStatus: resolutionUnknown}); got != "" {
		t.Errorf("unconfirmed handle link produced fields: %q", got)
	}
	// Request notifications describe no effect instance.
	if got := p.effectSemanticConsoleFields(buffLifecycleAuditRow{EventKind: "BUFF_APPLY_REQUEST_NOTIFY", ID: &apply}); got != "" {
		t.Errorf("request notification produced fields: %q", got)
	}
	unknown := uint64(1312010090)
	if got := p.effectSemanticConsoleFields(buffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY", ID: &unknown}); !strings.Contains(got, "semantic=definition_unresolved") {
		t.Errorf("unlinked id fields = %q", got)
	}
}

func TestEffectFeedReportsCorrelatedTermNameWithoutPromotingIt(t *testing.T) {
	p := newEffectTestProcessor()
	p.masters = &gameMaster{
		buffDefinitions: map[uint64]struct{}{10041000: {}, 10040002: {}},
		buffStatics:     map[uint64]buffStaticParameters{10041000: {stackLimit: 3, hasStackLimit: true}},
		buffTerms:       map[uint64]effectTermRecord{10041000: {termID: 850009, name: "風蝕效應", description: "持續時造成氣動傷害", status: resolutionCorrelated}},
	}
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 10041000, 200, 100, 1, 14.8, 14.8)))
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(2, 10040002, 200, 100, 1, 2, 2)))
	byHandle := map[uint64]EffectInstanceSnapshot{}
	for _, effect := range p.effects.Snapshot().Effects {
		byHandle[effect.HandleID] = effect
	}
	named := byHandle[1]
	if named.SemanticStatus != EffectSemanticNameCorrelated || named.Static == nil || named.Static.Term == nil ||
		named.Static.Term.Name != "風蝕效應" || named.Static.Term.Status != string(resolutionCorrelated) {
		t.Fatalf("named effect = %+v static=%+v", named, named.Static)
	}
	if named.DefinitionStatus != string(resolutionCorrelated) {
		t.Fatalf("definition status = %s", named.DefinitionStatus)
	}
	// A linked effect without a term keeps its id and an unresolved name.
	plain := byHandle[2]
	if plain.SemanticStatus != EffectSemanticNameUnresolved || (plain.Static != nil && plain.Static.Term != nil) {
		t.Fatalf("effect without a term = %+v", plain)
	}
	// The tracker owns its copy of the term.
	named.Static.Term.Name = "mutated"
	for _, effect := range p.effects.Snapshot().Effects {
		if effect.HandleID == 1 && effect.Static.Term.Name != "風蝕效應" {
			t.Fatal("tracker state changed through a snapshot term")
		}
	}

	apply := uint64(10041000)
	fields := p.effectSemanticConsoleFields(buffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY", ID: &apply})
	for _, want := range []string{"semantic=definition_linked_name_correlated", `name="風蝕效應"`, "nameStatus=CORRELATED", "nameSource=Term850009"} {
		if !strings.Contains(fields, want) {
			t.Errorf("console fields %q lack %q", fields, want)
		}
	}
}

// newEffectTestProcessor has a local role entity 100, a second local role 101,
// monsters 200 and 201, and an unclassified entity 300.
func newEffectTestProcessor() *processor {
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.sessionNumber = 1
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 201, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9002, true })
	addTestRuntimeEntity(p, 300, nil)
	return p
}

func TestEffectFeedUpgradesUnknownRelationWhenEvidenceArrives(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 102, 1, 30, 30)))
	if effect := onlyEffect(t, p.effects.Snapshot()); effect.SourceRelation != EffectRelationUnknown {
		t.Fatalf("source relation before the role is known = %s", effect.SourceRelation)
	}
	observeTestLocalRole(p, 102)
	addTestRuntimeEntity(p, 102, nil)
	deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataBuffDurationNotify, effectDurationPayload(1, 30, 30)))
	if effect := onlyEffect(t, p.effects.Snapshot()); effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("source relation after the role is known = %s", effect.SourceRelation)
	}
}

func TestEffectFeedRefreshesHolderRelationWhenLocalTeamBecomesKnown(t *testing.T) {
	p := newEffectTestProcessor()
	// Holder 102 is not yet known as a local role when its snapshot row arrives and gets no later message.
	deliverEffectSnapshot(t, p, 1, 102, 0, effectSnapshotResponse(165, 1001002004, 102, 1, -1, -1))
	if effect := onlyEffect(t, p.effects.Snapshot()); effect.HolderRelation != EffectRelationUnknown {
		t.Fatalf("holder relation before the team is known = %s", effect.HolderRelation)
	}
	addTestRuntimeEntity(p, 102, nil)
	observeTestLocalRole(p, 102)
	effect := onlyEffect(t, p.effects.Snapshot())
	if effect.HolderRelation != EffectRelationLocalRole || effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("relations after the team is known = %s/%s, want local_role/local_role", effect.HolderRelation, effect.SourceRelation)
	}
}

func TestEffectFeedLabelsEveryLocalTeamRoleAndKeepsOtherPlayersUnknown(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.sessionNumber = 1
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true) // team role never reported as the current entity
	addTestPlayerRoleGroupEntity(p, 102, 88, 1312)       // another player's role
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 300, func(entity *runtimeEntity) {
		entity.summonerID, entity.hasSummonerID = 101, true // a summon of the second team role
	})
	// Each player's Proto_PlayerEntity holds team-level effects; only the local player's counts as local.
	playerEntity := func(playerID uint64) func(*runtimeEntity) {
		return func(entity *runtimeEntity) {
			entity.entityTypeCode, entity.hasEntityType = entityTypePlayerEntity, true
			entity.playerID, entity.hasPlayerID = playerID, true
		}
	}
	addTestRuntimeEntity(p, 400, playerEntity(77))
	addTestRuntimeEntity(p, 401, playerEntity(88))

	for entityID, want := range map[uint64]EffectEntityRelation{
		100: EffectRelationLocalRole, 101: EffectRelationLocalRole, 102: EffectRelationUnknown,
		200: EffectRelationNonPlayer, 300: EffectRelationLocalOwned,
		400: EffectRelationLocalPlayer, 401: EffectRelationUnknown,
	} {
		if got := p.effectEntityRelation(entityID); got != want {
			t.Errorf("entity %d relation = %s, want %s", entityID, got, want)
		}
	}
}

// TestEffectFeedPlayerEntityHoldsMonsterDebuffs is the event-level shape of the Glacio case: a monster applies an element
// ailment to the local player's Proto_PlayerEntity, which is on the team's side although it is not a role.
func TestEffectFeedPlayerEntityHoldsMonsterDebuffs(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.sessionNumber = 1
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 400, func(entity *runtimeEntity) {
		entity.entityTypeCode, entity.hasEntityType = entityTypePlayerEntity, true
		entity.playerID, entity.hasPlayerID = 77, true
	})
	deliverEffectMessage(t, p, 0, effectPackBody(400, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(171, 10010000, 400, 200, 1, 15, 15)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 1)))
	if effect.HolderRelation != EffectRelationLocalPlayer || effect.SourceRelation != EffectRelationNonPlayer {
		t.Fatalf("relations = %s/%s, want local_player/non_player", effect.HolderRelation, effect.SourceRelation)
	}
	if got := effectDisplayFor(effect); got.Eligibility == EffectDisplayNotLocal || got.Target != EffectTargetTeam {
		t.Fatalf("display = %+v, want a team-side effect", got)
	}
}

func TestEffectFeedTracksDecodedApplyStackAndRemove(t *testing.T) {
	p := newEffectTestProcessor()

	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(171, 10041000, 200, 100, 1, 14.8, 14.8)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 1)))
	if effect.HolderEntityID != 200 || effect.HandleID != 171 || effect.EffectID == nil || *effect.EffectID != 10041000 {
		t.Fatalf("decoded apply = %+v", effect)
	}
	if effect.HolderRelation != EffectRelationNonPlayer || effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("relations = %s/%s, want non_player/local_role", effect.HolderRelation, effect.SourceRelation)
	}
	requireNear(t, "remaining one second after apply", effect.RemainingSeconds, 13.8)

	// A stack message carries a fresh remaining time for the same handle.
	deliverEffectMessage(t, p, 4, effectPackBody(200, fieldCombatNotifyDataBuffStackCountNotify, effectStackPayload(171, 2, 14.8, 14.8)))
	deliverEffectMessage(t, p, 4, effectPackBody(200, fieldCombatNotifyDataBuffDurationNotify, effectDurationPayload(171, 14.8, 14.8)))
	effect = onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 5)))
	if effect.StackCount == nil || *effect.StackCount != 2 {
		t.Fatalf("stack = %v, want 2", effect.StackCount)
	}
	requireNear(t, "remaining one second after refresh", effect.RemainingSeconds, 13.8)

	deliverEffectMessage(t, p, 18.9, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(171, 200, 100)))
	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 20))
	if len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 1 || snapshot.RecentEnded[0].Lifecycle != EffectLifecycleConfirmedRemoved {
		t.Fatalf("after remove: %+v", snapshot)
	}
	requireNear(t, "expiry delta", snapshot.RecentEnded[0].ExpiryDeltaSeconds, 0.1)
}

func TestEffectFeedSeparatesSameEffectOnDifferentHolders(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(7, 555, 200, 100, 1, 5, 5)))
	deliverEffectMessage(t, p, 0, effectPackBody(201, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(7, 555, 201, 100, 1, 5, 5)))
	deliverEffectMessage(t, p, 0, effectPackBody(100, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(8, 555, 100, 100, 1, 5, 5)))

	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 1))
	if len(snapshot.Effects) != 3 {
		t.Fatalf("effects = %+v, want three isolated instances", snapshot.Effects)
	}
	deliverEffectMessage(t, p, 2, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(7, 200, 100)))
	snapshot = p.effects.SnapshotAt(effectQueryFor(p, 3))
	if len(snapshot.Effects) != 2 {
		t.Fatalf("a REMOVE on holder 200 changed %d instances", 3-len(snapshot.Effects))
	}
	var selfBuff *EffectInstanceSnapshot
	for i := range snapshot.Effects {
		if snapshot.Effects[i].HolderEntityID == 100 {
			selfBuff = &snapshot.Effects[i]
		}
	}
	if selfBuff == nil || selfBuff.HolderRelation != EffectRelationLocalRole || selfBuff.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("self-applied effect on a local role = %+v", selfBuff)
	}
}

func TestEffectFeedDoesNotPromoteUnknownEntitiesToPlayers(t *testing.T) {
	p := newEffectTestProcessor()
	// Source 999 was never seen; holder 300 is a runtime entity with no monster or player evidence.
	deliverEffectMessage(t, p, 0, effectPackBody(300, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 300, 999, 1, 5, 5)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 1)))
	if effect.HolderRelation != EffectRelationUnknown || effect.SourceRelation != EffectRelationUnknown {
		t.Fatalf("unknown entities were classified: %s/%s", effect.HolderRelation, effect.SourceRelation)
	}
	if effect.SourceEntityID == nil || *effect.SourceEntityID != 999 {
		t.Fatalf("raw source entity lost: %+v", effect)
	}
}

func TestEffectFeedExcludesMonsterAppliedMonsterEffects(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 201, 1, 5, 5)))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.ExcludedUnrelated != 1 {
		t.Fatalf("NPC-versus-NPC effect was tracked: %+v", snapshot)
	}
}

func TestEffectFeedRequiresCorrelatedHolder(t *testing.T) {
	p := newEffectTestProcessor()
	// TargetEntityId 200 does not match CombatCommon.EntityId 201: the holder is not established.
	deliverEffectMessage(t, p, 0, effectPackBody(201, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 5, 5)))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.SkippedUncorrelatedHolder != 1 {
		t.Fatalf("uncorrelated apply was tracked: %+v", snapshot)
	}
}

func TestEffectFeedIgnoresRequestNotifications(t *testing.T) {
	p := newEffectTestProcessor()
	apply := appendVarintField(appendVarintField(nil, fieldApplyBuffS2cRequestNotifyID, 71001), fieldApplyBuffS2cRequestNotifyInstigatorID, 100) // ApplyBuffS2CRequestNotify
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyBuffS2CRequestNotify, apply))
	remove := appendVarintField(appendVarintField(nil, fieldRemoveBuffByIDS2cRequestNotifyBuffID, 71001), fieldRemoveBuffByIDS2cRequestNotifyInstigatorID, 100) // RemoveBuffByIdS2CRequestNotify
	deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify, remove))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters != (EffectCounters{}) {
		t.Fatalf("request notifications reached the tracker: %+v", snapshot)
	}
}

func TestEffectFeedInstantEffectsCreateNoLifecycle(t *testing.T) {
	p := newEffectTestProcessor()
	payload := appendVarintField(nil, fieldApplyGameplayEffectNotifyHandle, ^uint64(1)) // Handle -2 with no duration fields
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyID, 70052)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyEntityID, 100)
	payload = appendVarintField(payload, fieldApplyGameplayEffectNotifyInstigatorID, 100)
	deliverEffectMessage(t, p, 0, effectPackBody(100, fieldCombatNotifyDataApplyGameplayEffectNotify, payload))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.InstantApplies != 1 {
		t.Fatalf("instant effect result: %+v", snapshot)
	}
}

func TestEffectFeedUnknownDurationStaysActiveWithoutCountdown(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(100, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(3, 10044000, 100, 100, 1, -1, -1)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 30)))
	if effect.Lifecycle != EffectLifecycleActive || effect.RemainingStatus != EffectRemainingUnknown || effect.RemainingReason != "no_server_duration" || effect.RemainingSeconds != nil {
		t.Fatalf("effect with a -1 duration = %+v", effect)
	}
	deliverEffectMessage(t, p, 31, effectPackBody(100, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(3, 100, 100)))
	if snapshot := p.effects.Snapshot(); len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 1 || snapshot.RecentEnded[0].ExpiryDeltaSeconds != nil {
		t.Fatalf("remove of an effect without a finite duration: %+v", snapshot)
	}
}

func TestEffectFeedStatisticsIntervalResetIsolatesEffects(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	snapshot := p.effects.SnapshotAndResetAt(effectQueryFor(p, 1))
	if len(snapshot.Effects) != 1 || snapshot.StatisticsIntervalID != 1 {
		t.Fatalf("closing snapshot = %+v", snapshot)
	}
	deliverEffectMessage(t, p, 2, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(2, 43, 200, 100, 1, 30, 30)))
	snapshot = p.effects.SnapshotAt(effectQueryFor(p, 3))
	effect := onlyEffect(t, snapshot)
	if snapshot.StatisticsIntervalID != 2 || effect.HandleID != 2 || snapshot.Counters.Applies != 1 {
		t.Fatalf("next interval = %+v", snapshot)
	}
}

func TestEffectFeedDoesNotChangeDamageStatistics(t *testing.T) {
	p := newEffectTestProcessor()
	before := p.damageStatistics.Snapshot()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(1, 200, 100)))
	after := p.damageStatistics.Snapshot()
	if before.DamageDealt != after.DamageDealt || before.DamageTaken != after.DamageTaken || before.StatisticsIntervalID != after.StatisticsIntervalID ||
		before.OutgoingEvents != after.OutgoingEvents || before.IncomingEvents != after.IncomingEvents {
		t.Fatalf("effect messages changed damage statistics: %+v -> %+v", before, after)
	}
	if p.effects.captureSessionID != p.damageStatistics.captureSessionID {
		t.Fatalf("effect session %d differs from damage statistics session %d", p.effects.captureSessionID, p.damageStatistics.captureSessionID)
	}
}

func TestEffectFeedBoundaryDropsLiveState(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	p.effects.EndLiveState()
	deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(1, 200, 100)))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.BoundaryDrops != 1 || snapshot.Counters.RemovesWithoutInstance != 1 {
		t.Fatalf("state after boundary = %+v", snapshot)
	}
}

func TestEffectFeedSnapshotRowCreatesBaselineInstanceThatARemoveConfirms(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectSnapshot(t, p, 1, 200, 0, effectSnapshotResponse(168, 640012035, 100, 1, 3, 3.001))
	deliverEffectSnapshot(t, p, 2, 100, 0, effectSnapshotResponse(165, 1001002004, 100, 1, -1, -1))

	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 1))
	if len(snapshot.Effects) != 2 || snapshot.Counters.SnapshotInstances != 2 || snapshot.Counters.Applies != 0 {
		t.Fatalf("snapshot baseline = %+v", snapshot)
	}
	var timed, permanent EffectInstanceSnapshot
	for _, effect := range snapshot.Effects {
		if effect.HolderEntityID == 200 {
			timed = effect
		} else {
			permanent = effect
		}
	}
	if timed.Origin != EffectOriginSnapshot || timed.AppliedAt != nil || !timed.FirstObservedAt.Equal(effectAt(0)) {
		t.Fatalf("snapshot-origin instance claims an application time: %+v", timed)
	}
	requireNear(t, "snapshot remaining one second later", timed.RemainingSeconds, 2.001)
	if permanent.HolderRelation != EffectRelationLocalRole || permanent.RemainingStatus != EffectRemainingUnknown || permanent.RemainingReason != "no_server_duration" {
		t.Fatalf("permanent local buff = %+v", permanent)
	}

	// The REMOVE that follows names the same (holder, handle): it is no longer an orphan.
	deliverEffectMessage(t, p, 3.02, effectPackBody(200, fieldCombatNotifyDataRemoveGameplayEffectNotify, effectRemovePayload(168, 200, 100)))
	snapshot = p.effects.SnapshotAt(effectQueryFor(p, 4))
	if snapshot.Counters.Removes != 1 || snapshot.Counters.RemovesWithoutInstance != 0 || len(snapshot.RecentEnded) != 1 {
		t.Fatalf("remove after a snapshot baseline = %+v", snapshot.Counters)
	}
	requireNear(t, "expiry delta", snapshot.RecentEnded[0].ExpiryDeltaSeconds, 0.019)
}

func TestEffectFeedRepeatedSnapshotDumpsDoNotDuplicateInstances(t *testing.T) {
	p := newEffectTestProcessor()
	for i := 0; i < 3; i++ {
		deliverEffectSnapshot(t, p, uint16(i+1), 200, float64(i), effectSnapshotResponse(168, 640012035, 100, 1, 30, 30))
	}
	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 2.5))
	effect := onlyEffect(t, snapshot)
	if snapshot.Counters.SnapshotInstances != 1 || snapshot.Counters.DuplicateSnapshots != 2 {
		t.Fatalf("counters = %+v", snapshot.Counters)
	}
	requireNear(t, "remaining follows the newest dump", effect.RemainingSeconds, 29.5)
}

func TestEffectFeedSnapshotWithoutRequestMatchIsNotAttributed(t *testing.T) {
	p := newEffectTestProcessor()
	frame := gameFrame{commandID: cmdEntityActiveResponse, direction: "in", token: 9, timestamp: effectAt(0)}
	if err := p.handleActiveResponse(frame, effectSnapshotResponse(168, 640012035, 100, 1, 30, 30)); err != nil {
		t.Fatal(err)
	}
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.SnapshotsSkipped != 1 {
		t.Fatalf("snapshot row without a request was attributed: %+v", snapshot)
	}
}

func TestEffectFeedApplyAfterSnapshotUpgradesOrigin(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectSnapshot(t, p, 1, 200, 0, effectSnapshotResponse(7, 555, 100, 1, 30, 30))
	deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(7, 555, 200, 100, 1, 30, 30)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 2)))
	if effect.Origin != EffectOriginApply || effect.AppliedAt == nil {
		t.Fatalf("origin after a confirming APPLY = %+v", effect)
	}
}

func TestEffectFeedLinksStaticDefinitionOnlyByExactRow(t *testing.T) {
	p := newEffectTestProcessor()
	p.masters = &gameMaster{buffDefinitions: map[uint64]struct{}{10041000: {}}}
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 10041000, 200, 100, 1, 30, 30)))
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(2, 1312010090, 200, 100, 1, 30, 30)))
	byHandle := map[uint64]EffectInstanceSnapshot{}
	for _, effect := range p.effects.Snapshot().Effects {
		byHandle[effect.HandleID] = effect
	}
	if got := byHandle[1].DefinitionStatus; got != string(resolutionCorrelated) {
		t.Fatalf("effect with a static row: definitionStatus = %s", got)
	}
	// An id without a row is still tracked, keeps its raw id and is not named or dropped.
	unnamed := byHandle[2]
	if unnamed.DefinitionStatus != string(resolutionUnknown) || unnamed.EffectID == nil || *unnamed.EffectID != 1312010090 || unnamed.Lifecycle != EffectLifecycleActive {
		t.Fatalf("effect without a static row = %+v", unnamed)
	}
}

func TestEffectFeedAttachesStaticDefinitionWithoutResolvingAName(t *testing.T) {
	p := newEffectTestProcessor()
	p.masters = &gameMaster{
		buffDefinitions: map[uint64]struct{}{10041000: {}, 70000045: {}},
		buffStatics: map[uint64]buffStaticParameters{
			10041000: {durationSeconds: 14.8, hasDuration: true, stackLimit: 3, hasStackLimit: true, durationPolicy: 2, hasDurationPolicy: true, tags: []string{"a.b.c"}},
		},
	}
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 10041000, 200, 100, 1, 14.8, 14.8)))
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(2, 70000045, 200, 100, 1, -1, -1)))
	deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(3, 1312010090, 200, 100, 1, 5, 5)))

	byHandle := map[uint64]EffectInstanceSnapshot{}
	for _, effect := range p.effects.Snapshot().Effects {
		byHandle[effect.HandleID] = effect
	}
	aero := byHandle[1]
	if aero.SemanticStatus != EffectSemanticNameUnresolved || aero.Static == nil || aero.Static.StaticDurationSeconds == nil ||
		*aero.Static.StaticDurationSeconds != 14.8 || *aero.Static.StackLimit != 3 || len(aero.Static.DeveloperTags) != 1 {
		t.Fatalf("linked effect = %+v static=%+v", aero, aero.Static)
	}
	// A definition with no parameters is still linked, and the name is still unresolved.
	if bare := byHandle[2]; bare.SemanticStatus != EffectSemanticNameUnresolved || bare.DefinitionStatus != string(resolutionCorrelated) {
		t.Fatalf("definition without parameters = %+v", bare)
	}
	// An id with no static row is tracked and flagged unresolved, without a name or parameters.
	missing := byHandle[3]
	if missing.SemanticStatus != EffectSemanticDefinitionUnresolved || missing.Static != nil || missing.EffectID == nil || *missing.EffectID != 1312010090 {
		t.Fatalf("effect without a definition = %+v", missing)
	}
	// The snapshot owns its static block.
	*aero.Static.StackLimit = 99
	aero.Static.DeveloperTags[0] = "mutated"
	again := p.effects.Snapshot()
	for _, effect := range again.Effects {
		if effect.HandleID == 1 && (*effect.Static.StackLimit != 3 || effect.Static.DeveloperTags[0] != "a.b.c") {
			t.Fatalf("tracker state changed through a snapshot: %+v", effect.Static)
		}
	}
}
