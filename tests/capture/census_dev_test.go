//go:build wwdev

package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandCensusSeparatesDecodedFramesFromUnimplementedSchemas(t *testing.T) {
	census := newCommandCensus(nil)
	when := time.Date(2026, time.October, 26, 5, 0, 0, 0, time.UTC)
	// The processor names an entry from the current protocol only for a frame its protocol session decodes.
	entityAdd := census.ObserveFrame(GameFrame{CommandID: CmdEntityAddNotify, Direction: "in", Timestamp: when}, 1).(*commandCensusEntry)
	entityAdd.DescribeCurrentProtocol()
	entityAdd.DecryptedFrames++
	entityAdd.DecodedFrames++
	joinScene := census.ObserveFrame(GameFrame{CommandID: CmdJoinSceneNotify, Direction: "in", Timestamp: when.Add(time.Second)}, 1).(*commandCensusEntry)
	joinScene.DescribeCurrentProtocol()
	joinScene.DecryptedFrames++
	joinScene.GenericProtoValid++
	joinScene.DecodedFrames++
	phantomRecommend := census.ObserveFrame(GameFrame{CommandID: CmdPhantomAttrRecommendResponse, Direction: "in", Timestamp: when.Add(time.Second)}, 1).(*commandCensusEntry)
	phantomRecommend.DescribeCurrentProtocol()
	phantomRecommend.DecryptedFrames++
	phantomRecommend.GenericProtoValid++
	phantomRecommend.FailureReasons["message schema not implemented"]++
	census.FailedCRC(CRCFailureObservation{Direction: "in", CommandID: CmdEntityActiveResponse, Timestamp: when.Add(2 * time.Second), ReceivedCRC: 1, ComputedCRC: 2})
	census.Malformed("short_or_unaligned_frame_candidate", when.Add(3*time.Second), 4, 8, 32)
	census.sessions = 1

	path := filepath.Join(t.TempDir(), "census.json")
	if err := census.write(path, CaptureStats{UDPPackets: 3, Frames: 2, BadCRC: 1}, nil, protocolReport{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		SessionCount int                     `json:"sessionCount"`
		Commands     []commandCensusEntry    `json:"commands"`
		CRCFailures  []CRCFailureObservation `json:"crcFailureObservations"`
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
		if (entry.CommandID == CmdLoginRequest || entry.CommandID == CmdCheckClientPush) && (entry.Direction != "out" || entry.SchemaBasis != "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit") {
			t.Fatalf("version command metadata was not retained: %+v", entry)
		}
		statuses[entry.CommandID] = entry.Status
	}
	if statuses[CmdEntityAddNotify] != "PRESENT AND DECODED" {
		t.Fatalf("EntityAddNotify census status = %q", statuses[CmdEntityAddNotify])
	}
	if statuses[CmdJoinSceneNotify] != "PRESENT AND DECODED" || statuses[CmdEntityActiveResponse] != "UNKNOWN DUE TO CRC/CAPTURE LIMITATION" {
		t.Fatalf("JoinScene/CRC-header statuses = %q/%q", statuses[CmdJoinSceneNotify], statuses[CmdEntityActiveResponse])
	}
	if statuses[CmdUpdateSceneGroupDataNotify] != "NOT OBSERVED" {
		t.Fatalf("UpdateSceneGroupDataNotify census status = %q", statuses[CmdUpdateSceneGroupDataNotify])
	}
	if statuses[CmdApplyGameplayEffectNotify] != "NOT OBSERVED" {
		t.Fatalf("ApplyGameplayEffectNotify census status = %q", statuses[CmdApplyGameplayEffectNotify])
	}
	wantBuffCommands := map[uint16]struct{ name, direction string }{
		CmdOrderRemoveBuffByTagsResponse:           {name: "OrderRemoveBuffByTagsResponse", direction: "in"},
		CmdAtomicProcessAddBuffRequest:             {name: "AtomicProcessAddBuffRequest", direction: "out"},
		CmdSimpleCombatEntityAttributeUpdateNotify: {name: "SimpleCombatEntityAttributeUpdateNotify", direction: "in"},
		CmdApplyBuffS2cResponsePush:                {name: "ApplyBuffS2cResponsePush", direction: "in"},
		CmdBuffEffectResponse:                      {name: "BuffEffectResponse", direction: "in"},
		CmdBuffDurationNotify:                      {name: "BuffDurationNotify", direction: "in"},
		CmdActivateBuffRequest:                     {name: "ActivateBuffRequest", direction: "out"},
		CmdOrderRemoveBuffResponse:                 {name: "OrderRemoveBuffResponse", direction: "in"},
		CmdBuffEffectExecuteResponse:               {name: "BuffEffectExecuteResponse", direction: "in"},
		CmdOrderRemoveBuffNotify:                   {name: "OrderRemoveBuffNotify", direction: "in"},
		CmdSimpleCombatEntityBuffUpdateNotify:      {name: "SimpleCombatEntityBuffUpdateNotify", direction: "in"},
		CmdBuffEffectExecutePush:                   {name: "BuffEffectExecutePush", direction: "in"},
		CmdRemoveGameplayEffectNotify:              {name: "RemoveGameplayEffectNotify", direction: "in"},
		CmdApplyBuffS2cRequestNotify:               {name: "ApplyBuffS2cRequestNotify", direction: "in"},
		CmdEntityBuffProducerRequest:               {name: "EntityBuffProducerRequest", direction: "out"},
		CmdRemoveBuffByIDS2cResponsePush:           {name: "RemoveBuffByIdS2cResponsePush", direction: "in"},
		CmdRemoveBuffByIDS2cRequestNotify:          {name: "RemoveBuffByIdS2cRequestNotify", direction: "in"},
		CmdAtomicProcessAddBuffResponse:            {name: "AtomicProcessAddBuffResponse", direction: "in"},
		CmdBuffStackCountRequest:                   {name: "BuffStackCountRequest", direction: "out"},
		CmdRemoveGameplayEffectRequest:             {name: "RemoveGameplayEffectRequest", direction: "out"},
		CmdActivateBuffResponse:                    {name: "ActivateBuffResponse", direction: "in"},
		CmdRemoveBuffS2cRequestNotify:              {name: "RemoveBuffS2cRequestNotify", direction: "in"},
		CmdRemoveGameplayEffectPush:                {name: "RemoveGameplayEffectPush", direction: "in"},
		CmdBuffEffectPush:                          {name: "BuffEffectPush", direction: "in"},
		CmdActivateBuffPush:                        {name: "ActivateBuffPush", direction: "in"},
		CmdBuffStackCountPush:                      {name: "BuffStackCountPush", direction: "in"},
		CmdOrderApplyBuffRequest:                   {name: "OrderApplyBuffRequest", direction: "out"},
		CmdAddSysBuffNotify:                        {name: "AddSysBuffNotify", direction: "in"},
		CmdEntityBuffProducerResponse:              {name: "EntityBuffProducerResponse", direction: "in"},
		CmdSimpleCombatEntityBuffLayerCountNotify:  {name: "SimpleCombatEntityBuffLayerCountNotify", direction: "in"},
		CmdBuffEffectRequest:                       {name: "BuffEffectRequest", direction: "out"},
		CmdOrderRemoveBuffByTagsRequest:            {name: "OrderRemoveBuffByTagsRequest", direction: "out"},
		CmdApplyGameplayEffectResponse:             {name: "ApplyGameplayEffectResponse", direction: "in"},
		CmdApplyGameplayEffectRequest:              {name: "ApplyGameplayEffectRequest", direction: "out"},
		CmdAtomicProcessRemoveBuffResponse:         {name: "AtomicProcessRemoveBuffResponse", direction: "in"},
		CmdRemoveBuffByServerIDS2cRequestNotify:    {name: "RemoveBuffByServerIdS2cRequestNotify", direction: "in"},
		CmdRemoveBuffS2cResponsePush:               {name: "RemoveBuffS2cResponsePush", direction: "in"},
		CmdOrderRemoveBuffByTagsNotify:             {name: "OrderRemoveBuffByTagsNotify", direction: "in"},
		CmdRemoveSysBuffNotify:                     {name: "RemoveSysBuffNotify", direction: "in"},
		CmdApplyGameplayEffectNotify:               {name: "ApplyGameplayEffectNotify", direction: "in"},
		CmdBuffEffectExecuteRequest:                {name: "BuffEffectExecuteRequest", direction: "out"},
		CmdBuffStackCountResponse:                  {name: "BuffStackCountResponse", direction: "in"},
		CmdOrderApplyBuffNotify:                    {name: "OrderApplyBuffNotify", direction: "in"},
		CmdEntityBuffProducerOperateRequest:        {name: "EntityBuffProducerOperateRequest", direction: "out"},
		CmdRefreshBuffDurationPush:                 {name: "RefreshBuffDurationPush", direction: "in"},
		CmdEntityBuffProducerOperateResponse:       {name: "EntityBuffProducerOperateResponse", direction: "in"},
		CmdOrderApplyBuffResponse:                  {name: "OrderApplyBuffResponse", direction: "in"},
		CmdAtomicProcessRemoveBuffRequest:          {name: "AtomicProcessRemoveBuffRequest", direction: "out"},
		CmdTransformBuffStackNotify:                {name: "TransformBuffStackNotify", direction: "in"},
		CmdRemoveGameplayEffectResponse:            {name: "RemoveGameplayEffectResponse", direction: "in"},
		CmdActivateBuffNotify:                      {name: "ActivateBuffNotify", direction: "in"},
		CmdSimpleCombatEntitySyncAttributePush:     {name: "SimpleCombatEntitySyncAttributePush", direction: "in"},
		CmdOrderRemoveBuffRequest:                  {name: "OrderRemoveBuffRequest", direction: "out"},
		CmdBuffStackCountNotify:                    {name: "BuffStackCountNotify", direction: "in"},
		CmdApplyGameplayEffectPush:                 {name: "ApplyGameplayEffectPush", direction: "in"},
		CmdUpdateSysBuffNotify:                     {name: "UpdateSysBuffNotify", direction: "in"},
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
		CmdUpdatePlayerAllFightRoleNotify:    "UpdatePlayerAllFightRoleNotify",
		CmdUpdatePlayersFightRolesInfoNotify: "UpdatePlayersFightRolesInfoNotify",
		CmdUpdatePlayerSingleFightRoleNotify: "UpdatePlayerSingleFightRoleNotify",
		CmdChangePlayerFightRoleNotify:       "ChangePlayerFightRoleNotify",
	}
	for _, entry := range report.Commands {
		if entry.CommandID == CmdUpdateSceneGroupDataNotify && (entry.Name != "UpdateSceneGroupDataNotify" || entry.Direction != "in" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("scene-group mapping provenance was not retained: %+v", entry)
		}
		if entry.CommandID == CmdJoinSceneNotify && (entry.Name != "JoinSceneNotify" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("JoinSceneNotify provenance was not retained: %+v", entry)
		}
		if name, ok := wantRoleNotifies[entry.CommandID]; ok && (entry.Name != name || entry.Direction != "in" || entry.Status != "NOT OBSERVED" || entry.SchemaBasis != censusSchemaBasis()) {
			t.Fatalf("fight-role notification metadata was not retained: %+v", entry)
		}
	}
	var foundPhantom bool
	for _, entry := range report.Commands {
		if entry.CommandID == CmdPhantomAttrRecommendResponse {
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
	if entry := census.entries[censusKey{CommandID: CmdJoinSceneNotify, Direction: "in"}]; entry == nil || entry.Name != "JoinSceneNotify" || entry.SchemaBasis != censusSchemaBasis() {
		t.Fatalf("a current-protocol frame was not named from the current protocol: %+v", entry)
	}
	if text := censusSchemaBasis(); !strings.Contains(text, CurrentProtocol()) {
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
		if got := census.entry(CmdCombatSendPackRequest, "out").FailureReasons[protocolUnsupportedFailureReason]; got != 1 {
			t.Fatalf("%s: census reason = %d, want 1", build, got)
		}
		for _, key := range []censusKey{{CommandID: CmdJoinSceneNotify, Direction: "in"}, {CommandID: CmdCombatSendPackRequest, Direction: "out"}, {CommandID: CmdCombatSendPackResponse, Direction: "in"}} {
			if entry := census.entries[key]; entry == nil || entry.Name != "" || entry.SchemaBasis != "" {
				t.Fatalf("%s: a frame of another game version took a current-protocol name: %+v", build, entry)
			}
		}
	}
}
