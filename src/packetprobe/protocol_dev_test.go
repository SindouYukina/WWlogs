//go:build wwdev

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDamageResponsePreservesRawResultsAndMarksHealingOnlyAsCandidate(t *testing.T) {
	var stdout, diagnostic, audit bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, &audit)
	p.masters = &gameMaster{damage: map[uint64]damageMasterRecord{
		120900505: {ID: 120900505, CalculateType: 1, GameVersion: "3.7.0", ResourceVersion: "3.7.8", SourceCommit: "test-commit", SourceTable: "damage.json"},
	}}
	p.pending[9] = pendingDamageRequest{damageID: 120900505, hasDamageID: true}
	response := appendVarintField(nil, 4, 0)
	response = appendVarintField(response, 5, 0)
	changeLife := int32(-700)
	serverResult := int32(-1311)
	response = appendVarintField(response, 7, uint64(uint32(changeLife)))    // raw ChangeLife
	response = appendVarintField(response, 10, uint64(uint32(serverResult))) // raw server result

	p.handleOneDamageResult(9, response)

	if p.stats.printed != 1 || p.stats.matchedCombatResultAbs != 1311 || p.stats.damageCandidateResult != 0 || p.stats.healingCandidateEvents != 1 || p.stats.healingCandidateResult != 1311 || p.stats.otherCandidateResult != 0 || p.stats.unclassifiedResult != 0 {
		t.Fatalf("candidate classification changed raw result accounting: %+v", p.stats)
	}
	var summary bytes.Buffer
	p.printSummary(&summary)
	for _, field := range []string{"matched_combat_result_abs=1311", "damage_candidate_result_abs=0", "healing_candidate_result_abs=1311", "other_combat_candidate_result_abs=0", "unclassified_result_abs=0"} {
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
	if got["schemaVersion"] != float64(2) || got["eventType"] != "UNCLASSIFIED_RESULT" || got["sessionNumber"] != float64(3) || got["rpcId"] != float64(91) {
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
