package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// maxBuffOwnerNames bounds how many owner names one Buff event carries; ownerCount keeps the full number.
const maxBuffOwnerNames = 5

// BuffOwnerText names a game feature that lists a Buff id in an explicit Buff-id reference table
// (a resonant chain node, an echo set bonus, a weapon passive, a skill or a role observed carrying it). The Buff itself has no
// display name in the master; this is the readable text of the feature that grants it.
type BuffOwnerText struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	OwnerIDs    []uint64 `json:"ownerIds"`
	ownerSource string
}

// BuffOwnersJSON is what a Buff event carries for an id that has at least one named owner.
type BuffOwnersJSON struct {
	Status     ResolutionStatus `json:"status"`
	OwnerCount int              `json:"ownerCount"`
	Owners     []BuffOwnerText  `json:"owners"`
	Evidence   string           `json:"evidence"`
}

// buffOwnerQueries joins each explicit Buff-id reference table to the localized name of its owner.
var buffOwnerQueries = []struct {
	kind  string
	query string
	// anyBuff keeps rows whose Buff id has no static definition; the observation itself is the evidence.
	anyBuff bool
}{
	{"resonantchain",
		`SELECT CAST(b.value AS TEXT),CAST(b.owner_id AS TEXT),l.name FROM resonantchain_buff_ids b
		 JOIN resonantchain_locale l ON l.id=b.owner_id AND l.locale=? AND l.name<>''`, false},
	{"phantomfetter",
		`SELECT CAST(b.value AS TEXT),CAST(b.owner_id AS TEXT),l.name FROM phantomfetter_buff_ids b
		 JOIN phantomfetter_locale l ON l.id=b.owner_id AND l.locale=? AND l.name<>''`, false},
	{"weaponreson",
		`SELECT CAST(b.value AS TEXT),CAST(b.owner_id AS TEXT),l.name FROM weaponreson_effect b
		 JOIN weaponreson_locale l ON l.id=b.owner_id AND l.locale=? AND l.name<>''`, false},
	{"skill",
		`SELECT CAST(b.buff_id AS TEXT),CAST(b.skill_id AS TEXT),l.name FROM skill_buff_ref b
		 JOIN skilldescription_locale l ON l.id=b.skill_id AND l.locale=? AND l.name<>''`, false},
	// role_observation: the role carried this Buff as an active, self-instigated, indefinite-duration Buff in a capture.
	// It names the role only; it does not say which feature of the role grants the Buff.
	{"role_observation",
		`SELECT CAST(b.buff_id AS TEXT),CAST(b.role_id AS TEXT),l.name FROM role_buff_observation b
		 JOIN roleinfo_locale l ON l.id=b.role_id AND l.locale=? AND l.name<>''`, true},
}

// loadSQLiteBuffOwners indexes the named owners of every Buff id that exists in the buff table.
func loadSQLiteBuffOwners(db *sql.DB, locale string, master *GameMaster) error {
	type ownerKey struct {
		buff uint64
		kind string
		name string
	}
	collected := make(map[ownerKey]map[uint64]struct{})
	for _, source := range buffOwnerQueries {
		rows, err := db.Query(source.query, locale)
		if err != nil {
			return fmt.Errorf("query %s Buff owners: %w", source.kind, err)
		}
		for rows.Next() {
			var rawBuff, rawOwner, name string
			if err := rows.Scan(&rawBuff, &rawOwner, &name); err != nil {
				rows.Close()
				return fmt.Errorf("read %s Buff owner: %w", source.kind, err)
			}
			buffID, err := parseMasterID(rawBuff, source.kind+" Buff owner buff id")
			if err != nil {
				rows.Close()
				return err
			}
			ownerID, err := parseMasterID(rawOwner, source.kind+" Buff owner id")
			if err != nil {
				rows.Close()
				return err
			}
			if !source.anyBuff && !master.HasBuffDefinition(buffID) {
				continue
			}
			key := ownerKey{buffID, source.kind, name}
			if collected[key] == nil {
				collected[key] = make(map[uint64]struct{})
			}
			collected[key][ownerID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	for key, owners := range collected {
		ids := make([]uint64, 0, len(owners))
		for id := range owners {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		master.buffOwners[key.buff] = append(master.buffOwners[key.buff], BuffOwnerText{Kind: key.kind, Name: key.name, OwnerIDs: ids})
	}
	for _, list := range master.buffOwners {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Kind != list[j].Kind {
				return list[i].Kind < list[j].Kind
			}
			return list[i].Name < list[j].Name
		})
	}
	return nil
}

// ResolveBuffOwners returns the named owners of a Buff id, or nil when no reference table names one.
func ResolveBuffOwners(masters *GameMaster, buffID uint64) *BuffOwnersJSON {
	if masters == nil {
		return nil
	}
	list := masters.buffOwners[buffID]
	if len(list) == 0 {
		return nil
	}
	shown := list
	if len(shown) > maxBuffOwnerNames {
		shown = shown[:maxBuffOwnerNames]
	}
	return &BuffOwnersJSON{
		Status:     ResolutionCorrelated,
		OwnerCount: len(list),
		Owners:     shown,
		Evidence: "BuffId is listed by an explicit Buff-id reference table of each named owner (resonant chain, echo set, weapon passive or skill); the Buff has no display name of its own, " +
			"and an owner name does not prove that the effect was applied or for how long; client build observed, master compatibility unverified",
	}
}

// buffOwnerLookupID picks the Buff id whose owners describe a lifecycle row: typed BuffId first, then the apply Id, then the handle's earlier Id.
func (row *BuffLifecycleAuditRow) buffOwnerLookupID() (uint64, bool) {
	switch {
	case row.BuffDefinitionID != nil:
		return *row.BuffDefinitionID, true
	case row.GenericIDDefinitionID != nil:
		return *row.GenericIDDefinitionID, true
	case row.HandleLinkedStatus == ResolutionCorrelated && row.HandleLinkedID != nil:
		return *row.HandleLinkedID, true
	}
	return 0, false
}
