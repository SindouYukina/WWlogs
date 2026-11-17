package capture

import (
	"fmt"
)

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
