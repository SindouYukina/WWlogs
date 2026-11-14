//go:build wwdev

package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
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

// semanticEffectFact is one status that must be listed at some point of a capture: in the row of the Overlay, with the
// polarity of its cell (the row's own when empty), the target and the highest stack it showed.
type semanticEffectFact struct {
	capture  string
	effectID uint64
	row      EffectPolarity
	target   EffectDisplayTarget
	maxStack uint64
	polarity EffectPolarity
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

// The golden corpus of the current game version (Global 3.8.9, 2026-11-12). LIVE_20261112_110155 is a real capture of
// one fight of role 1409 and one of a trial team: the new role 1512 (TrialRoleInfo 15127004) with trial roles 13117004
// and 13127004. SYNTH-MISSED-LOGIN-RELOGIN-3.8 is the missed-login traffic of LIVE_20261112_105901 followed by that
// capture's relogin, so it must show the same facts. The facts are cross-checked outside the product's combat decoders
// (research/runs/2026-11-12/v0.7.0-3.8-compatibility): every damage value against the generic schema decode and the
// targets' server Life attribute, attacker and target against the request, stacks against the effect messages, and
// names against the client's skill tables. The Global 3.7 facts are kept there too; those captures are no longer decoded.
var currentGoldenCaptures = []string{"LIVE_20261112_110155", "SYNTH-MISSED-LOGIN-RELOGIN-3.8"}

// build29LiveCapture is the owner's Build 29 LIVE acceptance capture of Global 3.8.9 (2026-11-13): six attempts at one
// challenge, each opened by its own JoinSceneNotify and CountDownChallenge; the first five with roles 1102, 1312 and the
// owned role 1512, the sixth with 1209, 1409 and 1508. Its facts are checked outside the product's combat decoders
// (research/runs/2026-11-13/v0.7.0-live-3.8.9-archival): all 777 damage responses against the generic schema decode and
// their requests, each encounter re-added from the schema Damage between consecutive JoinSceneNotify frames with the
// direction taken from the server's EntityType, every target Life step explained, and stacks from the effect messages.
const build29LiveCapture = "LIVE_20261113_003512"

// build28LiveCapture is the owner's Build 28 LIVE capture of Global 3.8.9 (2026-11-12, 23:07-23:57): 30 encounters in 14
// scene windows, roles 1102, 1409 and 1508 for most of them, then 1311, 1312 and 1512 and trial teams. About a fifth of its
// confirmed damage came in standalone CombatReceivePackNotify responses, which made it the decisive sample for reading
// both containers. Its facts are checked outside the product's combat decoders
// (research/runs/2026-11-13/v0.7.0-damage-completeness): all 3,715 responses of both containers against the generic
// schema decode and their requests, the direction of every confirmed result against the server's EntityType, every
// attacker against the role entities of the server's JoinSceneNotify and EntityAddNotify, every non-zero ChangeLife
// against an equal step of its target's Life, and each encounter against the local player's battle intervals from the
// server's own PlayerBattleStateChangeNotify, cut at every JoinSceneNotify.
const build28LiveCapture = "LIVE_20261112_230754"

// build31LiveCapture is the owner's Build 31 LIVE capture of Global 3.8.9 (2026-11-13, 22:42-23:33): 18 encounters in the
// Tower of Adversity (V1-x-y), the Whimpering Wastes' Endless Torrents halves, a Tactical Hologram, a Tacet field, Lily's
// character tutorial and the open world, with roles 1102, 1311, 1312, 1505 and 1512 among others. It is the first capture
// in which the client's combat RequestId counter wraps (after 46,399 combat requests), and the first with Lily's statuses
// that the HUD draws without an arrow. Its facts are checked outside the product's decoders
// (research/runs/2026-11-14/v0.7.0-build31-live-findings).
const build31LiveCapture = "LIVE_20261113_224240"

// currentGoldenFacts states the same facts for each capture of the golden corpus.
func currentGoldenFacts[T any](facts func(capture string) []T) []T {
	var all []T
	for _, capture := range currentGoldenCaptures {
		all = append(all, facts(capture)...)
	}
	return all
}

var semanticEffectFacts = append(currentGoldenFacts(func(capture string) []semanticEffectFact {
	return []semanticEffectFact{
		// Monster Glacio a monster puts on the local player (ApplyGameplayEffectPush, StackCount 1).
		{capture, 10010000, EffectPolarityDebuff, EffectTargetTeam, 1, ""},
		// Aero Erosion role 1409 puts on a monster: OrderApplyBuffRequest from bullet 1409004021, then
		// ApplyGameplayEffectNotify on the monster with StackCount 1.
		{capture, 10041000, EffectPolarityDebuff, EffectTargetEnemy, 1, ""},
	}
}),
	// In encounter 6 role 1409 stacks Aero Erosion on the boss to 8 and role 1508 effect 10061000 to 6, both above the
	// buff table's StackLimitCount 3: ApplyGameplayEffectNotify opens each handle on the monster (EntityType 2) and
	// BuffStackCountNotify on that handle raises NewStackCount. Both buffs have a type 2 HUD cue with the same direction
	// parameter.
	semanticEffectFact{build29LiveCapture, 10041000, EffectPolarityDebuff, EffectTargetEnemy, 8, ""},
	semanticEffectFact{build29LiveCapture, 10061000, EffectPolarityDebuff, EffectTargetEnemy, 6, ""},
	// Statuses the HUD draws with their icon and without an arrow, listed in the first row as neutral cells. The game draws
	// them (Global 3.8.9: type 2 or 24 cues with a Path and an empty Parameters vector in the hotfix db_buffeffect.db;
	// BuffItem.Activate draws the icon and only an arrow for "1" or "2"). In the capture they come in the local client's
	// own ApplyGameplayEffectPush, checked outside the product's effect decoder
	// (work/live-build31/client_status_crosscheck.py): holder by CombatCommon.EntityId against the role entities of the
	// server's JoinSceneNotify and EntityAddNotify, stacks from the apply and BuffStackCountPush.NewStackCount on the same
	// HandleId, instigator role 1512. 1512310003 is on teammates 1311 and 1312 (stack 2 at most), 1512999015 on Lily
	// herself, 2002205502 on every team member, 1512800104 on the local player's PlayerEntity (EntityType 11, which
	// RoleBuffView reads too; stack 4 at most), and Xin's 1311400007 and 1311400008 on Xin.
	semanticEffectFact{build31LiveCapture, 1512310003, EffectPolarityBuff, EffectTargetTeam, 2, EffectPolarityNeutral},
	semanticEffectFact{build31LiveCapture, 1512800104, EffectPolarityBuff, EffectTargetTeam, 4, EffectPolarityNeutral},
	semanticEffectFact{build31LiveCapture, 1512999015, EffectPolarityBuff, EffectTargetTeam, 1, EffectPolarityNeutral},
	semanticEffectFact{build31LiveCapture, 2002205502, EffectPolarityBuff, EffectTargetTeam, 1, EffectPolarityNeutral},
	semanticEffectFact{build31LiveCapture, 1311400007, EffectPolarityBuff, EffectTargetTeam, 1, EffectPolarityNeutral},
	semanticEffectFact{build31LiveCapture, 1311400008, EffectPolarityBuff, EffectTargetTeam, 1, EffectPolarityNeutral},
)

// Effects that must never be listed: logic flags and refresh-period modifiers the game draws no buff-bar item for. Role
// statuses whose HUD cue carries no direction were listed here until 2026-11-14; the game draws them without an arrow,
// so they are now facts of the first row above.
var semanticNeverListed = map[uint64]string{
	1101003023: "dodge invulnerability flag",
	1409401095: "six-element fan-out modifier",
	1508952003: "six-element fan-out modifier",
	3098:       "fall damage immunity",
}

var semanticSkillFacts = append(currentGoldenFacts(func(capture string) []semanticSkillFact {
	return []semanticSkillFact{
		{capture, 1409, 1409021, "VERIFIED", "official", "此劍，為潮水的過去", false},
		// Trial roles name their rows through their parent role (TrialRoleInfo.ParentId): every DamageId of these rows
		// is listed by a UI skill of the parent, and starts with the parent's RoleId.
		{capture, 13127004, 1312031, "VERIFIED", "official", "攏傘形·閃裂", false},
		{capture, 13127004, 1312050, "DERIVED", "damage_list", "暝傘形·重鎖獄瘴", true},
		{capture, 13117004, 13111302, "DERIVED", "damage_list", "乘興一顧", true},
		{capture, 15127004, 15124104, "DERIVED", "damage_list", "花滿夢土", true},
		{capture, 15127004, 15124103, "DERIVED", "damage_list", "花滿夢土", true},
		{capture, 15127004, 15124002, "DERIVED", "damage_list", "夢愈淨瑕", true},
		{capture, 15127004, 15126001, "DERIVED", "damage_list", "幻夢調和", true},
		{capture, 15127004, 15129001, "DERIVED", "damage_list", "綺夢綻香", true},
		{capture, 15127004, 15128001, "DERIVED", "damage_list", "墜於幻夢搖籃", true},
		{capture, 15127004, 20015301, "DERIVED", "echo", "解形煞", true},
	}
}),
	// Lily's (role 1512) skill IDs the Build 31 capture adds to the corpus, owned and in her character tutorial (trial
	// role 100668, TrialRoleInfo.ParentId 1512). Each row's DamageId is listed by exactly one UI skill of role 1512 in the
	// 3.8.9 client's skill tables (skill_damage_ref): 151280022 only by 1006306, 151250032 only by 1006307, 151260012 only
	// by 1006301, whose official names these are. No hit of hers in the capture lacks a SkillId except effect healing.
	semanticSkillFact{build31LiveCapture, 1512, 15129002, "DERIVED", "damage_list", "綺夢綻香", true},
	semanticSkillFact{build31LiveCapture, 1512, 15124202, "DERIVED", "damage_list", "花滿夢土", true},
	semanticSkillFact{build31LiveCapture, 100668, 15124201, "DERIVED", "damage_list", "夢愈淨瑕", true},
)

// semanticNoSkillFact is what a capture's confirmed damage without a SkillId must show for one role. The damage and event
// counts stay exactly the no-SkillId damage of the capture: a name never moves a hit out of the bucket.
type semanticNoSkillFact struct {
	capture string
	roleID  uint64
	name    string
	source  string
	damage  int64
	events  uint64
}

var semanticNoSkillFacts = []semanticNoSkillFact{}

// semanticCompletenessFact is what one encounter must report about its own damage.
type semanticCompletenessFact struct {
	capture       string
	sequence      uint64
	total         int64
	vehicle       int64
	unknownDamage int64
	unknownEvents uint64
	// negativeStatus is the local team's negative-status damage the server executed through the scene's combat entity
	// (DamageExecuteNotify, DamageId of an AbnormalDamageConfig column): no role's, and the whole unassigned bucket.
	negativeStatus int64
}

// Encounter 1 is role 1409 with a motorcycle; encounter 2 is the trial team. The six monster-on-monster hits of the
// capture (1,675) belong to neither encounter. Encounter 2 includes the 11 server responses (80,862) that arrive in a
// standalone CombatReceivePackNotify; each answers a request with the same attacker and target and equals a step of its
// target's Life. Until 2026-11-13 the decoder read responses only from CombatSendPackResponse and this fact was the
// undercounted 514,319. The trial team's encounter spans three consecutive server battle intervals that the product's
// re-entry rule joins (segment sums 128,702, 202,312 and 264,167).
//
// Server-executed damage (DamageExecuteNotify, counted since 2026-11-14) is in every total below. Each notified result is
// checked outside the product's decoders (research/runs/2026-11-14/v0.7.0-damage-execute-notify-client-re,
// work/damage-execute-notify/push_audit.py and classify_pushes.py): its value is the generic schema decode's Damage; its
// attacker is a role entity of the server's JoinSceneNotify FightRoleInfos or the scene's SceneInformation.SceneEntityId,
// its target an EntityType 2 monster; no counted response explains it (no equal value on the target or equal attacker and
// DamageId within 2 s whose Life step is not its own, and no notified DamageId ever appears in a client request); a
// negative-status result's target holds a status instance of the executing extra effect type applied by a local role
// (server ApplyGameplayEffectNotify, BuffStackCountNotify and BuffDurationNotify by handle) in a scene of the local player
// alone; and every notified result with a ChangeLife has a Life step of its own after the responses take theirs. Here:
// encounter 1 +6,379 (role 1409's DamageId 1000009900, 5,633, and one Aero Erosion tick, 746), encounter 2 +41,368 (trial
// roles 13127004, 15127004 and 13117004, DamageId 1000009900).
var semanticCompletenessFacts = append(append(currentGoldenFacts(func(capture string) []semanticCompletenessFact {
	return []semanticCompletenessFact{
		{capture, 1, 355025, 78716, 0, 0, 746},
		{capture, 2, 636549, 0, 0, 0, 0},
	}
}),
	// The six encounters of the Build 29 capture; every hit comes from a role entity, none from a vehicle, and each
	// encounter is one scene window and one server battle interval. Encounter 6 includes two responses of role 1409 to
	// the boss (71,224 and 41,208) that arrive in a standalone CombatReceivePackNotify. Notified results (see above) add
	// 154,020 to encounter 6: role 1508's 61,803 and Aero Erosion 92,217 (30 ticks on statuses role 1409 applied; the
	// research audit's fixed 3 s lifetime missed the live instance of one, the effect tracker's refreshed duration did not).
	semanticCompletenessFact{build29LiveCapture, 1, 2239230, 0, 0, 0, 0},
	semanticCompletenessFact{build29LiveCapture, 2, 2661425, 0, 0, 0, 0},
	semanticCompletenessFact{build29LiveCapture, 3, 1290079, 0, 0, 0, 0},
	semanticCompletenessFact{build29LiveCapture, 4, 3558115, 0, 0, 0, 0},
	semanticCompletenessFact{build29LiveCapture, 5, 3039257, 0, 0, 0, 0},
	semanticCompletenessFact{build29LiveCapture, 6, 4181890, 0, 0, 0, 92217},
),
	// The 30 encounters of the Build 28 capture; every confirmed hit comes from a role entity, none from a vehicle.
	// Encounters 3, 9, 16 and 17 each join consecutive server battle intervals by the product's re-entry rule (an empty
	// interval lies inside encounter 9); the others are one interval each. The 69 hits of encounter 24 (trial roles
	// 100605 and 100668) leave their targets' Life unchanged (ChangeLife 0), so only the generic decode and the requests
	// check them. Standalone responses contribute to 15 of the encounters, 50,833,133 of them to encounter 15. Notified
	// results (see above) add 10,158,976: 62 hits of roles 1508, 1505, 1311 and trial roles (2,492,752) and 636
	// negative-status ticks (Aero Erosion 1001 and Spectro Frazzle 1005, 7,666,224) on statuses role 1409 applied; three
	// Spectro Frazzle ticks of encounters 5, 14 and 15 (17,173) came while the status was refreshed in the same frame and
	// removed seconds later.
	semanticCompletenessFact{build28LiveCapture, 1, 168148, 0, 0, 0, 727},
	semanticCompletenessFact{build28LiveCapture, 2, 197195, 0, 0, 0, 9238},
	semanticCompletenessFact{build28LiveCapture, 3, 803095, 0, 0, 0, 22764},
	semanticCompletenessFact{build28LiveCapture, 4, 568753, 0, 0, 0, 27135},
	semanticCompletenessFact{build28LiveCapture, 5, 1662887, 0, 0, 0, 36596},
	semanticCompletenessFact{build28LiveCapture, 6, 571466, 0, 0, 0, 8668},
	semanticCompletenessFact{build28LiveCapture, 7, 606702, 0, 0, 0, 13666},
	semanticCompletenessFact{build28LiveCapture, 8, 958964, 0, 0, 0, 9041},
	semanticCompletenessFact{build28LiveCapture, 9, 5237706, 0, 0, 0, 14724},
	semanticCompletenessFact{build28LiveCapture, 10, 2037906, 0, 0, 0, 38884},
	semanticCompletenessFact{build28LiveCapture, 11, 6505727, 0, 0, 0, 58655},
	semanticCompletenessFact{build28LiveCapture, 12, 2349349, 0, 0, 0, 12662},
	semanticCompletenessFact{build28LiveCapture, 13, 15504321, 0, 0, 0, 98061},
	semanticCompletenessFact{build28LiveCapture, 14, 13224089, 0, 0, 0, 1299394},
	semanticCompletenessFact{build28LiveCapture, 15, 306611253, 0, 0, 0, 5943391},
	semanticCompletenessFact{build28LiveCapture, 16, 883756, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 17, 1289458, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 18, 775586, 0, 0, 0, 22457},
	semanticCompletenessFact{build28LiveCapture, 19, 801006, 0, 0, 0, 7659},
	semanticCompletenessFact{build28LiveCapture, 20, 776286, 0, 0, 0, 9425},
	semanticCompletenessFact{build28LiveCapture, 21, 777776, 0, 0, 0, 5999},
	semanticCompletenessFact{build28LiveCapture, 22, 781738, 0, 0, 0, 7605},
	semanticCompletenessFact{build28LiveCapture, 23, 786617, 0, 0, 0, 19473},
	semanticCompletenessFact{build28LiveCapture, 24, 40936, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 25, 207131, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 26, 51662, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 27, 789843, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 28, 805035, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 29, 778104, 0, 0, 0, 0},
	semanticCompletenessFact{build28LiveCapture, 30, 774638, 0, 0, 0, 0},
	// The 18 encounters the engine finished in the Build 31 capture (it discarded a 19th, sequence 18). Checked outside the
	// product's combat decoders with work/live-build31/completeness_audit_wrap.py on the generic schema decode: all 9,069
	// responses of both containers keyed by their RequestId continued past the wrap of the client's counter and paired
	// with the latest request of that RequestId; every value, attacker and target equal to the decode and the request; the
	// direction from the server's EntityType; every outgoing attacker a role entity of the server's JoinSceneNotify or
	// EntityAddNotify, with the role split equal per encounter, except the vehicle (EntityType 10, ConfigId 36000052) in
	// encounters 17 and 19; 8,395 of the 8,397 results with a ChangeLife equal to a step of their target's Life (encounter
	// 13, trial roles 100605 and 100668 in Lily's character tutorial, leaves Life unchanged); each encounter one server
	// battle interval except 15 and 16, which join three consecutive intervals each by the re-entry rule. The counter
	// wrapped during encounter 11 (frame 54844); until 2026-11-14 the product dropped the 675 results whose RequestId was
	// used a second time, and encounters 11 to 19 read lower (encounter 12: 34,205,192). Notified results (see the note
	// above build29LiveCapture's facts) add 8,995,667: 157 hits of local roles (3,807,966; 154 with a Life step of their
	// own, 3 with Life unchanged) and 549 Aero Erosion ticks (5,187,701; 538 with a Life step) on statuses role 1409
	// applied, the negativeStatus column. Before 2026-11-14 the facts excluded them (encounter 7: 9,177,422).
	semanticCompletenessFact{build31LiveCapture, 1, 5510829, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 2, 5635117, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 3, 5976286, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 4, 11612715, 0, 0, 0, 1042266},
	semanticCompletenessFact{build31LiveCapture, 5, 6187588, 0, 0, 0, 587199},
	semanticCompletenessFact{build31LiveCapture, 6, 2700158, 0, 0, 0, 279397},
	semanticCompletenessFact{build31LiveCapture, 7, 10122800, 0, 0, 0, 945378},
	semanticCompletenessFact{build31LiveCapture, 8, 3998523, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 9, 3493921, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 10, 18459692, 0, 0, 0, 806830},
	semanticCompletenessFact{build31LiveCapture, 11, 34983860, 0, 0, 0, 1500447},
	semanticCompletenessFact{build31LiveCapture, 12, 43889235, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 13, 47918, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 14, 7960715, 0, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 15, 862762, 0, 0, 0, 19418},
	semanticCompletenessFact{build31LiveCapture, 16, 920878, 0, 0, 0, 6766},
	semanticCompletenessFact{build31LiveCapture, 17, 237931, 47230, 0, 0, 0},
	semanticCompletenessFact{build31LiveCapture, 19, 936031, 47105, 0, 0, 0},
)

type semanticEffectSeen struct {
	row      EffectPolarity
	polarity EffectPolarity
	target   EffectDisplayTarget
	maxStack uint64
}

func semanticCheckRows(view ViewEffects) error {
	check := func(row ViewEffectRow, polarity EffectPolarity) error {
		var previous time.Time
		timedDone := false
		for _, effect := range row.Effects {
			// The first row also holds the statuses the HUD draws without an arrow; each keeps its own polarity.
			rowPolarity := effect.Polarity == polarity || polarity == EffectPolarityBuff && effect.Polarity == EffectPolarityNeutral
			if !rowPolarity || !effect.HUDIcon || effect.Display.Eligibility != EffectDisplayEligible {
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
		if unassigned.OutgoingEvents > 0 && (unassigned.Name == "") == (unassigned.UnnamedReason == "") {
			return fmt.Errorf("%s: name %q with unnamed reason %q", label, unassigned.Name, unassigned.UnnamedReason)
		}
		if unassigned.Name != "" && unassigned.NameSource == "" {
			return fmt.Errorf("%s: name %q has no source", label, unassigned.Name)
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
	coverage := semanticCoverage{GameVersion: CurrentGameVersion(), Captures: []semanticCoverageCapture{}, Absent: []string{}}
	for _, capture := range pcaps {
		capture := capture
		ran := semanticCoverageCapture{Name: capture.name, SHA256: semanticFileSHA256(t, capture.path), Facts: semanticFactCounts(capture.name)}
		ran.Passed = t.Run(capture.name, func(t *testing.T) {
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
							entry = &semanticEffectSeen{row: polarity, polarity: effect.Polarity, target: effect.Display.Target}
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
			// The listed statuses are logged for review of new facts and of failures.
			listed := make([]uint64, 0, len(seen))
			for id := range seen {
				listed = append(listed, id)
			}
			sort.Slice(listed, func(i, j int) bool { return listed[i] < listed[j] })
			for _, id := range listed {
				t.Logf("listed effect %d: row %s, polarity %s, target %s, max stack %d", id, seen[id].row, seen[id].polarity, seen[id].target, seen[id].maxStack)
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
				polarity := fact.polarity
				if polarity == "" {
					polarity = fact.row
				}
				if got.row != fact.row || got.polarity != polarity || got.target != fact.target || got.maxStack != fact.maxStack {
					t.Errorf("effect %d = %+v, want row %s polarity %s target %s max stack %d", fact.effectID, *got, fact.row, polarity, fact.target, fact.maxStack)
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
				if got.RoleAttributionComplete != (encounter.UnassignedRole.OutgoingEvents == encounter.UnassignedRole.NegativeStatusEvents) {
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
				if encounter.Totals.DamageDealt != fact.total || vehicle != fact.vehicle || got.UnknownOwnershipDamage != fact.unknownDamage || got.UnknownOwnershipEvents != fact.unknownEvents || !got.RoleAttributionComplete ||
					encounter.UnassignedRole.NegativeStatusDamage != fact.negativeStatus || encounter.UnassignedRole.DamageDealt != fact.negativeStatus {
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
			// The capture's skill rows are logged for review of new facts, as the listed statuses are.
			for _, role := range view.Roles {
				for _, skill := range role.Skills {
					t.Logf("skill row %d/%d: %q %s/%s show %v icon %s/%d, %d in %d hits", role.RoleID, skill.SkillID, skill.Name, skill.NameStatus, skill.NameSource, skill.ShowSkillID, skill.IconKind, skill.IconID, skill.DamageDealt, skill.OutgoingEvents)
				}
				if unassigned := role.UnassignedSkill; unassigned.OutgoingEvents > 0 {
					t.Logf("skill row %d/none: %q %s/%s, %d in %d hits", role.RoleID, unassigned.Name, unassigned.NameStatus, unassigned.NameSource, unassigned.DamageDealt, unassigned.OutgoingEvents)
				}
			}
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
			noSkill := map[uint64]ViewUnassignedSkill{}
			for _, role := range view.Roles {
				noSkill[role.RoleID] = role.UnassignedSkill
			}
			for _, fact := range semanticNoSkillFacts {
				if !semanticCaptureMatches(capture.name, fact.capture) {
					continue
				}
				got, ok := noSkill[fact.roleID]
				if !ok {
					t.Errorf("role %d is missing from the view", fact.roleID)
					continue
				}
				if got.Name != fact.name || got.NameStatus != "DERIVED" || got.NameSource != fact.source || got.DamageDealt != fact.damage || got.OutgoingEvents != fact.events || got.MissingSkillEvents != fact.events || got.UnnamedReason != "" {
					t.Errorf("role %d no-skill row = %q %s/%s %d/%d (%d missing) reason %q, want %+v", fact.roleID, got.Name, got.NameStatus, got.NameSource, got.DamageDealt, got.OutgoingEvents, got.MissingSkillEvents, got.UnnamedReason, fact)
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
		coverage.Captures = append(coverage.Captures, ran)
	}
	var absent []string
	note := func(capture string) {
		if !covered(capture) && !slices.Contains(absent, capture) {
			absent = append(absent, capture)
		}
	}
	for _, fact := range semanticEffectFacts {
		note(fact.capture)
	}
	for _, fact := range semanticSkillFacts {
		note(fact.capture)
	}
	for _, fact := range semanticNoSkillFacts {
		note(fact.capture)
	}
	for _, fact := range semanticCompletenessFacts {
		note(fact.capture)
	}
	if len(absent) != 0 {
		t.Logf("expectations skipped, captures absent: %v", absent)
		coverage.Absent = absent
	}
	// The coverage tells the version readiness gate which Golden captures actually ran (scripts/readiness).
	if out := os.Getenv("WWLOGS_SEMANTIC_REPLAY_COVERAGE"); out != "" {
		data, err := json.MarshalIndent(coverage, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// semanticCoverage records which captures the collector ran, how many Golden facts each was checked against, and whether
// they held. Captures the facts name but the run did not have are absent.
type semanticCoverage struct {
	GameVersion string                    `json:"gameVersion"`
	Captures    []semanticCoverageCapture `json:"captures"`
	Absent      []string                  `json:"absent"`
}

type semanticCoverageCapture struct {
	Name   string         `json:"name"`
	SHA256 string         `json:"sha256"`
	Facts  map[string]int `json:"facts"`
	Passed bool           `json:"passed"`
}

// semanticFactCounts counts the Golden facts of each kind that apply to a capture.
func semanticFactCounts(name string) map[string]int {
	counts := map[string]int{}
	for _, fact := range semanticEffectFacts {
		if semanticCaptureMatches(name, fact.capture) {
			counts["effects"]++
		}
	}
	for _, fact := range semanticSkillFacts {
		if semanticCaptureMatches(name, fact.capture) {
			counts["skills"]++
		}
	}
	for _, fact := range semanticNoSkillFacts {
		if semanticCaptureMatches(name, fact.capture) {
			counts["noSkill"]++
		}
	}
	for _, fact := range semanticCompletenessFacts {
		if semanticCaptureMatches(name, fact.capture) {
			counts["completeness"]++
		}
	}
	return counts
}

func semanticFileSHA256(t *testing.T, path string) string {
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
