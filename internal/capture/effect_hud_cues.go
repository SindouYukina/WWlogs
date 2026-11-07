package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// The game's HUD buff bars (BuffItemContainer) draw an effect from the GameplayCues its definition lists
// (Buff.GameplayCueIds, buff_gameplay_cue_ids): cue types 2, 14 and 37 in RoleBuffView and the boss and monster head
// views, and type 24 in RoleUniqueBuffView, each drawn with the cue's Path as its icon (BuffItem.Activate). A
// definition with such a cue and Path is therefore an effect the game shows the player. That is static, client-side
// evidence of presentation; it says nothing about polarity, value, or who benefits.
var hudCueTypes = map[int32]bool{2: true, 14: true, 24: true, 37: true}

// loadBuffHUDCues records, for every buff row, the distinct HUD cue types of its GameplayCueIds whose cue has a Path.
// Both tables are optional, so an older Master without gameplaycue simply has no HUD evidence.
func loadBuffHUDCues(db *sql.DB, statics map[uint64]buffStaticParameters) error {
	for _, table := range []string{"buff_gameplay_cue_ids", "gameplaycue"} {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
	}
	rows, err := db.Query(`SELECT b.owner_id, g.cue_type FROM buff_gameplay_cue_ids b
		JOIN gameplaycue g ON g.id = CAST(CAST(b.value AS INTEGER) AS TEXT)
		WHERE g.path_asset_id IS NOT NULL AND g.path_asset_id <> ''`)
	if err != nil {
		return fmt.Errorf("query Buff HUD cues: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var cueType int32
		if err := rows.Scan(&raw, &cueType); err != nil {
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
		index := sort.Search(len(entry.hudCueTypes), func(i int) bool { return entry.hudCueTypes[i] >= cueType })
		if index < len(entry.hudCueTypes) && entry.hudCueTypes[index] == cueType {
			continue
		}
		entry.hudCueTypes = append(entry.hudCueTypes, 0)
		copy(entry.hudCueTypes[index+1:], entry.hudCueTypes[index:])
		entry.hudCueTypes[index] = cueType
		statics[id] = entry
	}
	return rows.Err()
}
