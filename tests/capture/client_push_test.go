package capture

import (
	"strings"
	"testing"
)

func pushWrapper(child decodedField) *decodedMessage {
	push := decodedField{Number: 2, Name: "Push", Kind: "class", Message: &decodedMessage{Fields: []decodedField{child}}}
	return &decodedMessage{Type: "CombatSendPackRequest", Fields: []decodedField{
		{Number: 14, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{push}}},
	}}
}

func TestLocalRoleWireReportDoesNotRequireOptionalProtocolSchema(t *testing.T) {
	p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
	role := appendVarintField(nil, 7, 1409) // ClientCurrentRoleReportPush.CurrentRoleId
	role = appendVarintField(role, clientCurrentRoleReportCurrentEntityField, 51522)
	push := appendBytesField(nil, combatPushClientRoleReportField, role)
	data := appendBytesField(nil, combatSendPackDataPushField, push)
	body := appendBytesField(nil, combatSendPackDataField, data)

	p.archiveClientPushFrame(gameFrame{commandID: 19470, direction: "out"}, body)
	if p.protocolSchema != nil {
		t.Fatal("test requires normal mode without an optional protocol schema")
	}
	if !p.playerRelevance.isRoleEntity(51522) {
		t.Fatal("normal product mode did not recognize the exact client-reported role entity")
	}
	if p.stats.playerRoleReports != 1 || p.stats.playerRoleReportParseErrors != 0 {
		t.Fatalf("role report stats = %+v", p.stats)
	}
}
