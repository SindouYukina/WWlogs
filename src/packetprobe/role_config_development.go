//go:build wwdev

package main

import (
	"encoding/json"
)

func (m *gameMaster) resolveRoleConfigRole(rawRoleID uint64) (roleConfigRoleResolution, bool) {
	if m == nil {
		return roleConfigRoleResolution{}, false
	}
	if character, ok := m.characters[rawRoleID]; ok {
		return roleConfigRoleResolution{
			CanonicalRoleID: character.ID,
			Character:       character,
			Status:          "MATCHED_ROLEINFO_ID",
			Basis:           "RoleConfigInfoNotify.RoleConfigs.RoleId -> roleinfo.id",
		}, true
	}
	parentID, ok := m.trialRoleParents[rawRoleID]
	if !ok {
		return roleConfigRoleResolution{}, false
	}
	character, ok := m.characters[parentID]
	if !ok {
		return roleConfigRoleResolution{}, false
	}
	return roleConfigRoleResolution{
		CanonicalRoleID: character.ID,
		Character:       character,
		Status:          "CORRELATED_TRIALROLEINFO_PARENT",
		Basis:           "RoleConfigInfoNotify.RoleConfigs.RoleId -> trialroleinfo.id -> trialroleinfo.parent_role_id -> roleinfo.id",
	}, true
}

// archiveRoleConfigRecords writes one result per RoleConfigInfo in a decoded notify.
func (p *processor) archiveRoleConfigRecords(frame gameFrame, decoded *decodedMessage) {
	if p == nil || p.roleConfigOut == nil || decoded == nil || decoded.Type != "RoleConfigInfoNotify" {
		return
	}
	for index, config := range childMessages(decoded, "RoleConfigs") {
		roleID, hasRoleID := scalarValue(config, "RoleId")
		record := roleConfigRecord{
			SchemaVersion:           roleConfigRecordSchemaVersion,
			FrameOrdinal:            p.frameOrdinal - 1,
			SessionNumber:           p.sessionNumber,
			Direction:               frame.direction,
			CommandID:               frame.commandID,
			ConfigIndex:             index,
			IDNamespace:             "RoleConfigInfoNotify.RoleConfigs.RoleId",
			CanonicalRoleNameStatus: string(resolutionUnknown),
			Status:                  string(resolutionUnknown),
			Basis:                   "no RoleInfo definition or TrialRoleInfo parent relation for the observed RoleId",
			Evidence:                "raw RoleId is observed in the decrypted RoleConfigInfoNotify; canonical identity requires a unique normalized master relation",
		}
		if !hasRoleID {
			record.Status = "MISSING_ROLE_ID"
			record.Basis = "RoleConfigInfo.RoleId is absent from the decoded message"
			record.Evidence = "the schema-decoded RoleConfigInfo contains no RoleId scalar"
		} else {
			record.RawRoleID = &roleID
			if roleID > 0 && p.masters != nil {
				if resolution, ok := p.masters.resolveRoleConfigRole(uint64(roleID)); ok {
					canonicalID := resolution.CanonicalRoleID
					record.CanonicalRoleID = &canonicalID
					record.CanonicalRoleName = resolution.Character.Name
					record.CanonicalRoleNameStatus = resolution.Character.NameStatus
					record.Language = resolution.Character.Language
					record.Status = resolution.Status
					record.Basis = resolution.Basis
					record.Evidence = "observed RoleId joined through the stated key path to a normalized RoleInfo definition"
				}
			} else if roleID <= 0 {
				record.Basis = "RoleConfigInfo.RoleId is not a positive role key"
			}
		}
		line, err := json.Marshal(record)
		if err != nil {
			p.roleConfigErrors++
			continue
		}
		if _, err := p.roleConfigOut.Write(append(line, '\n')); err != nil {
			p.roleConfigErrors++
		}
	}
}
