package capture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Master equivalence harness compares two Master SQLite files the way the product depends on them, in four layers:
//
//	schema   tables, columns (declared type, nullability, key), indexes, foreign keys and views
//	data     every row of every table both files have, with values normalized to text so that a storage-type change
//	         alone is not a data change; storage classes per column are compared separately
//	loaded   every field of GameMaster as LoadGameMasterDatabase builds it, entry by entry, in each Master locale
//	display  every DisplayResolver answer (Resolve, RoleElement, StaticRole, InstanceMode, InstanceStage) for every id the
//	         Master can list in each namespace, plus ids it does not have, in each locale
//
// The product loads everything it needs from the Master into GameMaster at start and asks DisplayResolver for the rest, so
// the loaded and display layers cover every Master-dependent decision for every id, whether or not a capture contains it.
// Only masterNonSemanticFields may differ in the loaded layer; nothing may differ in the display layer. A planned
// structural change (a removed table, a changed column type, an index) is declared in an expectation file and must then
// occur exactly; loaded and display differences can never be declared. The harness opens both files read-only.

// masterEquivalenceLocales are the Master locales of the product (supportedMasterLocales).
var masterEquivalenceLocales = []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}

const masterEquivalenceSamplesPerClass = 20

// masterNonSemanticFields name the file a Master was read from, not its data.
var masterNonSemanticFields = map[string]bool{
	"GameMaster.DatabasePath":        true,
	"GameMaster.Manifest.SourceHash": true,
}

var damageMasterRecordType = reflect.TypeOf(DamageMasterRecord{})

// masterDifference is one difference, or a counted group of them, between two Masters.
type masterDifference struct {
	Layer  string `json:"layer"`
	Class  string `json:"class"`
	Object string `json:"object"`
	Locale string `json:"locale,omitempty"`
	Key    string `json:"key,omitempty"`
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
	Count  int    `json:"count"`
}

type masterDifferences struct {
	counts   map[string]int // layer/class
	objects  map[string]int // layer/class/object
	sampled  map[string]int
	samples  []masterDifference
	maxValue int
}

func newMasterDifferences() *masterDifferences {
	return &masterDifferences{counts: map[string]int{}, objects: map[string]int{}, sampled: map[string]int{}, maxValue: 240}
}

func (d *masterDifferences) add(x masterDifference) {
	if x.Count == 0 {
		x.Count = 1
	}
	class := x.Layer + "/" + x.Class
	d.counts[class] += x.Count
	d.objects[class+"/"+x.Object] += x.Count
	if d.sampled[class] < masterEquivalenceSamplesPerClass {
		d.sampled[class]++
		x.Old, x.New, x.Key = truncateMasterText(x.Old, d.maxValue), truncateMasterText(x.New, d.maxValue), truncateMasterText(x.Key, d.maxValue)
		d.samples = append(d.samples, x)
	}
}

func truncateMasterText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// --- schema ---

type masterColumnSchema struct {
	Name    string
	Type    string
	NotNull bool
	Default sql.NullString
	PK      int
	Hidden  int
}

type masterIndexSchema struct {
	Name    string
	Unique  bool
	Origin  string
	Partial bool
	Columns []string
}

type masterTableSchema struct {
	Name         string
	WithoutRowid bool
	Columns      []masterColumnSchema
	Indexes      []masterIndexSchema
	ForeignKeys  []string
}

type masterViewSchema struct {
	SQL     string
	Columns []string
}

type masterSchemaInfo struct {
	Tables map[string]*masterTableSchema
	Views  map[string]*masterViewSchema
}

// readMasterSchemaInfo reads the structure of every user table and view. The connection may allow one open statement at a
// time, so each query is read to the end before the next one starts.
func readMasterSchemaInfo(db *sql.DB) (masterSchemaInfo, error) {
	info := masterSchemaInfo{Tables: map[string]*masterTableSchema{}, Views: map[string]*masterViewSchema{}}
	type object struct{ kind, name, sql string }
	var objects []object
	rows, err := db.Query("SELECT type, name, COALESCE(sql, '') FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		return info, err
	}
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.kind, &o.name, &o.sql); err != nil {
			rows.Close()
			return info, err
		}
		objects = append(objects, o)
	}
	if err := rows.Close(); err != nil {
		return info, err
	}
	for _, o := range objects {
		if o.kind == "view" {
			columns, err := masterPragmaColumnNames(db, o.name)
			if err != nil {
				return info, err
			}
			info.Views[o.name] = &masterViewSchema{SQL: strings.Join(strings.Fields(o.sql), " "), Columns: columns}
			continue
		}
		table := &masterTableSchema{Name: o.name, WithoutRowid: strings.Contains(strings.ToUpper(o.sql), "WITHOUT ROWID")}
		if table.Columns, err = masterPragmaColumns(db, o.name); err != nil {
			return info, err
		}
		if table.Indexes, err = masterPragmaIndexes(db, o.name, table.WithoutRowid); err != nil {
			return info, err
		}
		if table.ForeignKeys, err = masterPragmaForeignKeys(db, o.name); err != nil {
			return info, err
		}
		info.Tables[o.name] = table
	}
	return info, nil
}

func masterQuote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func masterPragmaColumns(db *sql.DB, table string) ([]masterColumnSchema, error) {
	rows, err := db.Query("PRAGMA table_xinfo(" + masterQuote(table) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []masterColumnSchema
	for rows.Next() {
		var cid int
		var c masterColumnSchema
		var notNull int
		if err := rows.Scan(&cid, &c.Name, &c.Type, &notNull, &c.Default, &c.PK, &c.Hidden); err != nil {
			return nil, err
		}
		c.NotNull = notNull != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func masterPragmaColumnNames(db *sql.DB, name string) ([]string, error) {
	columns, err := masterPragmaColumns(db, name)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, c.Name)
	}
	return names, nil
}

func masterPragmaIndexes(db *sql.DB, table string, withoutRowid bool) ([]masterIndexSchema, error) {
	rows, err := db.Query("PRAGMA index_list(" + masterQuote(table) + ")")
	if err != nil {
		return nil, err
	}
	var indexes []masterIndexSchema
	for rows.Next() {
		var seq, unique, partial int
		var ix masterIndexSchema
		if err := rows.Scan(&seq, &ix.Name, &unique, &ix.Origin, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		ix.Unique, ix.Partial = unique != 0, partial != 0
		// The primary key of a WITHOUT ROWID table is the table itself, not a separate index.
		if withoutRowid && ix.Origin == "pk" {
			continue
		}
		indexes = append(indexes, ix)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range indexes {
		keyRows, err := db.Query("PRAGMA index_xinfo(" + masterQuote(indexes[i].Name) + ")")
		if err != nil {
			return nil, err
		}
		for keyRows.Next() {
			var seqno, cid, desc, key int
			var name, collation sql.NullString
			if err := keyRows.Scan(&seqno, &cid, &name, &desc, &collation, &key); err != nil {
				keyRows.Close()
				return nil, err
			}
			if key == 1 {
				indexes[i].Columns = append(indexes[i].Columns, name.String)
			}
		}
		if err := keyRows.Close(); err != nil {
			return nil, err
		}
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i].Name < indexes[j].Name })
	return indexes, nil
}

func masterPragmaForeignKeys(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query("PRAGMA foreign_key_list(" + masterQuote(table) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		table    string
		from, to []string
	}
	keys := map[int]*key{}
	for rows.Next() {
		var id, seq int
		var target, from string
		var to sql.NullString
		var onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		if keys[id] == nil {
			keys[id] = &key{table: target}
		}
		keys[id].from = append(keys[id].from, from)
		keys[id].to = append(keys[id].to, to.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, strings.Join(k.from, ",")+"->"+k.table+"("+strings.Join(k.to, ",")+")")
	}
	sort.Strings(out)
	return out, nil
}

func masterIndexText(ix masterIndexSchema) string {
	return fmt.Sprintf("unique=%t origin=%s partial=%t columns=%s", ix.Unique, ix.Origin, ix.Partial, strings.Join(ix.Columns, ","))
}

// compareMasterSchemas records structural differences and returns the tables both files have, with the columns they
// share in the old file's order.
func compareMasterSchemas(oldInfo, newInfo masterSchemaInfo, d *masterDifferences) map[string][]string {
	common := map[string][]string{}
	for _, name := range sortedMasterKeys(oldInfo.Tables) {
		oldTable := oldInfo.Tables[name]
		newTable, ok := newInfo.Tables[name]
		if !ok {
			d.add(masterDifference{Layer: "schema", Class: "table-missing", Object: name})
			continue
		}
		if oldTable.WithoutRowid != newTable.WithoutRowid {
			d.add(masterDifference{Layer: "schema", Class: "without-rowid-changed", Object: name, Old: strconv.FormatBool(oldTable.WithoutRowid), New: strconv.FormatBool(newTable.WithoutRowid)})
		}
		newColumns := map[string]masterColumnSchema{}
		newOrder := []string{}
		for _, c := range newTable.Columns {
			newColumns[c.Name] = c
			newOrder = append(newOrder, c.Name)
		}
		var shared, oldOrder []string
		for _, c := range oldTable.Columns {
			oldOrder = append(oldOrder, c.Name)
			nc, ok := newColumns[c.Name]
			if !ok {
				d.add(masterDifference{Layer: "schema", Class: "column-missing", Object: name + "." + c.Name, Old: c.Type})
				continue
			}
			shared = append(shared, c.Name)
			if !strings.EqualFold(c.Type, nc.Type) {
				d.add(masterDifference{Layer: "schema", Class: "column-type-changed", Object: name + "." + c.Name, Old: c.Type, New: nc.Type})
			}
			if c.NotNull != nc.NotNull {
				d.add(masterDifference{Layer: "schema", Class: "column-notnull-changed", Object: name + "." + c.Name, Old: strconv.FormatBool(c.NotNull), New: strconv.FormatBool(nc.NotNull)})
			}
			if c.PK != nc.PK {
				d.add(masterDifference{Layer: "schema", Class: "primary-key-changed", Object: name + "." + c.Name, Old: strconv.Itoa(c.PK), New: strconv.Itoa(nc.PK)})
			}
			if c.Default != nc.Default {
				d.add(masterDifference{Layer: "schema", Class: "column-default-changed", Object: name + "." + c.Name, Old: c.Default.String, New: nc.Default.String})
			}
		}
		oldNames := map[string]bool{}
		for _, c := range oldTable.Columns {
			oldNames[c.Name] = true
		}
		for _, c := range newTable.Columns {
			if !oldNames[c.Name] {
				d.add(masterDifference{Layer: "schema", Class: "column-added", Object: name + "." + c.Name, New: c.Type})
			}
		}
		if strings.Join(filterMasterNames(oldOrder, newColumns), ",") != strings.Join(filterMasterNames(newOrder, oldNames), ",") {
			d.add(masterDifference{Layer: "schema", Class: "column-order-changed", Object: name, Old: strings.Join(oldOrder, ","), New: strings.Join(newOrder, ",")})
		}
		oldIndexes, newIndexes := map[string]masterIndexSchema{}, map[string]masterIndexSchema{}
		for _, ix := range oldTable.Indexes {
			oldIndexes[ix.Name] = ix
		}
		for _, ix := range newTable.Indexes {
			newIndexes[ix.Name] = ix
		}
		for _, ixName := range sortedMasterKeys(oldIndexes) {
			nix, ok := newIndexes[ixName]
			if !ok {
				d.add(masterDifference{Layer: "schema", Class: "index-missing", Object: name + ":" + ixName, Old: masterIndexText(oldIndexes[ixName])})
			} else if masterIndexText(nix) != masterIndexText(oldIndexes[ixName]) {
				d.add(masterDifference{Layer: "schema", Class: "index-changed", Object: name + ":" + ixName, Old: masterIndexText(oldIndexes[ixName]), New: masterIndexText(nix)})
			}
		}
		for _, ixName := range sortedMasterKeys(newIndexes) {
			if _, ok := oldIndexes[ixName]; !ok {
				d.add(masterDifference{Layer: "schema", Class: "index-added", Object: name + ":" + ixName, New: masterIndexText(newIndexes[ixName])})
			}
		}
		oldKeys, newKeys := map[string]bool{}, map[string]bool{}
		for _, k := range oldTable.ForeignKeys {
			oldKeys[k] = true
		}
		for _, k := range newTable.ForeignKeys {
			newKeys[k] = true
			if !oldKeys[k] {
				d.add(masterDifference{Layer: "schema", Class: "foreign-key-added", Object: name, New: k})
			}
		}
		for _, k := range oldTable.ForeignKeys {
			if !newKeys[k] {
				d.add(masterDifference{Layer: "schema", Class: "foreign-key-missing", Object: name, Old: k})
			}
		}
		common[name] = shared
	}
	for _, name := range sortedMasterKeys(newInfo.Tables) {
		if _, ok := oldInfo.Tables[name]; !ok {
			d.add(masterDifference{Layer: "schema", Class: "table-added", Object: name})
		}
	}
	for _, name := range sortedMasterKeys(oldInfo.Views) {
		nv, ok := newInfo.Views[name]
		switch {
		case !ok:
			d.add(masterDifference{Layer: "schema", Class: "view-missing", Object: name})
		case nv.SQL != oldInfo.Views[name].SQL:
			d.add(masterDifference{Layer: "schema", Class: "view-changed", Object: name, Old: oldInfo.Views[name].SQL, New: nv.SQL})
		}
		if ok && strings.Join(nv.Columns, ",") != strings.Join(oldInfo.Views[name].Columns, ",") {
			d.add(masterDifference{Layer: "schema", Class: "view-columns-changed", Object: name, Old: strings.Join(oldInfo.Views[name].Columns, ","), New: strings.Join(nv.Columns, ",")})
		}
	}
	for _, name := range sortedMasterKeys(newInfo.Views) {
		if _, ok := oldInfo.Views[name]; !ok {
			d.add(masterDifference{Layer: "schema", Class: "view-added", Object: name})
		}
	}
	return common
}

func filterMasterNames[V any](names []string, keep map[string]V) []string {
	var out []string
	for _, n := range names {
		if _, ok := keep[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

func sortedMasterKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- data ---

var masterTextEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

// renderMasterSQLValue writes a stored value as text; TEXT "12" and INTEGER 12 render the same, so a storage-type change
// alone is not a data change. NULL and BLOB render with a prefix no stored text has.
func renderMasterSQLValue(v any) (text, class string) {
	switch x := v.(type) {
	case nil:
		return "\x00NULL", "null"
	case int64:
		return strconv.FormatInt(x, 10), "integer"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return strconv.FormatInt(int64(x), 10), "real"
		}
		return strconv.FormatFloat(x, 'g', -1, 64), "real"
	case string:
		return masterTextEscaper.Replace(x), "text"
	case []byte:
		return "\x00BLOB:" + hex.EncodeToString(x), "blob"
	case bool:
		if x {
			return "1", "integer"
		}
		return "0", "integer"
	default:
		return fmt.Sprintf("\x00%T:%v", v, v), "other"
	}
}

// masterTableRow is one stored row: its rendered values and, column by column, the storage class of each value.
type masterTableRow struct {
	text    string
	classes []string
}

type masterTableRows struct {
	rows  map[string][]masterTableRow // key (primary key values) -> rows with that key
	total int
}

func readMasterTableRows(db *sql.DB, table string, columns []string, keyColumns []int) (masterTableRows, error) {
	out := masterTableRows{rows: map[string][]masterTableRow{}}
	if len(columns) == 0 {
		return out, nil
	}
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = masterQuote(c)
	}
	rows, err := db.Query("SELECT " + strings.Join(quoted, ",") + " FROM " + masterQuote(table))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	texts := make([]string, len(columns))
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return out, err
		}
		classes := make([]string, len(columns))
		for i, v := range values {
			texts[i], classes[i] = renderMasterSQLValue(v)
		}
		key := strings.Join(texts, "\t")
		if len(keyColumns) > 0 {
			parts := make([]string, len(keyColumns))
			for i, k := range keyColumns {
				parts[i] = texts[k]
			}
			key = strings.Join(parts, "\t")
		}
		out.rows[key] = append(out.rows[key], masterTableRow{text: strings.Join(texts, "\t"), classes: classes})
		out.total++
	}
	return out, rows.Err()
}

func sortMasterTableRows(list []masterTableRow) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].text != list[j].text {
			return list[i].text < list[j].text
		}
		return strings.Join(list[i].classes, "\t") < strings.Join(list[j].classes, "\t")
	})
}

func masterRowTexts(list []masterTableRow) string {
	texts := make([]string, len(list))
	for i, r := range list {
		texts[i] = r.text
	}
	return strings.Join(texts, " | ")
}

// compareMasterTableData compares the rows of one table by its primary key (the old file's key columns that both files
// share), or as a multiset when the table has no shared key.
func compareMasterTableData(oldDB, newDB *sql.DB, table string, columns []string, oldTable *masterTableSchema, d *masterDifferences) (int, error) {
	var keyColumns []int
	type keyed struct{ pk, index int }
	var keys []keyed
	for i, name := range columns {
		for _, c := range oldTable.Columns {
			if c.Name == name && c.PK > 0 {
				keys = append(keys, keyed{c.PK, i})
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].pk < keys[j].pk })
	pkCount := 0
	for _, c := range oldTable.Columns {
		if c.PK > 0 {
			pkCount++
		}
	}
	if len(keys) == pkCount && pkCount > 0 {
		for _, k := range keys {
			keyColumns = append(keyColumns, k.index)
		}
	}
	oldRows, err := readMasterTableRows(oldDB, table, columns, keyColumns)
	if err != nil {
		return 0, fmt.Errorf("read old %s: %w", table, err)
	}
	newRows, err := readMasterTableRows(newDB, table, columns, keyColumns)
	if err != nil {
		return 0, fmt.Errorf("read new %s: %w", table, err)
	}
	// A storage class change is counted only for a value both files hold in the same row (same key, same rendered
	// row), so rows that are added, lost or changed are reported as such and never also as a class change.
	transitions := make([]map[string]int, len(columns))
	for _, key := range sortedMasterKeys(oldRows.rows) {
		oldList := oldRows.rows[key]
		newList, ok := newRows.rows[key]
		if !ok {
			d.add(masterDifference{Layer: "data", Class: "rows-missing", Object: table, Key: key, Old: masterRowTexts(oldList), Count: len(oldList)})
			continue
		}
		sortMasterTableRows(oldList)
		sortMasterTableRows(newList)
		if masterRowTexts(oldList) != masterRowTexts(newList) {
			d.add(masterDifference{Layer: "data", Class: "rows-changed", Object: table, Key: key, Old: masterRowTexts(oldList), New: masterRowTexts(newList)})
			continue
		}
		for j := range oldList {
			for i := range columns {
				if from, to := oldList[j].classes[i], newList[j].classes[i]; from != to {
					if transitions[i] == nil {
						transitions[i] = map[string]int{}
					}
					transitions[i][from+"\t"+to]++
				}
			}
		}
	}
	for _, key := range sortedMasterKeys(newRows.rows) {
		if _, ok := oldRows.rows[key]; !ok {
			d.add(masterDifference{Layer: "data", Class: "rows-added", Object: table, Key: key, New: masterRowTexts(newRows.rows[key]), Count: len(newRows.rows[key])})
		}
	}
	for i, name := range columns {
		for _, transition := range sortedMasterKeys(transitions[i]) {
			from, to, _ := strings.Cut(transition, "\t")
			d.add(masterDifference{Layer: "data", Class: "storage-class-changed", Object: table + "." + name, Old: from, New: to, Count: transitions[i][transition]})
		}
	}
	return oldRows.total, nil
}

// --- loaded ---

// masterLoadedEntries renders every field of a loaded Master as entries: a map field has one entry per key, a slice one
// per index, any other field one entry. Pointer and struct fields are followed into their own fields.
func masterLoadedEntries(m *GameMaster) map[string]map[string]string {
	out := map[string]map[string]string{}
	collectMasterLoadedEntries(reflect.ValueOf(m).Elem(), "GameMaster", out)
	return out
}

func collectMasterLoadedEntries(v reflect.Value, prefix string, out map[string]map[string]string) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		name := prefix + "." + t.Field(i).Name
		if masterNonSemanticFields[name] {
			continue
		}
		field := v.Field(i)
		switch field.Kind() {
		case reflect.Pointer:
			if field.IsNil() {
				out[name] = map[string]string{"": "nil"}
			} else if field.Elem().Kind() == reflect.Struct {
				collectMasterLoadedEntries(field.Elem(), name, out)
			} else {
				out[name] = map[string]string{"": renderMasterLoaded(field)}
			}
		case reflect.Struct:
			collectMasterLoadedEntries(field, name, out)
		case reflect.Map:
			entries := make(map[string]string, field.Len())
			iter := field.MapRange()
			for iter.Next() {
				entries[renderMasterLoaded(iter.Key())] = renderMasterLoaded(iter.Value())
			}
			out[name] = entries
		case reflect.Slice, reflect.Array:
			entries := make(map[string]string, field.Len())
			for j := 0; j < field.Len(); j++ {
				entries[strconv.Itoa(j)] = renderMasterLoaded(field.Index(j))
			}
			out[name] = entries
		default:
			out[name] = map[string]string{"": renderMasterLoaded(field)}
		}
	}
}

func renderMasterLoaded(v reflect.Value) string {
	var b strings.Builder
	renderMasterLoadedInto(v, &b, 0)
	return b.String()
}

// renderMasterLoadedInto renders a value deterministically (maps sorted by rendered key, slices in order); the source hash
// of a damage record names the file and is left out.
func renderMasterLoadedInto(v reflect.Value, b *strings.Builder, depth int) {
	if depth > 64 {
		b.WriteString("<depth>")
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		renderMasterLoadedInto(v.Elem(), b, depth+1)
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		values := make(map[string]string, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			k := renderMasterLoaded(iter.Key())
			keys = append(keys, k)
			values[k] = renderMasterLoaded(iter.Value())
		}
		sort.Strings(keys)
		b.WriteString("{")
		for _, k := range keys {
			b.WriteString(k)
			b.WriteString(":")
			b.WriteString(values[k])
			b.WriteString(",")
		}
		b.WriteString("}")
	case reflect.Slice, reflect.Array:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			renderMasterLoadedInto(v.Index(i), b, depth+1)
			b.WriteString(",")
		}
		b.WriteString("]")
	case reflect.Struct:
		t := v.Type()
		b.WriteString("(")
		for i := 0; i < v.NumField(); i++ {
			if t == damageMasterRecordType && t.Field(i).Name == "SourceHash" {
				continue
			}
			b.WriteString(t.Field(i).Name)
			b.WriteString("=")
			renderMasterLoadedInto(v.Field(i), b, depth+1)
			b.WriteString(";")
		}
		b.WriteString(")")
	case reflect.String:
		b.WriteString(strconv.Quote(v.String()))
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		b.WriteString("<" + v.Kind().String() + ">")
	}
}

func compareMasterLoaded(oldEntries, newEntries map[string]map[string]string, locale string, d *masterDifferences) int {
	compared := 0
	names := map[string]bool{}
	for name := range oldEntries {
		names[name] = true
	}
	for name := range newEntries {
		names[name] = true
	}
	for _, name := range sortedMasterKeys(names) {
		oldField, oldOK := oldEntries[name]
		newField, newOK := newEntries[name]
		if !oldOK || !newOK {
			d.add(masterDifference{Layer: "loaded", Class: "field-changed", Object: name, Locale: locale, Old: strconv.FormatBool(oldOK), New: strconv.FormatBool(newOK)})
			continue
		}
		for _, key := range sortedMasterKeys(oldField) {
			compared++
			nv, ok := newField[key]
			if !ok {
				d.add(masterDifference{Layer: "loaded", Class: "entry-missing", Object: name, Locale: locale, Key: key, Old: oldField[key]})
			} else if nv != oldField[key] {
				d.add(masterDifference{Layer: "loaded", Class: "entry-changed", Object: name, Locale: locale, Key: key, Old: oldField[key], New: nv})
			}
		}
		for _, key := range sortedMasterKeys(newField) {
			if _, ok := oldField[key]; !ok {
				d.add(masterDifference{Layer: "loaded", Class: "entry-added", Object: name, Locale: locale, Key: key, New: newField[key]})
			}
		}
	}
	return compared
}

// --- display ---

// masterDisplayKinds are the DisplayResolver name kinds and the Master rows that list each kind's ids.
var masterDisplayKinds = []struct {
	kind    string
	queries []string
}{
	{"role", []string{"SELECT id FROM roleinfo", "SELECT id FROM roleinfo_locale", "SELECT id FROM trialroleinfo"}},
	{"skill", []string{"SELECT id FROM skill", "SELECT id FROM skill_locale"}},
	{"combat_skill", []string{"SELECT id FROM skilldescription", "SELECT id FROM skilldescription_locale"}},
	{"echo", []string{"SELECT id FROM phantomitem", "SELECT id FROM phantomitem_locale"}},
	{"monster", []string{"SELECT id FROM monsterinfo", "SELECT id FROM monsterinfo_locale"}},
	{"term", []string{"SELECT id FROM term", "SELECT id FROM term_locale"}},
	{"instance", []string{"SELECT id FROM instancedungeon", "SELECT id FROM instancedungeon_locale", "SELECT dungeon_id FROM lordgymentranceset", "SELECT instance_id FROM map_instance"}},
	{"template", []string{"SELECT id FROM templateconfig", "SELECT id FROM templateconfig_locale"}},
}

// masterDisplayProbes are ids no namespace should resolve: zero, the JavaScript safe-integer bound and the largest uint64.
var masterDisplayProbes = []uint64{0, 9007199254740991, 9007199254740992, math.MaxUint64}

// masterListIDs reads the numeric ids a query lists; a query on a table the file lacks lists none (the schema layer reports
// the table).
func masterListIDs(db *sql.DB, query string) ([]uint64, int) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()
	var ids []uint64
	invalid := 0
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil || !raw.Valid {
			invalid++
			continue
		}
		id, err := strconv.ParseUint(raw.String, 10, 64)
		if err != nil {
			invalid++
			continue
		}
		ids = append(ids, id)
	}
	return ids, invalid
}

func masterDisplayDomains(oldDB, newDB *sql.DB) (map[string][]uint64, map[string][]uint64) {
	domains := map[string][]uint64{}
	probes := map[string][]uint64{}
	sets := map[string]map[uint64]bool{}
	for _, k := range masterDisplayKinds {
		set := map[uint64]bool{}
		for _, db := range []*sql.DB{oldDB, newDB} {
			for _, q := range k.queries {
				ids, _ := masterListIDs(db, q)
				for _, id := range ids {
					set[id] = true
				}
			}
		}
		sets[k.kind] = set
		domain := make([]uint64, 0, len(set))
		for id := range set {
			domain = append(domain, id)
		}
		sort.Slice(domain, func(i, j int) bool { return domain[i] < domain[j] })
		domains[k.kind] = domain
	}
	// Unknown ids: the fixed probes, the next id after each namespace's largest, and up to 10 ids of every other namespace
	// that this namespace does not have, so a lookup in the wrong namespace is checked to stay unknown.
	for _, k := range masterDisplayKinds {
		probe := map[uint64]bool{}
		for _, p := range masterDisplayProbes {
			if !sets[k.kind][p] {
				probe[p] = true
			}
		}
		if domain := domains[k.kind]; len(domain) > 0 && domain[len(domain)-1] < math.MaxUint64 {
			probe[domain[len(domain)-1]+1] = true
		}
		for _, other := range masterDisplayKinds {
			if other.kind == k.kind {
				continue
			}
			taken := 0
			for _, id := range domains[other.kind] {
				if taken == 10 {
					break
				}
				if !sets[k.kind][id] {
					probe[id] = true
					taken++
				}
			}
		}
		list := make([]uint64, 0, len(probe))
		for id := range probe {
			list = append(list, id)
		}
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		probes[k.kind] = list
	}
	return domains, probes
}

func masterDisplayAnswer(name DisplayName, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return name.Status + "|" + name.Locale + "|" + name.Name
}

func masterModeAnswer(mode InstanceMode, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return string(mode.Kind) + "|" + mode.Status + "|" + mode.Locale + "|" + mode.Basis + "|" + mode.Name
}

func compareMasterDisplay(oldResolver, newResolver *DisplayResolver, domains, probes map[string][]uint64, locales []string, d *masterDifferences) map[string]int {
	ctx := context.Background()
	checked := map[string]int{}
	for _, k := range masterDisplayKinds {
		ids := append(append([]uint64(nil), domains[k.kind]...), probes[k.kind]...)
		for _, locale := range locales {
			for _, id := range ids {
				oldName, oldErr := oldResolver.Resolve(ctx, k.kind, id, locale)
				newName, newErr := newResolver.Resolve(ctx, k.kind, id, locale)
				checked["resolve"]++
				if a, b := masterDisplayAnswer(oldName, oldErr), masterDisplayAnswer(newName, newErr); a != b {
					d.add(masterDifference{Layer: "display", Class: "name-changed", Object: k.kind, Locale: locale, Key: strconv.FormatUint(id, 10), Old: a, New: b})
				}
			}
		}
		if k.kind == "role" {
			for _, id := range ids {
				checked["role-element"]++
				if a, b := oldResolver.RoleElement(ctx, id), newResolver.RoleElement(ctx, id); a != b {
					d.add(masterDifference{Layer: "display", Class: "role-element-changed", Object: "role", Key: strconv.FormatUint(id, 10), Old: strconv.FormatUint(a, 10), New: strconv.FormatUint(b, 10)})
				}
				checked["static-role"]++
				if a, b := oldResolver.StaticRole(ctx, id), newResolver.StaticRole(ctx, id); a != b {
					d.add(masterDifference{Layer: "display", Class: "static-role-changed", Object: "role", Key: strconv.FormatUint(id, 10), Old: strconv.FormatUint(a, 10), New: strconv.FormatUint(b, 10)})
				}
			}
		}
		if k.kind == "instance" {
			for _, locale := range locales {
				for _, id := range ids {
					checked["instance-mode"]++
					oldMode, oldErr := oldResolver.InstanceMode(ctx, id, locale)
					newMode, newErr := newResolver.InstanceMode(ctx, id, locale)
					if a, b := masterModeAnswer(oldMode, oldErr), masterModeAnswer(newMode, newErr); a != b {
						d.add(masterDifference{Layer: "display", Class: "instance-mode-changed", Object: "instance", Locale: locale, Key: strconv.FormatUint(id, 10), Old: a, New: b})
					}
					checked["instance-stage"]++
					oldStage, oldErr := oldResolver.InstanceStage(ctx, id, locale)
					newStage, newErr := newResolver.InstanceStage(ctx, id, locale)
					if a, b := masterDisplayAnswer(oldStage, oldErr), masterDisplayAnswer(newStage, newErr); a != b {
						d.add(masterDifference{Layer: "display", Class: "instance-stage-changed", Object: "instance", Locale: locale, Key: strconv.FormatUint(id, 10), Old: a, New: b})
					}
				}
			}
		}
	}
	return checked
}

// --- run ---

type masterEquivalenceOptions struct {
	Layers       []string
	Locales      []string
	Expectations []masterExpectation
}

// masterExpectation declares one planned structural difference. Only the schema and data layers can be declared.
type masterExpectation struct {
	Layer  string `json:"layer"`
	Class  string `json:"class"`
	Object string `json:"object"`
	Reason string `json:"reason,omitempty"`
}

type masterFileInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type masterEquivalenceReport struct {
	Old             masterFileInfo      `json:"old"`
	New             masterFileInfo      `json:"new"`
	Layers          []string            `json:"layers"`
	Locales         []string            `json:"locales"`
	NonSemantic     []string            `json:"nonSemanticExclusions"`
	Checked         map[string]int      `json:"checked"`
	Counts          map[string]int      `json:"counts"`
	Objects         map[string]int      `json:"objects"`
	Samples         []masterDifference  `json:"samples"`
	Expected        []masterExpectation `json:"expected,omitempty"`
	Unexpected      []string            `json:"unexpected,omitempty"`
	ExpectedNotSeen []string            `json:"expectedNotSeen,omitempty"`
	LoadErrors      []string            `json:"loadErrors,omitempty"`
	Equivalent      bool                `json:"equivalent"`
	SemanticDigest  string              `json:"semanticDigest,omitempty"`
}

func defaultMasterEquivalenceOptions() masterEquivalenceOptions {
	return masterEquivalenceOptions{Layers: []string{"schema", "data", "loaded", "display"}, Locales: append([]string(nil), masterEquivalenceLocales...)}
}

func masterFileFacts(path string) (masterFileInfo, error) {
	info := masterFileInfo{Path: path}
	stat, err := os.Stat(path)
	if err != nil {
		return info, err
	}
	info.Bytes = stat.Size()
	info.SHA256, err = hashFile(path)
	return info, err
}

func validateMasterExpectations(expectations []masterExpectation) error {
	for _, e := range expectations {
		if e.Layer != "schema" && e.Layer != "data" {
			return fmt.Errorf("expectation %s/%s/%s: only schema and data differences can be declared; loaded and display differences are never expected", e.Layer, e.Class, e.Object)
		}
		if e.Class == "" || e.Object == "" {
			return fmt.Errorf("expectation %+v names no class or object", e)
		}
	}
	return nil
}

// runMasterEquivalence compares two Master files. Both are opened read-only through the product's own file opener.
func runMasterEquivalence(oldPath, newPath string, options masterEquivalenceOptions) (masterEquivalenceReport, error) {
	report := masterEquivalenceReport{Layers: options.Layers, Locales: options.Locales, Checked: map[string]int{}, Expected: options.Expectations,
		NonSemantic: []string{"GameMaster.DatabasePath", "GameMaster.Manifest.SourceHash", "DamageMasterRecord.SourceHash"}}
	if err := validateMasterExpectations(options.Expectations); err != nil {
		return report, err
	}
	var err error
	if report.Old, err = masterFileFacts(oldPath); err != nil {
		return report, err
	}
	if report.New, err = masterFileFacts(newPath); err != nil {
		return report, err
	}
	layers := map[string]bool{}
	for _, l := range options.Layers {
		layers[l] = true
	}
	d := newMasterDifferences()
	oldDB, _, _, err := openFileMasterDatabase(oldPath)
	if err != nil {
		return report, fmt.Errorf("open old Master: %w", err)
	}
	defer oldDB.Close()
	newDB, _, _, err := openFileMasterDatabase(newPath)
	if err != nil {
		return report, fmt.Errorf("open new Master: %w", err)
	}
	defer newDB.Close()
	if layers["schema"] || layers["data"] {
		oldInfo, err := readMasterSchemaInfo(oldDB)
		if err != nil {
			return report, fmt.Errorf("read old schema: %w", err)
		}
		newInfo, err := readMasterSchemaInfo(newDB)
		if err != nil {
			return report, fmt.Errorf("read new schema: %w", err)
		}
		schemaDiffs := d
		if !layers["schema"] {
			schemaDiffs = newMasterDifferences()
		}
		common := compareMasterSchemas(oldInfo, newInfo, schemaDiffs)
		report.Checked["tables"] = len(oldInfo.Tables)
		if layers["data"] {
			for _, table := range sortedMasterKeys(common) {
				rows, err := compareMasterTableData(oldDB, newDB, table, common[table], oldInfo.Tables[table], d)
				if err != nil {
					return report, err
				}
				report.Checked["rows"] += rows
				report.Checked["columns"] += len(common[table])
			}
		}
	}
	semantic := sha256.New()
	if layers["loaded"] {
		for _, locale := range options.Locales {
			oldMaster, oldErr := LoadGameMasterDatabase(oldPath, locale)
			newMaster, newErr := LoadGameMasterDatabase(newPath, locale)
			if oldErr != nil || newErr != nil {
				old, nw := "", ""
				if oldErr != nil {
					old = oldErr.Error()
				}
				if newErr != nil {
					nw = newErr.Error()
				}
				if old != nw || old != "" {
					d.add(masterDifference{Layer: "loaded", Class: "load-error", Object: "LoadGameMasterDatabase", Locale: locale, Old: old, New: nw})
					report.LoadErrors = append(report.LoadErrors, fmt.Sprintf("%s: old=%q new=%q", locale, old, nw))
				}
				continue
			}
			oldEntries := masterLoadedEntries(oldMaster)
			newEntries := masterLoadedEntries(newMaster)
			report.Checked["loaded-entries"] += compareMasterLoaded(oldEntries, newEntries, locale, d)
			writeMasterSemanticDigest(semantic, locale, newEntries)
		}
	}
	if layers["display"] {
		domains, probes := masterDisplayDomains(oldDB, newDB)
		for kind, ids := range domains {
			report.Checked["display-ids:"+kind] = len(ids)
			report.Checked["display-probes:"+kind] = len(probes[kind])
		}
		oldResolver, err := OpenDisplayResolver(oldPath)
		if err != nil {
			return report, fmt.Errorf("open old display resolver: %w", err)
		}
		defer oldResolver.Close()
		newResolver, err := OpenDisplayResolver(newPath)
		if err != nil {
			return report, fmt.Errorf("open new display resolver: %w", err)
		}
		defer newResolver.Close()
		for k, v := range compareMasterDisplay(oldResolver, newResolver, domains, probes, options.Locales, d) {
			report.Checked["display-"+k] = v
		}
	}
	if layers["loaded"] && len(report.LoadErrors) == 0 {
		report.SemanticDigest = hex.EncodeToString(semantic.Sum(nil))
	}
	report.Counts, report.Objects, report.Samples = d.counts, d.objects, d.samples
	report.Unexpected, report.ExpectedNotSeen = masterUnexpected(d, options.Expectations)
	report.Equivalent = len(report.Unexpected) == 0 && len(report.ExpectedNotSeen) == 0
	return report, nil
}

// writeMasterSemanticDigest hashes the loaded entries of one locale in a fixed order. Unlike TestMasterLookupDigest it does
// not hash the DDL, so it stays equal across a structural change that keeps the loaded Master the same.
func writeMasterSemanticDigest(h interface{ Write([]byte) (int, error) }, locale string, entries map[string]map[string]string) {
	for _, field := range sortedMasterKeys(entries) {
		for _, key := range sortedMasterKeys(entries[field]) {
			fmt.Fprintf(h, "%s\t%s\t%s\t%s\n", locale, field, key, entries[field][key])
		}
	}
}

// masterUnexpected lists the differences no expectation declares and the expectations that did not happen.
func masterUnexpected(d *masterDifferences, expectations []masterExpectation) (unexpected, notSeen []string) {
	declared := map[string]bool{}
	for _, e := range expectations {
		declared[e.Layer+"/"+e.Class+"/"+e.Object] = true
	}
	for _, key := range sortedMasterKeys(d.objects) {
		if !declared[key] {
			unexpected = append(unexpected, fmt.Sprintf("%s (%d)", key, d.objects[key]))
		}
	}
	for _, e := range expectations {
		key := e.Layer + "/" + e.Class + "/" + e.Object
		if d.objects[key] == 0 {
			notSeen = append(notSeen, key)
		}
	}
	return unexpected, notSeen
}

func masterEquivalenceSummary(r masterEquivalenceReport) string {
	keys := sortedMasterKeys(r.Counts)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, r.Counts[k])
	}
	if len(parts) == 0 {
		parts = []string{"no differences"}
	}
	return strings.Join(parts, ", ")
}

func writeMasterEquivalenceReport(path string, report masterEquivalenceReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readMasterExpectations(path string) ([]masterExpectation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Allow []masterExpectation `json:"allow"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse expectations: %w", err)
	}
	return file.Allow, validateMasterExpectations(file.Allow)
}

// TestMasterEquivalenceCollector is a collector, not a regular test: it compares two Master files with every layer and
// fails unless they are equivalent. It reads only the paths the caller supplies:
//
//	WWLOGS_MASTER_EQUIVALENCE_OLD      the Master the product uses now (for example the v0.8.0 baseline copy)
//	WWLOGS_MASTER_EQUIVALENCE_NEW      the candidate Master
//	WWLOGS_MASTER_EQUIVALENCE_OUT      output directory for master-equivalence.json
//	WWLOGS_MASTER_EQUIVALENCE_EXPECT   optional JSON {"allow":[{"layer","class","object","reason"}]} of planned schema or
//	                                   data differences; each must occur, and none can cover loaded or display layers
//	WWLOGS_MASTER_EQUIVALENCE_LOCALES  optional comma-separated subset of the Master locales (default: all five)
//	WWLOGS_MASTER_EQUIVALENCE_MODE     "report" writes the report without failing (for reviewing a game update's delta)
func TestMasterEquivalenceCollector(t *testing.T) {
	oldPath, newPath, out := os.Getenv("WWLOGS_MASTER_EQUIVALENCE_OLD"), os.Getenv("WWLOGS_MASTER_EQUIVALENCE_NEW"), os.Getenv("WWLOGS_MASTER_EQUIVALENCE_OUT")
	if oldPath == "" || newPath == "" || out == "" {
		t.Skip("set WWLOGS_MASTER_EQUIVALENCE_OLD, _NEW and _OUT to compare two Master files")
	}
	options := defaultMasterEquivalenceOptions()
	if locales := os.Getenv("WWLOGS_MASTER_EQUIVALENCE_LOCALES"); locales != "" {
		options.Locales = strings.Split(locales, ",")
		for _, locale := range options.Locales {
			if _, ok := supportedMasterLocales[locale]; !ok {
				t.Fatalf("unsupported Master locale %q", locale)
			}
		}
	}
	if path := os.Getenv("WWLOGS_MASTER_EQUIVALENCE_EXPECT"); path != "" {
		expectations, err := readMasterExpectations(path)
		if err != nil {
			t.Fatal(err)
		}
		options.Expectations = expectations
	}
	report, err := runMasterEquivalence(oldPath, newPath, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMasterEquivalenceReport(filepath.Join(out, "master-equivalence.json"), report); err != nil {
		t.Fatal(err)
	}
	t.Logf("checked %v; %s", report.Checked, masterEquivalenceSummary(report))
	if os.Getenv("WWLOGS_MASTER_EQUIVALENCE_MODE") == "report" {
		return
	}
	if !report.Equivalent {
		t.Fatalf("the Masters are not equivalent: unexpected %v; expected but absent %v", report.Unexpected, report.ExpectedNotSeen)
	}
}
