package capture

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The content model of the combat Master: content systems (kinds of play); their entries (activities, seasons, cycles,
// series, stages, difficulties and modes, each in the namespace of its system and dimension) with the state of their
// official names and the official client texts of those names; and the static contexts an instance is played in, one row
// per context. The display resolver does not read it yet. These tests keep the shipped file consistent and prove that
// SQLite itself refuses a row that joins elements of different systems or dimensions, so the integrity of the model does
// not rest on the program that wrote it.

// masterContentDimensions are the dimension columns of content_context, in column order.
var masterContentDimensions = []string{"activity", "season", "cycle", "series", "stage", "difficulty", "mode"}

func masterContentCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestMasterContentModelIsConsistent(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for name, kind := range map[string]string{"content_system": "table", "content_entry": "table", "content_entry_locale": "table",
		"content_context": "table", "content_context_zh_tw": "view"} {
		if masterContentCount(t, db, "SELECT count(*) FROM sqlite_master WHERE name = ? AND type = ?", name, kind) != 1 {
			t.Fatalf("the Master has no %s %s", kind, name)
		}
	}
	for _, table := range []string{"content_system", "content_entry", "content_entry_locale", "content_context"} {
		if masterContentCount(t, db, "SELECT count(*) FROM "+table) == 0 {
			t.Errorf("%s is empty", table)
		}
		rows, err := db.Query("SELECT * FROM pragma_foreign_key_check(?)", table)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Errorf("%s has rows whose references are missing", table)
		}
		rows.Close()
	}
	checks := map[string]string{
		"a system named OFFICIAL without its guide category, or a guide category on another state": "SELECT count(*) FROM content_system WHERE (name_status = 'OFFICIAL') <> (guide_category_id IS NOT NULL)",
		"an OFFICIAL system name without an official text":                                         "SELECT count(*) FROM content_system s WHERE name_status = 'OFFICIAL' AND NOT EXISTS (SELECT 1 FROM secondaryguidedata_locale l WHERE l.id = s.guide_category_id AND l.text <> '' AND substr(l.text, 1, 4) <> 'dnt/')",
		"an OFFICIAL entry without a text":                                                         "SELECT count(*) FROM content_entry e WHERE name_status = 'OFFICIAL' AND NOT EXISTS (SELECT 1 FROM content_entry_locale l WHERE l.id = e.id)",
		"a text of an entry whose name is not OFFICIAL":                                            "SELECT count(*) FROM content_entry_locale l JOIN content_entry e ON e.id = l.id WHERE e.name_status <> 'OFFICIAL'",
		"an empty or placeholder text":                                                             "SELECT count(*) FROM content_entry_locale WHERE name = '' OR substr(name, 1, 4) = 'dnt/'",
		"an entry no context uses":                                                                 "SELECT count(*) FROM content_entry e WHERE NOT EXISTS (SELECT 1 FROM content_context c WHERE e.id IN (c.activity, c.season, c.cycle, c.series, c.stage, c.difficulty, c.mode))",
		"a system without a context":                                                               "SELECT count(*) FROM content_system s WHERE NOT EXISTS (SELECT 1 FROM content_context c WHERE c.system_id = s.id)",
		"ordinals of an instance's contexts in a system that are not 0 to n-1":                     "SELECT count(*) FROM (SELECT instance_id, system_id FROM content_context GROUP BY 1, 2 HAVING min(ordinal) <> 0 OR max(ordinal) <> count(*) - 1)",
		"a context with no dimension and no character":                                             "SELECT count(*) FROM content_context WHERE coalesce(activity, season, cycle, series, stage, difficulty, mode, role_id) IS NULL",
		"a view row count other than the contexts'":                                                "SELECT (SELECT count(*) FROM content_context_zh_tw) <> (SELECT count(*) FROM content_context)",
	}
	for _, d := range masterContentDimensions {
		checks["a "+d+" entry of another system or dimension"] = "SELECT count(*) FROM content_context c JOIN content_entry e ON e.id = c." + d +
			" WHERE e.system_id <> c.system_id OR e.dimension <> '" + d + "'"
	}
	for what, query := range checks {
		if n := masterContentCount(t, db, query); n != 0 {
			t.Errorf("%s: %d", what, n)
		}
	}
	// The instance query of a resolver is one primary-key search.
	plan, err := db.Query("EXPLAIN QUERY PLAN SELECT * FROM content_context WHERE instance_id = ?", "1")
	if err != nil {
		t.Fatal(err)
	}
	var detail []string
	for plan.Next() {
		var id, parent, unused int
		var text string
		if err := plan.Scan(&id, &parent, &unused, &text); err != nil {
			t.Fatal(err)
		}
		detail = append(detail, text)
	}
	plan.Close()
	if len(detail) != 1 || !strings.Contains(detail[0], "PRIMARY KEY (instance_id=?)") {
		t.Errorf("the instance query plan is %v, not one primary-key search", detail)
	}
}

// TestMasterContentModelRefusesWrongRows writes wrong rows into a copy of the embedded Master: SQLite refuses each one, and
// accepts the legal case of one key in two namespaces.
func TestMasterContentModelRefusesWrongRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	if err := os.WriteFile(path, []byte(embeddedMasterDatabase), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	pick := func(query string) string {
		t.Helper()
		var v string
		if err := db.QueryRow(query).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return strings.ReplaceAll(v, "'", "''")
	}
	sys := pick("SELECT system_id FROM content_context ORDER BY system_id LIMIT 1")
	other := pick("SELECT system_id FROM content_context WHERE system_id <> '" + sys + "' ORDER BY system_id LIMIT 1")
	inst := pick("SELECT instance_id FROM content_context WHERE system_id = '" + sys + "' ORDER BY instance_id LIMIT 1")
	ownStage := pick("SELECT id FROM content_entry WHERE system_id = '" + sys + "' ORDER BY dimension = 'stage' DESC, id LIMIT 1")
	ownDimension := pick("SELECT dimension FROM content_entry WHERE id = '" + ownStage + "'")
	otherDimension := "stage"
	if ownDimension == "stage" {
		otherDimension = "activity"
	}
	foreign := pick("SELECT id FROM content_entry WHERE system_id = '" + other + "' AND dimension = '" + ownDimension + "' ORDER BY id LIMIT 1")
	unnamed := pick("SELECT id FROM content_entry WHERE name_status <> 'OFFICIAL' ORDER BY id LIMIT 1")
	guide := pick("SELECT id FROM secondaryguidedata ORDER BY id LIMIT 1")
	ctx := func(column, value string) string {
		return "INSERT INTO content_context (instance_id, system_id, ordinal, " + column + ") VALUES ('" + inst + "', '" + sys + "', 999999, " + value + ")"
	}
	wrong := map[string]string{
		"an entry of another dimension":                ctx(otherDimension, "'"+ownStage+"'"),
		"an entry of another system":                   ctx(ownDimension, "'"+foreign+"'"),
		"an entry that does not exist":                 ctx(ownDimension, "'"+sys+":"+ownDimension+":no-such-key'"),
		"an instance that does not exist":              "INSERT INTO content_context (instance_id, system_id, ordinal) VALUES ('no-such-instance', '" + sys + "', 0)",
		"a character that does not exist":              ctx("role_id", "'no-such-role'"),
		"a context of a system that does not exist":    "INSERT INTO content_context (instance_id, system_id, ordinal) VALUES ('" + inst + "', 'no-such-system', 0)",
		"a context key twice":                          "INSERT INTO content_context (instance_id, system_id, ordinal) VALUES ('" + inst + "', '" + sys + "', 0)",
		"a negative ordinal":                           "INSERT INTO content_context (instance_id, system_id, ordinal) VALUES ('" + inst + "', '" + sys + "', -1)",
		"an entry id that is not its parts":            "INSERT INTO content_entry VALUES ('" + sys + ":stage:2', '" + sys + "', 'stage', '1', 'UNRESOLVED')",
		"an unknown dimension":                         "INSERT INTO content_entry VALUES ('" + sys + ":boss:1', '" + sys + "', 'boss', '1', 'UNRESOLVED')",
		"an entry of a system that does not exist":     "INSERT INTO content_entry VALUES ('no-such-system:stage:1', 'no-such-system', 'stage', '1', 'UNRESOLVED')",
		"an unknown name state":                        "INSERT INTO content_entry VALUES ('" + sys + ":stage:no-such-key', '" + sys + "', 'stage', 'no-such-key', 'MAYBE')",
		"a placeholder text":                           "INSERT INTO content_entry_locale VALUES ('" + unnamed + "', 'zh-CN', 'dnt/x', 'K')",
		"an empty text":                                "INSERT INTO content_entry_locale VALUES ('" + unnamed + "', 'zh-CN', '', 'K')",
		"an unknown locale":                            "INSERT INTO content_entry_locale VALUES ('" + unnamed + "', 'de-DE', 'x', 'K')",
		"a text of an entry that does not exist":       "INSERT INTO content_entry_locale VALUES ('" + sys + ":stage:no-such-key', 'zh-TW', 'x', 'K')",
		"a guide category that does not exist":         "INSERT INTO content_system VALUES ('probe', 'no-such-category', 'OFFICIAL', 'SHOW')",
		"an official system name without a guide":      "INSERT INTO content_system VALUES ('probe', NULL, 'OFFICIAL', 'SHOW')",
		"a guide on a system without an official name": "INSERT INTO content_system VALUES ('probe', '" + guide + "', 'NO_OFFICIAL_NAME', 'SHOW')",
		"an unknown scene name policy":                 "INSERT INTO content_system VALUES ('probe', NULL, 'NO_OFFICIAL_NAME', 'HIDE')",
		"a system id that splits an entry id":          "INSERT INTO content_system VALUES ('pro:be', NULL, 'UNRESOLVED', 'SHOW')",
	}
	for what, statement := range wrong {
		_, err := db.Exec(statement)
		switch {
		case err == nil:
			t.Errorf("%s was accepted: %s", what, statement)
		case !strings.Contains(err.Error(), "constraint failed"):
			t.Errorf("%s was refused for another reason than a constraint: %v", what, err)
		}
	}
	for what, statement := range map[string]string{
		"one key in two namespaces":   "INSERT INTO content_entry VALUES ('" + sys + ":season:" + strings.TrimPrefix(ownStage, sys+":"+ownDimension+":") + "', '" + sys + "', 'season', '" + strings.TrimPrefix(ownStage, sys+":"+ownDimension+":") + "', 'UNRESOLVED')",
		"a system that needs no code": "INSERT INTO content_system VALUES ('probe', NULL, 'NO_OFFICIAL_NAME', 'SHOW')",
	} {
		if ownDimension == "season" && what == "one key in two namespaces" {
			statement = strings.ReplaceAll(statement, "season", "mode")
		}
		if _, err := db.Exec(statement); err != nil {
			t.Errorf("%s was refused: %v", what, err)
		}
	}
}
