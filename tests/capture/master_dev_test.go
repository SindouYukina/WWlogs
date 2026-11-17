//go:build wwdev

package capture

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadGameMasterDatabaseLoadsNormalizedShieldDefinitionKeys(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE shield(id TEXT PRIMARY KEY)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO shield VALUES('1409404003'),('1508990003'),('18446744073709551614')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1409404003, 1508990003, 18446744073709551614} {
		if !hasShieldDefinition(master, id) {
			t.Fatalf("normalized shield definition %d was not loaded", id)
		}
	}
	if hasShieldDefinition(master, 1312010090) {
		t.Fatal("missing Shield ID unexpectedly resolved")
	}
}

func TestResolveBulletFollowsSourceChainToExactlyOneActor(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE asset_reference(id TEXT PRIMARY KEY,path TEXT)",
		"INSERT INTO asset_reference VALUES('actorA','/Game/A.A_C'),('actorB','/Game/B.B_C'),('actorV','/Game/V.V_C')",
		"CREATE TABLE modelconfigpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT)",
		"INSERT INTO modelconfigpreload VALUES('10','actorA'),('20','actorB'),('30','actorV')",
		"CREATE TABLE roleskin(id TEXT PRIMARY KEY,role_id TEXT,mesh_id TEXT)",
		"INSERT INTO roleskin VALUES('9001','1312','10')",
		"CREATE TABLE blueprintconfig(id TEXT PRIMARY KEY,blueprint_type TEXT,model_id TEXT)",
		"INSERT INTO blueprintconfig VALUES('1','MonsterB','20'),('2','VisionBP','30')",
		"CREATE TABLE summoncfg(id TEXT PRIMARY KEY,blueprint_type TEXT)",
		"INSERT INTO summoncfg VALUES('280026','VisionBP')",
		"CREATE TABLE vision(id TEXT PRIMARY KEY,birth_cast_skill_id TEXT)",
		"INSERT INTO vision VALUES('280026','280026010')",
		"CREATE TABLE bulletpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,bullet_id TEXT)",
		"INSERT INTO bulletpreload VALUES('1','actorA','111'),('2','actorB','222'),('3','actorV','280026012'),('4','actorA','333'),('5','actorB','333'),('6','actorA','444')",
		"CREATE TABLE entityskillpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,skill_id TEXT)",
		"INSERT INTO entityskillpreload VALUES('1','actorA','501'),('2','actorA','502'),('3','actorB','502'),('4','actorB','503')",
		"CREATE TABLE commonbullet(id TEXT PRIMARY KEY,dev_name_zh_cn TEXT)",
		"INSERT INTO commonbullet VALUES('210002005','label-a'),('444','label-shared')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if actor, ok := uniqueActorForSkill(master, 501); !ok || actor != "actorA" {
		t.Fatalf("a SkillId listed by exactly one actor asset must resolve in the reverse index: %q %t", actor, ok)
	}
	if _, ok := uniqueActorForSkill(master, 502); ok {
		t.Fatal("a SkillId listed by multiple actor assets must remain ambiguous")
	}
	character := bulletSource{RoleSkinID: 9001, HasRoleSkin: true}
	if r := resolveBullet(master, 111, character); r.Status != resolutionCorrelated || r.Namespace != "BulletPreload.ActorBlueprint" || r.ActorBlueprint != "/Game/A.A_C" {
		t.Fatalf("a character bullet held by its own actor must correlate: %+v", r)
	}
	// The chain is read from the combat master; the protocol schema label names where field names come from, not it.
	if r := resolveBullet(master, 111, character); !strings.Contains(r.Proof, "BulletPreload rows in the combat master") || strings.Contains(r.Proof, "pinned") {
		t.Fatalf("the preload chain proof must name the combat master it was read from: %q", r.Proof)
	}
	if r := resolveBullet(master, 111, bulletSource{SkillID: 501}); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/A.A_C" || r.Via == "" {
		t.Fatalf("a co-referenced SkillId with one actor owner must resolve its listed bullet: %+v", r)
	}
	if r := resolveBullet(master, 222, bulletSource{SkillID: 502}); r.Status != resolutionUnknown {
		t.Fatalf("a SkillId shared by multiple actors must not select a bullet owner: %+v", r)
	}
	if r := resolveBullet(master, 222, bulletSource{RoleSkinID: 9001, HasRoleSkin: true, SkillID: 503}); r.Status != resolutionUnknown {
		t.Fatalf("a runtime source chain must not be overridden by a conflicting SkillId owner: %+v", r)
	}
	if r := resolveBullet(master, 222, character); r.Status != resolutionUnknown {
		t.Fatalf("a bullet held only by another actor must not resolve for this source: %+v", r)
	}
	monster := bulletSource{BlueprintTypes: []string{"MonsterB"}}
	if r := resolveBullet(master, 222, monster); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/B.B_C" {
		t.Fatalf("a template-derived actor bullet must correlate: %+v", r)
	}
	both := bulletSource{RoleSkinID: 9001, HasRoleSkin: true, BlueprintTypes: []string{"MonsterB"}}
	if r := resolveBullet(master, 333, both); r.Status != resolutionUnknown || r.Namespace != "" {
		t.Fatalf("a key held by two source-chain actors must stay ambiguous: %+v", r)
	}
	echo := bulletSource{RoleSkinID: 9001, HasRoleSkin: true, SkillID: 280026010}
	if r := resolveBullet(master, 280026012, echo); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/V.V_C" {
		t.Fatalf("an echo bullet reached through Vision.BirthCastSkillId must correlate to the echo actor: %+v", r)
	}
	if r := resolveBullet(master, 210002005, character); r.Status != resolutionCorrelated || r.Namespace != "CDT_CommonBulletData.RowKey" {
		t.Fatalf("a common-table-only key must correlate to the common table: %+v", r)
	}
	if r := resolveBullet(master, 444, character); r.Status != resolutionUnknown {
		t.Fatalf("a key held by both the source actor and the common table must stay ambiguous: %+v", r)
	}
	if r := resolveBullet(master, 999, character); r.Status != resolutionUnknown {
		t.Fatalf("an unknown key must stay unknown: %+v", r)
	}
	if resolveBullet(nil, 1, character) != nil {
		t.Fatal("no master means no resolution")
	}
}

func TestResolveBulletUsesLiveVisionSummonOfTheSourceEntity(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE asset_reference(id TEXT PRIMARY KEY,path TEXT)",
		"INSERT INTO asset_reference VALUES('actorV','/Game/V.V_C')",
		"CREATE TABLE modelconfigpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT)",
		"INSERT INTO modelconfigpreload VALUES('30','actorV')",
		"CREATE TABLE blueprintconfig(id TEXT PRIMARY KEY,blueprint_type TEXT,model_id TEXT)",
		"INSERT INTO blueprintconfig VALUES('2','VisionBP','30')",
		"CREATE TABLE summoncfg(id TEXT PRIMARY KEY,blueprint_type TEXT)",
		"INSERT INTO summoncfg VALUES('280045','VisionBP')",
		"CREATE TABLE bulletpreload(id TEXT PRIMARY KEY,actor_asset_id TEXT,bullet_id TEXT)",
		"INSERT INTO bulletpreload VALUES('1','actorV','280045101')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	with := bulletSource{VisionSummonCfgs: []uint64{280045}}
	if r := resolveBullet(master, 280045101, with); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/V.V_C" {
		t.Fatalf("a live vision summon of the source entity must lead to the echo actor: %+v", r)
	}
	own := bulletSource{OwnSummonCfgID: 280045, HasOwnSummonCfgID: true}
	if r := resolveBullet(master, 280045101, own); r.Status != resolutionCorrelated || r.ActorBlueprint != "/Game/V.V_C" {
		t.Fatalf("a summon firing its own bullet must resolve through its own SummonCfg: %+v", r)
	}
	if r := resolveBullet(master, 280045101, bulletSource{}); r.Status != resolutionUnknown {
		t.Fatalf("without a vision summon the bullet must stay a numeric match: %+v", r)
	}

	resolver := &entityResolver{entities: map[uint64]runtimeEntity{
		92: {entityID: 92, summonerID: 90, hasSummonerID: true, summonType: 1, hasSummonType: true, summonConfigID: 280045, hasSummonConfigID: true},
		93: {entityID: 93, summonerID: 90, hasSummonerID: true, summonType: 5, hasSummonType: true, summonConfigID: 6013, hasSummonConfigID: true},
		94: {entityID: 94, summonerID: 77, hasSummonerID: true, summonType: 1, hasSummonType: true, summonConfigID: 280026, hasSummonConfigID: true},
	}}
	if got := resolver.visionSummonConfigs(90); len(got) != 1 || got[0] != 280045 {
		t.Fatalf("only ConcomitantVision summons of the named summoner count: %v", got)
	}
	if resolver.visionSummonConfigs(0) != nil || (*entityResolver)(nil).visionSummonConfigs(90) != nil {
		t.Fatal("no summoner or no resolver must yield nothing")
	}
}

func TestSceneTemplateDisplayRequiresUniqueLocalizedCandidate(t *testing.T) {
	candidate := sceneTemplateCandidate{TemplateID: 7, Name: "Localized Boss", NameStatus: string(resolutionVerified), NameTextKey: "Entity.Name", Language: "en-US"}
	for _, count := range []int{1, 2} {
		identity := entityIdentity{TemplateStatus: resolutionCorrelated, TemplateCandidates: []sceneTemplateCandidate{candidate}}
		if count == 2 {
			identity.TemplateCandidates = append(identity.TemplateCandidates, sceneTemplateCandidate{TemplateID: 8, Name: "Localized Boss", NameStatus: string(resolutionVerified)})
		}
		destination := combatEventEntityJSONL{}
		applySceneTemplateJSONL(&destination, identity)
		if count == 1 && (destination.SceneTemplate.Name != "Localized Boss" || destination.SceneTemplate.NameStatus != resolutionVerified) {
			t.Fatalf("unique localized name not exposed: %+v", destination.SceneTemplate)
		}
		if count == 2 && (destination.SceneTemplate.Name != "" || destination.SceneTemplate.NameStatus != resolutionUnknown) {
			t.Fatalf("ambiguous names selected a candidate: %+v", destination.SceneTemplate)
		}
	}
}

func TestUnknownTemplateIdentityDoesNotPublishLocalizedTopLevelName(t *testing.T) {
	identity := entityIdentity{TemplateStatus: resolutionUnknown, TemplateCandidates: []sceneTemplateCandidate{{TemplateID: 7, Name: "Candidate", NameStatus: string(resolutionVerified)}}}
	destination := combatEventEntityJSONL{}
	applySceneTemplateJSONL(&destination, identity)
	if destination.SceneTemplate.Name != "" || destination.SceneTemplate.NameStatus != resolutionUnknown {
		t.Fatalf("unknown identity published a display name: %+v", destination.SceneTemplate)
	}
}

func TestSceneTemplateResolutionDoesNotConflateTemplateConfigWithMonsterId(t *testing.T) {
	master := testSceneTemplateMaster([]sceneTemplateCandidate{{
		TemplateID: 667000003, BlueprintType: "Monster_branch1.3_013", RawName: "internal boss label",
	}})
	resolver := &entityResolver{entities: map[uint64]runtimeEntity{
		123: {
			entityID: 123, sceneInstanceID: 50, hasSceneInstanceID: true,
			configID: 244750005, hasConfigID: true, configType: 1, hasConfigType: true,
			sourceMessageID: cmdJoinSceneNotify,
		},
	}}
	identity := resolveEntityIdentity(resolver, master, 123)
	if identity.HasMonster || identity.Monster.ID != 0 {
		t.Fatalf("TemplateConfig must not be exposed as a MonsterId: %+v", identity)
	}
	if len(identity.TemplateCandidates) != 1 || identity.TemplateCandidates[0].TemplateID != 667000003 || identity.TemplateStatus != resolutionCorrelated {
		t.Fatalf("map-scoped TemplateConfig was not correlated: %+v", identity)
	}
	if identity.TemplateCandidates[0].RawName != "internal boss label" || identity.TemplateProof == "" {
		t.Fatalf("template raw label or evidence missing: %+v", identity)
	}

	view := jsonlView((combatEvent{Target: identity}), master)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	target := decoded["target"].(map[string]any)
	if _, exists := target["monster"]; exists {
		t.Fatalf("template identity was mislabeled as monster: %s", encoded)
	}
	template, ok := target["sceneTemplate"].(map[string]any)
	if !ok || template["identityType"] != "template-derived" || template["status"] != string(resolutionCorrelated) || template["nameStatus"] != string(resolutionUnknown) {
		t.Fatalf("template identity JSON lost evidence or unknown localization: %s", encoded)
	}
}
