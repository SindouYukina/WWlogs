package capture

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests prove that the equivalence harness and the contract checker catch each regression class: on a small
// synthetic Master in every run, and on mutated copies of the tracked Master in the collector
// TestMasterHarnessDetectsInjectedRegressionsInTheTrackedMaster.

const masterFixtureSummonView = `CREATE VIEW summon_skill_vision_ref AS
	WITH unique_phantom_skill AS (
		SELECT CAST(phantom_skill_id AS TEXT) AS phantom_skill_id FROM phantomskill
		WHERE phantom_skill_id IS NOT NULL AND CAST(phantom_skill_id AS TEXT) <> ''
		GROUP BY CAST(phantom_skill_id AS TEXT) HAVING COUNT(*) = 1
	)
	SELECT 1 AS summon_type, p.phantom_skill_id AS summon_skill_id, CAST(v.id AS TEXT) AS vision_id, p.phantom_skill_id AS phantom_skill_id
	FROM unique_phantom_skill AS p JOIN vision AS v ON CAST(v.id AS TEXT) = p.phantom_skill_id`

func execMasterSQL(t *testing.T, path string, statements []string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyMasterFile(t *testing.T, source, target string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// vacuumMasterInto writes a physically new copy of a Master through a read-only connection to the source.
func vacuumMasterInto(t *testing.T, source, target string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(source)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO '" + strings.ReplaceAll(target, "'", "''") + "'"); err != nil {
		t.Fatalf("vacuum into %s: %v", target, err)
	}
}

func buildEquivalenceFixture(t *testing.T) string {
	t.Helper()
	path := buildMasterDatabaseFixture(t)
	execMasterSQL(t, path, []string{
		"INSERT INTO roleinfo VALUES('1409','78','0')",
		"INSERT INTO roleinfo_locale VALUES('1409','zh-TW','另一角色','Role.Name'),('1409','ko-KR','다른 캐릭터','Role.Name')",
		"CREATE TABLE trialroleinfo(id TEXT PRIMARY KEY, parent_role_id TEXT)",
		"INSERT INTO trialroleinfo VALUES('13127004','1312')",
		"CREATE TABLE skill_locale(id TEXT, locale TEXT, name TEXT, PRIMARY KEY(id, locale))",
		"INSERT INTO skill_locale VALUES('1312','zh-TW','測試UI技能')",
		"INSERT INTO skilldescription VALUES('7001',NULL)",
		"CREATE TABLE skill_damage_ref(skill_id TEXT NOT NULL, ordinal INTEGER NOT NULL, damage_id TEXT, PRIMARY KEY(skill_id, ordinal))",
		"INSERT INTO skill_damage_ref VALUES('1312',0,'18446744073709551614')",
		"CREATE TABLE buff(id TEXT PRIMARY KEY, duration_policy INTEGER, stack_limit_count INTEGER)",
		"INSERT INTO buff VALUES('5001',1,3)",
		"CREATE TABLE buff_granted_tags(owner_id TEXT NOT NULL, ordinal INTEGER NOT NULL, value TEXT, PRIMARY KEY(owner_id, ordinal)) WITHOUT ROWID",
		"INSERT INTO buff_granted_tags VALUES('5001',0,'Tag.A'),('5001',1,'Tag.B')",
		"CREATE TABLE phantomskill(id TEXT PRIMARY KEY, phantom_skill_id TEXT)",
		"INSERT INTO phantomskill VALUES('280026','280026')",
		"CREATE TABLE vision(id TEXT PRIMARY KEY)",
		"INSERT INTO vision VALUES('280026')",
		masterFixtureSummonView,
		"CREATE TABLE instancedungeon_locale(id TEXT, locale TEXT, name TEXT, PRIMARY KEY(id, locale))",
		"INSERT INTO instancedungeon_locale VALUES('50','zh-TW','測試關卡')",
		"CREATE INDEX idx_map_entity_template_target ON map_entity_template_ref(template_id)",
	})
	return path
}

// masterIntegerColumnStatements rebuilds a table of the Master at path with one TEXT column declared INTEGER and its values
// cast, keeping the table name, every other column, constraint and index, as a TEXT-to-INTEGER migration would.
func masterIntegerColumnStatements(t *testing.T, path, table, column string) []string {
	t.Helper()
	db, _, _, err := openFileMasterDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var createSQL string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&createSQL); err != nil {
		t.Fatalf("table %s: %v", table, err)
	}
	pattern := regexp.MustCompile(`(?i)([(,]\s*)("?` + regexp.QuoteMeta(column) + `"?)(\s+)TEXT\b`)
	location := pattern.FindStringSubmatchIndex(createSQL)
	if location == nil {
		t.Fatalf("%s.%s is not declared TEXT in %s", table, column, createSQL)
	}
	rebuilt := createSQL[:location[0]] + createSQL[location[2]:location[3]] + createSQL[location[4]:location[5]] + createSQL[location[6]:location[7]] + "INTEGER" + createSQL[location[1]:]
	indexes, err := masterSamples(db, "SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name='"+table+"' AND sql IS NOT NULL ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	columns, err := masterPragmaColumnNames(db, table)
	if err != nil {
		t.Fatal(err)
	}
	selects := make([]string, len(columns))
	for i, c := range columns {
		selects[i] = masterQuote(c)
		if c == column {
			selects[i] = "CAST(" + masterQuote(c) + " AS INTEGER)"
		}
	}
	statements := []string{
		"CREATE TABLE masterharness_copy AS SELECT * FROM " + masterQuote(table),
		"DROP TABLE " + masterQuote(table),
		rebuilt,
		"INSERT INTO " + masterQuote(table) + " SELECT " + strings.Join(selects, ", ") + " FROM masterharness_copy",
		"DROP TABLE masterharness_copy",
	}
	return append(statements, indexes...)
}

// masterHasClass reports whether a layer/class was reported; "layer/*" accepts any class of the layer.
func masterHasClass(r masterEquivalenceReport, class string) bool {
	if layer, ok := strings.CutSuffix(class, "/*"); ok {
		return masterLayerCount(r, layer) > 0
	}
	return r.Counts[class] > 0
}

func masterLayerCount(r masterEquivalenceReport, layer string) int {
	n := 0
	for k, v := range r.Counts {
		if strings.HasPrefix(k, layer+"/") {
			n += v
		}
	}
	return n
}

func TestMasterEquivalenceHarnessDetectsEveryRegressionClass(t *testing.T) {
	base := buildEquivalenceFixture(t)
	dir := t.TempDir()
	typeChange := masterIntegerColumnStatements(t, base, "roleinfo_locale", "id")
	typeChangeExpected := []masterExpectation{
		{Layer: "schema", Class: "column-type-changed", Object: "roleinfo_locale.id"},
		{Layer: "data", Class: "storage-class-changed", Object: "roleinfo_locale.id"},
	}
	cases := []struct {
		name         string
		mutate       []string
		vacuum       bool
		locales      []string
		layers       []string
		expectations []masterExpectation
		want         []string // classes that must be reported
		absent       []string // classes that must not be reported
		none         []string // layers that must report nothing
		equivalent   bool
	}{
		{name: "identity", equivalent: true, none: []string{"schema", "data", "loaded", "display"}},
		{name: "physical reorganization", vacuum: true, equivalent: true, none: []string{"schema", "data", "loaded", "display"}},
		{name: "missing optional table", mutate: []string{"DROP TABLE trialroleinfo"},
			want: []string{"schema/table-missing", "loaded/entry-missing", "display/name-changed", "display/static-role-changed"}},
		{name: "missing optional column", mutate: []string{"ALTER TABLE roleinfo DROP COLUMN skin_id"},
			want: []string{"schema/column-missing", "loaded/entry-changed", "loaded/entry-missing"}},
		{name: "wrong mapping", mutate: []string{"UPDATE trialroleinfo SET parent_role_id='1409'"},
			want: []string{"data/rows-changed", "loaded/entry-changed", "display/name-changed", "display/static-role-changed"}},
		{name: "identifier type change undeclared", mutate: typeChange,
			want: []string{"schema/column-type-changed", "data/storage-class-changed"}, none: []string{"loaded", "display"}},
		{name: "identifier type change declared", mutate: typeChange, expectations: typeChangeExpected, equivalent: true, none: []string{"loaded", "display"}},
		{name: "non-canonical identifier", mutate: []string{"UPDATE roleinfo_locale SET id='01312' WHERE id='1312' AND locale='zh-TW'"},
			want: []string{"data/rows-missing", "data/rows-added", "loaded/entry-changed", "display/name-changed"}},
		{name: "data loss", mutate: []string{"DELETE FROM map_entity_template_ref"}, want: []string{"data/rows-missing", "loaded/entry-missing"},
			absent: []string{"data/storage-class-changed"}},
		{name: "partial data loss", mutate: []string{"DELETE FROM roleinfo_locale WHERE locale='ko-KR'"},
			want: []string{"data/rows-missing", "loaded/*", "display/name-changed"}, absent: []string{"data/storage-class-changed", "data/rows-changed"}},
		{name: "namespace mixing", mutate: []string{"UPDATE skill_damage_ref SET skill_id='7001'"},
			want: []string{"data/rows-missing", "data/rows-added", "loaded/entry-changed"}},
		{name: "view missing", mutate: []string{"DROP VIEW summon_skill_vision_ref"}, want: []string{"schema/view-missing", "loaded/entry-missing"}},
		{name: "unknown id becomes known", mutate: []string{"INSERT INTO monsterinfo VALUES('702')", "INSERT INTO monsterinfo_locale VALUES('702','zh-TW','新怪物','Monster.Name')"},
			want: []string{"data/rows-added", "loaded/entry-added", "display/name-changed"}},
		{name: "order change", mutate: []string{"UPDATE buff_granted_tags SET value = CASE ordinal WHEN 0 THEN 'Tag.B' ELSE 'Tag.A' END"},
			want: []string{"data/rows-changed", "loaded/entry-changed"}},
		{name: "index removed undeclared", mutate: []string{"DROP INDEX idx_map_entity_template_target"}, want: []string{"schema/index-missing"}, none: []string{"loaded", "display"}},
		{name: "index removed declared", mutate: []string{"DROP INDEX idx_map_entity_template_target"}, equivalent: true,
			expectations: []masterExpectation{{Layer: "schema", Class: "index-missing", Object: "map_entity_template_ref:idx_map_entity_template_target"}}},
		{name: "required table missing", mutate: []string{"DROP TABLE map_instance"}, want: []string{"schema/table-missing", "loaded/load-error"}},
		{name: "display-only table", mutate: []string{"UPDATE instancedungeon_locale SET name='改名'"}, want: []string{"display/name-changed"}, none: []string{"loaded"}},
		{name: "locale-only change seen in its locale", mutate: []string{"UPDATE roleinfo_locale SET name='改名' WHERE id='1312' AND locale='ko-KR'"},
			locales: []string{"ko-KR"}, layers: []string{"loaded", "display"}, want: []string{"loaded/entry-changed", "display/name-changed"}},
		{name: "locale-only change absent from another locale", mutate: []string{"UPDATE roleinfo_locale SET name='改名' WHERE id='1312' AND locale='ko-KR'"},
			locales: []string{"zh-TW"}, layers: []string{"loaded"}, equivalent: true, none: []string{"loaded"}},
		{name: "declared difference that does not happen", equivalent: false,
			expectations: []masterExpectation{{Layer: "schema", Class: "table-missing", Object: "trialroleinfo"}}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			candidate := filepath.Join(dir, fmt.Sprintf("candidate-%02d.db", i))
			if c.vacuum {
				vacuumMasterInto(t, base, candidate)
			} else {
				copyMasterFile(t, base, candidate)
			}
			if len(c.mutate) > 0 {
				execMasterSQL(t, candidate, c.mutate)
			}
			options := defaultMasterEquivalenceOptions()
			options.Locales = []string{"zh-TW", "ko-KR"}
			if c.locales != nil {
				options.Locales = c.locales
			}
			if c.layers != nil {
				options.Layers = c.layers
			}
			options.Expectations = c.expectations
			report, err := runMasterEquivalence(base, candidate, options)
			if err != nil {
				t.Fatal(err)
			}
			for _, class := range c.want {
				if !masterHasClass(report, class) {
					t.Errorf("did not report %s; reported %s", class, masterEquivalenceSummary(report))
				}
			}
			for _, class := range c.absent {
				if masterHasClass(report, class) {
					t.Errorf("reported %s, which this change does not make; reported %s", class, masterEquivalenceSummary(report))
				}
			}
			for _, layer := range c.none {
				if n := masterLayerCount(report, layer); n > 0 {
					t.Errorf("reported %d %s differences: %s", n, layer, masterEquivalenceSummary(report))
				}
			}
			if report.Equivalent != c.equivalent {
				t.Errorf("equivalent = %t, want %t (unexpected %v, absent %v)", report.Equivalent, c.equivalent, report.Unexpected, report.ExpectedNotSeen)
			}
		})
	}
}

// correctionDeclaration turns the differences a report found into a correction-mode declaration of one correction.
func correctionDeclaration(differences []masterDifference) ([]masterCorrection, []masterExpectation) {
	correction := masterCorrection{ID: "role-1312-ko-name", Table: "roleinfo_locale", Column: "name", RowKey: "id=1312, locale=ko-KR",
		Old: "(fixture name)", New: "改名", GameVersion: "fixture", Source: "fixture client row", Semantics: "the ko-KR display name of role 1312",
		ExpectedOutput: "role 1312 is named 改名 in ko-KR"}
	var expectations []masterExpectation
	for _, x := range differences {
		old, nw := x.Old, x.New
		expectations = append(expectations, masterExpectation{Layer: x.Layer, Class: x.Class, Object: x.Object, Locale: x.Locale, Key: x.Key,
			Old: &old, New: &nw, Count: x.Count, Correction: correction.ID})
	}
	return []masterCorrection{correction}, expectations
}

// A verified data correction passes only with its exact differences declared, every one tied to an evidenced correction:
// an extra change, a different value, a declared difference that does not happen, an incomplete declaration or a
// refactor declaration of loaded or display differences all fail.
func TestMasterCorrectionModeAcceptsOnlyTheDeclaredValues(t *testing.T) {
	base := buildEquivalenceFixture(t)
	dir := t.TempDir()
	rename := "UPDATE roleinfo_locale SET name='改名' WHERE id='1312' AND locale='ko-KR'"
	corrected := filepath.Join(dir, "corrected.db")
	copyMasterFile(t, base, corrected)
	execMasterSQL(t, corrected, []string{rename})
	options := defaultMasterEquivalenceOptions()
	options.Locales = []string{"zh-TW", "ko-KR"}
	options.Mode = masterModeCorrection
	// Collect what the correction changes (the review step), then declare exactly that.
	probe := options
	probe.Corrections, probe.Expectations = []masterCorrection{{ID: "x", Table: "t", Column: "c", RowKey: "k", New: "n", GameVersion: "g", Source: "s", Semantics: "m", ExpectedOutput: "o"}},
		[]masterExpectation{{Layer: "data", Class: "none", Object: "none", Key: "none", Old: new(string), New: new(string), Correction: "x"}}
	found, err := runMasterEquivalence(base, corrected, probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range []string{"data", "loaded", "display"} {
		if masterLayerCount(found, layer) == 0 {
			t.Fatalf("the fixture correction changed nothing in the %s layer: %s", layer, masterEquivalenceSummary(found))
		}
	}
	options.Corrections, options.Expectations = correctionDeclaration(found.Differences)
	report, err := runMasterEquivalence(base, corrected, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Equivalent {
		t.Fatalf("the declared correction is not accepted: unexpected %v, absent %v", report.Unexpected, report.ExpectedNotSeen)
	}

	t.Run("an undeclared change fails", func(t *testing.T) {
		extra := filepath.Join(t.TempDir(), "extra.db")
		copyMasterFile(t, corrected, extra)
		execMasterSQL(t, extra, []string{"UPDATE roleinfo_locale SET name='另一個' WHERE id='1312' AND locale='zh-TW'"})
		r, err := runMasterEquivalence(base, extra, options)
		if err != nil {
			t.Fatal(err)
		}
		if r.Equivalent || len(r.Unexpected) == 0 {
			t.Fatalf("an undeclared zh-TW change passed: %s", masterEquivalenceSummary(r))
		}
	})
	t.Run("another value fails", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other.db")
		copyMasterFile(t, base, other)
		execMasterSQL(t, other, []string{"UPDATE roleinfo_locale SET name='別名' WHERE id='1312' AND locale='ko-KR'"})
		r, err := runMasterEquivalence(base, other, options)
		if err != nil {
			t.Fatal(err)
		}
		if r.Equivalent || len(r.Unexpected) == 0 || len(r.ExpectedNotSeen) == 0 {
			t.Fatalf("a different value passed as the declared one: unexpected %v, absent %v", r.Unexpected, r.ExpectedNotSeen)
		}
	})
	t.Run("a declared difference that does not happen fails", func(t *testing.T) {
		r, err := runMasterEquivalence(base, base, options)
		if err != nil {
			t.Fatal(err)
		}
		if r.Equivalent || len(r.ExpectedNotSeen) == 0 {
			t.Fatalf("an unchanged Master passed a correction declaration")
		}
	})
	t.Run("incomplete declarations are refused", func(t *testing.T) {
		corrections, expectations := correctionDeclaration(found.Differences)
		broken := map[string]func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation){
			"no evidence": func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation) {
				c[0].Source = ""
				return c, e
			},
			"no key": func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation) {
				e[0].Key = ""
				return c, e
			},
			"no new value": func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation) {
				e[0].New = nil
				return c, e
			},
			"a schema difference": func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation) {
				e[0].Layer = "schema"
				return c, e
			},
			"an unknown correction": func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation) {
				e[0].Correction = "missing"
				return c, e
			},
			"an unused correction": func(c []masterCorrection, e []masterExpectation) ([]masterCorrection, []masterExpectation) {
				extra := c[0]
				extra.ID = "unused"
				return append(c, extra), e
			},
		}
		for name, change := range broken {
			c := append([]masterCorrection(nil), corrections...)
			e := append([]masterExpectation(nil), expectations...)
			c, e = change(c, e)
			o := options
			o.Corrections, o.Expectations = c, e
			if _, err := runMasterEquivalence(base, corrected, o); err == nil {
				t.Errorf("%s: the declaration was accepted", name)
			}
		}
		// A refactor cannot declare what a correction may.
		o := options
		o.Mode, o.Corrections, o.Expectations = masterModeRefactor, nil, expectations
		if _, err := runMasterEquivalence(base, corrected, o); err == nil {
			t.Error("refactor mode accepted loaded and display expectations")
		}
	})
}

func TestMasterEquivalenceRefusesToDeclareSemanticDifferences(t *testing.T) {
	for _, layer := range []string{"loaded", "display"} {
		if err := validateMasterExpectations([]masterExpectation{{Layer: layer, Class: "entry-changed", Object: "GameMaster.Characters"}}); err == nil {
			t.Errorf("a %s difference was accepted as an expectation", layer)
		}
	}
}

// masterContractForFixture describes a fixture Master exactly, as the Master contract describes the tracked Master.
func masterContractForFixture(t *testing.T, path string) masterContract {
	t.Helper()
	db, _, _, err := openFileMasterDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	info, err := readMasterSchemaInfo(db)
	if err != nil {
		t.Fatal(err)
	}
	c := masterContract{Version: 1, Locales: masterEquivalenceLocales}
	for _, name := range sortedMasterKeys(info.Tables) {
		table := info.Tables[name]
		ct := masterContractTable{Name: name, Scope: "product", Purpose: "fixture", WithoutRowid: table.WithoutRowid, NonEmpty: true, ForeignKeys: table.ForeignKeys}
		for _, col := range table.Columns {
			cc := masterContractColumn{Name: col.Name, Type: col.Type, NotNull: col.NotNull, PK: col.PK, Purpose: "fixture"}
			if col.Default.Valid {
				value := col.Default.String
				cc.Default = &value
			}
			ct.Columns = append(ct.Columns, cc)
		}
		for _, ix := range table.Indexes {
			ct.Indexes = append(ct.Indexes, masterContractIndex{Name: ix.Name, Unique: ix.Unique, Origin: ix.Origin, Columns: ix.Columns})
		}
		c.Tables = append(c.Tables, ct)
	}
	for _, name := range sortedMasterKeys(info.Views) {
		c.Views = append(c.Views, masterContractView{Name: name, Scope: "product", Purpose: "fixture", Columns: info.Views[name].Columns})
	}
	set := func(table, column string, apply func(*masterContractColumn)) {
		for i := range c.Tables {
			if c.Tables[i].Name != table {
				continue
			}
			for j := range c.Tables[i].Columns {
				if c.Tables[i].Columns[j].Name == column {
					apply(&c.Tables[i].Columns[j])
					return
				}
			}
		}
		t.Fatalf("fixture has no column %s.%s", table, column)
	}
	set("roleinfo_locale", "id", func(col *masterContractColumn) {
		col.Namespace, col.Values, col.References, col.ReferenceEnforced = "RoleInfo.Id", "id", "roleinfo.id", true
	})
	set("roleinfo_locale", "locale", func(col *masterContractColumn) { col.Values = "locale" })
	set("trialroleinfo", "parent_role_id", func(col *masterContractColumn) {
		col.Namespace, col.Values, col.References, col.ReferenceEnforced = "RoleInfo.Id", "id", "roleinfo.id", true
	})
	set("skill_damage_ref", "skill_id", func(col *masterContractColumn) {
		col.Namespace, col.Values, col.References, col.ReferenceEnforced = "Skill.Id", "id", "skill.id", true
	})
	return c
}

func masterContractClasses(t *testing.T, path string, c masterContract) map[string]int {
	t.Helper()
	db, _, _, err := openFileMasterDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	violations, err := checkMasterContract(db, c)
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]int{}
	for _, v := range violations {
		classes[v.Class]++
	}
	return classes
}

func TestMasterContractCheckerFindsEveryViolationClass(t *testing.T) {
	base := buildEquivalenceFixture(t)
	contract := masterContractForFixture(t, base)
	if classes := masterContractClasses(t, base, contract); len(classes) != 0 {
		t.Fatalf("the fixture breaks its own contract: %v", classes)
	}
	dir := t.TempDir()
	cases := []struct {
		name   string
		mutate []string
		edit   func(*masterContract)
		want   string
	}{
		{name: "missing table", mutate: []string{"DROP TABLE trialroleinfo"}, want: "table-missing"},
		{name: "missing column", mutate: []string{"ALTER TABLE roleinfo DROP COLUMN skin_id"}, want: "column-missing"},
		{name: "unlisted column", mutate: []string{"ALTER TABLE skill ADD COLUMN extra TEXT"}, want: "column-unlisted"},
		{name: "type change", mutate: masterIntegerColumnStatements(t, base, "roleinfo_locale", "id"), want: "column-type-changed"},
		{name: "non-canonical identifier", mutate: []string{"UPDATE roleinfo_locale SET id='01312' WHERE id='1312' AND locale='zh-TW'"}, want: "value-rule-violated"},
		{name: "identifier beyond the safe integer range", edit: func(c *masterContract) {
			for i := range c.Tables {
				if c.Tables[i].Name == "damage" {
					c.Tables[i].Columns[0].Values = "id"
				}
			}
		}, want: "value-rule-violated"},
		{name: "unknown locale", mutate: []string{"UPDATE roleinfo_locale SET locale='xx-XX' WHERE id='1409' AND locale='ko-KR'"}, want: "value-rule-violated"},
		{name: "namespace mixing", mutate: []string{"UPDATE skill_damage_ref SET skill_id='7001'"}, want: "reference-missing"},
		{name: "broken mapping", mutate: []string{"UPDATE trialroleinfo SET parent_role_id='9999'"}, want: "reference-missing"},
		{name: "unlisted table", mutate: []string{"CREATE TABLE stray(x TEXT)"}, want: "table-unlisted"},
		{name: "missing view", mutate: []string{"DROP VIEW summon_skill_vision_ref"}, want: "view-missing"},
		{name: "missing index", mutate: []string{"DROP INDEX idx_map_entity_template_target"}, want: "index-missing"},
		{name: "empty table", mutate: []string{"DELETE FROM buff_granted_tags"}, want: "table-empty"},
		{name: "stale uncontracted list", edit: func(c *masterContract) {
			c.Uncontracted = append(c.Uncontracted, masterContractUncontracted{Name: "gone", Classification: "RESEARCH_ONLY"})
		}, want: "uncontracted-missing"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			candidate := filepath.Join(dir, fmt.Sprintf("contract-%02d.db", i))
			copyMasterFile(t, base, candidate)
			if len(c.mutate) > 0 {
				execMasterSQL(t, candidate, c.mutate)
			}
			edited := contract
			edited.Tables = append([]masterContractTable(nil), contract.Tables...)
			for j := range edited.Tables {
				edited.Tables[j].Columns = append([]masterContractColumn(nil), contract.Tables[j].Columns...)
			}
			edited.Uncontracted = append([]masterContractUncontracted(nil), contract.Uncontracted...)
			if c.edit != nil {
				c.edit(&edited)
			}
			classes := masterContractClasses(t, candidate, edited)
			if classes[c.want] == 0 {
				t.Errorf("did not report %s; reported %v", c.want, classes)
			}
		})
	}
}

// masterInjection is one deliberate regression applied to a copy of the tracked Master.
type masterInjection struct {
	Name          string              `json:"name"`
	Class         string              `json:"class"`
	IntegerColumn string              `json:"integerColumn,omitempty"`
	SQL           []string            `json:"sql"`
	Locales       []string            `json:"locales"`
	Layers        []string            `json:"layers,omitempty"`
	Expectations  []masterExpectation `json:"expectations,omitempty"`
	Want          []string            `json:"want"`
	WantContract  []string            `json:"wantContract,omitempty"`
	None          []string            `json:"none,omitempty"`
	Equivalent    bool                `json:"equivalent"`
}

type masterInjectionResult struct {
	masterInjection
	Counts             map[string]int `json:"counts"`
	Unexpected         []string       `json:"unexpected,omitempty"`
	ContractViolations map[string]int `json:"contractViolations,omitempty"`
	Missed             []string       `json:"missed,omitempty"`
	Detected           bool           `json:"detected"`
}

func trackedMasterInjections() []masterInjection {
	typeExpect := []masterExpectation{
		{Layer: "schema", Class: "column-type-changed", Object: "roleinfo_locale.id", Reason: "injected TEXT to INTEGER migration"},
		{Layer: "data", Class: "storage-class-changed", Object: "roleinfo_locale.id", Reason: "injected TEXT to INTEGER migration"},
	}
	zh := []string{"zh-TW"}
	return []masterInjection{
		{Name: "missing optional table", Class: "missing table", SQL: []string{"DROP TABLE buff_granted_tags"}, Locales: zh,
			Want: []string{"schema/table-missing", "loaded/entry-changed"}, WantContract: []string{"table-missing"}},
		{Name: "missing required table", Class: "missing table", SQL: []string{"DROP TABLE map_instance"}, Locales: zh,
			Want: []string{"schema/table-missing", "loaded/load-error"}, WantContract: []string{"table-missing"}},
		{Name: "missing optional column", Class: "missing column", SQL: []string{"ALTER TABLE vision DROP COLUMN dev_note_zh_cn"}, Locales: zh,
			Want: []string{"schema/column-missing", "loaded/entry-missing"}, WantContract: []string{"column-missing"}},
		{Name: "missing view", Class: "missing column", SQL: []string{"DROP VIEW summon_skill_vision_ref"}, Locales: zh,
			Want: []string{"schema/view-missing", "loaded/entry-missing"}, WantContract: []string{"view-missing"}},
		{Name: "wrong trial parent", Class: "wrong mapping", SQL: []string{"UPDATE trialroleinfo SET parent_role_id='1409' WHERE id='15127004'"}, Locales: zh,
			Want: []string{"data/rows-changed", "loaded/entry-changed", "display/name-changed", "display/static-role-changed"}},
		{Name: "wrong template relation", Class: "wrong mapping",
			SQL: []string{"UPDATE map_entity_template_ref SET template_id=(SELECT id FROM templateconfig ORDER BY id LIMIT 1) WHERE rowid=(SELECT max(rowid) FROM map_entity_template_ref)"}, Locales: zh,
			Want: []string{"data/rows-changed", "loaded/entry-changed"}},
		{Name: "identifier type change undeclared", Class: "ID type change", IntegerColumn: "roleinfo_locale.id", Locales: zh,
			Want: []string{"schema/column-type-changed", "data/storage-class-changed"}, WantContract: []string{"column-type-changed"}, None: []string{"loaded", "display"}},
		{Name: "identifier type change declared", Class: "ID type change", IntegerColumn: "roleinfo_locale.id", Locales: masterEquivalenceLocales, Expectations: typeExpect,
			Equivalent: true, None: []string{"loaded", "display"}, WantContract: []string{"column-type-changed"}},
		{Name: "non-canonical identifier", Class: "ID type change", SQL: []string{"UPDATE roleinfo_locale SET id='01409' WHERE id='1409' AND locale='zh-TW'"}, Locales: zh,
			Want: []string{"data/rows-missing", "data/rows-added", "loaded/entry-changed", "display/name-changed"}, WantContract: []string{"value-rule-violated", "reference-missing"}},
		{Name: "lost relation rows", Class: "data loss",
			SQL: []string{"DELETE FROM map_entity_template_ref WHERE rowid IN (SELECT rowid FROM map_entity_template_ref ORDER BY rowid LIMIT 100)"}, Locales: zh,
			Want: []string{"data/rows-missing", "loaded/entry-missing"}},
		{Name: "lost names", Class: "data loss",
			SQL: []string{"DELETE FROM skilldescription_locale WHERE locale='zh-TW' AND id IN (SELECT id FROM skilldescription_locale WHERE locale='zh-TW' ORDER BY id LIMIT 10)"}, Locales: zh,
			Want: []string{"data/rows-missing", "loaded/entry-changed", "display/name-changed"}},
		{Name: "UI skill id replaced by a combat skill id", Class: "namespace mixing",
			SQL: []string{"UPDATE skill_damage_ref SET skill_id=(SELECT d.id FROM skilldescription d WHERE d.id NOT IN (SELECT id FROM skill) ORDER BY d.id LIMIT 1) WHERE rowid=(SELECT min(rowid) FROM skill_damage_ref)"}, Locales: zh,
			Want: []string{"data/rows-missing", "data/rows-added", "loaded/entry-changed"}, WantContract: []string{"reference-missing"}},
		{Name: "Buff owner source given combat skill ids", Class: "namespace mixing",
			SQL: []string{"UPDATE skill_buff_ref SET skill_id=(SELECT id FROM skilldescription_locale WHERE locale='zh-TW' AND name<>'' AND id NOT IN (SELECT id FROM skill) ORDER BY id LIMIT 1) WHERE rowid=(SELECT min(rowid) FROM skill_buff_ref WHERE buff_id IN (SELECT id FROM buff))"}, Locales: zh,
			Want: []string{"data/rows-missing", "data/rows-added", "loaded/*"}, WantContract: []string{"reference-missing"}},
		{Name: "locale-only change, other locale", Class: "locale coverage",
			SQL:     []string{"UPDATE skilldescription_locale SET name=name||'*' WHERE locale='ja-JP' AND id=(SELECT id FROM skilldescription_locale WHERE locale='ja-JP' ORDER BY id LIMIT 1)"},
			Locales: zh, Layers: []string{"loaded", "display"}, Equivalent: true, None: []string{"loaded", "display"}},
		{Name: "locale-only change, its locale", Class: "locale coverage",
			SQL:     []string{"UPDATE skilldescription_locale SET name=name||'*' WHERE locale='ja-JP' AND id=(SELECT id FROM skilldescription_locale WHERE locale='ja-JP' ORDER BY id LIMIT 1)"},
			Locales: []string{"ja-JP"}, Layers: []string{"loaded", "display"}, Want: []string{"loaded/entry-changed", "display/name-changed"}},
		{Name: "unknown id becomes known", Class: "unknown ids",
			SQL: []string{"INSERT INTO monsterinfo(id) VALUES('999999991')", "INSERT INTO monsterinfo_locale(id, locale, name) VALUES('999999991','zh-TW','*')"}, Locales: zh,
			Want: []string{"data/rows-added", "loaded/entry-added", "display/name-changed"}},
		{Name: "order of client arrays", Class: "order",
			SQL: []string{
				"UPDATE buff_granted_tags SET ordinal = ordinal + 100 WHERE owner_id=(SELECT owner_id FROM buff_granted_tags GROUP BY owner_id HAVING count(*)=2 AND count(DISTINCT value)=2 ORDER BY owner_id LIMIT 1)",
				"UPDATE buff_granted_tags SET ordinal = 101 - ordinal WHERE ordinal >= 100",
			}, Locales: zh,
			Want: []string{"loaded/entry-changed"}},
		{Name: "index removed undeclared", Class: "index", SQL: []string{"DROP INDEX idx_map_entity_template_target"}, Locales: zh,
			Want: []string{"schema/index-missing"}, WantContract: []string{"index-missing"}, None: []string{"loaded", "display"}},
		{Name: "index removed declared", Class: "index", SQL: []string{"DROP INDEX idx_map_entity_template_target"}, Locales: zh, Equivalent: true,
			Expectations: []masterExpectation{{Layer: "schema", Class: "index-missing", Object: "map_entity_template_ref:idx_map_entity_template_target", Reason: "injected"}},
			WantContract: []string{"index-missing"}},
		{Name: "display-only category name", Class: "display path",
			SQL:     []string{"UPDATE secondaryguidedata_locale SET text=text||'*' WHERE locale='zh-TW' AND id=(SELECT secondary FROM dungeondetection GROUP BY secondary ORDER BY count(*) DESC, secondary LIMIT 1)"},
			Locales: zh, Want: []string{"data/rows-changed", "display/instance-mode-changed"}, None: []string{"loaded"}},
		{Name: "Development-only label", Class: "Development data",
			SQL:     []string{"UPDATE dev_label_locale SET label=label||'*' WHERE namespace='vision' AND locale='zh-TW' AND id=(SELECT id FROM dev_label_locale WHERE namespace='vision' AND locale='zh-TW' ORDER BY id LIMIT 1)"},
			Locales: zh, Want: []string{"data/rows-changed", "loaded/entry-changed"}, None: []string{"display"}},
		{Name: "unannounced table", Class: "structure", SQL: []string{"CREATE TABLE stray_research(x TEXT)"}, Locales: zh,
			Want: []string{"schema/table-added"}, WantContract: []string{"table-unlisted"}, None: []string{"loaded", "display"}},
	}
}

// TestMasterHarnessDetectsInjectedRegressionsInTheTrackedMaster is a collector: with WWLOGS_MASTER_SELFTEST_OUT set it
// copies the tracked Master (or WWLOGS_MASTER_SELFTEST_BASELINE), injects each regression of trackedMasterInjections into
// its own copy, and requires the equivalence harness and the contract checker, with the contract WWLOGS_MASTER_CONTRACT
// names, to report it. It first proves that an unchanged physical copy is equivalent in every layer and locale, twice,
// with the same result.
func TestMasterHarnessDetectsInjectedRegressionsInTheTrackedMaster(t *testing.T) {
	out := os.Getenv("WWLOGS_MASTER_SELFTEST_OUT")
	if out == "" {
		t.Skip("set WWLOGS_MASTER_SELFTEST_OUT to inject regressions into copies of the tracked Master")
	}
	baseline := os.Getenv("WWLOGS_MASTER_SELFTEST_BASELINE")
	if baseline == "" {
		baseline = filepath.Join("..", "..", "data", "master_data.db")
	}
	contractPath := os.Getenv(masterContractEnv)
	if contractPath == "" {
		t.Fatal("set " + masterContractEnv + " to the Master contract file: every injected regression is also checked against it")
	}
	contract, err := readMasterContract(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.db")
	vacuumMasterInto(t, baseline, identityPath)
	var identity []masterEquivalenceReport
	for run := 0; run < 2; run++ {
		report, err := runMasterEquivalence(baseline, identityPath, defaultMasterEquivalenceOptions())
		if err != nil {
			t.Fatal(err)
		}
		if !report.Equivalent {
			t.Errorf("identity run %d is not equivalent: %s", run+1, masterEquivalenceSummary(report))
		}
		identity = append(identity, report)
	}
	if identity[0].SemanticDigest == "" || identity[0].SemanticDigest != identity[1].SemanticDigest {
		t.Errorf("identity runs disagree: %s vs %s", identity[0].SemanticDigest, identity[1].SemanticDigest)
	}
	if a, b := fmt.Sprint(identity[0].Checked), fmt.Sprint(identity[1].Checked); a != b {
		t.Errorf("identity runs checked different amounts: %s vs %s", a, b)
	}
	var results []masterInjectionResult
	for i, injection := range trackedMasterInjections() {
		candidate := filepath.Join(dir, fmt.Sprintf("injection-%02d.db", i))
		vacuumMasterInto(t, baseline, candidate)
		if injection.IntegerColumn != "" {
			table, column, _ := strings.Cut(injection.IntegerColumn, ".")
			execMasterSQL(t, candidate, masterIntegerColumnStatements(t, candidate, table, column))
		}
		if len(injection.SQL) > 0 {
			execMasterSQL(t, candidate, injection.SQL)
		}
		options := defaultMasterEquivalenceOptions()
		options.Locales = injection.Locales
		if injection.Layers != nil {
			options.Layers = injection.Layers
		}
		options.Expectations = injection.Expectations
		report, err := runMasterEquivalence(baseline, candidate, options)
		if err != nil {
			t.Fatalf("%s: %v", injection.Name, err)
		}
		result := masterInjectionResult{masterInjection: injection, Counts: report.Counts, Unexpected: report.Unexpected}
		for _, class := range injection.Want {
			if !masterHasClass(report, class) {
				result.Missed = append(result.Missed, class)
			}
		}
		for _, layer := range injection.None {
			if n := masterLayerCount(report, layer); n > 0 {
				result.Missed = append(result.Missed, fmt.Sprintf("no %s differences (found %d)", layer, n))
			}
		}
		if report.Equivalent != injection.Equivalent {
			result.Missed = append(result.Missed, fmt.Sprintf("equivalent=%t", injection.Equivalent))
		}
		result.ContractViolations = masterContractClasses(t, candidate, contract)
		for _, class := range injection.WantContract {
			if result.ContractViolations[class] == 0 {
				result.Missed = append(result.Missed, "contract "+class)
			}
		}
		result.Detected = len(result.Missed) == 0
		if !result.Detected {
			t.Errorf("%s: missed %v; reported %s; contract %v", injection.Name, result.Missed, masterEquivalenceSummary(report), result.ContractViolations)
		}
		results = append(results, result)
	}
	baselineHash, err := hashFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	summary := map[string]any{
		"baseline":   map[string]any{"path": baseline, "sha256": baselineHash},
		"identity":   identity,
		"injected":   results,
		"detected":   countMasterDetected(results),
		"injections": len(results),
	}
	data, err := json.MarshalIndent(summary, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "master-harness-selftest.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	t.Logf("identity equivalent twice (semantic digest %s); %d of %d injected regressions detected; report %s", identity[0].SemanticDigest, countMasterDetected(results), len(results), hex.EncodeToString(digest[:8]))
}

func countMasterDetected(results []masterInjectionResult) int {
	n := 0
	for _, r := range results {
		if r.Detected {
			n++
		}
	}
	return n
}
