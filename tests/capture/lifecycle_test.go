package capture

import (
	"testing"
)

// lifecycleAddNotify builds an EntityAddNotify carrying one EntityPb with the
// given ID and ConfigId. Field numbers follow the protocol tests in this package.
func lifecycleAddNotify(entityID, configID uint64) []byte {
	entity := appendVarintField(nil, fieldEntityPbID, entityID)
	entity = appendVarintField(entity, fieldEntityPbConfigID, configID)
	return appendBytesField(appendVarintField(nil, fieldEntityAddNotifyIsAdd, 1), fieldEntityAddNotifyEntityPbs, entity)
}

// lifecycleRemoveNotify builds an EntityRemoveNotify (remove flag 4, one
// EntityRemoveInfo in field 5 carrying the entity ID in field 1).
func lifecycleRemoveNotify(entityIDs ...uint64) []byte {
	notify := appendVarintField(nil, fieldEntityRemoveNotifyIsRemove, 1)
	for _, id := range entityIDs {
		notify = appendBytesField(notify, fieldEntityRemoveNotifyRemoveInfos, appendVarintField(nil, fieldEntityRemoveInfoEntityID, id))
	}
	return notify
}

func mustAdd(t *testing.T, resolver *entityResolver, entityID, configID uint64) {
	t.Helper()
	if _, err := resolver.applyEntityAddNotify(lifecycleAddNotify(entityID, configID)); err != nil {
		t.Fatalf("EntityAddNotify(%d): %v", entityID, err)
	}
}

func mustRemove(t *testing.T, resolver *entityResolver, entityIDs ...uint64) {
	t.Helper()
	if _, _, err := resolver.applyEntityRemoveNotify(lifecycleRemoveNotify(entityIDs...)); err != nil {
		t.Fatalf("EntityRemoveNotify(%v): %v", entityIDs, err)
	}
}

func TestLifecycleAddThenRemoveKeepsRemovedObservable(t *testing.T) {
	resolver := newEntityResolver()
	if view := resolver.lifecycleView(7001); view.Known || view.State != lifecycleUnknown {
		t.Fatalf("never-observed entity must be unknown, got %+v", view)
	}
	mustAdd(t, resolver, 7001, 500)
	if view := resolver.lifecycleView(7001); !view.Known || view.State != lifecyclePresent || view.AddCount != 1 || view.LastSource != "EntityAddNotify" {
		t.Fatalf("after add: %+v", view)
	}
	mustRemove(t, resolver, 7001)
	view := resolver.lifecycleView(7001)
	if view.State != lifecycleRemoved || view.RemoveCount != 1 || view.AddCount != 1 || view.LastSource != "EntityRemoveNotify" {
		t.Fatalf("after remove: %+v", view)
	}
	if _, ok := resolver.entities[7001]; ok {
		t.Fatal("runtime entry should be deleted on removal; lifecycle must not depend on it")
	}
}

func TestLifecycleReuseWithDifferentConfigIsCounted(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7002, 500)
	mustRemove(t, resolver, 7002)
	mustAdd(t, resolver, 7002, 900)
	view := resolver.lifecycleView(7002)
	if view.State != lifecyclePresent || view.AddCount != 2 || view.RemoveCount != 1 || view.ReusedAfterRemove != 1 {
		t.Fatalf("ID reuse with a new ConfigId not counted: %+v", view)
	}
}

func TestLifecycleReAddWithSameConfigAfterRemoveIsNotReuse(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7003, 500)
	mustRemove(t, resolver, 7003)
	mustAdd(t, resolver, 7003, 500)
	view := resolver.lifecycleView(7003)
	if view.ReusedAfterRemove != 0 || view.AddCount != 2 || view.State != lifecyclePresent {
		t.Fatalf("reconnect with the same ConfigId must not count as reuse: %+v", view)
	}
}

func TestLifecycleDuplicateAddWithoutRemoveOnlyAddsCount(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7004, 500)
	mustAdd(t, resolver, 7004, 900)
	view := resolver.lifecycleView(7004)
	if view.AddCount != 2 || view.ReusedAfterRemove != 0 || view.RemoveCount != 0 || view.State != lifecyclePresent {
		t.Fatalf("duplicate add changed more than addCount: %+v", view)
	}
}

func TestLifecycleRepeatedRemoveDoesNotDoubleCount(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7005, 500)
	mustRemove(t, resolver, 7005)
	mustRemove(t, resolver, 7005)
	if view := resolver.lifecycleView(7005); view.RemoveCount != 1 || view.State != lifecycleRemoved {
		t.Fatalf("duplicate remove changed removeCount: %+v", view)
	}
}

func TestLifecycleRemoveOfUnobservedIDRecordsRemovalWithoutAdd(t *testing.T) {
	resolver := newEntityResolver()
	mustRemove(t, resolver, 7006)
	view := resolver.lifecycleView(7006)
	if !view.Known || view.State != lifecycleRemoved || view.RemoveCount != 1 || view.AddCount != 0 {
		t.Fatalf("removal observed without an add: %+v", view)
	}
}

func TestLifecycleMalformedAddLeavesRecordsUnchanged(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7007, 500)
	before := resolver.lifecycleView(7007)
	if _, err := resolver.applyEntityAddNotify([]byte{0xff}); err == nil {
		t.Fatal("malformed EntityAddNotify should return an error")
	}
	if after := resolver.lifecycleView(7007); after != before {
		t.Fatalf("malformed notify changed lifecycle: before=%+v after=%+v", before, after)
	}
}

func TestLifecycleSnapshotMarksPresentAndScenesRemovalWithoutCounting(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7008, 500)
	mustAdd(t, resolver, 7009, 501)
	resolver.observeLifecycleSnapshot([]uint64{7009, 7010})

	gone := resolver.lifecycleView(7008)
	if gone.State != lifecycleRemoved || gone.RemoveCount != 0 || gone.LastSource != "JoinSceneNotify" {
		t.Fatalf("entity absent from snapshot should be REMOVED without removeCount: %+v", gone)
	}
	kept := resolver.lifecycleView(7009)
	if kept.State != lifecyclePresent || kept.AddCount != 1 || kept.LastSource != "JoinSceneNotify" {
		t.Fatalf("snapshot entity should be PRESENT without addCount: %+v", kept)
	}
	fresh := resolver.lifecycleView(7010)
	if fresh.State != lifecyclePresent || fresh.AddCount != 0 || !fresh.Known {
		t.Fatalf("new snapshot-only entity: %+v", fresh)
	}
}

func TestLifecycleIsBoundedAndEvictsOldestObservation(t *testing.T) {
	resolver := newEntityResolver()
	resolver.limit = 2
	mustAdd(t, resolver, 7011, 500)
	mustAdd(t, resolver, 7012, 500)
	mustAdd(t, resolver, 7013, 500)
	if len(resolver.lifecycles) != 2 {
		t.Fatalf("lifecycle table size = %d, want 2", len(resolver.lifecycles))
	}
	if view := resolver.lifecycleView(7011); view.Known {
		t.Fatalf("oldest lifecycle should be evicted to UNKNOWN: %+v", view)
	}
	if view := resolver.lifecycleView(7013); !view.Known {
		t.Fatalf("newest lifecycle should be retained: %+v", view)
	}
}

func TestLifecycleResetClearsRecordsAndSequence(t *testing.T) {
	resolver := newEntityResolver()
	mustAdd(t, resolver, 7014, 500)
	resolver.reset()
	if view := resolver.lifecycleView(7014); view.Known || resolver.lifecycleSequence != 0 {
		t.Fatalf("reset should clear lifecycle state: %+v sequence=%d", view, resolver.lifecycleSequence)
	}
}

func TestNilResolverLifecycleIsUnknown(t *testing.T) {
	var resolver *entityResolver
	if view := resolver.lifecycleView(7015); view.Known || view.State != lifecycleUnknown {
		t.Fatalf("nil resolver lifecycle = %+v", view)
	}
}
