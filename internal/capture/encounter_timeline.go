package capture

import (
	"math"
	"sort"
	"time"
)

// Encounter DPS timeline ("DPS over time").
//
// Each encounter keeps the confirmed local damage it was credited, summed into fixed 1-second intervals on a grid
// anchored at the encounter's opening time. Damage enters through the same encounterTracker.addToCurrent call that
// fills the encounter totals and role aggregate, and each amount is split by the contributor roleAggregate.add
// credited, so for every encounter:
//
//	sum(points.Damage) == Totals.DamageDealt (confirmed local outgoing damage), and per contributor
//	sum(points.ContributorDamage[role]) == Roles[role].DamageDealt, likewise Vehicle and UnassignedRole.
//
// Unattributed (UNKNOWN ownership) damage is not local and never enters the timeline. Times are capture observation
// times, so a replay yields the same timeline at any replay speed. A timeline is rendered on demand at the same end
// as Encounter DPS: the query time of an open encounter (never before the newest observed capture time), the decided
// end of an ending candidate or finished encounter. Intervals inside that span without damage are reported as zero.
//
// Interval boundaries are [start, start+1s). The span is clipped to [encounter start, end]; damage observed outside it
// (a late response before a moved start, or after the decided end) is clamped into the first or last interval, so the
// sum is always conserved. An interval shorter than its nominal length is reported as in_progress (open encounter)
// or partial (settled), with DPS computed over its real duration.

// TimelineContributor identifies who confirmed local damage was credited to.
type TimelineContributor struct {
	Kind        string `json:"kind"`
	PlayerID    uint64 `json:"playerId,omitempty"`
	RoleID      uint64 `json:"roleId,omitempty"`
	VehicleKind string `json:"vehicleKind,omitempty"`
}

// Timeline contributor kinds; they match the role aggregate's three outgoing buckets.
const (
	TimelineContributorRole       = "role"
	TimelineContributorVehicle    = "vehicle"
	TimelineContributorUnassigned = "unassigned"
)

// TimelinePointState says whether an interval covers its whole nominal length.
type TimelinePointState string

const (
	// TimelinePointComplete covers the whole interval length.
	TimelinePointComplete TimelinePointState = "complete"
	// TimelinePointInProgress is the newest interval of an open encounter; more damage may still arrive in it.
	TimelinePointInProgress TimelinePointState = "in_progress"
	// TimelinePointPartial is a settled interval shorter than the interval length, normally the last one of a
	// finished encounter. Its DPS uses its real duration.
	TimelinePointPartial TimelinePointState = "partial"
)

// TimelinePoint is one interval. DPS is Damage divided by DurationSeconds, or nil when the duration is zero.
type TimelinePoint struct {
	StartSeconds    float64            `json:"startSeconds"`
	DurationSeconds float64            `json:"durationSeconds"`
	State           TimelinePointState `json:"state"`
	Damage          int64              `json:"damage"`
	DPS             *float64           `json:"dps"`
	// ContributorDamage is aligned with EncounterTimelineSnapshot.Contributors.
	ContributorDamage []int64 `json:"contributorDamage"`
}

// EncounterTimelineSnapshot is one encounter's timeline. It holds no reference into engine state.
type EncounterTimelineSnapshot struct {
	EncounterID string `json:"encounterId"`
	// Final is set once the encounter finished: its end and every interval are settled.
	Final           bool      `json:"final"`
	IntervalSeconds float64   `json:"intervalSeconds"`
	StartedAt       time.Time `json:"startedAt"`
	EndAt           time.Time `json:"endAt"`
	DurationSeconds float64   `json:"durationSeconds"`
	// DamageDealt is the sum of every point; it equals the encounter's confirmed local damage dealt.
	DamageDealt  int64                 `json:"damageDealt"`
	Contributors []TimelineContributor `json:"contributors"`
	Points       []TimelinePoint       `json:"points"`
}

const (
	timelineIntervalLength = time.Second
	// maxTimelineBuckets bounds the stored intervals of one encounter (six hours of 1-second intervals). Beyond it
	// the stored resolution doubles by merging neighbours, which keeps every amount.
	maxTimelineBuckets = 6 * 60 * 60
	// maxTimelineRenderPoints bounds a full-resolution render the same way.
	maxTimelineRenderPoints = maxTimelineBuckets
)

type timelineBucket struct {
	damage int64
	parts  []int64 // indexed like encounterTimeline.contributors
}

// encounterTimeline is the incremental store behind one encounter's timeline. It is owned by the encounter tracker
// and used under the statistics lock.
type encounterTimeline struct {
	anchor       time.Time
	scale        int64 // stored interval length in units of timelineIntervalLength; a power of two
	buckets      map[int64]*timelineBucket
	contributors []TimelineContributor
	index        map[TimelineContributor]int
	damage       int64
}

func newEncounterTimeline(anchor time.Time) *encounterTimeline {
	return &encounterTimeline{anchor: anchor, scale: 1, buckets: make(map[int64]*timelineBucket), index: make(map[TimelineContributor]int)}
}

// floorDiv divides rounding toward negative infinity.
func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func (tl *encounterTimeline) unit() time.Duration {
	return timelineIntervalLength * time.Duration(tl.scale)
}

// add credits amount observed at at to contributor.
func (tl *encounterTimeline) add(at time.Time, amount int64, contributor TimelineContributor) {
	if tl == nil || amount <= 0 || at.IsZero() {
		return
	}
	position, ok := tl.index[contributor]
	if !ok {
		position = len(tl.contributors)
		tl.contributors = append(tl.contributors, contributor)
		tl.index[contributor] = position
	}
	k := floorDiv(int64(at.Sub(tl.anchor)), int64(tl.unit()))
	bucket := tl.buckets[k]
	if bucket == nil {
		bucket = &timelineBucket{}
		tl.buckets[k] = bucket
	}
	for len(bucket.parts) <= position {
		bucket.parts = append(bucket.parts, 0)
	}
	bucket.damage += amount
	bucket.parts[position] += amount
	tl.damage += amount
	for len(tl.buckets) > maxTimelineBuckets {
		tl.coarsen()
	}
}

// coarsen doubles the stored interval length by merging neighbouring buckets; no amount is lost.
func (tl *encounterTimeline) coarsen() {
	merged := make(map[int64]*timelineBucket, len(tl.buckets)/2+1)
	for k, bucket := range tl.buckets {
		target := floorDiv(k, 2)
		into := merged[target]
		if into == nil {
			into = &timelineBucket{}
			merged[target] = into
		}
		into.damage += bucket.damage
		for len(into.parts) < len(bucket.parts) {
			into.parts = append(into.parts, 0)
		}
		for i, part := range bucket.parts {
			into.parts[i] += part
		}
	}
	tl.buckets = merged
	tl.scale *= 2
}

// span returns the first and last interval index, in intervals of factor stored intervals, that [start, end] covers.
// An end exactly on a boundary closes the previous interval instead of opening an empty one.
func (tl *encounterTimeline) span(start, end time.Time, factor int64) (first, last int64) {
	size := int64(tl.unit()) * factor
	first = floorDiv(int64(start.Sub(tl.anchor)), size)
	last = floorDiv(int64(end.Sub(tl.anchor)), size)
	if last > first && tl.anchor.Add(time.Duration(last*size)).Equal(end) {
		last--
	}
	return first, last
}

// peak returns the stored interval of [start, end] with the most damage, with the same clipping as render: damage
// outside the span counts in the first or last interval. The interval is reported at its nominal length even when the
// span cuts it short, so a short first or last interval is never scaled up. ok is false without damage.
func (tl *encounterTimeline) peak(start, end time.Time) (damage int64, begin time.Time, length time.Duration, ok bool) {
	if tl == nil || len(tl.buckets) == 0 || start.IsZero() || end.IsZero() || end.Before(start) {
		return 0, time.Time{}, 0, false
	}
	first, last := tl.span(start, end, 1)
	sums := make(map[int64]int64, len(tl.buckets))
	for k, bucket := range tl.buckets {
		sums[max(first, min(last, k))] += bucket.damage
	}
	best := int64(0)
	for k, sum := range sums {
		// Ties go to the earliest interval so the result does not depend on map order.
		if sum > damage || (sum == damage && sum > 0 && k < best) {
			damage, best = sum, k
		}
	}
	if damage <= 0 {
		return 0, time.Time{}, 0, false
	}
	length = tl.unit()
	begin = tl.anchor.Add(time.Duration(best * int64(length)))
	if begin.Before(start) {
		begin = start
	}
	return damage, begin, length, true
}

// displaySeconds are the display interval lengths a bounded render may use, so labels stay on round durations.
var displaySeconds = []int64{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600}

// render returns the timeline over [start, end]. open marks an encounter that has not finished. maxPoints bounds the
// number of points (0 uses the stored resolution); a coarser display interval is a whole multiple of the stored one,
// so every display interval is an exact sum of stored intervals.
func (tl *encounterTimeline) render(encounterID string, start, end time.Time, open bool, maxPoints int) EncounterTimelineSnapshot {
	snapshot := EncounterTimelineSnapshot{
		EncounterID: encounterID, Final: !open, StartedAt: start, EndAt: end,
		Contributors: []TimelineContributor{}, Points: []TimelinePoint{},
	}
	if tl == nil {
		return snapshot
	}
	snapshot.IntervalSeconds = tl.unit().Seconds()
	snapshot.DamageDealt = tl.damage
	order := tl.contributorOrder()
	for _, position := range order {
		snapshot.Contributors = append(snapshot.Contributors, tl.contributors[position])
	}
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return snapshot
	}
	snapshot.DurationSeconds = end.Sub(start).Seconds()
	if maxPoints <= 0 || maxPoints > maxTimelineRenderPoints {
		maxPoints = maxTimelineRenderPoints
	}
	unit := int64(tl.unit())
	bounds := func(factor int64) (first, last int64) { return tl.span(start, end, factor) }
	fits := func(factor int64) bool {
		first, last := bounds(factor)
		return last-first+1 <= int64(maxPoints)
	}
	// A display interval must be a whole multiple of the stored one.
	factor := int64(0)
	for _, seconds := range displaySeconds {
		if seconds%tl.scale == 0 && fits(seconds/tl.scale) {
			factor = seconds / tl.scale
			break
		}
	}
	if factor == 0 {
		factor = max(1, displaySeconds[len(displaySeconds)-1]/tl.scale)
		for !fits(factor) {
			factor *= 2
		}
	}
	size := unit * factor
	interval := time.Duration(size)
	snapshot.IntervalSeconds = interval.Seconds()
	first, last := bounds(factor)
	count := last - first + 1
	damage := make([]int64, count)
	parts := make([][]int64, count)
	for k, bucket := range tl.buckets {
		g := floorDiv(k, factor) - first
		g = max(0, min(count-1, g))
		damage[g] += bucket.damage
		if parts[g] == nil {
			parts[g] = make([]int64, len(tl.contributors))
		}
		for i, part := range bucket.parts {
			parts[g][i] += part
		}
	}
	snapshot.Points = make([]TimelinePoint, 0, count)
	for g := int64(0); g < count; g++ {
		begin := tl.anchor.Add(time.Duration((first + g) * size))
		finish := begin.Add(interval)
		if begin.Before(start) {
			begin = start
		}
		if finish.After(end) || g == count-1 {
			finish = end
		}
		point := TimelinePoint{
			StartSeconds: begin.Sub(start).Seconds(), DurationSeconds: finish.Sub(begin).Seconds(),
			Damage: damage[g], ContributorDamage: make([]int64, len(order)),
		}
		switch {
		case finish.Sub(begin) == interval:
			point.State = TimelinePointComplete
		case open && g == count-1:
			point.State = TimelinePointInProgress
		default:
			point.State = TimelinePointPartial
		}
		if point.DurationSeconds > 0 {
			value := float64(point.Damage) / point.DurationSeconds
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				point.DPS = &value
			}
		}
		if parts[g] != nil {
			for out, position := range order {
				point.ContributorDamage[out] = parts[g][position]
			}
		}
		snapshot.Points = append(snapshot.Points, point)
	}
	return snapshot
}

// contributorOrder lists stored contributors as roles by PlayerId then RoleId, then vehicles, then unassigned.
func (tl *encounterTimeline) contributorOrder() []int {
	order := make([]int, len(tl.contributors))
	for i := range order {
		order[i] = i
	}
	rank := map[string]int{TimelineContributorRole: 0, TimelineContributorVehicle: 1, TimelineContributorUnassigned: 2}
	sort.Slice(order, func(i, j int) bool {
		a, b := tl.contributors[order[i]], tl.contributors[order[j]]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.PlayerID != b.PlayerID {
			return a.PlayerID < b.PlayerID
		}
		if a.RoleID != b.RoleID {
			return a.RoleID < b.RoleID
		}
		return a.VehicleKind < b.VehicleKind
	})
	return order
}

// completedTimeline keeps a finished encounter's timeline with the span it was settled at.
type completedTimeline struct {
	encounterID string
	timeline    *encounterTimeline
	start, end  time.Time
}

// timelineEnd is the end the timeline of an unfinished encounter is evaluated at: the same end as its Encounter DPS.
func (t *encounterTracker) timelineEnd(record *encounterRecord, query time.Time) time.Time {
	if record.State == EncounterActive {
		if query.Before(t.highWater) {
			query = t.highWater
		}
		return query
	}
	return record.endAt
}

// focusTimeline renders the timeline of the encounter the UI shows: the open one, otherwise the newest finished one.
func (t *encounterTracker) focusTimeline(query time.Time, maxPoints int) *EncounterTimelineSnapshot {
	if query.IsZero() {
		query = t.highWater
	}
	if t.Current != nil {
		snapshot := t.Current.timeline.render(t.encounterID(t.Current.sequence), t.Current.startAt, t.timelineEnd(t.Current, query), true, maxPoints)
		return &snapshot
	}
	if n := len(t.completedTimelines); n > 0 {
		done := t.completedTimelines[n-1]
		snapshot := done.timeline.render(done.encounterID, done.start, done.end, false, maxPoints)
		return &snapshot
	}
	return nil
}

// allTimelines renders every retained encounter at full resolution, finished ones first.
func (t *encounterTracker) allTimelines(query time.Time) []EncounterTimelineSnapshot {
	if query.IsZero() {
		query = t.highWater
	}
	out := make([]EncounterTimelineSnapshot, 0, len(t.completedTimelines)+1)
	for _, done := range t.completedTimelines {
		out = append(out, done.timeline.render(done.encounterID, done.start, done.end, false, 0))
	}
	if t.Current != nil {
		out = append(out, t.Current.timeline.render(t.encounterID(t.Current.sequence), t.Current.startAt, t.timelineEnd(t.Current, query), true, 0))
	}
	return out
}

// TimelineAt returns the DPS timeline of the encounter the UI shows, evaluated at an explicit capture time with the
// same rules as SnapshotAt. maxPoints bounds the points by widening the display interval; 0 keeps full resolution.
// It returns nil before the first encounter of the statistics interval.
func (s *DamageStatistics) TimelineAt(query CaptureQueryTime, maxPoints int) *EncounterTimelineSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	at := time.Time{}
	if query.CaptureSessionID == s.CaptureSessionID {
		at = query.Timestamp
	}
	return s.Encounters.focusTimeline(at, maxPoints)
}

// EncounterTimelines returns every retained encounter's timeline at full resolution, for regression collectors.
func (s *DamageStatistics) EncounterTimelines() []EncounterTimelineSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Encounters.allTimelines(time.Time{})
}

// StoredTimeline is an encounter timeline's stored intervals as Combat History keeps them: per contributor, the confirmed
// local damage of each stored interval. Render rebuilds the timeline and draws it with the engine's own rules, so a stored
// encounter shows exactly the timeline it had when it finished.
type StoredTimeline struct {
	Anchor time.Time
	// ScaleSeconds is the stored interval length in seconds: 1, or a power of two after a very long encounter coarsened.
	ScaleSeconds int64
	Contributors []TimelineContributor
	Buckets      []StoredTimelineBucket
}

// StoredTimelineBucket is one contributor's damage in one stored interval. Index counts intervals from Anchor.
type StoredTimelineBucket struct {
	Index       int64
	Contributor int
	Damage      int64
}

// store copies the timeline's intervals, ordered by interval then contributor.
func (tl *encounterTimeline) store() StoredTimeline {
	if tl == nil {
		return StoredTimeline{}
	}
	stored := StoredTimeline{Anchor: tl.anchor, ScaleSeconds: tl.scale, Contributors: append([]TimelineContributor{}, tl.contributors...)}
	for k, bucket := range tl.buckets {
		for position, part := range bucket.parts {
			if part != 0 {
				stored.Buckets = append(stored.Buckets, StoredTimelineBucket{Index: k, Contributor: position, Damage: part})
			}
		}
	}
	sort.Slice(stored.Buckets, func(i, j int) bool {
		if stored.Buckets[i].Index != stored.Buckets[j].Index {
			return stored.Buckets[i].Index < stored.Buckets[j].Index
		}
		return stored.Buckets[i].Contributor < stored.Buckets[j].Contributor
	})
	return stored
}

// restore rebuilds the incremental store. ok is false for a timeline no engine could have stored.
func (s StoredTimeline) restore() (*encounterTimeline, bool) {
	if s.ScaleSeconds < 1 || s.ScaleSeconds&(s.ScaleSeconds-1) != 0 {
		return nil, false
	}
	tl := newEncounterTimeline(s.Anchor)
	tl.scale = s.ScaleSeconds
	for _, contributor := range s.Contributors {
		if _, duplicate := tl.index[contributor]; duplicate {
			return nil, false
		}
		tl.index[contributor] = len(tl.contributors)
		tl.contributors = append(tl.contributors, contributor)
	}
	for _, stored := range s.Buckets {
		if stored.Contributor < 0 || stored.Contributor >= len(tl.contributors) || stored.Damage <= 0 {
			return nil, false
		}
		bucket := tl.buckets[stored.Index]
		if bucket == nil {
			bucket = &timelineBucket{}
			tl.buckets[stored.Index] = bucket
		}
		for len(bucket.parts) <= stored.Contributor {
			bucket.parts = append(bucket.parts, 0)
		}
		bucket.parts[stored.Contributor] += stored.Damage
		bucket.damage += stored.Damage
		tl.damage += stored.Damage
	}
	return tl, true
}

// Render draws a finished encounter's stored timeline over [start, end] with at most maxPoints points (0: stored
// resolution), with the same intervals and clipping the live view used. ok is false for a timeline that is not valid.
func (s StoredTimeline) Render(encounterID string, start, end time.Time, maxPoints int) (EncounterTimelineSnapshot, bool) {
	tl, ok := s.restore()
	if !ok {
		return EncounterTimelineSnapshot{EncounterID: encounterID, Final: true, StartedAt: start, EndAt: end, Contributors: []TimelineContributor{}, Points: []TimelinePoint{}}, false
	}
	return tl.render(encounterID, start, end, false, maxPoints), true
}

// RenderForView draws the stored timeline with the live view's point bound (maxViewTimelinePoints).
func (s StoredTimeline) RenderForView(encounterID string, start, end time.Time) (EncounterTimelineSnapshot, bool) {
	return s.Render(encounterID, start, end, maxViewTimelinePoints)
}

// encounterPeakDPS is the highest 1-second Encounter DPS interval of the encounter's timeline, evaluated at the same end
// as Encounter DPS, before data integrity is applied. Damage is that interval's confirmed local damage; Value is that
// damage over the nominal interval length.
func encounterPeakDPS(record *encounterRecord, end time.Time, confidence string) DPSResult {
	result := DPSResult{Confidence: confidence}
	damage, begin, length, ok := record.timeline.peak(record.startAt, end)
	if !ok {
		result.Status, result.Reason = DPSStatusUnavailable, "no_confirmed_damage"
		return result
	}
	result.Damage, result.Duration, result.StartObservedAt = damage, length, begin
	result.EndObservedAt = begin.Add(length)
	if result.EndObservedAt.After(end) {
		result.EndObservedAt = end
	}
	result.Status, result.Value = DPSStatusAvailable, float64(damage)/length.Seconds()
	return result
}
