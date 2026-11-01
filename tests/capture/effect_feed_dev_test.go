//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEffectFeedLeavesLifecycleJSONLUnchanged(t *testing.T) {
	var withTracker, withoutTracker []byte
	run := func(disable bool) []byte {
		p := newEffectTestProcessor()
		var lifecycle bytes.Buffer
		p.buffLifecycle = &lifecycle
		if disable {
			p.effects = nil
		}
		deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
		deliverEffectMessage(t, p, 1, effectPackBody(200, 41, effectStackPayload(1, 2, 30, 30)))
		deliverEffectMessage(t, p, 2, effectPackBody(200, 6, effectRemovePayload(1, 200, 100)))
		return lifecycle.Bytes()
	}
	withTracker, withoutTracker = run(false), run(true)
	if len(withTracker) == 0 || string(withTracker) != string(withoutTracker) {
		t.Fatalf("lifecycle JSONL differs with the tracker enabled:\n%s\n---\n%s", withTracker, withoutTracker)
	}
}

func TestEffectFeedWritesFinalStateAsOneJSONLine(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	p.currentFrameTime = effectAt(10)
	var out bytes.Buffer
	if err := p.writeEffectState(&out); err != nil {
		t.Fatalf("write effect state: %v", err)
	}
	if bytes.Count(out.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("effect state is not a single line: %q", out.String())
	}
	var snapshot EffectSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode effect state: %v", err)
	}
	effect := onlyEffect(t, snapshot)
	requireNear(t, "remaining at the last frame time", effect.RemainingSeconds, 20)
	if snapshot.QueryTime == nil || !snapshot.QueryTime.Equal(effectAt(10)) {
		t.Fatalf("query time = %v, want the last frame time", snapshot.QueryTime)
	}
}

// newEffectTestProcessor has a local role entity 100, a second local role 101,
// monsters 200 and 201, and an unclassified entity 300.
func newEffectTestProcessor() *processor {
	p, _, _ := newPlayerRelevanceTestProcessor()
	p.sessionNumber = 1
	observeTestLocalRole(p, 100)
	addTestRuntimeEntity(p, 100, nil)
	addTestRuntimeEntity(p, 200, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9001, true })
	addTestRuntimeEntity(p, 201, func(entity *runtimeEntity) { entity.monsterID, entity.hasMonsterID = 9002, true })
	addTestRuntimeEntity(p, 300, nil)
	return p
}

func TestEffectFeedUpgradesUnknownRelationWhenEvidenceArrives(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 102, 1, 30, 30)))
	if effect := onlyEffect(t, p.effects.Snapshot()); effect.SourceRelation != EffectRelationUnknown {
		t.Fatalf("source relation before the role is known = %s", effect.SourceRelation)
	}
	observeTestLocalRole(p, 102)
	addTestRuntimeEntity(p, 102, nil)
	deliverEffectMessage(t, p, 1, effectPackBody(200, 73, effectDurationPayload(1, 30, 30)))
	if effect := onlyEffect(t, p.effects.Snapshot()); effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("source relation after the role is known = %s", effect.SourceRelation)
	}
}

func TestEffectFeedTracksDecodedApplyStackAndRemove(t *testing.T) {
	p := newEffectTestProcessor()

	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(171, 10041000, 200, 100, 1, 14.8, 14.8)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 1)))
	if effect.HolderEntityID != 200 || effect.HandleID != 171 || effect.EffectID == nil || *effect.EffectID != 10041000 {
		t.Fatalf("decoded apply = %+v", effect)
	}
	if effect.HolderRelation != EffectRelationNonPlayer || effect.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("relations = %s/%s, want non_player/local_role", effect.HolderRelation, effect.SourceRelation)
	}
	requireNear(t, "remaining one second after apply", effect.RemainingSeconds, 13.8)

	// A stack message carries a fresh remaining time for the same handle.
	deliverEffectMessage(t, p, 4, effectPackBody(200, 41, effectStackPayload(171, 2, 14.8, 14.8)))
	deliverEffectMessage(t, p, 4, effectPackBody(200, 73, effectDurationPayload(171, 14.8, 14.8)))
	effect = onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 5)))
	if effect.StackCount == nil || *effect.StackCount != 2 {
		t.Fatalf("stack = %v, want 2", effect.StackCount)
	}
	requireNear(t, "remaining one second after refresh", effect.RemainingSeconds, 13.8)

	deliverEffectMessage(t, p, 18.9, effectPackBody(200, 6, effectRemovePayload(171, 200, 100)))
	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 20))
	if len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 1 || snapshot.RecentEnded[0].Lifecycle != EffectLifecycleConfirmedRemoved {
		t.Fatalf("after remove: %+v", snapshot)
	}
	requireNear(t, "expiry delta", snapshot.RecentEnded[0].ExpiryDeltaSeconds, 0.1)
}

func TestEffectFeedSeparatesSameEffectOnDifferentHolders(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(7, 555, 200, 100, 1, 5, 5)))
	deliverEffectMessage(t, p, 0, effectPackBody(201, 5, effectApplyPayload(7, 555, 201, 100, 1, 5, 5)))
	deliverEffectMessage(t, p, 0, effectPackBody(100, 5, effectApplyPayload(8, 555, 100, 100, 1, 5, 5)))

	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 1))
	if len(snapshot.Effects) != 3 {
		t.Fatalf("effects = %+v, want three isolated instances", snapshot.Effects)
	}
	deliverEffectMessage(t, p, 2, effectPackBody(200, 6, effectRemovePayload(7, 200, 100)))
	snapshot = p.effects.SnapshotAt(effectQueryFor(p, 3))
	if len(snapshot.Effects) != 2 {
		t.Fatalf("a REMOVE on holder 200 changed %d instances", 3-len(snapshot.Effects))
	}
	var selfBuff *EffectInstanceSnapshot
	for i := range snapshot.Effects {
		if snapshot.Effects[i].HolderEntityID == 100 {
			selfBuff = &snapshot.Effects[i]
		}
	}
	if selfBuff == nil || selfBuff.HolderRelation != EffectRelationLocalRole || selfBuff.SourceRelation != EffectRelationLocalRole {
		t.Fatalf("self-applied effect on a local role = %+v", selfBuff)
	}
}

func TestEffectFeedDoesNotPromoteUnknownEntitiesToPlayers(t *testing.T) {
	p := newEffectTestProcessor()
	// Source 999 was never seen; holder 300 is a runtime entity with no monster or player evidence.
	deliverEffectMessage(t, p, 0, effectPackBody(300, 5, effectApplyPayload(1, 42, 300, 999, 1, 5, 5)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 1)))
	if effect.HolderRelation != EffectRelationUnknown || effect.SourceRelation != EffectRelationUnknown {
		t.Fatalf("unknown entities were classified: %s/%s", effect.HolderRelation, effect.SourceRelation)
	}
	if effect.SourceEntityID == nil || *effect.SourceEntityID != 999 {
		t.Fatalf("raw source entity lost: %+v", effect)
	}
}

func TestEffectFeedExcludesMonsterAppliedMonsterEffects(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 201, 1, 5, 5)))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.ExcludedUnrelated != 1 {
		t.Fatalf("NPC-versus-NPC effect was tracked: %+v", snapshot)
	}
}

func TestEffectFeedRequiresCorrelatedHolder(t *testing.T) {
	p := newEffectTestProcessor()
	// TargetEntityId 200 does not match CombatCommon.EntityId 201: the holder is not established.
	deliverEffectMessage(t, p, 0, effectPackBody(201, 5, effectApplyPayload(1, 42, 200, 100, 1, 5, 5)))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.SkippedUncorrelatedHolder != 1 {
		t.Fatalf("uncorrelated apply was tracked: %+v", snapshot)
	}
}

func TestEffectFeedIgnoresRequestNotifications(t *testing.T) {
	p := newEffectTestProcessor()
	apply := appendVarintField(appendVarintField(nil, 4, 71001), 15, 100) // ApplyBuffS2CRequestNotify
	deliverEffectMessage(t, p, 0, effectPackBody(200, 53, apply))
	remove := appendVarintField(appendVarintField(nil, 4, 71001), 6, 100) // RemoveBuffByIdS2CRequestNotify
	deliverEffectMessage(t, p, 1, effectPackBody(200, 59, remove))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters != (EffectCounters{}) {
		t.Fatalf("request notifications reached the tracker: %+v", snapshot)
	}
}

func TestEffectFeedInstantEffectsCreateNoLifecycle(t *testing.T) {
	p := newEffectTestProcessor()
	payload := appendVarintField(nil, 2, ^uint64(1)) // Handle -2 with no duration fields
	payload = appendVarintField(payload, 11, 70052)
	payload = appendVarintField(payload, 7, 100)
	payload = appendVarintField(payload, 8, 100)
	deliverEffectMessage(t, p, 0, effectPackBody(100, 5, payload))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.InstantApplies != 1 {
		t.Fatalf("instant effect result: %+v", snapshot)
	}
}

func TestEffectFeedUnknownDurationStaysActiveWithoutCountdown(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(100, 5, effectApplyPayload(3, 10044000, 100, 100, 1, -1, -1)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 30)))
	if effect.Lifecycle != EffectLifecycleActive || effect.RemainingStatus != EffectRemainingUnknown || effect.RemainingReason != "no_server_duration" || effect.RemainingSeconds != nil {
		t.Fatalf("effect with a -1 duration = %+v", effect)
	}
	deliverEffectMessage(t, p, 31, effectPackBody(100, 6, effectRemovePayload(3, 100, 100)))
	if snapshot := p.effects.Snapshot(); len(snapshot.Effects) != 0 || len(snapshot.RecentEnded) != 1 || snapshot.RecentEnded[0].ExpiryDeltaSeconds != nil {
		t.Fatalf("remove of an effect without a finite duration: %+v", snapshot)
	}
}

func TestEffectFeedStatisticsIntervalResetIsolatesEffects(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	snapshot := p.effects.SnapshotAndResetAt(effectQueryFor(p, 1))
	if len(snapshot.Effects) != 1 || snapshot.StatisticsIntervalID != 1 {
		t.Fatalf("closing snapshot = %+v", snapshot)
	}
	deliverEffectMessage(t, p, 2, effectPackBody(200, 5, effectApplyPayload(2, 43, 200, 100, 1, 30, 30)))
	snapshot = p.effects.SnapshotAt(effectQueryFor(p, 3))
	effect := onlyEffect(t, snapshot)
	if snapshot.StatisticsIntervalID != 2 || effect.HandleID != 2 || snapshot.Counters.Applies != 1 {
		t.Fatalf("next interval = %+v", snapshot)
	}
}

func TestEffectFeedDoesNotChangeDamageStatistics(t *testing.T) {
	p := newEffectTestProcessor()
	before := p.damageStatistics.Snapshot()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	deliverEffectMessage(t, p, 1, effectPackBody(200, 6, effectRemovePayload(1, 200, 100)))
	after := p.damageStatistics.Snapshot()
	if before.DamageDealt != after.DamageDealt || before.DamageTaken != after.DamageTaken || before.StatisticsIntervalID != after.StatisticsIntervalID ||
		before.OutgoingEvents != after.OutgoingEvents || before.IncomingEvents != after.IncomingEvents {
		t.Fatalf("effect messages changed damage statistics: %+v -> %+v", before, after)
	}
	if p.effects.captureSessionID != p.damageStatistics.captureSessionID {
		t.Fatalf("effect session %d differs from damage statistics session %d", p.effects.captureSessionID, p.damageStatistics.captureSessionID)
	}
}

func TestEffectFeedBoundaryDropsLiveState(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectMessage(t, p, 0, effectPackBody(200, 5, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
	p.effects.EndLiveState()
	deliverEffectMessage(t, p, 1, effectPackBody(200, 6, effectRemovePayload(1, 200, 100)))
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.BoundaryDrops != 1 || snapshot.Counters.RemovesWithoutInstance != 1 {
		t.Fatalf("state after boundary = %+v", snapshot)
	}
}

func TestEffectFeedSnapshotRowCreatesBaselineInstanceThatARemoveConfirms(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectSnapshot(t, p, 1, 200, 0, effectSnapshotResponse(168, 640012035, 100, 1, 3, 3.001))
	deliverEffectSnapshot(t, p, 2, 100, 0, effectSnapshotResponse(165, 1001002004, 100, 1, -1, -1))

	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 1))
	if len(snapshot.Effects) != 2 || snapshot.Counters.SnapshotInstances != 2 || snapshot.Counters.Applies != 0 {
		t.Fatalf("snapshot baseline = %+v", snapshot)
	}
	var timed, permanent EffectInstanceSnapshot
	for _, effect := range snapshot.Effects {
		if effect.HolderEntityID == 200 {
			timed = effect
		} else {
			permanent = effect
		}
	}
	if timed.Origin != EffectOriginSnapshot || timed.AppliedAt != nil || !timed.FirstObservedAt.Equal(effectAt(0)) {
		t.Fatalf("snapshot-origin instance claims an application time: %+v", timed)
	}
	requireNear(t, "snapshot remaining one second later", timed.RemainingSeconds, 2.001)
	if permanent.HolderRelation != EffectRelationLocalRole || permanent.RemainingStatus != EffectRemainingUnknown || permanent.RemainingReason != "no_server_duration" {
		t.Fatalf("permanent local buff = %+v", permanent)
	}

	// The REMOVE that follows names the same (holder, handle): it is no longer an orphan.
	deliverEffectMessage(t, p, 3.02, effectPackBody(200, 6, effectRemovePayload(168, 200, 100)))
	snapshot = p.effects.SnapshotAt(effectQueryFor(p, 4))
	if snapshot.Counters.Removes != 1 || snapshot.Counters.RemovesWithoutInstance != 0 || len(snapshot.RecentEnded) != 1 {
		t.Fatalf("remove after a snapshot baseline = %+v", snapshot.Counters)
	}
	requireNear(t, "expiry delta", snapshot.RecentEnded[0].ExpiryDeltaSeconds, 0.019)
}

func TestEffectFeedRepeatedSnapshotDumpsDoNotDuplicateInstances(t *testing.T) {
	p := newEffectTestProcessor()
	for i := 0; i < 3; i++ {
		deliverEffectSnapshot(t, p, uint16(i+1), 200, float64(i), effectSnapshotResponse(168, 640012035, 100, 1, 30, 30))
	}
	snapshot := p.effects.SnapshotAt(effectQueryFor(p, 2.5))
	effect := onlyEffect(t, snapshot)
	if snapshot.Counters.SnapshotInstances != 1 || snapshot.Counters.DuplicateSnapshots != 2 {
		t.Fatalf("counters = %+v", snapshot.Counters)
	}
	requireNear(t, "remaining follows the newest dump", effect.RemainingSeconds, 29.5)
}

func TestEffectFeedSnapshotWithoutRequestMatchIsNotAttributed(t *testing.T) {
	p := newEffectTestProcessor()
	frame := gameFrame{commandID: 26702, direction: "in", token: 9, timestamp: effectAt(0)}
	if err := p.handleActiveResponse(frame, effectSnapshotResponse(168, 640012035, 100, 1, 30, 30)); err != nil {
		t.Fatal(err)
	}
	snapshot := p.effects.Snapshot()
	if len(snapshot.Effects) != 0 || snapshot.Counters.SnapshotsSkipped != 1 {
		t.Fatalf("snapshot row without a request was attributed: %+v", snapshot)
	}
}

func TestEffectFeedApplyAfterSnapshotUpgradesOrigin(t *testing.T) {
	p := newEffectTestProcessor()
	deliverEffectSnapshot(t, p, 1, 200, 0, effectSnapshotResponse(7, 555, 100, 1, 30, 30))
	deliverEffectMessage(t, p, 1, effectPackBody(200, 5, effectApplyPayload(7, 555, 200, 100, 1, 30, 30)))
	effect := onlyEffect(t, p.effects.SnapshotAt(effectQueryFor(p, 2)))
	if effect.Origin != EffectOriginApply || effect.AppliedAt == nil {
		t.Fatalf("origin after a confirming APPLY = %+v", effect)
	}
}
