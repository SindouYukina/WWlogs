//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

func (c *commandCensus) write(path string, stats captureStats, clientVersion *clientVersionReport, protocol protocolReport) error {
	entries := make([]commandCensusEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		copy := *entry
		if copy.CRCValidFrames == 0 && copy.CRCFailedHeaders > 0 {
			copy.Status = "UNKNOWN DUE TO CRC/CAPTURE LIMITATION"
		} else if copy.CRCValidFrames > 0 && copy.DecodedFrames == copy.CRCValidFrames {
			copy.Status = "PRESENT AND DECODED"
		}
		if len(copy.FailureReasons) == 0 {
			copy.FailureReasons = nil
		}
		entries = append(entries, copy)
	}
	for _, candidate := range []struct {
		id        uint16
		direction string
	}{
		{cmdUpdateSceneGroupDataNotify, "in"}, {cmdPlayerLivingStatesNotify, "in"}, {cmdAddSysBuffNotify, "in"}, {cmdApplyGameplayEffectNotify, "in"}, {cmdApplyGameplayEffectPush, "in"}, {cmdRemoveGameplayEffectNotify, "in"}, {cmdRemoveGameplayEffectPush, "in"}, {cmdSimpleCombatEntityBuffUpdateNotify, "in"}, {cmdSimpleCombatEntityBuffLayerCountNotify, "in"}, {cmdBuffStackCountNotify, "in"}, {cmdBuffDurationNotify, "in"}, {cmdOrderApplyBuffNotify, "in"}, {cmdOrderRemoveBuffNotify, "in"}, {cmdRefreshBuffDurationPush, "in"}, {cmdRemoveSysBuffNotify, "in"}, {cmdSimpleCombatEntityAttributeUpdateNotify, "in"}, {cmdSimpleCombatEntitySyncAttributePush, "in"}, {cmdUpdateSysBuffNotify, "in"}, {cmdJoinSceneNotify, "in"}, {cmdEntityAddNotify, "in"}, {cmdEntityRemoveNotify, "in"}, {cmdEntityActiveRequest, "out"}, {cmdEntityActiveResponse, "in"}, {cmdCombatSendPackRequest, "out"}, {cmdCombatSendPackResponse, "in"},
		{cmdApplyGameplayEffectRequest, "out"}, {cmdApplyGameplayEffectResponse, "in"}, {cmdRemoveGameplayEffectRequest, "out"}, {cmdRemoveGameplayEffectResponse, "in"}, {cmdOrderApplyBuffRequest, "out"}, {cmdOrderApplyBuffResponse, "in"}, {cmdOrderRemoveBuffRequest, "out"}, {cmdOrderRemoveBuffResponse, "in"}, {cmdApplyBuffS2cRequestNotify, "in"}, {cmdApplyBuffS2cResponsePush, "in"}, {cmdRemoveBuffS2cRequestNotify, "in"}, {cmdRemoveBuffS2cResponsePush, "in"}, {cmdRemoveBuffByIDS2cRequestNotify, "in"}, {cmdRemoveBuffByIDS2cResponsePush, "in"}, {cmdActivateBuffRequest, "out"}, {cmdActivateBuffResponse, "in"}, {cmdActivateBuffNotify, "in"}, {cmdActivateBuffPush, "in"},
		{cmdBuffStackCountRequest, "out"}, {cmdBuffStackCountResponse, "in"}, {cmdBuffStackCountPush, "in"}, {cmdBuffEffectRequest, "out"}, {cmdBuffEffectResponse, "in"}, {cmdBuffEffectPush, "in"}, {cmdBuffEffectExecuteRequest, "out"}, {cmdBuffEffectExecuteResponse, "in"}, {cmdBuffEffectExecutePush, "in"}, {cmdEntityBuffProducerRequest, "out"}, {cmdEntityBuffProducerResponse, "in"}, {cmdEntityBuffProducerOperateRequest, "out"}, {cmdEntityBuffProducerOperateResponse, "in"}, {cmdOrderRemoveBuffByTagsRequest, "out"}, {cmdOrderRemoveBuffByTagsResponse, "in"}, {cmdOrderRemoveBuffByTagsNotify, "in"}, {cmdRemoveBuffByServerIDS2cRequestNotify, "in"}, {cmdAtomicProcessAddBuffRequest, "out"}, {cmdAtomicProcessAddBuffResponse, "in"}, {cmdAtomicProcessRemoveBuffRequest, "out"}, {cmdAtomicProcessRemoveBuffResponse, "in"}, {cmdTransformBuffStackNotify, "in"},
		{cmdUpdatePlayerAllFightRoleNotify, "in"}, {cmdUpdatePlayersFightRolesInfoNotify, "in"}, {cmdUpdatePlayerSingleFightRoleNotify, "in"}, {cmdChangePlayerFightRoleNotify, "in"},
		{cmdLoginRequest, "out"}, {cmdCheckClientPush, "out"},
	} {
		key := censusKey{CommandID: candidate.id, Direction: candidate.direction}
		if _, exists := c.entries[key]; !exists {
			entry := commandCensusEntry{CommandID: candidate.id, Name: commandName(candidate.id), Direction: candidate.direction, Status: "NOT OBSERVED"}
			if candidate.id == cmdLoginRequest || candidate.id == cmdCheckClientPush {
				entry.SchemaBasis = "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"
			} else if hasCurrentProtocolSchema(candidate.id) {
				entry.SchemaBasis = censusSchemaBasis()
			}
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CommandID == entries[j].CommandID {
			return entries[i].Direction < entries[j].Direction
		}
		return entries[i].CommandID < entries[j].CommandID
	})
	inner := make(map[string]int, len(c.innerEntries))
	for name, count := range c.innerEntries {
		inner[name] = count
	}
	report := struct {
		GeneratedAt        time.Time               `json:"generatedAt"`
		SessionCount       int                     `json:"sessionCount"`
		ClientVersion      *clientVersionReport    `json:"clientVersion,omitempty"`
		Protocol           protocolReport          `json:"protocol"`
		Transport          map[string]int          `json:"transport"`
		InnerRPCs          map[string]int          `json:"innerRpcCounts"`
		CombatNotifyFields map[uint64]int          `json:"combatNotifyOneofFieldCounts,omitempty"`
		Commands           []commandCensusEntry    `json:"commands"`
		CRCFailures        []crcFailureObservation `json:"crcFailureObservations,omitempty"`
		CRCOmitted         int                     `json:"crcFailureObservationsOmitted,omitempty"`
		Malformed          []malformedObservation  `json:"malformedObservations,omitempty"`
		MalformedOmitted   int                     `json:"malformedObservationsOmitted,omitempty"`
		MoveEntityIDs      []struct {
			EntityID         uint64         `json:"entityId"`
			Count            int            `json:"observations"`
			OriginatorValues map[uint64]int `json:"originatorRawValueCounts,omitempty"`
		} `json:"movePackageEntityIds,omitempty"`
		MoveSceneOwnerIDs []struct {
			EntityID uint64 `json:"sceneOwnerId"`
			Count    int    `json:"observations"`
		} `json:"movePackageSceneOwnerIds,omitempty"`
		PlayerFollowers       []playerFollowerObservation `json:"playerFollowerObservations,omitempty"`
		MoveEntityOmitted     int                         `json:"movePackageEntityIdsOmitted,omitempty"`
		MoveOwnerOmitted      int                         `json:"movePackageSceneOwnerIdsOmitted,omitempty"`
		PlayerFollowerOmitted int                         `json:"playerFollowerObservationsOmitted,omitempty"`
		EntityKeys            []entityKeyObservation      `json:"entityNotifyRawIds,omitempty"`
	}{GeneratedAt: time.Now().UTC(), SessionCount: c.sessions, ClientVersion: clientVersion, Protocol: protocol, Transport: c.transport, InnerRPCs: inner, CombatNotifyFields: c.combatNotifyFields, Commands: entries,
		CRCFailures: c.crcFailures, CRCOmitted: c.crcOmitted, Malformed: c.malformedRecords, MalformedOmitted: c.malformedOmitted}
	report.PlayerFollowers = c.playerFollowers
	report.MoveEntityOmitted = c.moveEntityOmitted
	report.MoveOwnerOmitted = c.moveOwnerOmitted
	report.PlayerFollowerOmitted = c.playerFollowerOmitted
	for key, count := range c.entityKeyValues {
		report.EntityKeys = append(report.EntityKeys, entityKeyObservation{CommandID: key.commandID, FieldNumber: key.field, RawID: key.value, Count: count})
	}
	sort.Slice(report.EntityKeys, func(i, j int) bool {
		if report.EntityKeys[i].CommandID != report.EntityKeys[j].CommandID {
			return report.EntityKeys[i].CommandID < report.EntityKeys[j].CommandID
		}
		if report.EntityKeys[i].FieldNumber != report.EntityKeys[j].FieldNumber {
			return report.EntityKeys[i].FieldNumber < report.EntityKeys[j].FieldNumber
		}
		return report.EntityKeys[i].RawID < report.EntityKeys[j].RawID
	})
	for id, aggregate := range c.moveEntities {
		report.MoveEntityIDs = append(report.MoveEntityIDs, struct {
			EntityID         uint64         `json:"entityId"`
			Count            int            `json:"observations"`
			OriginatorValues map[uint64]int `json:"originatorRawValueCounts,omitempty"`
		}{EntityID: id, Count: aggregate.observations, OriginatorValues: aggregate.originators})
	}
	sort.Slice(report.MoveEntityIDs, func(i, j int) bool { return report.MoveEntityIDs[i].EntityID < report.MoveEntityIDs[j].EntityID })
	for id, count := range c.moveSceneOwners {
		report.MoveSceneOwnerIDs = append(report.MoveSceneOwnerIDs, struct {
			EntityID uint64 `json:"sceneOwnerId"`
			Count    int    `json:"observations"`
		}{EntityID: id, Count: count})
	}
	sort.Slice(report.MoveSceneOwnerIDs, func(i, j int) bool {
		return report.MoveSceneOwnerIDs[i].EntityID < report.MoveSceneOwnerIDs[j].EntityID
	})
	report.Transport["udpPackets"] = stats.udpPackets
	report.Transport["kcpPushSegments"] = stats.kcpPush
	report.Transport["kcpControlSegments"] = stats.kcpControl
	report.Transport["nonKcpUdpDatagrams"] = stats.nonKCPUDP
	report.Transport["invalidKcpDatagrams"] = stats.invalidKCP
	report.Transport["kcpOrderedSegments"] = stats.kcpOrdered
	report.Transport["kcpMessages"] = stats.kcpMessages
	report.Transport["crcValidFrames"] = stats.frames
	report.Transport["badCRCFrames"] = stats.badCRC
	report.Transport["malformedFrames"] = stats.malformed
	report.Transport["kernelDrops"] = stats.kernelDrops
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal command census: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write command census: %w", err)
	}
	return nil
}
