package capture

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"testing"
)

func TestEmbeddedMasterReadsThroughTheExecutableNotADisk(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sequence int
	var name, file string
	if err := db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &file); err != nil {
		t.Fatal(err)
	}
	// The VFS names the file inside its own file system; a real path here would mean a copy on disk.
	if file != embeddedMasterDatabaseName {
		t.Fatalf("main database file = %q", file)
	}
	for pragma, want := range map[string]int{"query_only": 1, "temp_store": 2} {
		var got int
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("PRAGMA %s = %d, %v; want %d", pragma, got, err, want)
		}
	}
	// A sort too large for the page cache must stay in memory, because this file system cannot create files.
	var count int
	if err := db.QueryRow("SELECT count(*) FROM (SELECT map_id FROM map_entity_template_ref ORDER BY template_id DESC, entity_id)").Scan(&count); err != nil || count == 0 {
		t.Fatalf("large sort: %d, %v", count, err)
	}
	var result string
	if err := db.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("quick_check = %q, %v", result, err)
	}
}

// Unlike an in-memory database, a new pooled connection must find the same data, with the same read-only pragmas.
func TestEmbeddedMasterSurvivesAReopenedConnection(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before := masterTableCount(t, db)
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(1)
	if after := masterTableCount(t, db); after != before || after == 0 {
		t.Fatalf("tables before %d, after a new connection %d", before, after)
	}
	if _, err := db.Exec("CREATE TABLE should_not_exist(id INTEGER)"); err == nil {
		t.Fatal("a reopened connection accepted a write")
	}
}

func masterTableCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestMasterLookupDigest prints one digest over the schema and the lookup families. verify.ps1 runs it in both profiles and
// requires equal digests, so the profiles cannot drift apart in schema, queries or lookup results.
func TestMasterLookupDigest(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	digest := sha256.New()
	rows, err := db.Query("SELECT type,name,COALESCE(sql,'') FROM sqlite_master ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind, name, schema string
		if err := rows.Scan(&kind, &name, &schema); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(digest, "%s|%s|%s\n", kind, name, schema)
	}
	rows.Close()
	for _, locale := range []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"} {
		master, err := loadMasterDataFromDatabase(db, "embedded", "digest", locale)
		if err != nil {
			t.Fatal(err)
		}
		writeSortedLookups(digest, "character", master.characters)
		writeSortedLookups(digest, "monster", master.monsters)
		writeSortedLookups(digest, "skill", master.skills)
		writeSortedLookups(digest, "commonSkill", master.commonSkills)
		writeSortedLookups(digest, "buff", master.buffDefinitions)
		writeSortedLookups(digest, "shield", master.shieldDefinitions)
		writeSortedLookups(digest, "bullet", master.commonBullets)
		writeSortedLookups(digest, "damage", master.damage)
		writeSortedLookups(digest, "template", master.sceneIdentity.templateCatalog)
		fmt.Fprintf(digest, "relations=%d monsters=%d\n", len(master.sceneIdentity.levelTemplates), len(master.sceneIdentity.levelMonsters))
	}
	t.Logf("master lookup digest: %s", hex.EncodeToString(digest.Sum(nil)))
}

func writeSortedLookups[V any](out io.Writer, family string, values map[uint64]V) {
	keys := make([]uint64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		fmt.Fprintf(out, "%s|%d|%+v\n", family, key, stripPointers(values[key]))
	}
}

// stripPointers renders a pointer-carrying template candidate by value, so a digest never depends on an address.
func stripPointers(value any) any {
	if candidate, ok := value.(sceneTemplateCandidate); ok && candidate.DevLabel != nil {
		label := *candidate.DevLabel
		candidate.DevLabel = nil
		return fmt.Sprintf("%+v|%+v", candidate, label)
	}
	return value
}

func TestEmbeddedMasterHashCoversTheWholeFile(t *testing.T) {
	got, err := embeddedMasterDatabaseHash()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(embeddedMasterDatabase))
	if want := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("hash %s, want %s", got, want)
	}
}

func TestSceneTemplateRelationsShareOnlySingleTemplateKeys(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"CREATE TABLE map_entity_template_ref(map_id TEXT, entity_id TEXT, template_id TEXT, ordinal INTEGER)",
		"INSERT INTO map_entity_template_ref VALUES ('1','10','7',0),('1','11','7',0),('1','12','7',0),('1','12','8',1),('1','13','8',0)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	master := &gameMaster{sceneIdentity: &sceneIdentityMaster{
		levelTemplates: make(map[sceneLevelKey][]sceneTemplateCandidate),
		templateCatalog: map[uint64]sceneTemplateCandidate{
			7: {TemplateID: 7, RawName: "seven"},
			8: {TemplateID: 8, RawName: "eight"},
		},
	}}
	if err := loadTemplateRelations(db, master); err != nil {
		t.Fatal(err)
	}
	ids := func(entity uint64) []uint64 {
		var out []uint64
		for _, candidate := range master.sceneIdentity.levelTemplates[sceneLevelKey{mapID: 1, entityID: entity}] {
			out = append(out, candidate.TemplateID)
		}
		return out
	}
	for entity, want := range map[uint64][]uint64{10: {7}, 11: {7}, 12: {7, 8}, 13: {8}} {
		got := ids(entity)
		if len(got) != len(want) {
			t.Fatalf("entity %d: templates %v, want %v", entity, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("entity %d: templates %v, want %v", entity, got, want)
			}
		}
	}
	first := master.sceneIdentity.levelTemplates[sceneLevelKey{mapID: 1, entityID: 10}]
	second := master.sceneIdentity.levelTemplates[sceneLevelKey{mapID: 1, entityID: 11}]
	if &first[0] != &second[0] {
		t.Fatal("entities of one template do not share a candidate")
	}
	// Adding a second template to a key must leave every sharer of the first one untouched.
	if len(first) != 1 || cap(first) != 1 {
		t.Fatalf("a shared slice has room to grow: len %d cap %d", len(first), cap(first))
	}
}

// A real relation row count survives sharing: every relation still resolves to its own template.
func TestEmbeddedMasterKeepsEverySceneTemplateRelation(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	master, err := loadMasterDataFromDatabase(db, "embedded", "test", "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	var relations int
	if err := db.QueryRow("SELECT count(*) FROM map_entity_template_ref").Scan(&relations); err != nil {
		t.Fatal(err)
	}
	total := 0
	for key, candidates := range master.sceneIdentity.levelTemplates {
		total += len(candidates)
		for _, candidate := range candidates {
			var found int
			if key.entityID%4096 != 0 {
				continue
			}
			if err := db.QueryRow("SELECT count(*) FROM map_entity_template_ref WHERE map_id=? AND entity_id=? AND template_id=?", key.mapID, key.entityID, candidate.TemplateID).Scan(&found); err != nil || found == 0 {
				t.Fatalf("relation %+v -> %d is not in the database: %d, %v", key, candidate.TemplateID, found, err)
			}
		}
	}
	if total != relations || relations == 0 {
		t.Fatalf("loaded %d relations, database has %d", total, relations)
	}
}
