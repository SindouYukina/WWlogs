package devcapture

import (
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func TestClientVersionCensusSchemaBasisExcludesSiblingIdentifiers(t *testing.T) {
	for _, commandID := range []uint16{103, 116} {
		entry := newCommandCensus(nil).entry(commandID, "out")
		if want := "Only extracted version fields are retained; command 103/116 sibling fields contain device/account identifiers and are excluded from logs and audit"; entry.SchemaBasis != want {
			t.Fatalf("command %d schema basis = %q, want %q", commandID, entry.SchemaBasis, want)
		}
	}
}

func TestFrameErrorObservationsAreBounded(t *testing.T) {
	census := newCommandCensus(nil)
	when := time.Date(2026, time.October, 26, 5, 0, 0, 0, time.UTC)
	for i := 0; i < maxFrameErrorObservations+3; i++ {
		census.FailedCRC(capture.CRCFailureObservation{Direction: "in", CommandID: uint16(i), Timestamp: when})
		census.Malformed("candidate", when, i, 1, 2)
	}
	if len(census.crcFailures) != maxFrameErrorObservations || census.crcOmitted != 3 {
		t.Fatalf("CRC detail cap = %d, omitted %d", len(census.crcFailures), census.crcOmitted)
	}
	if len(census.malformedRecords) != maxFrameErrorObservations || census.malformedOmitted != 3 {
		t.Fatalf("malformed detail cap = %d, omitted %d", len(census.malformedRecords), census.malformedOmitted)
	}
}

func TestCommandCensusLinksCRCFailureToNeighborFrames(t *testing.T) {
	census := newCommandCensus(nil)
	previous := capture.FrameBoundaryObservation{Offset: 0, MessageType: 1, Sequence: 12, CommandID: 1234, FrameLength: 14}
	index := census.FailedCRC(capture.CRCFailureObservation{Direction: "in", Offset: 14, PreviousValid: &previous})
	next := capture.FrameBoundaryObservation{Offset: 40, MessageType: 1, Sequence: 14, CommandID: 5678, FrameLength: 15}
	census.SetNextValidFrame(index, next)

	observation := census.crcFailures[index]
	if observation.PreviousValid == nil || observation.PreviousValid.Sequence != 12 {
		t.Fatalf("previous valid frame context = %#v", observation.PreviousValid)
	}
	if observation.NextValid == nil || observation.NextValid.Sequence != 14 {
		t.Fatalf("next valid frame context = %#v", observation.NextValid)
	}
	census.SetNextValidFrame(index, capture.FrameBoundaryObservation{Sequence: 99})
	if observation.NextValid.Sequence != 14 {
		t.Fatalf("next valid frame context was overwritten: %#v", observation.NextValid)
	}
}

func TestExtractMovePackageObservationUsesSchemaFieldNumbers(t *testing.T) {
	message := appendVarintField(nil, capture.FieldMovePackagePushSceneOwnerID, 99)
	message = appendBytesField(message, capture.FieldMovePackagePushMovingEntities, appendVarintField(appendVarintField(nil, capture.FieldMovingEntityDataEntityID, 7), capture.FieldMovingEntityDataOriginator, 3))
	message = appendBytesField(message, capture.FieldMovePackagePushMovingEntities, appendVarintField(nil, capture.FieldMovingEntityDataEntityID, 42))
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
	message := appendVarintField(nil, capture.FieldPlayerFollowerNotifyPlayerID, 99)
	message = appendBytesField(message, capture.FieldPlayerFollowerNotifyListPlayerFollowerPb, appendVarintField(appendVarintField(nil, capture.FieldPlayerFollowerPbType, 2), capture.FieldPlayerFollowerPbEntityID, 7))
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
