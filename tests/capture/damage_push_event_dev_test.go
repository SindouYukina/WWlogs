//go:build wwdev

package capture

import (
	"bytes"
	"testing"
)

// A notified result is described on its development event line; the accounting itself is shared (damage_notify_test.go).
func TestNotifiedResultEventLineUsesTheAuxiliaryNamespace(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := newProcessor(nil, &stdout, &diagnostic, nil)
	p.combatEventsJSONL = &jsonl
	p.masters = &gameMaster{damage: map[uint64]damageMasterRecord{1001: {ID: 1001, CalculateType: 0, SourceTable: "damage.id"}}}

	event := p.buildDamageEvent(0, pendingDamageRequest{attackerEntityID: 10, hasAttackerEntityID: true, targetEntityID: 20, hasTargetEntityID: true, damageID: 1001, hasDamageID: true}, 176, 176, true, 176)
	event.Notify = &notifiedResult{Key: notifyEventKey(7, 1), NegativeStatus: true, NegativeStatusBasis: "no live status instance on the target executes this DamageId"}
	p.writeDamageEventJSONL(event)

	rows := decodeJSONLLines(t, jsonl.Bytes())
	if len(rows) != 1 {
		t.Fatalf("one line expected, got %d", len(rows))
	}
	row := rows[0]
	push, _ := row["push"].(map[string]any)
	if row["eventType"] != "DAMAGE_NOTIFY_RESULT" || push == nil || push["stream"] != "DAMAGE_EXECUTE_NOTIFY" || push["pairedWithRpc"] != false ||
		push["negativeStatus"] != true || push["negativeStatusBasis"] == "" || push["notifyKey"] != float64(7<<16|1) {
		t.Fatalf("notified result line = %v", row)
	}
	if id, _ := row["eventId"].(string); !contains(id, "-p") {
		t.Fatalf("notified results must use the p id namespace: %v", row["eventId"])
	}
}
