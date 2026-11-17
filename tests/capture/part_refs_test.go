//go:build wwdev

package capture

import (
	"bytes"
	"math"
	"testing"
)

func partInformationWire(partID int32, lifeMax float32, activated bool, partTag int32) []byte {
	row := appendVarintField(nil, FieldPartInformationPartID, uint64(uint32(partID)))
	row = appendFixed32TestField(row, FieldPartInformationLifeMax, math.Float32bits(lifeMax))
	if activated {
		row = appendVarintField(row, FieldPartInformationActivated, 1)
	} else {
		row = appendVarintField(row, FieldPartInformationActivated, 0)
	}
	row = appendVarintField(row, FieldPartInformationPartTag, uint64(uint32(partTag)))
	return row
}

func partComponentWire(rows ...[]byte) []byte {
	var component []byte
	for _, row := range rows {
		component = appendBytesField(component, FieldPartComponentPbPartLifeInfos, row)
	}
	return component
}

func entityAddWithPartComponent(entityID uint64, partComponent []byte) []byte {
	entityComponent := appendBytesField(nil, FieldEntityComponentPbPartComponent, partComponent)
	entity := appendVarintField(nil, FieldEntityPbID, entityID)
	entity = appendBytesField(entity, FieldEntityPbComponentPbs, entityComponent)
	notify := appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1)
	return appendBytesField(notify, FieldEntityAddNotifyEntityPbs, entity)
}

func damageRequestWithPart(rpcID, targetID uint64, partID int32, includePart bool) []byte {
	damage := appendVarintField(nil, FieldDamageExecuteRequestDamageID, 123456) // DamageId
	damage = appendVarintField(damage, FieldDamageExecuteRequestTargetEntityID, targetID)
	if includePart {
		damage = appendVarintField(damage, FieldDamageExecuteRequestPartID, uint64(uint32(partID)))
	}
	damage = appendVarintField(damage, FieldDamageExecuteRequestAttackerEntityID, 654321) // AttackerEntityId
	request := appendVarintField(nil, FieldCombatRequestDataRequestID, rpcID)
	request = appendBytesField(request, FieldCombatRequestDataDamageExecuteRequest, damage)
	return appendBytesField(nil, FieldCombatSendPackRequestData, appendBytesField(nil, FieldCombatSendDataRequest, request))
}

func TestPartReferencePreservesUnknownAndNoPartSentinel(t *testing.T) {
	resolver := NewEntityResolver()
	missing := resolvePartReference(resolver, 902, 2)
	if missing.status != partReferenceUnknown || missing.information != nil {
		t.Fatalf("missing target component resolution = %+v", missing)
	}
	noPart := resolvePartReference(resolver, 902, -1)
	if noPart.status != partReferenceNoPart || noPart.information != nil {
		t.Fatalf("no-part sentinel resolution = %+v", noPart)
	}
	otherNonPositive := resolvePartReference(resolver, 902, 0)
	if otherNonPositive.status != partReferenceUnknown || otherNonPositive.information != nil {
		t.Fatalf("unclassified non-positive PartId = %+v", otherNonPositive)
	}
}

func TestPartComponentUpdatesReplaceAndFollowEntityLifecycle(t *testing.T) {
	resolver := NewEntityResolver()
	if _, err := resolver.ApplyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(1, 100, true, 1)))); err != nil {
		t.Fatal(err)
	}
	if got := resolvePartReference(resolver, 902, 1); got.status != partReferenceCorrelated {
		t.Fatalf("initial part row = %+v", got)
	}
	partialEntity := appendVarintField(nil, FieldEntityPbID, 902)
	partial := appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 0), FieldEntityAddNotifyEntityPbs, partialEntity)
	if _, err := resolver.ApplyEntityAddNotify(partial); err != nil {
		t.Fatal(err)
	}
	if got := resolvePartReference(resolver, 902, 1); got.status != partReferenceCorrelated {
		t.Fatalf("partial entity update should preserve observed component: %+v", got)
	}
	if _, err := resolver.ApplyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(2, 200, false, 2)))); err != nil {
		t.Fatal(err)
	}
	if got := resolvePartReference(resolver, 902, 1); got.status != partReferenceUnknown {
		t.Fatalf("new component must replace old part rows: %+v", got)
	}
	if got := resolvePartReference(resolver, 902, 2); got.status != partReferenceCorrelated {
		t.Fatalf("replacement part row = %+v", got)
	}
	if _, _, err := resolver.ApplyEntityRemoveNotify(lifecycleRemoveNotify(902)); err != nil {
		t.Fatal(err)
	}
	if got := resolvePartReference(resolver, 902, 2); got.status != partReferenceUnknown {
		t.Fatalf("removed entity part row must not resolve: %+v", got)
	}
}

func TestUnmatchedActivePartDeltaInvalidatesOldPartSnapshots(t *testing.T) {
	var diagnostic bytes.Buffer
	p := NewProcessor(nil, &bytes.Buffer{}, &diagnostic, nil)
	if _, err := p.Entities.ApplyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(1, 100, true, 1)))); err != nil {
		t.Fatal(err)
	}
	response := appendBytesField(nil, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbPartComponent, partComponentWire(partInformationWire(1, 200, true, 1))))
	if err := p.HandleActiveResponse(GameFrame{Token: 7, CommandID: CmdEntityActiveResponse}, response); err != nil {
		t.Fatal(err)
	}
	if got := resolvePartReference(p.Entities, 902, 1); got.status != partReferenceUnknown {
		t.Fatalf("unmatched ActiveResponse part delta must invalidate stale entity-local state: %+v", got)
	}
}
