package capture

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// Where a fight happened and what kind of play it was, for display only.
//
// JoinSceneNotify.SceneInfo.InstanceId is the id the client itself looks up in InstanceDungeon
// (CreatureController.JoinSceneNotify sets CreatureModel.SetInstanceId from SceneInfo field 2, and the battle views read
// InstanceDungeonConfig.GetConfig(CreatureModel.GetInstanceId())), so its MapName is the stage or map the game names.
//
// The kind of play comes first from the Master's content model (content_display.go): an instance a content system lists
// belongs to that system, named by the system's Adventure Guide category, or by a WWlogs label in the UI when the game
// names no system. Every other instance is placed by the game's own content category: the Adventure Guide lists each kind
// of content as a DungeonDetection or SilentAreaDetection row whose Secondary names its category, and the client draws
// that category's name from SecondaryGuideData (AdventureModuleConfig.GetSecondaryGuideDataConf(Conf.Secondary).Text). A
// row reaches the instances it offers through ids the client resolves: DungeonId and InstanceDungeonEntranceId are
// entrances (InstanceDungeonEntranceConfig.GetConfig, HandleInstanceDungeonEntranceJump), whose InstanceDungeonList names
// the instances, and SubDungeonId is an instance when the row jumps with it or lists a role tutorial (Secondary 6 or 62).
// The open world is InstanceDungeon.InstType BigWorldInstance (protocol enum InstanceType = 1), not a guide category.
//
// The content model never overrides a guide relation that names another category: such an instance keeps the guide
// relation's answer and reports the disagreement (Conflict), so a new content row can neither take an identified instance
// away nor hide a contradiction. None of this decides statistics or encounter boundaries.

// InstanceModeKind says where an instance's mode comes from.
type InstanceModeKind string

const (
	// InstanceModeOpenWorld is the open world (InstanceType BigWorldInstance); the UI labels it.
	InstanceModeOpenWorld InstanceModeKind = "open_world"
	// InstanceModeCategory is an Adventure Guide content category, named by the Master.
	InstanceModeCategory InstanceModeKind = "category"
	// InstanceModeContentSystem is a content system the game gives no system name (System names it); the UI labels it
	// with a WWlogs classification, never as a game name.
	InstanceModeContentSystem InstanceModeKind = "content_system"
)

// InstanceMode is the kind of play of one instance dungeon with its evidence.
type InstanceMode struct {
	Kind   InstanceModeKind `json:"kind,omitempty"`
	Name   string           `json:"name,omitempty"`
	Status string           `json:"status"`
	Locale string           `json:"locale,omitempty"`
	// Basis names the relation that gave the mode, for diagnostics and tests.
	Basis string `json:"basis,omitempty"`
	// System is the content system that gave the mode (basis "content").
	System string `json:"system,omitempty"`
	// NameStatus says why a content system mode has no name: NO_OFFICIAL_NAME (approved: the game names no such system),
	// UNRESOLVED (its name source is not decided) or NAME_MISSING (named, but not in this locale or English).
	NameStatus string `json:"nameStatus,omitempty"`
	// Conflict names a content model answer that disagrees with the one given: "content:<system>" for a content system
	// whose category differs from the guide relation's, "systems:<a>,<b>" for an instance several content systems list.
	Conflict string `json:"conflict,omitempty"`
}

const instanceTypeBigWorld = 1

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
}

// InstanceMode returns the kind of play of an instance dungeon in a locale (English as the fallback name). An instance
// neither the content model nor a guide relation places stays UNKNOWN.
func (r *DisplayResolver) InstanceMode(ctx context.Context, instanceID uint64, locale string) (InstanceMode, error) {
	key := "mode:" + strconv.FormatUint(instanceID, 10) + ":" + locale
	r.mu.Lock()
	defer r.mu.Unlock()
	if mode, ok := r.modes.get(key); ok {
		return mode, nil
	}
	mode, err := r.instanceModeLocked(ctx, instanceID, locale)
	if err != nil {
		return InstanceMode{Status: "UNKNOWN"}, err
	}
	r.modes.put(key, mode)
	return mode, nil
}

func (r *DisplayResolver) instanceModeLocked(ctx context.Context, instanceID uint64, locale string) (InstanceMode, error) {
	unknown := InstanceMode{Status: "UNKNOWN"}
	if instanceID == 0 {
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
	guide, err := r.guideModeLocked(ctx, id, locale)
	if err != nil {
		return unknown, err
	}
	systems, err := r.contentSystemsLocked(ctx, id)
	if err != nil {
		return unknown, err
	}
	switch {
	case len(systems) == 0:
		return guide.InstanceMode, nil
	case len(systems) > 1:
		// Several systems list the instance: the content model does not choose one.
		mode := guide.InstanceMode
		mode.Conflict = "systems:" + strings.Join(systems, ",")
		if mode.Kind == "" {
			mode.Status = "AMBIGUOUS"
		}
		return mode, nil
	}
	system, err := r.contentSystemLocked(ctx, systems[0])
	if err != nil {
		return unknown, err
	}
	if guide.Kind != "" && (system.guideCategory == "" || guide.category != system.guideCategory) {
		mode := guide.InstanceMode
		mode.Conflict = "content:" + system.id
		return mode, nil
	}
	mode := InstanceMode{Status: "VERIFIED", Basis: "content", System: system.id}
	if system.guideCategory == "" {
		mode.Kind, mode.NameStatus = InstanceModeContentSystem, system.nameStatus
		return mode, nil
	}
	name, nameLocale, err := r.guideCategoryNameLocked(ctx, system.guideCategory, locale)
	if err != nil {
		return unknown, err
	}
	if name == "" {
		// The system is known; only its name is missing, which never makes the instance UNKNOWN.
		mode.Kind, mode.NameStatus = InstanceModeContentSystem, "NAME_MISSING"
		return mode, nil
	}
	mode.Kind, mode.Name, mode.Locale = InstanceModeCategory, name, nameLocale
	return mode, nil
}

// guideMode is a guide relation's answer, with the category it names.
type guideMode struct {
	InstanceMode
	category string
}

// guideModeLocked places an instance by the first guide relation that gives it exactly one category; UNKNOWN without one,
// or when the category has no name in the locale or English.
func (r *DisplayResolver) guideModeLocked(ctx context.Context, id, locale string) (guideMode, error) {
	unknown := guideMode{InstanceMode: InstanceMode{Status: "UNKNOWN"}}
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
		return guideMode{InstanceMode: InstanceMode{Kind: InstanceModeCategory, Name: name, Status: rule.status, Locale: nameLocale, Basis: rule.basis}, category: categories[0]}, nil
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
		if err == nil && masterOfficialText(name) {
			return name, candidate, nil
		}
	}
	return "", "", nil
}

// InstanceStage names the stage or map of an instance dungeon: its official MapName in the locale, else in English
// (VERIFIED); without one it stays UNKNOWN. The name of another layer, such as an activity, series, mode or difficulty of
// the content model, is never given as the stage: InstanceContent gives those with their dimension.
func (r *DisplayResolver) InstanceStage(ctx context.Context, instanceID uint64, locale string) (DisplayName, error) {
	return r.Resolve(ctx, "instance", instanceID, locale)
}
