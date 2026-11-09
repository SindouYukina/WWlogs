package capture

import (
	"reflect"
	"testing"
	"time"
)

// These tests run the live clock with a virtual wall clock. They are synthetic: they establish what the clock does for a
// given sequence of frames, and say nothing about how a real game or adapter behaves.

// liveClockHarness drives one processor the way a live session does: frame(at) is a frame received at wall time at, and
// publish(wall) is a publish at that wall time. Times are seconds on the shared effect test base.
type liveClockHarness struct {
	p     *processor
	clock liveViewClock
}

func newLiveClockHarness() *liveClockHarness { return &liveClockHarness{p: newEffectViewProcessor()} }

func (h *liveClockHarness) frame(at float64) {
	h.p.currentFrameTime = effectAt(at)
	h.clock.observeFrame(effectAt(at), effectAt(at))
}

func (h *liveClockHarness) publish(wall float64) ViewEffects {
	h.p.viewClockLead, h.p.viewClockStalled = h.clock.read(effectAt(wall), h.p.currentFrameTime)
	return h.p.viewEffects()
}

func timedEffect(handle uint64, seconds float64) EffectApplyEvent {
	return displayApply(handle, 500+handle, 0, func(e *EffectApplyEvent) {
		e.DurationSeconds, e.RemainingSeconds = effectFloat(seconds), effectFloat(seconds)
		e.DefinitionLinked, e.Static = true, &EffectStaticInfo{DurationPolicy: finitePolicy(), HUDCueTypes: []int32{2}}
	})
}

func TestLiveClockCountsDownThroughNormalQuietGaps(t *testing.T) {
	for _, silence := range []float64{0, 1, 3, 5} {
		h := newLiveClockHarness()
		h.p.effects.ObserveApply(timedEffect(1, 30))
		h.frame(0)
		view := h.publish(silence)
		if view.ClockStalled {
			t.Fatalf("a %.0f s gap stalled the clock", silence)
		}
		requireNear(t, "remaining", view.Active[0].RemainingSeconds, 30-silence)
	}
}

func TestLiveClockStallsExactlyAtTheLimit(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 30))
	h.frame(0)
	limit := LiveFrameSilenceLimit.Seconds()
	if view := h.publish(limit - 0.001); view.ClockStalled {
		t.Fatal("stalled before the limit")
	}
	view := h.publish(limit)
	if !view.ClockStalled {
		t.Fatal("not stalled at the limit")
	}
	requireNear(t, "remaining at the limit", view.Active[0].RemainingSeconds, 30-limit)
}

func TestLiveClockHoldsPastTheLimitWithoutInventingTime(t *testing.T) {
	for _, silence := range []float64{10, 20, 30} {
		h := newLiveClockHarness()
		h.p.effects.ObserveApply(timedEffect(1, 60))
		h.frame(0)
		view := h.publish(silence)
		if !view.ClockStalled {
			t.Fatalf("%.0f s of silence did not stall the clock", silence)
		}
		// The reading stays at the limit: the capture does not know what happened after it.
		requireNear(t, "held remaining", view.Active[0].RemainingSeconds, 60-LiveFrameSilenceLimit.Seconds())
	}
}

func TestLiveClockEndsAnEffectThatRunsOutDuringSilence(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 3))
	h.frame(0)
	if view := h.publish(2); len(view.Active) != 1 {
		t.Fatalf("before its end: %+v", view.Active)
	}
	view := h.publish(3 + effectDisplayExpiryGrace.Seconds() + 0.5)
	if len(view.Active) != 0 || view.Counts.PredictedExpired != 1 {
		t.Fatalf("after its end: %+v", view.Counts)
	}
}

func TestLiveClockNeverReportsANegativeRemaining(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 3))
	h.frame(0)
	for wall := 0.0; wall <= 6; wall += 0.25 {
		for _, effect := range h.publish(wall).Active {
			if effect.RemainingSeconds != nil && *effect.RemainingSeconds < 0 {
				t.Fatalf("remaining %v at %.2f s", *effect.RemainingSeconds, wall)
			}
		}
	}
}

func TestLiveClockKeepsALongEffectThroughSilence(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 120))
	h.frame(0)
	if view := h.publish(30); len(view.Active) != 1 {
		t.Fatalf("a long effect left during silence: %+v", view.Counts)
	}
}

func TestLiveClockRecoveryResynchronizesWithoutReviving(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 15))
	h.p.effects.ObserveApply(timedEffect(2, 120))
	h.frame(0)
	if view := h.publish(30); !view.ClockStalled {
		t.Fatal("expected a stalled clock")
	}
	// Frames return 30 s later and nothing refreshed the effects.
	h.frame(30)
	view := h.publish(30)
	if view.ClockStalled {
		t.Fatal("the first frame back did not clear the stall")
	}
	if len(view.Active) != 1 || *view.Active[0].EffectID != 502 {
		t.Fatalf("after recovery: %+v", view.Active)
	}
	requireNear(t, "remaining after recovery", view.Active[0].RemainingSeconds, 90)
	if view.Counts.PredictedExpired != 1 {
		t.Fatalf("the effect that ran out must stay ended: %+v", view.Counts)
	}
}

func TestLiveClockRecoveryRestoresAnEffectOnlyWhenTheServerDoes(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 3))
	h.frame(0)
	h.frame(30)
	if view := h.publish(30); len(view.Active) != 0 {
		t.Fatalf("an ended effect came back on its own: %+v", view.Active)
	}
	h.p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 1, RemainingSeconds: effectFloat(5), DurationSeconds: effectFloat(5), ObservedAt: effectAt(30)})
	view := h.publish(30)
	if len(view.Active) != 1 {
		t.Fatalf("a server refresh did not restore it: %+v", view.Counts)
	}
	requireNear(t, "refreshed remaining", view.Active[0].RemainingSeconds, 5)
}

func TestLiveClockReadingNeverRunsBackwards(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 60))
	h.frame(10)
	first := h.publish(10.5).Active[0]
	// A frame reassembled late carries an older capture time.
	h.p.currentFrameTime = effectAt(9.9)
	h.clock.observeFrame(effectAt(9.9), effectAt(10.6))
	second := h.publish(10.6).Active[0]
	if *second.RemainingSeconds > *first.RemainingSeconds {
		t.Fatalf("remaining rose from %v to %v", *first.RemainingSeconds, *second.RemainingSeconds)
	}
	requireNear(t, "older frame", second.RemainingSeconds, 60-10.6)
	// A delayed frame: its capture time is older than the wall time already read.
	h.p.currentFrameTime = effectAt(10.7)
	h.clock.observeFrame(effectAt(10.7), effectAt(11.5))
	third := h.publish(11.5).Active[0]
	if *third.RemainingSeconds > *second.RemainingSeconds {
		t.Fatalf("remaining rose from %v to %v", *second.RemainingSeconds, *third.RemainingSeconds)
	}
}

func TestLiveClockStartsOverWhenTheCaptureClockIsSetBack(t *testing.T) {
	var clock liveViewClock
	clock.observeFrame(effectAt(100), effectAt(100))
	clock.observeFrame(effectAt(50), effectAt(101))
	lead, stalled := clock.read(effectAt(101.5), effectAt(50))
	if stalled || lead != 500*time.Millisecond {
		t.Fatalf("after a clock step: lead %v, stalled %v", lead, stalled)
	}
}

func TestLiveClockBeforeTheFirstFrameReadsNothing(t *testing.T) {
	var clock liveViewClock
	if lead, stalled := clock.read(effectAt(100), time.Time{}); lead != 0 || stalled {
		t.Fatalf("lead %v stalled %v", lead, stalled)
	}
	clock.observeFrame(effectAt(1), effectAt(1))
	if lead, stalled := clock.read(effectAt(0), effectAt(1)); lead != 0 || stalled {
		t.Fatalf("a wall clock that went backwards: lead %v stalled %v", lead, stalled)
	}
}

func TestLiveClockOnlyReadsTheEffectsView(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 30))
	h.frame(5)
	before := h.p.effectSnapshotAtLastFrame()
	h.publish(12)
	after := h.p.effectSnapshotAtLastFrame()
	if !reflect.DeepEqual(before.Effects[0].RemainingSeconds, after.Effects[0].RemainingSeconds) || h.p.currentFrameTime != effectAt(5) {
		t.Fatal("reading the clock changed the capture-time state")
	}
}

func TestTransportLossMarksOnlyEffectsNotUpdatedSince(t *testing.T) {
	h := newLiveClockHarness()
	h.p.effects.ObserveApply(timedEffect(1, 60))
	h.p.effects.ObserveApply(timedEffect(2, 60))
	h.frame(0)
	h.p.effects.MarkTransportLoss(effectAt(1))
	h.p.effects.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 2, RemainingSeconds: effectFloat(60), DurationSeconds: effectFloat(60), ObservedAt: effectAt(2)})
	h.frame(2)
	view := h.publish(2)
	if view.Status != EffectSnapshotDegraded {
		t.Fatalf("status = %s", view.Status)
	}
	stale := map[uint64]bool{}
	for _, effect := range view.Active {
		stale[*effect.EffectID] = effect.Stale
	}
	if !stale[501] || stale[502] {
		t.Fatalf("stale = %v, want only the effect nothing updated since the loss", stale)
	}
}

func TestReplayViewDoesNotDependOnAnyWallClock(t *testing.T) {
	build := func() ViewEffects {
		p := newEffectViewProcessor()
		p.effects.ObserveApply(timedEffect(1, 30))
		p.currentFrameTime = effectAt(4)
		return p.viewEffects()
	}
	first := build()
	if first.ClockStalled {
		t.Fatal("a replay reported a stalled clock")
	}
	requireNear(t, "replay remaining", first.Active[0].RemainingSeconds, 26)
	time.Sleep(5 * time.Millisecond)
	if second := build(); !reflect.DeepEqual(first, second) {
		t.Fatalf("the same events gave a different view: %+v vs %+v", first, second)
	}
}
