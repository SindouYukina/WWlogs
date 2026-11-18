package capture

import (
	"testing"
)

func u64(value uint64) *uint64 { return &value }

func TestLifecycleGenericIDLinksOnlyApplyMessagesAndKeepsTypedFieldsEmpty(t *testing.T) {
	p := NewProcessor(nil, nil)
	p.Masters = &GameMaster{BuffDefinitions: map[uint64]struct{}{70001: {}}}
	p.SessionNumber = 1

	apply := &BuffLifecycleAuditRow{EventKind: "BUFF_APPLY_REQUEST_NOTIFY", ID: u64(70001)}
	p.linkBuffLifecycleGenericAndHandle(apply)
	if apply.GenericIDDefinitionStatus != ResolutionCorrelated || apply.GenericIDDefinitionTable != "buff" || apply.GenericIDDefinitionID == nil || *apply.GenericIDDefinitionID != 70001 {
		t.Fatalf("an apply Id with a buff row must correlate: %+v", apply)
	}
	if apply.BuffDefinitionStatus != "" || apply.BuffDefinitionID != nil || apply.BuffID != nil {
		t.Fatalf("the typed BuffId fields must stay untouched: %+v", apply)
	}

	missing := &BuffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY", ID: u64(999)}
	p.linkBuffLifecycleGenericAndHandle(missing)
	if missing.GenericIDDefinitionStatus != ResolutionUnknown || missing.GenericIDDefinitionID != nil {
		t.Fatalf("an Id without a row must stay unknown: %+v", missing)
	}

	other := &BuffLifecycleAuditRow{EventKind: "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY", ID: u64(70001)}
	p.linkBuffLifecycleGenericAndHandle(other)
	if other.GenericIDDefinitionStatus != "" {
		t.Fatalf("only apply-type messages carry the generic Id link: %+v", other)
	}
}

func TestLifecycleHandleEventsInheritTheApplyIdWithinSessionAndEntity(t *testing.T) {
	p := NewProcessor(nil, nil)
	p.Masters = &GameMaster{BuffDefinitions: map[uint64]struct{}{70001: {}, 70002: {}}}
	p.SessionNumber = 1
	apply := func(entity, handle, id uint64) {
		row := &BuffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_APPLY", ID: u64(id), HandleID: u64(handle), TargetEntityID: u64(entity)}
		p.linkBuffLifecycleGenericAndHandle(row)
	}
	apply(500, 68, 70001)
	apply(501, 68, 70002) // the same handle on another entity is a different buff

	duration := &BuffLifecycleAuditRow{EventKind: "BUFF_DURATION_CHANGE", HandleID: u64(68), CombatCommonEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(duration)
	if duration.HandleLinkedID == nil || *duration.HandleLinkedID != 70001 || duration.HandleLinkedStatus != ResolutionCorrelated {
		t.Fatalf("the handle must inherit the Id applied on the same entity: %+v", duration)
	}
	other := &BuffLifecycleAuditRow{EventKind: "BUFF_STACK_COUNT_CHANGE", HandleID: u64(68), CombatCommonEntityID: u64(501)}
	p.linkBuffLifecycleGenericAndHandle(other)
	if other.HandleLinkedID == nil || *other.HandleLinkedID != 70002 {
		t.Fatalf("handles are scoped per entity: %+v", other)
	}
	unseen := &BuffLifecycleAuditRow{EventKind: "GAMEPLAY_EFFECT_REMOVE", HandleID: u64(7), TargetEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(unseen)
	if unseen.HandleLinkedID != nil || unseen.HandleLinkedStatus != ResolutionUnknown {
		t.Fatalf("a handle never applied must stay unlinked: %+v", unseen)
	}

	apply(500, 69, 70001)
	apply(500, 69, 70002) // conflicting reuse of one handle
	conflict := &BuffLifecycleAuditRow{EventKind: "BUFF_DURATION_CHANGE", HandleID: u64(69), CombatCommonEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(conflict)
	if conflict.HandleLinkedID != nil || conflict.HandleLinkedStatus != ResolutionUnknown {
		t.Fatalf("a handle applied with different ids must be rejected: %+v", conflict)
	}

	p.SessionNumber = 2
	crossSession := &BuffLifecycleAuditRow{EventKind: "BUFF_DURATION_CHANGE", HandleID: u64(68), CombatCommonEntityID: u64(500)}
	p.linkBuffLifecycleGenericAndHandle(crossSession)
	if crossSession.HandleLinkedID != nil {
		t.Fatalf("handles must not leak across sessions: %+v", crossSession)
	}
}
