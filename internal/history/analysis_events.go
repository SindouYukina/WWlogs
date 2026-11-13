package history

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"wwlogs/internal/capture"
)

// The event browser of the analysis workspace: the stored combat results and effect changes of one encounter, in the order
// the engine processed them, filtered by the workspace's range, contributor and enemy, and paged. It lists normalized
// product records only: endpoints are named as roles, the vehicle, enemy instances or Master types, never by runtime
// entity, handle or RPC identifiers.

// MaxEventPage bounds one page of the event browser.
const MaxEventPage = 200

// maxSkillHits bounds the hits of one skill row an analysis marks on the timeline.
const maxSkillHits = 5000

// Event kinds.
const (
	EventDamage  = "damage"
	EventHealing = "healing"
	EventEffect  = "effect"
)

// EventQuery selects stored events of one encounter.
type EventQuery struct {
	AnalysisQuery
	// Kinds lists damage, healing and effect; empty lists all three.
	Kinds []string `json:"kinds"`
	// EffectRow is an effect row key; empty lists every row of the scope.
	EffectRow string `json:"effectRow"`
	// CorrelateEffect and Correlation list only the selected hits of one correlation class (confirmed, stated, unknown or
	// outside) against that effect row, as EffectCorrelation counts them; effect changes are then not listed.
	CorrelateEffect string `json:"correlateEffect,omitempty"`
	Correlation     string `json:"correlation,omitempty"`
	Offset          int    `json:"offset"`
	Limit           int    `json:"limit"`
}

// EventRow is one stored event in product terms.
type EventRow struct {
	Key  string   `json:"key"`
	AtMs *float64 `json:"atMs,omitempty"`
	Kind string   `json:"kind"`
	// Combat results.
	Flow       string    `json:"flow,omitempty"`
	Amount     int64     `json:"amount,omitempty"`
	Effective  *int64    `json:"effective,omitempty"`
	Source     *ActorRef `json:"source,omitempty"`
	Target     *ActorRef `json:"target,omitempty"`
	SkillID    uint64    `json:"skillId,omitempty"`
	SkillRow   string    `json:"skillRow,omitempty"`
	Confidence string    `json:"confidence,omitempty"`
	Downgraded bool      `json:"downgraded,omitempty"`
	// Correlation and CorrelationReason place a hit against the effect row of a correlated list.
	Correlation       string `json:"correlation,omitempty"`
	CorrelationReason string `json:"correlationReason,omitempty"`
	// Effect changes.
	EffectRow   string `json:"effectRow,omitempty"`
	EffectID    *int64 `json:"effectId,omitempty"`
	Change      string `json:"change,omitempty"`
	Observation string `json:"observation,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Stack       *int64 `json:"stack,omitempty"`
}

// EventPage is one page of the event browser.
type EventPage struct {
	State  string     `json:"state"`
	Total  int        `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
	Rows   []EventRow `json:"rows"`
	// EffectsAvailable is false when the record stored no effect changes, so the list holds combat results only.
	EffectsAvailable bool `json:"effectsAvailable"`
}

// SkillHit is one hit of the selected skill row, for the timeline.
type SkillHit struct {
	AtMs   float64 `json:"atMs"`
	Amount int64   `json:"amount"`
}

// skillRowKey is the skill row a confirmed local outgoing result joined: "<contributor>:<SkillId>", or
// "<contributor>:unassigned" for a hit without a row; empty for any other result.
func (m *encounterModel) skillRowKey(event analysisEvent) string {
	if event.healing || event.flow != capture.CombatFlowLocalOutgoing || event.contributor < 0 {
		return ""
	}
	contributor := m.contributors[event.contributor]
	if _, known := contributor.skills[event.skill]; known && event.skill != 0 {
		return contributor.key + ":" + strconv.FormatUint(event.skill, 10)
	}
	return contributor.key + ":unassigned"
}

// skillHits lists the hits of one skill row over the whole encounter, with the enemy filter applied, for the timeline.
func (m *encounterModel) skillHits(q resolvedQuery) ([]SkillHit, bool) {
	if q.SkillRow == "" {
		return nil, false
	}
	hits := []SkillHit{}
	for _, event := range m.events {
		if event.rel < 0 || !q.matchesEnemy(event) || m.skillRowKey(event) != q.SkillRow {
			continue
		}
		if len(hits) == maxSkillHits {
			return hits, true
		}
		hits = append(hits, SkillHit{AtMs: nsToMs(event.rel), Amount: event.amount})
	}
	return hits, false
}

func (m *encounterModel) endpointActor(endpoint analysisEndpoint, contributor int, role bool) ActorRef {
	switch {
	case contributor >= 0:
		c := m.contributors[contributor]
		if c.vehicle {
			return ActorRef{Kind: ActorVehicle, Contributor: c.key, Relation: endpoint.relation}
		}
		return ActorRef{Kind: ActorRole, Contributor: c.key, RoleID: c.role, Relation: endpoint.relation}
	case role:
		if endpoint.role.Valid && nullUint(endpoint.role) != 0 {
			return ActorRef{Kind: ActorRole, RoleID: nullUint(endpoint.role), Relation: endpoint.relation}
		}
		return ActorRef{Kind: ActorUnassigned, Relation: endpoint.relation}
	}
	if ordinal, ok := m.enemyOf[endpoint.key]; ok {
		return m.enemyActor(ordinal, endpoint.relation)
	}
	if endpoint.monster != 0 || endpoint.template != 0 {
		return ActorRef{Kind: ActorOther, MonsterID: endpoint.monster, TemplateID: endpoint.template, Relation: endpoint.relation}
	}
	return ActorRef{Kind: ActorUnknown, Relation: endpoint.relation}
}

// combatRow describes one stored result.
func (m *encounterModel) combatRow(event analysisEvent) EventRow {
	row := EventRow{Key: "c" + strconv.FormatInt(event.sequence, 10), Kind: EventDamage, Flow: string(event.flow), Amount: event.amount,
		SkillID: event.skill, SkillRow: m.skillRowKey(event), Confidence: event.confidence, Downgraded: event.downgrade != ""}
	if event.rel >= 0 {
		at := nsToMs(event.rel)
		row.AtMs = &at
	}
	var source, target ActorRef
	switch {
	case event.healing:
		row.Kind = EventHealing
		applied := event.applied
		row.Effective = &applied
		healer := -1
		if index, ok := m.byKey[roleKey(nullUint(event.source.player), nullUint(event.source.role))]; ok && event.source.role.Valid {
			healer = index
		}
		target := -1
		if index, ok := m.byKey[roleKey(nullUint(event.target.player), nullUint(event.target.role))]; ok && event.target.role.Valid {
			target = index
		}
		source, targetActor := m.endpointActor(event.source, healer, true), m.endpointActor(event.target, target, true)
		row.Source, row.Target = &source, &targetActor
		return row
	case event.flow == capture.CombatFlowLocalOutgoing:
		source = m.endpointActor(event.source, event.contributor, true)
		target = m.endpointActor(event.target, -1, false)
	case event.flow == capture.CombatFlowLocalIncoming:
		source = m.endpointActor(event.source, -1, false)
		target = m.endpointActor(event.target, event.taken, true)
	default:
		source, target = ActorRef{Kind: ActorUnknown, Relation: event.source.relation}, ActorRef{Kind: ActorUnknown, Relation: event.target.relation}
	}
	row.Source, row.Target = &source, &target
	return row
}

func effectRow(fact windowFact, start int64) EventRow {
	at := nsToMs(fact.fact.at - start)
	row := EventRow{Key: "e" + strconv.FormatInt(fact.fact.sequence, 10), AtMs: &at, Kind: EventEffect, EffectRow: fact.instance.row.key,
		Change: fact.fact.change, Observation: fact.fact.observation, Evidence: fact.fact.evidence, Reason: fact.fact.reason}
	if fact.instance.effectID.Valid {
		id := fact.instance.effectID.Int64
		row.EffectID = &id
	}
	if fact.fact.stack.Valid {
		stack := fact.fact.stack.Int64
		row.Stack = &stack
	}
	holder, source := fact.instance.holder, fact.instance.source
	row.Target, row.Source = &holder, &source
	return row
}

// matchesContributor applies the contributor filter to a result: what it dealt, what it took and what it healed.
func (m *encounterModel) matchesContributor(q resolvedQuery, event analysisEvent) bool {
	if q.contributor < 0 {
		return true
	}
	if event.contributor == q.contributor || event.taken == q.contributor {
		return true
	}
	if event.healing && event.source.role.Valid {
		index, ok := m.byKey[roleKey(nullUint(event.source.player), nullUint(event.source.role))]
		return ok && index == q.contributor
	}
	return false
}

// Events lists one page of a finished encounter's stored events for the browser.
func (s *Store) Events(ctx context.Context, id int64, query EventQuery, masters *capture.MasterData) (EventPage, error) {
	page := EventPage{Rows: []EventRow{}}
	model, err := s.model(ctx, id, masters)
	var state errAnalysisState
	if errors.As(err, &state) {
		page.State = state.state
		return page, nil
	}
	if err != nil {
		return page, err
	}
	q := model.resolve(query.AnalysisQuery)
	kinds := map[string]bool{}
	for _, kind := range query.Kinds {
		kinds[strings.TrimSpace(kind)] = true
	}
	want := func(kind string) bool { return len(kinds) == 0 || kinds[kind] }
	page.State, page.Offset, page.Limit = AnalysisReady, max(0, query.Offset), query.Limit
	if page.Limit <= 0 || page.Limit > MaxEventPage {
		page.Limit = MaxEventPage
	}
	duration := int64(model.duration)
	start := model.start.UnixNano()
	// The two lists are merged in processing order: an effect change follows the combat result journaled just before it.
	type entry struct {
		combat   int
		effect   int
		order    int64
		kind     int
		sequence int64
	}
	var entries []entry
	// A correlated list holds the selected hits of one class against one effect row, and nothing else.
	correlated := query.CorrelateEffect != ""
	var timeline rowTimeline
	if correlated {
		row := (*effectRowModel)(nil)
		if model.effects != nil && !model.effects.truncated {
			row = model.effects.byKey[query.CorrelateEffect]
		}
		if row == nil {
			page.State = AnalysisReady
			return page, nil
		}
		timeline = model.rowTimeline(row)
		for index, event := range model.events {
			if !model.selectedHit(q, event) || event.rel < 0 {
				continue
			}
			if class, _ := model.correlationOf(timeline, event); class != query.Correlation {
				continue
			}
			entries = append(entries, entry{combat: index, effect: -1, order: event.sequence, sequence: event.sequence})
		}
	}
	if query.EffectRow == "" && !correlated {
		for index, event := range model.events {
			kind := EventDamage
			if event.healing {
				kind = EventHealing
			}
			// Healing belongs to no enemy, so an enemy filter leaves it out.
			if !want(kind) || !q.inRange(event, duration) || event.healing && q.Enemy >= 0 || !event.healing && !q.matchesEnemy(event) || !model.matchesContributor(q, event) {
				continue
			}
			if q.SkillRow != "" && model.skillRowKey(event) != q.SkillRow {
				continue
			}
			entries = append(entries, entry{combat: index, effect: -1, order: event.sequence, sequence: event.sequence})
		}
	}
	if model.effects != nil && !model.effects.truncated && want(EventEffect) && q.SkillRow == "" && !correlated {
		page.EffectsAvailable = true
		from, to := start+q.from, start+q.to
		for index, fact := range model.effects.window {
			row := fact.instance.row
			if query.EffectRow != "" && row.key != query.EffectRow || query.EffectRow == "" && q.Effects == "hud" && !row.hud || !q.rowMatches(row) {
				continue
			}
			if !q.full && (fact.fact.at < from || fact.fact.at >= to && !(q.to == duration && fact.fact.at == to)) {
				continue
			}
			entries = append(entries, entry{combat: -1, effect: index, order: fact.fact.afterCombat, kind: 1, sequence: fact.fact.sequence})
		}
	} else if model.effects != nil && !model.effects.truncated {
		page.EffectsAvailable = true
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.order != b.order {
			return a.order < b.order
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.sequence < b.sequence
	})
	page.Total = len(entries)
	for i := page.Offset; i < len(entries) && len(page.Rows) < page.Limit; i++ {
		if entries[i].combat >= 0 {
			row := model.combatRow(model.events[entries[i].combat])
			if correlated {
				row.Correlation, row.CorrelationReason = model.correlationOf(timeline, model.events[entries[i].combat])
			}
			page.Rows = append(page.Rows, row)
		} else {
			page.Rows = append(page.Rows, effectRow(model.effects.window[entries[i].effect], start))
		}
	}
	return page, nil
}
