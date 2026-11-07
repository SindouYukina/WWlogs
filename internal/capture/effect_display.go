package capture

import (
	"fmt"
	"strings"
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
	// EffectKindRuntimeLink has no classifying definition, but the local team put it on a monster or a monster on the
	// local team. Its meaning is unknown; only its combat context is established.
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
	effectKindBasisInternalTag  = "developer_tag_internal_family:"
	effectKindBasisCombatTag    = "developer_tag_combat_word"
	effectKindBasisRuntimeLink  = "runtime_monster_link"
	effectKindBasisNoEvidence   = "no_classifying_evidence"
	effectKindBasisNoDefinition = "definition_unresolved"
)

// classifyEffectKind derives the kind from an instance's combat classification and its definition's developer tags.
// Player-facing evidence wins over internal evidence, as combat evidence wins over non-combat evidence in the combat
// class, so an effect with both is never hidden: a term, then a combat attribute modifier, then a buff marker, then
// non-combat evidence, then an internal-state family, then other combat words, then the runtime link.
func classifyEffectKind(class EffectCombatClass, basis string, static *EffectStaticInfo) (EffectKind, string) {
	var tags []string
	if static != nil {
		tags = static.DeveloperTags
	}
	if basis == effectCombatBasisTerm {
		return EffectKindElementAilment, effectKindBasisTerm
	}
	if class == EffectCombatClassCombat && strings.HasPrefix(basis, effectCombatBasisAttribute) {
		return EffectKindStatModifier, effectKindBasisAttribute
	}
	if segment := tagSegmentMatch(tags, func(s string) bool { return buffMarkerTagSegments[strings.ToLower(s)] }); segment != "" {
		return EffectKindBuffMarker, effectKindBasisBuffMarker + segment
	}
	if class == EffectCombatClassNonCombat {
		return EffectKindNonCombat, effectKindBasisNonCombat
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
	// EffectDisplayEstimatedExpired is an instance whose estimated expiry passed long ago without a REMOVE. The game
	// most likely ended it; the tracker keeps it only until a REMOVE or its bound.
	EffectDisplayEstimatedExpired EffectDisplayEligibility = "estimated_expired"
	// EffectDisplayNotLocal is an instance whose holder and source are both not proven to be the local team.
	EffectDisplayNotLocal EffectDisplayEligibility = "not_local"
	// EffectDisplayNonCombat is an instance of kind non_combat.
	EffectDisplayNonCombat EffectDisplayEligibility = "non_combat"
	// EffectDisplayInternal is an instance of kind internal_state.
	EffectDisplayInternal EffectDisplayEligibility = "internal"
	// EffectDisplayUnclassified is a local instance of kind unknown. It is counted, never listed and never called
	// internal: unknown is not evidence that an effect is irrelevant.
	EffectDisplayUnclassified EffectDisplayEligibility = "unclassified"
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

// effectDisplayFor decides whether an instance is listed. The order of the checks fixes which reason an instance is
// counted under: an ended estimate first, then the local relation, then the kind.
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
	case EffectKindElementAilment, EffectKindStatModifier, EffectKindBuffMarker, EffectKindCombatState, EffectKindRuntimeLink:
		return EffectDisplay{Eligibility: EffectDisplayEligible, Target: target}
	case EffectKindNonCombat:
		return EffectDisplay{Eligibility: EffectDisplayNonCombat, Target: target}
	case EffectKindInternalState:
		return EffectDisplay{Eligibility: EffectDisplayInternal, Target: target}
	default:
		return EffectDisplay{Eligibility: EffectDisplayUnclassified, Target: target}
	}
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
//	Local    = NonCombat + Internal + Unclassified + Eligible
//	Eligible = Listed + Truncated
//
// Eligible is the display set: the Effects Overlay title and its overflow count are both taken from it. Truncated
// counts eligible instances beyond the view's bound; they are part of the overflow, never of another reason.
type ViewEffectCounts struct {
	Tracked          int `json:"tracked"`
	EstimatedExpired int `json:"estimatedExpired"`
	Active           int `json:"active"`
	NotLocal         int `json:"notLocal"`
	Local            int `json:"local"`
	NonCombat        int `json:"nonCombat"`
	Internal         int `json:"internal"`
	Unclassified     int `json:"unclassified"`
	Eligible         int `json:"eligible"`
	Listed           int `json:"listed"`
	Truncated        int `json:"truncated"`
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
	default:
		c.Eligible++
	}
}

// accountingError reports a count that breaks the partition, or a listed count that differs from the list.
func (c ViewEffectCounts) accountingError(listed int) error {
	switch {
	case c.Tracked != c.Active+c.EstimatedExpired:
		return fmt.Errorf("tracked %d != active %d + estimated expired %d", c.Tracked, c.Active, c.EstimatedExpired)
	case c.Active != c.NotLocal+c.Local:
		return fmt.Errorf("active %d != not local %d + local %d", c.Active, c.NotLocal, c.Local)
	case c.Local != c.NonCombat+c.Internal+c.Unclassified+c.Eligible:
		return fmt.Errorf("local %d != non-combat %d + internal %d + unclassified %d + eligible %d", c.Local, c.NonCombat, c.Internal, c.Unclassified, c.Eligible)
	case c.Eligible != c.Listed+c.Truncated:
		return fmt.Errorf("eligible %d != listed %d + truncated %d", c.Eligible, c.Listed, c.Truncated)
	case c.Listed != listed:
		return fmt.Errorf("listed %d != %d rows", c.Listed, listed)
	}
	return nil
}
