package capture

import (
	"strings"
)

// EffectCombatClass says whether an effect is evidently part of combat. It is decided from the
// Master definition and, for one runtime case, from the entity relations; it never comes from an
// effect id list. UNKNOWN is a real answer: it means no evidence either way, not "probably harmless".
type EffectCombatClass string

const (
	EffectCombatClassCombat    EffectCombatClass = "combat"
	EffectCombatClassNonCombat EffectCombatClass = "non_combat"
	EffectCombatClassUnknown   EffectCombatClass = "unknown"
)

// Classification bases. Each names the kind of evidence; the attribute or tag word is appended.
const (
	effectCombatBasisTerm              = "element_ailment_term"
	EffectCombatBasisAttribute         = "attribute_modifier:"
	effectCombatBasisTag               = "developer_tag:"
	effectCombatBasisLocalOnMonster    = "local_source_on_monster"
	effectCombatBasisMonsterOnLocal    = "monster_source_on_local_team"
	effectCombatBasisNoEvidence        = "no_classifying_evidence"
	effectCombatBasisDefinitionMissing = "definition_unresolved"
)

// attributeTypeNames is the client protocol's EAttributeType enumeration, named from the client's protocol assembly.
// buff.game_attribute_id uses the same numbering: across two live captures the server's
// AttributeChangedNotify for the holder changed by exactly the row's modifier for attributes 8, 9,
// 11, 24, 60 and 77 when the effect was applied and removed.
var attributeTypeNames = map[uint64]string{
	1: "Lv", 2: "LifeMax", 3: "Life", 4: "Sheild", 5: "SheildDamageChange", 6: "SheildDamageReduce", 7: "Atk",
	8: "Crit", 9: "CritDamage", 10: "Def", 11: "EnergyEfficiency", 12: "CdReduse", 13: "ElementEfficiency",
	14: "DamageChangeNormalSkill", 15: "DamageChange", 16: "DamageReduce", 17: "DamageChangeAuto",
	18: "DamageChangeCast", 19: "DamageChangeUltra", 20: "DamageChangeQte", 21: "DamageChangePhys",
	22: "DamageChangeElement1", 23: "DamageChangeElement2", 24: "DamageChangeElement3", 25: "DamageChangeElement4",
	26: "DamageChangeElement5", 27: "DamageChangeElement6", 28: "DamageResistancePhys",
	29: "DamageResistanceElement1", 30: "DamageResistanceElement2", 31: "DamageResistanceElement3",
	32: "DamageResistanceElement4", 33: "DamageResistanceElement5", 34: "DamageResistanceElement6",
	35: "HealChange", 36: "HealedChange", 37: "DamageReducePhys", 38: "DamageReduceElement1",
	39: "DamageReduceElement2", 40: "DamageReduceElement3", 41: "DamageReduceElement4", 42: "DamageReduceElement5",
	43: "DamageReduceElement6", 44: "SpecialEnergy5Max", 45: "SpecialEnergy5", 46: "ReactionChange3",
	47: "ReactionChange4", 48: "ReactionChange5", 49: "ReactionChange6", 50: "ReactionChange7", 51: "ReactionChange8",
	52: "ReactionChange9", 53: "ReactionChange10", 54: "ReactionChange11", 55: "ReactionChange12",
	56: "ReactionChange13", 57: "ReactionChange14", 58: "ReactionChange15", 59: "EnergyMax", 60: "Energy",
	61: "SpecialEnergy1Max", 62: "SpecialEnergy1", 63: "SpecialEnergy2Max", 64: "SpecialEnergy2",
	65: "SpecialEnergy3Max", 66: "SpecialEnergy3", 67: "SpecialEnergy4Max", 68: "SpecialEnergy4", 69: "StrengthMax",
	70: "Strength", 71: "StrengthRecover", 72: "StrengthPunishTime", 73: "StrengthRun", 74: "StrengthSwim",
	75: "StrengthFastSwim", 76: "ElementEnergyMax", 77: "ElementEnergy", 78: "HardnessMax", 79: "Hardness",
	80: "HardnessRecover", 81: "HardnessPunishTime", 82: "HardnessChange", 83: "HardnessReduce", 84: "ToughMax",
	85: "Tough", 86: "ToughRecover", 87: "ToughChange", 88: "ToughReduce", 89: "ElementPower1", 90: "ElementPower2",
	91: "ElementPower3", 92: "ElementPower4", 93: "ElementPower5", 94: "ElementPower6", 95: "SpecialDamageChange",
	96: "StrengthFastClimbCost", 97: "ElementPropertyType", 98: "WeakTime", 99: "IgnoreDefRate",
	100: "IgnoreDamageResistancePhys", 101: "IgnoreDamageResistanceElement1", 102: "IgnoreDamageResistanceElement2",
	103: "IgnoreDamageResistanceElement3", 104: "IgnoreDamageResistanceElement4", 105: "IgnoreDamageResistanceElement5",
	106: "IgnoreDamageResistanceElement6", 107: "SkillToughRatio", 108: "StrengthClimbJump", 109: "StrengthGliding",
	110: "Mass", 111: "BrakingFrictionFactor", 112: "GravityScale", 113: "SpeedRatio", 114: "DamageChangePhantom",
	115: "AutoAttackSpeed", 116: "CastAttackSpeed", 117: "StatusBuildUp1Max", 118: "StatusBuildUp1",
	119: "StatusBuildUp2Max", 120: "StatusBuildUp2", 121: "StatusBuildUp3Max", 122: "StatusBuildUp3",
	123: "StatusBuildUp4Max", 124: "StatusBuildUp4", 125: "StatusBuildUp5Max", 126: "StatusBuildUp5", 127: "RageMax",
	128: "Rage", 129: "RageRecover", 130: "RagePunishTime", 131: "RageChange", 132: "RageReduce",
	133: "ToughRecoverDelayTime", 134: "Jump", 135: "ParalysisTimeMax", 136: "ParalysisTime",
	137: "ParalysisTimeRecover", 138: "WeaknessBuildUp", 139: "WeaknessBuildUpMax", 140: "WeaknessTotalBonus",
	141: "BreakWeaknessRatio", 142: "WeaknessMastery",
}

// attributeCombatClass classifies an attribute by what its protocol name says it changes.
// Exploration stamina costs (swimming, climbing, gliding) are non-combat. Level, general
// stamina, sprint cost, movement speed and movement physics may matter in combat, so they stay
// unknown. Every other attribute is a combat statistic, resource, resistance or toughness value.
func attributeCombatClass(attribute uint64) EffectCombatClass {
	switch attribute {
	case 74, 75, 96, 108, 109: // StrengthSwim, StrengthFastSwim, StrengthFastClimbCost, StrengthClimbJump, StrengthGliding
		return EffectCombatClassNonCombat
	case 1, 69, 70, 71, 72, 73, 110, 111, 112, 113, 134: // Lv, Strength*, Mass, physics, SpeedRatio, Jump
		return EffectCombatClassUnknown
	}
	if _, known := attributeTypeNames[attribute]; known {
		return EffectCombatClassCombat
	}
	return EffectCombatClassUnknown
}

// Developer-tag words with a combat meaning. A tag segment that contains one of them marks the effect
// as combat: invulnerability, super armour, combat and ailment states, a role's buff marker, dodge,
// concerto/QTE mechanics, rage, grab and knock-up immunity, hit-effect suppression and ultimate states.
var combatTagWords = []string{
	"无敌", "霸体", "战斗状态", "异常状态", "buff标识", "闪避", "QTE", "协奏", "狂暴", "免疫抓取", "无法被击飞", "不接受命中特效", "大招",
}

// Developer-tag words with a non-combat meaning: protection from fall damage (environment damage,
// not combat damage) and the echo-capture gourd and its capture animation states.
var nonCombatTagWords = []string{"跌落无伤", "幻象收服", "葫芦"}

func tagWordMatch(tags []string, words []string) string {
	for _, tag := range tags {
		for _, segment := range strings.Split(tag, ".") {
			for _, word := range words {
				if strings.Contains(segment, word) {
					return word
				}
			}
		}
	}
	return ""
}

// classifyEffectDefinition classifies an effect from its static definition alone. Combat evidence
// wins over non-combat evidence, so a definition with both is never hidden.
func classifyEffectDefinition(static BuffStaticParameters, hasDefinition, hasTerm bool) (EffectCombatClass, string) {
	if hasTerm {
		return EffectCombatClassCombat, effectCombatBasisTerm
	}
	if !hasDefinition {
		return EffectCombatClassUnknown, effectCombatBasisDefinitionMissing
	}
	attributeClass := EffectCombatClassUnknown
	if static.HasAttribute && static.HasModifier {
		attributeClass = attributeCombatClass(static.Attribute)
	}
	if attributeClass == EffectCombatClassCombat {
		return EffectCombatClassCombat, EffectCombatBasisAttribute + attributeTypeNames[static.Attribute]
	}
	if word := tagWordMatch(static.Tags, combatTagWords); word != "" {
		return EffectCombatClassCombat, effectCombatBasisTag + word
	}
	if attributeClass == EffectCombatClassNonCombat {
		return EffectCombatClassNonCombat, EffectCombatBasisAttribute + attributeTypeNames[static.Attribute]
	}
	if word := tagWordMatch(static.Tags, nonCombatTagWords); word != "" {
		return EffectCombatClassNonCombat, effectCombatBasisTag + word
	}
	return EffectCombatClassUnknown, effectCombatBasisNoEvidence
}

// effectParties describes the two ends of an effect instance at classification time.
type effectParties struct {
	holder, source               EffectEntityRelation
	holderMonster, sourceMonster bool
}

// classifyEffectInstance adds the runtime rules for an otherwise unknown effect: one that the local
// team put on a monster, or a monster put on the local team, is combat. The other end must be a
// resolved monster whose player relation is non-player, so neither a scene object nor a monster-typed
// summon of the local team qualifies. A non-combat definition is not overridden.
func classifyEffectInstance(staticClass EffectCombatClass, staticBasis string, parties effectParties) (EffectCombatClass, string) {
	if staticClass != EffectCombatClassUnknown {
		return staticClass, staticBasis
	}
	switch {
	case parties.holderMonster && parties.holder == EffectRelationNonPlayer && localEffectRelation(parties.source):
		return EffectCombatClassCombat, effectCombatBasisLocalOnMonster
	case parties.sourceMonster && parties.source == EffectRelationNonPlayer && localEffectRelation(parties.holder):
		return EffectCombatClassCombat, effectCombatBasisMonsterOnLocal
	}
	return staticClass, staticBasis
}

func localEffectRelation(relation EffectEntityRelation) bool {
	return relation == EffectRelationLocalRole || relation == EffectRelationLocalOwned || relation == EffectRelationLocalPlayer
}

// effectMonsterEntity reports a runtime entity resolved as a monster.
func (p *Processor) effectMonsterEntity(entityID uint64) bool {
	if p == nil || p.Entities == nil || entityID == 0 {
		return false
	}
	identity := ResolveEntityIdentity(p.Entities, p.Masters, entityID)
	return identity.HasMonster || IsExplicitMonsterEntity(identity.Runtime)
}

// effectPartiesFor resolves both ends of an effect for classification.
func (p *Processor) effectPartiesFor(holder uint64, source uint64, hasSource bool) effectParties {
	parties := effectParties{holder: p.EffectEntityRelation(holder), source: EffectRelationUnknown, holderMonster: p.effectMonsterEntity(holder)}
	if hasSource && source != 0 {
		parties.source, parties.sourceMonster = p.EffectEntityRelation(source), p.effectMonsterEntity(source)
	}
	return parties
}

// effectDefinitionClass classifies an effect id against the loaded Master.
func (p *Processor) effectDefinitionClass(effectID uint64) (EffectCombatClass, string) {
	if p == nil || !p.Masters.HasBuffDefinition(effectID) {
		return EffectCombatClassUnknown, effectCombatBasisDefinitionMissing
	}
	static, _ := p.Masters.buffStatic(effectID)
	_, hasTerm := p.Masters.buffTerm(effectID)
	return classifyEffectDefinition(static, true, hasTerm)
}
