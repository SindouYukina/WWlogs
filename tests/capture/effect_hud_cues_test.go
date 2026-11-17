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
		{"an attribute modifier stays a modifier", EffectCombatClassCombat, EffectCombatBasisAttribute + "CritDamage", hud(), EffectKindStatModifier},
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
	if got := effectPolarityFor(effect); got != EffectPolarityUnclassified {
		t.Fatalf("a HUD effect without a direction cue must stay unclassified: %s", got)
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
	statics := map[uint64]BuffStaticParameters{}
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
	older := map[uint64]BuffStaticParameters{}
	if _, err := db.Exec("DROP TABLE gameplaycue"); err != nil {
		t.Fatal(err)
	}
	if err := loadBuffHUDCues(db, older); err != nil || len(older) != 0 {
		t.Fatalf("a Master without gameplaycue: %v %v", older, err)
	}
}

func TestLoadBuffHUDCuesReadsDirectionAndMonsterHeadFromTheFirstCue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cues.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE TABLE buff_gameplay_cue_ids(owner_id TEXT,ordinal INTEGER,value TEXT,PRIMARY KEY(owner_id,ordinal))",
		"CREATE TABLE gameplaycue(id TEXT PRIMARY KEY,cue_type INTEGER NOT NULL,path_asset_id TEXT)",
		"CREATE TABLE gameplaycue_parameters(owner_id TEXT NOT NULL,ordinal INTEGER NOT NULL,value TEXT NOT NULL,PRIMARY KEY(owner_id,ordinal))",
		"INSERT INTO gameplaycue VALUES('1',2,'a'),('2',2,'b'),('3',14,'c'),('4',24,'d'),('5',2,'e'),('6',2,'f')",
		"INSERT INTO gameplaycue_parameters VALUES('1',0,'1'),('2',0,'2'),('3',0,'2'),('4',0,'0'),('5',0,''),('2',4,'1')",
		// 20: buff arrow; 21: debuff arrow hidden on monster heads; 22: type 14 debuff listed second behind a type 2 buff;
		// 23: a value without an arrow; 24: an empty value; 25: a cue with no parameter rows (an empty Parameters vector).
		// The HUD draws 23 to 25 with their icon and without an arrow.
		"INSERT INTO buff_gameplay_cue_ids VALUES('20',0,'1'),('21',0,'2'),('22',0,'1'),('22',1,'3'),('23',0,'4'),('24',0,'5'),('25',0,'6')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	statics := map[uint64]BuffStaticParameters{}
	if err := loadBuffHUDCues(db, statics); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uint64]struct {
		polarity EffectPolarity
		hidden   bool
	}{
		20: {EffectPolarityBuff, false}, 21: {EffectPolarityDebuff, true}, 22: {EffectPolarityBuff, false},
		23: {EffectPolarityNeutral, false}, 24: {EffectPolarityNeutral, false}, 25: {EffectPolarityNeutral, false},
	} {
		if got := statics[id]; got.hudPolarity != want.polarity || got.hudHiddenOnMonsterHead != want.hidden {
			t.Errorf("effect %d = %s hidden=%v, want %+v", id, got.hudPolarity, got.hudHiddenOnMonsterHead, want)
		}
	}
	// Without the parameters table there is no direction evidence at all: an older Master stays unclassified.
	if _, err := db.Exec("DROP TABLE gameplaycue_parameters"); err != nil {
		t.Fatal(err)
	}
	older := map[uint64]BuffStaticParameters{}
	if err := loadBuffHUDCues(db, older); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{20, 23, 25} {
		if got := older[id].hudPolarity; got != EffectPolarityUnclassified {
			t.Errorf("effect %d without cue parameters = %s, want unclassified", id, got)
		}
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
	for id, polarity := range map[uint64]EffectPolarity{
		2002202311: EffectPolarityBuff, 2002203902: EffectPolarityBuff, 1311400208: EffectPolarityBuff,
		10010000: EffectPolarityDebuff, 10041000: EffectPolarityDebuff, 10061000: EffectPolarityDebuff, 9100001202: EffectPolarityDebuff,
		// Role statuses the HUD draws with their icon and without an arrow: Xin's type-24 statuses, and Lily's (role 1512)
		// four Global 3.8.9 statuses, whose cues 1512623002..1512623005 (T_IconLiliBuff02..05) are type 2 with an empty
		// Parameters vector in the 3.8.9 hotfix db_buffeffect.db, read with the shipped GameplayCue.js slots.
		1311400007: EffectPolarityNeutral, 1311400008: EffectPolarityNeutral,
		1512310003: EffectPolarityNeutral, 1512800104: EffectPolarityNeutral, 2002205502: EffectPolarityNeutral, 1512999015: EffectPolarityNeutral,
	} {
		static, ok := master.buffStatic(id)
		if !ok || static.hudPolarity != polarity {
			t.Errorf("effect %d HUD polarity = %s (row %v), want %s", id, static.hudPolarity, ok, polarity)
		}
	}
	p := newEffectViewProcessor()
	p.Masters = master
	for id, kind := range map[uint64]EffectKind{2002202312: EffectKindHUDEffect, 1312010093: EffectKindUnknown} {
		if got, _ := classifyEffectKind(EffectCombatClassUnknown, effectCombatBasisNoEvidence, p.EffectStaticInfo(id)); got != kind {
			t.Errorf("effect %d without other evidence: kind %s, want %s", id, got, kind)
		}
	}
}
