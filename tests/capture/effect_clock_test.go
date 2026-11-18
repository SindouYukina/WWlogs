package capture

import (
	"math"
	"testing"
	"time"
)

// The effect clock follows the game's effect timers: they stand still while a TimeStopRequest or a global pause holds,
// and run at the global dilation otherwise (effect_clock.go). Evidence and the model's fit to the corpus:
// research/runs/2026-11-14/v0.7.0-effect-time-semantics.

func clockElapsed(c *effectClock, from, to float64) float64 {
	return c.at(effectAt(to)) - c.at(effectAt(from))
}

func requireSeconds(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func TestEffectClockStandsStillDuringATimeStop(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.beginStop(77, 3*time.Second, 0, effectAt(10))
	requireSeconds(t, "during the stop", clockElapsed(&c, 10, 12), 0)
	c.endStop(77, effectAt(13))
	requireSeconds(t, "across the stop", clockElapsed(&c, 9, 15), 3)
	requireSeconds(t, "paused", c.counters.PausedSeconds, 3)
	if c.counters.StopsBegun != 1 || c.counters.StopsEnded != 1 || c.counters.StopsBounded != 0 {
		t.Fatalf("counters = %+v", c.counters)
	}
}

// A stop whose end the capture never saw holds at most for its declared duration and the margin.
func TestEffectClockBoundsAStopWithoutEnd(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.beginStop(77, 3*time.Second, 0, effectAt(10))
	// A query does not change the clock: the limit (10 + 3 + 1 s) is taken as the end.
	requireSeconds(t, "past the limit", clockElapsed(&c, 10, 20), 6)
	c.mark(effectAt(30))
	requireSeconds(t, "after the limit was recorded", clockElapsed(&c, 10, 20), 6)
	if c.counters.StopsBounded != 1 || len(c.stops) != 0 {
		t.Fatalf("counters = %+v, stops %v", c.counters, c.stops)
	}
	// A stop that declared no duration is bounded too.
	c.beginStop(78, 0, 0, effectAt(40))
	requireSeconds(t, "undeclared stop", clockElapsed(&c, 40, 40+effectStopDefaultLimit.Seconds()+5), 5)
}

func TestEffectClockFollowsTheGlobalDilation(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.setDilation(0, effectAt(5))
	c.setDilation(1, effectAt(8))
	requireSeconds(t, "global pause", clockElapsed(&c, 0, 10), 7)
	c.setDilation(0.5, effectAt(10))
	requireSeconds(t, "half speed", clockElapsed(&c, 10, 14), 2)
}

// A time stop inside a global pause neither shortens nor doubles the pause.
func TestEffectClockNestsStopsInAGlobalPause(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.setDilation(0, effectAt(5))
	c.beginStop(77, 2*time.Second, 0, effectAt(6))
	c.endStop(77, effectAt(8))
	requireSeconds(t, "still paused after the stop", clockElapsed(&c, 8, 11), 0)
	c.setDilation(1, effectAt(12))
	requireSeconds(t, "whole span", clockElapsed(&c, 0, 14), 7)
	requireSeconds(t, "paused", c.counters.PausedSeconds, 7)
}

// The client's periodic time check ends a pause whose end the capture did not see, but never starts one.
func TestEffectClockTimeCheckEndsAStuckPause(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.setDilation(0, effectAt(5))
	c.sample(1, 1, effectAt(5.5)) // built before the pause reached it: inside the grace, no contradiction
	requireSeconds(t, "within the grace", clockElapsed(&c, 5, 9), 0)
	c.sample(1, 1, effectAt(20))
	requireSeconds(t, "resumed by the sample", clockElapsed(&c, 0, 25), 10)
	if c.counters.DilationSampled != 1 {
		t.Fatalf("counters = %+v", c.counters)
	}

	c.beginStop(77, time.Minute, 0, effectAt(30))
	c.sample(1, 1, effectAt(33))
	requireSeconds(t, "stop ended by the sample", clockElapsed(&c, 30, 40), 7)
	if c.counters.StopsSampled != 1 {
		t.Fatalf("counters = %+v", c.counters)
	}

	c.sample(0, 0, effectAt(50))
	requireSeconds(t, "a sample never starts a pause", clockElapsed(&c, 49, 55), 6)
}

// Before any stop or dilation the clock is the capture clock, and a zero clock runs.
func TestEffectClockRunsWithTheCaptureClockByDefault(t *testing.T) {
	var zero effectClock
	requireSeconds(t, "zero clock", clockElapsed(&zero, 1, 4), 3)
	zero.setDilation(1, effectAt(2))
	requireSeconds(t, "zero clock after a change", clockElapsed(&zero, 1, 4), 3)
	c := newEffectClock()
	requireSeconds(t, "new clock", clockElapsed(&c, 1, 4), 3)
}

func TestDecodeRTimeStopPush(t *testing.T) {
	begin := appendVarintField(nil, fieldRTimeStopPushDuration, 3451)
	end := appendVarintField(nil, fieldRTimeStopPushDilation, rTimeStopPushFullDilation)
	for _, tc := range []struct {
		name string
		body []byte
		want rTimeStop
	}{
		{"begin", begin, rTimeStop{begin: true, duration: 3451 * time.Millisecond, known: true}},
		{"end", end, rTimeStop{known: true}},
		{"slowed flow", appendVarintField(nil, fieldRTimeStopPushDilation, 30), rTimeStop{begin: true, scale: 0.3, known: true}},
		{"slowed begin", appendVarintField(appendVarintField(nil, fieldRTimeStopPushDuration, 2000), fieldRTimeStopPushDilation, 25), rTimeStop{begin: true, duration: 2 * time.Second, scale: 0.25, known: true}},
		{"empty", nil, rTimeStop{}},
	} {
		got, err := decodeRTimeStopPush(tc.body)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %+v (%v), want %+v", tc.name, got, err, tc.want)
		}
	}
}

// A HUD status the game keeps frozen through a time stop keeps its countdown and stays listed, instead of running out on
// the capture clock and being hidden as predicted_expired while the game still draws it (Build 31 LIVE: 101 statuses).
func TestEffectCountdownStandsStillDuringATimeStop(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, func(event *EffectApplyEvent) {
		event.HolderRelation = EffectRelationLocalRole
		event.DurationSeconds, event.RemainingSeconds = effectFloat(10), effectFloat(10)
		// A status the HUD draws with a BUFF arrow.
		event.DefinitionLinked, event.Static = true, &EffectStaticInfo{HUDCueTypes: []int32{2}, HUDPolarity: EffectPolarityBuff}
	}))
	tracker.ObserveTimeStop(1, 20, true, 5*time.Second, 0, effectAt(4))
	during := onlyEffect(t, tracker.SnapshotAt(effectQuery(7)))
	requireNear(t, "remaining during the stop", during.RemainingSeconds, 6)
	tracker.ObserveTimeStop(1, 20, false, 0, 0, effectAt(9))
	after := onlyEffect(t, tracker.SnapshotAt(effectQuery(13.5)))
	requireNear(t, "remaining after the stop", after.RemainingSeconds, 1.5)
	if after.RemainingStatus != EffectRemainingEstimated || after.OverdueSeconds != nil {
		t.Fatalf("after the stop = %s overdue %v", after.RemainingStatus, after.OverdueSeconds)
	}
	if display := EffectDisplayFor(after); display.Eligibility != EffectDisplayEligible {
		t.Fatalf("a frozen status is not listed: %+v", display)
	}
	if want := effectAt(15); !after.ExpectedExpiry.Equal(want) {
		t.Fatalf("expected expiry = %v, want %v", after.ExpectedExpiry, want)
	}
	// On the capture clock the status would be 3.5 s overdue here; on the effect clock it runs out at 15 s.
	overdue := onlyEffect(t, tracker.SnapshotAt(effectQuery(17.5)))
	requireNear(t, "overdue after its effect-time expiry", overdue.OverdueSeconds, 2.5)
	if display := EffectDisplayFor(overdue); display.Eligibility != EffectDisplayPredictedExpired {
		t.Fatalf("an expired status is still listed: %+v", display)
	}
	if counters := tracker.SnapshotAt(effectQuery(17.5)).Counters.Clock; counters.StopsBegun != 1 || counters.StopsEnded != 1 {
		t.Fatalf("clock counters = %+v", counters)
	}
}

// A stop of an earlier connection never holds the clock of the next one.
func TestEffectClockStartsAgainWithANewProtocolSession(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, nil))
	tracker.ObserveTimeStop(1, 20, true, time.Minute, 0, effectAt(1))
	tracker.ObserveApply(testEffectApply(6, 2, func(event *EffectApplyEvent) {
		event.SessionNumber = 2
		event.DurationSeconds, event.RemainingSeconds = effectFloat(10), effectFloat(10)
	}))
	effect := onlyEffect(t, tracker.SnapshotAt(effectQuery(5)))
	requireNear(t, "new session runs", effect.RemainingSeconds, 7)
}

// A global pause freezes a stop's animation with everything else: its limit does not run while the pause holds.
func TestEffectClockStopLimitWaitsOutAGlobalPause(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.beginStop(77, 3*time.Second, 0, effectAt(10))
	c.setDilation(0, effectAt(11))
	c.mark(effectAt(20)) // past the unpaused limit (14 s): the stop still holds
	if len(c.stops) != 1 {
		t.Fatalf("a stop ended during a global pause: %+v", c.counters)
	}
	c.setDilation(1, effectAt(21))
	// The limit moved on by the 10 s pause: 14 + 10 = 24 s.
	requireSeconds(t, "stop after the pause", clockElapsed(&c, 21, 26), 2)
	c.endStop(77, effectAt(23.5))
	requireSeconds(t, "ended by its own end", clockElapsed(&c, 21, 26), 2.5)
}

// proto3 leaves a 0 dilation off the wire: an empty TimeStopPush pauses the effect clock.
func TestEmptyTimeStopPushPausesTheEffectClock(t *testing.T) {
	p := newPairingTestProcessor()
	p.Session = make([]byte, 32)
	p.Protocol.BeginSession()
	p.Protocol.ObserveVersion(CurrentGameVersion())
	p.HandleFrame(GameFrame{CommandID: cmdTimeStopPush, Direction: "out", MessageType: 2, Body: nil, Timestamp: effectAt(5)})
	if counters := p.Effects.SnapshotAt(effectQuery(6)).Counters.Clock; counters.DilationChanges != 1 {
		t.Fatalf("clock counters = %+v", counters)
	}
	requireSeconds(t, "paused", p.Effects.clock.at(effectAt(9))-p.Effects.clock.at(effectAt(5)), 0)
}

// A TimeStopRequest with a scale above 0 slows effect timers to that scale instead of stopping them; concurrent requests
// combine as the client does: 0 if any pauses, the lowest scale otherwise (TimeController.Wgp).
func TestEffectClockFollowsAScaledTimeStop(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.beginStop(77, 10*time.Second, 0.5, effectAt(10))
	requireSeconds(t, "at half speed", clockElapsed(&c, 10, 14), 2)
	c.beginStop(78, 10*time.Second, 0.25, effectAt(14))
	requireSeconds(t, "the lowest scale holds", clockElapsed(&c, 14, 18), 1)
	c.beginStop(79, 2*time.Second, 0, effectAt(18))
	requireSeconds(t, "a pausing stop wins", clockElapsed(&c, 18, 19), 0)
	c.endStop(79, effectAt(19))
	c.endStop(78, effectAt(20))
	requireSeconds(t, "back to the remaining scale", clockElapsed(&c, 19, 20), 0.25)
	c.endStop(77, effectAt(22))
	requireSeconds(t, "after every stop", clockElapsed(&c, 22, 23), 1)
	requireSeconds(t, "paused seconds count only the pause", c.counters.PausedSeconds, 1)
}

// The client keeps its flow clock running during a scaled stop, so a TimeCheckRequest never ends one; it still ends a
// pausing stop whose end the capture missed.
func TestEffectClockTimeCheckDoesNotEndAScaledStop(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.beginStop(77, time.Minute, 0.5, effectAt(10))
	c.sample(0.5, 1, effectAt(13))
	if len(c.stops) != 1 || c.counters.StopsSampled != 0 {
		t.Fatalf("a scaled stop was ended by a sample: stops %v, counters %+v", c.stops, c.counters)
	}
	requireSeconds(t, "still at half speed", clockElapsed(&c, 13, 15), 1)
	c.beginStop(78, time.Minute, 0, effectAt(15))
	c.sample(0.5, 1, effectAt(17))
	if _, scaled := c.stops[77]; len(c.stops) != 1 || !scaled || c.counters.StopsSampled != 1 {
		t.Fatalf("a running flow clock ends the pausing stop only: stops %v, counters %+v", c.stops, c.counters)
	}
	requireSeconds(t, "the scaled stop still holds", clockElapsed(&c, 17, 19), 1)
}

// A scaled stop without an end slows effects until its declared duration and the margin, then the clock runs again.
func TestEffectClockBoundsAScaledStopWithoutEnd(t *testing.T) {
	c := newEffectClock()
	c.mark(effectAt(0))
	c.beginStop(77, 3*time.Second, 0.5, effectAt(10))
	// 10..14 at half speed (limit 10 + 3 + 1 s), then 14..20 at full speed.
	requireSeconds(t, "past the limit", clockElapsed(&c, 10, 20), 8)
}

// Changes the server states during a stop take effect in effect time: an APPLY or a refresh in a stop counts down only
// once the stop ends, and a REMOVE in a stop ends the instance at once.
func TestEffectCountdownFollowsChangesMadeDuringAStop(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveTimeStop(1, 20, true, 10*time.Second, 0, effectAt(1))
	tracker.ObserveApply(testEffectApply(5, 2, func(event *EffectApplyEvent) {
		event.DurationSeconds, event.RemainingSeconds = effectFloat(10), effectFloat(10)
	}))
	requireNear(t, "applied during the stop", onlyEffect(t, tracker.SnapshotAt(effectQuery(4))).RemainingSeconds, 10)
	tracker.ObserveUpdate(EffectUpdateEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, StackCount: 2, HasStackCount: true,
		DurationSeconds: effectFloat(8), RemainingSeconds: effectFloat(8), ObservedAt: effectAt(5)})
	requireNear(t, "refreshed during the stop", onlyEffect(t, tracker.SnapshotAt(effectQuery(5.5))).RemainingSeconds, 8)
	tracker.ObserveTimeStop(1, 20, false, 0, 0, effectAt(6))
	requireNear(t, "after the stop", onlyEffect(t, tracker.SnapshotAt(effectQuery(9))).RemainingSeconds, 5)
	tracker.ObserveTimeStop(1, 21, true, 10*time.Second, 0, effectAt(10))
	tracker.ObserveRemove(EffectRemoveEvent{SessionNumber: 1, HolderEntityID: 10, HandleID: 5, ObservedAt: effectAt(11)})
	if snapshot := tracker.SnapshotAt(effectQuery(12)); len(snapshot.Effects) != 0 {
		t.Fatalf("a REMOVE during a stop must end the instance: %+v", snapshot.Effects)
	}
}

// A scaled stop slows a status countdown to its scale.
func TestEffectCountdownSlowsDuringAScaledStop(t *testing.T) {
	tracker := newTestEffectTracker()
	tracker.ObserveApply(testEffectApply(5, 0, func(event *EffectApplyEvent) {
		event.DurationSeconds, event.RemainingSeconds = effectFloat(10), effectFloat(10)
	}))
	tracker.ObserveTimeStop(1, 20, true, 6*time.Second, 0.5, effectAt(2))
	requireNear(t, "half speed", onlyEffect(t, tracker.SnapshotAt(effectQuery(6))).RemainingSeconds, 6)
	tracker.ObserveTimeStop(1, 20, false, 0, 0, effectAt(6))
	requireNear(t, "full speed again", onlyEffect(t, tracker.SnapshotAt(effectQuery(8))).RemainingSeconds, 4)
}
