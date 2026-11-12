package capture

import (
	"strings"
	"testing"
)

func pushWrapper(child decodedField) *decodedMessage {
	push := decodedField{Number: fieldCombatSendDataPush, Name: "Push", Kind: "class", Message: &decodedMessage{Fields: []decodedField{child}}}
	return &decodedMessage{Type: "CombatSendPackRequest", Fields: []decodedField{
		{Number: fieldCombatSendPackRequestData, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{push}}},
	}}
}

func TestLocalRoleWireReportDoesNotRequireOptionalProtocolSchema(t *testing.T) {
	p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
	role := appendVarintField(nil, fieldClientCurrentRoleReportPushCurrentRoleID, 1409) // ClientCurrentRoleReportPush.CurrentRoleId
	role = appendVarintField(role, fieldClientCurrentRoleReportPushCurrentEntityID, 51522)
	push := appendBytesField(nil, fieldCombatPushDataClientCurrentRoleReportPush, role)
	data := appendBytesField(nil, fieldCombatSendDataPush, push)
	body := appendBytesField(nil, fieldCombatSendPackRequestData, data)

	p.archiveClientPushFrame(gameFrame{commandID: cmdCombatSendPackRequest, direction: "out"}, body)
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
