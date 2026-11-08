package capture

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClassifyEffectKindUsesTheHUDCueOnlyWithoutAMoreSpecificKind(t *testing.T) {
	hud := func(tags ...string) *EffectStaticInfo {
		return &EffectStaticInfo{DeveloperTags: tags, HUDCueTypes: []int32{2}}
	}
	cases := []struct {
		name   string
		class  EffectCombatClass
		basis  string
		static *EffectStaticInfo
		want   EffectKind
	}{
		{"unknown with a HUD cue", EffectCombatClassUnknown, effectCombatBasisNoEvidence, hud(), EffectKindHUDEffect},
		{"hashed tags with a HUD cue", EffectCombatClassUnknown, effectCombatBasisNoEvidence, hud("角色.dc777977.8fd425dc.3a374e56"), EffectKindHUDEffect},
		{"player-visible wins over an internal family", EffectCombatClassCombat, effectCombatBasisTag + "状态标识", hud("角色.R2T1ShouanrenMd10011.状态标识.x"), EffectKindHUDEffect},
		{"runtime link with a HUD cue", EffectCombatClassCombat, effectCombatBasisMonsterOnLocal, hud(), EffectKindHUDEffect},
		{"an ailment term stays an ailment", EffectCombatClassCombat, effectCombatBasisTerm, hud("战斗状态.异常状态.基础元素异常.风蚀效应"), EffectKindElementAilment},
		{"an attribute modifier stays a modifier", EffectCombatClassCombat, effectCombatBasisAttribute + "CritDamage", hud(), EffectKindStatModifier},
		{"a buff marker stays a marker", EffectCombatClassCombat, effectCombatBasisTag + "buff标识", hud("角色.R2T1ShouanrenMd10011.buff标识.退场技.buff1"), EffectKindBuffMarker},
		{"non-combat evidence still wins", EffectCombatClassNonCombat, effectCombatBasisTag + "跌落无伤", hud("功能.逻辑状态标识.跌落无伤"), EffectKindNonCombat},
		{"no HUD cue keeps unknown", EffectCombatClassUnknown, effectCombatBasisNoEvidence, &EffectStaticInfo{}, EffectKindUnknown},
		{"no definition keeps unknown", EffectCombatClassUnknown, effectCombatBasisDefinitionMissing, nil, EffectKindUnknown},
	}
	for _, tc := range cases {
		got, basis := classifyEffectKind(tc.class, tc.basis, tc.static)
		if got != tc.want || basis == "" {
			t.Errorf("%s: kind = %s (%s), want %s", tc.name, got, basis, tc.want)
		}
		if got == EffectKindHUDEffect && basis != effectKindBasisHUDCue {
			t.Errorf("%s: basis = %s", tc.name, basis)
		}
	}
	effect := EffectInstanceSnapshot{Lifecycle: EffectLifecycleActive, HolderRelation: EffectRelationLocalRole, SourceRelation: EffectRelationLocalRole, Kind: EffectKindHUDEffect}
	if got := effectDisplayFor(effect); got != (EffectDisplay{EffectDisplayEligible, EffectTargetTeam}) {
		t.Fatalf("HUD effect display = %+v", got)
	}
	if got := effectPolarityFor(effect); got != EffectPolarityUnclassified {
		t.Fatalf("a HUD cue must not decide polarity: %s", got)
	}
}

func TestViewEffectsListHUDEffectsFirstWithinEachCountdownGroup(t *testing.T) {
	p := newEffectViewProcessor()
	hud := func(e *EffectApplyEvent) {
		e.DefinitionLinked, e.Static = true, &EffectStaticInfo{HUDCueTypes: []int32{2}}
	}
	untimed := func(e *EffectApplyEvent) { e.DurationSeconds, e.RemainingSeconds = nil, nil }
	p.effects.ObserveApply(displayApply(1, 900, 0, untimed))
	p.effects.ObserveApply(displayApply(2, 901, 0, nil))
	p.effects.ObserveApply(displayApply(3, 902, 0, func(e *EffectApplyEvent) { untimed(e); hud(e) }))
	p.effects.ObserveApply(displayApply(4, 903, 0, hud))
	p.effects.ObserveApply(displayApply(5, 904, 0, nil))
	// An unknown effect with a HUD cue becomes eligible; one without stays counted as unclassified.
	p.effects.ObserveApply(displayApply(6, 905, 0, func(e *EffectApplyEvent) {
		e.CombatClass, e.CombatBasis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
		hud(e)
	}))
	p.effects.ObserveApply(displayApply(7, 906, 0, func(e *EffectApplyEvent) {
		e.CombatClass, e.CombatBasis = EffectCombatClassUnknown, effectCombatBasisNoEvidence
	}))
	view := effectViewAt(p, 0.5)
	requireEffectCounts(t, view, ViewEffectCounts{Tracked: 7, Active: 7, Local: 7, Unclassified: 1, Eligible: 6, Listed: 6})
	var order []uint64
	for _, effect := range view.Active {
		order = append(order, *effect.EffectID)
	}
	if want := []uint64{903, 905, 901, 904, 902, 900}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want timed HUD %v", order, want)
	}
	for _, effect := range view.Active {
		hud := *effect.EffectID == 902 || *effect.EffectID == 903 || *effect.EffectID == 905
		if effect.HUDIcon != hud {
			t.Fatalf("effect %d HUDIcon = %v, want %v", *effect.EffectID, effect.HUDIcon, hud)
		}
	}
}

func TestLoadBuffHUDCuesKeepsHUDTypesWithAPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cues.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE TABLE buff_gameplay_cue_ids(owner_id TEXT,ordinal INTEGER,value TEXT,PRIMARY KEY(owner_id,ordinal))",
		"CREATE TABLE gameplaycue(id TEXT PRIMARY KEY,cue_type INTEGER NOT NULL,path_asset_id TEXT)",
		"INSERT INTO gameplaycue VALUES('1',2,'a'),('2',24,'b'),('3',14,NULL),('4',1,'c'),('5',2,'d'),('6',37,'')",
		// 10: two HUD cues of one type and one of another; 11: a HUD type without a path; 12: a non-HUD type;
		// 13: a float-formatted cue id; 14: an empty path; 15: a cue id with no row.
		"INSERT INTO buff_gameplay_cue_ids VALUES('10',0,'5'),('10',1,'2'),('10',2,'1'),('11',0,'3'),('12',0,'4'),('13',0,'1.0'),('14',0,'6'),('15',0,'99')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	statics := map[uint64]buffStaticParameters{}
	if err := loadBuffHUDCues(db, statics); err != nil {
		t.Fatal(err)
	}
	got := map[uint64][]int32{}
	for id, entry := range statics {
		got[id] = entry.hudCueTypes
	}
	if want := map[uint64][]int32{10: {2, 24}, 13: {2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HUD cue types = %v, want %v", got, want)
	}
	older := map[uint64]buffStaticParameters{}
	if _, err := db.Exec("DROP TABLE gameplaycue"); err != nil {
		t.Fatal(err)
	}
	if err := loadBuffHUDCues(db, older); err != nil || len(older) != 0 {
		t.Fatalf("a Master without gameplaycue: %v %v", older, err)
	}
}

// TestTrackedMasterHUDCuesOfKnownEffects pins the HUD evidence of effects studied in archived captures to the tracked
// Master. Outro and team buffs that carried no other evidence gain it; confirmed buffs without a HUD cue keep their kind.
func TestTrackedMasterHUDCuesOfKnownEffects(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	master, err := loadMasterDataFromDatabase(db, "embedded", "test", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint64][]int32{
		2002202311: {2}, 2002202312: {2}, 2002203612: {2}, 2002203902: {2}, 2002205402: {2}, 1311400208: {24},
		1312010033: {2}, 1312010013: {2}, 10041000: {2}, 10061000: {2},
		// Confirmed buffs that the game does not draw in a HUD buff bar.
		1312010093: nil, 2002202314: nil, 1505701002: nil, 1505701003: nil, 3029: nil, 10040002: nil, 3098: nil,
	}
	for id, types := range want {
		static, ok := master.buffStatic(id)
		if !ok {
			t.Fatalf("effect %d has no static row", id)
		}
		if !reflect.DeepEqual(static.hudCueTypes, types) {
			t.Errorf("effect %d HUD cue types = %v, want %v", id, static.hudCueTypes, types)
		}
	}
	p := newEffectViewProcessor()
	p.masters = master
	for id, kind := range map[uint64]EffectKind{2002202312: EffectKindHUDEffect, 1312010093: EffectKindUnknown} {
		if got, _ := classifyEffectKind(EffectCombatClassUnknown, effectCombatBasisNoEvidence, p.effectStaticInfo(id)); got != kind {
			t.Errorf("effect %d without other evidence: kind %s, want %s", id, got, kind)
		}
	}
}
