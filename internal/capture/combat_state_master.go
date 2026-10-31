package capture

import (
	"database/sql"
	"fmt"
	"sort"
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

// fsmLabel returns the display text of a developer node name: the master locale text, else the zh-CN original.
func (m *gameMaster) fsmLabel(devName string) *combatEventLabelTextJSONL {
	if m == nil || m.fsm == nil || devName == "" {
		return nil
	}
	byLocale := m.fsm.labels[devName]
	chosen, ok := byLocale[m.locale]
	if !ok {
		chosen, ok = byLocale["zh-CN"]
	}
	if !ok || chosen.Text == "" {
		return nil
	}
	return &combatEventLabelTextJSONL{Locale: chosen.Locale, Text: chosen.Text, Kind: chosen.Kind,
		Evidence: "aistatemachine_node_name_locale row for this node name; an internal developer node name, not an official in-game text"}
}

// descendants returns every node uuid reachable from a node by child links, following reference links into the
// machines that define the referenced uuid.
func (f *fsmMaster) descendants(machine string, uuid int64) map[int64]struct{} {
	out := make(map[int64]struct{})
	seen := make(map[fsmNodeKey]struct{})
	stack := []fsmNodeKey{{machine, uuid}}
	for len(stack) > 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, done := seen[key]; done {
			continue
		}
		seen[key] = struct{}{}
		for _, child := range f.children[key] {
			out[child] = struct{}{}
			stack = append(stack, fsmNodeKey{key.machine, child})
		}
		if reference, ok := f.references[key]; ok {
			for _, other := range f.machinesByNode[reference] {
				out[reference] = struct{}{}
				stack = append(stack, fsmNodeKey{other, reference})
			}
		}
	}
	return out
}

// fsmResolution is the outcome of naming one state transition.
type fsmResolution struct {
	Status       resolutionStatus
	MachineCount int
	Machines     []string
	FromName     string
	ToName       string
	Evidence     string
}

const maxFsmMachinesShown = 5

// resolveFsmTransition names the two states of a ChangeStateNotify. FsmId and the state ids are node uuids, which are
// only unique inside one state-machine config, so the candidate machines are those that define the FsmId node and
// contain both states beneath it (the states alone when no machine does). The developer names are given only when all
// candidates agree on them; with several candidates that disagree, nothing is named.
func (m *gameMaster) resolveFsmTransition(fsmID, fromState, toState int64, entityHash, commonHash *int64) *fsmResolution {
	if m == nil || m.fsm == nil {
		return nil
	}
	candidates := make([]string, 0, 4)
	basis := "state machines that define the FsmId node and hold both states beneath it"
	allowed := map[string]struct{}(nil)
	// The entity's own config version (HashCode) and the common machine's version (CommonHashCode) narrow the machines
	// to the ones the entity runs; either hash alone is used when the other was not observed.
	for _, hash := range []*int64{entityHash, commonHash} {
		if hash == nil {
			continue
		}
		for _, machine := range m.fsm.machinesByVersion[*hash] {
			if allowed == nil {
				allowed = make(map[string]struct{})
				basis = "state machines whose config Version equals the entity's EntityFsmComponentPb HashCode or CommonHashCode and that hold both states beneath the FsmId node"
			}
			allowed[machine] = struct{}{}
		}
	}
	for _, machine := range m.fsm.machinesByNode[fsmID] {
		if allowed != nil {
			if _, ok := allowed[machine]; !ok {
				continue
			}
		}
		below := m.fsm.descendants(machine, fsmID)
		_, hasFrom := below[fromState]
		_, hasTo := below[toState]
		if hasFrom && hasTo {
			candidates = append(candidates, machine)
		}
	}
	if len(candidates) == 0 {
		// The states may live in the entity's own machine while the FsmId names a node of the common machine.
		pool := m.fsm.machinesByNode[fsmID]
		basis = "state machines that define the FsmId node and both state nodes"
		if allowed != nil {
			pool = pool[:0:0]
			for machine := range allowed {
				pool = append(pool, machine)
			}
			basis = "state machines whose config Version equals the entity's EntityFsmComponentPb HashCode or CommonHashCode and that define both state nodes"
		}
		for _, machine := range pool {
			if _, hasFrom := m.fsm.names[fsmNodeKey{machine, fromState}]; !hasFrom {
				continue
			}
			if _, hasTo := m.fsm.names[fsmNodeKey{machine, toState}]; hasTo {
				candidates = append(candidates, machine)
			}
		}
	}
	sort.Strings(candidates)
	result := &fsmResolution{Status: resolutionUnknown, MachineCount: len(candidates)}
	result.Machines = candidates
	if len(result.Machines) > maxFsmMachinesShown {
		result.Machines = result.Machines[:maxFsmMachinesShown]
	}
	if len(candidates) == 0 {
		result.Evidence = "No state-machine config defines this FsmId node together with both state nodes; raw ids kept"
		return result
	}
	fromNames, toNames := map[string]struct{}{}, map[string]struct{}{}
	for _, machine := range candidates {
		fromNames[m.fsm.names[fsmNodeKey{machine, fromState}]] = struct{}{}
		toNames[m.fsm.names[fsmNodeKey{machine, toState}]] = struct{}{}
	}
	if len(fromNames) != 1 || len(toNames) != 1 {
		result.Evidence = fmt.Sprintf("%d state-machine configs fit this transition and they name the states differently; the entity's own machine is not known, so no name is given", len(candidates))
		return result
	}
	for name := range fromNames {
		result.FromName = name
	}
	for name := range toNames {
		result.ToName = name
	}
	result.Status = resolutionCorrelated
	result.Evidence = fmt.Sprintf("%d %s; all agree on the developer node names (zh-CN developer labels, not localized text)", len(candidates), basis)
	return result
}
