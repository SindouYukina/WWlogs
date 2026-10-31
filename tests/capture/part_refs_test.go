package capture

import (
	"bytes"
	"math"
	"testing"
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
