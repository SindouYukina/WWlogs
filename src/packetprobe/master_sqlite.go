package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

var supportedMasterLocales = map[string]struct{}{
	"zh-TW": {}, "zh-CN": {}, "en-US": {}, "ja-JP": {}, "ko-KR": {},
}

func loadGameMasterDatabase(path, locale string) (*gameMaster, error) {
	if _, ok := supportedMasterLocales[locale]; !ok {
		return nil, fmt.Errorf("unsupported master locale %q", locale)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect master database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("master database path is not a regular file: %s", path)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve master database path: %w", err)
	}
	uriPath := filepath.ToSlash(absPath)
	if len(uriPath) >= 2 && uriPath[1] == ':' {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open master database read-only: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		return nil, fmt.Errorf("set master database query-only: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open master database: %w", err)
	}

	hash, err := hashFile(path)
	if err != nil {
		return nil, fmt.Errorf("hash master database: %w", err)
	}
	master := &gameMaster{
		manifest:       masterManifest{Language: locale, ClientRegion: "Global", SourceHash: hash},
		databasePath:   absPath,
		locale:         locale,
		characters:     make(map[uint64]masterRecord),
		charactersSkin: make(map[uint64]masterRecord),
		ambiguousSkins: make(map[uint64]struct{}),
		monsters:       make(map[uint64]masterRecord),
		skills:         make(map[uint64]masterRecord),
		commonSkills:   make(map[uint64]masterRecord),
		roleSkins:      make(map[uint64]uint64),
		phantomSkills:  make(map[uint64]struct{}),
		visions:        make(map[uint64]struct{}),
		commonBullets:  make(map[uint64]string),
		preloadBullets: make(map[uint64]struct{}),
		roleSkinMesh:    make(map[uint64]uint64),
		modelActor:      make(map[uint64]string),
		blueprintModel:  make(map[string]uint64),
		summonBlueprint: make(map[uint64]string),
		visionCast:      make(map[uint64][]uint64),
		actorBullets:    make(map[string]map[uint64]struct{}),
		assetPath:       make(map[string]string),
		actorSkills:     make(map[string]map[uint64]struct{}),
		skillLabels:     make(map[uint64]map[string]struct{}),
		damage:         make(map[uint64]damageMasterRecord),
		sceneIdentity: &sceneIdentityMaster{
			instanceMaps:    make(map[uint64]uint64),
			levelMonsters:   make(map[sceneLevelKey]uint64),
			levelTemplates:  make(map[sceneLevelKey][]sceneTemplateCandidate),
			templateCatalog: make(map[uint64]sceneTemplateCandidate),
		},
	}
	if err := loadLocalizedDefinitions(db, locale, "roleinfo", "roleinfo_locale", &master.characters); err != nil {
		return nil, err
	}
	if err := loadSQLiteCharacterSkins(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteRoleSkins(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLitePhantomSkills(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteVisions(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteBullets(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteActorChain(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteSkillLabels(db, master); err != nil {
		return nil, err
	}
	if err := loadLocalizedDefinitions(db, locale, "monsterinfo", "monsterinfo_locale", &master.monsters); err != nil {
		return nil, err
	}
	if err := loadSQLiteSkills(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteCommonSkills(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteDamage(db, hash, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteScene(db, master); err != nil {
		return nil, err
	}
	if len(master.characters) == 0 || len(master.monsters) == 0 || len(master.skills) == 0 || len(master.damage) == 0 {
		return nil, fmt.Errorf("master database is missing required combat definitions: roles=%d monsters=%d skills=%d damage=%d",
			len(master.characters), len(master.monsters), len(master.skills), len(master.damage))
	}
	return master, nil
}

// loadSQLiteSkillLabels reads the developer skill labels of the skillinfo table when it exists.
func loadSQLiteSkillLabels(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "skillinfo")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT skill_id,dev_name_zh_cn FROM skillinfo WHERE dev_name_zh_cn IS NOT NULL")
	if err != nil {
		return fmt.Errorf("query SkillInfo labels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, label string
		if err := rows.Scan(&raw, &label); err != nil {
			return fmt.Errorf("read SkillInfo label: %w", err)
		}
		id, err := parseMasterID(raw, "skillinfo.skill_id")
		if err != nil {
			return err
		}
		set := master.skillLabels[id]
		if set == nil {
			set = make(map[string]struct{})
			master.skillLabels[id] = set
		}
		set[label] = struct{}{}
	}
	return rows.Err()
}

// masterColumnExists reports whether table has the named column.
func masterColumnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("read %s column: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// loadSQLiteActorChain reads the static tables that link a source entity to the actor
// blueprint whose BulletPreload rows list the bullets it may use. Every table is optional,
// so an older master still loads; a missing link only shortens the chain.
func loadSQLiteActorChain(db *sql.DB, master *gameMaster) error {
	pairs := func(table, query string, visit func(a, b sql.NullString) error) error {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
		rows, err := db.Query(query)
		if err != nil {
			return fmt.Errorf("query %s chain: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b sql.NullString
			if err := rows.Scan(&a, &b); err != nil {
				return fmt.Errorf("read %s chain: %w", table, err)
			}
			if err := visit(a, b); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	id := func(value sql.NullString, what string) (uint64, bool, error) {
		if !value.Valid || value.String == "" {
			return 0, false, nil
		}
		parsed, err := parseMasterID(value.String, what)
		return parsed, err == nil, err
	}
	hasMesh, err := masterColumnExists(db, "roleskin", "mesh_id")
	if err != nil {
		return err
	}
	if hasMesh {
		if err := pairs("roleskin", "SELECT id,mesh_id FROM roleskin", func(a, b sql.NullString) error {
			skin, ok, err := id(a, "roleskin.id")
			if err != nil || !ok {
				return err
			}
			mesh, ok, err := id(b, "roleskin.mesh_id")
			if ok {
				master.roleSkinMesh[skin] = mesh
			}
			return err
		}); err != nil {
			return err
		}
	}
	if err := pairs("modelconfigpreload", "SELECT id,actor_asset_id FROM modelconfigpreload WHERE actor_asset_id IS NOT NULL", func(a, b sql.NullString) error {
		model, ok, err := id(a, "modelconfigpreload.id")
		if err != nil || !ok {
			return err
		}
		master.modelActor[model] = b.String
		return nil
	}); err != nil {
		return err
	}
	if err := pairs("blueprintconfig", "SELECT blueprint_type,model_id FROM blueprintconfig WHERE model_id IS NOT NULL", func(a, b sql.NullString) error {
		model, ok, err := id(b, "blueprintconfig.model_id")
		if err != nil || !ok {
			return err
		}
		master.blueprintModel[a.String] = model
		return nil
	}); err != nil {
		return err
	}
	if err := pairs("summoncfg", "SELECT id,blueprint_type FROM summoncfg WHERE blueprint_type IS NOT NULL", func(a, b sql.NullString) error {
		cfg, ok, err := id(a, "summoncfg.id")
		if err != nil || !ok {
			return err
		}
		master.summonBlueprint[cfg] = b.String
		return nil
	}); err != nil {
		return err
	}
	hasCast, err := masterColumnExists(db, "vision", "birth_cast_skill_id")
	if err != nil {
		return err
	}
	if hasCast {
		if err := pairs("vision", "SELECT id,birth_cast_skill_id FROM vision WHERE birth_cast_skill_id IS NOT NULL", func(a, b sql.NullString) error {
			vision, ok, err := id(a, "vision.id")
			if err != nil || !ok {
				return err
			}
			skill, ok, err := id(b, "vision.birth_cast_skill_id")
			if ok {
				master.visionCast[skill] = append(master.visionCast[skill], vision)
			}
			return err
		}); err != nil {
			return err
		}
	}
	if err := pairs("bulletpreload", "SELECT actor_asset_id,bullet_id FROM bulletpreload WHERE actor_asset_id IS NOT NULL AND bullet_id IS NOT NULL", func(a, b sql.NullString) error {
		bullet, ok, err := id(b, "bulletpreload.bullet_id")
		if err != nil || !ok {
			return err
		}
		set := master.actorBullets[a.String]
		if set == nil {
			set = make(map[uint64]struct{})
			master.actorBullets[a.String] = set
		}
		set[bullet] = struct{}{}
		return nil
	}); err != nil {
		return err
	}
	hasActorSkills, err := masterColumnExists(db, "entityskillpreload", "actor_asset_id")
	if err != nil {
		return err
	}
	if !hasActorSkills {
		return pairs("asset_reference", "SELECT id,path FROM asset_reference", func(a, b sql.NullString) error {
			master.assetPath[a.String] = b.String
			return nil
		})
	}
	if err := pairs("entityskillpreload", "SELECT actor_asset_id,skill_id FROM entityskillpreload WHERE actor_asset_id IS NOT NULL AND skill_id IS NOT NULL", func(a, b sql.NullString) error {
		skill, ok, err := id(b, "entityskillpreload.skill_id")
		if err != nil || !ok {
			return err
		}
		set := master.actorSkills[a.String]
		if set == nil {
			set = make(map[uint64]struct{})
			master.actorSkills[a.String] = set
		}
		set[skill] = struct{}{}
		return nil
	}); err != nil {
		return err
	}
	return pairs("asset_reference", "SELECT id,path FROM asset_reference", func(a, b sql.NullString) error {
		master.assetPath[a.String] = b.String
		return nil
	})
}

// loadSQLiteBullets reads commonbullet and bulletpreload row keys when the tables exist.
func loadSQLiteBullets(db *sql.DB, master *gameMaster) error {
	load := func(table, query string, visit func(id uint64, label sql.NullString)) error {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
		rows, err := db.Query(query)
		if err != nil {
			return fmt.Errorf("query %s definitions: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var label sql.NullString
			if err := rows.Scan(&raw, &label); err != nil {
				return fmt.Errorf("read %s definition: %w", table, err)
			}
			id, err := parseMasterID(raw, table+" bullet id")
			if err != nil {
				return err
			}
			visit(id, label)
		}
		return rows.Err()
	}
	if err := load("commonbullet", "SELECT id,dev_name_zh_cn FROM commonbullet", func(id uint64, label sql.NullString) {
		master.commonBullets[id] = label.String
	}); err != nil {
		return err
	}
	return load("bulletpreload", "SELECT DISTINCT bullet_id,NULL FROM bulletpreload WHERE bullet_id IS NOT NULL", func(id uint64, _ sql.NullString) {
		master.preloadBullets[id] = struct{}{}
	})
}

// loadSQLiteVisions reads DT_Vision row keys from the vision table when it exists.
func loadSQLiteVisions(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "vision")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT id FROM vision")
	if err != nil {
		return fmt.Errorf("query Vision definitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read Vision definition: %w", err)
		}
		id, err := parseMasterID(raw, "vision.id")
		if err != nil {
			return err
		}
		master.visions[id] = struct{}{}
	}
	return rows.Err()
}

// loadSQLitePhantomSkills reads PhantomSkill.PhantomSkillId when the table exists.
func loadSQLitePhantomSkills(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "phantomskill")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT phantom_skill_id FROM phantomskill WHERE phantom_skill_id IS NOT NULL")
	if err != nil {
		return fmt.Errorf("query PhantomSkill definitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read PhantomSkill definition: %w", err)
		}
		id, err := parseMasterID(raw, "phantomskill.phantom_skill_id")
		if err != nil {
			return err
		}
		master.phantomSkills[id] = struct{}{}
	}
	return rows.Err()
}

// loadSQLiteRoleSkins reads the normalized roleskin table when the database has one.
// Each skin must reference a character that exists in the localized RoleInfo set;
// a missing referent is a build defect, not an unknown value.
func loadSQLiteRoleSkins(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "roleskin")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := db.Query("SELECT id,role_id FROM roleskin ORDER BY id")
	if err != nil {
		return fmt.Errorf("query RoleSkin definitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var skinRaw, roleRaw string
		if err := rows.Scan(&skinRaw, &roleRaw); err != nil {
			return fmt.Errorf("read RoleSkin definition: %w", err)
		}
		skinID, err := parseMasterID(skinRaw, "roleskin.id")
		if err != nil {
			return err
		}
		roleID, err := parseMasterID(roleRaw, "roleskin.role_id")
		if err != nil {
			return err
		}
		character, ok := master.characters[roleID]
		if !ok {
			return fmt.Errorf("RoleSkin %d references character %d absent from localized RoleInfo", skinID, roleID)
		}
		master.roleSkins[skinID] = roleID
		if _, ambiguous := master.ambiguousSkins[skinID]; ambiguous {
			continue
		}
		if previous, exists := master.charactersSkin[skinID]; exists && previous.ID != roleID {
			delete(master.charactersSkin, skinID)
			master.ambiguousSkins[skinID] = struct{}{}
			continue
		}
		if _, exists := master.charactersSkin[skinID]; !exists {
			master.charactersSkin[skinID] = character
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate RoleSkin definitions: %w", err)
	}
	return nil
}

func loadSQLiteCommonSkills(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "commonskillpreload")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := db.Query("SELECT id FROM commonskillpreload ORDER BY id")
	if err != nil {
		return fmt.Errorf("query CommonSkillPreload definitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID string
		if err := rows.Scan(&rawID); err != nil {
			return fmt.Errorf("read CommonSkillPreload definition: %w", err)
		}
		id, err := parseMasterID(rawID, "commonskillpreload.id")
		if err != nil {
			return err
		}
		record := masterRecord{ID: id, Status: string(resolutionVerified), NameStatus: string(resolutionUnknown), SourceTable: "CommonSkillPreload.Id"}
		master.commonSkills[id] = record
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate CommonSkillPreload definitions: %w", err)
	}
	return nil
}

func loadSQLiteCharacterSkins(db *sql.DB, master *gameMaster) error {
	rows, err := db.Query("PRAGMA table_info(roleinfo)")
	if err != nil {
		return fmt.Errorf("inspect RoleInfo columns: %w", err)
	}
	hasSkinID := false
	for rows.Next() {
		var ordinal int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read RoleInfo column: %w", err)
		}
		if name == "skin_id" {
			hasSkinID = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate RoleInfo columns: %w", err)
	}
	rows.Close()
	if !hasSkinID {
		return nil
	}

	rows, err = db.Query("SELECT id,skin_id FROM roleinfo WHERE skin_id IS NOT NULL AND skin_id <> '' AND skin_id <> '0' ORDER BY id")
	if err != nil {
		return fmt.Errorf("query RoleInfo skin references: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var roleRaw, skinRaw string
		if err := rows.Scan(&roleRaw, &skinRaw); err != nil {
			return fmt.Errorf("read RoleInfo skin reference: %w", err)
		}
		roleID, err := parseMasterID(roleRaw, "roleinfo.id")
		if err != nil {
			return err
		}
		skinID, err := parseMasterID(skinRaw, "roleinfo.skin_id")
		if err != nil {
			return err
		}
		character, exists := master.characters[roleID]
		if !exists {
			return fmt.Errorf("RoleInfo.SkinId references character %d absent from localized RoleInfo", roleID)
		}
		character.SkinID = skinID
		master.characters[roleID] = character
		if _, ambiguous := master.ambiguousSkins[skinID]; ambiguous {
			continue
		}
		if previous, exists := master.charactersSkin[skinID]; exists && previous.ID != roleID {
			delete(master.charactersSkin, skinID)
			master.ambiguousSkins[skinID] = struct{}{}
			continue
		}
		master.charactersSkin[skinID] = character
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate RoleInfo skin references: %w", err)
	}
	return nil
}

func loadLocalizedDefinitions(db *sql.DB, locale, baseTable, localeTable string, destination *map[uint64]masterRecord) error {
	query := fmt.Sprintf("SELECT b.id,l.name FROM %s b LEFT JOIN %s l ON l.id=b.id AND l.locale=? ORDER BY b.id", baseTable, localeTable)
	rows, err := db.Query(query, locale)
	if err != nil {
		return fmt.Errorf("query %s definitions: %w", baseTable, err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID string
		var name sql.NullString
		if err := rows.Scan(&rawID, &name); err != nil {
			return fmt.Errorf("read %s definition: %w", baseTable, err)
		}
		id, err := parseMasterID(rawID, baseTable)
		if err != nil {
			return err
		}
		if _, exists := (*destination)[id]; exists {
			return fmt.Errorf("duplicate %s ID %d", baseTable, id)
		}
		record := masterRecord{ID: id, Language: locale, Status: string(resolutionVerified), SourceTable: baseTable}
		if name.Valid {
			record.Name = name.String
		}
		if record.Name == "" {
			record.NameStatus = string(resolutionUnknown)
		} else {
			record.NameStatus = string(resolutionVerified)
		}
		(*destination)[id] = record
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s definitions: %w", baseTable, err)
	}
	return nil
}

func loadSQLiteSkills(db *sql.DB, locale string, master *gameMaster) error {
	roleGroups := make(map[string]map[uint64]struct{})
	rows, err := db.Query("SELECT role_id,skill_group_id FROM role_skill_group_ref ORDER BY role_id,skill_group_id")
	if err != nil {
		return fmt.Errorf("query role skill groups: %w", err)
	}
	for rows.Next() {
		var roleRaw, groupRaw string
		if err := rows.Scan(&roleRaw, &groupRaw); err != nil {
			rows.Close()
			return fmt.Errorf("read role skill group: %w", err)
		}
		roleID, err := parseMasterID(roleRaw, "role_skill_group_ref.role_id")
		if err != nil {
			rows.Close()
			return err
		}
		groups := roleGroups[groupRaw]
		if groups == nil {
			groups = make(map[uint64]struct{})
			roleGroups[groupRaw] = groups
		}
		groups[roleID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate role skill groups: %w", err)
	}
	rows.Close()

	hasPreload, err := masterTableExists(db, "entityskillpreload")
	if err != nil {
		return err
	}
	preloadMatch := "0"
	if hasPreload {
		preloadMatch = "EXISTS(SELECT 1 FROM entityskillpreload p WHERE p.skill_id=d.id)"
	}
	rows, err = db.Query(`SELECT d.id,d.skill_level_group_id,
		(SELECT skill_group_id FROM skill WHERE skill_level_group_id=d.skill_level_group_id ORDER BY id LIMIT 1),l.name,`+preloadMatch+`
		FROM skilldescription d
		LEFT JOIN skilldescription_locale l ON l.id=d.id AND l.locale=?
		ORDER BY d.id`, locale)
	if err != nil {
		return fmt.Errorf("query skill descriptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID string
		var rawLevelGroup sql.NullString
		var rawGroup sql.NullString
		var name sql.NullString
		var hasRuntimeBinding bool
		if err := rows.Scan(&rawID, &rawLevelGroup, &rawGroup, &name, &hasRuntimeBinding); err != nil {
			return fmt.Errorf("read skill description: %w", err)
		}
		id, err := parseMasterID(rawID, "skilldescription")
		if err != nil {
			return err
		}
		record := masterRecord{ID: id, Language: locale, Status: string(resolutionVerified), SourceTable: "skilldescription -> skill"}
		if name.Valid {
			record.Name = name.String
		}
		if record.Name == "" {
			record.NameStatus = string(resolutionUnknown)
		} else {
			record.NameStatus = string(resolutionVerified)
		}
		if hasRuntimeBinding && !rawLevelGroup.Valid {
			record.SourceTable = "EntitySkillPreload.SkillId -> skilldescription (group UNKNOWN)"
		}
		record.NameKind = "SkillName"
		if rawLevelGroup.Valid && rawGroup.Valid {
			roles := roleGroups[rawGroup.String]
			if len(roles) == 1 {
				for roleID := range roles {
					record.RoleID = roleID
					record.RoleStatus = string(resolutionCorrelated)
					if character, ok := master.characters[roleID]; ok {
						record.CharacterName = character.Name
					}
				}
			}
		}
		if _, exists := master.skills[id]; exists {
			return fmt.Errorf("duplicate skilldescription ID %d", id)
		}
		master.skills[id] = record
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate skill descriptions: %w", err)
	}
	return nil
}

func loadSQLiteDamage(db *sql.DB, hash string, master *gameMaster) error {
	rows, err := db.Query("SELECT id,calculate_type FROM damage WHERE calculate_type IS NOT NULL ORDER BY id")
	if err != nil {
		return fmt.Errorf("query damage classifications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID string
		var calculateType int32
		if err := rows.Scan(&rawID, &calculateType); err != nil {
			return fmt.Errorf("read damage classification: %w", err)
		}
		id, err := parseMasterID(rawID, "damage")
		if err != nil {
			return err
		}
		if calculateType < 0 || calculateType > 2 {
			return fmt.Errorf("damage ID %d has unsupported CalculateType %d", id, calculateType)
		}
		master.damage[id] = damageMasterRecord{ID: id, CalculateType: calculateType, SourceHash: hash, SourceTable: "damage.id"}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate damage classifications: %w", err)
	}
	return nil
}

func loadSQLiteScene(db *sql.DB, master *gameMaster) error {
	rows, err := db.Query("SELECT instance_id,map_id FROM map_instance ORDER BY instance_id")
	if err != nil {
		return fmt.Errorf("query scene instances: %w", err)
	}
	for rows.Next() {
		var instanceRaw, mapRaw string
		if err := rows.Scan(&instanceRaw, &mapRaw); err != nil {
			rows.Close()
			return fmt.Errorf("read scene instance: %w", err)
		}
		instanceID, err := parseMasterID(instanceRaw, "map_instance.instance_id")
		if err != nil {
			rows.Close()
			return err
		}
		mapID, err := parseMasterID(mapRaw, "map_instance.map_id")
		if err != nil {
			rows.Close()
			return err
		}
		master.sceneIdentity.instanceMaps[instanceID] = mapID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate scene instances: %w", err)
	}
	rows.Close()

	monsterTargets := make(map[sceneLevelKey]map[uint64]struct{})
	rows, err = db.Query("SELECT map_id,entity_id,monster_id FROM map_entity_monster ORDER BY map_id,entity_id,monster_id")
	if err != nil {
		return fmt.Errorf("query scene monsters: %w", err)
	}
	for rows.Next() {
		var mapRaw, entityRaw, monsterRaw string
		if err := rows.Scan(&mapRaw, &entityRaw, &monsterRaw); err != nil {
			rows.Close()
			return fmt.Errorf("read scene monster relation: %w", err)
		}
		mapID, err := parseMasterID(mapRaw, "map_entity_monster.map_id")
		if err != nil {
			rows.Close()
			return err
		}
		entityID, err := parseMasterID(entityRaw, "map_entity_monster.entity_id")
		if err != nil {
			rows.Close()
			return err
		}
		monsterID, err := parseMasterID(monsterRaw, "map_entity_monster.monster_id")
		if err != nil {
			rows.Close()
			return err
		}
		if _, exists := master.monsters[monsterID]; !exists {
			rows.Close()
			return fmt.Errorf("scene relation references MonsterId %d absent from MonsterInfo", monsterID)
		}
		key := sceneLevelKey{mapID: mapID, entityID: entityID}
		if monsterTargets[key] == nil {
			monsterTargets[key] = make(map[uint64]struct{})
		}
		monsterTargets[key][monsterID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate scene monsters: %w", err)
	}
	rows.Close()
	for key, targets := range monsterTargets {
		if len(targets) == 1 {
			for monsterID := range targets {
				master.sceneIdentity.levelMonsters[key] = monsterID
			}
		}
	}

	if err := loadTemplateCatalog(db, master); err != nil {
		return err
	}
	return loadTemplateRelations(db, master)
}

func loadTemplateCatalog(db *sql.DB, master *gameMaster) error {
	hasTemplateLocales, err := masterTableExists(db, "templateconfig_locale")
	if err != nil {
		return err
	}
	query := "SELECT id,blueprint_type,raw_name,NULL,NULL,NULL FROM templateconfig ORDER BY id"
	args := []any{}
	if hasTemplateLocales {
		query = `SELECT t.id,t.blueprint_type,t.raw_name,l.name,l.name_text_key,l.subtitle FROM templateconfig t LEFT JOIN templateconfig_locale l ON l.id=t.id AND l.locale=? AND l.name_text_key=t.tid_name ORDER BY t.id`
		args = append(args, master.locale)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query scene template catalog: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID, blueprintType, rawName string
		var name, textKey, subtitle sql.NullString
		if err := rows.Scan(&rawID, &blueprintType, &rawName, &name, &textKey, &subtitle); err != nil {
			return fmt.Errorf("read scene template catalog: %w", err)
		}
		id, err := parseMasterID(rawID, "templateconfig")
		if err != nil {
			return err
		}
		candidate := sceneTemplateCandidate{TemplateID: id, BlueprintType: blueprintType, RawName: rawName, NameStatus: string(resolutionUnknown), Language: master.locale}
		if name.Valid && strings.TrimSpace(name.String) != "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(name.String)), "dnt/") && textKey.Valid && textKey.String != "" {
			candidate.Name = name.String
			candidate.NameTextKey = textKey.String
			candidate.NameStatus = string(resolutionVerified)
			candidate.Subtitle = subtitle.String
		}
		master.sceneIdentity.templateCatalog[id] = candidate
	}
	return rows.Err()
}

func loadTemplateRelations(db *sql.DB, master *gameMaster) error {
	rows, err := db.Query("SELECT map_id,entity_id,template_id FROM map_entity_template_ref ORDER BY map_id,entity_id,ordinal")
	if err != nil {
		return fmt.Errorf("query scene template relations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mapRaw, entityRaw, templateRaw string
		if err := rows.Scan(&mapRaw, &entityRaw, &templateRaw); err != nil {
			return fmt.Errorf("read scene template relation: %w", err)
		}
		mapID, err := parseMasterID(mapRaw, "map_entity_template_ref.map_id")
		if err != nil {
			return err
		}
		entityID, err := parseMasterID(entityRaw, "map_entity_template_ref.entity_id")
		if err != nil {
			return err
		}
		templateID, err := parseMasterID(templateRaw, "map_entity_template_ref.template_id")
		if err != nil {
			return err
		}
		candidate, exists := master.sceneIdentity.templateCatalog[templateID]
		if !exists {
			return fmt.Errorf("scene template relation references missing TemplateConfig Id %d", templateID)
		}
		key := sceneLevelKey{mapID: mapID, entityID: entityID}
		master.sceneIdentity.levelTemplates[key] = append(master.sceneIdentity.levelTemplates[key], candidate)
	}
	return rows.Err()
}

func parseMasterID(raw, field string) (uint64, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid non-zero unsigned ID %q in %s", raw, field)
	}
	return id, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func masterTableExists(db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect optional master table %s: %w", table, err)
	}
	return count == 1, nil
}
