package capture

import (
	"database/sql"
	"testing"
)

func TestTemplateConfigTypeNamesDynamicallySpawnedEntities(t *testing.T) {
	resolver := NewEntityResolver()
	resolver.Entities[500] = RuntimeEntity{EntityID: 500, ConfigID: 650000050, HasConfigID: true, ConfigType: 4, HasConfigType: true, SourceMessageID: CmdEntityAddNotify}
	resolver.Entities[501] = RuntimeEntity{EntityID: 501, ConfigID: 650000050, HasConfigID: true, ConfigType: 1, HasConfigType: true, SourceMessageID: CmdEntityAddNotify}
	masters := &GameMaster{Locale: "zh-TW", SceneIdentity: &SceneIdentityMaster{
		TemplateCatalog: map[uint64]SceneTemplateCandidate{650000050: {TemplateID: 650000050, Name: "Boss", NameStatus: "VERIFIED"}},
	}}

	named := ResolveEntityIdentity(resolver, masters, 500)
	if len(named.TemplateCandidates) != 1 || named.TemplateCandidates[0].Name != "Boss" || named.TemplateStatus != ResolutionCorrelated {
		t.Fatalf("a Template(4) ConfigId must resolve to its TemplateConfig row: %+v", named)
	}
	level := ResolveEntityIdentity(resolver, masters, 501)
	if len(level.TemplateCandidates) != 0 {
		t.Fatalf("a Level(1) ConfigId is not a TemplateConfig id and must not be named this way: %+v", level)
	}
}

func TestTemplateWithoutOfficialNameCarriesDeveloperLabel(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"INSERT INTO templateconfig VALUES('36000052','Vehicle_Test','TsEntity_载具_摩托_技能用')",
		"CREATE TABLE dev_label_locale(namespace TEXT, id TEXT, locale TEXT, label TEXT, label_kind TEXT)",
		"INSERT INTO dev_label_locale VALUES('templateconfig','36000052','zh-CN','载具_摩托_技能用','developer-original'),('templateconfig','36000052','en-US','Vehicle: Motorcycle (Skill Use)','unofficial-translation')",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	master, err := LoadGameMasterDatabase(path, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	candidate, ok := master.templateByID(36000052)
	if !ok {
		t.Fatal("TemplateConfig 36000052 must be in the catalog")
	}
	if candidate.Name != "" || candidate.DevLabel == nil || candidate.DevLabel.Kind != "unofficial-translation" || candidate.DevLabel.Locale != "en-US" {
		t.Fatalf("master locale label must be attached without becoming an official name: %+v", candidate)
	}
}

func TestCommonBulletDevLabelTravelsWithTheBulletResolution(t *testing.T) {
	master := &GameMaster{
		Locale:                "en-US",
		CommonBulletDevLabels: map[uint64]*CombatEventLabelTextJSONL{210002002: {Locale: "en-US", Text: "Ultimate Pull", Kind: "unofficial-translation"}},
	}
	if got := master.CommonBulletDevLabels[210002002]; got == nil || got.Text != "Ultimate Pull" {
		t.Fatalf("bullet developer label must be addressable by BulletId: %+v", got)
	}
}
