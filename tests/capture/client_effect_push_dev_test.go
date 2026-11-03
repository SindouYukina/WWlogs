//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestClientEffectReportsPrintLabelledConsoleLines(t *testing.T) {
	p := newClientEffectTestProcessor()
	var stdout bytes.Buffer
	p.stdout, p.showEffectEvents = &stdout, true
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436729, 1505701002, 102, 1, 1.1)))
	deliverClientPush(t, p, 0.9, clientPushBody(common, 37, clientStackPayload(268436729, 102, 1, 1.1, false)))
	deliverClientPush(t, p, 1.8, clientPushBody(common, 37, clientStackPayload(268436729, 102, 2, 1.1, false)))
	deliverClientPush(t, p, 3, clientPushBody(common, 10, clientRemovePayload(268436729, 100, false)))
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("console lines = %d, want apply, changed stack and remove only:\n%s", len(lines), stdout.String())
	}
	for _, want := range []string{
		"kind=CLIENT_EFFECT_APPLY_REPORT evidence=CLIENT_REPORTED id=1505701002 handle=268436729 holder=100 holderStatus=CORRELATED instigator=102 stack=1 duration_s=1.100",
		"kind=CLIENT_EFFECT_STACK_REPORT evidence=CLIENT_REPORTED handle=268436729 handleLinkedId=1505701002 holder=100",
		"kind=CLIENT_EFFECT_REMOVE_REPORT evidence=CLIENT_REPORTED handle=268436729 handleLinkedId=1505701002 holder=100 holderStatus=CORRELATED handleLinkedStatus=",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("console lacks %q:\n%s", want, stdout.String())
		}
	}
	if !strings.Contains(lines[1], "stack=2") || !strings.Contains(lines[2], "removal=EXPIRED") {
		t.Fatalf("stack or removal label missing:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "test reason") {
		t.Fatal("the client's free-text reason reached the console")
	}
}

func TestClientEffectReportJSONLKeepsServerRowsUnchanged(t *testing.T) {
	p := newClientEffectTestProcessor()
	var lifecycle bytes.Buffer
	p.buffLifecycle = &lifecycle
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	deliverClientPush(t, p, 1, clientPushBody(testCombatCommon{entityID: 100, messageID: 9}, 9, clientApplyPayload(268436729, 1505701002, 102, 1, 1.1)))
	lines := strings.Split(strings.TrimSpace(lifecycle.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lifecycle rows = %d", len(lines))
	}
	if strings.Contains(lines[0], "evidenceSource") || strings.Contains(lines[0], "clientReasonRaw") {
		t.Fatalf("server notify row gained client fields: %s", lines[0])
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &row); err != nil {
		t.Fatal(err)
	}
	if row["evidenceSource"] != string(EffectEvidenceClientReport) || row["eventKind"] != "CLIENT_EFFECT_APPLY_REPORT" ||
		row["clientReasonRaw"] != "test reason" || row["providerStatus"] != string(resolutionUnknown) {
		t.Fatalf("client row = %s", lines[1])
	}
}
