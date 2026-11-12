package history

import (
	"context"
	"errors"
	"sort"

	"wwlogs/internal/capture"
)

// The skill hit timeline of the analysis workspace's icon view.
//
// It lists every confirmed local outgoing damage result of one finished encounter over the whole encounter, with the
// contributor and enemy filters of the query (never its range: the timeline is the whole fight, the range is drawn over
// it), in the skill row the engine's skill split (skill_breakdown.go, as skillStats applies it) puts each one in. These are
// damage hits, never casts: no build stores a server-confirmed skill cast, so two hits close together are two hits and
// no answer groups them into a cast. Damage the engine credited to no contributor is listed apart, with the SkillId it
// carried as observed and no skill row.

// maxTimelineHits bounds the hits one answer lists; any more are counted and the answer says so.
const maxTimelineHits = 50000

// UnassignedContributor is the contributor key of confirmed local outgoing damage the engine credited to no contributor.
const UnassignedContributor = "unassigned"

// HitRow is one skill row's hits in processing order, as parallel lists: the time in milliseconds from the encounter start,
// the confirmed amount, and the stored result's sequence (the event browser lists the same result as "c<sequence>").
type HitRow struct {
	Contributor string `json:"contributor"`
	// SkillRow is the skill table's row key ("<contributor>:<SkillId>" or "<contributor>:unassigned"); empty for damage
	// credited to no contributor, which joins no contributor's skill table.
	SkillRow string `json:"skillRow"`
	// SkillID is the row's SkillId: 0 for a contributor's row without one; for damage credited to no contributor, the
	// SkillId the result carried (0 without one).
	SkillID  uint64    `json:"skillId"`
	AtMs     []float64 `json:"atMs"`
	Amount   []int64   `json:"amount"`
	Sequence []int64   `json:"sequence"`
}

// HitTimeline answers one skill hit timeline query.
type HitTimeline struct {
	State      string  `json:"state"`
	DurationMs float64 `json:"durationMs"`
	// Query is the query as applied: its range and skill row are cleared, an unknown contributor or an unmapped enemy dropped.
	Query AnalysisQuery `json:"query"`
	Rows  []HitRow      `json:"rows"`
	// Total counts every hit the filters select; Listed counts the hits the rows hold. Untimed hits have no time to place
	// on a timeline; Truncated is set when more timed hits than one answer lists were left out.
	Total     int  `json:"total"`
	Listed    int  `json:"listed"`
	Untimed   int  `json:"untimed"`
	Truncated bool `json:"truncated,omitempty"`
}

// Hits lists the skill hits of a finished stored encounter for the icon timeline.
func (s *Store) Hits(ctx context.Context, id int64, query AnalysisQuery, masters *capture.MasterData) (HitTimeline, error) {
	out := HitTimeline{Rows: []HitRow{}}
	model, err := s.model(ctx, id, masters)
	var state errAnalysisState
	if errors.As(err, &state) {
		out.State = state.state
		return out, nil
	}
	if err != nil {
		return out, err
	}
	query.FromMs, query.ToMs, query.SkillRow = 0, 0, ""
	q := model.resolve(query)
	out.State, out.DurationMs, out.Query = AnalysisReady, nsToMs(int64(model.duration)), q.AnalysisQuery
	type rowKey struct {
		contributor int
		skill       uint64
	}
	index := map[rowKey]int{}
	for _, event := range model.events {
		if event.healing || event.flow != capture.CombatFlowLocalOutgoing || !q.matchesEnemy(event) {
			continue
		}
		if q.contributor >= 0 && event.contributor != q.contributor {
			continue
		}
		out.Total++
		if event.rel < 0 {
			out.Untimed++
			continue
		}
		if out.Listed == maxTimelineHits {
			out.Truncated = true
			continue
		}
		key := rowKey{contributor: event.contributor, skill: event.skill}
		row := HitRow{Contributor: UnassignedContributor, SkillID: event.skill}
		if event.contributor >= 0 {
			// The row the skill split puts the hit in: its SkillId's row when the contributor has one, otherwise the
			// contributor's row without a skill.
			if _, known := model.contributors[event.contributor].skills[event.skill]; !known || event.skill == 0 {
				key.skill = 0
			}
			row = HitRow{Contributor: model.contributors[event.contributor].key, SkillRow: model.skillRowKey(event), SkillID: key.skill}
		}
		position, found := index[key]
		if !found {
			position = len(out.Rows)
			index[key] = position
			out.Rows = append(out.Rows, row)
		}
		target := &out.Rows[position]
		target.AtMs = append(target.AtMs, nsToMs(event.rel))
		target.Amount = append(target.Amount, event.amount)
		target.Sequence = append(target.Sequence, event.sequence)
		out.Listed++
	}
	// Contributors in the encounter's order, then damage credited to no contributor; within one, SkillIds in order with the
	// row without a skill last.
	order := func(row HitRow) int {
		if position, ok := model.byKey[row.Contributor]; ok {
			return position
		}
		return len(model.contributors)
	}
	sort.SliceStable(out.Rows, func(a, b int) bool {
		ra, rb := out.Rows[a], out.Rows[b]
		if oa, ob := order(ra), order(rb); oa != ob {
			return oa < ob
		}
		if (ra.SkillID == 0) != (rb.SkillID == 0) {
			return rb.SkillID == 0
		}
		return ra.SkillID < rb.SkillID
	})
	return out, nil
}
