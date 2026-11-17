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
// arrow) and "2" (DEBUFF arrow). Anything else, an empty Parameters included, draws the icon without an arrow: the game
// shows the status and states no direction for it (EffectPolarityNeutral). Global 3.8.9 draws Lily's four statuses
// (cues 1512623002..1512623005, T_IconLiliBuff02..05) and Xin's type-24 statuses this way.
var hudCueTypes = map[int32]bool{2: true, 14: true, 24: true, 37: true}

const (
	// hudParameterDirection is GameplayCue.Parameters[0], read by BuffItem.Activate.
	hudParameterDirection = 0
	// hudParameterMonsterHead is GameplayCue.Parameters[4]; "1" hides the cue on monster head views (BuffItemContainer.oJl).
	hudParameterMonsterHead = 4
	hudDirectionBuff        = "1"
	hudDirectionDebuff      = "2"
	hudHiddenOnMonsterHead  = "1"
)

// hudCuePolarity reads the direction parameter of a HUD cue with a Path. A value the HUD draws no arrow for, or no value
// (an empty Parameters vector stores no row), is neutral; without the parameters table there is no direction evidence.
func hudCuePolarity(direction string, haveParameters bool) EffectPolarity {
	switch {
	case !haveParameters:
		return EffectPolarityUnclassified
	case direction == hudDirectionBuff:
		return EffectPolarityBuff
	case direction == hudDirectionDebuff:
		return EffectPolarityDebuff
	}
	return EffectPolarityNeutral
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
// buff handle in BuffItemContainer, so the first one is the one the bar keeps; type 14 can add a second item, and in the
// Master data every definition with both uses the same direction for each. Both tables are optional, so a Master without
// gameplaycue simply has no HUD evidence.
func loadBuffHUDCues(db *sql.DB, statics map[uint64]BuffStaticParameters) error {
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
	rows, err := db.Query(`SELECT b.owner_id, g.id, g.cue_type FROM buff_gameplay_cue_ids b
		JOIN gameplaycue g ON g.id = CAST(CAST(b.value AS INTEGER) AS TEXT)
		WHERE g.path_asset_id IS NOT NULL AND g.path_asset_id <> ''
		ORDER BY b.owner_id, b.ordinal`)
	if err != nil {
		return fmt.Errorf("query Buff HUD cues: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, cue string
		var cueType int32
		if err := rows.Scan(&raw, &cue, &cueType); err != nil {
			return fmt.Errorf("read Buff HUD cue: %w", err)
		}
		if !hudCueTypes[cueType] {
			continue
		}
		id, err := parseMasterID(raw, "buff_gameplay_cue_ids.owner_id")
		if err != nil {
			return err
		}
		entry := statics[id]
		if len(entry.hudCueTypes) == 0 {
			entry.hudPolarity = hudCuePolarity(parameters[cue][hudParameterDirection], parameters != nil)
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
	return rows.Err()
}
