//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"wwlogs/internal/versioninfo"
)

func (c *commandCensus) write(path string, stats captureStats, clientVersion *clientVersionReport, protocol protocolReport) error {
	entries := make([]commandCensusEntry, 0, len(c.entries))
	// The current protocol carries an internal command under its own wire number; that internal command is present.
	observedInternal := make(map[censusKey]bool)
	for key, entry := range c.entries {
		if entry.InternalCommandID != 0 {
			observedInternal[censusKey{CommandID: entry.InternalCommandID, Direction: key.Direction}] = true
		}
	}
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
		{17286, "in"}, {22492, "in"}, {21803, "in"}, {25830, "in"}, {29488, "in"}, {18345, "in"}, {20358, "in"}, {17893, "in"}, {22102, "in"}, {29363, "in"}, {16974, "in"}, {26471, "in"}, {17807, "in"}, {27178, "in"}, {24831, "in"}, {16764, "in"}, {29113, "in"}, {29655, "in"}, {18559, "in"}, {25185, "in"}, {17769, "in"}, {28481, "out"}, {26702, "in"}, {19470, "out"}, {20332, "in"},
		{22973, "out"}, {22915, "in"}, {19884, "out"}, {28077, "in"}, {21800, "out"}, {27266, "in"}, {29255, "out"}, {17452, "in"}, {18641, "in"}, {16890, "in"}, {20022, "in"}, {23971, "in"}, {19482, "in"}, {19162, "in"}, {17149, "out"}, {19917, "in"}, {28221, "in"}, {20926, "in"},
		{19820, "out"}, {26251, "in"}, {21666, "in"}, {22540, "out"}, {16967, "in"}, {20706, "in"}, {26094, "out"}, {17533, "in"}, {18099, "in"}, {18682, "out"}, {22006, "in"}, {26503, "out"}, {27203, "in"}, {22742, "out"}, {16551, "in"}, {24520, "in"}, {23494, "in"}, {16727, "out"}, {19678, "in"}, {27565, "out"}, {23095, "in"}, {27991, "in"},
		{21051, "in"}, {18899, "in"}, {18580, "in"}, {27633, "in"},
		{103, "out"}, {116, "out"},
	} {
		key := censusKey{CommandID: candidate.id, Direction: candidate.direction}
		if _, exists := c.entries[key]; !exists && !observedInternal[key] {
			entry := commandCensusEntry{CommandID: candidate.id, Name: commandName(candidate.id), Direction: candidate.direction, Status: "NOT OBSERVED"}
			if candidate.id == 103 || candidate.id == 116 {
				entry.SchemaBasis = "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"
			} else if hasWindowsProtocolSchema(candidate.id) {
				entry.SchemaBasis = versioninfo.WindowsSchemaLabel() + " Aki.Protocol metadata; client version observed, schema compatibility unverified"
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
