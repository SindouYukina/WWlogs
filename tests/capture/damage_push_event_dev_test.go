//go:build wwdev

package capture

import (
	"bytes"
	"testing"
)

// A notified result is described on its development event line; the accounting itself is shared (damage_notify_test.go).
func TestNotifiedResultEventLineUsesTheAuxiliaryNamespace(t *testing.T) {
	var stdout, diagnostic, jsonl bytes.Buffer
	p := NewProcessor(nil, &stdout, &diagnostic, nil)
	p.Dev.combatEventsJSONL = &jsonl
	p.Masters = &GameMaster{Damage: map[uint64]DamageMasterRecord{1001: {ID: 1001, CalculateType: 0, SourceTable: "damage.id"}}}

	event := p.BuildDamageEvent(0, PendingDamageRequest{AttackerEntityID: 10, HasAttackerEntityID: true, TargetEntityID: 20, HasTargetEntityID: true, DamageID: 1001, HasDamageID: true}, 176, 176, true, 176)
	event.Notify = &NotifiedResult{Key: NotifyEventKey(7, 1), NegativeStatus: true, NegativeStatusBasis: "no live status instance on the target executes this DamageId"}
	p.writeDamageEventJSONL(event, nil)

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

// A local negative-status result that an overflow on another enemy can have spread names its settlement and how many such
// overflows its moment had; an instant status Buff of the moment is marked.
func TestNotifiedSpreadResultLineNamesItsSettlement(t *testing.T) {
	var jsonl bytes.Buffer
	p := newCausalityTestProcessor()
	p.Dev.combatEventsJSONL = &jsonl
	buildFusionToLimit(t, p, 1.0, 2, 200, 8, 100, 100)
	deliverNotifyFrame(t, p, 1.5, 3, items(overflow(200, 8, 101), [][]byte{fusionTick(201, 4000)})...)
	deliverNotifyFrame(t, p, 2.0, 4, triggerData(200, causalInstantBuff, 101), spectroTick(200, 2500))
	var spread, instant map[string]any
	for _, row := range decodeJSONLLines(t, jsonl.Bytes()) {
		if row["eventType"] != "DAMAGE_NOTIFY_RESULT" {
			continue
		}
		push, _ := row["push"].(map[string]any)
		switch push["settlement"] {
		case "spread":
			spread = push
		case "instant":
			instant = push
		}
	}
	if spread == nil || spread["spreadCandidates"] != float64(1) || spread["creditBasis"] != "negative_status_trigger" || spread["creditRoleId"] != float64(1505) {
		t.Fatalf("spread result line = %v", spread)
	}
	if instant == nil || instant["instantTrigger"] != true || instant["creditRoleId"] != float64(1505) {
		t.Fatalf("instant result line = %v", instant)
	}
}
