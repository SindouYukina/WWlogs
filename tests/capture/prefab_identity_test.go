package capture

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// EntityConfigType Prefab is enum value 5 in the Global 3.8 client protocol (Protocol.js rLs: "lTs" = 5). Such an entity is
// one entity of the prefab EntityPb.PrefabId names (field 17, c3s.LEs) and EntityPb.ConfigId is its EntityData.Id there
// (CreateEntityData.Init: PrefabId = EntityData.LEs; InitPbEntityData -> CreatureModel.GetEntityPrefab).

func TestEntityPbReadsThePrefabID(t *testing.T) {
	data := appendVarintField(nil, FieldEntityPbID, 13056)
	data = appendVarintField(data, FieldEntityPbConfigID, 1850002)
	data = appendVarintField(data, FieldEntityPbConfigType, 5)
	data = appendVarintField(data, fieldEntityPbEntityType, 2)
	data = appendVarintField(data, fieldEntityPbPrefabID, 222850001)
	entity, err := parseEntityPb(data)
	if err != nil {
		t.Fatal(err)
	}
	if !entity.HasPrefabID || entity.PrefabID != 222850001 || entity.ConfigID != 1850002 || entity.ConfigType != 5 {
		t.Fatalf("EntityPb.PrefabId was not read: %+v", entity)
	}
	without, err := parseEntityPb(appendVarintField(appendVarintField(nil, FieldEntityPbID, 13057), FieldEntityPbConfigType, 5))
	if err != nil {
		t.Fatal(err)
	}
	if without.HasPrefabID {
		t.Fatalf("an absent PrefabId must stay absent, never 0 as a value: %+v", without)
	}
	// A later update of the same entity without the field keeps the prefab it was spawned from.
	if merged := mergeRuntimeEntity(entity, RuntimeEntity{EntityID: 13056}); !merged.HasPrefabID || merged.PrefabID != 222850001 {
		t.Fatalf("a merge dropped the PrefabId: %+v", merged)
	}
}

func prefabTestMaster() *GameMaster {
	predator := SceneTemplateCandidate{TemplateID: 24000013, Name: "Glacio Predator", NameStatus: "VERIFIED"}
	other := SceneTemplateCandidate{TemplateID: 24000014, Name: "Other", NameStatus: "VERIFIED"}
	return &GameMaster{Locale: "en-US",
		Monsters: map[uint64]MasterRecord{310000040: {ID: 310000040, Name: "Glacio Predator", Status: "VERIFIED"}},
		SceneIdentity: &SceneIdentityMaster{
			prefabMonsters: map[prefabEntityKey]uint64{{prefabID: 222850001, entityID: 1850000}: 310000040},
			prefabTemplates: map[prefabEntityKey][]SceneTemplateCandidate{
				{prefabID: 222850001, entityID: 1850000}: {predator},
				// A relation that answers with two templates is a conflict: it names neither.
				{prefabID: 222850001, entityID: 1850001}: {predator, other},
			},
		}}
}

func TestPrefabConfigTypeNamesEntitiesSpawnedFromPrefabs(t *testing.T) {
	resolver := NewEntityResolver()
	prefab := func(id, config, prefabID uint64, hasPrefab bool, configType uint64) RuntimeEntity {
		return RuntimeEntity{EntityID: id, ConfigID: config, HasConfigID: true, ConfigType: configType, HasConfigType: true, PrefabID: prefabID, HasPrefabID: hasPrefab,
			EntityTypeCode: EntityTypeMonster, HasEntityType: true, SourceMessageID: CmdEntityAddNotify}
	}
	resolver.Entities[500] = prefab(500, 1850000, 222850001, true, 5)
	resolver.Entities[501] = prefab(501, 1850001, 222850001, true, 5) // two templates in the relation
	resolver.Entities[502] = prefab(502, 1850000, 0, false, 5)        // no PrefabId on the wire
	resolver.Entities[503] = prefab(503, 1850099, 222850001, true, 5) // the Master has no row for this entity
	resolver.Entities[504] = prefab(504, 1850000, 222850001, true, 1) // a Level entity is never named through a prefab
	masters := prefabTestMaster()

	named := ResolveEntityIdentity(resolver, masters, 500)
	if len(named.TemplateCandidates) != 1 || named.TemplateCandidates[0].TemplateID != 24000013 || named.TemplateStatus != ResolutionCorrelated {
		t.Fatalf("a Prefab(5) entity must resolve to the TemplateConfig of its prefab entry: %+v", named)
	}
	if !named.HasMonster || named.Monster.ID != 310000040 || named.MonsterStatus != ResolutionCorrelated || !strings.Contains(named.MonsterProof, "PrefabId=222850001") {
		t.Fatalf("a Prefab(5) entity with one MonsterInfo of its BlueprintType must name it, CORRELATED: %+v", named)
	}
	if actor := combatActorTypeOf(named); actor.MonsterID != 310000040 || actor.TemplateID != 24000013 || actor.MonsterStatus != "CORRELATED" || actor.TemplateStatus != "CORRELATED" {
		t.Fatalf("the History enemy type must keep both links: %+v", actor)
	}

	conflict := ResolveEntityIdentity(resolver, masters, 501)
	if len(conflict.TemplateCandidates) != 2 || conflict.TemplateStatus != ResolutionUnknown || conflict.HasMonster {
		t.Fatalf("two templates for one prefab entity must stay unresolved: %+v", conflict)
	}
	if actor := combatActorTypeOf(conflict); actor.TemplateID != 0 || actor.MonsterID != 0 {
		t.Fatalf("an unresolved prefab entity must not reach the History with a guessed type: %+v", actor)
	}
	for _, id := range []uint64{502, 503, 504} {
		identity := ResolveEntityIdentity(resolver, masters, id)
		if len(identity.TemplateCandidates) != 0 || identity.HasMonster {
			t.Fatalf("entity %d must stay unidentified: %+v", id, identity)
		}
		if actor := combatActorTypeOf(identity); actor.TemplateID != 0 || actor.MonsterID != 0 || actor.TemplateStatus != "UNKNOWN" {
			t.Fatalf("entity %d: %+v", id, actor)
		}
	}
}

func TestPrefabRelationsLoadFromTheMaster(t *testing.T) {
	plain := buildMasterDatabaseFixture(t)
	master, err := LoadGameMasterDatabase(plain, "zh-TW")
	if err != nil {
		t.Fatalf("a Master without prefab relations must still load: %v", err)
	}
	if len(master.SceneIdentity.prefabTemplates) != 0 || len(master.SceneIdentity.prefabMonsters) != 0 {
		t.Fatal("a Master without the tables knows no prefab entity")
	}
	withRelations := func(t *testing.T, extra ...string) string {
		path := buildMasterDatabaseFixture(t)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		statements := append([]string{
			"INSERT INTO templateconfig VALUES('24000013','Monster010',''),('24000014','Monster011','')",
			"CREATE TABLE prefab_entity_monster(prefab_id TEXT NOT NULL, entity_id TEXT NOT NULL, monster_id TEXT NOT NULL, PRIMARY KEY(prefab_id,entity_id,monster_id))",
			"CREATE TABLE prefab_entity_template_ref(prefab_id TEXT NOT NULL, entity_id TEXT NOT NULL, ordinal INTEGER NOT NULL, template_id TEXT NOT NULL, PRIMARY KEY(prefab_id,entity_id,ordinal))",
			"INSERT INTO prefab_entity_monster VALUES('222850001','1850000','700'),('222850001','1850001','700'),('222850001','1850001','701')",
			"INSERT INTO prefab_entity_template_ref VALUES('222850001','1850000',0,'24000013'),('222850001','1850001',0,'24000013'),('222850001','1850001',1,'24000014')",
		}, extra...)
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		return path
	}
	master, err = LoadGameMasterDatabase(withRelations(t), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := master.prefabMonster(222850001, 1850000); !ok || got != 700 {
		t.Fatalf("one MonsterInfo for the entity: %d %v", got, ok)
	}
	if got, ok := master.prefabMonster(222850001, 1850001); ok {
		t.Fatalf("two MonsterInfo rows for one entity must name neither, got %d", got)
	}
	if got, ok := master.prefabTemplates(222850001, 1850000); !ok || len(got) != 1 || got[0].TemplateID != 24000013 {
		t.Fatalf("templates of 1850000: %+v", got)
	}
	if got, _ := master.prefabTemplates(222850001, 1850001); len(got) != 2 || got[0].TemplateID != 24000013 || got[1].TemplateID != 24000014 {
		t.Fatalf("every candidate is kept in ordinal order: %+v", got)
	}
	// A relation naming a row the Master does not hold refuses the Master, like a broken level relation.
	if _, err := LoadGameMasterDatabase(withRelations(t, "INSERT INTO prefab_entity_template_ref VALUES('222850001','1850002',0,'99999999')"), "zh-TW"); err == nil || !strings.Contains(err.Error(), "prefab_entity_template_ref") {
		t.Fatalf("a dangling template must refuse the Master: %v", err)
	}
	if _, err := LoadGameMasterDatabase(withRelations(t, "INSERT INTO prefab_entity_monster VALUES('222850001','1850003','99999999')"), "zh-TW"); err == nil || !strings.Contains(err.Error(), "prefab_entity_monster") {
		t.Fatalf("a dangling MonsterInfo must refuse the Master: %v", err)
	}
}

// capturedPrefabEnemies are prefab entities that took confirmed local damage in the Global 3.8.9 LIVE capture
// LIVE_20261113_224240 (Tower of Adversity and the ShipTower stages), read independently of the product decoder: the
// generic schema decode of its EntityAddNotify.EntityPbs[] (ConfigType 5, PrefabId, ConfigId), and the client's own
// PrefabConfig (db_prefab.db, the same file in the 3.8.9 and 3.8.11 hot patches) with TemplateConfig.BlueprintType
// (db_template.db) and MonsterInfo.MonsterEntityID (db_monster_Info.db), joined by work/version-update/tools/windows/prefab_identity.py.
// monster 0 means no MonsterInfo shares the BlueprintType.
var capturedPrefabEnemies = []struct {
	prefab, entity, template, monster uint64
	blueprint                         string
}{
	{222700104, 1700001, 665000001, 320000190, "Monster_branch1.1_001"},
	{222700105, 1700000, 665900001, 320000260, "Monster_branch2.0_030"},
	{222750009, 1750001, 665000015, 310000450, "Monster_branch2.0_022"},
	{222750016, 1750000, 24000049, 320000070, "Monster041"},
	{222850001, 1850000, 24000013, 310000040, "Monster010"},
	{401700024, 1700000, 604000001, 320000620, "branch3.2_604_Monster_branch3.2_002"},
	{405850005, 1850002, 650000041, 0, "Monster104"},
	{405850006, 1850000, 650000050, 0, "Monster113"},
	{405850007, 1850003, 24000009, 310000090, "Monster007"},
}

func TestEmbeddedMasterNamesTheCapturedPrefabEnemies(t *testing.T) {
	masters, err := LoadMasterData("", "en-US")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = masters.Close() })
	names := masters.DisplayResolver()
	for _, fact := range capturedPrefabEnemies {
		templates, ok := masters.masters.prefabTemplates(fact.prefab, fact.entity)
		if !ok || len(templates) != 1 || templates[0].TemplateID != fact.template || templates[0].BlueprintType != fact.blueprint {
			t.Fatalf("prefab %d entity %d: templates %+v, want %d (%s)", fact.prefab, fact.entity, templates, fact.template, fact.blueprint)
		}
		monster, ok := masters.masters.prefabMonster(fact.prefab, fact.entity)
		if fact.monster != 0 && (!ok || monster != fact.monster) || fact.monster == 0 && ok {
			t.Fatalf("prefab %d entity %d: monster %d %v, want %d", fact.prefab, fact.entity, monster, ok, fact.monster)
		}
		// The History names an enemy by its MonsterInfo link, else its TemplateConfig link (desktop nameProjection.enemy).
		for _, locale := range []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"} {
			var name DisplayName
			if fact.monster != 0 {
				if name, err = names.Resolve(context.Background(), "monster", fact.monster, locale); err != nil {
					t.Fatal(err)
				}
			}
			if name.Name == "" {
				if name, err = names.Resolve(context.Background(), "template", fact.template, locale); err != nil {
					t.Fatal(err)
				}
			}
			if name.Name == "" {
				t.Errorf("prefab %d entity %d has no %s name", fact.prefab, fact.entity, locale)
			}
		}
	}
}
