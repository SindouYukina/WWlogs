package capture

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The runtime schema of the combat Master: every table and view this runtime reads, with every column it reads and the
// declared types it accepts. It is the same in both build profiles, which load one Master through one loader (the
// Development tables are read by the shared loader too, and only Development outputs use their values). Before a Master is
// loaded or a display resolver reads it, checkMasterRuntimeSchema compares the file with this declaration and refuses a
// file that lacks any of it, so no capability is ever loaded half: the loader and the resolver then query these objects
// directly, without asking whether they exist. Everything else in the file is not read by the runtime.
//
// What the declaration does not cover is checked elsewhere: the row-level rules a load enforces as it reads (a parent role,
// skin owner, map monster or template a relation names must exist; identifiers must parse; the core definitions must not
// be empty) fail the load; the full model of the file (every column, key and index, ID namespaces, value rules and
// references) is the maintainer's private Master contract, checked in Maintainer verification, where the two declarations
// are also compared (TestMasterRuntimeSchemaAgreesWithTheContract).
//
// Changing the declaration (a table, column, accepted type or key) changes its fingerprint and needs a new
// masterRuntimeSchemaVersion: TestMasterRuntimeSchemaVersionNamesItsDeclaration pins one to the other. Removing a table the
// declaration does not list needs neither. A column type migration lists the old and the new type while the current and
// the candidate Master are compared under one runtime, then only the new one.
//
// Version 2 (v0.9.0 M2.5-B3-2) adds the content model the display resolver reads (content_system, content_entry,
// content_entry_locale, content_context), each with the key its lookups rely on, and drops the four tables of the two
// display rules the content model replaced (the ShipTower stage rule and the Tactical Hologram title fallback): the runtime
// no longer reads them, though the Master still holds them.
//
// Version 3 (v0.9.0 M4) declares that the runtime reads roleinfo's rowid: the damage owners list the roles of a skill
// group in roleinfo's storage order, so roleinfo must stay a rowid table while most other tables are WITHOUT ROWID.

// masterRuntimeSchemaVersion identifies the declaration below.
const masterRuntimeSchemaVersion = 3

// MasterSchemaTable is a table or view of the combat Master the runtime reads, with the columns it reads.
type MasterSchemaTable struct {
	Name string
	View bool
	// Scope is "product" when core or display code uses the values and "development" when only Development outputs do.
	// Both profiles load every table, so both require each one.
	Scope   string
	Columns []MasterSchemaColumn
	// Key, when set, is the primary key or a unique index over exactly these columns in this order that the runtime relies
	// on: a lookup by the key reads at most one row, and a lookup by its leading columns searches the key, not the table.
	Key []string
	// Rowid is set when the runtime reads the table's rowid, its storage order: the table must not be WITHOUT ROWID.
	Rowid bool
}

// MasterSchemaColumn is a column the runtime reads and the declared types it accepts. Without a type any is accepted: the
// runtime reads only the column's name, or the column is a view's computed column.
type MasterSchemaColumn struct {
	Name  string
	Types []string
}

// masterTable declares a table whose values the product uses; each column is "name TYPE", "name TYPE|TYPE" or "name *".
func masterTable(name string, columns ...string) MasterSchemaTable {
	return masterSchemaObject(name, false, "product", columns)
}

// masterKeyedTable declares a product table with the key its lookups rely on.
func masterKeyedTable(name string, key []string, columns ...string) MasterSchemaTable {
	table := masterSchemaObject(name, false, "product", columns)
	declared := map[string]bool{}
	for _, column := range table.Columns {
		declared[column.Name] = true
	}
	for _, column := range key {
		if !declared[column] {
			panic("master runtime schema: key column " + name + "." + column + " is not a declared column")
		}
	}
	table.Key = append([]string(nil), key...)
	return table
}

// masterRowidTable declares a product table whose rowid the runtime reads as the storage order of its rows.
func masterRowidTable(name string, columns ...string) MasterSchemaTable {
	table := masterSchemaObject(name, false, "product", columns)
	table.Rowid = true
	return table
}

// masterDevelopmentTable declares a table whose values only Development outputs use.
func masterDevelopmentTable(name string, columns ...string) MasterSchemaTable {
	return masterSchemaObject(name, false, "development", columns)
}

// masterView declares a view by its column names.
func masterView(name string, columns ...string) MasterSchemaTable {
	declared := make([]string, len(columns))
	for i, column := range columns {
		declared[i] = column + " *"
	}
	return masterSchemaObject(name, true, "product", declared)
}

func masterSchemaObject(name string, view bool, scope string, columns []string) MasterSchemaTable {
	table := MasterSchemaTable{Name: name, View: view, Scope: scope}
	for _, column := range columns {
		columnName, types, ok := strings.Cut(column, " ")
		if !ok || columnName == "" || types == "" {
			panic("master runtime schema: malformed column " + name + "." + column)
		}
		declared := MasterSchemaColumn{Name: columnName}
		if types != "*" {
			declared.Types = strings.Split(types, "|")
		}
		table.Columns = append(table.Columns, declared)
	}
	return table
}

// masterRuntimeSchema lists the objects in the order the loader reads them.
var masterRuntimeSchema = []MasterSchemaTable{
	masterRowidTable("roleinfo", "id TEXT", "skin_id TEXT", "element_id TEXT", "skill_id TEXT"),
	masterTable("roleinfo_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("trialroleinfo", "id TEXT", "parent_role_id TEXT"),
	masterTable("roleskin", "id TEXT", "role_id TEXT", "mesh_id TEXT"),
	masterTable("phantomskill", "phantom_skill_id TEXT"),
	masterTable("vision", "id TEXT", "skill_id TEXT", "birth_cast_skill_id TEXT", "dev_note_zh_cn TEXT"),
	masterTable("buff", "id TEXT", "duration_policy INTEGER", "stack_limit_count INTEGER", "game_attribute_id TEXT", "extra_effect_id TEXT", "period INTEGER", "b_execute_periodic_effect_on_application INTEGER", "b_clear_stack_on_overflow INTEGER"),
	masterTable("buff_duration_magnitude", "owner_id TEXT", "ordinal INTEGER", "value REAL"),
	masterTable("buff_granted_tags", "owner_id TEXT", "ordinal INTEGER", "value TEXT"),
	masterTable("buff_modifier_magnitude", "owner_id TEXT"),
	masterTable("buff_gameplay_cue_ids", "owner_id TEXT", "ordinal INTEGER", "value TEXT"),
	masterTable("gameplaycue", "id TEXT", "cue_type INTEGER", "path_asset_id TEXT"),
	masterTable("gameplaycue_parameters", "owner_id TEXT", "ordinal INTEGER", "value TEXT"),
	masterTable("buff_term_ref", "buff_id TEXT", "term_id TEXT", "evidence_status TEXT"),
	masterTable("term_locale", "id TEXT", "locale TEXT", "name TEXT", "description TEXT"),
	masterDevelopmentTable("shield", "id TEXT"),
	masterTable("commonbullet", "id TEXT", "dev_name_zh_cn TEXT", "damage_id TEXT"),
	masterTable("bulletpreload", "actor_asset_id TEXT", "bullet_id TEXT"),
	masterTable("modelconfigpreload", "id TEXT", "actor_asset_id TEXT"),
	masterTable("blueprintconfig", "blueprint_type TEXT", "model_id TEXT", "entity_type TEXT"),
	masterTable("summoncfg", "id TEXT", "blueprint_type TEXT"),
	masterTable("entityskillpreload", "actor_asset_id TEXT", "skill_id TEXT", "is_common INTEGER"),
	masterTable("asset_reference", "id TEXT", "path TEXT"),
	masterTable("skillinfo", "skill_id TEXT", "dev_name_zh_cn TEXT", "skill_genre_raw INTEGER"),
	masterTable("abnormaldamageconfig", "abnormal1001 *", "abnormal1002 *", "abnormal1003 *", "abnormal1004 *", "abnormal1005 *", "abnormal1006 *"),
	masterTable("damage", "id TEXT", "calculate_type INTEGER", "element INTEGER", "formula_type INTEGER", "type INTEGER"),
	masterTable("buff_extra_effect_parameters", "owner_id TEXT", "ordinal INTEGER", "value TEXT"),
	masterDevelopmentTable("propertyindex", "id TEXT", "key TEXT"),
	masterDevelopmentTable("propertyindex_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterDevelopmentTable("aistatemachine", "id TEXT", "version INTEGER"),
	masterDevelopmentTable("aistatemachine_node", "machine_id TEXT", "uuid INTEGER", "dev_name_zh_cn TEXT", "reference_uuid INTEGER"),
	masterDevelopmentTable("aistatemachine_node_child", "machine_id TEXT", "uuid INTEGER", "ordinal INTEGER", "child_uuid INTEGER"),
	masterDevelopmentTable("aistatemachine_node_name_locale", "dev_name_zh_cn TEXT", "locale TEXT", "label TEXT", "label_kind TEXT"),
	masterDevelopmentTable("resonantchain_buff_ids", "owner_id TEXT", "value TEXT"),
	masterDevelopmentTable("resonantchain_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterDevelopmentTable("phantomfetter_buff_ids", "owner_id TEXT", "value TEXT"),
	masterDevelopmentTable("phantomfetter_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterDevelopmentTable("weaponreson_effect", "owner_id TEXT", "value INTEGER"),
	masterDevelopmentTable("weaponreson_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterDevelopmentTable("skill_buff_ref", "skill_id TEXT", "buff_id TEXT"),
	masterDevelopmentTable("role_buff_observation", "role_id TEXT", "buff_id TEXT"),
	masterDevelopmentTable("skill_dev_label_locale", "skill_id TEXT", "locale TEXT", "label TEXT", "label_kind TEXT"),
	masterDevelopmentTable("dev_label_locale", "namespace TEXT", "id TEXT", "locale TEXT", "label TEXT", "label_kind TEXT"),
	masterDevelopmentTable("elementinfo_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("monsterinfo", "id TEXT"),
	masterTable("monsterinfo_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("skilldescription", "id TEXT", "skill_level_group_id TEXT"),
	masterTable("skilldescription_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("skill", "id TEXT", "skill_group_id TEXT", "skill_level_group_id TEXT", "skill_type INTEGER"),
	masterTable("skill_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("role_skill_group_ref", "role_id TEXT", "skill_group_id TEXT"),
	masterTable("skill_damage_ref", "skill_id TEXT", "damage_id TEXT"),
	masterDevelopmentTable("commonskillpreload", "id TEXT"),
	masterTable("phantomitem", "id TEXT", "skill_id TEXT", "monster_id TEXT", "parent_monster_id TEXT"),
	masterTable("phantomitem_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("map_instance", "instance_id TEXT", "map_id TEXT"),
	masterTable("map_entity_monster", "map_id TEXT", "entity_id TEXT", "monster_id TEXT"),
	masterTable("templateconfig", "id TEXT", "blueprint_type TEXT", "raw_name TEXT", "tid_name TEXT"),
	masterTable("templateconfig_locale", "id TEXT", "locale TEXT", "name TEXT", "name_text_key TEXT", "subtitle TEXT"),
	masterTable("map_entity_template_ref", "map_id TEXT", "entity_id TEXT", "ordinal INTEGER", "template_id TEXT"),
	masterTable("prefab_entity_monster", "prefab_id TEXT", "entity_id TEXT", "monster_id TEXT"),
	masterTable("prefab_entity_template_ref", "prefab_id TEXT", "entity_id TEXT", "ordinal INTEGER", "template_id TEXT"),
	masterTable("instancedungeon", "id TEXT", "inst_type INTEGER"),
	masterTable("instancedungeon_locale", "id TEXT", "locale TEXT", "name TEXT"),
	masterTable("secondaryguidedata_locale", "id TEXT", "locale TEXT", "text TEXT"),
	masterTable("dungeondetection", "dungeon_id TEXT", "secondary TEXT", "sub_dungeon_id TEXT", "jump_with_sub_dungeon_id INTEGER", "instance_dungeon_entrance_id TEXT"),
	masterTable("silentareadetection", "dungeon_id TEXT", "secondary TEXT", "instance_dungeon_entrance_id TEXT"),
	masterTable("instancedungeonentrance_instance_ref", "entrance_id TEXT", "instance_dungeon_id TEXT"),
	// The content model (content_display.go): a context's system and entries, an entry's name state, and its names.
	masterKeyedTable("content_system", []string{"id"}, "id TEXT", "guide_category_id TEXT", "name_status TEXT"),
	masterKeyedTable("content_entry", []string{"id"}, "id TEXT", "name_status TEXT"),
	masterKeyedTable("content_entry_locale", []string{"id", "locale"}, "id TEXT", "locale TEXT", "name TEXT"),
	masterKeyedTable("content_context", []string{"instance_id", "system_id", "ordinal"}, "instance_id TEXT", "system_id TEXT", "ordinal INTEGER",
		"activity TEXT", "season TEXT", "cycle TEXT", "series TEXT", "stage TEXT", "difficulty TEXT", "mode TEXT", "role_id TEXT"),
	masterView("summon_skill_vision_ref", "summon_type", "summon_skill_id", "vision_id", "phantom_skill_id"),
}

// MasterRuntimeSchema returns a copy of the runtime schema: the structure a combat Master must provide to this runtime.
func MasterRuntimeSchema() []MasterSchemaTable {
	schema := make([]MasterSchemaTable, len(masterRuntimeSchema))
	for i, table := range masterRuntimeSchema {
		table.Columns = append([]MasterSchemaColumn(nil), table.Columns...)
		for j := range table.Columns {
			table.Columns[j].Types = append([]string(nil), table.Columns[j].Types...)
		}
		if table.Key != nil {
			table.Key = append([]string(nil), table.Key...)
		}
		schema[i] = table
	}
	return schema
}

// masterRuntimeSchemaFingerprint is the SHA-256 of the declaration in a fixed text form. A table without a key or a rowid
// requirement hashes as it did before they were declared.
func masterRuntimeSchemaFingerprint() string {
	hash := sha256.New()
	for _, table := range masterRuntimeSchema {
		kind := "table"
		if table.View {
			kind = "view"
		}
		fmt.Fprintf(hash, "%s %s %s\n", kind, table.Name, table.Scope)
		for _, column := range table.Columns {
			fmt.Fprintf(hash, "\t%s %s\n", column.Name, strings.Join(column.Types, "|"))
		}
		if len(table.Key) > 0 {
			fmt.Fprintf(hash, "\tkey %s\n", strings.Join(table.Key, ","))
		}
		if table.Rowid {
			fmt.Fprintf(hash, "\trowid\n")
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// errMasterRuntimeSchema marks a Master that lacks part of the runtime schema.
var errMasterRuntimeSchema = errors.New("the combat Master database does not have the structure this version of WWlogs reads")

// checkMasterRuntimeSchema compares a Master with the runtime schema and names every difference: a missing table or view,
// an object of the other kind, a missing column, a column declared with a type the runtime does not read, a missing key,
// a WITHOUT ROWID table whose rowid the runtime reads, and a view the database cannot compile. It reads only the schema,
// never a row.
func checkMasterRuntimeSchema(db *sql.DB) error {
	kinds := map[string]string{}
	rows, err := db.Query("SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view')")
	if err != nil {
		return fmt.Errorf("read the combat Master database structure: %w", err)
	}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			rows.Close()
			return fmt.Errorf("read the combat Master database structure: %w", err)
		}
		kinds[name] = kind
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read the combat Master database structure: %w", err)
	}
	var problems []string
	for _, table := range masterRuntimeSchema {
		want := "table"
		if table.View {
			want = "view"
		}
		switch kind, ok := kinds[table.Name]; {
		case !ok:
			problems = append(problems, "missing "+want+" "+table.Name)
			continue
		case kind != want:
			problems = append(problems, table.Name+" is a "+kind+", not a "+want)
			continue
		}
		declared, err := masterDeclaredColumns(db, table.Name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s %s cannot be read: %v", want, table.Name, err))
			continue
		}
		for _, column := range table.Columns {
			declaredType, ok := declared[column.Name]
			if !ok {
				problems = append(problems, "missing column "+table.Name+"."+column.Name)
				continue
			}
			if len(column.Types) > 0 && !masterTypeAccepted(column.Types, declaredType) {
				problems = append(problems, fmt.Sprintf("column %s.%s is declared %q, this runtime reads %s", table.Name, column.Name, declaredType, strings.Join(column.Types, " or ")))
			}
		}
		if table.Rowid {
			var withoutRowid bool
			if err := db.QueryRow(`SELECT wr FROM pragma_table_list(?)`, table.Name).Scan(&withoutRowid); err != nil {
				problems = append(problems, fmt.Sprintf("the layout of table %s cannot be read: %v", table.Name, err))
			} else if withoutRowid {
				problems = append(problems, "table "+table.Name+" is WITHOUT ROWID; this runtime reads its rowid")
			}
		}
		if len(table.Key) == 0 {
			continue
		}
		keys, err := masterUniqueKeys(db, table.Name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("the keys of table %s cannot be read: %v", table.Name, err))
			continue
		}
		if !slices.ContainsFunc(keys, func(key []string) bool { return slices.Equal(key, table.Key) }) {
			problems = append(problems, fmt.Sprintf("missing key %s(%s)", table.Name, strings.Join(table.Key, ", ")))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w (runtime schema %d): %s", errMasterRuntimeSchema, masterRuntimeSchemaVersion, strings.Join(problems, "; "))
	}
	return nil
}

func masterTypeAccepted(accepted []string, declared string) bool {
	for _, t := range accepted {
		if strings.EqualFold(t, declared) {
			return true
		}
	}
	return false
}

// masterUniqueKeys reads the unique keys of a table, each as its columns in order: the primary key, and every unique index
// that covers the whole table (a partial index does not, and an expression index has no column list).
func masterUniqueKeys(db *sql.DB, table string) ([][]string, error) {
	var keys [][]string
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
	if err != nil {
		return nil, err
	}
	var primary []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			rows.Close()
			return nil, err
		}
		primary = append(primary, column)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(primary) > 0 {
		keys = append(keys, primary)
	}
	rows, err = db.Query(`SELECT name FROM pragma_index_list(?) WHERE "unique" = 1 AND partial = 0`, table)
	if err != nil {
		return nil, err
	}
	var indexes []string
	for rows.Next() {
		var index string
		if err := rows.Scan(&index); err != nil {
			rows.Close()
			return nil, err
		}
		indexes = append(indexes, index)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, index := range indexes {
		rows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, index)
		if err != nil {
			return nil, err
		}
		var columns []string
		complete := true
		for rows.Next() {
			var column sql.NullString
			if err := rows.Scan(&column); err != nil {
				rows.Close()
				return nil, err
			}
			complete = complete && column.Valid
			columns = append(columns, column.String)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if complete && len(columns) > 0 {
			keys = append(keys, columns)
		}
	}
	return keys, nil
}

// masterDeclaredColumns reads a table's or view's columns and declared types. For a view SQLite compiles it, so a view
// whose base table or column is gone is an error here.
func masterDeclaredColumns(db *sql.DB, name string) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, type FROM pragma_table_info(?)`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var column, declared string
		if err := rows.Scan(&column, &declared); err != nil {
			return nil, err
		}
		columns[column] = declared
	}
	return columns, rows.Err()
}
