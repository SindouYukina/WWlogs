package capture

import (
	"strings"
	"testing"
)

// localRoleReportBody is a CombatSendPackRequest with one ClientCurrentRoleReportPush naming roleID and the given
// CurrentEntityId values (two different values make a conflicting report).
func localRoleReportBody(roleID uint64, entityIDs ...uint64) []byte {
	role := appendVarintField(nil, fieldClientCurrentRoleReportPushCurrentRoleID, roleID) // ClientCurrentRoleReportPush.CurrentRoleId
	for _, entityID := range entityIDs {
		role = appendVarintField(role, fieldClientCurrentRoleReportPushCurrentEntityID, entityID)
	}
	push := appendBytesField(nil, fieldCombatPushDataClientCurrentRoleReportPush, role)
	data := appendBytesField(nil, fieldCombatSendDataPush, push)
	return appendBytesField(nil, fieldCombatSendPackRequestData, data)
}

func TestLocalRoleWireReportDoesNotRequireOptionalProtocolSchema(t *testing.T) {
	p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
	p.observeClientPushFrame(gameFrame{commandID: cmdCombatSendPackRequest, direction: "out"}, localRoleReportBody(1409, 51522))
	if !p.playerRelevance.isRoleEntity(51522) {
		t.Fatal("normal product mode did not recognize the exact client-reported role entity")
	}
	if p.stats.playerRoleReports != 1 || p.stats.playerRoleReportParseErrors != 0 {
		t.Fatalf("role report stats = %+v", p.stats)
	}
}
