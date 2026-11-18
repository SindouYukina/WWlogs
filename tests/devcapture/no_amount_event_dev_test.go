package devcapture

import (
	"bytes"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

func TestResponseWithoutAmountBecomesItsOwnEventAndNeverCountsAsDamage(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.dev.combatEventsJSONL = &jsonl
	p.Masters = &capture.GameMaster{Damage: map[uint64]capture.DamageMasterRecord{70010001: {ID: 70010001, CalculateType: 0, SourceTable: "damage.id"}}}
	p.Pending[5] = capture.PendingDamageRequest{AttackerEntityID: 100, HasAttackerEntityID: true, TargetEntityID: 200, HasTargetEntityID: true, DamageID: 70010001, HasDamageID: true}
	p.Pending[6] = capture.PendingDamageRequest{DamageID: 70010001, HasDamageID: true}
	p.Pending[7] = capture.PendingDamageRequest{DamageID: 70010001, HasDamageID: true}

	omitted := appendVarintField(nil, capture.FieldDamageExecuteResponseErrorCode, 0) // success, no Damage field
	p.HandleOneDamageResult(5, omitted)
	p.HandleOneDamageResult(5, omitted) // a retransmission must not emit a second event
	failed := appendVarintField(nil, capture.FieldDamageExecuteResponseErrorCode, 1500140)
	failed = appendVarintField(failed, capture.FieldDamageExecuteResponseDamage, 500)
	p.HandleOneDamageResult(6, failed)
	immune := appendVarintField(nil, capture.FieldDamageExecuteResponseErrorCode, 0)
	immune = appendVarintField(immune, capture.FieldDamageExecuteResponseImmuneType, 2)
	immune = appendVarintField(immune, capture.FieldDamageExecuteResponseDamage, 500)
	p.HandleOneDamageResult(7, immune)

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
	if stdout.Len() != 0 || p.Stats.Printed != 0 || p.Stats.MatchedCombatResultAbs != 0 || p.Stats.DamageCandidateEvents != 0 {
		t.Fatalf("no-amount hits must not print or count: stdout=%q stats=%+v", stdout.String(), p.Stats)
	}
	if p.Stats.InvalidResponses != 3 || p.Stats.DuplicateResponses != 1 || len(p.Pending) != 0 {
		t.Fatalf("invalid=%d duplicate=%d pending=%d", p.Stats.InvalidResponses, p.Stats.DuplicateResponses, len(p.Pending))
	}
}
