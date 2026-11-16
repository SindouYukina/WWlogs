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
	p.archiveClientPushFrame(gameFrame{commandID: cmdCombatSendPackRequest, direction: "out"}, localRoleReportBody(1409, 51522))
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

// A research protocol schema must not replace the core role decoder (Phase 3A R03): the role report is read, counted
// and conflict-checked the same way with and without one.
func TestProtocolSchemaKeepsTheCoreRoleDecoder(t *testing.T) {
	for _, test := range []struct {
		name      string
		entityIDs []uint64
		wantRole  bool
	}{
		{"one reported entity", []uint64{51522}, true},
		{"conflicting reported entities", []uint64{51522, 51523}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := func(schema *protocolSchema) *processor {
				p := newProcessor(nil, &strings.Builder{}, &strings.Builder{}, nil)
				p.protocolSchema = schema
				p.archiveClientPushFrame(gameFrame{commandID: cmdCombatSendPackRequest, direction: "out"}, localRoleReportBody(1409, test.entityIDs...))
				return p
			}
			plain, research := run(nil), run(&protocolSchema{})
			for name, p := range map[string]*processor{"without schema": plain, "with schema": research} {
				if got := p.playerRelevance.isRoleEntity(51522); got != test.wantRole {
					t.Fatalf("%s: local role entity = %v, want %v", name, got, test.wantRole)
				}
			}
			if plain.stats != research.stats {
				t.Fatalf("core statistics differ with a research schema:\nwithout %+v\nwith    %+v", plain.stats, research.stats)
			}
		})
	}
}
