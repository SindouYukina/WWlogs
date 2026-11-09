//go:build wwdev

package capture

import (
	"fmt"
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
			view := p.viewSnapshot()
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
