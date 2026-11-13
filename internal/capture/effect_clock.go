package capture

import (
	"sort"
	"time"
)

// The game's effect clock.
//
// The client runs every effect timer on its unscaled clock times a rate (ActiveBuffInternal, Global 3.8.9): the rate is 0
// while the holder's buff component holds a pause lock, which a TimeStopRequest puts on every entity
// (TimeController.AddTimeStopRequestLock), and it otherwise follows the global time dilation that menus, UI and QTE set
// (GameModeController.SetTimeDilation; 0 pauses every effect). The client reports both changes to the server: an
// RTimeStopPush in its combat pushes when a TimeStopRequest begins (Duration, no Dilation) and ends (Dilation 100), and a
// TimeStopPush with each new global dilation (an empty body is 0). Effect time is therefore the capture clock with the
// stops cut out and the rest scaled by the dilation; a server statement of remaining time counts down in effect time.
//
// AbsoluteTimeStop (RTimeStopAnimPush) changes only effects flagged DurationAffectedByBulletTime, and no definition a
// HUD draws carries that flag, so it is not followed. A per-effect rate (ModifyBuffTimeScale) is not followed either: in
// Global 3.8.9 it targets no HUD effect. Evidence: research/runs/2026-11-14/v0.7.0-effect-time-semantics (client code;
// 1,783 of 1,785 natural client expiries of the corpus within 0.25 s of this model, against 1,068 on the capture clock).
//
// A missing end never freezes the clock: a stop ends at the latest at its declared duration plus a margin, and the
// client's TimeCheckRequest, sent every 3 s with its current FlowTimeDilation and TimeDilation, ends a stop or a global
// pause whose end the capture did not see. The clock never starts a pause from such a sample, and both TimeStopRequests
// are off in multiplayer, where the clock simply runs with the capture clock.

const (
	// effectStopEndMargin is how long past its declared duration a stop without an end still holds. The corpus stops
	// ended up to 0.53 s before their declared duration and never after it.
	effectStopEndMargin = time.Second
	// effectStopDefaultLimit bounds a stop whose begin declared no duration.
	effectStopDefaultLimit = 30 * time.Second
	// effectClockSampleGrace keeps a TimeCheckRequest sent just after a change from contradicting it: a sample is the
	// client's state when it was built, up to one frame before its packet.
	effectClockSampleGrace = time.Second
	// maxEffectClockPoints bounds the rate history. Effect bases are converted when they are observed, so only queries of
	// a time older than the kept history fall back to the capture clock.
	maxEffectClockPoints = 512
	// maxEffectStops bounds the concurrent stops followed (one per caster entity).
	maxEffectStops = 64
)

// effectClockPoint is a rate change: from at, effect time advances at rate seconds per capture second.
type effectClockPoint struct {
	at        time.Time
	effective float64
	rate      float64
}

// effectStop is one TimeStopRequest the capture saw begin and not end yet.
type effectStop struct {
	since time.Time
	limit time.Time
}

// EffectClockCounters report what the effect clock followed and every place it had to assume.
type EffectClockCounters struct {
	StopsBegun uint64 `json:"stopsBegun"`
	StopsEnded uint64 `json:"stopsEnded"`
	// StopsBounded ended at their declared duration plus the margin, without an end the capture saw.
	StopsBounded uint64 `json:"stopsBounded"`
	// StopsSampled were ended by a TimeCheckRequest that reported the flow clock running.
	StopsSampled uint64 `json:"stopsSampled"`
	// StopsRefused were beyond maxEffectStops and are not followed.
	StopsRefused       uint64  `json:"stopsRefused"`
	DilationChanges    uint64  `json:"dilationChanges"`
	DilationSampled    uint64  `json:"dilationSampled"`
	PausedSeconds      float64 `json:"pausedSeconds"`
	PrunedClockHistory uint64  `json:"prunedClockHistory"`
}

type effectClock struct {
	points   []effectClockPoint
	stops    map[uint64]effectStop
	dilation float64
	// dilatedSince is when the current global dilation began.
	dilatedSince time.Time
	counters     EffectClockCounters
}

func newEffectClock() effectClock {
	return effectClock{stops: map[uint64]effectStop{}, dilation: 1}
}

// reset starts the clock again at a protocol session boundary: a new connection's client starts without stops or
// dilation.
func (c *effectClock) reset() {
	c.points, c.stops, c.dilation, c.dilatedSince = nil, map[uint64]effectStop{}, 1, time.Time{}
}

// ensure makes a zero clock a running one.
func (c *effectClock) ensure() {
	if c.stops == nil {
		*c = newEffectClock()
	}
}

// rate is the rate the clock runs at after its last point, before any stop limit passes.
func (c *effectClock) rate() float64 {
	switch {
	case c.stops == nil:
		return 1
	case len(c.stops) > 0:
		return 0
	}
	return c.dilation
}

// stopsEnd is when the current stops end at the latest: the last of their limits.
func (c *effectClock) stopsEnd() time.Time {
	var end time.Time
	for _, stop := range c.stops {
		if stop.limit.After(end) {
			end = stop.limit
		}
	}
	return end
}

// at returns the effect time at capture time t, in seconds on the clock's own scale (only differences mean anything).
// It does not change the clock: a stop whose limit lies before t is taken as ended at that limit.
func (c *effectClock) at(t time.Time) float64 {
	if len(c.points) == 0 {
		return float64(t.UnixNano()) / 1e9
	}
	index := sort.Search(len(c.points), func(i int) bool { return c.points[i].at.After(t) }) - 1
	if index < 0 {
		// Before the kept history the clock is taken to run with the capture clock.
		first := c.points[0]
		return first.effective - first.at.Sub(t).Seconds()
	}
	point := c.points[index]
	if index < len(c.points)-1 || len(c.stops) == 0 {
		return point.effective + point.rate*t.Sub(point.at).Seconds()
	}
	// After the last point, while stops hold, the clock stands until the last stop's limit, then runs at the dilation.
	end := c.stopsEnd()
	if !t.After(end) {
		return point.effective
	}
	from := point.at
	if end.After(from) {
		from = end
	}
	return point.effective + c.dilation*t.Sub(from).Seconds()
}

// mark records the current rate from at on. It first ends the stops whose limits passed before at.
func (c *effectClock) mark(at time.Time) {
	if at.IsZero() {
		return
	}
	if last := len(c.points) - 1; last >= 0 && at.Before(c.points[last].at) {
		// Frames arrive in capture order; a change dated earlier than the last one applies at the last one.
		at = c.points[last].at
	}
	c.expireStops(at)
	c.appendPoint(at)
}

func (c *effectClock) appendPoint(at time.Time) {
	point := effectClockPoint{at: at, effective: c.at(at), rate: c.rate()}
	// Every stretch at rate 0 is counted once, when the point after it closes it.
	if last := len(c.points) - 1; last >= 0 && c.points[last].rate == 0 && at.After(c.points[last].at) {
		c.counters.PausedSeconds += at.Sub(c.points[last].at).Seconds()
	}
	if last := len(c.points) - 1; last >= 0 && c.points[last].at.Equal(at) {
		c.points[last] = point
	} else {
		c.points = append(c.points, point)
	}
	if len(c.points) > maxEffectClockPoints {
		c.points = append([]effectClockPoint(nil), c.points[len(c.points)-maxEffectClockPoints:]...)
		c.counters.PrunedClockHistory++
	}
}

// expireStops ends, at their limits, the stops whose limits passed before at.
func (c *effectClock) expireStops(at time.Time) {
	for len(c.stops) > 0 {
		end := c.stopsEnd()
		if end.After(at) {
			return
		}
		// Every stop ends by the last limit; the clock runs again from there.
		for key := range c.stops {
			delete(c.stops, key)
			c.counters.StopsBounded++
		}
		c.appendPoint(end)
	}
}

// beginStop follows a TimeStopRequest the caster began at at, declared to last duration (0: undeclared).
func (c *effectClock) beginStop(caster uint64, duration time.Duration, at time.Time) {
	if at.IsZero() {
		return
	}
	c.ensure()
	c.mark(at)
	if _, held := c.stops[caster]; !held && len(c.stops) >= maxEffectStops {
		c.counters.StopsRefused++
		return
	}
	limit := at.Add(effectStopDefaultLimit)
	if duration > 0 {
		limit = at.Add(duration + effectStopEndMargin)
	}
	c.stops[caster] = effectStop{since: at, limit: limit}
	c.counters.StopsBegun++
	c.appendPoint(at)
}

// endStop follows the end of the caster's TimeStopRequest.
func (c *effectClock) endStop(caster uint64, at time.Time) {
	if at.IsZero() {
		return
	}
	c.ensure()
	c.mark(at)
	if _, held := c.stops[caster]; !held {
		return
	}
	delete(c.stops, caster)
	c.counters.StopsEnded++
	c.appendPoint(at)
}

// setDilation follows a new global time dilation.
func (c *effectClock) setDilation(value float64, at time.Time) {
	if at.IsZero() || value < 0 || value != value {
		return
	}
	c.ensure()
	c.mark(at)
	if value == c.dilation {
		return
	}
	c.dilation, c.dilatedSince = value, at
	c.counters.DilationChanges++
	c.appendPoint(at)
}

// sample reconciles the clock with a TimeCheckRequest: the client's flow clock running means no TimeStopRequest holds,
// and a running time dilation means no global pause holds. A sample never starts a pause.
func (c *effectClock) sample(flowDilation, dilation float64, at time.Time) {
	if at.IsZero() {
		return
	}
	c.ensure()
	c.mark(at)
	if flowDilation > 0 && len(c.stops) > 0 {
		recent := false
		for _, stop := range c.stops {
			recent = recent || at.Sub(stop.since) < effectClockSampleGrace
		}
		if !recent {
			for key := range c.stops {
				delete(c.stops, key)
				c.counters.StopsSampled++
			}
			c.appendPoint(at)
		}
	}
	if dilation > 0 && c.dilation == 0 && at.Sub(c.dilatedSince) >= effectClockSampleGrace {
		c.dilation, c.dilatedSince = dilation, at
		c.counters.DilationSampled++
		c.appendPoint(at)
	}
}
