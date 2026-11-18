package devcapture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestRPCPairingLineReportsCounters(t *testing.T) {
	line := rpcPairingLine(capture.RPCPairingSnapshot{Requests: 3, Paired: 2, PairedOutOfOrder: 1, UnpairedExpired: 1})
	for _, want := range []string{"damage_rpc_pairing ", "requests=3", "paired=2", "paired_out_of_order=1", "unpaired_expired=1", "rewound_entities=0"} {
		if !bytes.Contains([]byte(line), []byte(want)) {
			t.Fatalf("pairing line %q is missing %q", line, want)
		}
	}
}

func TestCaptureLatencyHistogramReportsUpperBounds(t *testing.T) {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	p.ObservePacketTiming(base, base.Add(time.Millisecond), base.Add(2*time.Millisecond))
	if got := p.CaptureLayerHealth().DeliveryDelay.Status; got != capture.CaptureCounterNotApplicable {
		t.Fatalf("replay must not report live latency, got %s", got)
	}
	p.ObserveCaptureSource(true)
	if got := p.CaptureLayerHealth().DeliveryDelay.Status; got != capture.CaptureCounterUnavailable {
		t.Fatalf("live latency without samples = %s", got)
	}
	for i := 0; i < 90; i++ {
		p.ObservePacketTiming(base, base.Add(3*time.Millisecond), base.Add(4*time.Millisecond))
	}
	for i := 0; i < 9; i++ {
		p.ObservePacketTiming(base, base.Add(80*time.Millisecond), base.Add(81*time.Millisecond))
	}
	p.ObservePacketTiming(base, base.Add(700*time.Millisecond), base.Add(701*time.Millisecond))
	p.ObservePacketTiming(base, base.Add(-time.Microsecond), time.Time{}) // clock read order, counts as no delay
	delivery := p.CaptureLayerHealth().DeliveryDelay
	if delivery.Status != capture.CaptureCounterAvailable || delivery.Samples != 101 || delivery.P50ms != 5 || delivery.P95ms != 100 ||
		delivery.P99ms != 100 || delivery.MaxMs != 700 {
		t.Fatalf("delivery summary = %+v", delivery)
	}
	if processed := p.CaptureLayerHealth().ProcessedDelay; processed.Samples != 100 {
		t.Fatalf("processed samples = %+v", processed)
	}
	if line := captureLatencyLine(p.CaptureLayerHealth()); !strings.Contains(line, "delivery=samples:101/p50<=5ms/p95<=100ms/p99<=100ms/max:700.0ms") {
		t.Fatalf("latency line = %q", line)
	}
}

func TestDamageResponseKeepsUnresolvedEntityVisible(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.dev.combatEventsJSONL = &jsonl
	p.Pending[42] = capture.PendingDamageRequest{AttackerEntityID: 25320, HasAttackerEntityID: true, DamageID: 70010001, HasDamageID: true, DamageSourceType: 1, HasDamageSourceType: true, BulletID: -1, HasBulletID: true}
	response := appendVarintField(nil, capture.FieldDamageExecuteResponseErrorCode, 0)      // success
	response = appendVarintField(response, capture.FieldDamageExecuteResponseImmuneType, 0) // not immune
	response = appendVarintField(response, capture.FieldDamageExecuteResponseDamage, 9001)

	p.HandleOneDamageResult(42, response)
	p.HandleOneDamageResult(42, response)

	if stdout.Len() == 0 {
		t.Fatal("unresolved damage was hidden even though it was not proven unrelated")
	}
	if !strings.Contains(stdout.String(), "[PlayerTeamSide: UNCONFIRMED]") {
		t.Fatalf("unresolved relation was not labeled in normal output: %q", stdout.String())
	}
	if p.Stats.Printed != 1 || p.Stats.DuplicateResponses != 1 || p.Stats.MatchedCombatResultAbs != 9001 || p.Stats.DecodedMatchedEvents != 1 || p.Stats.DecodedCombatResultAbs != 9001 || p.Stats.IncludedUnresolvedRelations != 1 || p.Stats.SuppressedUnrelatedEvents != 0 || p.Stats.UnclassifiedResult != 9001 {
		t.Fatalf("unexpected stats: visible=%d duplicates=%d visible_abs=%d decoded_events=%d decoded_abs=%d unresolved=%d suppressed=%d unclassified_abs=%d", p.Stats.Printed, p.Stats.DuplicateResponses, p.Stats.MatchedCombatResultAbs, p.Stats.DecodedMatchedEvents, p.Stats.DecodedCombatResultAbs, p.Stats.IncludedUnresolvedRelations, p.Stats.SuppressedUnrelatedEvents, p.Stats.UnclassifiedResult)
	}
	if !strings.Contains(jsonl.String(), `"entityId":25320`) || strings.Contains(jsonl.String(), `"characterId":25320`) {
		t.Fatalf("raw entity identity was lost or promoted to a character: %q", jsonl.String())
	}
}

func TestDamageResponsePreservesRawResultsAndMarksHealingOnlyAsCandidate(t *testing.T) {
	var stdout, diagnostic, audit bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, &audit)
	p.Masters = &capture.GameMaster{Damage: map[uint64]capture.DamageMasterRecord{
		120900505: {ID: 120900505, CalculateType: 1, SourceTable: "damage"},
	}}
	p.Pending[9] = capture.PendingDamageRequest{DamageID: 120900505, HasDamageID: true}
	response := appendVarintField(nil, capture.FieldDamageExecuteResponseErrorCode, 0)
	response = appendVarintField(response, capture.FieldDamageExecuteResponseImmuneType, 0)
	changeLife := int32(-700)
	serverResult := int32(-1311)
	response = appendVarintField(response, capture.FieldDamageExecuteResponseChangeLife, uint64(uint32(changeLife))) // raw ChangeLife
	response = appendVarintField(response, capture.FieldDamageExecuteResponseDamage, uint64(uint32(serverResult)))   // raw server result

	p.HandleOneDamageResult(9, response)

	if p.Stats.Printed != 1 || p.Stats.MatchedCombatResultAbs != 1311 || p.Stats.DecodedMatchedEvents != 1 || p.Stats.DecodedCombatResultAbs != 1311 || p.Stats.IncludedUnresolvedRelations != 1 || p.Stats.SuppressedUnrelatedEvents != 0 || p.Stats.DamageCandidateResult != 0 || p.Stats.HealingCandidateEvents != 1 || p.Stats.HealingCandidateResult != 1311 || p.Stats.OtherCandidateResult != 0 || p.Stats.UnclassifiedResult != 0 {
		t.Fatalf("candidate classification changed raw result accounting: %+v", p.Stats)
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
	p.dev.combatEventsJSONL = &jsonl
	event := capture.CombatEvent{
		Timestamp: time.Date(2026, 10, 27, 1, 2, 3, 4000, time.UTC), SessionNumber: 3, RPCID: 91,
		SourceEntityID: 30298, HasSourceEntity: true, TargetEntityID: 30802, HasTargetEntity: true,
		SkillID: 280026010, DamageID: 28002601, HasDamageID: true, Damage: 177,
		ServerResultRaw: -177, HasServerResult: true, ChangeLifeRaw: -200, HasChangeLife: true,
		CharacterStatus: capture.ResolutionUnknown, CharacterNameStatus: capture.ResolutionUnknown,
		SkillStatus: capture.ResolutionUnknown, SkillNameStatus: capture.ResolutionUnknown, EventTypeStatus: capture.ResolutionUnknown,
		Source: capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 30298}, RuntimeStatus: capture.ResolutionUnknown},
		Target: capture.EntityIdentity{Runtime: capture.RuntimeEntity{EntityID: 30802}, RuntimeStatus: capture.ResolutionUnknown},
	}
	p.WriteDamageEvent(event)
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
	if _, exists := credit["characterId"]; exists || credit["status"] != string(capture.ResolutionUnknown) {
		t.Fatalf("unknown credit was promoted: %#v", credit)
	}
	result := got["combatResult"].(map[string]any)
	if result["damageId"] != float64(28002601) || result["combatResultAbs"] != float64(177) || result["serverResultRaw"] != float64(-177) || result["classStatus"] != string(capture.ResolutionUnknown) {
		t.Fatalf("raw combat result evidence or unknown class changed: %#v", result)
	}
	runtimeFields := source["runtimeFields"].(map[string]any)
	runtimeEntityID := runtimeFields["entityId"].(map[string]any)
	if _, exists := result["sourceType"]; exists || runtimeEntityID["present"] != true || runtimeEntityID["value"] != float64(30298) {
		t.Fatalf("optional IDs or nested runtime fields malformed: source=%#v result=%#v", source, result)
	}
}

func TestFormatNormalLabelsHealingAsCandidateResult(t *testing.T) {
	event := capture.CombatEvent{Damage: 1311, EventTypeCandidate: "HEALING_CANDIDATE"}
	line := formatNormal(event)
	if !strings.Contains(line, "1311 [Healing Candidate Result]") || strings.Contains(line, "Damage Candidate Result") {
		t.Fatalf("healing output was not labeled separately: %s", line)
	}
}

func TestFormatNormalLabelsVerifiedMonsterInsteadOfUnknownCredit(t *testing.T) {
	event := capture.CombatEvent{
		Source: capture.EntityIdentity{
			HasMonster: true, Monster: capture.MasterRecord{ID: 602000019}, MonsterStatus: capture.ResolutionVerified,
		},
		Damage: 2151,
	}
	line := formatNormal(event)
	if !strings.Contains(line, "Credit:NonPlayerSource [Monster:602000019; VERIFIED]") || strings.Contains(line, "Credit:?") {
		t.Fatalf("verified monster source was presented as unknown credit: %q", line)
	}
}

func TestFormatNormalLabelsCorrelatedTemplateInsteadOfUnknownCredit(t *testing.T) {
	event := capture.CombatEvent{
		Source: capture.EntityIdentity{
			TemplateStatus:     capture.ResolutionCorrelated,
			TemplateCandidates: []capture.SceneTemplateCandidate{{TemplateID: 602000019}},
		},
		Damage: 2151,
	}
	line := formatNormal(event)
	if !strings.Contains(line, "Credit:NonPlayerSource [Template:602000019; CORRELATED]") || strings.Contains(line, "Credit:?") {
		t.Fatalf("correlated non-player template was presented as unknown credit: %q", line)
	}
}

func TestCorrelatedRoleIsCreditNotSourceIdentity(t *testing.T) {
	event := capture.CombatEvent{
		SourceEntityID:      90,
		HasSourceEntity:     true,
		CharacterID:         1312,
		CharacterName:       "Role",
		CharacterNameStatus: capture.ResolutionVerified,
		CharacterStatus:     capture.ResolutionCorrelated,
		DamageID:            70010001,
		HasDamageID:         true,
		Damage:              100,
	}
	got := formatNormal(event)
	if !strings.Contains(got, "Unknown [Entity:90]") || !strings.Contains(got, "Credit:Role [CORRELATED; name VERIFIED]") {
		t.Fatalf("correlated role must be separate from source entity identity: %q", got)
	}
	if strings.Contains(got, "Role [CORRELATED] [Entity:90]") {
		t.Fatalf("correlated role was presented as the source entity: %q", got)
	}
}

func TestUnverifiedLocalizedSkillNameIsNotDisplayed(t *testing.T) {
	event := capture.CombatEvent{
		SkillID:         1312011,
		SkillName:       "Unverified label",
		SkillStatus:     capture.ResolutionVerified,
		SkillNameStatus: capture.ResolutionUnknown,
	}
	got := formatNormal(event)
	if !strings.Contains(got, "SkillId:1312011") || strings.Contains(got, "Unverified label") {
		t.Fatalf("unverified skill label was displayed: %q", got)
	}
}

func TestResolvedCharacterIDRemainsVisibleWhenNameIsUnknown(t *testing.T) {
	event := capture.CombatEvent{
		SourceEntityID:      90,
		HasSourceEntity:     true,
		CharacterID:         1312,
		CharacterStatus:     capture.ResolutionCorrelated,
		CharacterNameStatus: capture.ResolutionUnknown,
		Damage:              100,
	}
	got := formatNormal(event)
	if !strings.Contains(got, "Credit:Character:1312 [CORRELATED; name UNKNOWN]") {
		t.Fatalf("resolved character ID should remain visible without a localized name: %q", got)
	}
	if !strings.Contains(got, "Unknown [Entity:90]") {
		t.Fatalf("character credit must not replace source entity identity: %q", got)
	}
}

func TestSummonerEntityChainResolvesCharacterSeparatelyFromDamageCredit(t *testing.T) {
	resolver := capture.NewEntityResolver()
	add := func(entityID, configID, configType uint64, summonerID uint64, hasSummoner bool) {
		entity := appendVarintField(nil, capture.FieldEntityPbID, entityID)
		if configID != 0 {
			entity = appendVarintField(entity, capture.FieldEntityPbConfigID, configID)
		}
		if configType != 0 {
			entity = appendVarintField(entity, capture.FieldEntityPbConfigType, configType)
		}
		if hasSummoner {
			summoner := appendVarintField(nil, capture.FieldSummonerComponentPbSummonerID, summonerID)
			entity = appendBytesField(entity, capture.FieldEntityPbComponentPbs, appendBytesField(nil, capture.FieldEntityComponentPbSummonerComponent, summoner))
		}
		notify := appendBytesField(appendVarintField(nil, capture.FieldEntityAddNotifyIsAdd, 1), capture.FieldEntityAddNotifyEntityPbs, entity)
		if _, err := resolver.ApplyEntityAddNotify(notify); err != nil {
			t.Fatal(err)
		}
	}
	add(100, 1312, 3, 0, false) // summoner entity is a character.
	add(101, 0, 0, 100, true)   // intermediate summon.
	add(200, 0, 0, 101, true)   // damage source summon.
	masters := &capture.GameMaster{Characters: map[uint64]capture.MasterRecord{
		1312: {ID: 1312, Name: "Test Role", NameStatus: string(capture.ResolutionVerified), Status: string(capture.ResolutionVerified)},
	}}
	source, _ := resolver.Resolve(200)
	got := capture.ResolveSummonerCharacter(resolver, masters, source)
	if !got.HasCharacterID || got.CharacterID != 1312 || !got.HasCharacter || got.Status != capture.ResolutionCorrelated {
		t.Fatalf("summoner chain did not resolve a separately correlated character: %+v", got)
	}
	if len(got.EntityIDs) != 2 || got.EntityIDs[0] != 101 || got.EntityIDs[1] != 100 {
		t.Fatalf("summoner chain = %v, want [101 100]", got.EntityIDs)
	}
	if !strings.Contains(got.Proof, "SummonerId") || !strings.Contains(got.Proof, "CreatureDataId") {
		t.Fatalf("summoner chain provenance missing: %q", got.Proof)
	}
	event := capture.CombatEvent{Source: capture.ResolveEntityIdentity(resolver, masters, 200), SummonerCharacter: got, Damage: 42}
	line := formatNormal(event)
	if !strings.Contains(line, "Credit:?") || !strings.Contains(line, "Summoner:Test Role [CORRELATED]") {
		t.Fatalf("summoner relation must remain separate from damage credit: %q", line)
	}
}

func TestHealingCandidateJSONLSeparatesReportedMagnitudeFromUnknownEffectiveness(t *testing.T) {
	event := capture.CombatEvent{
		Damage: 1311, ServerResultRaw: -1311, HasServerResult: true,
		ChangeLifeRaw: -700, HasChangeLife: true, DamageCalculateType: 1, HasCalculateType: true,
		EventTypeCandidate: "HEALING_CANDIDATE", EventTypeStatus: capture.ResolutionCorrelated,
		EventTypeProof: "CalculateType=1 uses cure/heal display; HP restoration not verified",
	}
	view := jsonlView(event, nil)
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
	if healing["reportedMagnitudeAbs"] != float64(1311) || healing["magnitudeStatus"] != string(capture.ResolutionCorrelated) || healing["effectiveStatus"] != string(capture.ResolutionUnknown) || healing["hpDeltaStatus"] != string(capture.ResolutionUnknown) || healing["overhealStatus"] != string(capture.ResolutionUnknown) {
		t.Fatalf("healing evidence classification = %#v", healing)
	}
	if healing["effectiveAmountAbs"] != nil || healing["overhealAmountAbs"] != nil {
		t.Fatalf("unverified effective healing/overheal received values: %#v", healing)
	}
}

func TestRuntimeIdentityAuditKeepsOwnerAndSummonerNamespacesSeparate(t *testing.T) {
	entity := capture.RuntimeEntity{
		EntityID: 500, SceneInstanceID: 43, HasSceneInstanceID: true,
		OwnerID: 88, HasOwnerID: true, OwnerIncID: 889, HasOwnerIncID: true,
		SummonerID: 600, HasSummonerID: true, SceneSummonerID: 800, HasSceneSummonerID: true,
		PlayerEntityID: 640, HasPlayerEntityID: true,
		PlayerID: 77, HasPlayerID: true, RoleID: 1312, HasRoleID: true,
		RoleSourceFieldPath: "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId",
		SourceMessageID:     capture.CmdJoinSceneNotify, SourceFieldPath: "JoinSceneNotify.SceneInfo.AoiData.Entities.EntityPb.Id",
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
	if got["sourceMessageId"] != float64(capture.CmdJoinSceneNotify) || got["sourceFieldPath"] != entity.SourceFieldPath {
		t.Fatalf("runtime audit provenance missing: %#v", got)
	}
	if got["roleSourceFieldPath"] != entity.RoleSourceFieldPath {
		t.Fatalf("role identity provenance missing: %#v", got)
	}
}
