package capture

import (
	"bytes"
	"testing"
)

func TestPushedDamageBecomesASeparateEventWithoutCreditOrTotals(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.combatEventsJSONL = &jsonl
	p.masters = &gameMaster{
		damage:            map[uint64]damageMasterRecord{1001: {ID: 1001, CalculateType: 0, Element: 4, HasElement: true, SourceTable: "damage.id"}, 555: {ID: 555, CalculateType: 0, SourceTable: "damage.id"}},
		abnormalDamageIDs: map[uint64]struct{}{1001: {}},
		elementNames:      map[int32]string{4: "Aero"},
	}

	p.writePushDamageEvent(damagePushRecord{Stream: "DAMAGE_EXECUTE_NOTIFY_PUSH", DamageID: 1001, AttackerID: 10, TargetID: 20, Damage: 176, ChangeLife: 176, ElementType: 4, ElementName: "Aero"})
	p.writePushDamageEvent(damagePushRecord{Stream: "DAMAGE_EXECUTE_NOTIFY_PUSH", DamageID: 555, AttackerID: 10, TargetID: 20, Damage: 0, ElementType: 0})

	rows := decodeJSONLLines(t, jsonl.Bytes())
	if len(rows) != 2 {
		t.Fatalf("one event per push expected, got %d", len(rows))
	}
	abnormal, uncategorized := rows[0], rows[1]
	push, _ := abnormal["push"].(map[string]any)
	if abnormal["eventType"] != "DAMAGE_PUSH_RESULT" || push == nil || push["category"] != pushCategoryAbnormal || push["categoryStatus"] != string(resolutionCorrelated) || push["pairedWithRpc"] != false {
		t.Fatalf("abnormal push = %v", abnormal)
	}
	if id, _ := abnormal["eventId"].(string); id == "" || !contains(id, "-p") {
		t.Fatalf("push events must use the p id namespace: %v", abnormal["eventId"])
	}
	if credit, _ := abnormal["credit"].(map[string]any); credit["status"] != string(resolutionUnknown) {
		t.Fatalf("a push must credit nobody: %v", abnormal["credit"])
	}
	actors, _ := abnormal["actors"].(map[string]any)
	if credited, _ := actors["creditedCharacter"].(map[string]any); credited["status"] != string(resolutionUnknown) {
		t.Fatalf("the actor projection must not credit a character: %v", actors)
	}
	if element, _ := abnormal["combatResult"].(map[string]any)["element"].(map[string]any); element["name"] != "Aero" {
		t.Fatalf("the damage row element must be named: %v", abnormal["combatResult"])
	}
	other, _ := uncategorized["push"].(map[string]any)
	if other["category"] != pushCategoryUnclassified || other["categoryStatus"] != string(resolutionUnknown) {
		t.Fatalf("an unknown DamageId must stay uncategorized: %v", uncategorized)
	}
	if stdout.Len() != 0 || p.stats.printed != 0 || p.stats.matchedCombatResultAbs != 0 || p.stats.damageCandidateEvents != 0 {
		t.Fatalf("pushes must not print or count: stdout=%q stats=%+v", stdout.String(), p.stats)
	}
}

func contains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
