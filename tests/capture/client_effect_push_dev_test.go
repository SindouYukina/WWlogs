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
	p.masters = &gameMaster{
		buffDefinitions: map[uint64]struct{}{1505701002: {}},
		buffStatics:     map[uint64]buffStaticParameters{1505701002: {attribute: 11, hasAttribute: true, hasModifier: true}},
	}
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
		"kind=CLIENT_EFFECT_APPLY_REPORT evidence=CLIENT_REPORTED combatClass=COMBAT combatBasis=attribute_modifier:EnergyEfficiency localTeam=true id=1505701002 handle=268436729 holder=100 holderStatus=CORRELATED instigator=102 stack=1 duration_s=1.100",
		"kind=CLIENT_EFFECT_STACK_REPORT evidence=CLIENT_REPORTED combatClass=COMBAT combatBasis=attribute_modifier:EnergyEfficiency localTeam=true handle=268436729 handleLinkedId=1505701002 holder=100",
		"kind=CLIENT_EFFECT_REMOVE_REPORT evidence=CLIENT_REPORTED combatClass=COMBAT combatBasis=attribute_modifier:EnergyEfficiency localTeam=true handle=268436729 handleLinkedId=1505701002 holder=100 holderStatus=CORRELATED handleLinkedStatus=",
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

func TestDefaultEffectConsoleShowsOnlyCombatLifecycleOfTheLocalTeam(t *testing.T) {
	run := func(all bool) string {
		p := newClientEffectTestProcessor()
		p.masters = classifiedTestMaster()
		var stdout bytes.Buffer
		p.stdout, p.showEffectEvents, p.showAllEffectEvents = &stdout, true, all
		common := testCombatCommon{entityID: 100}
		deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436801, 12, 100, 1, 12)))           // combat
		deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436802, 13, 100, 1, 10)))           // non-combat
		deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(268436803, 14, 100, 1, 5)))            // unknown
		deliverClientPush(t, p, 0, clientPushBody(common, 9, clientApplyPayload(18446744073709551614, 12, 100, 1, 0))) // instant
		deliverClientPush(t, p, 1, clientPushBody(common, 37, clientStackPayload(268436801, 100, 1, 12, false)))       // refresh, same stack
		deliverClientPush(t, p, 2, clientPushBody(common, 37, clientStackPayload(268436801, 100, 2, 12, false)))       // stack change
		deliverClientPush(t, p, 3, clientPushBody(common, 10, clientRemovePayload(268436802, 100, true)))
		deliverClientPush(t, p, 12, clientPushBody(common, 10, clientRemovePayload(268436801, 100, false)))
		// A monster effect between monsters is not player-related even when its definition is combat.
		deliverClientPush(t, p, 4, clientPushBody(testCombatCommon{entityID: 200}, 9, clientApplyPayload(268436810, 12, 201, 1, 5)))
		return stdout.String()
	}
	combat := run(false)
	lines := strings.Split(strings.TrimSpace(combat), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "kind=CLIENT_EFFECT_APPLY_REPORT") || !strings.Contains(lines[1], "stack=2") ||
		!strings.Contains(lines[2], "removal=EXPIRED") {
		t.Fatalf("default console:\n%s", combat)
	}
	for _, hidden := range []string{"id=13 ", "id=14 ", "handle=18446744073709551614", "holder=200"} {
		if strings.Contains(combat, hidden) {
			t.Fatalf("default console shows %q:\n%s", hidden, combat)
		}
	}
	all := run(true)
	if got := strings.Count(all, "BuffEvent"); got != 9 {
		t.Fatalf("diagnostic console lines = %d, want every message:\n%s", got, all)
	}
	for _, want := range []string{"combatClass=UNKNOWN combatBasis=no_classifying_evidence", "combatClass=NON_COMBAT combatBasis=developer_tag:跌落无伤", "localTeam=false"} {
		if !strings.Contains(all, want) {
			t.Fatalf("diagnostic console lacks %q:\n%s", want, all)
		}
	}
}

func TestEffectConsoleReevaluatesTheTeamWhenTheLocalAnchorArrivesLater(t *testing.T) {
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.sessionNumber = 1
	p.masters = classifiedTestMaster()
	var stdout bytes.Buffer
	p.stdout, p.showEffectEvents = &stdout, true
	addTestPlayerRoleGroupEntity(p, 100, 7, 1312)
	// No local role report yet: the holder's relation is unknown and the apply stays off the console.
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 100}, 9, clientApplyPayload(268436801, 12, 100, 1, 12)))
	if stdout.Len() != 0 {
		t.Fatalf("printed before the local team was known:\n%s", stdout.String())
	}
	p.observeLocalRoleEntity(100)
	deliverClientPush(t, p, 12, clientPushBody(testCombatCommon{entityID: 100}, 10, clientRemovePayload(268436801, 100, false)))
	if !strings.Contains(stdout.String(), "kind=CLIENT_EFFECT_REMOVE_REPORT") || !strings.Contains(stdout.String(), "localTeam=true") {
		t.Fatalf("remove after the anchor was not printed:\n%s", stdout.String())
	}
}
