package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// The History Library in the host: the history package filters stored encounters by stored values; the host turns the
// filters that need the Master (mode, stage, character, text) into the instance and role IDs those values are, and names
// what the page shows in the UI locale. A trial role is the role it is a trial of (TrialRoleInfo.ParentId), as everywhere
// in the UI; the stored runtime RoleId is kept beside it.

// HistoryLibraryQuery is the library query of the view: the stored-value filters, and the filters the host resolves.
type HistoryLibraryQuery struct {
	history.LibraryQuery
	// Modes are mode keys of the facets: "open_world", "category:" and the category name, or "content:" and the content
	// system of a mode the game names no system for.
	Modes []string `json:"modes,omitempty"`
	// Stages are instance IDs.
	Stages []uint64 `json:"stages,omitempty"`
	// Roles are static RoleIds (a trial role counts as its parent).
	Roles []uint64 `json:"roles,omitempty"`
	// Text is matched, case-insensitively, against the Master's mode, stage, content and character names in the UI locale
	// (the open world and a content system without a game name are UI labels, not Master names: they are chosen as a mode).
	Text string `json:"text,omitempty"`
}

type historyLibraryRoleJSON struct {
	RoleID       uint64      `json:"roleId"`
	StaticRoleID uint64      `json:"staticRoleId"`
	ElementID    uint64      `json:"elementId,omitempty"`
	Name         historyName `json:"name"`
	Damage       int64       `json:"damage"`
}

type historyLibraryEntryJSON struct {
	historySummaryJSON
	Class   string                   `json:"class"`
	Roles   []historyLibraryRoleJSON `json:"roles"`
	Vehicle bool                     `json:"vehicle,omitempty"`
}

type historyModeFacetJSON struct {
	Key    string                   `json:"key"`
	Kind   capture.InstanceModeKind `json:"kind"`
	Name   string                   `json:"name,omitempty"`
	Status string                   `json:"status"`
	Count  int                      `json:"count"`
}

type historyStageFacetJSON struct {
	InstanceID uint64          `json:"instanceId"`
	Name       historyName     `json:"name"`
	Content    *historyContent `json:"content,omitempty"`
	Count      int             `json:"count"`
}

type historyRoleFacetJSON struct {
	RoleID    uint64      `json:"roleId"`
	ElementID uint64      `json:"elementId,omitempty"`
	Name      historyName `json:"name"`
	Count     int         `json:"count"`
}

// instanceNames is what the host knows about one stored scene instance.
type instanceNames struct {
	modeKey string
	mode    historyMode
	stage   historyName
	content *historyContent
}

// modeKey is the facet key of a mode: open world, a named category, a content system without a game name, or "" without
// a mode.
func modeKey(mode historyMode) string {
	switch {
	case mode.Kind == capture.InstanceModeOpenWorld:
		return "open_world"
	case mode.Kind == capture.InstanceModeCategory && mode.Name != "":
		return "category:" + mode.Name
	case mode.Kind == capture.InstanceModeContentSystem && mode.System != "":
		return "content:" + mode.System
	}
	return ""
}

// instanceNames names one stored instance in the UI locale. A name is VERIFIED only for the game version of the embedded
// Master; the library and the career mix versions, so names there are capped at CORRELATED.
func (n *nameProjection) instanceNames(id uint64) instanceNames {
	mode := n.mode(id, false)
	return instanceNames{modeKey: modeKey(mode), mode: mode, stage: n.stage(id, false), content: n.content(id, false)}
}

// instances names every instance of a facet count.
func (n *nameProjection) instances(counts map[uint64]int) map[uint64]instanceNames {
	out := make(map[uint64]instanceNames, len(counts))
	for id := range counts {
		out[id] = n.instanceNames(id)
	}
	return out
}

// staticRoles maps every runtime RoleId of a facet count to the role it shows as.
func (b *Bridge) staticRoles(ctx context.Context, counts map[uint64]int) map[uint64]uint64 {
	out := make(map[uint64]uint64, len(counts))
	resolver := b.projection.resolver
	for id := range counts {
		static := id
		if resolver != nil {
			if parent := resolver.StaticRole(ctx, id); parent != 0 {
				static = parent
			}
		}
		out[id] = static
	}
	return out
}

func (n *nameProjection) roleName(staticRole uint64) historyName {
	if staticRole == 0 {
		return historyName{Status: "UNKNOWN"}
	}
	if name := n.resolve("role", staticRole); name.Name != "" {
		return historyName{Name: name.Name, Status: name.Status}
	}
	return historyName{Status: "UNKNOWN"}
}

// HistoryLibrary returns one page of the stored encounters the query selects, across dates, in the view's time zone, with
// the facets the filters offer.
func (b *Bridge) HistoryLibrary(query HistoryLibraryQuery, zoneName string) (json.RawMessage, error) {
	names, cancel := b.historyQuery()
	defer cancel()
	zone := history.ResolveViewZone(zoneName)
	// The first pass reads the facets of the dated selection, which every Master-resolved filter is resolved against.
	probe := query.LibraryQuery
	probe.Instances, probe.RoleIDs, probe.MatchInstances, probe.MatchRoleIDs, probe.Limit, probe.Offset = nil, nil, nil, nil, 1, 0
	base, err := b.history.Library(names.ctx, probe, zone.Location)
	if err != nil && !errors.Is(err, history.ErrUnavailable) {
		return nil, err
	}
	instances := names.instances(base.Facets.Instances)
	static := b.staticRoles(names.ctx, base.Facets.Roles)
	resolved := resolveLibraryFilters(query, instances, static, func(role uint64) string { return names.roleName(role).Name })
	view, err := b.history.Library(names.ctx, resolved, zone.Location)
	if err != nil && !errors.Is(err, history.ErrUnavailable) {
		return nil, err
	}
	entries := make([]historyLibraryEntryJSON, 0, len(view.Encounters))
	for _, entry := range view.Encounters {
		out := historyLibraryEntryJSON{historySummaryJSON: names.summary(entry.EncounterSummary), Class: entry.Class, Roles: []historyLibraryRoleJSON{}, Vehicle: entry.Vehicle}
		for _, role := range entry.Roles {
			parent := static[role.RoleID]
			if parent == 0 {
				parent = role.RoleID
			}
			out.Roles = append(out.Roles, historyLibraryRoleJSON{RoleID: role.RoleID, StaticRoleID: parent, ElementID: b.roleElement(names.ctx, parent), Name: names.roleName(parent), Damage: role.Damage})
		}
		entries = append(entries, out)
	}
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(struct {
		Status     history.Status            `json:"status"`
		Zone       historyZoneJSON           `json:"zone"`
		Total      int                       `json:"total"`
		Offset     int                       `json:"offset"`
		Encounters []historyLibraryEntryJSON `json:"encounters"`
		Facets     any                       `json:"facets"`
	}{b.history.Status(), zoneJSON(zone), view.Total, view.Offset, entries, libraryFacets(base.Facets, instances, static, names, b, names.ctx)})
}

// resolveLibraryFilters turns the Master-backed filters of a query into the stored IDs they select: instances by mode and
// stage, runtime RoleIds by the static role they show as, and a text search over mode, stage and role names.
func resolveLibraryFilters(query HistoryLibraryQuery, instances map[uint64]instanceNames, static map[uint64]uint64, roleName func(uint64) string) history.LibraryQuery {
	resolved := query.LibraryQuery
	if len(query.Modes) > 0 || len(query.Stages) > 0 {
		resolved.Instances = []uint64{}
		for id, known := range instances {
			if len(query.Modes) > 0 && !containsKey(query.Modes, known.modeKey) {
				continue
			}
			if len(query.Stages) > 0 && !containsID(query.Stages, id) {
				continue
			}
			resolved.Instances = append(resolved.Instances, id)
		}
	}
	if len(query.Roles) > 0 {
		resolved.RoleIDs = []uint64{}
		for runtime, role := range static {
			if containsID(query.Roles, role) {
				resolved.RoleIDs = append(resolved.RoleIDs, runtime)
			}
		}
	}
	if text := strings.ToLower(strings.TrimSpace(query.Text)); text != "" {
		resolved.MatchInstances, resolved.MatchRoleIDs = []uint64{}, []uint64{}
		for id, known := range instances {
			match := strings.Contains(strings.ToLower(known.mode.Name), text) || strings.Contains(strings.ToLower(known.stage.Name), text)
			for _, name := range known.content.names() {
				match = match || strings.Contains(strings.ToLower(name), text)
			}
			if match {
				resolved.MatchInstances = append(resolved.MatchInstances, id)
			}
		}
		for runtime, role := range static {
			if name := roleName(role); name != "" && strings.Contains(strings.ToLower(name), text) {
				resolved.MatchRoleIDs = append(resolved.MatchRoleIDs, runtime)
			}
		}
	}
	return resolved
}

func libraryFacets(facets history.LibraryFacets, instances map[uint64]instanceNames, static map[uint64]uint64, names *nameProjection, b *Bridge, ctx context.Context) any {
	modes := map[string]*historyModeFacetJSON{}
	stages := make([]historyStageFacetJSON, 0, len(facets.Instances))
	for id, count := range facets.Instances {
		known := instances[id]
		stages = append(stages, historyStageFacetJSON{InstanceID: id, Name: known.stage, Content: known.content, Count: count})
		if known.modeKey == "" {
			continue
		}
		if modes[known.modeKey] == nil {
			modes[known.modeKey] = &historyModeFacetJSON{Key: known.modeKey, Kind: known.mode.Kind, Name: known.mode.Name, Status: known.mode.Status}
		}
		modes[known.modeKey].Count += count
	}
	modeList := make([]historyModeFacetJSON, 0, len(modes))
	for _, mode := range modes {
		modeList = append(modeList, *mode)
	}
	sort.Slice(modeList, func(i, j int) bool {
		if modeList[i].Count != modeList[j].Count {
			return modeList[i].Count > modeList[j].Count
		}
		return modeList[i].Key < modeList[j].Key
	})
	sort.Slice(stages, func(i, j int) bool {
		if stages[i].Count != stages[j].Count {
			return stages[i].Count > stages[j].Count
		}
		return stages[i].InstanceID < stages[j].InstanceID
	})
	roleCounts := map[uint64]int{}
	for runtime, count := range facets.Roles {
		roleCounts[static[runtime]] += count
	}
	roles := make([]historyRoleFacetJSON, 0, len(roleCounts))
	for role, count := range roleCounts {
		roles = append(roles, historyRoleFacetJSON{RoleID: role, ElementID: b.roleElement(ctx, role), Name: names.roleName(role), Count: count})
	}
	sort.Slice(roles, func(i, j int) bool {
		if roles[i].Count != roles[j].Count {
			return roles[i].Count > roles[j].Count
		}
		return roles[i].RoleID < roles[j].RoleID
	})
	return struct {
		GameVersions []history.LibraryCount  `json:"gameVersions"`
		Classes      []history.LibraryCount  `json:"classes"`
		Sources      []history.LibraryCount  `json:"sources"`
		Modes        []historyModeFacetJSON  `json:"modes"`
		Stages       []historyStageFacetJSON `json:"stages"`
		Roles        []historyRoleFacetJSON  `json:"roles"`
		FirstDate    string                  `json:"firstDate,omitempty"`
		LastDate     string                  `json:"lastDate,omitempty"`
	}{facets.GameVersions, facets.Classes, facets.Sources, modeList, stages, roles, facets.FirstDate, facets.LastDate}
}

func (b *Bridge) roleElement(ctx context.Context, role uint64) uint64 {
	if b.projection.resolver == nil || role == 0 {
		return 0
	}
	return b.projection.resolver.RoleElement(ctx, role)
}

func containsKey(values []string, value string) bool {
	for _, v := range values {
		if v == value && value != "" {
			return true
		}
	}
	return false
}

func containsID(values []uint64, value uint64) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
