package capture

// EntityLifecycleState is the last lifecycle transition observed for a runtime
// entity ID in the current session.
type EntityLifecycleState string

const (
	LifecyclePresent EntityLifecycleState = "PRESENT"
	lifecycleRemoved EntityLifecycleState = "REMOVED"
	LifecycleUnknown EntityLifecycleState = "UNKNOWN"
)

// entityLifecycle records how one entity ID has appeared and disappeared. It is
// kept apart from runtimeEntity: the resolver deletes the runtime entry on
// removal, but the removal itself is a fact that must stay observable.
type entityLifecycle struct {
	state             EntityLifecycleState
	addCount          uint64
	removeCount       uint64
	reusedAfterRemove uint64
	lastConfigID      uint64
	hasConfigID       bool
	lastSource        string
	lastSequence      uint64
}

// EntityLifecycleView is the normalized lifecycle of one entity at the moment an
// event is written. Known is false when no add, remove or scene snapshot named
// the entity, so consumers must not read a zero count as "never added".
type EntityLifecycleView struct {
	Known             bool
	State             EntityLifecycleState
	AddCount          uint64
	RemoveCount       uint64
	ReusedAfterRemove uint64
	LastSource        string
}

// lifecycleView returns the lifecycle of entityID. A nil resolver or an
// unobserved ID yields an unknown view rather than a guessed state.
func (r *EntityResolver) lifecycleView(entityID uint64) EntityLifecycleView {
	if r == nil {
		return EntityLifecycleView{State: LifecycleUnknown}
	}
	record, ok := r.lifecycles[entityID]
	if !ok {
		return EntityLifecycleView{State: LifecycleUnknown}
	}
	return EntityLifecycleView{
		Known:             true,
		State:             record.state,
		AddCount:          record.addCount,
		RemoveCount:       record.removeCount,
		ReusedAfterRemove: record.reusedAfterRemove,
		LastSource:        record.lastSource,
	}
}

// ObserveLifecycleAdd records an EntityAddNotify for entity. An add after a
// removal with a different ConfigId is counted as ID reuse. An add that repeats
// the same ConfigId without a removal is a duplicate and changes no counter other
// than addCount.
func (r *EntityResolver) ObserveLifecycleAdd(entity RuntimeEntity) {
	r.lifecycleSequence++
	record := r.lifecycles[entity.EntityID]
	if record.state == lifecycleRemoved && record.hasConfigID && entity.HasConfigID && record.lastConfigID != entity.ConfigID {
		record.reusedAfterRemove++
	}
	record.state = LifecyclePresent
	record.addCount++
	record.lastSource = "EntityAddNotify"
	if entity.HasConfigID {
		record.lastConfigID, record.hasConfigID = entity.ConfigID, true
	}
	record.lastSequence = r.lifecycleSequence
	r.storeLifecycle(entity.EntityID, record)
}

// observeLifecycleRemove records an EntityRemoveNotify entry. Removing an entity
// that is already REMOVED is a duplicate and changes nothing. Removing an ID with
// no prior record still records the removal, so the state is REMOVED rather than
// UNKNOWN.
func (r *EntityResolver) observeLifecycleRemove(entityID uint64) {
	record, ok := r.lifecycles[entityID]
	if ok && record.state == lifecycleRemoved {
		return
	}
	r.lifecycleSequence++
	record.state = lifecycleRemoved
	record.removeCount++
	record.lastSource = "EntityRemoveNotify"
	record.lastSequence = r.lifecycleSequence
	r.storeLifecycle(entityID, record)
}

// observeLifecycleSnapshot applies a JoinSceneNotify snapshot. Entities in the
// snapshot become PRESENT without changing addCount, because the snapshot is not
// an EntityAddNotify. Entities that were PRESENT but are absent from the new
// snapshot became REMOVED through the scene change, not through an EntityRemove
// message, so their removeCount is left unchanged.
func (r *EntityResolver) observeLifecycleSnapshot(snapshotIDs []uint64) {
	inSnapshot := make(map[uint64]struct{}, len(snapshotIDs))
	for _, id := range snapshotIDs {
		inSnapshot[id] = struct{}{}
	}
	for id, record := range r.lifecycles {
		if _, ok := inSnapshot[id]; ok || record.state != LifecyclePresent {
			continue
		}
		r.lifecycleSequence++
		record.state = lifecycleRemoved
		record.lastSource = "JoinSceneNotify"
		record.lastSequence = r.lifecycleSequence
		r.lifecycles[id] = record
	}
	for _, id := range snapshotIDs {
		r.lifecycleSequence++
		record := r.lifecycles[id]
		record.state = LifecyclePresent
		record.lastSource = "JoinSceneNotify"
		record.lastSequence = r.lifecycleSequence
		r.storeLifecycle(id, record)
	}
}

// storeLifecycle writes one record and evicts the least recently observed record
// when the table exceeds the resolver limit. Eviction is the same bounded policy
// used for runtime entities; an evicted ID reads as UNKNOWN again.
func (r *EntityResolver) storeLifecycle(entityID uint64, record entityLifecycle) {
	r.lifecycles[entityID] = record
	if r.limit <= 0 || len(r.lifecycles) <= r.limit {
		return
	}
	var oldestID, oldestSequence uint64
	first := true
	for id, candidate := range r.lifecycles {
		if first || candidate.lastSequence < oldestSequence {
			oldestID, oldestSequence, first = id, candidate.lastSequence, false
		}
	}
	if !first {
		delete(r.lifecycles, oldestID)
	}
}
