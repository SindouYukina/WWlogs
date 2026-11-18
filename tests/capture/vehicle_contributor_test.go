package capture

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadSQLiteBlueprintEntityTypesPreservesAmbiguity(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "vehicle-master.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE blueprintconfig (blueprint_type TEXT, entity_type TEXT);
		INSERT INTO blueprintconfig VALUES ('MotorcycleBP', 'BattleVehicle'), ('MotorcycleBP', 'OtherType');`); err != nil {
		t.Fatal(err)
	}

	master := &GameMaster{}
	if err := loadSQLiteBlueprintEntityTypes(db, master); err != nil {
		t.Fatal(err)
	}
	if len(master.BlueprintEntityTypes["MotorcycleBP"]) != 2 {
		t.Fatalf("blueprint entity types = %v, want both conflicting values retained", master.BlueprintEntityTypes["MotorcycleBP"])
	}
	if _, ok := master.BlueprintEntityTypes["MotorcycleBP"]["BattleVehicle"]; !ok {
		t.Fatal("BattleVehicle evidence was lost")
	}
}

func TestResolveVehicleDamageContributorCandidateRequiresCorrelatedChain(t *testing.T) {
	resolver, master, event := vehicleDamageContributorFixture()
	candidate := ResolveVehicleDamageContributorCandidate(resolver, master, event)
	if candidate == nil {
		t.Fatal("complete motorcycle chain did not resolve a contributor candidate")
	}
	if candidate.ActorType != "Vehicle" || candidate.EntityType != "BattleVehicle" || candidate.FollowerConfigID != 290101 || candidate.BlueprintType != "branch3.0_Motorcycle_Skill" || candidate.Status != ResolutionCorrelated {
		t.Fatalf("candidate = %+v", candidate)
	}

	cases := map[string]func(*EntityResolver, *GameMaster, *CombatEvent){
		"no linked motorcycle": func(r *EntityResolver, _ *GameMaster, e *CombatEvent) {
			delete(r.Entities, 11)
		},
		"wrong linked entity type": func(r *EntityResolver, _ *GameMaster, _ *CombatEvent) {
			entity := r.Entities[11]
			entity.SummonType = SummonTypeConcomitantVision
			r.Entities[11] = entity
		},
		"wrong linked summoner": func(r *EntityResolver, _ *GameMaster, _ *CombatEvent) {
			entity := r.Entities[11]
			entity.SummonerID = 99
			r.Entities[11] = entity
		},
		"unmatched bullet": func(_ *EntityResolver, _ *GameMaster, e *CombatEvent) {
			e.BulletID = 100010111
		},
		"skill not listed for vehicle actor": func(_ *EntityResolver, m *GameMaster, _ *CombatEvent) {
			m.ActorSkills["motorcycle-actor"] = map[uint64]struct{}{10001010: {}}
		},
		"missing skill message id": func(_ *EntityResolver, _ *GameMaster, e *CombatEvent) {
			e.SkillMessageID = 0
		},
		"ambiguous blueprint entity type": func(_ *EntityResolver, m *GameMaster, _ *CombatEvent) {
			m.BlueprintEntityTypes["branch3.0_Motorcycle_Skill"]["OtherType"] = struct{}{}
		},
		"non-damage result": func(_ *EntityResolver, _ *GameMaster, e *CombatEvent) {
			e.EventTypeCandidate = "HEALING_CANDIDATE"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, m, e := vehicleDamageContributorFixture()
			mutate(r, m, &e)
			if got := ResolveVehicleDamageContributorCandidate(r, m, e); got != nil {
				t.Fatalf("unexpected candidate: %+v", got)
			}
		})
	}
}

func TestResolveVehicleDamageContributorCandidateWithoutResolvedSkillRequest(t *testing.T) {
	resolver, master, event := vehicleDamageContributorFixture()
	candidate := ResolveVehicleDamageContributorCandidate(resolver, master, event)
	if candidate == nil {
		t.Fatal("verified runtime and static vehicle chain did not resolve without an optional request link")
	}
	if candidate.Status != ResolutionCorrelated || !strings.Contains(candidate.Evidence, "no matching outbound UseSkillRequest was resolved") {
		t.Fatalf("missing request evidence was not kept explicit: %+v", candidate)
	}
}

// The outbound skill request exists only with a research schema (-protocol-schema); it is not core evidence, so it
// neither confirms nor vetoes the vehicle candidate (Phase 3A R03). The core damage event the candidate is decided from
// cannot carry it: no field of combatEvent, at any depth, holds the research request record. The Development event line
// makes and writes the link itself (writeDamageEventOutputs).
func TestCoreDamageEventCarriesNoResearchSkillRequest(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type, string)
	visit = func(value reflect.Type, path string) {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Array || value.Kind() == reflect.Map {
			value = value.Elem()
		}
		if strings.Contains(value.Name(), "combatActionRequest") {
			t.Fatalf("%s holds the research skill request (%s)", path, value)
		}
		if value.Kind() != reflect.Struct || seen[value] {
			return
		}
		seen[value] = true
		for i := 0; i < value.NumField(); i++ {
			visit(value.Field(i).Type, path+"."+value.Field(i).Name)
		}
	}
	visit(reflect.TypeOf(CombatEvent{}), "combatEvent")
	resolver, master, event := vehicleDamageContributorFixture()
	if ResolveVehicleDamageContributorCandidate(resolver, master, event) == nil {
		t.Fatal("fixture chain did not resolve without a request")
	}
}

func vehicleDamageContributorFixture() (*EntityResolver, *GameMaster, CombatEvent) {
	source := RuntimeEntity{
		EntityID: 10, FollowerSummonConfigID: 290101, HasFollowerSummonConfigID: true,
		FollowEntityID: 11, HasFollowEntityID: true, SourceMessageID: CmdEntityAddNotify,
	}
	motorcycle := RuntimeEntity{
		EntityID: 11, SummonerID: 10, HasSummonerID: true,
		SummonType: SummonTypeConcomitantMotorcycle, HasSummonType: true,
		SummonSkillID: 6013, HasSummonSkillID: true, SourceMessageID: CmdEntityAddNotify,
	}
	resolver := &EntityResolver{Entities: map[uint64]RuntimeEntity{10: source, 11: motorcycle}}
	master := &GameMaster{
		Visions:         map[uint64]struct{}{6013: {}},
		SummonBlueprint: map[uint64]string{290101: "branch3.0_Motorcycle_Skill"},
		BlueprintEntityTypes: map[string]map[string]struct{}{
			"branch3.0_Motorcycle_Skill": {"BattleVehicle": {}},
		},
		BlueprintModel: map[string]uint64{"branch3.0_Motorcycle_Skill": 900000},
		ModelActor:     map[uint64]string{900000: "motorcycle-actor"},
		AssetPath:      map[string]string{"motorcycle-actor": "/Game/Vehicle/BP_Motorcycle_C"},
		ActorBullets:   map[string]map[uint64]struct{}{"motorcycle-actor": {100010101: {}}},
		ActorSkills:    map[string]map[uint64]struct{}{"motorcycle-actor": {10001009: {}}},
	}
	event := CombatEvent{
		SessionNumber: 1, EventTypeCandidate: "DAMAGE_CANDIDATE", SourceEntityID: 10, HasSourceEntity: true,
		Source:  EntityIdentity{Runtime: source, RuntimeStatus: ResolutionVerified},
		SkillID: 10001009, SkillMessageID: 50, HasBulletID: true, BulletID: 100010101,
		CharacterStatus: ResolutionUnknown, CharacterNameStatus: ResolutionUnknown,
	}
	return resolver, master, event
}
