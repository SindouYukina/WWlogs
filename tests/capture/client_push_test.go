package capture

import (
	"strings"
	"testing"
)

// localRoleReportBody is a CombatSendPackRequest with one ClientCurrentRoleReportPush naming roleID and the given
// CurrentEntityId values (two different values make a conflicting report).
func localRoleReportBody(roleID uint64, entityIDs ...uint64) []byte {
	role := appendVarintField(nil, FieldClientCurrentRoleReportPushCurrentRoleID, roleID) // ClientCurrentRoleReportPush.CurrentRoleId
	for _, entityID := range entityIDs {
		role = appendVarintField(role, FieldClientCurrentRoleReportPushCurrentEntityID, entityID)
	}
	push := appendBytesField(nil, FieldCombatPushDataClientCurrentRoleReportPush, role)
	data := appendBytesField(nil, FieldCombatSendDataPush, push)
	return appendBytesField(nil, FieldCombatSendPackRequestData, data)
}

func TestLocalRoleWireReportDoesNotRequireOptionalProtocolSchema(t *testing.T) {
	p := NewProcessor(nil, &strings.Builder{})
	p.ObserveClientPushFrame(GameFrame{CommandID: CmdCombatSendPackRequest, Direction: "out"}, localRoleReportBody(1409, 51522))
	if !p.PlayerRelevance.IsRoleEntity(51522) {
		t.Fatal("normal product mode did not recognize the exact client-reported role entity")
	}
	if p.Stats.PlayerRoleReports != 1 || p.Stats.PlayerRoleReportParseErrors != 0 {
		t.Fatalf("role report stats = %+v", p.Stats)
	}
}
