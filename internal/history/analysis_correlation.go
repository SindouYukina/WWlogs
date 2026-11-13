package history

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"wwlogs/internal/capture"
)

// Damage and effect correlation: where the selected confirmed local hits of a finished encounter fall against one effect
// row's intervals. It is a time correlation only. It never says how much damage an effect added, and no rDPS comes from it.
//
// A hit is placed by its observed time, clamped to the encounter as every analysis is:
//
//   - confirmed: inside an interval the recorded changes confirm (an observed start and a later observed change);
//   - stated: inside an interval only the server's stated time covers;
//   - unknown: the effect's state then cannot be told: inside an unknown interval, a capture gap, the time before an
//     instance's unconfirmed start, at the very instant the effect's state changed (the order of the two is not known), or
//     while the recorded changes cannot rule an instance out (changes the tracker rejected, an instance gone stale);
//   - outside: the recorded changes show no instance of the effect then.

// Correlation classes of a hit against an effect row.
const (
	CorrelationConfirmed = "confirmed"
	CorrelationStated    = "stated"
	CorrelationUnknown   = "unknown"
	CorrelationOutside   = "outside"
)

// Why a hit's correlation is unknown.
const (
	reasonGap             = "gap"
	reasonUnknownInterval = "unknown_interval"
	reasonUnobserved      = "start_unconfirmed"
	reasonSimultaneous    = "simultaneous"
	reasonRejected        = "rejected_changes"
	reasonStale           = "stale_instance"
)

// CorrelationCount is a number of hits and their confirmed damage; Share is of the damage of the split it belongs to.
type CorrelationCount struct {
	Hits   int     `json:"hits"`
	Damage int64   `json:"damage"`
	Share  float64 `json:"share"`
}

// CorrelationSplit divides hits by their correlation class.
type CorrelationSplit struct {
	Confirmed CorrelationCount `json:"confirmed"`
	Stated    CorrelationCount `json:"stated"`
	Unknown   CorrelationCount `json:"unknown"`
	Outside   CorrelationCount `json:"outside"`
	Hits      int              `json:"hits"`
	Damage    int64            `json:"damage"`
}

func (s *CorrelationSplit) add(class string, amount int64) {
	count := &s.Unknown
	switch class {
	case CorrelationConfirmed:
		count = &s.Confirmed
	case CorrelationStated:
		count = &s.Stated
	case CorrelationOutside:
		count = &s.Outside
	}
	count.Hits++
	count.Damage += amount
	s.Hits++
	s.Damage += amount
}

func (s *CorrelationSplit) shares() {
	if s.Damage <= 0 {
		return
	}
	for _, count := range []*CorrelationCount{&s.Confirmed, &s.Stated, &s.Unknown, &s.Outside} {
		count.Share = float64(count.Damage) / float64(s.Damage)
	}
}

// CorrelationRow is one contributor's, skill row's or enemy's split. Key is the contributor key (or "unassigned"), the
// skill row key, or the enemy ordinal ("-1" for a hit on no mapped enemy).
type CorrelationRow struct {
	Key string `json:"key"`
	CorrelationSplit
}

// EffectCorrelation answers one correlation query.
type EffectCorrelation struct {
	State string `json:"state"`
	// Available is false when the encounter's effect changes were more than one analysis reads, or the row is not there.
	Available bool             `json:"available"`
	Effect    string           `json:"effect"`
	Query     AnalysisQuery    `json:"query"`
	Range     AnalysisRange    `json:"range"`
	Total     CorrelationSplit `json:"total"`
	// Untimed counts selected hits without an observed time, which no interval can hold.
	Untimed int `json:"untimed"`
	// Reasons counts the unknown hits by why they are unknown.
	Reasons       map[string]int   `json:"reasons"`
	ByContributor []CorrelationRow `json:"byContributor"`
	BySkill       []CorrelationRow `json:"bySkill"`
	ByEnemy       []CorrelationRow `json:"byEnemy"`
}

// rowTimeline is one effect row's intervals, as unions, with what the recorded changes can rule out.
type rowTimeline struct {
	confirmed, stated, unknown []span
	// unobserved is the time before an instance's unconfirmed start: it may have been active then.
	unobserved []span
	gaps       []span
	// ruleOut is empty when the recorded changes show every instance of the effect; otherwise why they do not.
	ruleOut string
	// start and end bound the encounter: intervals are clamped there, so neither is a change of the effect's state.
	start, end int64
}

func (m *encounterModel) rowTimeline(row *effectRowModel) rowTimeline {
	start := m.start.UnixNano()
	out := rowTimeline{start: start, end: m.end.UnixNano()}
	for _, instance := range row.instances {
		for _, segment := range instance.segments {
			s := span{segment.from, segment.to}
			switch segment.evidence {
			case EffectConfirmed:
				out.confirmed = append(out.confirmed, s)
			case EffectStated:
				out.stated = append(out.stated, s)
			default:
				out.unknown = append(out.unknown, s)
			}
		}
		if instance.startUnconfirmed && instance.firstFact > start {
			out.unobserved = append(out.unobserved, span{start, instance.firstFact})
		}
	}
	out.confirmed, out.stated, out.unknown, out.unobserved = union(out.confirmed), union(out.stated), union(out.unknown), union(out.unobserved)
	for _, gap := range m.gaps {
		out.gaps = append(out.gaps, span{gap.from, gap.to})
	}
	out.gaps = union(out.gaps)
	switch {
	case m.rejected > 0:
		out.ruleOut = reasonRejected
	case row.stale > 0:
		out.ruleOut = reasonStale
	}
	return out
}

// classAt places the moment t, counting an interval's start (closedEnd false) or its end (closedEnd true) as inside it.
func (r rowTimeline) classAt(t int64, closedEnd bool) (string, string) {
	// Every list is a union: sorted and disjoint, so the one interval that can hold t is found by its end.
	inside := func(spans []span) bool {
		i := sort.Search(len(spans), func(k int) bool {
			if closedEnd {
				return spans[k].to >= t
			}
			return spans[k].to > t
		})
		if i == len(spans) {
			return false
		}
		if closedEnd {
			return t > spans[i].from && t <= spans[i].to
		}
		return t >= spans[i].from && t < spans[i].to
	}
	switch {
	case inside(r.gaps):
		return CorrelationUnknown, reasonGap
	case inside(r.confirmed):
		return CorrelationConfirmed, ""
	case inside(r.stated):
		return CorrelationStated, ""
	case inside(r.unknown):
		return CorrelationUnknown, reasonUnknownInterval
	case inside(r.unobserved):
		return CorrelationUnknown, reasonUnobserved
	case r.ruleOut != "":
		return CorrelationUnknown, r.ruleOut
	}
	return CorrelationOutside, ""
}

// classify places a hit at t. A hit at the very instant the effect's state changed is unknown: the two were observed at the
// same time and their order is not known.
func (r rowTimeline) classify(t int64) (string, string) {
	// The encounter's own bounds clamp every interval, so a hit there is read on the encounter's side only.
	if t <= r.start {
		return r.classAt(t, false)
	}
	if t >= r.end {
		return r.classAt(t, true)
	}
	before, reason := r.classAt(t, true)
	after, _ := r.classAt(t, false)
	if before != after {
		return CorrelationUnknown, reasonSimultaneous
	}
	return after, reason
}

// selectedHit reports whether a stored result is one of the hits a correlation (or a correlated event list) selects: a
// confirmed local outgoing damage result in the query's contributor, enemy, skill row and range.
func (m *encounterModel) selectedHit(q resolvedQuery, event analysisEvent) bool {
	if event.healing || event.flow != capture.CombatFlowLocalOutgoing || !q.matchesEnemy(event) {
		return false
	}
	if q.contributor >= 0 && event.contributor != q.contributor {
		return false
	}
	if q.SkillRow != "" && m.skillRowKey(event) != q.SkillRow {
		return false
	}
	return q.inRange(event, int64(m.duration))
}

// correlationOf places one hit against a row's timeline.
func (m *encounterModel) correlationOf(timeline rowTimeline, event analysisEvent) (string, string) {
	return timeline.classify(m.start.UnixNano() + event.rel)
}

// EffectCorrelation places the selected hits of a finished stored encounter against one effect row's intervals.
func (s *Store) EffectCorrelation(ctx context.Context, id int64, query AnalysisQuery, effect string, masters *capture.MasterData) (EffectCorrelation, error) {
	out := EffectCorrelation{Effect: effect, Reasons: map[string]int{}, ByContributor: []CorrelationRow{}, BySkill: []CorrelationRow{}, ByEnemy: []CorrelationRow{}}
	model, err := s.model(ctx, id, masters)
	var state errAnalysisState
	if errors.As(err, &state) {
		out.State = state.state
		return out, nil
	}
	if err != nil {
		return out, err
	}
	q := model.resolve(query)
	out.State, out.Query = AnalysisReady, q.AnalysisQuery
	out.Range = AnalysisRange{FromMs: nsToMs(q.from), ToMs: nsToMs(q.to), DurationMs: nsToMs(int64(model.duration)), Full: q.full}
	if model.effects == nil || model.effects.truncated {
		return out, nil
	}
	row := model.effects.byKey[effect]
	if row == nil {
		return out, nil
	}
	out.Available = true
	timeline := model.rowTimeline(row)
	byContributor, bySkill, byEnemy := map[string]*CorrelationRow{}, map[string]*CorrelationRow{}, map[string]*CorrelationRow{}
	var contributorOrder, skillOrder, enemyOrder []string
	into := func(rows map[string]*CorrelationRow, order *[]string, key string) *CorrelationRow {
		row := rows[key]
		if row == nil {
			row = &CorrelationRow{Key: key}
			rows[key] = row
			*order = append(*order, key)
		}
		return row
	}
	for _, event := range model.events {
		if !model.selectedHit(q, event) {
			continue
		}
		if event.rel < 0 {
			out.Untimed++
			continue
		}
		class, reason := model.correlationOf(timeline, event)
		out.Total.add(class, event.amount)
		if class == CorrelationUnknown {
			out.Reasons[reason]++
		}
		contributor := UnassignedContributor
		if event.contributor >= 0 {
			contributor = model.contributors[event.contributor].key
		}
		into(byContributor, &contributorOrder, contributor).add(class, event.amount)
		if skill := model.skillRowKey(event); skill != "" {
			into(bySkill, &skillOrder, skill).add(class, event.amount)
		}
		into(byEnemy, &enemyOrder, strconv.Itoa(event.enemy)).add(class, event.amount)
	}
	out.Total.shares()
	collect := func(rows map[string]*CorrelationRow, order []string) []CorrelationRow {
		list := make([]CorrelationRow, 0, len(order))
		for _, key := range order {
			row := rows[key]
			row.shares()
			list = append(list, *row)
		}
		sort.SliceStable(list, func(a, b int) bool { return list[a].Damage > list[b].Damage })
		return list
	}
	out.ByContributor, out.BySkill, out.ByEnemy = collect(byContributor, contributorOrder), collect(bySkill, skillOrder), collect(byEnemy, enemyOrder)
	return out, nil
}
