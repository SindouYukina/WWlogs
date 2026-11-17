package capture

import (
	"database/sql"
	"fmt"
)

// propertyName is one PropertyIndex row: the attribute key used by the client and its localized display name.
type propertyName struct {
	Key  string
	Name string
}

// fsmNodeKey identifies a node inside one state-machine config; node uuids are only unique per machine.
type fsmNodeKey struct {
	machine string
	uuid    int64
}

// fsmMaster indexes the AI state-machine node identities (not their behaviour logic).
type fsmMaster struct {
	machinesByNode    map[int64][]string // node uuid -> machines that define it
	machinesByVersion map[int64][]string // config Version -> machines (EntityFsmComponentPb.HashCode)
	names             map[fsmNodeKey]string
	children          map[fsmNodeKey][]int64
	references        map[fsmNodeKey]int64
	// labels holds the localized developer node names by zh-CN name: the master locale and the zh-CN original.
	labels map[string]map[string]localizedSkillLabel
}

// loadSQLitePropertyNames reads PropertyIndex keys and the master-locale names; the tables are optional.
func loadSQLitePropertyNames(db *sql.DB, locale string, master *gameMaster) error {
	exists, err := masterTableExists(db, "propertyindex")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query(`SELECT CAST(p.id AS INTEGER),p.key,COALESCE(l.name,'') FROM propertyindex p
		LEFT JOIN propertyindex_locale l ON l.id=p.id AND l.locale=? ORDER BY CAST(p.id AS INTEGER)`, locale)
	if err != nil {
		return fmt.Errorf("query PropertyIndex names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var entry propertyName
		if err := rows.Scan(&id, &entry.Key, &entry.Name); err != nil {
			return fmt.Errorf("read PropertyIndex name: %w", err)
		}
		master.propertyNames[id] = entry
	}
	return rows.Err()
}

// loadSQLiteStateMachines reads the state-machine node identities and their containment; the tables are optional.
func loadSQLiteStateMachines(db *sql.DB, locale string, master *gameMaster) error {
	exists, err := masterTableExists(db, "aistatemachine_node")
	if err != nil || !exists {
		return err
	}
	fsm := &fsmMaster{
		machinesByNode:    make(map[int64][]string),
		machinesByVersion: make(map[int64][]string),
		names:             make(map[fsmNodeKey]string),
		children:          make(map[fsmNodeKey][]int64),
		references:        make(map[fsmNodeKey]int64),
		labels:            make(map[string]map[string]localizedSkillLabel),
	}
	versions, err := db.Query("SELECT id,version FROM aistatemachine ORDER BY id")
	if err != nil {
		return fmt.Errorf("query state-machine versions: %w", err)
	}
	for versions.Next() {
		var machine string
		var version int64
		if err := versions.Scan(&machine, &version); err != nil {
			versions.Close()
			return fmt.Errorf("read state-machine version: %w", err)
		}
		fsm.machinesByVersion[version] = append(fsm.machinesByVersion[version], machine)
	}
	if err := versions.Close(); err != nil {
		return err
	}
	rows, err := db.Query("SELECT machine_id,uuid,COALESCE(dev_name_zh_cn,''),reference_uuid FROM aistatemachine_node ORDER BY machine_id,uuid")
	if err != nil {
		return fmt.Errorf("query state-machine nodes: %w", err)
	}
	for rows.Next() {
		var machine, name string
		var uuid int64
		var reference sql.NullInt64
		if err := rows.Scan(&machine, &uuid, &name, &reference); err != nil {
			rows.Close()
			return fmt.Errorf("read state-machine node: %w", err)
		}
		key := fsmNodeKey{machine, uuid}
		fsm.machinesByNode[uuid] = append(fsm.machinesByNode[uuid], machine)
		fsm.names[key] = name
		if reference.Valid {
			fsm.references[key] = reference.Int64
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	childRows, err := db.Query("SELECT machine_id,uuid,child_uuid FROM aistatemachine_node_child ORDER BY machine_id,uuid,ordinal")
	if err != nil {
		return fmt.Errorf("query state-machine node children: %w", err)
	}
	defer childRows.Close()
	for childRows.Next() {
		var machine string
		var uuid, child int64
		if err := childRows.Scan(&machine, &uuid, &child); err != nil {
			return fmt.Errorf("read state-machine node child: %w", err)
		}
		key := fsmNodeKey{machine, uuid}
		fsm.children[key] = append(fsm.children[key], child)
	}
	if err := childRows.Err(); err != nil {
		return err
	}
	labelsPresent, err := masterTableExists(db, "aistatemachine_node_name_locale")
	if err != nil {
		return err
	}
	if labelsPresent {
		labelRows, err := db.Query("SELECT dev_name_zh_cn,locale,label,label_kind FROM aistatemachine_node_name_locale WHERE locale IN (?,?)", locale, "zh-CN")
		if err != nil {
			return fmt.Errorf("query state-machine node labels: %w", err)
		}
		defer labelRows.Close()
		for labelRows.Next() {
			var name string
			var label localizedSkillLabel
			if err := labelRows.Scan(&name, &label.Locale, &label.Text, &label.Kind); err != nil {
				return fmt.Errorf("read state-machine node label: %w", err)
			}
			byLocale := fsm.labels[name]
			if byLocale == nil {
				byLocale = make(map[string]localizedSkillLabel)
				fsm.labels[name] = byLocale
			}
			byLocale[label.Locale] = label
		}
		if err := labelRows.Err(); err != nil {
			return err
		}
	}
	master.fsm = fsm
	return nil
}
