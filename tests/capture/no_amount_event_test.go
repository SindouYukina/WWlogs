package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decodeJSONLLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestResponseWithoutAmountBecomesItsOwnEventAndNeverCountsAsDamage(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.combatEventsJSONL = &jsonl
	p.masters = &gameMaster{damage: map[uint64]damageMasterRecord{70010001: {ID: 70010001, CalculateType: 0, SourceTable: "damage.id"}}}
	p.pending[5] = pendingDamageRequest{attackerEntityID: 100, hasAttackerEntityID: true, targetEntityID: 200, hasTargetEntityID: true, damageID: 70010001, hasDamageID: true}
	p.pending[6] = pendingDamageRequest{damageID: 70010001, hasDamageID: true}
	p.pending[7] = pendingDamageRequest{damageID: 70010001, hasDamageID: true}

	omitted := appendVarintField(nil, 4, 0) // success, no Damage field
	p.handleOneDamageResult(5, omitted)
	p.handleOneDamageResult(5, omitted) // a retransmission must not emit a second event
	failed := appendVarintField(nil, 4, 1500140)
	failed = appendVarintField(failed, 10, 500)
	p.handleOneDamageResult(6, failed)
	immune := appendVarintField(nil, 4, 0)
	immune = appendVarintField(immune, 5, 2)
	immune = appendVarintField(immune, 10, 500)
	p.handleOneDamageResult(7, immune)

	rows := decodeJSONLLines(t, jsonl.Bytes())
	if len(rows) != 3 {
		t.Fatalf("one event per answered request expected, got %d: %v", len(rows), rows)
	}
	wantOutcome := []string{"NO_AMOUNT_FIELD", "ERROR_CODE", "IMMUNE"}
	for i, row := range rows {
		response, _ := row["response"].(map[string]any)
		if row["eventType"] != "DAMAGE_RESPONSE_WITHOUT_AMOUNT" || response == nil || response["outcome"] != wantOutcome[i] {
			t.Fatalf("row %d = %v, want outcome %s", i, row, wantOutcome[i])
		}
		if id, _ := row["eventId"].(string); !strings.Contains(id, "-x") {
			t.Fatalf("no-amount events must use the auxiliary id namespace: %v", row["eventId"])
		}
		if result, _ := row["combatResult"].(map[string]any); result["combatResultAbs"] != float64(0) {
			t.Fatalf("a no-amount event must carry no amount: %v", result)
		}
	}
	if rows[0]["raw"].(map[string]any)["damageId"] != float64(70010001) {
		t.Fatalf("the request ids must be kept: %v", rows[0]["raw"])
	}
	if stdout.Len() != 0 || p.stats.printed != 0 || p.stats.matchedCombatResultAbs != 0 || p.stats.damageCandidateEvents != 0 {
		t.Fatalf("no-amount hits must not print or count: stdout=%q stats=%+v", stdout.String(), p.stats)
	}
	if p.stats.invalidResponses != 3 || p.stats.duplicateResponses != 1 || len(p.pending) != 0 {
		t.Fatalf("invalid=%d duplicate=%d pending=%d", p.stats.invalidResponses, p.stats.duplicateResponses, len(p.pending))
	}
}
