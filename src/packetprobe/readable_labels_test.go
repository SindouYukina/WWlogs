package main

import (
	"database/sql"
	"testing"
)

func TestReadableLabelsLoadFromMasterAndFallBackToTheOriginal(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE skillinfo(asset_id TEXT, skill_id TEXT, dev_name_zh_cn TEXT)",
		"INSERT INTO skillinfo VALUES('a','80010001','横挥'),('a','80010002','撞'),('a','80010003','甲'),('b','80010003','乙')",
		"CREATE TABLE skill_dev_label_locale(skill_id TEXT NOT NULL, locale TEXT NOT NULL, label TEXT NOT NULL, label_kind TEXT NOT NULL, PRIMARY KEY(skill_id, locale))",
		"INSERT INTO skill_dev_label_locale VALUES('80010001','zh-CN','横挥','developer-original'),('80010001','en-US','Horizontal Swing','unofficial-translation'),('80010001','ja-JP','横振り','unofficial-translation')",
		"INSERT INTO skill_dev_label_locale VALUES('80010002','zh-CN','撞','developer-original')",
		"INSERT INTO skill_dev_label_locale VALUES('80010003','zh-CN','甲','developer-original'),('80010003','en-US','A','unofficial-translation')",
		"CREATE TABLE elementinfo_locale(id TEXT NOT NULL, locale TEXT NOT NULL, name TEXT, PRIMARY KEY(id, locale))",
		"INSERT INTO elementinfo_locale VALUES('0','en-US','Physical'),('2','en-US','Fusion'),('2','zh-TW','熱熔')",
		"ALTER TABLE damage ADD COLUMN element INTEGER",
		"UPDATE damage SET element=2",
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

	english, err := loadGameMasterDatabase(path, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	translated := resolveSkillLabelText(english, 80010001)
	if translated == nil || translated.Text != "Horizontal Swing" || translated.Locale != "en-US" || translated.Kind != "unofficial-translation" {
		t.Fatalf("the master locale text must be chosen: %+v", translated)
	}
	if original := resolveSkillLabelText(english, 80010002); original == nil || original.Text != "撞" || original.Locale != "zh-CN" || original.Kind != "developer-original" {
		t.Fatalf("a skill without a translation must fall back to the zh-CN original: %+v", original)
	}
	if ambiguous := resolveSkillLabelText(english, 80010003); ambiguous != nil {
		t.Fatalf("a skill with conflicting SkillInfo labels must get no label text: %+v", ambiguous)
	}
	if unknown := resolveSkillLabelText(english, 99); unknown != nil {
		t.Fatalf("an unknown skill must get no label text: %+v", unknown)
	}

	for damageID := range english.damage {
		element := resolveDamageElement(english, damageID, true)
		if element == nil || element.ID != 2 || element.Name != "Fusion" || element.Status != resolutionCorrelated {
			t.Fatalf("damage element must resolve to the locale name: %+v", element)
		}
		break
	}
	japanese, err := loadGameMasterDatabase(path, "ja-JP")
	if err != nil {
		t.Fatal(err)
	}
	for damageID := range japanese.damage {
		element := resolveDamageElement(japanese, damageID, true)
		if element == nil || element.ID != 2 || element.Name != "" || element.Status != resolutionUnknown {
			t.Fatalf("a missing element name must stay UNKNOWN with the raw id: %+v", element)
		}
		break
	}
	if resolveDamageElement(japanese, 123456789, true) != nil || resolveDamageElement(japanese, 0, false) != nil {
		t.Fatal("an unknown damage id or missing id must give no element")
	}
}

func TestOlderMasterWithoutReadableLabelTablesStillLoads(t *testing.T) {
	master, err := loadGameMasterDatabase(buildMasterDatabaseFixture(t), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if resolveSkillLabelText(master, 80010001) != nil {
		t.Fatal("no label table must give no label text")
	}
	for damageID := range master.damage {
		if resolveDamageElement(master, damageID, true) != nil {
			t.Fatal("a damage table without an element column must give no element")
		}
		break
	}
}
