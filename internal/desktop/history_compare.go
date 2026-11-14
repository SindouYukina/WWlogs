package desktop

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// The Encounter Comparison: two to four stored encounters side by side, for one player's own analysis. Each encounter is
// read and projected exactly as its History detail is (the same names, shares and DPS). Go then lines up contributors by
// the role they show as and skills by SkillId, states which conditions the encounters share, and gives the change from the
// first encounter only between complete records. Conditions are stated, never weighed into a score.

// maxCompared bounds a comparison: four value columns beside their labels still fit the Desktop window's width.
const maxCompared = 4

var errCompareCount = errors.New("a comparison needs 2 to 4 distinct stored encounters")

// Value statuses of a compared value.
const (
	compareAvailable   = "available"   // a complete result
	compareLowerBound  = "lower_bound" // degraded or known_affected: the true value may be higher
	comparePartial     = "partial"     // an incomplete or unfinished record: not the whole result
	compareUnreliable  = "unreliable"  // the boundaries or the extent of a loss are unknown
	compareUnavailable = "unavailable" // no value
)

type compareInput struct {
	summary historySummaryJSON
	class   string
	// view is the projected stored result (a checkpoint for an unfinished encounter); nil when the encounter stored none.
	view *capture.ViewEncounter
	// olderAccounting is set when the recording build did not count server-executed damage
	// (history.EncounterSummary.NotifiedDamageCounted): the stored values of a complete record are then lower bounds.
	olderAccounting bool
}

// status is recordStatus of the input's class, a lower bound for a complete record of an older accounting.
func (input compareInput) status() string {
	status := recordStatus(input.class)
	if status == compareAvailable && input.olderAccounting {
		return compareLowerBound
	}
	return status
}

// rate is rateStatus of the input, with the same lower bound for an older accounting.
func (input compareInput) rate(dps capture.ViewDPS) string {
	status := rateStatus(input.class, dps)
	if status == compareAvailable && input.olderAccounting {
		return compareLowerBound
	}
	return status
}

type compareValue struct {
	Value  *float64 `json:"value,omitempty"`
	Status string   `json:"status"`
	// Delta is the change from the first encounter as a ratio (0.12 is 12 % more), given only when both are complete
	// records with available values and the first is above zero.
	Delta *float64 `json:"delta,omitempty"`
}

type compareMetric struct {
	// Key is duration, damage, dps or peak_dps.
	Key    string         `json:"key"`
	Values []compareValue `json:"values"`
}

type compareContribution struct {
	Damage int64 `json:"damage"`
	// Share is of the encounter's confirmed damage; DPS is the contributor's damage over the encounter's DPS duration.
	Share  *float64 `json:"share,omitempty"`
	DPS    *float64 `json:"dps,omitempty"`
	Status string   `json:"status"`
	// Trial is set when the damage came from a trial role shown as its parent.
	Trial bool `json:"trial,omitempty"`
	// DPSDelta is the change of DPS from the first encounter, between complete records only.
	DPSDelta *float64 `json:"dpsDelta,omitempty"`
}

type compareSkillValue struct {
	Damage int64 `json:"damage"`
	// Share is of the contributor's damage in that encounter.
	Share *float64 `json:"share,omitempty"`
	Hits  uint64   `json:"hits"`
}

type compareSkill struct {
	// SkillID is the DamageContext.SkillId; the unassigned row has none.
	SkillID     uint64      `json:"skillId,omitempty"`
	Unassigned  bool        `json:"unassigned,omitempty"`
	Name        historyName `json:"name"`
	NameSource  string      `json:"nameSource,omitempty"`
	ShowSkillID bool        `json:"showSkillId,omitempty"`
	IconKind    string      `json:"iconKind,omitempty"`
	IconID      uint64      `json:"iconId,omitempty"`
	// Values has one entry per encounter; nil where the contributor dealt no damage with the skill.
	Values []*compareSkillValue `json:"values"`
}

type compareContributor struct {
	// Kind is role, vehicle or unassigned (confirmed local damage without a role or vehicle).
	Kind string `json:"kind"`
	// RoleID is the role the contributor shows as (a trial role's parent).
	RoleID    uint64      `json:"roleId,omitempty"`
	ElementID uint64      `json:"elementId,omitempty"`
	Name      historyName `json:"name"`
	// Values has one entry per encounter; nil where the contributor dealt no confirmed damage or no result was stored.
	Values []*compareContribution `json:"values"`
	Skills []compareSkill         `json:"skills"`
}

type compareCondition struct {
	// Key is game_version, mode, stage or main_enemy.
	Key string `json:"key"`
	// State is same, different or unknown (an encounter without the fact).
	State string `json:"state"`
}

type compareContext struct {
	Conditions []compareCondition `json:"conditions"`
	// OpenWorld is set when an encounter was fought in the open world, where enemies and conditions are not fixed.
	OpenWorld bool `json:"openWorld,omitempty"`
	// DurationRatio is the longest duration over the shortest, when every duration is known.
	DurationRatio *float64 `json:"durationRatio,omitempty"`
	// Reliable is set when every encounter is a complete record; only then is every change given.
	Reliable bool `json:"reliable"`
}

type compareEncounter struct {
	historySummaryJSON
	Class string `json:"class"`
	// Reliable is set for a complete record; Results when a result (or checkpoint) was stored.
	Reliable bool `json:"reliable"`
	Results  bool `json:"results"`
}

type comparison struct {
	Encounters   []compareEncounter   `json:"encounters"`
	Metrics      []compareMetric      `json:"metrics"`
	Contributors []compareContributor `json:"contributors"`
	Context      compareContext       `json:"context"`
}

// compareIDs checks a comparison's encounter IDs: two to maxCompared distinct IDs.
func compareIDs(ids []int64) error {
	if len(ids) < 2 || len(ids) > maxCompared {
		return errCompareCount
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return errCompareCount
		}
		seen[id] = true
	}
	return nil
}

// recordStatus is what a stored value of a record class is.
func recordStatus(class string) string {
	switch class {
	case history.ClassComplete:
		return compareAvailable
	case history.ClassDegraded, history.ClassKnownAffected:
		return compareLowerBound
	case history.ClassUnreliable:
		return compareUnreliable
	default:
		return comparePartial
	}
}

// rateStatus is what an engine rate (Encounter or peak DPS) of a record class is: the engine's own status, lowered by
// what the record class knows.
func rateStatus(class string, dps capture.ViewDPS) string {
	switch dps.Status {
	case capture.DPSStatusAvailable, capture.DPSStatusLowerBound:
	case capture.DPSStatusUnreliable:
		return compareUnreliable
	default:
		return compareUnavailable
	}
	record := recordStatus(class)
	if record != compareAvailable {
		return record
	}
	if dps.Status == capture.DPSStatusLowerBound {
		return compareLowerBound
	}
	return compareAvailable
}

func number(value float64) *float64 { return &value }

// withDeltas gives each value its change from the first one where both are complete records with available values.
func withDeltas(values []compareValue, reliable []bool) {
	if len(values) == 0 || !reliable[0] || values[0].Status != compareAvailable || values[0].Value == nil || *values[0].Value <= 0 {
		return
	}
	base := *values[0].Value
	for i := 1; i < len(values); i++ {
		if reliable[i] && values[i].Status == compareAvailable && values[i].Value != nil {
			values[i].Delta = number((*values[i].Value - base) / base)
		}
	}
}

func skillName(skill capture.ViewSkill) historyName {
	if skill.Name == "" {
		return historyName{Status: "UNKNOWN"}
	}
	return historyName{Name: skill.Name, Status: skill.NameStatus}
}

// contributorRows gathers one encounter's contributors: roles by the role they show as (a trial role and its parent in
// one encounter add up), the vehicle, and the unassigned local damage.
type contributorRow struct {
	kind      string
	roleID    uint64
	elementID uint64
	name      historyName
	value     compareContribution
	skills    map[uint64]*compareSkillValue
	named     map[uint64]capture.ViewSkill
	// unassigned is the contributor's damage without a SkillId.
	unassigned compareSkillValue
}

func contributorKey(kind string, role uint64) string {
	if kind == "role" {
		return "role:" + strconv.FormatUint(role, 10)
	}
	return kind
}

func addShare(total *float64, share *float64) *float64 {
	if share == nil {
		return total
	}
	if total == nil {
		return number(*share)
	}
	return number(*total + *share)
}

func encounterContributors(view *capture.ViewEncounter, status string) map[string]*contributorRow {
	rows := map[string]*contributorRow{}
	addSkills := func(row *contributorRow, skills []capture.ViewSkill, unassigned capture.ViewUnassignedSkill) {
		for _, skill := range skills {
			value := row.skills[skill.SkillID]
			if value == nil {
				value = &compareSkillValue{}
				row.skills[skill.SkillID] = value
			}
			value.Damage += skill.DamageDealt
			value.Hits += skill.OutgoingEvents
			if _, named := row.named[skill.SkillID]; !named || skill.NameStatus == "VERIFIED" {
				row.named[skill.SkillID] = skill
			}
		}
		row.unassigned.Damage += unassigned.DamageDealt
		row.unassigned.Hits += unassigned.OutgoingEvents
	}
	for _, role := range view.Roles {
		static := role.StaticRoleID
		if static == 0 {
			static = role.RoleID
		}
		key := contributorKey("role", static)
		row := rows[key]
		if row == nil {
			row = &contributorRow{kind: "role", roleID: static, elementID: role.ElementID, value: compareContribution{Status: status}, skills: map[uint64]*compareSkillValue{}, named: map[uint64]capture.ViewSkill{}}
			rows[key] = row
		}
		// The name a trial role shows is its parent's; a parent row met first keeps its own.
		if row.name.Name == "" || role.RoleID == static {
			row.name = historyName{Name: role.Name, Status: role.NameStatus}
			if role.Name == "" {
				row.name = historyName{Status: "UNKNOWN"}
			}
		}
		row.value.Damage += role.DamageDealt
		row.value.Share = addShare(row.value.Share, role.DamageShare)
		row.value.DPS = addShare(row.value.DPS, role.DPS)
		row.value.Trial = row.value.Trial || role.RoleID != static
		addSkills(row, role.Skills, role.UnassignedSkill)
	}
	if vehicle := view.Vehicle; vehicle != nil && vehicle.DamageDealt > 0 {
		row := &contributorRow{kind: "vehicle", name: historyName{Status: "UNKNOWN"}, value: compareContribution{Damage: vehicle.DamageDealt, Share: vehicle.DamageShare, DPS: vehicle.DPS, Status: status},
			skills: map[uint64]*compareSkillValue{}, named: map[uint64]capture.ViewSkill{}}
		addSkills(row, vehicle.Skills, vehicle.UnassignedSkill)
		rows["vehicle"] = row
	}
	if unassigned := view.UnassignedRole; unassigned.DamageDealt > 0 {
		rows["unassigned"] = &contributorRow{kind: "unassigned", name: historyName{Status: "UNKNOWN"}, value: compareContribution{Damage: unassigned.DamageDealt, Share: view.UnassignedRoleShare, Status: status},
			skills: map[uint64]*compareSkillValue{}, named: map[uint64]capture.ViewSkill{}}
	}
	for _, row := range rows {
		if row.value.Damage <= 0 {
			continue
		}
		for _, value := range row.skills {
			value.Share = number(float64(value.Damage) / float64(row.value.Damage))
		}
		if row.unassigned.Damage > 0 {
			row.unassigned.Share = number(float64(row.unassigned.Damage) / float64(row.value.Damage))
		}
	}
	return rows
}

// sameState says whether every encounter has the same known value (keys; "" is unknown).
func sameState(keys []string) string {
	for _, key := range keys {
		if key == "" {
			return "unknown"
		}
	}
	for _, key := range keys[1:] {
		if key != keys[0] {
			return "different"
		}
	}
	return "same"
}

func enemyKey(enemy *history.EnemyRef) string {
	switch {
	case enemy == nil || enemy.IdentityConflict:
		return ""
	case enemy.MonsterID != 0:
		return "monster:" + strconv.FormatUint(enemy.MonsterID, 10)
	case enemy.TemplateID != 0:
		return "template:" + strconv.FormatUint(enemy.TemplateID, 10)
	}
	return ""
}

// compareEncounters lines the encounters up, in the order given; the first is the one changes are measured from.
func compareEncounters(inputs []compareInput) comparison {
	out := comparison{Encounters: make([]compareEncounter, 0, len(inputs)), Metrics: []compareMetric{}, Contributors: []compareContributor{}}
	reliable := make([]bool, len(inputs))
	out.Context.Reliable = true
	for i, input := range inputs {
		reliable[i] = input.class == history.ClassComplete && input.view != nil && !input.olderAccounting
		out.Context.Reliable = out.Context.Reliable && reliable[i]
		out.Encounters = append(out.Encounters, compareEncounter{historySummaryJSON: input.summary, Class: input.class, Reliable: reliable[i], Results: input.view != nil})
	}

	duration := compareMetric{Key: "duration"}
	damage := compareMetric{Key: "damage"}
	dps := compareMetric{Key: "dps"}
	peak := compareMetric{Key: "peak_dps"}
	for _, input := range inputs {
		status := input.status()
		if seconds := input.summary.DurationSeconds; seconds != nil {
			value := compareValue{Value: number(*seconds), Status: compareAvailable}
			if status == comparePartial || status == compareUnreliable {
				value.Status = status
			}
			duration.Values = append(duration.Values, value)
		} else {
			duration.Values = append(duration.Values, compareValue{Status: compareUnavailable})
		}
		if input.view == nil {
			damage.Values = append(damage.Values, compareValue{Status: compareUnavailable})
			dps.Values = append(dps.Values, compareValue{Status: compareUnavailable})
			peak.Values = append(peak.Values, compareValue{Status: compareUnavailable})
			continue
		}
		damage.Values = append(damage.Values, compareValue{Value: number(float64(input.view.Totals.DamageDealt)), Status: status})
		for _, rate := range []struct {
			metric *compareMetric
			value  capture.ViewDPS
		}{{&dps, input.view.DPS}, {&peak, input.view.PeakDPS}} {
			status := input.rate(rate.value)
			value := compareValue{Status: status}
			if status != compareUnavailable && status != compareUnreliable {
				value.Value = number(rate.value.Value)
			}
			rate.metric.Values = append(rate.metric.Values, value)
		}
	}
	for _, metric := range []*compareMetric{&duration, &damage, &dps, &peak} {
		withDeltas(metric.Values, reliable)
		out.Metrics = append(out.Metrics, *metric)
	}

	// Contributors across the encounters, by the role they show as.
	perEncounter := make([]map[string]*contributorRow, len(inputs))
	order := map[string]int{}
	var keys []string
	for i, input := range inputs {
		if input.view == nil {
			continue
		}
		perEncounter[i] = encounterContributors(input.view, input.status())
		for key := range perEncounter[i] {
			if _, known := order[key]; !known {
				order[key] = len(keys)
				keys = append(keys, key)
			}
		}
	}
	for _, key := range keys {
		contributor := compareContributor{Values: make([]*compareContribution, len(inputs)), Skills: []compareSkill{}}
		skillRows := map[uint64]*compareSkill{}
		var skillOrder []uint64
		unassigned := compareSkill{Unassigned: true, Name: historyName{Status: "UNKNOWN"}, Values: make([]*compareSkillValue, len(inputs))}
		hasUnassigned := false
		for i, rows := range perEncounter {
			row := rows[key]
			if row == nil {
				continue
			}
			if contributor.Kind == "" || (contributor.Name.Name == "" && row.name.Name != "") {
				contributor.Kind, contributor.RoleID, contributor.ElementID, contributor.Name = row.kind, row.roleID, row.elementID, row.name
			}
			value := row.value
			contributor.Values[i] = &value
			for skill, amount := range row.skills {
				compared := skillRows[skill]
				if compared == nil {
					named := row.named[skill]
					compared = &compareSkill{SkillID: skill, Name: skillName(named), NameSource: named.NameSource, ShowSkillID: named.ShowSkillID, IconKind: named.IconKind, IconID: named.IconID, Values: make([]*compareSkillValue, len(inputs))}
					skillRows[skill] = compared
					skillOrder = append(skillOrder, skill)
				} else if named := row.named[skill]; compared.Name.Status != "VERIFIED" && named.NameStatus == "VERIFIED" {
					compared.Name, compared.NameSource, compared.ShowSkillID = skillName(named), named.NameSource, named.ShowSkillID
				}
				copied := *amount
				compared.Values[i] = &copied
			}
			if row.unassigned.Damage > 0 {
				copied := row.unassigned
				unassigned.Values[i] = &copied
				hasUnassigned = true
			}
		}
		// The DPS change of a contributor is measured between complete records where it dealt damage in both.
		if base := contributor.Values[0]; base != nil && reliable[0] && base.DPS != nil && *base.DPS > 0 {
			for i := 1; i < len(inputs); i++ {
				if value := contributor.Values[i]; value != nil && reliable[i] && value.DPS != nil {
					value.DPSDelta = number((*value.DPS - *base.DPS) / *base.DPS)
				}
			}
		}
		largest := func(values []*compareSkillValue) int64 {
			var best int64
			for _, value := range values {
				if value != nil && value.Damage > best {
					best = value.Damage
				}
			}
			return best
		}
		sort.Slice(skillOrder, func(a, b int) bool {
			left, right := largest(skillRows[skillOrder[a]].Values), largest(skillRows[skillOrder[b]].Values)
			if left != right {
				return left > right
			}
			return skillOrder[a] < skillOrder[b]
		})
		for _, skill := range skillOrder {
			contributor.Skills = append(contributor.Skills, *skillRows[skill])
		}
		if hasUnassigned {
			contributor.Skills = append(contributor.Skills, unassigned)
		}
		out.Contributors = append(out.Contributors, contributor)
	}
	// Roles first, by how many encounters they dealt damage in and then by their largest damage; then the vehicle and the
	// unassigned damage.
	rank := map[string]int{"role": 0, "vehicle": 1, "unassigned": 2}
	present := func(c compareContributor) (count int, best int64) {
		for _, value := range c.Values {
			if value != nil {
				count++
				best = max(best, value.Damage)
			}
		}
		return count, best
	}
	sort.SliceStable(out.Contributors, func(a, b int) bool {
		left, right := out.Contributors[a], out.Contributors[b]
		if rank[left.Kind] != rank[right.Kind] {
			return rank[left.Kind] < rank[right.Kind]
		}
		leftCount, leftBest := present(left)
		rightCount, rightBest := present(right)
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		if leftBest != rightBest {
			return leftBest > rightBest
		}
		return left.RoleID < right.RoleID
	})

	// The conditions the encounters share, stated as facts.
	versions, modes, stages, enemies := make([]string, len(inputs)), make([]string, len(inputs)), make([]string, len(inputs)), make([]string, len(inputs))
	shortest, longest, durations := 0.0, 0.0, 0
	for i, input := range inputs {
		summary := input.summary
		versions[i] = summary.GameVersion
		modes[i] = modeKey(summary.Mode)
		if summary.Scene.Known {
			stages[i] = strconv.FormatUint(summary.Scene.InstanceID, 10)
		}
		enemies[i] = enemyKey(summary.MainEnemy)
		out.Context.OpenWorld = out.Context.OpenWorld || summary.Mode.Kind == capture.InstanceModeOpenWorld
		if seconds := summary.DurationSeconds; seconds != nil && *seconds > 0 {
			if durations == 0 || *seconds < shortest {
				shortest = *seconds
			}
			longest = max(longest, *seconds)
			durations++
		}
	}
	out.Context.Conditions = []compareCondition{
		{Key: "game_version", State: sameState(versions)},
		{Key: "mode", State: sameState(modes)},
		{Key: "stage", State: sameState(stages)},
		{Key: "main_enemy", State: sameState(enemies)},
	}
	if durations == len(inputs) && shortest > 0 {
		out.Context.DurationRatio = number(longest / shortest)
	}
	return out
}

// HistoryCompare returns two to four stored encounters side by side in the view's time zone, named in the UI locale. An
// encounter deleted meanwhile is listed under missing, so the view can drop it.
func (b *Bridge) HistoryCompare(ids []int64, zoneName string) (json.RawMessage, error) {
	if err := compareIDs(ids); err != nil {
		return nil, err
	}
	names, cancel := b.historyQuery()
	defer cancel()
	zone := history.ResolveViewZone(zoneName)
	classes, err := b.history.RecordClasses(names.ctx, ids)
	if err != nil {
		return nil, err
	}
	inputs := make([]compareInput, 0, len(ids))
	missing := []int64{}
	for _, id := range ids {
		detail, err := b.history.Encounter(names.ctx, id, zone.Location)
		if errors.Is(err, history.ErrNotFound) {
			missing = append(missing, id)
			continue
		}
		if err != nil {
			return nil, err
		}
		input := compareInput{summary: names.summary(detail.Summary), class: classes[id], olderAccounting: !detail.Summary.NotifiedDamageCounted}
		if detail.Snapshot != nil {
			view := b.masters.ProjectEncounter(*detail.Snapshot)
			names.encounter(&view)
			input.view = &view
		}
		inputs = append(inputs, input)
	}
	if names.err != nil {
		return nil, names.err
	}
	compared := comparison{Encounters: []compareEncounter{}, Metrics: []compareMetric{}, Contributors: []compareContributor{}}
	if len(inputs) > 0 {
		compared = compareEncounters(inputs)
	}
	return json.Marshal(struct {
		Status  history.Status  `json:"status"`
		Zone    historyZoneJSON `json:"zone"`
		Missing []int64         `json:"missing"`
		Limit   int             `json:"limit"`
		comparison
	}{Status: b.history.Status(), Zone: zoneJSON(zone), Missing: missing, Limit: maxCompared, comparison: compared})
}
