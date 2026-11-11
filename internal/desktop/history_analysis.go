package desktop

import (
	"context"
	"encoding/json"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// The analysis workspace of the History view: one stored encounter by time range, contributor and enemy. The history
// package rebuilds every number from the stored results with the engine's rules; this projects them like a live encounter
// and names roles, enemies, skills and effects in the UI locale. The view only lays out what these return.

// historyAnalysisLimit bounds one analysis query; the first one of an encounter loads its model.
const historyAnalysisLimit = 15 * time.Second

func (b *Bridge) analysisQuery() (*nameProjection, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), historyAnalysisLimit)
	locale := b.catalog.Languages[b.settings.current().Settings.Locale].MasterLocale
	return &nameProjection{ctx: ctx, resolver: b.projection.resolver, locale: locale}, cancel
}

// historyActorJSON is an endpoint with its name in the UI locale: a role's VERIFIED name, or an enemy's or other non-player
// entity's Master name (CORRELATED at most); UNKNOWN without one.
type historyActorJSON struct {
	history.ActorRef
	Name historyName `json:"name"`
}

func (n *nameProjection) actor(ref history.ActorRef) historyActorJSON {
	out := historyActorJSON{ActorRef: ref, Name: historyName{Status: "UNKNOWN"}}
	switch {
	case ref.RoleID != 0 && (ref.Kind == history.ActorRole || ref.Kind == history.ActorSummon):
		if name := n.resolve("role", ref.RoleID); name.Name != "" {
			out.Name = historyName{Name: name.Name, Status: name.Status}
		}
	case ref.MonsterID != 0 || ref.TemplateID != 0:
		out.Name = n.enemy(ref.MonsterID, ref.TemplateID)
	}
	return out
}

func (n *nameProjection) actors(refs []history.ActorRef) []historyActorJSON {
	out := make([]historyActorJSON, 0, len(refs))
	for _, ref := range refs {
		out = append(out, n.actor(ref))
	}
	return out
}

// HistoryAnalysis answers one analysis query of a finished stored encounter: the range rebuilt and projected as the
// Dashboard projects an encounter, the DPS timeline with the enemy filter, the selected contributor's numbers, skill hit
// statistics, enemy flows, effect coverage and capture gaps.
func (b *Bridge) HistoryAnalysis(id int64, query history.AnalysisQuery) (json.RawMessage, error) {
	names, cancel := b.analysisQuery()
	defer cancel()
	analysis, err := b.history.Analysis(names.ctx, id, query, b.masters)
	if err != nil {
		return nil, err
	}
	out := struct {
		history.EncounterAnalysis
		Encounter *capture.ViewEncounter `json:"encounter"`
	}{EncounterAnalysis: analysis}
	if analysis.Snapshot != nil {
		view := b.masters.ProjectEncounter(*analysis.Snapshot)
		names.encounter(&view)
		out.Encounter = &view
	}
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(out)
}

type historyEffectRowJSON struct {
	history.EffectRow
	// Name is the effect's glossary term (CORRELATED at most); UNKNOWN keeps the effect ID as its only identity.
	Name    historyName        `json:"name"`
	Holder  historyActorJSON   `json:"holder"`
	Sources []historyActorJSON `json:"sources"`
}

// HistoryEffects lists the effect rows of a finished stored encounter for the scope and filters of the query, each with its
// intervals and how each part is known.
func (b *Bridge) HistoryEffects(id int64, query history.AnalysisQuery) (json.RawMessage, error) {
	names, cancel := b.analysisQuery()
	defer cancel()
	lanes, err := b.history.Effects(names.ctx, id, query, b.masters)
	if err != nil {
		return nil, err
	}
	rows := make([]historyEffectRowJSON, 0, len(lanes.Rows))
	for _, row := range lanes.Rows {
		named := historyEffectRowJSON{EffectRow: row, Name: historyName{Status: "UNKNOWN"}, Holder: names.actor(row.Holder), Sources: names.actors(row.Sources)}
		if row.TermID != 0 && (row.TermStatus == "VERIFIED" || row.TermStatus == "CORRELATED") {
			if term := names.resolve("term", row.TermID); term.Name != "" {
				named.Name = historyName{Name: term.Name, Status: "CORRELATED"}
			}
		}
		rows = append(rows, named)
	}
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(struct {
		history.EffectLanes
		Rows []historyEffectRowJSON `json:"rows"`
	}{lanes, rows})
}

type historyEventRowJSON struct {
	history.EventRow
	Source *historyActorJSON `json:"source,omitempty"`
	Target *historyActorJSON `json:"target,omitempty"`
}

// HistoryEvents returns one page of a finished stored encounter's combat results and effect changes for the event browser.
func (b *Bridge) HistoryEvents(id int64, query history.EventQuery) (json.RawMessage, error) {
	names, cancel := b.analysisQuery()
	defer cancel()
	page, err := b.history.Events(names.ctx, id, query, b.masters)
	if err != nil {
		return nil, err
	}
	rows := make([]historyEventRowJSON, 0, len(page.Rows))
	for _, row := range page.Rows {
		named := historyEventRowJSON{EventRow: row}
		if row.Source != nil {
			actor := names.actor(*row.Source)
			named.Source = &actor
		}
		if row.Target != nil {
			actor := names.actor(*row.Target)
			named.Target = &actor
		}
		rows = append(rows, named)
	}
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(struct {
		history.EventPage
		Rows []historyEventRowJSON `json:"rows"`
	}{page, rows})
}
