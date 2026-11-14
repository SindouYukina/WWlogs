package capture

import (
	"database/sql"
	"path/filepath"
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

	master := &gameMaster{}
	if err := loadSQLiteBlueprintEntityTypes(db, master); err != nil {
		t.Fatal(err)
	}
	if len(master.blueprintEntityTypes["MotorcycleBP"]) != 2 {
		t.Fatalf("blueprint entity types = %v, want both conflicting values retained", master.blueprintEntityTypes["MotorcycleBP"])
	}
	if _, ok := master.blueprintEntityTypes["MotorcycleBP"]["BattleVehicle"]; !ok {
		t.Fatal("BattleVehicle evidence was lost")
	}
}

func TestResolveVehicleDamageContributorCandidateRequiresCorrelatedChain(t *testing.T) {
	resolver, master, event := vehicleDamageContributorFixture()
	candidate := resolveVehicleDamageContributorCandidate(resolver, master, event)
	if candidate == nil {
		t.Fatal("complete motorcycle chain did not resolve a contributor candidate")
	}
	if candidate.ActorType != "Vehicle" || candidate.EntityType != "BattleVehicle" || candidate.FollowerConfigID != 290101 || candidate.BlueprintType != "branch3.0_Motorcycle_Skill" || candidate.Status != resolutionCorrelated {
		t.Fatalf("candidate = %+v", candidate)
	}

	cases := map[string]func(*entityResolver, *gameMaster, *combatEvent){
		"no linked motorcycle": func(r *entityResolver, _ *gameMaster, e *combatEvent) {
			delete(r.entities, 11)
		},
		"wrong linked entity type": func(r *entityResolver, _ *gameMaster, _ *combatEvent) {
			entity := r.entities[11]
			entity.summonType = summonTypeConcomitantVision
			r.entities[11] = entity
		},
		"wrong linked summoner": func(r *entityResolver, _ *gameMaster, _ *combatEvent) {
			entity := r.entities[11]
			entity.summonerID = 99
			r.entities[11] = entity
		},
		"unmatched bullet": func(_ *entityResolver, _ *gameMaster, e *combatEvent) {
			e.BulletID = 100010111
		},
		"skill not listed for vehicle actor": func(_ *entityResolver, m *gameMaster, _ *combatEvent) {
			m.actorSkills["motorcycle-actor"] = map[uint64]struct{}{10001010: {}}
		},
		"unlinked request source": func(_ *entityResolver, _ *gameMaster, e *combatEvent) {
			e.SkillRequest.EntityID = 99
		},
		"mismatched request message": func(_ *entityResolver, _ *gameMaster, e *combatEvent) {
			e.SkillRequest.MessageID = 99
		},
		"missing skill message id": func(_ *entityResolver, _ *gameMaster, e *combatEvent) {
			e.SkillMessageID = 0
		},
		"ambiguous blueprint entity type": func(_ *entityResolver, m *gameMaster, _ *combatEvent) {
			m.blueprintEntityTypes["branch3.0_Motorcycle_Skill"]["OtherType"] = struct{}{}
		},
		"non-damage result": func(_ *entityResolver, _ *gameMaster, e *combatEvent) {
			e.EventTypeCandidate = "HEALING_CANDIDATE"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, m, e := vehicleDamageContributorFixture()
			mutate(r, m, &e)
			if got := resolveVehicleDamageContributorCandidate(r, m, e); got != nil {
				t.Fatalf("unexpected candidate: %+v", got)
			}
		})
	}
}

func TestResolveVehicleDamageContributorCandidateWithoutResolvedSkillRequest(t *testing.T) {
	resolver, master, event := vehicleDamageContributorFixture()
	event.SkillRequest = nil
	candidate := resolveVehicleDamageContributorCandidate(resolver, master, event)
	if candidate == nil {
		t.Fatal("verified runtime and static vehicle chain did not resolve without an optional request link")
	}
	if candidate.Status != resolutionCorrelated || !strings.Contains(candidate.Evidence, "no matching outbound UseSkillRequest was resolved") {
		t.Fatalf("missing request evidence was not kept explicit: %+v", candidate)
	}
}

func vehicleDamageContributorFixture() (*entityResolver, *gameMaster, combatEvent) {
	source := runtimeEntity{
		entityID: 10, followerSummonConfigID: 290101, hasFollowerSummonConfigID: true,
		followEntityID: 11, hasFollowEntityID: true, sourceMessageID: cmdEntityAddNotify,
	}
	motorcycle := runtimeEntity{
		entityID: 11, summonerID: 10, hasSummonerID: true,
		summonType: summonTypeConcomitantMotorcycle, hasSummonType: true,
		summonSkillID: 6013, hasSummonSkillID: true, sourceMessageID: cmdEntityAddNotify,
	}
	resolver := &entityResolver{entities: map[uint64]runtimeEntity{10: source, 11: motorcycle}}
	master := &gameMaster{
		visions:         map[uint64]struct{}{6013: {}},
		summonBlueprint: map[uint64]string{290101: "branch3.0_Motorcycle_Skill"},
		blueprintEntityTypes: map[string]map[string]struct{}{
			"branch3.0_Motorcycle_Skill": {"BattleVehicle": {}},
		},
		blueprintModel: map[string]uint64{"branch3.0_Motorcycle_Skill": 900000},
		modelActor:     map[uint64]string{900000: "motorcycle-actor"},
		assetPath:      map[string]string{"motorcycle-actor": "/Game/Vehicle/BP_Motorcycle_C"},
		actorBullets:   map[string]map[uint64]struct{}{"motorcycle-actor": {100010101: {}}},
		actorSkills:    map[string]map[uint64]struct{}{"motorcycle-actor": {10001009: {}}},
	}
	event := combatEvent{
		SessionNumber: 1, EventTypeCandidate: "DAMAGE_CANDIDATE", SourceEntityID: 10, HasSourceEntity: true,
		Source:  entityIdentity{Runtime: source, RuntimeStatus: resolutionVerified},
		SkillID: 10001009, SkillMessageID: 50, HasBulletID: true, BulletID: 100010101,
		CharacterStatus: resolutionUnknown, CharacterNameStatus: resolutionUnknown,
		SkillRequest: &combatActionRequest{SessionNumber: 1, MessageID: 50, EntityID: 10, SkillID: 10001009, RequestType: "UseSkillRequest"},
	}
	return resolver, master, event
}
