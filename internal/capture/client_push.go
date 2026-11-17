package capture

import (
	"fmt"
)

// clientPushSchemaVersion identifies the layout of clientPushRecord.
const clientPushSchemaVersion = 2

// clientPushRecord is one client-reported push that is not a server notify. The
// client reports its own current role (ClientCurrentRoleReportPush) and the end
// of its skills (EndSkillPush). These are the observable evidence for character
// switches and skill use; the server does not send a separate switch notify in
// the captures examined so far.
type clientPushRecord struct {
	SchemaVersion  int      `json:"schemaVersion"`
	FrameOrdinal   uint64   `json:"frameOrdinal"`
	SessionNumber  int      `json:"sessionNumber"`
	Timestamp      string   `json:"timestamp"`
	PushName       string   `json:"pushName"`
	ActiveRoleID   *int64   `json:"activeRoleId,omitempty"`
	CurrentRoleID  *int64   `json:"currentRoleId,omitempty"`
	EntityID       *int64   `json:"currentEntityId,omitempty"`
	ActorEntityID  *int64   `json:"entityId,omitempty"`
	SkillID        *int64   `json:"skillId,omitempty"`
	SkillTime      *float64 `json:"clientSkillTime,omitempty"`
	SkillRoleKey   *int64   `json:"skillRolePrefix,omitempty"`
	MessageID      *int64   `json:"messageId,omitempty"`
	TargetEntityID *int64   `json:"targetEntityId,omitempty"`
	RequestType    string   `json:"requestType,omitempty"`
	Evidence       string   `json:"evidence"`
}

// combatActionRequest preserves the minimum outbound skill-request fields
// needed to correlate a later server-reported damage request by MessageId.
type combatActionRequest struct {
	SessionNumber      int
	MessageID          uint64
	EntityID           uint64
	SkillID            uint64
	TargetID           uint64
	RequestType        string
	Timestamp          string
	LinkedDamageEvents int
}

type combatActionRequestKey struct {
	sessionNumber int
	messageID     uint64
}

const maxCombatActionRequests = 8192

// observeClientPushFrame tracks the self-reported role entity of an outbound CombatSendPackRequest with the core decoder
// in every build and run. The Development research decoder then reads the same request for its research outputs only
// (archiveClientPushResearch); it never replaces the core decoder or changes core state, so a research input cannot
// change the statistics.
func (p *processor) observeClientPushFrame(frame gameFrame, plain []byte) {
	if frame.commandID != cmdCombatSendPackRequest || frame.direction != "out" {
		return
	}
	p.observeLocalRoleReports(plain)
	p.archiveClientPushResearch(frame, plain)
}

func (p *processor) observeLocalRoleReports(plain []byte) {
	outer, err := parseProtoFields(plain)
	if err != nil {
		p.stats.playerRoleReportParseErrors++
		fmt.Fprintf(p.diagnostic, "player_role_report_parse_error=%v\n", err)
		return
	}
	for _, dataField := range outer {
		if dataField.number != fieldCombatSendPackRequestData || dataField.wire != 2 {
			continue
		}
		dataFields, err := parseProtoFields(dataField.data)
		if err != nil {
			p.stats.playerRoleReportParseErrors++
			fmt.Fprintf(p.diagnostic, "player_role_report_parse_error=%v\n", err)
			continue
		}
		for _, pushField := range dataFields {
			if pushField.number != fieldCombatSendDataPush || pushField.wire != 2 {
				continue
			}
			pushFields, err := parseProtoFields(pushField.data)
			if err != nil {
				p.stats.playerRoleReportParseErrors++
				fmt.Fprintf(p.diagnostic, "player_role_report_parse_error=%v\n", err)
				continue
			}
			for _, roleField := range pushFields {
				if roleField.number != fieldCombatPushDataClientCurrentRoleReportPush || roleField.wire != 2 {
					continue
				}
				roleFields, err := parseProtoFields(roleField.data)
				if err != nil {
					p.stats.playerRoleReportParseErrors++
					fmt.Fprintf(p.diagnostic, "player_role_report_parse_error=%v\n", err)
					continue
				}
				var entityID uint64
				var hasEntityID, conflictingEntityID bool
				for _, roleValue := range roleFields {
					if roleValue.number != fieldClientCurrentRoleReportPushCurrentEntityID || roleValue.wire != 0 || roleValue.value == 0 {
						continue
					}
					if hasEntityID && entityID != roleValue.value {
						conflictingEntityID = true
					}
					entityID, hasEntityID = roleValue.value, true
				}
				if !hasEntityID || conflictingEntityID {
					continue
				}
				p.stats.playerRoleReports++
				p.observeLocalRoleEntity(entityID)
			}
		}
	}
}
