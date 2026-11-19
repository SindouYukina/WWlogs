package capture

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// The Master contract declares what the product relies on in data/master_data.db: the tables it reads with every column
// (declared type, nullability, key position), index and foreign key; the ID namespace, value rule and references of the
// columns that hold identifiers; the views it reads; and, by name only, every other table of the file. checkMasterContract
// turns any departure into a named violation, so a missing table, column or view, a changed type, an identifier a
// TEXT-to-INTEGER conversion could alter, a reference into the wrong namespace or a table that appears unannounced fails a
// test instead of being skipped by the loader's tolerance of optional tables. The contract is internal work kept outside
// the repository: the tests that need it read the file WWLOGS_MASTER_CONTRACT names and skip without it, and the checker
// itself is tested on a contract built from the fixture Master (TestMasterContractCheckerFindsEveryViolationClass).

const (
	// masterContractEnv names the environment variable that gives the Master contract file to the tests that need it.
	masterContractEnv = "WWLOGS_MASTER_CONTRACT"
	// masterJSSafeInteger is the largest integer a JavaScript number holds exactly; identifiers reach the frontend as JSON
	// numbers, and SQLite INTEGER is signed 64-bit, so an identifier must not exceed it.
	masterJSSafeInteger = 9007199254740991
)

type masterContract struct {
	Version      int                          `json:"version"`
	Description  string                       `json:"description"`
	Locales      []string                     `json:"locales"`
	Namespaces   []masterContractNamespace    `json:"namespaces"`
	Tables       []masterContractTable        `json:"tables"`
	Views        []masterContractView         `json:"views"`
	Uncontracted []masterContractUncontracted `json:"uncontracted"`
}

type masterContractNamespace struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type masterContractTable struct {
	Name         string                 `json:"name"`
	Scope        string                 `json:"scope"`
	Purpose      string                 `json:"purpose"`
	Source       string                 `json:"source,omitempty"`
	Curation     string                 `json:"curation,omitempty"`
	WithoutRowid bool                   `json:"withoutRowid"`
	NonEmpty     bool                   `json:"nonEmpty"`
	Columns      []masterContractColumn `json:"columns"`
	Indexes      []masterContractIndex  `json:"indexes"`
	ForeignKeys  []string               `json:"foreignKeys"`
}

type masterContractColumn struct {
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	NotNull           bool     `json:"notNull"`
	PK                int      `json:"pk"`
	Default           *string  `json:"default,omitempty"`
	Readers           []string `json:"readers,omitempty"`
	Namespace         string   `json:"namespace,omitempty"`
	Values            string   `json:"values,omitempty"`
	References        string   `json:"references,omitempty"`
	ReferenceEnforced bool     `json:"referenceEnforced,omitempty"`
	ReferenceNote     string   `json:"referenceNote,omitempty"`
	Purpose           string   `json:"purpose"`
}

type masterContractIndex struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Origin  string   `json:"origin"`
	Columns []string `json:"columns"`
}

type masterContractView struct {
	Name    string   `json:"name"`
	Scope   string   `json:"scope"`
	Purpose string   `json:"purpose"`
	Columns []string `json:"columns"`
}

type masterContractUncontracted struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	View           bool   `json:"view,omitempty"`
}

type masterContractViolation struct {
	Class  string `json:"class"`
	Object string `json:"object"`
	Detail string `json:"detail,omitempty"`
}

func (v masterContractViolation) String() string {
	if v.Detail == "" {
		return v.Class + " " + v.Object
	}
	return v.Class + " " + v.Object + ": " + v.Detail
}

func readMasterContract(path string) (masterContract, error) {
	var c masterContract
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// masterContractFromEnv reads the Master contract WWLOGS_MASTER_CONTRACT names and skips the test when it is unset.
func masterContractFromEnv(t *testing.T) masterContract {
	t.Helper()
	path := os.Getenv(masterContractEnv)
	if path == "" {
		t.Skip("set " + masterContractEnv + " to the Master contract file to check the Master against it")
	}
	c, err := readMasterContract(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// masterValueRule is the SQL condition a non-null value of a column must meet for each value rule of the contract.
func masterValueRule(rule, column string, locales []string) (string, bool) {
	c := masterQuote(column)
	switch rule {
	case "id":
		// A canonical non-negative decimal integer: digits only, no leading zero, at most the JavaScript safe integer.
		return fmt.Sprintf(`(typeof(%[1]s) = 'integer' AND %[1]s BETWEEN 0 AND %[2]d) OR (typeof(%[1]s) = 'text' AND %[1]s <> '' AND %[1]s NOT GLOB '*[^0-9]*' AND (%[1]s = '0' OR %[1]s NOT GLOB '0*') AND length(%[1]s) <= 16 AND CAST(%[1]s AS INTEGER) <= %[2]d)`, c, masterJSSafeInteger), true
	case "hash":
		return fmt.Sprintf(`typeof(%[1]s) = 'text' AND length(%[1]s) = 64 AND %[1]s NOT GLOB '*[^0-9a-f]*'`, c), true
	case "text-key":
		return fmt.Sprintf(`typeof(%[1]s) = 'text' AND %[1]s <> ''`, c), true
	case "optional-text-key":
		// Empty text means the row names none, as 0 does for a numeric identifier.
		return fmt.Sprintf(`typeof(%[1]s) = 'text'`, c), true
	case "locale":
		quoted := make([]string, len(locales))
		for i, l := range locales {
			quoted[i] = "'" + strings.ReplaceAll(l, "'", "''") + "'"
		}
		return fmt.Sprintf(`%s IN (%s)`, c, strings.Join(quoted, ",")), true
	case "":
		return "", false
	}
	return "", false
}

func masterCount(db *sql.DB, query string) (int, error) {
	var n int
	err := db.QueryRow(query).Scan(&n)
	return n, err
}

func masterSamples(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v.String)
	}
	return out, rows.Err()
}

// checkMasterContract compares a Master with its contract and lists every violation.
func checkMasterContract(db *sql.DB, c masterContract) ([]masterContractViolation, error) {
	info, err := readMasterSchemaInfo(db)
	if err != nil {
		return nil, err
	}
	var violations []masterContractViolation
	add := func(class, object, detail string) {
		violations = append(violations, masterContractViolation{class, object, detail})
	}
	contracted := map[string]bool{}
	for _, t := range c.Tables {
		contracted[t.Name] = true
	}
	contractedViews := map[string]bool{}
	for _, v := range c.Views {
		contractedViews[v.Name] = true
	}
	listedTables, listedViews := map[string]bool{}, map[string]bool{}
	for _, u := range c.Uncontracted {
		if u.View {
			listedViews[u.Name] = true
			if info.Views[u.Name] == nil {
				add("uncontracted-missing", u.Name, "listed view is absent; update the contract")
			}
			continue
		}
		listedTables[u.Name] = true
		if info.Tables[u.Name] == nil {
			add("uncontracted-missing", u.Name, "listed table is absent; update the contract")
		}
	}
	for _, name := range sortedMasterKeys(info.Tables) {
		if !contracted[name] && !listedTables[name] {
			add("table-unlisted", name, "a table the contract does not know")
		}
	}
	for _, name := range sortedMasterKeys(info.Views) {
		if !contractedViews[name] && !listedViews[name] {
			add("view-unlisted", name, "a view the contract does not know")
		}
	}
	for _, v := range c.Views {
		view := info.Views[v.Name]
		if view == nil {
			add("view-missing", v.Name, "")
			continue
		}
		if strings.Join(view.Columns, ",") != strings.Join(v.Columns, ",") {
			add("view-columns-changed", v.Name, "contract "+strings.Join(v.Columns, ",")+"; file "+strings.Join(view.Columns, ","))
		}
	}
	for _, t := range c.Tables {
		table := info.Tables[t.Name]
		if table == nil {
			add("table-missing", t.Name, "")
			continue
		}
		if table.WithoutRowid != t.WithoutRowid {
			add("without-rowid-changed", t.Name, fmt.Sprintf("contract %t; file %t", t.WithoutRowid, table.WithoutRowid))
		}
		fileColumns := map[string]masterColumnSchema{}
		var fileOrder []string
		for _, col := range table.Columns {
			fileColumns[col.Name] = col
			fileOrder = append(fileOrder, col.Name)
		}
		contractColumns := map[string]bool{}
		var contractOrder []string
		for _, col := range t.Columns {
			contractColumns[col.Name] = true
			contractOrder = append(contractOrder, col.Name)
			fc, ok := fileColumns[col.Name]
			if !ok {
				add("column-missing", t.Name+"."+col.Name, "")
				continue
			}
			if !strings.EqualFold(fc.Type, col.Type) {
				add("column-type-changed", t.Name+"."+col.Name, "contract "+col.Type+"; file "+fc.Type)
			}
			if fc.NotNull != col.NotNull {
				add("column-notnull-changed", t.Name+"."+col.Name, fmt.Sprintf("contract %t; file %t", col.NotNull, fc.NotNull))
			}
			if fc.PK != col.PK {
				add("column-pk-changed", t.Name+"."+col.Name, fmt.Sprintf("contract %d; file %d", col.PK, fc.PK))
			}
			contractDefault := ""
			if col.Default != nil {
				contractDefault = *col.Default
			}
			if fc.Default.String != contractDefault {
				add("column-default-changed", t.Name+"."+col.Name, "contract "+contractDefault+"; file "+fc.Default.String)
			}
		}
		for _, name := range fileOrder {
			if !contractColumns[name] {
				add("column-unlisted", t.Name+"."+name, "a column the contract does not know")
			}
		}
		if strings.Join(filterMasterNames(fileOrder, contractColumns), ",") != strings.Join(filterMasterNames(contractOrder, fileColumns), ",") {
			add("column-order-changed", t.Name, "contract "+strings.Join(contractOrder, ",")+"; file "+strings.Join(fileOrder, ","))
		}
		fileIndexes := map[string]masterIndexSchema{}
		for _, ix := range table.Indexes {
			fileIndexes[ix.Name] = ix
		}
		contractIndexes := map[string]bool{}
		for _, ix := range t.Indexes {
			contractIndexes[ix.Name] = true
			fix, ok := fileIndexes[ix.Name]
			if !ok {
				add("index-missing", t.Name+":"+ix.Name, "")
				continue
			}
			if fix.Unique != ix.Unique || fix.Origin != ix.Origin || strings.Join(fix.Columns, ",") != strings.Join(ix.Columns, ",") {
				add("index-changed", t.Name+":"+ix.Name, fmt.Sprintf("contract unique=%t origin=%s columns=%s; file %s", ix.Unique, ix.Origin, strings.Join(ix.Columns, ","), masterIndexText(fix)))
			}
		}
		for _, name := range sortedMasterKeys(fileIndexes) {
			if !contractIndexes[name] {
				add("index-unlisted", t.Name+":"+name, masterIndexText(fileIndexes[name]))
			}
		}
		fileKeys := map[string]bool{}
		for _, k := range table.ForeignKeys {
			fileKeys[k] = true
		}
		contractKeys := map[string]bool{}
		for _, k := range t.ForeignKeys {
			contractKeys[k] = true
			if !fileKeys[k] {
				add("foreign-key-missing", t.Name, k)
			}
		}
		for _, k := range table.ForeignKeys {
			if !contractKeys[k] {
				add("foreign-key-unlisted", t.Name, k)
			}
		}
		rows, err := masterCount(db, "SELECT count(*) FROM "+masterQuote(t.Name))
		if err != nil {
			return nil, err
		}
		if t.NonEmpty && rows == 0 {
			add("table-empty", t.Name, "the product needs rows here")
		}
		for _, col := range t.Columns {
			if _, ok := fileColumns[col.Name]; !ok {
				continue
			}
			if rule, ok := masterValueRule(col.Values, col.Name, c.Locales); ok {
				where := fmt.Sprintf("%s IS NOT NULL AND NOT (%s)", masterQuote(col.Name), rule)
				bad, err := masterCount(db, "SELECT count(*) FROM "+masterQuote(t.Name)+" WHERE "+where)
				if err != nil {
					return nil, err
				}
				if bad > 0 {
					samples, err := masterSamples(db, "SELECT DISTINCT CAST("+masterQuote(col.Name)+" AS TEXT) FROM "+masterQuote(t.Name)+" WHERE "+where+" LIMIT 5")
					if err != nil {
						return nil, err
					}
					add("value-rule-violated", t.Name+"."+col.Name, fmt.Sprintf("%d value(s) break rule %q, e.g. %q", bad, col.Values, samples))
				}
			}
			if col.References != "" && col.ReferenceEnforced {
				target := strings.SplitN(col.References, ".", 2)
				if len(target) != 2 || info.Tables[target[0]] == nil {
					add("reference-target-missing", t.Name+"."+col.Name, col.References)
					continue
				}
				where := fmt.Sprintf("a.%[1]s IS NOT NULL AND CAST(a.%[1]s AS TEXT) NOT IN ('', '0') AND NOT EXISTS (SELECT 1 FROM %[2]s b WHERE b.%[3]s = a.%[1]s)",
					masterQuote(col.Name), masterQuote(target[0]), masterQuote(target[1]))
				missing, err := masterCount(db, "SELECT count(*) FROM "+masterQuote(t.Name)+" a WHERE "+where)
				if err != nil {
					return nil, err
				}
				if missing > 0 {
					samples, err := masterSamples(db, "SELECT DISTINCT CAST(a."+masterQuote(col.Name)+" AS TEXT) FROM "+masterQuote(t.Name)+" a WHERE "+where+" LIMIT 5")
					if err != nil {
						return nil, err
					}
					add("reference-missing", t.Name+"."+col.Name, fmt.Sprintf("%d value(s) are not in %s (%s), e.g. %q", missing, col.References, col.Namespace, samples))
				}
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Class != violations[j].Class {
			return violations[i].Class < violations[j].Class
		}
		return violations[i].Object < violations[j].Object
	})
	return violations, nil
}

func TestTrackedMasterSatisfiesItsContract(t *testing.T) {
	contract := masterContractFromEnv(t)
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	violations, err := checkMasterContract(db, contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v.String())
	}
}

func TestMasterContractIsConsistent(t *testing.T) {
	contract := masterContractFromEnv(t)
	namespaces := map[string]bool{}
	for _, n := range contract.Namespaces {
		if n.Description == "" {
			t.Errorf("namespace %s has no description", n.Name)
		}
		namespaces[n.Name] = true
	}
	scopes := map[string]bool{"product": true, "development": true, "integrity": true, "maintenance": true}
	seen := map[string]bool{}
	for _, table := range contract.Tables {
		if seen[table.Name] {
			t.Errorf("table %s is listed twice", table.Name)
		}
		seen[table.Name] = true
		if !scopes[table.Scope] || table.Purpose == "" {
			t.Errorf("table %s needs a known scope and a purpose", table.Name)
		}
		for _, col := range table.Columns {
			if col.Purpose == "" {
				t.Errorf("column %s.%s has no purpose", table.Name, col.Name)
			}
			if col.Namespace != "" && !namespaces[col.Namespace] {
				t.Errorf("column %s.%s names undeclared namespace %s", table.Name, col.Name, col.Namespace)
			}
			if _, ok := masterValueRule(col.Values, col.Name, contract.Locales); col.Values != "" && !ok {
				t.Errorf("column %s.%s has unknown value rule %q", table.Name, col.Name, col.Values)
			}
		}
	}
	for _, u := range contract.Uncontracted {
		if seen[u.Name] {
			t.Errorf("%s is both contracted and uncontracted", u.Name)
		}
		seen[u.Name] = true
	}
	if strings.Join(contract.Locales, ",") != strings.Join(masterEquivalenceLocales, ",") {
		t.Errorf("contract locales %v differ from the product's Master locales %v", contract.Locales, masterEquivalenceLocales)
	}
	for locale := range supportedMasterLocales {
		found := false
		for _, l := range contract.Locales {
			found = found || l == locale
		}
		if !found {
			t.Errorf("supported Master locale %s is not in the contract", locale)
		}
	}
}
