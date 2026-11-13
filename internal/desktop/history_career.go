package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"wwlogs/internal/history"
)

// The Career Overview in the host: the history package computes every number (career.go); the host turns the mode and
// stage filters into stored instance IDs, counts a trial role as its parent, groups instances into modes, and names roles,
// skills, modes and stages in the UI locale.

// HistoryCareerQuery is the career query of the view.
type HistoryCareerQuery struct {
	history.CareerQuery
	// Modes are mode keys ("open_world" or "category:" and the name); Stages are instance IDs.
	Modes  []string `json:"modes,omitempty"`
	Stages []uint64 `json:"stages,omitempty"`
}

type careerRecordJSON struct {
	history.CareerRecord
	RoleName  *historyName `json:"roleName,omitempty"`
	ElementID uint64       `json:"elementId,omitempty"`
	SkillName *historyName `json:"skillName,omitempty"`
	Mode      historyMode  `json:"mode"`
	Stage     historyName  `json:"stage"`
}

type careerRoleJSON struct {
	history.CareerRole
	Name       historyName       `json:"name"`
	ElementID  uint64            `json:"elementId,omitempty"`
	BestDPS    *careerRecordJSON `json:"bestDps,omitempty"`
	BestDamage *careerRecordJSON `json:"bestDamage,omitempty"`
	MaxHit     *careerRecordJSON `json:"maxHit,omitempty"`
}

type careerComboRoleJSON struct {
	RoleID    uint64      `json:"roleId"`
	ElementID uint64      `json:"elementId,omitempty"`
	Name      historyName `json:"name"`
}

type careerComboJSON struct {
	Roles      []careerComboRoleJSON `json:"roles"`
	Encounters int                   `json:"encounters"`
	Damage     int64                 `json:"damage"`
	BestDPS    *careerRecordJSON     `json:"bestDps,omitempty"`
}

type careerPlaceJSON struct {
	Key        string            `json:"key"`
	InstanceID uint64            `json:"instanceId,omitempty"`
	Mode       historyMode       `json:"mode"`
	Stage      historyName       `json:"stage"`
	Encounters int               `json:"encounters"`
	Counted    int               `json:"counted"`
	Damage     int64             `json:"damage"`
	BestDPS    *careerRecordJSON `json:"bestDps,omitempty"`
}

// careerNames names the numbers of one career answer.
type careerNames struct {
	names     *nameProjection
	bridge    *Bridge
	ctx       context.Context
	instances map[uint64]instanceNames
	elements  map[uint64]uint64
}

func (c *careerNames) element(role uint64) uint64 {
	if element, ok := c.elements[role]; ok {
		return element
	}
	element := c.bridge.roleElement(c.ctx, role)
	c.elements[role] = element
	return element
}

func (c *careerNames) place(instance uint64) (historyMode, historyName) {
	if instance == 0 {
		return historyMode{Status: "UNKNOWN"}, historyName{Status: "UNKNOWN"}
	}
	known, ok := c.instances[instance]
	if !ok {
		known = instanceNames{mode: c.names.mode(instance, false), stage: c.names.stage(instance, false)}
		known.modeKey = modeKey(known.mode)
		c.instances[instance] = known
	}
	return known.mode, known.stage
}

func (c *careerNames) record(record *history.CareerRecord) *careerRecordJSON {
	if record == nil {
		return nil
	}
	out := &careerRecordJSON{CareerRecord: *record}
	if record.RoleID != 0 {
		name := c.names.roleName(record.RoleID)
		out.RoleName, out.ElementID = &name, c.element(record.RoleID)
	}
	if record.SkillID != 0 {
		name := historyName{Status: "UNKNOWN"}
		if resolved := c.names.resolve("skill", record.SkillID); resolved.Name != "" {
			name = historyName{Name: resolved.Name, Status: resolved.Status}
		}
		out.SkillName = &name
	}
	out.Mode, out.Stage = c.place(record.Instance)
	return out
}

// HistoryCareer returns the Career Overview of the selection in the view's time zone, named in the UI locale.
func (b *Bridge) HistoryCareer(query HistoryCareerQuery, zoneName string) (json.RawMessage, error) {
	names, cancel := b.historyQuery()
	defer cancel()
	zone := history.ResolveViewZone(zoneName)
	// The instances of the dated selection are the ones a mode or stage filter can name.
	probe, err := b.history.Library(names.ctx, history.LibraryQuery{From: query.From, To: query.To, GameVersions: query.GameVersions, Limit: 1}, zone.Location)
	if err != nil && !errors.Is(err, history.ErrUnavailable) {
		return nil, err
	}
	instances := names.instances(probe.Facets.Instances)
	resolved := query.CareerQuery
	if len(query.Modes) > 0 || len(query.Stages) > 0 {
		filter := resolveLibraryFilters(HistoryLibraryQuery{Modes: query.Modes, Stages: query.Stages}, instances, nil, func(uint64) string { return "" })
		resolved.Instances = filter.Instances
	}
	static := map[uint64]uint64{}
	staticRole := func(runtime uint64) uint64 {
		if role, ok := static[runtime]; ok {
			return role
		}
		role := runtime
		if b.projection.resolver != nil {
			if parent := b.projection.resolver.StaticRole(names.ctx, runtime); parent != 0 {
				role = parent
			}
		}
		static[runtime] = role
		return role
	}
	view, err := b.history.Career(names.ctx, resolved, zone.Location, staticRole)
	if err != nil && !errors.Is(err, history.ErrUnavailable) {
		return nil, err
	}
	c := &careerNames{names: names, bridge: b, ctx: names.ctx, instances: instances, elements: map[uint64]uint64{}}
	roles := make([]careerRoleJSON, 0, len(view.Roles))
	for _, role := range view.Roles {
		roles = append(roles, careerRoleJSON{CareerRole: role, Name: names.roleName(role.RoleID), ElementID: c.element(role.RoleID),
			BestDPS: c.record(role.BestDPS), BestDamage: c.record(role.BestDamage), MaxHit: c.record(role.MaxHit)})
	}
	combos := make([]careerComboJSON, 0, len(view.Combos))
	for _, combo := range view.Combos {
		out := careerComboJSON{Roles: make([]careerComboRoleJSON, 0, len(combo.Roles)), Encounters: combo.Encounters, Damage: combo.Damage, BestDPS: c.record(combo.BestDPS)}
		for _, role := range combo.Roles {
			out.Roles = append(out.Roles, careerComboRoleJSON{RoleID: role, ElementID: c.element(role), Name: names.roleName(role)})
		}
		combos = append(combos, out)
	}
	stages := make([]careerPlaceJSON, 0, len(view.Instances))
	modes := map[string]*careerPlaceJSON{}
	for _, instance := range view.Instances {
		mode, stage := c.place(instance.Instance)
		best := c.record(instance.BestDPS)
		stages = append(stages, careerPlaceJSON{Key: modeKey(mode), InstanceID: instance.Instance, Mode: mode, Stage: stage, Encounters: instance.Encounters, Counted: instance.Counted, Damage: instance.Damage, BestDPS: best})
		key := modeKey(mode)
		if key == "" {
			key = "unknown"
		}
		group := modes[key]
		if group == nil {
			group = &careerPlaceJSON{Key: key, Mode: mode, Stage: historyName{Status: "UNKNOWN"}}
			modes[key] = group
		}
		group.Encounters += instance.Encounters
		group.Counted += instance.Counted
		group.Damage += instance.Damage
		if best != nil && (group.BestDPS == nil || best.Value > group.BestDPS.Value) {
			group.BestDPS = best
		}
	}
	modeList := make([]careerPlaceJSON, 0, len(modes))
	for _, mode := range modes {
		modeList = append(modeList, *mode)
	}
	sort.Slice(modeList, func(i, j int) bool {
		if modeList[i].Encounters != modeList[j].Encounters {
			return modeList[i].Encounters > modeList[j].Encounters
		}
		return modeList[i].Key < modeList[j].Key
	})
	// The filters offer every mode and stage of the dated selection, whatever the current filter keeps.
	filterModes := map[string]historyModeFacetJSON{}
	filterStages := make([]historyStageFacetJSON, 0, len(instances))
	for id, known := range instances {
		filterStages = append(filterStages, historyStageFacetJSON{InstanceID: id, Name: known.stage, Count: probe.Facets.Instances[id]})
		if known.modeKey == "" {
			continue
		}
		facet := filterModes[known.modeKey]
		facet.Key, facet.Kind, facet.Name, facet.Status = known.modeKey, known.mode.Kind, known.mode.Name, known.mode.Status
		facet.Count += probe.Facets.Instances[id]
		filterModes[known.modeKey] = facet
	}
	modeFacets := make([]historyModeFacetJSON, 0, len(filterModes))
	for _, facet := range filterModes {
		modeFacets = append(modeFacets, facet)
	}
	sort.Slice(modeFacets, func(i, j int) bool {
		return modeFacets[i].Count > modeFacets[j].Count || modeFacets[i].Count == modeFacets[j].Count && modeFacets[i].Key < modeFacets[j].Key
	})
	sort.Slice(filterStages, func(i, j int) bool {
		return filterStages[i].Count > filterStages[j].Count || filterStages[i].Count == filterStages[j].Count && filterStages[i].InstanceID < filterStages[j].InstanceID
	})
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(struct {
		Status   history.Status       `json:"status"`
		Zone     historyZoneJSON      `json:"zone"`
		Today    string               `json:"today"`
		Totals   history.CareerTotals `json:"totals"`
		Days     []history.CareerDay  `json:"days"`
		Roles    []careerRoleJSON     `json:"roles"`
		Combos   []careerComboJSON    `json:"combos"`
		Modes    []careerPlaceJSON    `json:"modes"`
		Stages   []careerPlaceJSON    `json:"stages"`
		Records  map[string]any       `json:"records"`
		Versions []string             `json:"versions"`
		Filters  map[string]any       `json:"filters"`
	}{
		Status: b.history.Status(), Zone: zoneJSON(zone), Today: view.Today, Totals: view.Totals, Days: view.Days, Roles: roles, Combos: combos, Modes: modeList, Stages: stages,
		Records:  map[string]any{"maxHit": c.record(view.Records.MaxHit), "bestDps": c.record(view.Records.BestDPS), "bestDamage": c.record(view.Records.BestDamage), "bestPeak": c.record(view.Records.BestPeak)},
		Versions: view.Versions, Filters: map[string]any{"modes": modeFacets, "stages": filterStages},
	})
}
