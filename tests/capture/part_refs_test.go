package capture

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func partInformationWire(partID int32, lifeMax float32, activated bool, partTag int32) []byte {
	row := appendVarintField(nil, 1, uint64(uint32(partID)))
	row = appendFixed32TestField(row, 3, math.Float32bits(lifeMax))
	if activated {
		row = appendVarintField(row, 4, 1)
	} else {
		row = appendVarintField(row, 4, 0)
	}
	row = appendVarintField(row, 5, uint64(uint32(partTag)))
	return row
}

func partComponentWire(rows ...[]byte) []byte {
	var component []byte
	for _, row := range rows {
		component = appendBytesField(component, 1, row)
	}
	return component
}

func entityAddWithPartComponent(entityID uint64, partComponent []byte) []byte {
	entityComponent := appendBytesField(nil, 5, partComponent)
	entity := appendVarintField(nil, 1, entityID)
	entity = appendBytesField(entity, 11, entityComponent)
	notify := appendVarintField(nil, 1, 1)
	return appendBytesField(notify, 6, entity)
}

func damageRequestWithPart(rpcID, targetID uint64, partID int32, includePart bool) []byte {
	damage := appendVarintField(nil, 9, 123456) // DamageId
	damage = appendVarintField(damage, 10, targetID)
	if includePart {
		damage = appendVarintField(damage, 5, uint64(uint32(partID)))
	}
	damage = appendVarintField(damage, 13, 654321) // AttackerEntityId
	request := appendVarintField(nil, 2, rpcID)
	request = appendBytesField(request, 5, damage)
	return appendBytesField(nil, 14, appendBytesField(nil, 3, request))
}

func TestPartReferenceOutputLinksRequestToCurrentTargetPart(t *testing.T) {
	var output, diagnostic bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &diagnostic, nil)
	p.sessionNumber = 3
	p.frameOrdinal = 45
	p.partRefOut = &output
	if _, err := p.entities.applyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(2, 28046.3, true, 17)))); err != nil {
		t.Fatal(err)
	}
	frame := gameFrame{direction: "out", commandID: 19470, timestamp: time.Date(2026, 10, 30, 3, 4, 5, 0, time.UTC)}
	p.handleDamageRequests(frame, damageRequestWithPart(91, 902, 2, true))

	var row partReferenceRecord
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.SchemaVersion != partReferenceSchemaVersion || row.EventType != "DAMAGE_REQUEST_PART_REFERENCE" || row.FrameOrdinal != 44 || row.SessionNumber != 3 || row.Direction != "out" || row.CommandID != 19470 || row.RPCID != 91 || row.FieldNumber != 5 {
		t.Fatalf("request envelope = %+v", row)
	}
	if row.Status != partReferenceCorrelated || row.PartID == nil || *row.PartID != 2 || !row.HasPartID || row.TargetEntityID == nil || *row.TargetEntityID != 902 {
		t.Fatalf("reference = %+v, want exact target-local PartId link", row)
	}
	if row.PartInformation == nil || row.PartInformation.PartID != 2 || row.PartInformation.LifeMax == nil || math.Abs(float64(*row.PartInformation.LifeMax-28046.3)) > 0.01 || row.PartInformation.Activated == nil || !*row.PartInformation.Activated || row.PartInformation.PartTag == nil || *row.PartInformation.PartTag != 17 {
		t.Fatalf("part attributes = %+v", row.PartInformation)
	}
	if row.PartInformationSource != "EntityAddNotify.EntityPbs[].ComponentPbs.PartComponent.PartLifeInfos[]" {
		t.Fatalf("part source = %q", row.PartInformationSource)
	}
	if _, ok := p.pending[91]; !ok {
		t.Fatal("request sidecar must not alter the existing RPC pending state")
	}
}

func TestPartReferencePreservesUnknownAndNoPartSentinel(t *testing.T) {
	resolver := newEntityResolver()
	missing := resolver.resolvePartReference(902, 2)
	if missing.status != partReferenceUnknown || missing.information != nil {
		t.Fatalf("missing target component resolution = %+v", missing)
	}
	noPart := resolver.resolvePartReference(902, -1)
	if noPart.status != partReferenceNoPart || noPart.information != nil {
		t.Fatalf("no-part sentinel resolution = %+v", noPart)
	}
	otherNonPositive := resolver.resolvePartReference(902, 0)
	if otherNonPositive.status != partReferenceUnknown || otherNonPositive.information != nil {
		t.Fatalf("unclassified non-positive PartId = %+v", otherNonPositive)
	}
}

func TestPartComponentUpdatesReplaceAndFollowEntityLifecycle(t *testing.T) {
	resolver := newEntityResolver()
	if _, err := resolver.applyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(1, 100, true, 1)))); err != nil {
		t.Fatal(err)
	}
	if got := resolver.resolvePartReference(902, 1); got.status != partReferenceCorrelated {
		t.Fatalf("initial part row = %+v", got)
	}
	partialEntity := appendVarintField(nil, 1, 902)
	partial := appendBytesField(appendVarintField(nil, 1, 0), 6, partialEntity)
	if _, err := resolver.applyEntityAddNotify(partial); err != nil {
		t.Fatal(err)
	}
	if got := resolver.resolvePartReference(902, 1); got.status != partReferenceCorrelated {
		t.Fatalf("partial entity update should preserve observed component: %+v", got)
	}
	if _, err := resolver.applyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(2, 200, false, 2)))); err != nil {
		t.Fatal(err)
	}
	if got := resolver.resolvePartReference(902, 1); got.status != partReferenceUnknown {
		t.Fatalf("new component must replace old part rows: %+v", got)
	}
	if got := resolver.resolvePartReference(902, 2); got.status != partReferenceCorrelated {
		t.Fatalf("replacement part row = %+v", got)
	}
	if _, _, err := resolver.applyEntityRemoveNotify(lifecycleRemoveNotify(902)); err != nil {
		t.Fatal(err)
	}
	if got := resolver.resolvePartReference(902, 2); got.status != partReferenceUnknown {
		t.Fatalf("removed entity part row must not resolve: %+v", got)
	}
}

func TestUnmatchedActivePartDeltaInvalidatesOldPartSnapshots(t *testing.T) {
	var diagnostic bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &diagnostic, nil)
	if _, err := p.entities.applyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(1, 100, true, 1)))); err != nil {
		t.Fatal(err)
	}
	response := appendBytesField(nil, 11, appendBytesField(nil, 5, partComponentWire(partInformationWire(1, 200, true, 1))))
	if err := p.handleActiveResponse(gameFrame{token: 7, commandID: 26702}, response); err != nil {
		t.Fatal(err)
	}
	if got := p.entities.resolvePartReference(902, 1); got.status != partReferenceUnknown {
		t.Fatalf("unmatched ActiveResponse part delta must invalidate stale entity-local state: %+v", got)
	}
}
