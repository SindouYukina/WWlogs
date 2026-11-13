package capture

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Where a fight happened and what kind of play it was, for display only.
//
// JoinSceneNotify.SceneInfo.InstanceId is the id the client itself looks up in InstanceDungeon
// (CreatureController.JoinSceneNotify sets CreatureModel.SetInstanceId from SceneInfo field 2, and the battle views read
// InstanceDungeonConfig.GetConfig(CreatureModel.GetInstanceId())), so its MapName is the stage or map the game names.
//
// The kind of play is the game's own content category: the Adventure Guide lists each kind of content as a
// DungeonDetection or SilentAreaDetection row whose Secondary names its category, and the client draws that category's
// name from SecondaryGuideData (AdventureModuleConfig.GetSecondaryGuideDataConf(Conf.Secondary).Text). A row reaches the
// instances it offers through ids the client resolves: DungeonId and InstanceDungeonEntranceId are entrances
// (InstanceDungeonEntranceConfig.GetConfig, HandleInstanceDungeonEntranceJump), whose InstanceDungeonList names the
// instances, and SubDungeonId is an instance when the row jumps with it or lists a role tutorial (Secondary 6 or 62). The
// open world is InstanceDungeon.InstType BigWorldInstance (protocol enum InstanceType = 1), not a guide category.
//
// None of this decides statistics or encounter boundaries.

// InstanceModeKind says where an instance's mode comes from.
type InstanceModeKind string

const (
	// InstanceModeOpenWorld is the open world (InstanceType BigWorldInstance); the UI labels it.
	InstanceModeOpenWorld InstanceModeKind = "open_world"
	// InstanceModeCategory is an Adventure Guide content category, named by the Master.
	InstanceModeCategory InstanceModeKind = "category"
)

// InstanceMode is the kind of play of one instance dungeon with its evidence.
type InstanceMode struct {
	Kind   InstanceModeKind `json:"kind,omitempty"`
	Name   string           `json:"name,omitempty"`
	Status string           `json:"status"`
	Locale string           `json:"locale,omitempty"`
	// Basis names the relation that gave the mode, for diagnostics and tests.
	Basis string `json:"basis,omitempty"`
}

const (
	instanceTypeBigWorld = 1
	// shipTowerGuideCategory is the Adventure Guide category the client draws from the ShipTower model
	// (NewSoundTowerItem.Update: 28 === Secondary ? ShipTowerModel : TowerModel). The ShipTower system's stages are
	// SlashTowerStageInfo.InstId and SlashAndTowerCfg.InstIds (ShipTowerConfig). Its guide rows share their entrance with
	// another category, so a ShipTower stage reaches the category through the system, not through a row: CORRELATED.
	shipTowerGuideCategory = "28"
)

// instanceModeRules are tried in order; the first that gives exactly one category decides. A rule that gives several
// categories is skipped as ambiguous. Every query takes the instance id as each of its parameters.
var instanceModeRules = []struct {
	basis  string
	status string
	query  string
	params int
}{
	// A detection row that names this very instance: a role tutorial or basic training (Secondary 6 or 62), whose
	// confirmation and jump use SubDungeonId, or a row that jumps with it (JumpWithSubDungeonId 1).
	{"sub_dungeon", "VERIFIED", `SELECT secondary FROM dungeondetection WHERE sub_dungeon_id = ? AND (jump_with_sub_dungeon_id = 1 OR secondary IN ('6', '62'))`, 1},
	// A detection row of an entrance whose InstanceDungeonList offers this instance.
	{"entrance", "VERIFIED", `SELECT secondary FROM dungeondetection WHERE dungeon_id IN (SELECT entrance_id FROM instancedungeonentrance_instance_ref WHERE instance_dungeon_id = ?)
		OR instance_dungeon_entrance_id IN (SELECT entrance_id FROM instancedungeonentrance_instance_ref WHERE instance_dungeon_id = ?)
		UNION SELECT secondary FROM silentareadetection WHERE instance_dungeon_entrance_id IN (SELECT entrance_id FROM instancedungeonentrance_instance_ref WHERE instance_dungeon_id = ?)`, 3},
	// SilentAreaDetection.DungeonId equal to the instance id; the client code does not show that field's namespace.
	{"silent_area_dungeon", "CORRELATED", `SELECT secondary FROM silentareadetection WHERE dungeon_id = ?`, 1},
	{"ship_tower", "CORRELATED", `SELECT '` + shipTowerGuideCategory + `' FROM slashtowerstageinfo WHERE inst_id = ? UNION SELECT '` + shipTowerGuideCategory + `' FROM slashandtowercfg_instance_ref WHERE instance_dungeon_id = ?`, 2},
}

// instanceModeTables must all exist for the guide categories; an older Master without them names no category.
var instanceModeTables = []string{"instancedungeon", "secondaryguidedata_locale", "dungeondetection", "silentareadetection", "instancedungeonentrance_instance_ref", "slashtowerstageinfo", "slashandtowercfg_instance_ref"}

// InstanceMode returns the kind of play of an instance dungeon in a locale (English as the fallback name). An instance the
// Master does not place stays UNKNOWN.
func (r *DisplayResolver) InstanceMode(ctx context.Context, instanceID uint64, locale string) (InstanceMode, error) {
	key := "mode:" + strconv.FormatUint(instanceID, 10) + ":" + locale
	r.mu.Lock()
	defer r.mu.Unlock()
	if mode, ok := r.modes[key]; ok {
		return mode, nil
	}
	mode, err := r.instanceModeLocked(ctx, instanceID, locale)
	if err != nil {
		return InstanceMode{Status: "UNKNOWN"}, err
	}
	if len(r.modeOrder) >= maxDisplayNames {
		delete(r.modes, r.modeOrder[0])
		r.modeOrder = r.modeOrder[1:]
	}
	r.modes[key] = mode
	r.modeOrder = append(r.modeOrder, key)
	return mode, nil
}

func (r *DisplayResolver) instanceModeLocked(ctx context.Context, instanceID uint64, locale string) (InstanceMode, error) {
	unknown := InstanceMode{Status: "UNKNOWN"}
	if instanceID == 0 {
		return unknown, nil
	}
	if r.modeTables == nil {
		available := true
		for _, table := range instanceModeTables {
			exists, err := masterTableExists(r.db, table)
			if err != nil {
				return unknown, errors.New("Master instance tables could not be read")
			}
			available = available && exists
		}
		r.modeTables = &available
	}
	if !*r.modeTables {
		return unknown, nil
	}
	id := strconv.FormatUint(instanceID, 10)
	var instType sql.NullInt64
	switch err := r.db.QueryRowContext(ctx, "SELECT inst_type FROM instancedungeon WHERE id=?", id).Scan(&instType); {
	case err == sql.ErrNoRows:
		return unknown, nil
	case err != nil:
		return unknown, errors.New("Master instance tables could not be read")
	}
	if instType.Valid && instType.Int64 == instanceTypeBigWorld {
		return InstanceMode{Kind: InstanceModeOpenWorld, Status: "VERIFIED", Basis: "inst_type"}, nil
	}
	for _, rule := range instanceModeRules {
		params := make([]any, rule.params)
		for i := range params {
			params[i] = id
		}
		categories, err := r.distinctLocked(ctx, rule.query, params...)
		if err != nil {
			return unknown, err
		}
		if len(categories) != 1 {
			continue
		}
		name, nameLocale, err := r.guideCategoryNameLocked(ctx, categories[0], locale)
		if err != nil {
			return unknown, err
		}
		if name == "" {
			return unknown, nil
		}
		return InstanceMode{Kind: InstanceModeCategory, Name: name, Status: rule.status, Locale: nameLocale, Basis: rule.basis}, nil
	}
	return unknown, nil
}

func (r *DisplayResolver) distinctLocked(ctx context.Context, query string, params ...any) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, errors.New("Master instance tables could not be read")
	}
	defer rows.Close()
	seen := map[string]bool{}
	var values []string
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, errors.New("Master instance tables could not be read")
		}
		if value.Valid && value.String != "" && !seen[value.String] {
			seen[value.String] = true
			values = append(values, value.String)
		}
	}
	if rows.Err() != nil {
		return nil, errors.New("Master instance tables could not be read")
	}
	return values, nil
}

// guideCategoryNameLocked is SecondaryGuideData.Text of a category in the locale, else in English.
func (r *DisplayResolver) guideCategoryNameLocked(ctx context.Context, category, locale string) (string, string, error) {
	for _, candidate := range []string{locale, "en-US"} {
		var name string
		err := r.db.QueryRowContext(ctx, "SELECT text FROM secondaryguidedata_locale WHERE id=? AND locale=?", category, candidate).Scan(&name)
		if err != nil && err != sql.ErrNoRows {
			return "", "", errors.New("Master instance tables could not be read")
		}
		if err == nil && name != "" {
			return name, candidate, nil
		}
	}
	return "", "", nil
}

// InstanceStage names the stage or map of an instance dungeon: its MapName (VERIFIED), else the title of the Tactical
// Hologram entrance set whose DungeonId is the instance, when exactly one title names it. Several holograms are fought
// in one such instance, so that title is CORRELATED. Without either it stays UNKNOWN.
func (r *DisplayResolver) InstanceStage(ctx context.Context, instanceID uint64, locale string) (DisplayName, error) {
	name, err := r.Resolve(ctx, "instance", instanceID, locale)
	if err != nil || name.Name != "" || instanceID == 0 {
		return name, err
	}
	key := "stage:" + strconv.FormatUint(instanceID, 10) + ":" + locale
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.names[key]; ok {
		return cached, nil
	}
	result := DisplayName{Status: "UNKNOWN"}
	if exists, err := masterTableExists(r.db, "lordgymentranceset_locale"); err != nil {
		return result, errors.New("Master instance tables could not be read")
	} else if exists {
		for _, candidate := range []string{locale, "en-US"} {
			titles, err := r.distinctLocked(ctx, `SELECT l.title FROM lordgymentranceset s JOIN lordgymentranceset_locale l ON l.id = s.id
				WHERE s.dungeon_id = ? AND l.locale = ?`, strconv.FormatUint(instanceID, 10), candidate)
			if err != nil {
				return result, err
			}
			if len(titles) == 1 {
				result = DisplayName{Name: titles[0], Status: "CORRELATED", Locale: candidate}
				break
			}
			if len(titles) > 1 {
				break
			}
		}
	}
	r.remember(key, result)
	return result, nil
}
