package capture

import (
	"fmt"
)

// ObserveClientPushFrame tracks the self-reported role entity of an outbound CombatSendPackRequest with the core decoder
// in every build and run. The Development research decoder then reads the same request for its research outputs only
// (archiveClientPushResearch); it never replaces the core decoder or changes core state, so a research input cannot
// change the statistics.
func (p *Processor) ObserveClientPushFrame(frame GameFrame, plain []byte) {
	if frame.CommandID != CmdCombatSendPackRequest || frame.Direction != "out" {
		return
	}
	p.observeLocalRoleReports(plain)
	p.ArchiveClientPushResearch(frame, plain)
}

func (p *Processor) observeLocalRoleReports(plain []byte) {
	outer, err := ParseProtoFields(plain)
	if err != nil {
		p.Stats.PlayerRoleReportParseErrors++
		fmt.Fprintf(p.Diagnostic, "player_role_report_parse_error=%v\n", err)
		return
	}
	for _, dataField := range outer {
		if dataField.Number != FieldCombatSendPackRequestData || dataField.Wire != 2 {
			continue
		}
		dataFields, err := ParseProtoFields(dataField.Data)
		if err != nil {
			p.Stats.PlayerRoleReportParseErrors++
			fmt.Fprintf(p.Diagnostic, "player_role_report_parse_error=%v\n", err)
			continue
		}
		for _, pushField := range dataFields {
			if pushField.Number != FieldCombatSendDataPush || pushField.Wire != 2 {
				continue
			}
			pushFields, err := ParseProtoFields(pushField.Data)
			if err != nil {
				p.Stats.PlayerRoleReportParseErrors++
				fmt.Fprintf(p.Diagnostic, "player_role_report_parse_error=%v\n", err)
				continue
			}
			for _, roleField := range pushFields {
				if roleField.Number != FieldCombatPushDataClientCurrentRoleReportPush || roleField.Wire != 2 {
					continue
				}
				roleFields, err := ParseProtoFields(roleField.Data)
				if err != nil {
					p.Stats.PlayerRoleReportParseErrors++
					fmt.Fprintf(p.Diagnostic, "player_role_report_parse_error=%v\n", err)
					continue
				}
				var entityID uint64
				var hasEntityID, conflictingEntityID bool
				for _, roleValue := range roleFields {
					if roleValue.Number != FieldClientCurrentRoleReportPushCurrentEntityID || roleValue.Wire != 0 || roleValue.Value == 0 {
						continue
					}
					if hasEntityID && entityID != roleValue.Value {
						conflictingEntityID = true
					}
					entityID, hasEntityID = roleValue.Value, true
				}
				if !hasEntityID || conflictingEntityID {
					continue
				}
				p.Stats.PlayerRoleReports++
				p.ObserveLocalRoleEntity(entityID)
			}
		}
	}
}
