//go:build wwdev

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEntityActiveResponseDecodesBuffSnapshotWithCorrelatedEntity(t *testing.T) {
	var output, eventStream bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffSnapshots = &output
	p.combatEventsJSONL = &eventStream
	p.sessionNumber = 5
	const token = 42
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, 8801)); err != nil {
		t.Fatal(err)
	}

	var buffRecord []byte
	buffRecord = appendVarintField(buffRecord, 1, 111)
	buffRecord = appendVarintField(buffRecord, 2, 70001)
	buffRecord = appendVarintField(buffRecord, 3, 2)
	buffRecord = appendVarintField(buffRecord, 4, 3)
	buffRecord = appendVarintField(buffRecord, 5, 99002)
	buffRecord = appendVarintField(buffRecord, 6, 8801)
	buffRecord = appendVarintField(buffRecord, 7, 1)
	buffRecord = appendFixed32TestField(buffRecord, 8, mathFloat32bits(12.5))
	buffRecord = appendFixed32TestField(buffRecord, 9, mathFloat32bits(4.25))
	buffRecord = appendBytesField(buffRecord, 10, appendVarintField(nil, 1, 7))
	buffRecord = appendVarintField(buffRecord, 11, 1)
	buffRecord = appendVarintField(buffRecord, 14, 70002)
	buffRecord = appendVarintField(buffRecord, 30, 9)

	xAs := appendBytesField(nil, 1, buffRecord)
	componentPb := appendBytesField(nil, 15, xAs)
	response := appendBytesField(nil, 11, componentPb)
	frame := gameFrame{commandID: 26702, direction: "in", token: token, timestamp: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)}
	if err := p.handleActiveResponse(frame, response); err != nil {
		t.Fatal(err)
	}
	if p.stats.buffSnapshotResponses != 1 {
		t.Fatalf("snapshot responses = %d, want 1", p.stats.buffSnapshotResponses)
	}
	if p.stats.observedBuffEvents != 1 {
		t.Fatalf("observed buff records = %d, want 1", p.stats.observedBuffEvents)
	}
	var row buffSnapshotAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.EntityID == nil || *row.EntityID != 8801 || row.EntityIDStatus != resolutionCorrelated {
		t.Fatalf("entity association = %v/%s", row.EntityID, row.EntityIDStatus)
	}
	if row.Buff.ID != 70001 || row.Buff.Level != 2 || row.Buff.Stack != 3 || row.Buff.InstigatorID != 99002 || !row.Buff.HasActive || !row.Buff.Active {
		t.Fatalf("decoded Buff snapshot = %+v", row.Buff)
	}
	if row.BuffIDStatus != resolutionCorrelated || row.InstigatorSemantics != resolutionCorrelated || !strings.Contains(row.WindowsSchemaVerification, "client/master compatibility UNKNOWN") {
		t.Fatalf("evidence levels were overstated: %+v", row)
	}
	if !strings.Contains(row.InstigatorEvidence, "CreatureModel.GetEntity") || !strings.Contains(row.InstigatorEvidence, "client/master compatibility UNKNOWN") {
		t.Fatalf("snapshot instigator evidence basis = %q", row.InstigatorEvidence)
	}
	if row.ComponentPath != "EntityActiveResponse.field11(ComponentPbs[])->EntityComponentPb.field15(FightBuffComponent)->FightBuffComponentPb.field1(FightBuffInfos[])->FightBuffInformation" {
		t.Fatalf("component schema path = %q", row.ComponentPath)
	}
	if row.Buff.HandleID != 111 || !row.Buff.HasHandleID || row.Buff.EntityID != 8801 || !row.Buff.HasEntityID || row.Buff.ConfBuffID != 70002 || !row.Buff.HasConfBuffID {
		t.Fatalf("additional Windows schema fields = %+v", row.Buff)
	}
	if len(row.Buff.Context) != 1 || !bytes.Equal(row.Buff.Context[0], appendVarintField(nil, 1, 7)) {
		t.Fatalf("opaque Buff context = %v", row.Buff.Context)
	}
	if len(row.Buff.UnknownFieldNums) != 1 || row.Buff.UnknownFieldNums[0] != 30 {
		t.Fatalf("unknown fields = %v, want [30]", row.Buff.UnknownFieldNums)
	}
	var streamRow map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(eventStream.Bytes()), &streamRow); err != nil {
		t.Fatalf("decode unified event row: %v", err)
	}
	if streamRow["schemaVersion"] != float64(1) || streamRow["eventType"] != "BUFF_SNAPSHOT" || streamRow["sessionNumber"] != float64(5) || streamRow["messageId"] != float64(26702) {
		t.Fatalf("snapshot missing from unified event stream: %#v", streamRow)
	}
}

func TestEntityActiveResponseWithoutMatchingRequestKeepsEntityUnknown(t *testing.T) {
	var output bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffSnapshots = &output
	buffRecord := appendVarintField(nil, 2, 70001)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: 7}, response); err != nil {
		t.Fatal(err)
	}
	var row buffSnapshotAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.EntityID != nil || row.EntityIDStatus != resolutionUnknown {
		t.Fatalf("unmatched response entity = %v/%s", row.EntityID, row.EntityIDStatus)
	}
	if strings.Contains(output.String(), "entityId\":0") {
		t.Fatal("missing entity ID was serialized as a fabricated zero value")
	}
}

func TestEntityActiveResponseAddsBuffObservationToEncounterSummary(t *testing.T) {
	var summary bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.encounterSummary = &summary
	p.sessionNumber = 3
	const token = 17
	if err := p.handleActiveRequest(token, appendVarintField(nil, 4, 8801)); err != nil {
		t.Fatal(err)
	}
	buffRecord := appendVarintField(nil, 2, 70001)
	response := appendBytesField(nil, 11, appendBytesField(nil, 15, appendBytesField(nil, 1, buffRecord)))
	if err := p.handleActiveResponse(gameFrame{commandID: 26702, direction: "in", token: token}, response); err != nil {
		t.Fatal(err)
	}
	if err := p.writeEncounterSummary(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BuffId 70001 / EntityId 8801: 1 snapshot observation(s)", "do not establish apply, refresh, removal"} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("encounter summary missing %q:\n%s", want, summary.String())
		}
	}
}

func TestBuffLifecycleOutputRetainsUnknownFieldsAndRejectsWireMismatch(t *testing.T) {
	var output, eventStream bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.buffLifecycle = &output
	p.combatEventsJSONL = &eventStream
	p.sessionNumber = 2
	body := appendVarintField(nil, 5, 8801)
	body = appendVarintField(body, 99, 1)
	frame := gameFrame{commandID: 18345, direction: "in"}
	if err := p.handleBuffLifecycleMessage(frame, body); err != nil {
		t.Fatal(err)
	}
	var row buffLifecycleAuditRow
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.TargetEntityID == nil || *row.TargetEntityID != 8801 || len(row.UnknownFieldNumbers) != 1 || row.UnknownFieldNumbers[0] != 99 {
		t.Fatalf("unexpected fields were not preserved as unknown: %+v", row)
	}
	var streamRow map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(eventStream.Bytes()), &streamRow); err != nil {
		t.Fatalf("decode unified event row: %v", err)
	}
	if streamRow["schemaVersion"] != float64(1) || streamRow["eventType"] != "BUFF_LIFECYCLE" || streamRow["sessionNumber"] != float64(2) || streamRow["messageId"] != float64(18345) || streamRow["direction"] != "in" {
		t.Fatalf("Buff lifecycle missing from unified event stream: %#v", streamRow)
	}
	badWire := appendBytesField(nil, 5, []byte{1})
	if _, err := decodeBuffLifecycleMessage(gameFrame{commandID: 18345}, badWire); err == nil {
		t.Fatal("recognized field with a schema-incompatible wire type should fail")
	}
}
