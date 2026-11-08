package capture

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Display eligibility is the product's one rule for which tracked effect instances the Effects Overlay lists. It reads
// the tracker's snapshot and never changes it: an instance that is not eligible stays tracked, keeps its raw identity in
// the development diagnostics, and is still counted under the reason it was left out.

// EffectKind is what an effect definition says the effect is for. It is decided from the Master definition (glossary
// term, attribute modifier, developer GameplayTag path) and the runtime relation, never from an effect ID list. Every
// kind except unknown rests on correlated evidence: the tag paths are the game's own developer text, not an engine key,
// so a kind is a classification with a named basis, not a verified game rule.
type EffectKind string

const (
	// EffectKindElementAilment is an element ailment with an official glossary term.
	EffectKindElementAilment EffectKind = "element_ailment"
	// EffectKindStatModifier changes a combat attribute (attack, crit, damage bonus, resistance, energy...).
	EffectKindStatModifier EffectKind = "stat_modifier"
	// EffectKindBuffMarker carries a developer tag that marks it as a role's buff (a "buff标识" or "BUFF通用标识" segment).
	EffectKindBuffMarker EffectKind = "buff_marker"
	// EffectKindCombatState has a combat word in a developer tag outside the internal-state families.
	EffectKindCombatState EffectKind = "combat_state"
	// EffectKindHUDEffect has no more specific kind, but the game draws it in a HUD buff bar: its definition lists a
	// GameplayCue of a HUD type with an icon (effect_hud_cues.go). This is the client's own presentation evidence that
	// the player sees the effect; its meaning and polarity stay unknown.
	EffectKindHUDEffect EffectKind = "hud_effect"
	// EffectKindRuntimeLink has no classifying definition, but the local team put it on a monster or a monster on the
	// local team. Its meaning is unknown; only its combat context is established, so it is counted and never listed.
	EffectKindRuntimeLink EffectKind = "runtime_link"
	// EffectKindInternalState is a logic flag, skill or input restriction, process or state marker, or camera state, by
	// its developer tag family (for example 功能.逻辑状态标识, 战斗状态.技能限制, <role>.状态标识). It is part of how a
	// skill runs, not a status a player tracks.
	EffectKindInternalState EffectKind = "internal_state"
	// EffectKindNonCombat has non-combat evidence and no combat evidence (EffectCombatClassNonCombat).
	EffectKindNonCombat EffectKind = "non_combat"
	// EffectKindUnknown has no classifying evidence. It is never a statement that the effect is irrelevant.
	EffectKindUnknown EffectKind = "unknown"
)

// Developer-tag path segments of the internal-state families. A segment must equal one of these words, except that any
// segment naming a camera (镜头) is a presentation state.
var internalStateTagSegments = map[string]bool{
	"逻辑状态标识": true, "逻辑状态": true, "状态标识": true, "状态通用标识": true, "过程标识": true, "角色标识": true, "技能通用标识": true,
	"技能限制": true, "行为限制": true, "输入限制": true, "AI仇恨": true, "行为改变": true, "功能制作": true,
}

// Developer-tag path segments that mark an effect as a role's buff, compared in lower case.
var buffMarkerTagSegments = map[string]bool{"buff标识": true, "buff通用标识": true}

func tagSegmentMatch(tags []string, match func(segment string) bool) string {
	for _, tag := range tags {
		for _, segment := range strings.Split(tag, ".") {
			if match(segment) {
				return segment
			}
		}
	}
	return ""
}

// Kind bases name the evidence; a tag basis appends the matching segment.
const (
	effectKindBasisTerm         = "element_ailment_term"
	effectKindBasisAttribute    = "combat_attribute_modifier"
	effectKindBasisBuffMarker   = "developer_tag_buff_marker:"
	effectKindBasisNonCombat    = "non_combat_evidence"
	effectKindBasisHUDCue       = "hud_buff_bar_cue"
	effectKindBasisInternalTag  = "developer_tag_internal_family:"
	effectKindBasisCombatTag    = "developer_tag_combat_word"
	effectKindBasisRuntimeLink  = "runtime_monster_link"
	effectKindBasisNoEvidence   = "no_classifying_evidence"
	effectKindBasisNoDefinition = "definition_unresolved"
)

// classifyEffectKind derives the kind from an instance's combat classification and its definition's developer tags.
// Combat evidence wins over non-combat evidence, as in the combat class. The order is: a term, then an attribute modifier
// that names a logic state, then a combat attribute modifier, then a buff marker, then non-combat evidence, then a HUD
// buff-bar cue, then an internal-state family, then other combat words, then the runtime link. A HUD cue never replaces
// a more specific kind: an effect without one keeps whatever its other evidence says.
//
// An attribute modifier is only a status when nothing says otherwise. A definition that changes an attribute and whose
// developer tag names a logic state (super armor, a charging flag) is part of how a skill runs, unless the game draws it
// in a HUD buff bar.
func classifyEffectKind(class EffectCombatClass, basis string, static *EffectStaticInfo) (EffectKind, string) {
	var tags []string
	if static != nil {
		tags = static.DeveloperTags
	}
	if basis == effectCombatBasisTerm {
		return EffectKindElementAilment, effectKindBasisTerm
	}
	if class == EffectCombatClassCombat && strings.HasPrefix(basis, effectCombatBasisAttribute) {
		if static == nil || len(static.HUDCueTypes) == 0 {
			if segment := tagSegmentMatch(tags, func(s string) bool { return internalStateTagSegments[s] || strings.Contains(s, "镜头") }); segment != "" {
				return EffectKindInternalState, effectKindBasisInternalTag + segment
			}
		}
		return EffectKindStatModifier, effectKindBasisAttribute
	}
	if segment := tagSegmentMatch(tags, func(s string) bool { return buffMarkerTagSegments[strings.ToLower(s)] }); segment != "" {
		return EffectKindBuffMarker, effectKindBasisBuffMarker + segment
	}
	if class == EffectCombatClassNonCombat {
		return EffectKindNonCombat, effectKindBasisNonCombat
	}
	if static != nil && len(static.HUDCueTypes) > 0 {
		return EffectKindHUDEffect, effectKindBasisHUDCue
	}
	if segment := tagSegmentMatch(tags, func(s string) bool { return internalStateTagSegments[s] || strings.Contains(s, "镜头") }); segment != "" {
		return EffectKindInternalState, effectKindBasisInternalTag + segment
	}
	switch {
	case class == EffectCombatClassCombat && strings.HasPrefix(basis, effectCombatBasisTag):
		return EffectKindCombatState, effectKindBasisCombatTag
	case basis == effectCombatBasisLocalOnMonster || basis == effectCombatBasisMonsterOnLocal:
		return EffectKindRuntimeLink, effectKindBasisRuntimeLink
	case basis == effectCombatBasisDefinitionMissing:
		return EffectKindUnknown, effectKindBasisNoDefinition
	}
	return EffectKindUnknown, effectKindBasisNoEvidence
}

// EffectDisplayEligibility says whether an effect instance is listed, and if not, why. Each instance has exactly one.
type EffectDisplayEligibility string

const (
	// EffectDisplayEligible is an active instance with a local end and a player-facing kind.
	EffectDisplayEligible EffectDisplayEligibility = "eligible"
	// EffectDisplayPredictedExpired is an instance past its expected expiry by more than effectDisplayExpiryGrace. Nothing
	// confirmed its end: it stays tracked and returns if the game refreshes it, but it is no longer shown as active.
	EffectDisplayPredictedExpired EffectDisplayEligibility = "predicted_expired"
	// EffectDisplayPassive is a permanent modifier: its definition never expires, the server reports no duration, and the
	// game draws no HUD icon for it. It is true of the holder for the whole scene, so it carries no information in combat.
	EffectDisplayPassive EffectDisplayEligibility = "passive"
	// EffectDisplayEstimatedExpired is an instance whose estimated expiry passed long ago without a REMOVE. The game
	// most likely ended it; the tracker keeps it only until a REMOVE or its bound.
	EffectDisplayEstimatedExpired EffectDisplayEligibility = "estimated_expired"
	// EffectDisplayNotLocal is an instance whose holder and source are both not proven to be the local team.
	EffectDisplayNotLocal EffectDisplayEligibility = "not_local"
	// EffectDisplayNonCombat is an instance of kind non_combat.
	EffectDisplayNonCombat EffectDisplayEligibility = "non_combat"
	// EffectDisplayInternal is an instance of kind internal_state.
	EffectDisplayInternal EffectDisplayEligibility = "internal"
	// EffectDisplayUnclassified is a local instance whose meaning is not established (kind unknown or runtime_link). It is
	// counted, never listed and never called internal: unknown is not evidence that an effect is irrelevant.
	EffectDisplayUnclassified EffectDisplayEligibility = "unclassified"
)

const (
	// effectDisplayExpiryGrace is how long an instance past its expected expiry stays shown while a refresh or a REMOVE
	// may still be on the way. It only affects what is listed; the tracker keeps the instance for effectOverdueRetention.
	effectDisplayExpiryGrace = 2 * time.Second
	// effectMaintainedDurationLimit is the longest server duration of a modifier treated as maintained rather than timed.
	// In the archived captures every display-eligible modifier at or under this length without a HUD icon is refreshed by
	// the game about once per duration, so its countdown shows a refresh period, not a time left.
	effectMaintainedDurationLimit = 2.0
	// effectCellExpiryTolerance bounds how far apart the expected expiries of one cell may lie.
	effectCellExpiryTolerance = 100 * time.Millisecond
	// effectDurationPolicyInfinite is Buff.DurationPolicy for a definition without an end. Observed: no instance of such a
	// definition ever reported a finite duration.
	effectDurationPolicyInfinite = 1
)

// EffectDisplayTarget says whom an effect is on, from the runtime relation of its holder.
type EffectDisplayTarget string

const (
	// EffectTargetTeam is held by a local role or an entity the local team owns.
	EffectTargetTeam EffectDisplayTarget = "team"
	// EffectTargetEnemy is held by a resolved non-player entity and was applied by the local team.
	EffectTargetEnemy EffectDisplayTarget = "enemy"
	// EffectTargetUnresolved is held by an entity whose relation the capture could not establish.
	EffectTargetUnresolved EffectDisplayTarget = "unresolved"
)

// EffectDisplay is the display decision for one instance.
type EffectDisplay struct {
	Eligibility EffectDisplayEligibility `json:"eligibility"`
	Target      EffectDisplayTarget      `json:"target,omitempty"`
}

// effectIsPassive reports a permanent modifier (EffectDisplayPassive). A buff marker counts only when it came from a scene
// snapshot, because a marker applied during combat records something that just happened.
func effectIsPassive(effect EffectInstanceSnapshot) bool {
	static := effect.Static
	if static == nil || static.DurationPolicy == nil || *static.DurationPolicy != effectDurationPolicyInfinite {
		return false
	}
	if len(static.HUDCueTypes) > 0 || effect.ServerDurationSeconds != nil {
		return false
	}
	return effect.Kind == EffectKindStatModifier || (effect.Kind == EffectKindBuffMarker && effect.Origin == EffectOriginSnapshot)
}

// effectIsMaintained reports a modifier the game keeps alive by refreshing a very short duration. It is shown without a
// countdown. A HUD icon proves the game presents it as a timed status, so an effect with one always keeps its time.
func effectIsMaintained(effect EffectInstanceSnapshot) bool {
	if effect.Kind != EffectKindStatModifier || effect.ServerDurationSeconds == nil {
		return false
	}
	if effect.Static != nil && len(effect.Static.HUDCueTypes) > 0 {
		return false
	}
	return *effect.ServerDurationSeconds <= effectMaintainedDurationLimit
}

// effectDisplayFor decides whether an instance is listed. The order of the checks fixes which reason an instance is
// counted under: an ended estimate first, then the local relation, then the kind, then whether a player-facing kind is
// permanent or already past its time.
func effectDisplayFor(effect EffectInstanceSnapshot) EffectDisplay {
	if effect.Lifecycle != EffectLifecycleActive {
		return EffectDisplay{Eligibility: EffectDisplayEstimatedExpired}
	}
	holderLocal, sourceLocal := localEffectRelation(effect.HolderRelation), localEffectRelation(effect.SourceRelation)
	if !holderLocal && !sourceLocal {
		return EffectDisplay{Eligibility: EffectDisplayNotLocal}
	}
	target := EffectTargetUnresolved
	switch {
	case holderLocal:
		target = EffectTargetTeam
	case effect.HolderRelation == EffectRelationNonPlayer:
		target = EffectTargetEnemy
	}
	switch effect.Kind {
	case EffectKindElementAilment, EffectKindStatModifier, EffectKindBuffMarker, EffectKindHUDEffect, EffectKindCombatState:
	case EffectKindNonCombat:
		return EffectDisplay{Eligibility: EffectDisplayNonCombat, Target: target}
	case EffectKindInternalState:
		return EffectDisplay{Eligibility: EffectDisplayInternal, Target: target}
	default:
		return EffectDisplay{Eligibility: EffectDisplayUnclassified, Target: target}
	}
	if effectIsPassive(effect) {
		return EffectDisplay{Eligibility: EffectDisplayPassive, Target: target}
	}
	if effect.OverdueSeconds != nil && *effect.OverdueSeconds > effectDisplayExpiryGrace.Seconds() {
		return EffectDisplay{Eligibility: EffectDisplayPredictedExpired, Target: target}
	}
	return EffectDisplay{Eligibility: EffectDisplayEligible, Target: target}
}

// effectDisplayCell is one cell of the Effects Overlay: the eligible instances the strip cannot tell apart. The strip
// shows no holder and no attribute, so the same effect copied onto three team members, or one bonus the game implements
// as one definition per element, would otherwise read as many identical cells. Instances are never merged in the tracker.
type effectDisplayCell struct {
	representative EffectInstanceSnapshot
	members        []EffectInstanceSnapshot
	elementFanOut  bool
}

// instances returns how many tracker instances the cell stands for.
func (c effectDisplayCell) instances() int { return len(c.members) }

func effectExpiryOf(effect EffectInstanceSnapshot) (time.Time, bool) {
	if effect.ExpectedExpiry == nil {
		return time.Time{}, false
	}
	return *effect.ExpectedExpiry, true
}

func effectStackOf(effect EffectInstanceSnapshot) uint64 {
	if effect.StackCount == nil {
		return 0
	}
	return *effect.StackCount
}

// sameCellTiming reports whether two instances end together: both without an expiry, or within the tolerance.
func sameCellTiming(a, b EffectInstanceSnapshot) bool {
	expiryA, timedA := effectExpiryOf(a)
	expiryB, timedB := effectExpiryOf(b)
	if timedA != timedB {
		return false
	}
	if !timedA {
		return true
	}
	gap := expiryA.Sub(expiryB)
	return gap <= effectCellExpiryTolerance && gap >= -effectCellExpiryTolerance
}

// sortForCells puts instances in expiry order, untimed last, so that instances ending together are adjacent.
func sortForCells(effects []EffectInstanceSnapshot) {
	sort.SliceStable(effects, func(i, j int) bool {
		expiryI, timedI := effectExpiryOf(effects[i])
		expiryJ, timedJ := effectExpiryOf(effects[j])
		if timedI != timedJ {
			return timedI
		}
		if timedI && !expiryI.Equal(expiryJ) {
			return expiryI.Before(expiryJ)
		}
		return effectViewKey(effects[i]) < effectViewKey(effects[j])
	})
}

func newEffectDisplayCell(members []EffectInstanceSnapshot) effectDisplayCell {
	representative := members[0]
	for _, member := range members[1:] {
		if effectViewKey(member) < effectViewKey(representative) {
			representative = member
		}
	}
	return effectDisplayCell{representative: representative, members: members}
}

// effectHolderSignature names the set of holders of a cell, independent of their order.
func effectHolderSignature(members []EffectInstanceSnapshot) string {
	holders := make([]uint64, 0, len(members))
	for _, member := range members {
		holders = append(holders, member.HolderEntityID)
	}
	sort.Slice(holders, func(i, j int) bool { return holders[i] < holders[j] })
	var signature strings.Builder
	for _, holder := range holders {
		fmt.Fprintf(&signature, "%d,", holder)
	}
	return signature.String()
}

// effectDisplayCells groups eligible instances into cells. Instances of one effect ID that end together and share a
// stack count become one cell. Then cells that together cover all six elements of one element attribute family, on the
// same holders, ending together and with the same stack count, become one cell; five of six stay separate. The result
// is deterministic for a given input and independent of the input order.
func effectDisplayCells(eligible []EffectInstanceSnapshot) []effectDisplayCell {
	byID := make(map[uint64][]EffectInstanceSnapshot, len(eligible))
	var ids []uint64
	cells := make([]effectDisplayCell, 0, len(eligible))
	for _, effect := range eligible {
		if effect.EffectID == nil {
			cells = append(cells, newEffectDisplayCell([]EffectInstanceSnapshot{effect}))
			continue
		}
		id := *effect.EffectID
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], effect)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		instances := byID[id]
		sortForCells(instances)
		for start := 0; start < len(instances); {
			end := start + 1
			for end < len(instances) && sameCellTiming(instances[end-1], instances[end]) && effectStackOf(instances[end-1]) == effectStackOf(instances[end]) {
				end++
			}
			cells = append(cells, newEffectDisplayCell(append([]EffectInstanceSnapshot(nil), instances[start:end]...)))
			start = end
		}
	}
	return mergeElementFanOut(cells)
}

// mergeElementFanOut folds a complete set of one element attribute family into a single cell. The merged cell states no
// element: it concerns all of them.
func mergeElementFanOut(cells []effectDisplayCell) []effectDisplayCell {
	type fanOutKey struct {
		family  uint64
		stack   uint64
		holders string
	}
	groups := map[fanOutKey][]int{}
	var keys []fanOutKey
	for index, cell := range cells {
		static := cell.representative.Static
		if static == nil || static.ElementFamily == 0 || cell.representative.Kind != EffectKindStatModifier {
			continue
		}
		key := fanOutKey{family: static.ElementFamily, stack: effectStackOf(cell.representative), holders: effectHolderSignature(cell.members)}
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], index)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.family != b.family {
			return a.family < b.family
		}
		if a.stack != b.stack {
			return a.stack < b.stack
		}
		return a.holders < b.holders
	})
	absorbed := make([]bool, len(cells))
	var merged []effectDisplayCell
	for _, key := range keys {
		indexes := groups[key]
		sort.SliceStable(indexes, func(i, j int) bool {
			expiryI, timedI := effectExpiryOf(cells[indexes[i]].representative)
			expiryJ, timedJ := effectExpiryOf(cells[indexes[j]].representative)
			if timedI != timedJ {
				return timedI
			}
			return timedI && expiryI.Before(expiryJ)
		})
		for start := 0; start < len(indexes); {
			end := start + 1
			for end < len(indexes) && sameCellTiming(cells[indexes[end-1]].representative, cells[indexes[end]].representative) {
				end++
			}
			elements := map[uint64]bool{}
			for _, index := range indexes[start:end] {
				elements[cells[index].representative.Static.ElementID] = true
			}
			if end-start == 6 && len(elements) == 6 {
				var members []EffectInstanceSnapshot
				for _, index := range indexes[start:end] {
					members = append(members, cells[index].members...)
					absorbed[index] = true
				}
				cell := newEffectDisplayCell(members)
				cell.elementFanOut = true
				merged = append(merged, cell)
			}
			start = end
		}
	}
	result := make([]effectDisplayCell, 0, len(cells))
	for index, cell := range cells {
		if !absorbed[index] {
			result = append(result, cell)
		}
	}
	return append(result, merged...)
}

// effectPolarityFor returns a polarity only where the definition proves one. An element-ailment glossary term describes
// a harmful state of its holder (periodic damage or reduced defence), so its holder carries a debuff. The term link is
// correlated, not verified, and every other effect stays unclassified: an attribute modifier does not prove polarity,
// because the attribute it changes need not be the holder's, and a buff-marker tag names the effect table, not a sign.
func effectPolarityFor(effect EffectInstanceSnapshot) EffectPolarity {
	if effect.Kind == EffectKindElementAilment && effect.Static != nil && effect.Static.Term != nil {
		return EffectPolarityDebuff
	}
	return EffectPolarityUnclassified
}

// ViewEffectCounts partitions the tracked effect set the view was built from. Every instance is counted exactly once
// at each level:
//
//	Tracked  = Active + EstimatedExpired
//	Active   = NotLocal + Local
//	Local    = NonCombat + Internal + Unclassified + Passive + PredictedExpired + Eligible
//	Eligible = Listed + Truncated
//	Cells    = ListedCells + TruncatedCells
//
// Eligible counts instances; the Effects Overlay shows Cells, each of which stands for one or more eligible instances
// (effectDisplayCells). Truncated counts eligible instances beyond the view's bound; they are part of the overflow, never
// of another reason.
type ViewEffectCounts struct {
	Tracked          int `json:"tracked"`
	EstimatedExpired int `json:"estimatedExpired"`
	Active           int `json:"active"`
	NotLocal         int `json:"notLocal"`
	Local            int `json:"local"`
	NonCombat        int `json:"nonCombat"`
	Internal         int `json:"internal"`
	Unclassified     int `json:"unclassified"`
	Passive          int `json:"passive"`
	PredictedExpired int `json:"predictedExpired"`
	Eligible         int `json:"eligible"`
	Listed           int `json:"listed"`
	Truncated        int `json:"truncated"`
	Cells            int `json:"cells"`
	ListedCells      int `json:"listedCells"`
	TruncatedCells   int `json:"truncatedCells"`
}

func (c *ViewEffectCounts) add(eligibility EffectDisplayEligibility) {
	c.Tracked++
	switch eligibility {
	case EffectDisplayEstimatedExpired:
		c.EstimatedExpired++
		return
	case EffectDisplayNotLocal:
		c.Active++
		c.NotLocal++
		return
	}
	c.Active++
	c.Local++
	switch eligibility {
	case EffectDisplayNonCombat:
		c.NonCombat++
	case EffectDisplayInternal:
		c.Internal++
	case EffectDisplayUnclassified:
		c.Unclassified++
	case EffectDisplayPassive:
		c.Passive++
	case EffectDisplayPredictedExpired:
		c.PredictedExpired++
	default:
		c.Eligible++
	}
}

// accountingError reports a count that breaks the partition, or a listed cell count that differs from the list.
func (c ViewEffectCounts) accountingError(listedCells int) error {
	switch {
	case c.Tracked != c.Active+c.EstimatedExpired:
		return fmt.Errorf("tracked %d != active %d + estimated expired %d", c.Tracked, c.Active, c.EstimatedExpired)
	case c.Active != c.NotLocal+c.Local:
		return fmt.Errorf("active %d != not local %d + local %d", c.Active, c.NotLocal, c.Local)
	case c.Local != c.NonCombat+c.Internal+c.Unclassified+c.Passive+c.PredictedExpired+c.Eligible:
		return fmt.Errorf("local %d != non-combat %d + internal %d + unclassified %d + passive %d + predicted expired %d + eligible %d", c.Local, c.NonCombat, c.Internal, c.Unclassified, c.Passive, c.PredictedExpired, c.Eligible)
	case c.Eligible != c.Listed+c.Truncated:
		return fmt.Errorf("eligible %d != listed %d + truncated %d", c.Eligible, c.Listed, c.Truncated)
	case c.Cells != c.ListedCells+c.TruncatedCells:
		return fmt.Errorf("cells %d != listed cells %d + truncated cells %d", c.Cells, c.ListedCells, c.TruncatedCells)
	case c.ListedCells != listedCells:
		return fmt.Errorf("listed cells %d != %d rows", c.ListedCells, listedCells)
	}
	return nil
}
