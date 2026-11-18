package capture

import (
	"testing"
)

// The definitions below are SYNTHETIC: their shapes copy Master rows seen in live captures (an
// attribute modifier, a developer tag, an element-ailment term), but the ids are arbitrary.

func TestClassifyEffectDefinitionUsesStructuredEvidenceFirst(t *testing.T) {
	cases := []struct {
		name      string
		static    BuffStaticParameters
		defined   bool
		term      bool
		wantClass EffectCombatClass
		wantBasis string
	}{
		{"element ailment term", BuffStaticParameters{}, true, true, EffectCombatClassCombat, effectCombatBasisTerm},
		{"combat attribute", BuffStaticParameters{Attribute: 9, HasAttribute: true, HasModifier: true}, true, false, EffectCombatClassCombat, "attribute_modifier:CritDamage"},
		{"attribute without modifier is not evidence", BuffStaticParameters{Attribute: 9, HasAttribute: true}, true, false, EffectCombatClassUnknown, effectCombatBasisNoEvidence},
		{"exploration stamina", BuffStaticParameters{Attribute: 74, HasAttribute: true, HasModifier: true}, true, false, EffectCombatClassNonCombat, "attribute_modifier:StrengthSwim"},
		{"movement speed stays unknown", BuffStaticParameters{Attribute: 113, HasAttribute: true, HasModifier: true}, true, false, EffectCombatClassUnknown, effectCombatBasisNoEvidence},
		{"invulnerability tag", BuffStaticParameters{Tags: []string{"功能.逻辑状态标识.无敌.通用无敌"}}, true, false, EffectCombatClassCombat, "developer_tag:无敌"},
		{"role buff marker tag", BuffStaticParameters{Tags: []string{"角色.R2T1ShouanrenMd10011.buff标识.退场技.buff1"}}, true, false, EffectCombatClassCombat, "developer_tag:buff标识"},
		{"fall damage tag", BuffStaticParameters{Tags: []string{"功能.逻辑状态标识.跌落无伤"}}, true, false, EffectCombatClassNonCombat, "developer_tag:跌落无伤"},
		{"combat evidence wins", BuffStaticParameters{Tags: []string{"功能.逻辑状态标识.跌落无伤", "功能.逻辑状态标识.无敌"}}, true, false, EffectCombatClassCombat, "developer_tag:无敌"},
		{"combat attribute wins over a non-combat tag", BuffStaticParameters{Attribute: 7, HasAttribute: true, HasModifier: true, Tags: []string{"角色.BaseRole.状态通用标识.幻象收服葫芦回收"}}, true, false, EffectCombatClassCombat, "attribute_modifier:Atk"},
		{"hashed tag only", BuffStaticParameters{Tags: []string{"角色.41f5baca.a9c8f7db.dd0bc2ac"}}, true, false, EffectCombatClassUnknown, effectCombatBasisNoEvidence},
		{"no definition", BuffStaticParameters{}, false, false, EffectCombatClassUnknown, effectCombatBasisDefinitionMissing},
	}
	for _, c := range cases {
		class, basis := classifyEffectDefinition(c.static, c.defined, c.term)
		if class != c.wantClass || basis != c.wantBasis {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, class, basis, c.wantClass, c.wantBasis)
		}
	}
}

func TestClassifyEffectInstanceRuntimeRulesNeedAHostileMonster(t *testing.T) {
	unknown, basis := EffectCombatClassUnknown, effectCombatBasisNoEvidence
	cases := []struct {
		name    string
		parties effectParties
		want    EffectCombatClass
	}{
		{"local team on a monster", effectParties{holder: EffectRelationNonPlayer, holderMonster: true, source: EffectRelationLocalRole}, EffectCombatClassCombat},
		{"monster on the local team", effectParties{holder: EffectRelationLocalRole, source: EffectRelationNonPlayer, sourceMonster: true}, EffectCombatClassCombat},
		{"local team on a scene template", effectParties{holder: EffectRelationNonPlayer, source: EffectRelationLocalRole}, EffectCombatClassUnknown},
		{"monster-typed local summon", effectParties{holder: EffectRelationLocalRole, source: EffectRelationLocalOwned, sourceMonster: true}, EffectCombatClassUnknown},
		{"unknown source", effectParties{holder: EffectRelationNonPlayer, holderMonster: true, source: EffectRelationUnknown}, EffectCombatClassUnknown},
	}
	for _, c := range cases {
		if got, _ := classifyEffectInstance(unknown, basis, c.parties); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	monsterOnLocal := effectParties{holder: EffectRelationLocalRole, source: EffectRelationNonPlayer, sourceMonster: true}
	if got, _ := classifyEffectInstance(EffectCombatClassNonCombat, "developer_tag:跌落无伤", monsterOnLocal); got != EffectCombatClassNonCombat {
		t.Fatalf("a non-combat definition was overridden: %s", got)
	}
}

// classifiedTestMaster defines a combat attribute effect (12), a non-combat tag effect (13) and an
// effect with no classifying evidence (14).
func classifiedTestMaster() *GameMaster {
	return &GameMaster{
		BuffDefinitions: map[uint64]struct{}{12: {}, 13: {}, 14: {}},
		BuffStatics: map[uint64]BuffStaticParameters{
			12: {Attribute: 9, HasAttribute: true, HasModifier: true},
			13: {Tags: []string{"功能.逻辑状态标识.跌落无伤"}},
		},
	}
}

func TestEffectSnapshotCarriesCombatClassification(t *testing.T) {
	p := newClientEffectTestProcessor()
	p.Masters = classifiedTestMaster()
	common := testCombatCommon{entityID: 100}
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436801, 12, 100, 1, 12)))
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436802, 13, 100, 1, 10)))
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436803, 14, 100, 1, 5)))
	deliverClientPush(t, p, 0, clientPushBody(common, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436804, 99, 100, 1, 5)))
	// A monster's own effect, applied by the local team, has no definition and becomes combat at runtime.
	deliverClientPush(t, p, 0, clientPushBody(testCombatCommon{entityID: 200}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436805, 14, 100, 1, 5)))
	want := map[uint64]struct {
		class EffectCombatClass
		basis string
	}{
		268436801: {EffectCombatClassCombat, "attribute_modifier:CritDamage"},
		268436802: {EffectCombatClassNonCombat, "developer_tag:跌落无伤"},
		268436803: {EffectCombatClassUnknown, effectCombatBasisNoEvidence},
		268436804: {EffectCombatClassUnknown, effectCombatBasisDefinitionMissing},
		268436805: {EffectCombatClassCombat, effectCombatBasisLocalOnMonster},
	}
	effects := p.Effects.SnapshotAt(clientQuery(p, 1)).Effects
	if len(effects) != len(want) {
		t.Fatalf("effects = %d, want %d: classification must never drop an instance", len(effects), len(want))
	}
	for _, effect := range effects {
		w := want[effect.HandleID]
		if effect.CombatClass != w.class || effect.CombatBasis != w.basis {
			t.Errorf("handle %d: got %s/%s, want %s/%s", effect.HandleID, effect.CombatClass, effect.CombatBasis, w.class, w.basis)
		}
	}
}
