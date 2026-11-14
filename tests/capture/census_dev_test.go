//go:build wwdev

package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandCensusSeparatesDecodedFramesFromUnimplementedSchemas(t *testing.T) {
	census := newCommandCensus()
	when := time.Date(2026, time.October, 26, 5, 0, 0, 0, time.UTC)
	// The processor names an entry from the current protocol only for a frame its protocol session decodes.
	entityAdd := census.observeFrame(gameFrame{commandID: cmdEntityAddNotify, direction: "in", timestamp: when}, 1)
	entityAdd.describeCurrentProtocol()
	entityAdd.DecryptedFrames++
	entityAdd.DecodedFrames++
	joinScene := census.observeFrame(gameFrame{commandID: cmdJoinSceneNotify, direction: "in", timestamp: when.Add(time.Second)}, 1)
	joinScene.describeCurrentProtocol()
	joinScene.DecryptedFrames++
	joinScene.GenericProtoValid++
	joinScene.DecodedFrames++
	phantomRecommend := census.observeFrame(gameFrame{commandID: cmdPhantomAttrRecommendResponse, direction: "in", timestamp: when.Add(time.Second)}, 1)
	phantomRecommend.describeCurrentProtocol()
	phantomRecommend.DecryptedFrames++
	phantomRecommend.GenericProtoValid++
	phantomRecommend.FailureReasons["message schema not implemented"]++
	census.failedCRC(crcFailureObservation{Direction: "in", CommandID: cmdEntityActiveResponse, Timestamp: when.Add(2 * time.Second), ReceivedCRC: 1, ComputedCRC: 2})
	census.malformed("short_or_unaligned_frame_candidate", when.Add(3*time.Second), 4, 8, 32)
	census.sessions = 1

	path := filepath.Join(t.TempDir(), "census.json")
	if err := census.write(path, captureStats{udpPackets: 3, frames: 2, badCRC: 1}, nil, protocolReport{}); err != nil {
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
		if (entry.CommandID == cmdLoginRequest || entry.CommandID == cmdCheckClientPush) && (entry.Direction != "out" || entry.SchemaBasis != "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit") {
			t.Fatalf("version command metadata was not retained: %+v", entry)
		}
		statuses[entry.CommandID] = entry.Status
	}
	if statuses[cmdEntityAddNotify] != "PRESENT AND DECODED" {
		t.Fatalf("EntityAddNotify census status = %q", statuses[cmdEntityAddNotify])
	}
	if statuses[cmdJoinSceneNotify] != "PRESENT AND DECODED" || statuses[cmdEntityActiveResponse] != "UNKNOWN DUE TO CRC/CAPTURE LIMITATION" {
		t.Fatalf("JoinScene/CRC-header statuses = %q/%q", statuses[cmdJoinSceneNotify], statuses[cmdEntityActiveResponse])
	}
	if statuses[cmdUpdateSceneGroupDataNotify] != "NOT OBSERVED" {
		t.Fatalf("UpdateSceneGroupDataNotify census status = %q", statuses[cmdUpdateSceneGroupDataNotify])
	}
	if statuses[cmdApplyGameplayEffectNotify] != "NOT OBSERVED" {
		t.Fatalf("ApplyGameplayEffectNotify census status = %q", statuses[cmdApplyGameplayEffectNotify])
	}
	wantBuffCommands := map[uint16]struct{ name, direction string }{
		cmdOrderRemoveBuffByTagsResponse:           {name: "OrderRemoveBuffByTagsResponse", direction: "in"},
		cmdAtomicProcessAddBuffRequest:             {name: "AtomicProcessAddBuffRequest", direction: "out"},
		cmdSimpleCombatEntityAttributeUpdateNotify: {name: "SimpleCombatEntityAttributeUpdateNotify", direction: "in"},
		cmdApplyBuffS2cResponsePush:                {name: "ApplyBuffS2cResponsePush", direction: "in"},
		cmdBuffEffectResponse:                      {name: "BuffEffectResponse", direction: "in"},
		cmdBuffDurationNotify:                      {name: "BuffDurationNotify", direction: "in"},
		cmdActivateBuffRequest:                     {name: "ActivateBuffRequest", direction: "out"},
		cmdOrderRemoveBuffResponse:                 {name: "OrderRemoveBuffResponse", direction: "in"},
		cmdBuffEffectExecuteResponse:               {name: "BuffEffectExecuteResponse", direction: "in"},
		cmdOrderRemoveBuffNotify:                   {name: "OrderRemoveBuffNotify", direction: "in"},
		cmdSimpleCombatEntityBuffUpdateNotify:      {name: "SimpleCombatEntityBuffUpdateNotify", direction: "in"},
		cmdBuffEffectExecutePush:                   {name: "BuffEffectExecutePush", direction: "in"},
		cmdRemoveGameplayEffectNotify:              {name: "RemoveGameplayEffectNotify", direction: "in"},
		cmdApplyBuffS2cRequestNotify:               {name: "ApplyBuffS2cRequestNotify", direction: "in"},
		cmdEntityBuffProducerRequest:               {name: "EntityBuffProducerRequest", direction: "out"},
		cmdRemoveBuffByIDS2cResponsePush:           {name: "RemoveBuffByIdS2cResponsePush", direction: "in"},
		cmdRemoveBuffByIDS2cRequestNotify:          {name: "RemoveBuffByIdS2cRequestNotify", direction: "in"},
		cmdAtomicProcessAddBuffResponse:            {name: "AtomicProcessAddBuffResponse", direction: "in"},
		cmdBuffStackCountRequest:                   {name: "BuffStackCountRequest", direction: "out"},
		cmdRemoveGameplayEffectRequest:             {name: "RemoveGameplayEffectRequest", direction: "out"},
		cmdActivateBuffResponse:                    {name: "ActivateBuffResponse", direction: "in"},
		cmdRemoveBuffS2cRequestNotify:              {name: "RemoveBuffS2cRequestNotify", direction: "in"},
		cmdRemoveGameplayEffectPush:                {name: "RemoveGameplayEffectPush", direction: "in"},
		cmdBuffEffectPush:                          {name: "BuffEffectPush", direction: "in"},
		cmdActivateBuffPush:                        {name: "ActivateBuffPush", direction: "in"},
		cmdBuffStackCountPush:                      {name: "BuffStackCountPush", direction: "in"},
		cmdOrderApplyBuffRequest:                   {name: "OrderApplyBuffRequest", direction: "out"},
		cmdAddSysBuffNotify:                        {name: "AddSysBuffNotify", direction: "in"},
		cmdEntityBuffProducerResponse:              {name: "EntityBuffProducerResponse", direction: "in"},
		cmdSimpleCombatEntityBuffLayerCountNotify:  {name: "SimpleCombatEntityBuffLayerCountNotify", direction: "in"},
		cmdBuffEffectRequest:                       {name: "BuffEffectRequest", direction: "out"},
		cmdOrderRemoveBuffByTagsRequest:            {name: "OrderRemoveBuffByTagsRequest", direction: "out"},
		cmdApplyGameplayEffectResponse:             {name: "ApplyGameplayEffectResponse", direction: "in"},
		cmdApplyGameplayEffectRequest:              {name: "ApplyGameplayEffectRequest", direction: "out"},
		cmdAtomicProcessRemoveBuffResponse:         {name: "AtomicProcessRemoveBuffResponse", direction: "in"},
		cmdRemoveBuffByServerIDS2cRequestNotify:    {name: "RemoveBuffByServerIdS2cRequestNotify", direction: "in"},
		cmdRemoveBuffS2cResponsePush:               {name: "RemoveBuffS2cResponsePush", direction: "in"},
		cmdOrderRemoveBuffByTagsNotify:             {name: "OrderRemoveBuffByTagsNotify", direction: "in"},
		cmdRemoveSysBuffNotify:                     {name: "RemoveSysBuffNotify", direction: "in"},
		cmdApplyGameplayEffectNotify:               {name: "ApplyGameplayEffectNotify", direction: "in"},
		cmdBuffEffectExecuteRequest:                {name: "BuffEffectExecuteRequest", direction: "out"},
		cmdBuffStackCountResponse:                  {name: "BuffStackCountResponse", direction: "in"},
		cmdOrderApplyBuffNotify:                    {name: "OrderApplyBuffNotify", direction: "in"},
		cmdEntityBuffProducerOperateRequest:        {name: "EntityBuffProducerOperateRequest", direction: "out"},
		cmdRefreshBuffDurationPush:                 {name: "RefreshBuffDurationPush", direction: "in"},
		cmdEntityBuffProducerOperateResponse:       {name: "EntityBuffProducerOperateResponse", direction: "in"},
		cmdOrderApplyBuffResponse:                  {name: "OrderApplyBuffResponse", direction: "in"},
		cmdAtomicProcessRemoveBuffRequest:          {name: "AtomicProcessRemoveBuffRequest", direction: "out"},
		cmdTransformBuffStackNotify:                {name: "TransformBuffStackNotify", direction: "in"},
		cmdRemoveGameplayEffectResponse:            {name: "RemoveGameplayEffectResponse", direction: "in"},
		cmdActivateBuffNotify:                      {name: "ActivateBuffNotify", direction: "in"},
		cmdSimpleCombatEntitySyncAttributePush:     {name: "SimpleCombatEntitySyncAttributePush", direction: "in"},
		cmdOrderRemoveBuffRequest:                  {name: "OrderRemoveBuffRequest", direction: "out"},
		cmdBuffStackCountNotify:                    {name: "BuffStackCountNotify", direction: "in"},
		cmdApplyGameplayEffectPush:                 {name: "ApplyGameplayEffectPush", direction: "in"},
		cmdUpdateSysBuffNotify:                     {name: "UpdateSysBuffNotify", direction: "in"},
	}
	for commandID, want := range wantBuffCommands {
		found := false
		for _, entry := range report.Commands {
			if entry.CommandID == commandID {
				found = entry.Name == want.name && entry.Direction == want.direction && entry.Status == "NOT OBSERVED" && entry.SchemaBasis == censusSchemaBasis()
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
		cmdUpdatePlayerAllFightRoleNotify:    "UpdatePlayerAllFightRoleNotify",
		cmdUpdatePlayersFightRolesInfoNotify: "UpdatePlayersFightRolesInfoNotify",
		cmdUpdatePlayerSingleFightRoleNotify: "UpdatePlayerSingleFightRoleNotify",
		cmdChangePlayerFightRoleNotify:       "ChangePlayerFightRoleNotify",
	}
	for _, entry := range report.Commands {
		if entry.CommandID == cmdUpdateSceneGroupDataNotify && (entry.Name != "UpdateSceneGroupDataNotify" || entry.Direction != "in" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("scene-group mapping provenance was not retained: %+v", entry)
		}
		if entry.CommandID == cmdJoinSceneNotify && (entry.Name != "JoinSceneNotify" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("JoinSceneNotify provenance was not retained: %+v", entry)
		}
		if name, ok := wantRoleNotifies[entry.CommandID]; ok && (entry.Name != name || entry.Direction != "in" || entry.Status != "NOT OBSERVED" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("fight-role notification metadata was not retained: %+v", entry)
		}
	}
	var foundPhantom bool
	for _, entry := range report.Commands {
		if entry.CommandID == cmdPhantomAttrRecommendResponse {
			foundPhantom = entry.Name == "PhantomAttrRecommendResponse" && entry.Status == "PRESENT BUT UNDECODED" && entry.SchemaBasis == censusSchemaBasis()
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
