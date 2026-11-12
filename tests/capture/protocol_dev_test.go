//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDamageResponseKeepsUnresolvedEntityVisible(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.combatEventsJSONL = &jsonl
	p.pending[42] = pendingDamageRequest{attackerEntityID: 25320, hasAttackerEntityID: true, damageID: 70010001, hasDamageID: true, damageSourceType: 1, hasDamageSourceType: true, bulletID: -1, hasBulletID: true}
	response := appendVarintField(nil, fieldDamageExecuteResponseErrorCode, 0)      // success
	response = appendVarintField(response, fieldDamageExecuteResponseImmuneType, 0) // not immune
	response = appendVarintField(response, fieldDamageExecuteResponseDamage, 9001)

	p.handleOneDamageResult(42, response)
	p.handleOneDamageResult(42, response)

	if stdout.Len() == 0 {
		t.Fatal("unresolved damage was hidden even though it was not proven unrelated")
	}
	if !strings.Contains(stdout.String(), "[PlayerTeamSide: UNCONFIRMED]") {
		t.Fatalf("unresolved relation was not labeled in normal output: %q", stdout.String())
	}
	if p.stats.printed != 1 || p.stats.duplicateResponses != 1 || p.stats.matchedCombatResultAbs != 9001 || p.stats.decodedMatchedEvents != 1 || p.stats.decodedCombatResultAbs != 9001 || p.stats.includedUnresolvedRelations != 1 || p.stats.suppressedUnrelatedEvents != 0 || p.stats.unclassifiedResult != 9001 {
		t.Fatalf("unexpected stats: visible=%d duplicates=%d visible_abs=%d decoded_events=%d decoded_abs=%d unresolved=%d suppressed=%d unclassified_abs=%d", p.stats.printed, p.stats.duplicateResponses, p.stats.matchedCombatResultAbs, p.stats.decodedMatchedEvents, p.stats.decodedCombatResultAbs, p.stats.includedUnresolvedRelations, p.stats.suppressedUnrelatedEvents, p.stats.unclassifiedResult)
	}
	if !strings.Contains(jsonl.String(), `"entityId":25320`) || strings.Contains(jsonl.String(), `"characterId":25320`) {
		t.Fatalf("raw entity identity was lost or promoted to a character: %q", jsonl.String())
	}
}

func TestDamageResponsePreservesRawResultsAndMarksHealingOnlyAsCandidate(t *testing.T) {
	var stdout, diagnostic, audit bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, &audit)
	p.masters = &gameMaster{damage: map[uint64]damageMasterRecord{
		120900505: {ID: 120900505, CalculateType: 1, SourceTable: "damage"},
	}}
	p.pending[9] = pendingDamageRequest{damageID: 120900505, hasDamageID: true}
	response := appendVarintField(nil, fieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, fieldDamageExecuteResponseImmuneType, 0)
	changeLife := int32(-700)
	serverResult := int32(-1311)
	response = appendVarintField(response, fieldDamageExecuteResponseChangeLife, uint64(uint32(changeLife))) // raw ChangeLife
	response = appendVarintField(response, fieldDamageExecuteResponseDamage, uint64(uint32(serverResult)))   // raw server result

	p.handleOneDamageResult(9, response)

	if p.stats.printed != 1 || p.stats.matchedCombatResultAbs != 1311 || p.stats.decodedMatchedEvents != 1 || p.stats.decodedCombatResultAbs != 1311 || p.stats.includedUnresolvedRelations != 1 || p.stats.suppressedUnrelatedEvents != 0 || p.stats.damageCandidateResult != 0 || p.stats.healingCandidateEvents != 1 || p.stats.healingCandidateResult != 1311 || p.stats.otherCandidateResult != 0 || p.stats.unclassifiedResult != 0 {
		t.Fatalf("candidate classification changed raw result accounting: %+v", p.stats)
	}
	var summary bytes.Buffer
	p.printSummary(&summary)
	for _, field := range []string{"visible_combat_result_abs=1311", "decoded_matched_events=1", "decoded_combat_result_abs=1311", "included_unresolved_relation_events=1", "suppressed_confirmed_unrelated_events=0", "decoded_damage_candidate_result_abs=0", "decoded_healing_candidate_result_abs=1311", "decoded_other_combat_candidate_result_abs=0", "decoded_unclassified_result_abs=0"} {
		if !strings.Contains(summary.String(), field) {
			t.Fatalf("summary missing semantic result bucket %q: %s", field, summary.String())
		}
	}
	if !strings.Contains(audit.String(), "\t-1311\t-700\t1\tHEALING_CANDIDATE\tCORRELATED\t") {
		t.Fatalf("audit did not preserve signed server fields and candidate provenance: %q", audit.String())
	}
	if strings.Contains(audit.String(), "VERIFIED\t") {
		t.Fatalf("cross-version CalculateType was promoted to VERIFIED: %q", audit.String())
	}
}

func TestCombatEventJSONLPreservesRawIDsAndUnknownResolution(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.combatEventsJSONL = &jsonl
	event := combatEvent{
		Timestamp: time.Date(2026, 10, 27, 1, 2, 3, 4000, time.UTC), SessionNumber: 3, RPCID: 91,
		SourceEntityID: 30298, HasSourceEntity: true, TargetEntityID: 30802, HasTargetEntity: true,
		SkillID: 280026010, DamageID: 28002601, HasDamageID: true, Damage: 177,
		ServerResultRaw: -177, HasServerResult: true, ChangeLifeRaw: -200, HasChangeLife: true,
		CharacterStatus: resolutionUnknown, CharacterNameStatus: resolutionUnknown,
		SkillStatus: resolutionUnknown, SkillNameStatus: resolutionUnknown, EventTypeStatus: resolutionUnknown,
		Source: entityIdentity{Runtime: runtimeEntity{entityID: 30298}, RuntimeStatus: resolutionUnknown},
		Target: entityIdentity{Runtime: runtimeEntity{entityID: 30802}, RuntimeStatus: resolutionUnknown},
	}
	p.writeDamageEvent(event)
	var got map[string]any
	if err := json.NewDecoder(&jsonl).Decode(&got); err != nil {
		t.Fatalf("decode event JSONL: %v", err)
	}
	if got["schemaVersion"] != float64(5) || got["eventType"] != "UNCLASSIFIED_RESULT" || got["sessionNumber"] != float64(3) || got["rpcId"] != float64(91) {
		t.Fatalf("event envelope = %#v", got)
	}
	source := got["source"].(map[string]any)
	target := got["target"].(map[string]any)
	if source["entityId"] != float64(30298) || target["entityId"] != float64(30802) {
		t.Fatalf("raw source/target IDs were lost: source=%#v target=%#v", source, target)
	}
	if _, exists := got["characterId"]; exists {
		t.Fatalf("unresolved event gained a top-level character ID: %#v", got)
	}
	credit := got["credit"].(map[string]any)
	if _, exists := credit["characterId"]; exists || credit["status"] != string(resolutionUnknown) {
		t.Fatalf("unknown credit was promoted: %#v", credit)
	}
	result := got["combatResult"].(map[string]any)
	if result["damageId"] != float64(28002601) || result["combatResultAbs"] != float64(177) || result["serverResultRaw"] != float64(-177) || result["classStatus"] != string(resolutionUnknown) {
		t.Fatalf("raw combat result evidence or unknown class changed: %#v", result)
	}
	runtimeFields := source["runtimeFields"].(map[string]any)
	runtimeEntityID := runtimeFields["entityId"].(map[string]any)
	if _, exists := result["sourceType"]; exists || runtimeEntityID["present"] != true || runtimeEntityID["value"] != float64(30298) {
		t.Fatalf("optional IDs or nested runtime fields malformed: source=%#v result=%#v", source, result)
	}
}

func TestFormatNormalLabelsHealingAsCandidateResult(t *testing.T) {
	event := combatEvent{Damage: 1311, EventTypeCandidate: "HEALING_CANDIDATE"}
	line := event.formatNormal()
	if !strings.Contains(line, "1311 [Healing Candidate Result]") || strings.Contains(line, "Damage Candidate Result") {
		t.Fatalf("healing output was not labeled separately: %s", line)
	}
}

func TestFormatNormalLabelsVerifiedMonsterInsteadOfUnknownCredit(t *testing.T) {
	event := combatEvent{
		Source: entityIdentity{
			HasMonster: true, Monster: masterRecord{ID: 602000019}, MonsterStatus: resolutionVerified,
		},
		Damage: 2151,
	}
	line := event.formatNormal()
	if !strings.Contains(line, "Credit:NonPlayerSource [Monster:602000019; VERIFIED]") || strings.Contains(line, "Credit:?") {
		t.Fatalf("verified monster source was presented as unknown credit: %q", line)
	}
}

func TestFormatNormalLabelsCorrelatedTemplateInsteadOfUnknownCredit(t *testing.T) {
	event := combatEvent{
		Source: entityIdentity{
			TemplateStatus:     resolutionCorrelated,
			TemplateCandidates: []sceneTemplateCandidate{{TemplateID: 602000019}},
		},
		Damage: 2151,
	}
	line := event.formatNormal()
	if !strings.Contains(line, "Credit:NonPlayerSource [Template:602000019; CORRELATED]") || strings.Contains(line, "Credit:?") {
		t.Fatalf("correlated non-player template was presented as unknown credit: %q", line)
	}
}

func TestCorrelatedRoleIsCreditNotSourceIdentity(t *testing.T) {
	event := combatEvent{
		SourceEntityID:      90,
		HasSourceEntity:     true,
		CharacterID:         1312,
		CharacterName:       "Role",
		CharacterNameStatus: resolutionVerified,
		CharacterStatus:     resolutionCorrelated,
		DamageID:            70010001,
		HasDamageID:         true,
		Damage:              100,
	}
	got := event.formatNormal()
	if !strings.Contains(got, "Unknown [Entity:90]") || !strings.Contains(got, "Credit:Role [CORRELATED; name VERIFIED]") {
		t.Fatalf("correlated role must be separate from source entity identity: %q", got)
	}
	if strings.Contains(got, "Role [CORRELATED] [Entity:90]") {
		t.Fatalf("correlated role was presented as the source entity: %q", got)
	}
}

func TestUnverifiedLocalizedSkillNameIsNotDisplayed(t *testing.T) {
	event := combatEvent{
		SkillID:         1312011,
		SkillName:       "Unverified label",
		SkillStatus:     resolutionVerified,
		SkillNameStatus: resolutionUnknown,
	}
	got := event.formatNormal()
	if !strings.Contains(got, "SkillId:1312011") || strings.Contains(got, "Unverified label") {
		t.Fatalf("unverified skill label was displayed: %q", got)
	}
}

func TestResolvedCharacterIDRemainsVisibleWhenNameIsUnknown(t *testing.T) {
	event := combatEvent{
		SourceEntityID:      90,
		HasSourceEntity:     true,
		CharacterID:         1312,
		CharacterStatus:     resolutionCorrelated,
		CharacterNameStatus: resolutionUnknown,
		Damage:              100,
	}
	got := event.formatNormal()
	if !strings.Contains(got, "Credit:Character:1312 [CORRELATED; name UNKNOWN]") {
		t.Fatalf("resolved character ID should remain visible without a localized name: %q", got)
	}
	if !strings.Contains(got, "Unknown [Entity:90]") {
		t.Fatalf("character credit must not replace source entity identity: %q", got)
	}
}

func TestSummonerEntityChainResolvesCharacterSeparatelyFromDamageCredit(t *testing.T) {
	resolver := newEntityResolver()
	add := func(entityID, configID, configType uint64, summonerID uint64, hasSummoner bool) {
		entity := appendVarintField(nil, fieldEntityPbID, entityID)
		if configID != 0 {
			entity = appendVarintField(entity, fieldEntityPbConfigID, configID)
		}
		if configType != 0 {
			entity = appendVarintField(entity, fieldEntityPbConfigType, configType)
		}
		if hasSummoner {
			summoner := appendVarintField(nil, fieldSummonerComponentPbSummonerID, summonerID)
			entity = appendBytesField(entity, fieldEntityPbComponentPbs, appendBytesField(nil, fieldEntityComponentPbSummonerComponent, summoner))
		}
		notify := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
		if _, err := resolver.applyEntityAddNotify(notify); err != nil {
			t.Fatal(err)
		}
	}
	add(100, 1312, 3, 0, false) // summoner entity is a character.
	add(101, 0, 0, 100, true)   // intermediate summon.
	add(200, 0, 0, 101, true)   // damage source summon.
	masters := &gameMaster{characters: map[uint64]masterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(resolutionVerified), Status: string(resolutionVerified)},
	}}
	source, _ := resolver.resolve(200)
	got := resolveSummonerCharacter(resolver, masters, source)
	if !got.HasCharacterID || got.CharacterID != 1312 || !got.HasCharacter || got.Status != resolutionCorrelated {
		t.Fatalf("summoner chain did not resolve a separately correlated character: %+v", got)
	}
	if len(got.EntityIDs) != 2 || got.EntityIDs[0] != 101 || got.EntityIDs[1] != 100 {
		t.Fatalf("summoner chain = %v, want [101 100]", got.EntityIDs)
	}
	if !strings.Contains(got.Proof, "SummonerId") || !strings.Contains(got.Proof, "CreatureDataId") {
		t.Fatalf("summoner chain provenance missing: %q", got.Proof)
	}
	event := combatEvent{Source: resolveEntityIdentity(resolver, masters, 200), SummonerCharacter: got, Damage: 42}
	line := event.formatNormal()
	if !strings.Contains(line, "Credit:?") || !strings.Contains(line, "Summoner:Test Role [CORRELATED]") {
		t.Fatalf("summoner relation must remain separate from damage credit: %q", line)
	}
}

func TestHealingCandidateJSONLSeparatesReportedMagnitudeFromUnknownEffectiveness(t *testing.T) {
	event := combatEvent{
		Damage: 1311, ServerResultRaw: -1311, HasServerResult: true,
		ChangeLifeRaw: -700, HasChangeLife: true, DamageCalculateType: 1, HasCalculateType: true,
		EventTypeCandidate: "HEALING_CANDIDATE", EventTypeStatus: resolutionCorrelated,
		EventTypeProof: "CalculateType=1 uses cure/heal display; HP restoration not verified",
	}
	view := event.jsonlView(nil)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["schemaVersion"] != float64(5) || got["eventType"] != "HEALING_RESULT_CANDIDATE" {
		t.Fatalf("healing event envelope = %#v", got)
	}
	if _, exists := got["damage"]; exists {
		t.Fatalf("healing result was serialized in the damage object: %#v", got)
	}
	result := got["combatResult"].(map[string]any)
	if result["combatResultAbs"] != float64(1311) || result["changeLifeRaw"] != float64(-700) || result["classCandidate"] != "HEALING_CANDIDATE" {
		t.Fatalf("raw result fields changed: %#v", result)
	}
	healing := got["healing"].(map[string]any)
	if healing["reportedMagnitudeAbs"] != float64(1311) || healing["magnitudeStatus"] != string(resolutionCorrelated) || healing["effectiveStatus"] != string(resolutionUnknown) || healing["hpDeltaStatus"] != string(resolutionUnknown) || healing["overhealStatus"] != string(resolutionUnknown) {
		t.Fatalf("healing evidence classification = %#v", healing)
	}
	if healing["effectiveAmountAbs"] != nil || healing["overhealAmountAbs"] != nil {
		t.Fatalf("unverified effective healing/overheal received values: %#v", healing)
	}
}

func TestRuntimeIdentityAuditKeepsOwnerAndSummonerNamespacesSeparate(t *testing.T) {
	entity := runtimeEntity{
		entityID: 500, sceneInstanceID: 43, hasSceneInstanceID: true,
		ownerID: 88, hasOwnerID: true, ownerIncID: 889, hasOwnerIncID: true,
		summonerID: 600, hasSummonerID: true, sceneSummonerID: 800, hasSceneSummonerID: true,
		playerEntityID: 640, hasPlayerEntityID: true,
		playerID: 77, hasPlayerID: true, roleID: 1312, hasRoleID: true,
		roleSourceFieldPath: "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId",
		sourceMessageID:     cmdJoinSceneNotify, sourceFieldPath: "JoinSceneNotify.SceneInfo.AoiData.Entities.EntityPb.Id",
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(marshalRuntimeIdentity(entity)), &got); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]uint64{
		"entityId": 500, "sceneInstanceId": 43, "ownerId": 88, "ownerIncId": 889,
		"summonerId": 600, "sceneSummonerId": 800, "playerEntityId": 640,
		"playerId": 77, "roleId": 1312,
	} {
		field, ok := got[name].(map[string]any)
		if !ok || field["value"] != float64(value) || field["present"] != true {
			t.Fatalf("runtime audit field %s lost its value/presence: %#v", name, got[name])
		}
	}
	if got["sourceMessageId"] != float64(cmdJoinSceneNotify) || got["sourceFieldPath"] != entity.sourceFieldPath {
		t.Fatalf("runtime audit provenance missing: %#v", got)
	}
	if got["roleSourceFieldPath"] != entity.roleSourceFieldPath {
		t.Fatalf("role identity provenance missing: %#v", got)
	}
}
