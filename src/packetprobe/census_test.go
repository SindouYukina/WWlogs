package main

import (
	"testing"
	"time"
)

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
