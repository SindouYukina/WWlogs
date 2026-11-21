package capture

import (
	"database/sql"
	"fmt"
)

// PropertyName is one PropertyIndex row: the attribute key used by the client and its localized display name.
type PropertyName struct {
	Key  string
	Name string
}

// FSMNodeKey identifies a node inside one state-machine config; node uuids are only unique per machine.
type FSMNodeKey struct {
	Machine string
	UUID    int64
}

// FSMMaster indexes the AI state-machine node identities (not their behaviour logic).
type FSMMaster struct {
	MachinesByNode    map[int64][]string // node uuid -> machines that define it
	MachinesByVersion map[int64][]string // config Version -> machines (EntityFsmComponentPb.HashCode)
	Names             map[FSMNodeKey]string
	Children          map[FSMNodeKey][]int64
	References        map[FSMNodeKey]int64
	// Labels holds the localized developer node names by zh-CN name: the master locale and the zh-CN original.
	Labels map[string]map[string]LocalizedSkillLabel
}

// loadSQLitePropertyNames reads PropertyIndex keys and the master-locale names.
func loadSQLitePropertyNames(db *sql.DB, locale string, master *GameMaster) error {
	rows, err := db.Query(`SELECT CAST(p.id AS INTEGER),p.key,COALESCE(l.name,'') FROM propertyindex p
		LEFT JOIN propertyindex_locale l ON l.id=p.id AND l.locale=? ORDER BY CAST(p.id AS INTEGER)`, locale)
	if err != nil {
		return fmt.Errorf("query PropertyIndex names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var entry PropertyName
		if err := rows.Scan(&id, &entry.Key, &entry.Name); err != nil {
			return fmt.Errorf("read PropertyIndex name: %w", err)
		}
		master.PropertyNames[id] = entry
	}
	return rows.Err()
}

// loadSQLiteStateMachines reads the state-machine node identities, their containment and their localized labels.
func loadSQLiteStateMachines(db *sql.DB, locale string, master *GameMaster) error {
	fsm := &FSMMaster{
		MachinesByNode:    make(map[int64][]string),
		MachinesByVersion: make(map[int64][]string),
		Names:             make(map[FSMNodeKey]string),
		Children:          make(map[FSMNodeKey][]int64),
		References:        make(map[FSMNodeKey]int64),
		Labels:            make(map[string]map[string]LocalizedSkillLabel),
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
		fsm.MachinesByVersion[version] = append(fsm.MachinesByVersion[version], machine)
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
		key := FSMNodeKey{machine, uuid}
		fsm.MachinesByNode[uuid] = append(fsm.MachinesByNode[uuid], machine)
		fsm.Names[key] = name
		if reference.Valid {
			fsm.References[key] = reference.Int64
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
		key := FSMNodeKey{machine, uuid}
		fsm.Children[key] = append(fsm.Children[key], child)
	}
	if err := childRows.Err(); err != nil {
		return err
	}
	labelRows, err := db.Query("SELECT dev_name_zh_cn,locale,label,label_kind FROM aistatemachine_node_name_locale WHERE locale IN (?,?)", locale, "zh-CN")
	if err != nil {
		return fmt.Errorf("query state-machine node labels: %w", err)
	}
	defer labelRows.Close()
	for labelRows.Next() {
		var name string
		var label LocalizedSkillLabel
		if err := labelRows.Scan(&name, &label.Locale, &label.Text, &label.Kind); err != nil {
			return fmt.Errorf("read state-machine node label: %w", err)
		}
		byLocale := fsm.Labels[name]
		if byLocale == nil {
			byLocale = make(map[string]LocalizedSkillLabel)
			fsm.Labels[name] = byLocale
		}
		byLocale[label.Locale] = label
	}
	if err := labelRows.Err(); err != nil {
		return err
	}
	master.FSM = fsm
	return nil
}
