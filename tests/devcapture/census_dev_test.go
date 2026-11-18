package devcapture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestCommandCensusSeparatesDecodedFramesFromUnimplementedSchemas(t *testing.T) {
	census := newCommandCensus(nil)
	when := time.Date(2026, time.October, 26, 5, 0, 0, 0, time.UTC)
	// The processor names an entry from the current protocol only for a frame its protocol session decodes.
	entityAdd := census.ObserveFrame(capture.GameFrame{CommandID: capture.CmdEntityAddNotify, Direction: "in", Timestamp: when}, 1).(*commandCensusEntry)
	entityAdd.DescribeCurrentProtocol()
	entityAdd.DecryptedFrames++
	entityAdd.DecodedFrames++
	joinScene := census.ObserveFrame(capture.GameFrame{CommandID: capture.CmdJoinSceneNotify, Direction: "in", Timestamp: when.Add(time.Second)}, 1).(*commandCensusEntry)
	joinScene.DescribeCurrentProtocol()
	joinScene.DecryptedFrames++
	joinScene.GenericProtoValid++
	joinScene.DecodedFrames++
	phantomRecommend := census.ObserveFrame(capture.GameFrame{CommandID: capture.CmdPhantomAttrRecommendResponse, Direction: "in", Timestamp: when.Add(time.Second)}, 1).(*commandCensusEntry)
	phantomRecommend.DescribeCurrentProtocol()
	phantomRecommend.DecryptedFrames++
	phantomRecommend.GenericProtoValid++
	phantomRecommend.FailureReasons["message schema not implemented"]++
	census.FailedCRC(capture.CRCFailureObservation{Direction: "in", CommandID: capture.CmdEntityActiveResponse, Timestamp: when.Add(2 * time.Second), ReceivedCRC: 1, ComputedCRC: 2})
	census.Malformed("short_or_unaligned_frame_candidate", when.Add(3*time.Second), 4, 8, 32)
	census.sessions = 1

	path := filepath.Join(t.TempDir(), "census.json")
	if err := census.write(path, capture.CaptureStats{UDPPackets: 3, Frames: 2, BadCRC: 1}, nil, protocolReport{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		SessionCount int                             `json:"sessionCount"`
		Commands     []commandCensusEntry            `json:"commands"`
		CRCFailures  []capture.CRCFailureObservation `json:"crcFailureObservations"`
		Malformed    []malformedObservation          `json:"malformedObservations"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.SessionCount != 1 || len(report.Commands) != 72 {
		t.Fatalf("session/command counts = %d/%d", report.SessionCount, len(report.Commands))
	}
	statuses := make(map[uint16]string, len(report.Commands))
	for _, entry := range report.Commands {
		if (entry.CommandID == capture.CmdLoginRequest || entry.CommandID == capture.CmdCheckClientPush) && (entry.Direction != "out" || entry.SchemaBasis != "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit") {
			t.Fatalf("version command metadata was not retained: %+v", entry)
		}
		statuses[entry.CommandID] = entry.Status
	}
	if statuses[capture.CmdEntityAddNotify] != "PRESENT AND DECODED" {
		t.Fatalf("EntityAddNotify census status = %q", statuses[capture.CmdEntityAddNotify])
	}
	if statuses[capture.CmdJoinSceneNotify] != "PRESENT AND DECODED" || statuses[capture.CmdEntityActiveResponse] != "UNKNOWN DUE TO CRC/CAPTURE LIMITATION" {
		t.Fatalf("JoinScene/CRC-header statuses = %q/%q", statuses[capture.CmdJoinSceneNotify], statuses[capture.CmdEntityActiveResponse])
	}
	if statuses[capture.CmdUpdateSceneGroupDataNotify] != "NOT OBSERVED" {
		t.Fatalf("UpdateSceneGroupDataNotify census status = %q", statuses[capture.CmdUpdateSceneGroupDataNotify])
	}
	if statuses[capture.CmdApplyGameplayEffectNotify] != "NOT OBSERVED" {
		t.Fatalf("ApplyGameplayEffectNotify census status = %q", statuses[capture.CmdApplyGameplayEffectNotify])
	}
	wantBuffCommands := map[uint16]struct{ name, direction string }{
		capture.CmdOrderRemoveBuffByTagsResponse:           {name: "OrderRemoveBuffByTagsResponse", direction: "in"},
		capture.CmdAtomicProcessAddBuffRequest:             {name: "AtomicProcessAddBuffRequest", direction: "out"},
		capture.CmdSimpleCombatEntityAttributeUpdateNotify: {name: "SimpleCombatEntityAttributeUpdateNotify", direction: "in"},
		capture.CmdApplyBuffS2cResponsePush:                {name: "ApplyBuffS2cResponsePush", direction: "in"},
		capture.CmdBuffEffectResponse:                      {name: "BuffEffectResponse", direction: "in"},
		capture.CmdBuffDurationNotify:                      {name: "BuffDurationNotify", direction: "in"},
		capture.CmdActivateBuffRequest:                     {name: "ActivateBuffRequest", direction: "out"},
		capture.CmdOrderRemoveBuffResponse:                 {name: "OrderRemoveBuffResponse", direction: "in"},
		capture.CmdBuffEffectExecuteResponse:               {name: "BuffEffectExecuteResponse", direction: "in"},
		capture.CmdOrderRemoveBuffNotify:                   {name: "OrderRemoveBuffNotify", direction: "in"},
		capture.CmdSimpleCombatEntityBuffUpdateNotify:      {name: "SimpleCombatEntityBuffUpdateNotify", direction: "in"},
		capture.CmdBuffEffectExecutePush:                   {name: "BuffEffectExecutePush", direction: "in"},
		capture.CmdRemoveGameplayEffectNotify:              {name: "RemoveGameplayEffectNotify", direction: "in"},
		capture.CmdApplyBuffS2cRequestNotify:               {name: "ApplyBuffS2cRequestNotify", direction: "in"},
		capture.CmdEntityBuffProducerRequest:               {name: "EntityBuffProducerRequest", direction: "out"},
		capture.CmdRemoveBuffByIDS2cResponsePush:           {name: "RemoveBuffByIdS2cResponsePush", direction: "in"},
		capture.CmdRemoveBuffByIDS2cRequestNotify:          {name: "RemoveBuffByIdS2cRequestNotify", direction: "in"},
		capture.CmdAtomicProcessAddBuffResponse:            {name: "AtomicProcessAddBuffResponse", direction: "in"},
		capture.CmdBuffStackCountRequest:                   {name: "BuffStackCountRequest", direction: "out"},
		capture.CmdRemoveGameplayEffectRequest:             {name: "RemoveGameplayEffectRequest", direction: "out"},
		capture.CmdActivateBuffResponse:                    {name: "ActivateBuffResponse", direction: "in"},
		capture.CmdRemoveBuffS2cRequestNotify:              {name: "RemoveBuffS2cRequestNotify", direction: "in"},
		capture.CmdRemoveGameplayEffectPush:                {name: "RemoveGameplayEffectPush", direction: "in"},
		capture.CmdBuffEffectPush:                          {name: "BuffEffectPush", direction: "in"},
		capture.CmdActivateBuffPush:                        {name: "ActivateBuffPush", direction: "in"},
		capture.CmdBuffStackCountPush:                      {name: "BuffStackCountPush", direction: "in"},
		capture.CmdOrderApplyBuffRequest:                   {name: "OrderApplyBuffRequest", direction: "out"},
		capture.CmdAddSysBuffNotify:                        {name: "AddSysBuffNotify", direction: "in"},
		capture.CmdEntityBuffProducerResponse:              {name: "EntityBuffProducerResponse", direction: "in"},
		capture.CmdSimpleCombatEntityBuffLayerCountNotify:  {name: "SimpleCombatEntityBuffLayerCountNotify", direction: "in"},
		capture.CmdBuffEffectRequest:                       {name: "BuffEffectRequest", direction: "out"},
		capture.CmdOrderRemoveBuffByTagsRequest:            {name: "OrderRemoveBuffByTagsRequest", direction: "out"},
		capture.CmdApplyGameplayEffectResponse:             {name: "ApplyGameplayEffectResponse", direction: "in"},
		capture.CmdApplyGameplayEffectRequest:              {name: "ApplyGameplayEffectRequest", direction: "out"},
		capture.CmdAtomicProcessRemoveBuffResponse:         {name: "AtomicProcessRemoveBuffResponse", direction: "in"},
		capture.CmdRemoveBuffByServerIDS2cRequestNotify:    {name: "RemoveBuffByServerIdS2cRequestNotify", direction: "in"},
		capture.CmdRemoveBuffS2cResponsePush:               {name: "RemoveBuffS2cResponsePush", direction: "in"},
		capture.CmdOrderRemoveBuffByTagsNotify:             {name: "OrderRemoveBuffByTagsNotify", direction: "in"},
		capture.CmdRemoveSysBuffNotify:                     {name: "RemoveSysBuffNotify", direction: "in"},
		capture.CmdApplyGameplayEffectNotify:               {name: "ApplyGameplayEffectNotify", direction: "in"},
		capture.CmdBuffEffectExecuteRequest:                {name: "BuffEffectExecuteRequest", direction: "out"},
		capture.CmdBuffStackCountResponse:                  {name: "BuffStackCountResponse", direction: "in"},
		capture.CmdOrderApplyBuffNotify:                    {name: "OrderApplyBuffNotify", direction: "in"},
		capture.CmdEntityBuffProducerOperateRequest:        {name: "EntityBuffProducerOperateRequest", direction: "out"},
		capture.CmdRefreshBuffDurationPush:                 {name: "RefreshBuffDurationPush", direction: "in"},
		capture.CmdEntityBuffProducerOperateResponse:       {name: "EntityBuffProducerOperateResponse", direction: "in"},
		capture.CmdOrderApplyBuffResponse:                  {name: "OrderApplyBuffResponse", direction: "in"},
		capture.CmdAtomicProcessRemoveBuffRequest:          {name: "AtomicProcessRemoveBuffRequest", direction: "out"},
		capture.CmdTransformBuffStackNotify:                {name: "TransformBuffStackNotify", direction: "in"},
		capture.CmdRemoveGameplayEffectResponse:            {name: "RemoveGameplayEffectResponse", direction: "in"},
		capture.CmdActivateBuffNotify:                      {name: "ActivateBuffNotify", direction: "in"},
		capture.CmdSimpleCombatEntitySyncAttributePush:     {name: "SimpleCombatEntitySyncAttributePush", direction: "in"},
		capture.CmdOrderRemoveBuffRequest:                  {name: "OrderRemoveBuffRequest", direction: "out"},
		capture.CmdBuffStackCountNotify:                    {name: "BuffStackCountNotify", direction: "in"},
		capture.CmdApplyGameplayEffectPush:                 {name: "ApplyGameplayEffectPush", direction: "in"},
		capture.CmdUpdateSysBuffNotify:                     {name: "UpdateSysBuffNotify", direction: "in"},
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
		capture.CmdUpdatePlayerAllFightRoleNotify:    "UpdatePlayerAllFightRoleNotify",
		capture.CmdUpdatePlayersFightRolesInfoNotify: "UpdatePlayersFightRolesInfoNotify",
		capture.CmdUpdatePlayerSingleFightRoleNotify: "UpdatePlayerSingleFightRoleNotify",
		capture.CmdChangePlayerFightRoleNotify:       "ChangePlayerFightRoleNotify",
	}
	for _, entry := range report.Commands {
		if entry.CommandID == capture.CmdUpdateSceneGroupDataNotify && (entry.Name != "UpdateSceneGroupDataNotify" || entry.Direction != "in" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("scene-group mapping provenance was not retained: %+v", entry)
		}
		if entry.CommandID == capture.CmdJoinSceneNotify && (entry.Name != "JoinSceneNotify" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("JoinSceneNotify provenance was not retained: %+v", entry)
		}
		if name, ok := wantRoleNotifies[entry.CommandID]; ok && (entry.Name != name || entry.Direction != "in" || entry.Status != "NOT OBSERVED" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("fight-role notification metadata was not retained: %+v", entry)
		}
	}
	var foundPhantom bool
	for _, entry := range report.Commands {
		if entry.CommandID == capture.CmdPhantomAttrRecommendResponse {
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

// A fight sent in the current protocol names its census entries from the current protocol.
func TestCensusNamesCurrentProtocolFrames(t *testing.T) {
	p, _ := initialProcessor(t)
	census := newCommandCensus(p)
	p.Census = census
	sendCurrentProtocolFight(t, p)
	if entry := census.entries[censusKey{CommandID: capture.CmdJoinSceneNotify, Direction: "in"}]; entry == nil || entry.Name != "JoinSceneNotify" || entry.SchemaBasis != censusSchemaBasis() {
		t.Fatalf("a current-protocol frame was not named from the current protocol: %+v", entry)
	}
	if text := censusSchemaBasis(); !strings.Contains(text, capture.CurrentProtocol()) {
		t.Fatalf("census schema basis %q does not name the current client protocol", text)
	}
}

// The same number means another message in another version: the census must not name it from the current one.
func TestCensusKeepsOtherGameVersionFramesUnnamed(t *testing.T) {
	for _, build := range otherVersionBuilds {
		p, _ := initialProcessorWithBuild(t, build)
		census := newCommandCensus(p)
		p.Census = census
		sendOtherVersionFight(t, p)
		if got := census.entry(capture.CmdCombatSendPackRequest, "out").FailureReasons[protocolUnsupportedFailureReason]; got != 1 {
			t.Fatalf("%s: census reason = %d, want 1", build, got)
		}
		for _, key := range []censusKey{{CommandID: capture.CmdJoinSceneNotify, Direction: "in"}, {CommandID: capture.CmdCombatSendPackRequest, Direction: "out"}, {CommandID: capture.CmdCombatSendPackResponse, Direction: "in"}} {
			if entry := census.entries[key]; entry == nil || entry.Name != "" || entry.SchemaBasis != "" {
				t.Fatalf("%s: a frame of another game version took a current-protocol name: %+v", build, entry)
			}
		}
	}
}
