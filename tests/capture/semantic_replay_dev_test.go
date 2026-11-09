//go:build wwdev

package capture

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSemanticReplayCollector checks the meaning of the Effects Overlay and the skill table on real captures: not only that
// counts add up, but that a status is listed in the right row, with the right target, stack and order, and that a skill row
// carries the right name, evidence grade and ID rule. Expectations are event-level facts of the named captures; a capture
// that is absent is skipped, and the skip is reported.
//
// It is skipped unless WWLOGS_SEMANTIC_REPLAY_PCAPS is set and reads only paths supplied through the environment.
//
// Environment:
//
//	WWLOGS_SEMANTIC_REPLAY_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_SEMANTIC_REPLAY_MASTER_DB  Master SQLite database

// semanticEffectFact is one status that must be listed at some point of a capture.
type semanticEffectFact struct {
	capture  string
	effectID uint64
	row      EffectPolarity
	target   EffectDisplayTarget
	maxStack uint64
}

// semanticSkillFact is one skill row a capture must show.
type semanticSkillFact struct {
	capture string
	roleID  uint64
	skillID uint64
	status  string
	source  string
	name    string
	showID  bool
}

var semanticEffectFacts = []semanticEffectFact{
	// Monster Glacio on the local player's entity, with its real stack count.
	{"LIVE_20261103_122337", 10010000, EffectPolarityDebuff, EffectTargetTeam, 7},
	{"LIVE_20261109_121001", 10010000, EffectPolarityDebuff, EffectTargetTeam, 1},
	// Aero Erosion and Havoc Bane the team puts on enemies.
	{"LIVE_20261109_121001", 10041000, EffectPolarityDebuff, EffectTargetEnemy, 3},
	{"LIVE_20261109_121001", 10061000, EffectPolarityDebuff, EffectTargetEnemy, 6},
	// Team buffs the HUD draws with a BUFF arrow.
	{"LIVE_20261109_121001", 2002203612, EffectPolarityBuff, EffectTargetTeam, 1},
	{"LIVE_20261109_121001", 2002203902, EffectPolarityBuff, EffectTargetTeam, 1},
	{"LIVE_20261109_121001", 1508970001, EffectPolarityBuff, EffectTargetTeam, 1},
}

// Effects that must never be listed: logic flags and refresh-period modifiers the game draws no buff-bar item for.
var semanticNeverListed = map[uint64]string{
	1101003023: "dodge invulnerability flag",
	1409401095: "six-element fan-out modifier",
	1508952003: "six-element fan-out modifier",
	3098:       "fall damage immunity",
	// The game draws this role buff without a direction arrow, so it belongs to neither row.
	1311400007: "HUD cue without a direction",
}

var semanticSkillFacts = []semanticSkillFact{
	{"LIVE_20261109_121001", 1409, 1409002, "VERIFIED", "official", "以劍奉讀此身", false},
	{"LIVE_20261109_121001", 1209, 1209400, "DERIVED", "skill_type", "歸斂", true},
	{"LIVE_20261109_121001", 1209, 1209200, "DERIVED", "damage_list", "臨界協議", true},
	{"LIVE_20261108_230549", 1209, 280034001, "DERIVED", "echo", "爐芯機骸", true},
	{"LIVE_20261108_230549", 1508, 1508404, "DERIVED", "damage_list", "解弦視界·萬理歸塵", true},
	{"LIVE_20261109_121001", 1409, 1409991, "DERIVED", "tune_break", "", true},
	{"LIVE_20261103_122337", 1505, 280013010, "DERIVED", "echo", "異構武裝", true},
	{"LIVE_20261105_195015", 1102, 1102030, "DERIVED", "skill_type", "焦瞑凍土", true},
}

// semanticCompletenessFact is what one encounter must report about its own damage.
type semanticCompletenessFact struct {
	capture       string
	sequence      uint64
	total         int64
	vehicle       int64
	unknownDamage int64
	unknownEvents uint64
}

var semanticCompletenessFacts = []semanticCompletenessFact{
	// A motorcycle-heavy fight with nothing unknown: the vehicle is a contributor, not a gap.
	{"LIVE_20261103_122337", 6, 250027, 188920, 0, 0},
	// Hits on a Monster the enemy summoned count as damage dealt; its summoner chain ends at a verified Monster.
	{"LIVE_20261109_121001", 10, 628238, 125613, 0, 0},
	{"LIVE_20261109_121001", 11, 591112, 0, 0, 0},
	{"LIVE_20261105_195015", 5, 812471, 115146, 0, 0},
}

type semanticEffectSeen struct {
	row      EffectPolarity
	target   EffectDisplayTarget
	maxStack uint64
}

func semanticCheckRows(view ViewEffects) error {
	check := func(row ViewEffectRow, polarity EffectPolarity) error {
		var previous time.Time
		timedDone := false
		for _, effect := range row.Effects {
			if effect.Polarity != polarity || !effect.HUDIcon || effect.Display.Eligibility != EffectDisplayEligible {
				return fmt.Errorf("listed cell in the %s row: polarity %s, icon %v, eligibility %s", polarity, effect.Polarity, effect.HUDIcon, effect.Display.Eligibility)
			}
			if effect.ShowCountdown && effect.ExpectedExpiry != nil && effect.RemainingStatus == EffectRemainingEstimated && !effect.Stale {
				if timedDone {
					return fmt.Errorf("a timed cell follows an untimed one in the %s row", polarity)
				}
				if effect.ExpectedExpiry.Before(previous) {
					return fmt.Errorf("the %s row is not soonest-first", polarity)
				}
				previous = *effect.ExpectedExpiry
			} else if effect.RemainingStatus != EffectRemainingEstimated || effect.Stale || effect.ExpectedExpiry == nil {
				timedDone = true
			}
		}
		return nil
	}
	if err := check(view.Buffs, EffectPolarityBuff); err != nil {
		return err
	}
	return check(view.Debuffs, EffectPolarityDebuff)
}

// semanticCheckRankingScope checks one scope of a real view: the order, each row's numerator and the shared denominator, and
// that the per-contributor DPS uses the scope's own duration. Percentages adding up would not catch two rows using different
// denominators, so every row is checked against the scope's total.
func semanticCheckRankingScope(roles []ViewRole, vehicle *ViewVehicle, unassigned UnassignedRoleTotals, unassignedShare *float64, total int64, dps *ViewDPS) error {
	type row struct {
		key    string
		damage int64
		share  *float64
		rate   *float64
		rank   int
	}
	rows := make([]row, 0, len(roles)+1)
	for _, role := range roles {
		rows = append(rows, row{fmt.Sprintf("role-%d-%d", role.PlayerID, role.RoleID), role.DamageDealt, role.DamageShare, role.DPS, role.Rank})
	}
	if vehicle != nil {
		rows = append(rows, row{"vehicle-" + vehicle.Kind, vehicle.DamageDealt, vehicle.DamageShare, vehicle.DPS, vehicle.Rank})
	}
	ordered := make([]*row, len(rows))
	sum, shares, rates := unassigned.DamageDealt, 0.0, 0.0
	usable := dps != nil && (dps.Status == DPSStatusAvailable || dps.Status == DPSStatusLowerBound) && dps.DurationSeconds > 0
	for i := range rows {
		item := &rows[i]
		if item.rank < 0 || item.rank >= len(rows) || ordered[item.rank] != nil {
			return fmt.Errorf("%s has rank %d of %d rows", item.key, item.rank, len(rows))
		}
		ordered[item.rank] = item
		sum += item.damage
		if total > 0 {
			if item.share == nil || *item.share != float64(item.damage)/float64(total) {
				return fmt.Errorf("%s share %v is not %d over %d", item.key, item.share, item.damage, total)
			}
			shares += *item.share
		} else if item.share != nil {
			return fmt.Errorf("%s has a share of a zero total", item.key)
		}
		if usable {
			if item.rate == nil || *item.rate != float64(item.damage)/dps.DurationSeconds {
				return fmt.Errorf("%s dps %v is not %d over %v s", item.key, item.rate, item.damage, dps.DurationSeconds)
			}
			rates += *item.rate
		} else if item.rate != nil {
			return fmt.Errorf("%s has a dps without a usable scope dps", item.key)
		}
	}
	for i := 1; i < len(ordered); i++ {
		a, b := ordered[i-1], ordered[i]
		if a.damage < b.damage || (a.damage == b.damage && a.key >= b.key) {
			return fmt.Errorf("%s (%d) is ranked above %s (%d)", a.key, a.damage, b.key, b.damage)
		}
	}
	if sum != total {
		return fmt.Errorf("rows and unassigned add up to %d of %d", sum, total)
	}
	if total > 0 {
		if unassignedShare == nil || *unassignedShare != float64(unassigned.DamageDealt)/float64(total) {
			return fmt.Errorf("unassigned share %v is not %d over %d", unassignedShare, unassigned.DamageDealt, total)
		}
		if math.Abs(shares+*unassignedShare-1) > 1e-9 {
			return fmt.Errorf("shares add up to %v", shares+*unassignedShare)
		}
	}
	if usable && dps.Damage == total && math.Abs(rates+float64(unassigned.DamageDealt)/dps.DurationSeconds-dps.Value) > 1e-6*math.Max(1, dps.Value) {
		return fmt.Errorf("row dps %v does not add up to the scope's %v", rates, dps.Value)
	}
	return nil
}

// semanticCheckSkillPartition splits one scope's confirmed contributor damage by what is known about its skill. Every hit
// falls in exactly one class: a row with its own official name, a row with a derived name, a row with a SkillId and no name, or
// no row at all (the hit carried no SkillId). An UNKNOWN row and a hit without a SkillId are different gaps, so a scope can
// have no UNKNOWN row and still lack a skill for some of its damage; the classes must add up either way.
func semanticCheckSkillPartition(roles []ViewRole, vehicle *ViewVehicle, completeness ViewDamageCompleteness) error {
	var noSkillDamage int64
	var noSkillEvents uint64
	check := func(label string, dealt int64, events uint64, skills []ViewSkill, unassigned ViewUnassignedSkill) error {
		rowDamage, rowEvents := int64(0), uint64(0)
		for _, skill := range skills {
			if skill.SkillID == 0 {
				return fmt.Errorf("%s has a row without a SkillId", label)
			}
			switch skill.NameStatus {
			case "VERIFIED", "DERIVED", "UNKNOWN":
			default:
				return fmt.Errorf("%s skill %d has status %q", label, skill.SkillID, skill.NameStatus)
			}
			if (skill.Name == "") != (skill.NameStatus == "UNKNOWN") {
				return fmt.Errorf("%s skill %d: name %q with status %s", label, skill.SkillID, skill.Name, skill.NameStatus)
			}
			rowDamage += skill.DamageDealt
			rowEvents += skill.OutgoingEvents
		}
		if rowDamage+unassigned.DamageDealt != dealt || rowEvents+unassigned.OutgoingEvents != events {
			return fmt.Errorf("%s: rows %d/%d and no-skill %d/%d do not add up to %d/%d", label, rowDamage, rowEvents, unassigned.DamageDealt, unassigned.OutgoingEvents, dealt, events)
		}
		if unassigned.OutgoingEvents != unassigned.MissingSkillEvents+unassigned.OverflowEvents {
			return fmt.Errorf("%s: %d no-skill events are not %d missing and %d overflow", label, unassigned.OutgoingEvents, unassigned.MissingSkillEvents, unassigned.OverflowEvents)
		}
		if unassigned.Name != "" && unassigned.NameStatus != "DERIVED" {
			return fmt.Errorf("%s: a row without a SkillId has a %q name", label, unassigned.NameStatus)
		}
		noSkillDamage += unassigned.DamageDealt
		noSkillEvents += unassigned.OutgoingEvents
		return nil
	}
	for _, role := range roles {
		if err := check(fmt.Sprintf("role %d", role.RoleID), role.DamageDealt, role.OutgoingEvents, role.Skills, role.UnassignedSkill); err != nil {
			return err
		}
	}
	if vehicle != nil {
		if err := check("vehicle "+vehicle.Kind, vehicle.DamageDealt, vehicle.OutgoingEvents, vehicle.Skills, vehicle.UnassignedSkill); err != nil {
			return err
		}
	}
	if noSkillDamage != completeness.NoSkillDamage || noSkillEvents != completeness.NoSkillEvents {
		return fmt.Errorf("no-skill rows %d/%d differ from the scope's completeness %d/%d", noSkillDamage, noSkillEvents, completeness.NoSkillDamage, completeness.NoSkillEvents)
	}
	if completeness.SkillBreakdownComplete != (noSkillEvents == 0) {
		return fmt.Errorf("skill breakdown complete = %v with %d no-skill events", completeness.SkillBreakdownComplete, noSkillEvents)
	}
	return nil
}

// semanticCheckRanking checks every scope of a view, and that the timeline belongs to the encounter the rows of the screen
// come from.
func semanticCheckRanking(view ViewSnapshot) error {
	if err := semanticCheckRankingScope(view.Roles, view.Vehicle, view.UnassignedRole, view.UnassignedRoleShare, view.Statistics.DamageDealt, nil); err != nil {
		return fmt.Errorf("interval: %w", err)
	}
	if err := semanticCheckSkillPartition(view.Roles, view.Vehicle, view.Completeness); err != nil {
		return fmt.Errorf("interval skills: %w", err)
	}
	encounters := append([]ViewEncounter(nil), view.Encounters.Recent...)
	if view.Encounters.Current != nil {
		encounters = append(encounters, *view.Encounters.Current)
	}
	for i := range encounters {
		encounter := &encounters[i]
		if err := semanticCheckRankingScope(encounter.Roles, encounter.Vehicle, encounter.UnassignedRole, encounter.UnassignedRoleShare, encounter.Totals.DamageDealt, &encounter.DPS); err != nil {
			return fmt.Errorf("encounter %d: %w", encounter.Sequence, err)
		}
		if err := semanticCheckSkillPartition(encounter.Roles, encounter.Vehicle, encounter.Completeness); err != nil {
			return fmt.Errorf("encounter %d skills: %w", encounter.Sequence, err)
		}
		if encounter.DPS.Damage != 0 && encounter.DPS.Damage != encounter.Totals.DamageDealt {
			return fmt.Errorf("encounter %d: dps damage %d differs from the total %d", encounter.Sequence, encounter.DPS.Damage, encounter.Totals.DamageDealt)
		}
	}
	if view.Timeline != nil && len(encounters) > 0 && view.Timeline.EncounterID != encounters[len(encounters)-1].EncounterID {
		return fmt.Errorf("timeline of %s beside rows of %s", view.Timeline.EncounterID, encounters[len(encounters)-1].EncounterID)
	}
	return nil
}

func semanticCaptureMatches(name, capture string) bool { return strings.Contains(name, capture) }

func TestSemanticReplayCollector(t *testing.T) {
	list := os.Getenv("WWLOGS_SEMANTIC_REPLAY_PCAPS")
	if list == "" {
		t.Skip("WWLOGS_SEMANTIC_REPLAY_PCAPS is not set")
	}
	pcaps := readReplayPCAPList(t, list)
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	masters, err := loadGameMasterDatabase(os.Getenv("WWLOGS_SEMANTIC_REPLAY_MASTER_DB"), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, capture := range pcaps {
		present[capture.name] = true
	}
	covered := func(name string) bool {
		for capture := range present {
			if semanticCaptureMatches(capture, name) {
				return true
			}
		}
		return false
	}
	inputs := equivalenceInputs{localIP: "192.168.1.2"}
	for _, capture := range pcaps {
		capture := capture
		t.Run(capture.name, func(t *testing.T) {
			seen := map[uint64]*semanticEffectSeen{}
			last := time.Time{}
			var rowsErr error
			p, _ := equivalenceReplay(t, inputs, masters, key, capture.path, nil, func(index int, p *processor) {
				if p.currentFrameTime.IsZero() || (!last.IsZero() && p.currentFrameTime.Sub(last) < 250*time.Millisecond) {
					return
				}
				last = p.currentFrameTime
				if err := semanticCheckRanking(p.viewSnapshot()); err != nil && rowsErr == nil {
					rowsErr = fmt.Errorf("ranking at %s: %w", p.currentFrameTime.UTC().Format(time.RFC3339Nano), err)
				}
				view := p.viewEffects()
				if err := view.Counts.accountingError(len(view.Active)); err != nil && rowsErr == nil {
					rowsErr = err
				}
				if err := semanticCheckRows(view); err != nil && rowsErr == nil {
					rowsErr = fmt.Errorf("at %s: %w", p.currentFrameTime.UTC().Format(time.RFC3339Nano), err)
				}
				record := func(row ViewEffectRow, polarity EffectPolarity) {
					for _, effect := range row.Effects {
						id := *effect.EffectID
						if reason, never := semanticNeverListed[id]; never && rowsErr == nil {
							rowsErr = fmt.Errorf("effect %d (%s) was listed", id, reason)
						}
						entry := seen[id]
						if entry == nil {
							entry = &semanticEffectSeen{row: polarity, target: effect.Display.Target}
							seen[id] = entry
						}
						if effect.StackCount != nil && *effect.StackCount > entry.maxStack {
							entry.maxStack = *effect.StackCount
						}
					}
				}
				record(view.Buffs, EffectPolarityBuff)
				record(view.Debuffs, EffectPolarityDebuff)
			})
			p.finishCapture()
			if rowsErr != nil {
				t.Fatal(rowsErr)
			}
			for _, fact := range semanticEffectFacts {
				if !semanticCaptureMatches(capture.name, fact.capture) {
					continue
				}
				got := seen[fact.effectID]
				if got == nil {
					t.Errorf("effect %d was never listed", fact.effectID)
					continue
				}
				if got.row != fact.row || got.target != fact.target || got.maxStack != fact.maxStack {
					t.Errorf("effect %d = %+v, want row %s target %s max stack %d", fact.effectID, *got, fact.row, fact.target, fact.maxStack)
				}
			}
			statistics := p.damageStatisticsSnapshot()
			if got := intervalDamageCompleteness(statistics); !got.Conserved {
				t.Errorf("interval damage is not conserved: %+v", got)
			}
			encounters := append([]EncounterSnapshot(nil), statistics.Encounters.Completed...)
			if statistics.Encounters.Current != nil {
				encounters = append(encounters, *statistics.Encounters.Current)
			}
			bySequence := make(map[uint64]EncounterSnapshot, len(encounters))
			for _, encounter := range encounters {
				bySequence[encounter.Sequence] = encounter
				got := encounterDamageCompleteness(encounter)
				if !got.Conserved {
					t.Errorf("encounter %d is not conserved: %+v", encounter.Sequence, got)
				}
				if got.RoleAttributionComplete != (encounter.UnassignedRole.OutgoingEvents == 0) {
					t.Errorf("encounter %d attribution does not follow its unassigned bucket: %+v", encounter.Sequence, got)
				}
			}
			for _, fact := range semanticCompletenessFacts {
				if !semanticCaptureMatches(capture.name, fact.capture) {
					continue
				}
				encounter, ok := bySequence[fact.sequence]
				if !ok {
					t.Errorf("encounter %d is missing", fact.sequence)
					continue
				}
				var vehicle int64
				if encounter.Vehicle != nil {
					vehicle = encounter.Vehicle.DamageDealt
				}
				got := encounterDamageCompleteness(encounter)
				if encounter.Totals.DamageDealt != fact.total || vehicle != fact.vehicle || got.UnknownOwnershipDamage != fact.unknownDamage || got.UnknownOwnershipEvents != fact.unknownEvents || !got.RoleAttributionComplete {
					t.Errorf("encounter %d = total %d, vehicle %d, %+v, want %+v", fact.sequence, encounter.Totals.DamageDealt, vehicle, got, fact)
				}
			}
			view := p.viewSnapshot()
			if err := semanticCheckRanking(view); err != nil {
				t.Errorf("final ranking: %v", err)
			}
			rows := map[string]ViewSkill{}
			collect := func(roles []ViewRole) {
				for _, role := range roles {
					for _, skill := range role.Skills {
						rows[fmt.Sprintf("%d/%d", role.RoleID, skill.SkillID)] = skill
					}
				}
			}
			collect(view.Roles)
			if view.Encounters.Current != nil {
				collect(view.Encounters.Current.Roles)
			}
			for _, encounter := range view.Encounters.Recent {
				collect(encounter.Roles)
			}
			for _, fact := range semanticSkillFacts {
				if !semanticCaptureMatches(capture.name, fact.capture) {
					continue
				}
				got, ok := rows[fmt.Sprintf("%d/%d", fact.roleID, fact.skillID)]
				if !ok {
					t.Errorf("role %d has no row for skill %d", fact.roleID, fact.skillID)
					continue
				}
				if got.NameStatus != fact.status || got.NameSource != fact.source || got.ShowSkillID != fact.showID || (fact.name != "" && got.Name != fact.name) {
					t.Errorf("role %d skill %d = %q %s/%s show %v, want %q %s/%s show %v", fact.roleID, fact.skillID, got.Name, got.NameStatus, got.NameSource, got.ShowSkillID, fact.name, fact.status, fact.source, fact.showID)
				}
			}
			for key, skill := range rows {
				switch skill.NameStatus {
				case "VERIFIED":
					if skill.ShowSkillID || skill.Name == "" {
						t.Errorf("verified row %s = %+v", key, skill)
					}
				case "DERIVED":
					if !skill.ShowSkillID || skill.Name == "" || skill.NameSource == "" {
						t.Errorf("derived row %s = %+v", key, skill)
					}
				default:
					if !skill.ShowSkillID || skill.Name != "" {
						t.Errorf("unknown row %s = %+v", key, skill)
					}
				}
			}
		})
	}
	var absent []string
	for _, fact := range semanticEffectFacts {
		if !covered(fact.capture) {
			absent = append(absent, fact.capture)
		}
	}
	for _, fact := range semanticSkillFacts {
		if !covered(fact.capture) {
			absent = append(absent, fact.capture)
		}
	}
	if len(absent) != 0 {
		t.Logf("expectations skipped, captures absent: %v", absent)
	}
}
