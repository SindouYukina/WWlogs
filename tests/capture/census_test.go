package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wwlogs/internal/versioninfo"
)

func TestCommandCensusSeparatesDecodedFramesFromUnimplementedSchemas(t *testing.T) {
	census := newCommandCensus()
	when := time.Date(2026, time.October, 26, 5, 0, 0, 0, time.UTC)
	entityAdd := census.observeFrame(gameFrame{commandID: 25185, direction: "in", timestamp: when}, 1)
	entityAdd.DecryptedFrames++
	entityAdd.DecodedFrames++
	joinScene := census.observeFrame(gameFrame{commandID: 18559, direction: "in", timestamp: when.Add(time.Second)}, 1)
	joinScene.DecryptedFrames++
	joinScene.GenericProtoValid++
	joinScene.DecodedFrames++
	phantomRecommend := census.observeFrame(gameFrame{commandID: 22535, direction: "in", timestamp: when.Add(time.Second)}, 1)
	phantomRecommend.DecryptedFrames++
	phantomRecommend.GenericProtoValid++
	phantomRecommend.FailureReasons["message schema not implemented"]++
	census.failedCRC(crcFailureObservation{Direction: "in", CommandID: 26702, Timestamp: when.Add(2 * time.Second), ReceivedCRC: 1, ComputedCRC: 2})
	census.malformed("short_or_unaligned_frame_candidate", when.Add(3*time.Second), 4, 8, 32)
	census.sessions = 1

	path := filepath.Join(t.TempDir(), "census.json")
	if err := census.write(path, captureStats{udpPackets: 3, frames: 2, badCRC: 1}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		SessionCount int                     `json:"sessionCount"`
		Commands     []commandCensusEntry    `json:"commands"`
		CRCFailures  []crcFailureObservation `json:"crcFailureObservations"`
		Malformed    []malformedObservation  `json:"malformedObservations"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.SessionCount != 1 || len(report.Commands) != 72 {
		t.Fatalf("session/command counts = %d/%d", report.SessionCount, len(report.Commands))
	}
	statuses := make(map[uint16]string, len(report.Commands))
	for _, entry := range report.Commands {
		if (entry.CommandID == 103 || entry.CommandID == 116) && (entry.Direction != "out" || entry.SchemaBasis != "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit") {
			t.Fatalf("version command metadata was not retained: %+v", entry)
		}
		statuses[entry.CommandID] = entry.Status
	}
	if statuses[25185] != "PRESENT AND DECODED" {
		t.Fatalf("EntityAddNotify census status = %q", statuses[25185])
	}
	if statuses[18559] != "PRESENT AND DECODED" || statuses[26702] != "UNKNOWN DUE TO CRC/CAPTURE LIMITATION" {
		t.Fatalf("JoinScene/CRC-header statuses = %q/%q", statuses[18559], statuses[26702])
	}
	if statuses[17286] != "NOT OBSERVED" {
		t.Fatalf("UpdateSceneGroupDataNotify census status = %q", statuses[17286])
	}
	if statuses[25830] != "NOT OBSERVED" {
		t.Fatalf("ApplyGameplayEffectNotify census status = %q", statuses[25830])
	}
	wantBuffCommands := map[uint16]struct{ name, direction string }{
		16551: {name: "OrderRemoveBuffByTagsResponse", direction: "in"},
		16727: {name: "AtomicProcessAddBuffRequest", direction: "out"},
		16764: {name: "SimpleCombatEntityAttributeUpdateNotify", direction: "in"},
		16890: {name: "ApplyBuffS2cResponsePush", direction: "in"},
		16967: {name: "BuffEffectResponse", direction: "in"},
		16974: {name: "BuffDurationNotify", direction: "in"},
		17149: {name: "ActivateBuffRequest", direction: "out"},
		17452: {name: "OrderRemoveBuffResponse", direction: "in"},
		17533: {name: "BuffEffectExecuteResponse", direction: "in"},
		17807: {name: "OrderRemoveBuffNotify", direction: "in"},
		17893: {name: "SimpleCombatEntityBuffUpdateNotify", direction: "in"},
		18099: {name: "BuffEffectExecutePush", direction: "in"},
		18345: {name: "RemoveGameplayEffectNotify", direction: "in"},
		18641: {name: "ApplyBuffS2cRequestNotify", direction: "in"},
		18682: {name: "EntityBuffProducerRequest", direction: "out"},
		19162: {name: "RemoveBuffByIdS2cResponsePush", direction: "in"},
		19482: {name: "RemoveBuffByIdS2cRequestNotify", direction: "in"},
		19678: {name: "AtomicProcessAddBuffResponse", direction: "in"},
		19820: {name: "BuffStackCountRequest", direction: "out"},
		19884: {name: "RemoveGameplayEffectRequest", direction: "out"},
		19917: {name: "ActivateBuffResponse", direction: "in"},
		20022: {name: "RemoveBuffS2cRequestNotify", direction: "in"},
		20358: {name: "RemoveGameplayEffectPush", direction: "in"},
		20706: {name: "BuffEffectPush", direction: "in"},
		20926: {name: "ActivateBuffPush", direction: "in"},
		21666: {name: "BuffStackCountPush", direction: "in"},
		21800: {name: "OrderApplyBuffRequest", direction: "out"},
		21803: {name: "AddSysBuffNotify", direction: "in"},
		22006: {name: "EntityBuffProducerResponse", direction: "in"},
		22102: {name: "SimpleCombatEntityBuffLayerCountNotify", direction: "in"},
		22540: {name: "BuffEffectRequest", direction: "out"},
		22742: {name: "OrderRemoveBuffByTagsRequest", direction: "out"},
		22915: {name: "ApplyGameplayEffectResponse", direction: "in"},
		22973: {name: "ApplyGameplayEffectRequest", direction: "out"},
		23095: {name: "AtomicProcessRemoveBuffResponse", direction: "in"},
		23494: {name: "RemoveBuffByServerIdS2cRequestNotify", direction: "in"},
		23971: {name: "RemoveBuffS2cResponsePush", direction: "in"},
		24520: {name: "OrderRemoveBuffByTagsNotify", direction: "in"},
		24831: {name: "RemoveSysBuffNotify", direction: "in"},
		25830: {name: "ApplyGameplayEffectNotify", direction: "in"},
		26094: {name: "BuffEffectExecuteRequest", direction: "out"},
		26251: {name: "BuffStackCountResponse", direction: "in"},
		26471: {name: "OrderApplyBuffNotify", direction: "in"},
		26503: {name: "EntityBuffProducerOperateRequest", direction: "out"},
		27178: {name: "RefreshBuffDurationPush", direction: "in"},
		27203: {name: "EntityBuffProducerOperateResponse", direction: "in"},
		27266: {name: "OrderApplyBuffResponse", direction: "in"},
		27565: {name: "AtomicProcessRemoveBuffRequest", direction: "out"},
		27991: {name: "TransformBuffStackNotify", direction: "in"},
		28077: {name: "RemoveGameplayEffectResponse", direction: "in"},
		28221: {name: "ActivateBuffNotify", direction: "in"},
		29113: {name: "SimpleCombatEntitySyncAttributePush", direction: "in"},
		29255: {name: "OrderRemoveBuffRequest", direction: "out"},
		29363: {name: "BuffStackCountNotify", direction: "in"},
		29488: {name: "ApplyGameplayEffectPush", direction: "in"},
		29655: {name: "UpdateSysBuffNotify", direction: "in"},
	}
	for commandID, want := range wantBuffCommands {
		found := false
		for _, entry := range report.Commands {
			if entry.CommandID == commandID {
				found = entry.Name == want.name && entry.Direction == want.direction && entry.Status == "NOT OBSERVED" && entry.SchemaBasis == versioninfo.WindowsSchemaLabel()+" Aki.Protocol metadata; client version observed, schema compatibility unverified"
				break
			}
		}
		if !found {
			t.Errorf("Buff schema census entry %d (%s) missing or incorrect", commandID, want.name)
		}
	}
	if len(report.Commands) != 23+len(wantBuffCommands)-7 {
		t.Fatalf("command census entries = %d, want %d", len(report.Commands), 23+len(wantBuffCommands)-7)
	}
	wantRoleNotifies := map[uint16]string{
		21051: "UpdatePlayerAllFightRoleNotify",
		18899: "UpdatePlayersFightRolesInfoNotify",
		18580: "UpdatePlayerSingleFightRoleNotify",
		27633: "ChangePlayerFightRoleNotify",
	}
	for _, entry := range report.Commands {
		if entry.CommandID == 17286 && (entry.Name != "UpdateSceneGroupDataNotify" || entry.Direction != "in" || entry.SchemaBasis != versioninfo.WindowsSchemaLabel()+" Aki.Protocol metadata; client version observed, schema compatibility unverified") {
			t.Fatalf("scene-group mapping provenance was not retained: %+v", entry)
		}
		if entry.CommandID == 18559 && (entry.Name != "JoinSceneNotify" || entry.SchemaBasis != versioninfo.WindowsSchemaLabel()+" Aki.Protocol metadata; client version observed, schema compatibility unverified") {
			t.Fatalf("JoinSceneNotify provenance was not retained: %+v", entry)
		}
		if name, ok := wantRoleNotifies[entry.CommandID]; ok && (entry.Name != name || entry.Direction != "in" || entry.Status != "NOT OBSERVED" || entry.SchemaBasis != versioninfo.WindowsSchemaLabel()+" Aki.Protocol metadata; client version observed, schema compatibility unverified") {
			t.Fatalf("fight-role notification metadata was not retained: %+v", entry)
		}
	}
	var foundPhantom bool
	for _, entry := range report.Commands {
		if entry.CommandID == 22535 {
			foundPhantom = entry.Name == "PhantomAttrRecommendResponse" && entry.Status == "PRESENT BUT UNDECODED" && entry.SchemaBasis == versioninfo.WindowsSchemaLabel()+" Aki.Protocol metadata; client version observed, schema compatibility unverified"
		}
	}
	if !foundPhantom {
		t.Fatal("PhantomAttrRecommendResponse metadata was not retained in census")
	}
	if len(report.CRCFailures) != 1 || report.CRCFailures[0].ReceivedCRC != 1 || report.CRCFailures[0].ComputedCRC != 2 {
		t.Fatalf("CRC failure detail was not retained: %+v", report.CRCFailures)
	}
	if len(report.Malformed) != 1 || report.Malformed[0].Reason != "short_or_unaligned_frame_candidate" {
		t.Fatalf("malformed candidate detail was not retained: %+v", report.Malformed)
	}
}

func TestClientVersionCensusSchemaBasisExcludesSiblingIdentifiers(t *testing.T) {
	for _, commandID := range []uint16{103, 116} {
		entry := newCommandCensus().entry(commandID, "out")
		if want := "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"; entry.SchemaBasis != want {
			t.Fatalf("command %d schema basis = %q, want %q", commandID, entry.SchemaBasis, want)
		}
	}
}

func TestFrameErrorObservationsAreBounded(t *testing.T) {
	census := newCommandCensus()
	when := time.Date(2026, time.October, 26, 5, 0, 0, 0, time.UTC)
	for i := 0; i < maxFrameErrorObservations+3; i++ {
		census.failedCRC(crcFailureObservation{Direction: "in", CommandID: uint16(i), Timestamp: when})
		census.malformed("candidate", when, i, 1, 2)
	}
	if len(census.crcFailures) != maxFrameErrorObservations || census.crcOmitted != 3 {
		t.Fatalf("CRC detail cap = %d, omitted %d", len(census.crcFailures), census.crcOmitted)
	}
	if len(census.malformedRecords) != maxFrameErrorObservations || census.malformedOmitted != 3 {
		t.Fatalf("malformed detail cap = %d, omitted %d", len(census.malformedRecords), census.malformedOmitted)
	}
}

func TestCommandCensusLinksCRCFailureToNeighborFrames(t *testing.T) {
	census := newCommandCensus()
	previous := frameBoundaryObservation{Offset: 0, MessageType: 1, Sequence: 12, CommandID: 1234, FrameLength: 14}
	index := census.failedCRC(crcFailureObservation{Direction: "in", Offset: 14, PreviousValid: &previous})
	next := frameBoundaryObservation{Offset: 40, MessageType: 1, Sequence: 14, CommandID: 5678, FrameLength: 15}
	census.setNextValidFrame(index, next)

	observation := census.crcFailures[index]
	if observation.PreviousValid == nil || observation.PreviousValid.Sequence != 12 {
		t.Fatalf("previous valid frame context = %#v", observation.PreviousValid)
	}
	if observation.NextValid == nil || observation.NextValid.Sequence != 14 {
		t.Fatalf("next valid frame context = %#v", observation.NextValid)
	}
	census.setNextValidFrame(index, frameBoundaryObservation{Sequence: 99})
	if observation.NextValid.Sequence != 14 {
		t.Fatalf("next valid frame context was overwritten: %#v", observation.NextValid)
	}
}

func TestExtractMovePackageObservationUsesSchemaFieldNumbers(t *testing.T) {
	message := []byte{0x20, 0x63, 0x62, 0x04, 0x08, 0x07, 0x10, 0x03, 0x62, 0x02, 0x08, 0x2a}
	observation, err := extractMovePackageObservation(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Entities) != 2 || observation.Entities[0].EntityID != 7 || !observation.Entities[0].HasOriginator || observation.Entities[0].OriginatorValue != 3 || observation.Entities[1].EntityID != 42 || observation.Entities[1].HasOriginator {
		t.Fatalf("MovePackagePush entities = %+v", observation.Entities)
	}
	if len(observation.SceneOwnerIDs) != 1 || observation.SceneOwnerIDs[0] != 99 {
		t.Fatalf("MovePackagePush scene owner IDs = %v", observation.SceneOwnerIDs)
	}
}

func TestExtractPlayerFollowerObservationUsesSchemaFieldNumbers(t *testing.T) {
	message := []byte{0x68, 0x63, 0x52, 0x04, 0x08, 0x02, 0x10, 0x07}
	observation, err := extractPlayerFollowerObservation(message)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.HasPlayerID || observation.PlayerID != 99 || len(observation.Followers) != 1 {
		t.Fatalf("PlayerFollowerNotify observation = %+v", observation)
	}
	follower := observation.Followers[0]
	if !follower.HasType || follower.Type != 2 || !follower.HasEntityID || follower.EntityID != 7 {
		t.Fatalf("follower record = %+v", follower)
	}
}

func TestExtractVarintFieldKeepsRepeatedValues(t *testing.T) {
	values, err := extractVarintField([]byte{0x28, 0x07, 0x28, 0x2a, 0x30, 0x63}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != 7 || values[1] != 42 {
		t.Fatalf("field 5 values = %v", values)
	}
}
