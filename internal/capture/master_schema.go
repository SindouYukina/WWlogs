package capture

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The runtime schema of the combat Master: every table and view this runtime reads, with every column it reads and the
// declared types it accepts. It is the same in both build profiles, which load one Master through one loader (the
// Development tables are read by the shared loader too, and only Development outputs use their values).
// checkMasterRuntimeSchema compares a file with this declaration and names every part of it the file lacks. Everything
// else in the file is not read by the runtime.
//
// What the declaration does not cover is checked elsewhere: the row-level rules a load enforces as it reads (a parent role,
// skin owner, map monster or template a relation names must exist; identifiers must parse; the core definitions must not
// be empty) fail the load; the full model of the file (every column, key and index, ID namespaces, value rules and
// references) is the maintainer's private Master contract, checked in Maintainer verification, where the two declarations
// are also compared (TestMasterRuntimeSchemaAgreesWithTheContract).
//
// Changing the declaration (a table, column or accepted type) changes its fingerprint and needs a new
// masterRuntimeSchemaVersion: TestMasterRuntimeSchemaVersionNamesItsDeclaration pins one to the other. Removing a table the
// declaration does not list needs neither. A column type migration lists the old and the new type while the current and
// the candidate Master are compared under one runtime, then only the new one.

// masterRuntimeSchemaVersion identifies the declaration below.
const masterRuntimeSchemaVersion = 1

// MasterSchemaTable is a table or view of the combat Master the runtime reads, with the columns it reads.
type MasterSchemaTable struct {
	Name string
	View bool
	// Scope is "product" when core or display code uses the values and "development" when only Development outputs do.
	// Both profiles load every table, so both require each one.
	Scope   string
	Columns []MasterSchemaColumn
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
	masterTable("roleinfo", "id TEXT", "skin_id TEXT", "element_id TEXT", "skill_id TEXT"),
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
	masterTable("slashtowerstageinfo", "inst_id TEXT"),
	masterTable("slashandtowercfg_instance_ref", "instance_dungeon_id TEXT"),
	masterTable("lordgymentranceset", "id TEXT", "dungeon_id TEXT"),
	masterTable("lordgymentranceset_locale", "id TEXT", "locale TEXT", "title TEXT"),
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
		schema[i] = table
	}
	return schema
}

// masterRuntimeSchemaFingerprint is the SHA-256 of the declaration in a fixed text form.
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
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// errMasterRuntimeSchema marks a Master that lacks part of the runtime schema.
var errMasterRuntimeSchema = errors.New("the combat Master database does not have the structure this version of WWlogs reads")

// checkMasterRuntimeSchema compares a Master with the runtime schema and names every difference: a missing table or view,
// an object of the other kind, a missing column, a column declared with a type the runtime does not read, and a view the
// database cannot compile. It reads only the schema, never a row.
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
