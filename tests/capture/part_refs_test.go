package capture

import (
	"bytes"
	"math"
	"testing"
)

func partInformationWire(partID int32, lifeMax float32, activated bool, partTag int32) []byte {
	row := appendVarintField(nil, fieldPartInformationPartID, uint64(uint32(partID)))
	row = appendFixed32TestField(row, fieldPartInformationLifeMax, math.Float32bits(lifeMax))
	if activated {
		row = appendVarintField(row, fieldPartInformationActivated, 1)
	} else {
		row = appendVarintField(row, fieldPartInformationActivated, 0)
	}
	row = appendVarintField(row, fieldPartInformationPartTag, uint64(uint32(partTag)))
	return row
}

func partComponentWire(rows ...[]byte) []byte {
	var component []byte
	for _, row := range rows {
		component = appendBytesField(component, fieldPartComponentPbPartLifeInfos, row)
	}
	return component
}

func entityAddWithPartComponent(entityID uint64, partComponent []byte) []byte {
	entityComponent := appendBytesField(nil, fieldEntityComponentPbPartComponent, partComponent)
	entity := appendVarintField(nil, fieldEntityPbID, entityID)
	entity = appendBytesField(entity, fieldEntityPbComponentPbs, entityComponent)
	notify := appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1)
	return appendBytesField(notify, fieldEntityAddNotifyEntityPbs, entity)
}

func damageRequestWithPart(rpcID, targetID uint64, partID int32, includePart bool) []byte {
	damage := appendVarintField(nil, fieldDamageExecuteRequestDamageID, 123456) // DamageId
	damage = appendVarintField(damage, fieldDamageExecuteRequestTargetEntityID, targetID)
	if includePart {
		damage = appendVarintField(damage, fieldDamageExecuteRequestPartID, uint64(uint32(partID)))
	}
	damage = appendVarintField(damage, fieldDamageExecuteRequestAttackerEntityID, 654321) // AttackerEntityId
	request := appendVarintField(nil, fieldCombatRequestDataRequestID, rpcID)
	request = appendBytesField(request, fieldCombatRequestDataDamageExecuteRequest, damage)
	return appendBytesField(nil, fieldCombatSendPackRequestData, appendBytesField(nil, fieldCombatSendDataRequest, request))
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
	partialEntity := appendVarintField(nil, fieldEntityPbID, 902)
	partial := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 0), fieldEntityAddNotifyEntityPbs, partialEntity)
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
	response := appendBytesField(nil, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbPartComponent, partComponentWire(partInformationWire(1, 200, true, 1))))
	if err := p.handleActiveResponse(gameFrame{token: 7, commandID: cmdEntityActiveResponse}, response); err != nil {
		t.Fatal(err)
	}
	if got := p.entities.resolvePartReference(902, 1); got.status != partReferenceUnknown {
		t.Fatalf("unmatched ActiveResponse part delta must invalidate stale entity-local state: %+v", got)
	}
}
