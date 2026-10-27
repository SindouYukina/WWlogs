//go:build wwdev

package main

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
	if report.SessionCount != 1 || len(report.Commands) != 23 {
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
	wantRoleNotifies := map[uint16]string{
		21051: "UpdatePlayerAllFightRoleNotify",
		18899: "UpdatePlayersFightRolesInfoNotify",
		18580: "UpdatePlayerSingleFightRoleNotify",
		27633: "ChangePlayerFightRoleNotify",
	}
	for _, entry := range report.Commands {
		if entry.CommandID == 17286 && (entry.Name != "UpdateSceneGroupDataNotify" || entry.Direction != "in" || entry.SchemaBasis != "Windows Global UHD 3.7.1 Aki.Protocol metadata; client version observed, schema compatibility unverified") {
			t.Fatalf("scene-group mapping provenance was not retained: %+v", entry)
		}
		if entry.CommandID == 18559 && (entry.Name != "JoinSceneNotify" || entry.SchemaBasis != "Windows Global UHD 3.7.1 Aki.Protocol metadata; client version observed, schema compatibility unverified") {
			t.Fatalf("JoinSceneNotify provenance was not retained: %+v", entry)
		}
		if name, ok := wantRoleNotifies[entry.CommandID]; ok && (entry.Name != name || entry.Direction != "in" || entry.Status != "NOT OBSERVED" || entry.SchemaBasis != "Windows Global UHD 3.7.1 Aki.Protocol metadata; client version observed, schema compatibility unverified") {
			t.Fatalf("fight-role notification metadata was not retained: %+v", entry)
		}
	}
	var foundPhantom bool
	for _, entry := range report.Commands {
		if entry.CommandID == 22535 {
			foundPhantom = entry.Name == "PhantomAttrRecommendResponse" && entry.Status == "PRESENT BUT UNDECODED" && entry.SchemaBasis == "Windows Global UHD 3.7.1 Aki.Protocol metadata; client version observed, schema compatibility unverified"
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
