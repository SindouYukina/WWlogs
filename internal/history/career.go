package history

import (
	"context"
	"sort"
	"time"

	"wwlogs/internal/capture"
)

// Career statistics: long-term personal numbers over the stored encounters, from the same index and record classes as the
// library (library.go). Go is the only place they are computed; the view lays them out.
//
// Definitions:
//
//   - An encounter counts for totals when its class is complete, degraded or known_affected; the last two make a total a
//     lower bound. Personal records count complete encounters only.
//   - Encounter DPS is the engine's: confirmed damage over the encounter's wall-clock duration, pauses and cut-ins
//     included. A role's Encounter DPS is its confirmed damage over the same duration.
//   - The peak record is the engine's peak 1-second DPS (the largest 1 s timeline bucket of the encounter).
//   - A role's participation is an encounter it dealt confirmed damage in (a stored contributor), never a team slot.
//   - Days are calendar dates of the view's zone where encounters started; active days are dates with any stored
//     encounter, which is WWlogs' recorded activity, not the player's play time.

// CareerQuery selects the encounters a career covers. Instances is set by the host from mode and stage filters.
type CareerQuery struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// LastDays, when set, selects the last LastDays dates of the view's zone up to today instead of From and To.
	LastDays     int      `json:"lastDays,omitempty"`
	GameVersions []string `json:"gameVersions,omitempty"`
	Instances    []uint64 `json:"-"`
}

// CareerRef names the encounter behind a number.
type CareerRef struct {
	EncounterID int64     `json:"encounterId"`
	StartedAt   time.Time `json:"startedAt"`
	Start       LocalTime `json:"start"`
	GameVersion string    `json:"gameVersion"`
	Instance    uint64    `json:"instanceId,omitempty"`
	// Roles are the static roles that dealt confirmed damage in it.
	Roles []uint64 `json:"roles"`
}

// CareerRecord is a personal record: a value and the encounter, role and skill it came from.
type CareerRecord struct {
	Value   float64 `json:"value"`
	RoleID  uint64  `json:"roleId,omitempty"`
	SkillID uint64  `json:"skillId,omitempty"`
	CareerRef
}

// CareerTotals are cumulative values over the selection. Damage and seconds cover counted encounters only.
type CareerTotals struct {
	Encounters int            `json:"encounters"`
	Counted    int            `json:"counted"`
	Classes    map[string]int `json:"classes"`
	Damage     int64          `json:"damage"`
	// DamageLowerBound is set when a counted encounter's damage is a lower bound (degraded or known_affected).
	DamageLowerBound bool       `json:"damageLowerBound,omitempty"`
	Seconds          float64    `json:"seconds"`
	ActiveDays       int        `json:"activeDays"`
	First            *CareerRef `json:"first,omitempty"`
	Latest           *CareerRef `json:"latest,omitempty"`
}

// CareerDay is one date's activity.
type CareerDay struct {
	Date       string `json:"date"`
	Encounters int    `json:"encounters"`
	Counted    int    `json:"counted"`
	Damage     int64  `json:"damage"`
	// DamageLowerBound is set when a counted encounter's damage in it is a lower bound (degraded or known_affected).
	DamageLowerBound bool    `json:"damageLowerBound,omitempty"`
	Seconds          float64 `json:"seconds"`
	// BestDPS is the date's highest Encounter DPS among record-eligible encounters (0: none).
	BestDPS float64 `json:"bestDps,omitempty"`
}

// CareerRole is one role's career (a trial role counts as its parent).
type CareerRole struct {
	RoleID          uint64 `json:"roleId"`
	Encounters      int    `json:"encounters"`
	Counted         int    `json:"counted"`
	TrialEncounters int    `json:"trialEncounters,omitempty"`
	Damage          int64  `json:"damage"`
	// DamageLowerBound is set when a counted encounter's damage in it is a lower bound (degraded or known_affected).
	DamageLowerBound bool `json:"damageLowerBound,omitempty"`
	// BestDPS, BestDamage and MaxHit are the role's personal records (complete encounters only).
	BestDPS    *CareerRecord `json:"bestDps,omitempty"`
	BestDamage *CareerRecord `json:"bestDamage,omitempty"`
	MaxHit     *CareerRecord `json:"maxHit,omitempty"`
	FirstDate  string        `json:"firstDate"`
	LastDate   string        `json:"lastDate"`
	// Days are the role's dates of the selection, ascending.
	Days []CareerRoleDay `json:"days"`
}

// CareerRoleDay is one role's activity on one date: participations, its damage in counted encounters, and its best DPS
// in complete ones (the role's damage over the encounter's wall-clock duration).
type CareerRoleDay struct {
	Date       string `json:"date"`
	Encounters int    `json:"encounters"`
	Damage     int64  `json:"damage"`
	// DamageLowerBound is set when a counted encounter's damage in it is a lower bound (degraded or known_affected).
	DamageLowerBound bool    `json:"damageLowerBound,omitempty"`
	BestDPS          float64 `json:"bestDps,omitempty"`
}

// CareerCombo is a set of roles that dealt confirmed damage together: stored contributors, not a team composition.
type CareerCombo struct {
	Roles      []uint64 `json:"roles"`
	Encounters int      `json:"encounters"`
	Damage     int64    `json:"damage"`
	// DamageLowerBound is set when a counted encounter's damage in it is a lower bound (degraded or known_affected).
	DamageLowerBound bool          `json:"damageLowerBound,omitempty"`
	BestDPS          *CareerRecord `json:"bestDps,omitempty"`
}

// CareerInstance is one stored scene instance's activity; the host groups instances into modes.
type CareerInstance struct {
	Instance   uint64 `json:"instanceId"`
	Encounters int    `json:"encounters"`
	Counted    int    `json:"counted"`
	Damage     int64  `json:"damage"`
	// DamageLowerBound is set when a counted encounter's damage in it is a lower bound (degraded or known_affected).
	DamageLowerBound bool          `json:"damageLowerBound,omitempty"`
	BestDPS          *CareerRecord `json:"bestDps,omitempty"`
}

// CareerRecords are the selection's personal records over all roles.
type CareerRecords struct {
	MaxHit     *CareerRecord `json:"maxHit,omitempty"`
	BestDPS    *CareerRecord `json:"bestDps,omitempty"`
	BestDamage *CareerRecord `json:"bestDamage,omitempty"`
	BestPeak   *CareerRecord `json:"bestPeak,omitempty"`
}

// CareerView is the career of a selection.
type CareerView struct {
	Today string `json:"today"`
	// From and To are the dates the answer covers ("" for an open end).
	From      string           `json:"from"`
	To        string           `json:"to"`
	Totals    CareerTotals     `json:"totals"`
	Days      []CareerDay      `json:"days"`
	Roles     []CareerRole     `json:"roles"`
	Combos    []CareerCombo    `json:"combos"`
	Instances []CareerInstance `json:"instances"`
	Records   CareerRecords    `json:"records"`
	// Versions lists every game version the history holds, for the filter.
	Versions []string `json:"versions"`
}

// maxCombos bounds the role combinations a career lists (by encounters).
const maxCombos = 24

// CareerDates resolves a LastDays query into the dates of the view's zone it covers, today included, so the host can
// select the same dates elsewhere (the filters of the view).
func (s *Store) CareerDates(query CareerQuery, zone *time.Location) CareerQuery {
	return careerDates(query, s.now().In(viewLocation(zone)))
}

func careerDates(query CareerQuery, today time.Time) CareerQuery {
	if query.LastDays <= 0 {
		return query
	}
	query.To = today.Format(dateLayout)
	query.From = time.Date(today.Year(), today.Month(), today.Day()-(query.LastDays-1), 0, 0, 0, 0, today.Location()).Format(dateLayout)
	query.LastDays = 0
	return query
}

// Career computes the career of the selection. staticRole maps a stored runtime RoleId to the role it shows as (a trial
// role to its parent); nil keeps the stored RoleId.
func (s *Store) Career(ctx context.Context, query CareerQuery, zone *time.Location, staticRole func(uint64) uint64) (CareerView, error) {
	zone = viewLocation(zone)
	today := s.now().In(zone)
	query = careerDates(query, today)
	view := CareerView{Today: today.Format(dateLayout), From: query.From, To: query.To, Totals: CareerTotals{Classes: map[string]int{}}, Days: []CareerDay{},
		Roles: []CareerRole{}, Combos: []CareerCombo{}, Instances: []CareerInstance{}, Versions: []string{}}
	index, err := s.index(ctx)
	if err != nil {
		return view, err
	}
	if staticRole == nil {
		staticRole = func(id uint64) uint64 { return id }
	}
	versions := map[string]bool{}
	instances := idSet(query.Instances)
	var selected []*indexedEncounter
	for i := range index {
		e := &index[i]
		if e.GameVersion != "" {
			versions[e.GameVersion] = true
		}
		if _, in := dateIn(e.StartedNs, zone, query.From, query.To); !in {
			continue
		}
		if len(query.GameVersions) > 0 && !containsString(query.GameVersions, e.GameVersion) {
			continue
		}
		if instances != nil && (!e.InstanceKnown || !instances[e.Instance]) {
			continue
		}
		selected = append(selected, e)
	}
	for version := range versions {
		view.Versions = append(view.Versions, version)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(view.Versions)))
	var eligible []int64
	for _, e := range selected {
		if e.recordEligible() {
			eligible = append(eligible, e.ID)
		}
	}
	hits, err := s.maxHits(ctx, eligible)
	if err != nil {
		return view, err
	}

	ref := func(e *indexedEncounter) CareerRef {
		at := time.Unix(0, e.StartedNs).UTC()
		r := CareerRef{EncounterID: e.ID, StartedAt: at, Start: localTimeOf(at, zone), GameVersion: e.GameVersion, Roles: []uint64{}}
		if e.InstanceKnown {
			r.Instance = e.Instance
		}
		seen := map[uint64]bool{}
		for _, role := range e.Roles {
			if static := staticRole(role.RoleID); role.Damage > 0 && !seen[static] {
				seen[static] = true
				r.Roles = append(r.Roles, static)
			}
		}
		return r
	}
	better := func(current *CareerRecord, value float64, build func() CareerRecord) *CareerRecord {
		if value <= 0 || current != nil && current.Value >= value {
			return current
		}
		record := build()
		record.Value = value
		return &record
	}

	days := map[string]*CareerDay{}
	roles := map[uint64]*CareerRole{}
	roleDays := map[uint64]map[string]*CareerRoleDay{}
	combos := map[string]*CareerCombo{}
	byInstance := map[uint64]*CareerInstance{}
	for _, e := range selected {
		date := time.Unix(0, e.StartedNs).In(zone).Format(dateLayout)
		class := e.Class()
		view.Totals.Encounters++
		view.Totals.Classes[class]++
		if view.Totals.First == nil {
			first := ref(e)
			view.Totals.First = &first
		}
		latest := ref(e)
		view.Totals.Latest = &latest
		day := days[date]
		if day == nil {
			day = &CareerDay{Date: date}
			days[date] = day
		}
		day.Encounters++
		counted, eligibleRecord := e.counted(), e.recordEligible()
		seconds := e.durationSeconds()
		encounterDPS, hasDPS := e.dps()
		if counted {
			view.Totals.Counted++
			view.Totals.Damage += e.Damage
			view.Totals.Seconds += seconds
			view.Totals.DamageLowerBound = view.Totals.DamageLowerBound || e.lowerBound()
			day.Counted++
			day.Damage += e.Damage
			day.DamageLowerBound = day.DamageLowerBound || e.lowerBound()
			day.Seconds += seconds
		}
		if eligibleRecord && hasDPS {
			day.BestDPS = max(day.BestDPS, encounterDPS)
			view.Records.BestDPS = better(view.Records.BestDPS, encounterDPS, func() CareerRecord { return CareerRecord{CareerRef: ref(e)} })
			view.Records.BestDamage = better(view.Records.BestDamage, float64(e.Damage), func() CareerRecord { return CareerRecord{CareerRef: ref(e)} })
			if capture.DPSStatus(e.PeakStatus) == capture.DPSStatusAvailable {
				view.Records.BestPeak = better(view.Records.BestPeak, e.PeakValue, func() CareerRecord { return CareerRecord{CareerRef: ref(e)} })
			}
		}
		if eligibleRecord {
			for runtime, hit := range hits[e.ID] {
				role := uint64(0)
				if runtime != 0 {
					role = staticRole(runtime)
				}
				view.Records.MaxHit = better(view.Records.MaxHit, float64(hit.Amount), func() CareerRecord {
					return CareerRecord{RoleID: role, SkillID: hit.SkillID, CareerRef: ref(e)}
				})
			}
		}
		if e.InstanceKnown {
			instance := byInstance[e.Instance]
			if instance == nil {
				instance = &CareerInstance{Instance: e.Instance}
				byInstance[e.Instance] = instance
			}
			instance.Encounters++
			if counted {
				instance.Counted++
				instance.Damage += e.Damage
				instance.DamageLowerBound = instance.DamageLowerBound || e.lowerBound()
			}
			if eligibleRecord && hasDPS {
				instance.BestDPS = better(instance.BestDPS, encounterDPS, func() CareerRecord { return CareerRecord{CareerRef: ref(e)} })
			}
		}
		// Roles, by the role they show as: a trial and its parent in one encounter count once.
		participated := map[uint64]int64{}
		trial := map[uint64]bool{}
		for _, contributor := range e.Roles {
			role := staticRole(contributor.RoleID)
			participated[role] += contributor.Damage
			trial[role] = trial[role] || role != contributor.RoleID
		}
		var comboRoles []uint64
		for role, damage := range participated {
			if damage > 0 {
				comboRoles = append(comboRoles, role)
			}
			stats := roles[role]
			if stats == nil {
				stats = &CareerRole{RoleID: role, FirstDate: date}
				roles[role] = stats
				roleDays[role] = map[string]*CareerRoleDay{}
			}
			stats.Encounters++
			stats.LastDate = date
			roleDay := roleDays[role][date]
			if roleDay == nil {
				roleDay = &CareerRoleDay{Date: date}
				roleDays[role][date] = roleDay
			}
			roleDay.Encounters++
			if trial[role] {
				stats.TrialEncounters++
			}
			if counted {
				stats.Counted++
				stats.Damage += damage
				stats.DamageLowerBound = stats.DamageLowerBound || e.lowerBound()
				roleDay.Damage += damage
				roleDay.DamageLowerBound = roleDay.DamageLowerBound || e.lowerBound()
			}
			if eligibleRecord && seconds > 0 && damage > 0 {
				roleDay.BestDPS = max(roleDay.BestDPS, float64(damage)/seconds)
				stats.BestDPS = better(stats.BestDPS, float64(damage)/seconds, func() CareerRecord { return CareerRecord{RoleID: role, CareerRef: ref(e)} })
				stats.BestDamage = better(stats.BestDamage, float64(damage), func() CareerRecord { return CareerRecord{RoleID: role, CareerRef: ref(e)} })
			}
		}
		if eligibleRecord {
			for runtime, hit := range hits[e.ID] {
				if runtime == 0 {
					continue
				}
				role := staticRole(runtime)
				if stats := roles[role]; stats != nil {
					stats.MaxHit = better(stats.MaxHit, float64(hit.Amount), func() CareerRecord {
						return CareerRecord{RoleID: role, SkillID: hit.SkillID, CareerRef: ref(e)}
					})
				}
			}
		}
		if counted && len(comboRoles) > 0 {
			sort.Slice(comboRoles, func(i, j int) bool { return comboRoles[i] < comboRoles[j] })
			key := comboKey(comboRoles)
			combo := combos[key]
			if combo == nil {
				combo = &CareerCombo{Roles: comboRoles}
				combos[key] = combo
			}
			combo.Encounters++
			combo.Damage += e.Damage
			combo.DamageLowerBound = combo.DamageLowerBound || e.lowerBound()
			if eligibleRecord && hasDPS {
				combo.BestDPS = better(combo.BestDPS, encounterDPS, func() CareerRecord { return CareerRecord{CareerRef: ref(e)} })
			}
		}
	}
	view.Totals.ActiveDays = len(days)
	for _, day := range days {
		view.Days = append(view.Days, *day)
	}
	sort.Slice(view.Days, func(i, j int) bool { return view.Days[i].Date < view.Days[j].Date })
	for role, stats := range roles {
		for _, day := range roleDays[role] {
			stats.Days = append(stats.Days, *day)
		}
		sort.Slice(stats.Days, func(i, j int) bool { return stats.Days[i].Date < stats.Days[j].Date })
		view.Roles = append(view.Roles, *stats)
	}
	sort.Slice(view.Roles, func(i, j int) bool {
		if view.Roles[i].Encounters != view.Roles[j].Encounters {
			return view.Roles[i].Encounters > view.Roles[j].Encounters
		}
		return view.Roles[i].RoleID < view.Roles[j].RoleID
	})
	for _, combo := range combos {
		view.Combos = append(view.Combos, *combo)
	}
	sort.Slice(view.Combos, func(i, j int) bool {
		if view.Combos[i].Encounters != view.Combos[j].Encounters {
			return view.Combos[i].Encounters > view.Combos[j].Encounters
		}
		return comboKey(view.Combos[i].Roles) < comboKey(view.Combos[j].Roles)
	})
	if len(view.Combos) > maxCombos {
		view.Combos = view.Combos[:maxCombos]
	}
	for _, instance := range byInstance {
		view.Instances = append(view.Instances, *instance)
	}
	sort.Slice(view.Instances, func(i, j int) bool {
		if view.Instances[i].Encounters != view.Instances[j].Encounters {
			return view.Instances[i].Encounters > view.Instances[j].Encounters
		}
		return view.Instances[i].Instance < view.Instances[j].Instance
	})
	return view, nil
}

func comboKey(roles []uint64) string {
	key := make([]byte, 0, len(roles)*6)
	for i, role := range roles {
		if i > 0 {
			key = append(key, '+')
		}
		key = appendUint(key, role)
	}
	return string(key)
}

func appendUint(out []byte, value uint64) []byte {
	if value == 0 {
		return append(out, '0')
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return append(out, digits[i:]...)
}
