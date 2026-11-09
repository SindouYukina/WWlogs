package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// The game's HUD buff bars (BuffItemContainer) draw an effect from the GameplayCues its definition lists
// (Buff.GameplayCueIds, buff_gameplay_cue_ids): cue types 2, 14 and 37 in RoleBuffView and the boss and monster head
// views, and type 24 in RoleUniqueBuffView, each drawn with the cue's Path as its icon (BuffItem.Activate). A
// definition with such a cue and Path is therefore an effect the game shows the player, and a definition without one is
// an effect no buff bar can draw.
//
// The status's direction lives on the cue, not on the Buff row: BuffItem.Activate compares Parameters[0] with "1" (BUFF
// arrow) and "2" (DEBUFF arrow). Anything else draws no arrow, so the game itself states no direction for it.
var hudCueTypes = map[int32]bool{2: true, 14: true, 24: true, 37: true}

// hudGenericIconShare is how many buff definitions must share a HUD icon before it stops telling them apart. In 3.7.17
// the three icons above it are the crossed swords and weapon marks used by 241 to 448 definitions, and the next one is
// used by 79. An effect with only such an icon keeps it, at a lower visual weight.
const hudGenericIconShare = 100

const (
	// hudParameterDirection is GameplayCue.Parameters[0], read by BuffItem.Activate.
	hudParameterDirection = 0
	// hudParameterMonsterHead is GameplayCue.Parameters[4]; "1" hides the cue on monster head views (BuffItemContainer.oJl).
	hudParameterMonsterHead = 4
	hudDirectionBuff        = "1"
	hudDirectionDebuff      = "2"
	hudHiddenOnMonsterHead  = "1"
)

// hudCuePolarity reads a cue's direction parameter. A value the HUD draws no arrow for stays unclassified.
func hudCuePolarity(direction string) EffectPolarity {
	switch direction {
	case hudDirectionBuff:
		return EffectPolarityBuff
	case hudDirectionDebuff:
		return EffectPolarityDebuff
	}
	return EffectPolarityUnclassified
}

// loadHUDCueParameters reads the direction and monster-head parameters of every HUD cue, keyed by the cue's id as stored.
// The table is optional, so an older Master has no direction evidence.
func loadHUDCueParameters(db *sql.DB) (map[string]map[int]string, error) {
	exists, err := masterTableExists(db, "gameplaycue_parameters")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := db.Query(`SELECT owner_id, ordinal, value FROM gameplaycue_parameters WHERE ordinal IN (?, ?)`, hudParameterDirection, hudParameterMonsterHead)
	if err != nil {
		return nil, fmt.Errorf("query GameplayCue parameters: %w", err)
	}
	defer rows.Close()
	parameters := map[string]map[int]string{}
	for rows.Next() {
		var cue, value string
		var ordinal int
		if err := rows.Scan(&cue, &ordinal, &value); err != nil {
			return nil, fmt.Errorf("read GameplayCue parameter: %w", err)
		}
		if parameters[cue] == nil {
			parameters[cue] = map[int]string{}
		}
		parameters[cue][ordinal] = value
	}
	return parameters, rows.Err()
}

// loadBuffHUDCues records, for every buff row, the distinct HUD cue types of its GameplayCueIds whose cue has a Path, and
// the direction and monster-head visibility of the first such cue in list order. Cues of types 2, 24 and 37 are keyed by
// buff handle in BuffItemContainer, so the first one is the one the bar keeps; type 14 can add a second item, and every
// definition with both in 3.7.17 uses the same direction for each. Both tables are optional, so an older Master without
// gameplaycue simply has no HUD evidence.
func loadBuffHUDCues(db *sql.DB, statics map[uint64]buffStaticParameters) error {
	for _, table := range []string{"buff_gameplay_cue_ids", "gameplaycue"} {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
	}
	parameters, err := loadHUDCueParameters(db)
	if err != nil {
		return err
	}
	rows, err := db.Query(`SELECT b.owner_id, g.id, g.cue_type, g.path_asset_id FROM buff_gameplay_cue_ids b
		JOIN gameplaycue g ON g.id = CAST(CAST(b.value AS INTEGER) AS TEXT)
		WHERE g.path_asset_id IS NOT NULL AND g.path_asset_id <> ''
		ORDER BY b.owner_id, b.ordinal`)
	if err != nil {
		return fmt.Errorf("query Buff HUD cues: %w", err)
	}
	defer rows.Close()
	iconsOf := map[uint64]map[string]bool{}
	sharedBy := map[string]int{}
	for rows.Next() {
		var raw, cue, icon string
		var cueType int32
		if err := rows.Scan(&raw, &cue, &cueType, &icon); err != nil {
			return fmt.Errorf("read Buff HUD cue: %w", err)
		}
		if !hudCueTypes[cueType] {
			continue
		}
		id, err := parseMasterID(raw, "buff_gameplay_cue_ids.owner_id")
		if err != nil {
			return err
		}
		if iconsOf[id] == nil {
			iconsOf[id] = map[string]bool{}
		}
		if !iconsOf[id][icon] {
			iconsOf[id][icon] = true
			sharedBy[icon]++
		}
		entry := statics[id]
		if len(entry.hudCueTypes) == 0 {
			entry.hudPolarity = hudCuePolarity(parameters[cue][hudParameterDirection])
			entry.hudHiddenOnMonsterHead = parameters[cue][hudParameterMonsterHead] == hudHiddenOnMonsterHead
		}
		index := sort.Search(len(entry.hudCueTypes), func(i int) bool { return entry.hudCueTypes[i] >= cueType })
		if index < len(entry.hudCueTypes) && entry.hudCueTypes[index] == cueType {
			statics[id] = entry
			continue
		}
		entry.hudCueTypes = append(entry.hudCueTypes, 0)
		copy(entry.hudCueTypes[index+1:], entry.hudCueTypes[index:])
		entry.hudCueTypes[index] = cueType
		statics[id] = entry
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, icons := range iconsOf {
		generic := true
		for icon := range icons {
			generic = generic && sharedBy[icon] >= hudGenericIconShare
		}
		entry := statics[id]
		entry.hudIconGeneric = generic
		statics[id] = entry
	}
	return nil
}
