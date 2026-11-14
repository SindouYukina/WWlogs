package capture

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	db, databasePath, hash, err := openMasterDatabase(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return loadMasterDataFromDatabase(db, databasePath, hash, locale)
}

func openMasterDatabase(path string) (*sql.DB, string, string, error) {
	if path == "" {
		db, err := openEmbeddedMasterDatabase()
		if err != nil {
			return nil, "", "", err
		}
		hash, err := embeddedMasterDatabaseHash()
		if err != nil {
			_ = db.Close()
			return nil, "", "", fmt.Errorf("hash embedded combat Master database: %w", err)
		}
		return db, "embedded", hash, nil
	}
	return openFileMasterDatabase(path)
}

func masterDatabaseSourceLabel(databasePath string) string {
	if databasePath == "embedded" {
		return "embedded"
	}
	return "external test override"
}

// openEmbeddedMasterDatabase opens the embedded file through the read-only VFS. immutable tells SQLite there is nothing to
// lock or recover, and temp_store keeps sorting in memory because this file system cannot create files. The pragmas are
// in the DSN so they hold for any connection the pool opens, and the page cache stays at SQLite's default bound.
func openEmbeddedMasterDatabase() (*sql.DB, error) {
	if embeddedMasterDatabase == "" {
		return nil, fmt.Errorf("embedded combat Master database is unavailable")
	}
	vfsName, err := embeddedMasterVFS()
	if err != nil {
		return nil, fmt.Errorf("register embedded combat Master file system: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+embeddedMasterDatabaseName+"?vfs="+vfsName+"&mode=ro&immutable=1&_pragma=query_only(1)&_pragma=temp_store(2)")
	if err != nil {
		return nil, fmt.Errorf("open embedded combat Master database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open embedded combat Master database: %w", err)
	}
	return db, nil
}

// embeddedMasterDatabaseHash hashes in chunks, because converting the whole string would copy the database to the heap.
func embeddedMasterDatabaseHash() (string, error) {
	hash := sha256.New()
	reader := struct{ io.Reader }{strings.NewReader(embeddedMasterDatabase)}
	if _, err := io.CopyBuffer(hash, reader, make([]byte, 1<<16)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func openFileMasterDatabase(path string) (*sql.DB, string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", "", fmt.Errorf("inspect master database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", "", fmt.Errorf("master database path is not a regular file: %s", path)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve master database path: %w", err)
	}
	uriPath := filepath.ToSlash(absPath)
	if len(uriPath) >= 2 && uriPath[1] == ':' {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", "", fmt.Errorf("open master database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		_ = db.Close()
		return nil, "", "", fmt.Errorf("set master database query-only: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, "", "", fmt.Errorf("open master database: %w", err)
	}

	hash, err := hashFile(path)
	if err != nil {
		_ = db.Close()
		return nil, "", "", fmt.Errorf("hash master database: %w", err)
	}
	return db, absPath, hash, nil
}

func loadMasterDataFromDatabase(db *sql.DB, databasePath, hash, locale string) (*gameMaster, error) {
	master := &gameMaster{
		manifest:                      masterManifest{Language: locale, ClientRegion: "Global", SourceHash: hash},
		databasePath:                  databasePath,
		locale:                        locale,
		characters:                    make(map[uint64]masterRecord),
		charactersSkin:                make(map[uint64]masterRecord),
		ambiguousSkins:                make(map[uint64]struct{}),
		monsters:                      make(map[uint64]masterRecord),
		skills:                        make(map[uint64]masterRecord),
		commonSkills:                  make(map[uint64]masterRecord),
		roleSkins:                     make(map[uint64]uint64),
		trialRoleParents:              make(map[uint64]uint64),
		phantomSkills:                 make(map[uint64]struct{}),
		visions:                       make(map[uint64]struct{}),
		concomitantVisionSummonSkills: make(map[uint64]struct{}),
		buffDefinitions:               make(map[uint64]struct{}),
		shieldDefinitions:             make(map[uint64]struct{}),
		commonBullets:                 make(map[uint64]string),
		preloadBullets:                make(map[uint64]struct{}),
		roleSkinMesh:                  make(map[uint64]uint64),
		modelActor:                    make(map[uint64]string),
		blueprintModel:                make(map[string]uint64),
		blueprintEntityTypes:          make(map[string]map[string]struct{}),
		summonBlueprint:               make(map[uint64]string),
		visionCast:                    make(map[uint64][]uint64),
		actorBullets:                  make(map[string]map[uint64]struct{}),
		assetPath:                     make(map[string]string),
		actorSkills:                   make(map[string]map[uint64]struct{}),
		skillActors:                   make(map[uint64]map[string]struct{}),
		commonBulletDamage:            make(map[uint64]uint64),
		damageOwners:                  make(map[uint64][]damageOwnerJSONL),
		commonBulletDevLabels:         make(map[uint64]*combatEventLabelTextJSONL),
		skillLabels:                   make(map[uint64]map[string]struct{}),
		skillLabelText:                make(map[uint64]map[string]localizedSkillLabel),
		visionDevLabels:               make(map[uint64]*combatEventLabelTextJSONL),
		buffOwners:                    make(map[uint64][]buffOwnerText),
		propertyNames:                 make(map[int64]propertyName),
		abnormalDamageIDs:             make(map[uint64]struct{}),
		negativeStatusExecutors:       make(map[uint64]uint64),
		elementNames:                  make(map[int32]string),
		damage:                        make(map[uint64]damageMasterRecord),
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
	if err := loadSQLiteTrialRoleParents(db, master); err != nil {
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
	if err := loadSQLiteConcomitantVisionSummonSkills(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteBuffDefinitions(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteBuffStaticParameters(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteBuffTerms(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteShieldDefinitions(db, master); err != nil {
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
	if err := loadSQLiteNegativeStatusDamage(db, master); err != nil {
		return nil, err
	}
	if err := loadSQLitePropertyNames(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteStateMachines(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteBuffOwners(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteSkillLabelText(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteDamageOwners(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteVisionDevLabels(db, locale, master); err != nil {
		return nil, err
	}
	if err := loadSQLiteDevLabelMap(db, "commonbullet", locale, master.commonBulletDevLabels); err != nil {
		return nil, err
	}
	if err := loadSQLiteElementNames(db, locale, master); err != nil {
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
	if err := loadSQLiteSkillNameSources(db, locale, master); err != nil {
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

// loadSQLiteTrialRoleParents reads the optional normalized TrialRoleInfo relation.
func loadSQLiteTrialRoleParents(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "trialroleinfo")
	if err != nil || !exists {
		return err
	}
	for _, column := range []string{"id", "parent_role_id"} {
		present, err := masterColumnExists(db, "trialroleinfo", column)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("trialroleinfo is missing required column %s", column)
		}
	}
	rows, err := db.Query("SELECT id,parent_role_id FROM trialroleinfo ORDER BY id")
	if err != nil {
		return fmt.Errorf("query TrialRoleInfo parent relations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID, rawParent string
		if err := rows.Scan(&rawID, &rawParent); err != nil {
			return fmt.Errorf("read TrialRoleInfo parent relation: %w", err)
		}
		id, err := parseMasterID(rawID, "trialroleinfo.id")
		if err != nil {
			return err
		}
		parentID, err := parseMasterID(rawParent, "trialroleinfo.parent_role_id")
		if err != nil {
			return err
		}
		if _, exists := master.characters[parentID]; !exists {
			return fmt.Errorf("TrialRoleInfo Id %d references missing RoleInfo Id %d", id, parentID)
		}
		if _, duplicate := master.trialRoleParents[id]; duplicate {
			return fmt.Errorf("duplicate TrialRoleInfo Id %d", id)
		}
		master.trialRoleParents[id] = parentID
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate TrialRoleInfo parent relations: %w", err)
	}
	return nil
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
	if err := loadSQLiteBlueprintEntityTypes(db, master); err != nil {
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
	hasRowDamage, err := masterColumnExists(db, "commonbullet", "damage_id")
	if err != nil {
		return err
	}
	if hasRowDamage {
		if err := pairs("commonbullet", "SELECT id,damage_id FROM commonbullet WHERE damage_id IS NOT NULL", func(a, b sql.NullString) error {
			bullet, ok, err := id(a, "commonbullet.id")
			if err != nil || !ok {
				return err
			}
			damage, ok, err := id(b, "commonbullet.damage_id")
			if ok {
				master.commonBulletDamage[bullet] = damage
			}
			return err
		}); err != nil {
			return err
		}
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
		owners := master.skillActors[skill]
		if owners == nil {
			owners = make(map[string]struct{})
			master.skillActors[skill] = owners
		}
		owners[a.String] = struct{}{}
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

// loadSQLiteBuffDefinitions reads normalized buff.Id keys when the table exists.
func loadSQLiteBuffDefinitions(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "buff")
	if err != nil || !exists {
		return err
	}
	present, err := masterColumnExists(db, "buff", "id")
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("buff is missing required column id")
	}
	rows, err := db.Query("SELECT id FROM buff ORDER BY id")
	if err != nil {
		return fmt.Errorf("query Buff definitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read Buff definition: %w", err)
		}
		id, err := parseMasterID(raw, "buff.id")
		if err != nil {
			return err
		}
		if _, duplicate := master.buffDefinitions[id]; duplicate {
			return fmt.Errorf("duplicate Buff definition Id %d", id)
		}
		master.buffDefinitions[id] = struct{}{}
	}
	return rows.Err()
}

// loadSQLiteBuffStaticParameters reads the definition fields of existing buff rows: the duration
// policy, the stack limit, the single static duration magnitude and the granted tag paths. Every
// table and column is optional so an older Master still loads. Rows without a buff.id are ignored.
func loadSQLiteBuffStaticParameters(db *sql.DB, master *gameMaster) error {
	if len(master.buffDefinitions) == 0 {
		return nil
	}
	statics := make(map[uint64]buffStaticParameters)
	exists, err := masterTableExists(db, "buff")
	if err != nil || !exists {
		return err
	}
	hasPolicy, err := masterColumnExists(db, "buff", "duration_policy")
	if err != nil {
		return err
	}
	hasLimit, err := masterColumnExists(db, "buff", "stack_limit_count")
	if err != nil {
		return err
	}
	hasAttribute, err := masterColumnExists(db, "buff", "game_attribute_id")
	if err != nil {
		return err
	}
	if hasPolicy || hasLimit || hasAttribute {
		rows, err := db.Query("SELECT id, " + nullableColumn(hasPolicy, "duration_policy") + ", " + nullableColumn(hasLimit, "stack_limit_count") + ", " + nullableColumn(hasAttribute, "game_attribute_id") + " FROM buff")
		if err != nil {
			return fmt.Errorf("query Buff static parameters: %w", err)
		}
		for rows.Next() {
			var raw string
			var policy, limit, attribute sql.NullInt64
			if err := rows.Scan(&raw, &policy, &limit, &attribute); err != nil {
				rows.Close()
				return fmt.Errorf("read Buff static parameters: %w", err)
			}
			id, err := parseMasterID(raw, "buff.id")
			if err != nil {
				rows.Close()
				return err
			}
			entry := statics[id]
			if policy.Valid {
				entry.durationPolicy, entry.hasDurationPolicy = policy.Int64, true
			}
			if limit.Valid && limit.Int64 >= 0 {
				entry.stackLimit, entry.hasStackLimit = uint64(limit.Int64), true
			}
			if attribute.Valid && attribute.Int64 > 0 {
				entry.attribute, entry.hasAttribute = uint64(attribute.Int64), true
			}
			statics[id] = entry
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	if err := loadBuffMagnitudes(db, statics); err != nil {
		return err
	}
	if err := loadBuffGrantedTags(db, statics); err != nil {
		return err
	}
	if err := loadBuffModifierPresence(db, statics); err != nil {
		return err
	}
	if err := loadBuffHUDCues(db, statics); err != nil {
		return err
	}
	master.buffStatics = make(map[uint64]buffStaticParameters, len(statics))
	for id, entry := range statics {
		if _, defined := master.buffDefinitions[id]; defined {
			master.buffStatics[id] = entry
		}
	}
	return nil
}

var richTextTag = regexp.MustCompile(`<[^>]*>`)

// loadSQLiteBuffTerms reads buff_term_ref joined to the localized term text. Both tables are optional.
// A relation whose evidence is not VERIFIED or CORRELATED, or whose buff row is absent, is ignored.
func loadSQLiteBuffTerms(db *sql.DB, locale string, master *gameMaster) error {
	for _, table := range []string{"buff_term_ref", "term_locale"} {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
	}
	rows, err := db.Query(`SELECT r.buff_id, r.term_id, r.evidence_status, l.name, l.description
		FROM buff_term_ref r JOIN term_locale l ON l.id = r.term_id AND l.locale = ? ORDER BY r.buff_id`, locale)
	if err != nil {
		return fmt.Errorf("query Buff term references: %w", err)
	}
	defer rows.Close()
	terms := make(map[uint64]effectTermRecord)
	for rows.Next() {
		var rawBuff, rawTerm, evidence, name, description string
		if err := rows.Scan(&rawBuff, &rawTerm, &evidence, &name, &description); err != nil {
			return fmt.Errorf("read Buff term reference: %w", err)
		}
		buffID, err := parseMasterID(rawBuff, "buff_term_ref.buff_id")
		if err != nil {
			return err
		}
		termID, err := parseMasterID(rawTerm, "buff_term_ref.term_id")
		if err != nil {
			return err
		}
		status := resolutionStatus(evidence)
		if status != resolutionVerified && status != resolutionCorrelated {
			continue
		}
		if _, defined := master.buffDefinitions[buffID]; !defined || name == "" {
			continue
		}
		terms[buffID] = effectTermRecord{termID: termID, name: name, description: strings.TrimSpace(richTextTag.ReplaceAllString(description, "")), status: status}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	master.buffTerms = terms
	return nil
}

func nullableColumn(present bool, name string) string {
	if present {
		return name
	}
	return "NULL"
}

// loadBuffMagnitudes keeps the duration magnitude only for rows that list exactly one value;
// several values would need a rule to pick one.
func loadBuffMagnitudes(db *sql.DB, statics map[uint64]buffStaticParameters) error {
	exists, err := masterTableExists(db, "buff_duration_magnitude")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT owner_id, value FROM buff_duration_magnitude ORDER BY owner_id, ordinal")
	if err != nil {
		return fmt.Errorf("query Buff duration magnitudes: %w", err)
	}
	defer rows.Close()
	counts := make(map[uint64]int)
	values := make(map[uint64]float64)
	for rows.Next() {
		var raw string
		var value sql.NullFloat64
		if err := rows.Scan(&raw, &value); err != nil {
			return fmt.Errorf("read Buff duration magnitude: %w", err)
		}
		id, err := parseMasterID(raw, "buff_duration_magnitude.owner_id")
		if err != nil {
			return err
		}
		counts[id]++
		if value.Valid {
			values[id] = value.Float64
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, count := range counts {
		value, ok := values[id]
		if count != 1 || !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		entry := statics[id]
		entry.durationSeconds, entry.hasDuration = value, true
		statics[id] = entry
	}
	return nil
}

// loadBuffModifierPresence marks rows that list at least one modifier magnitude. The value itself is
// not kept: a magnitude can be a raw amount, a percentage or a calculation input, and nothing here needs it.
func loadBuffModifierPresence(db *sql.DB, statics map[uint64]buffStaticParameters) error {
	exists, err := masterTableExists(db, "buff_modifier_magnitude")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT DISTINCT owner_id FROM buff_modifier_magnitude")
	if err != nil {
		return fmt.Errorf("query Buff modifier magnitudes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read Buff modifier magnitude: %w", err)
		}
		id, err := parseMasterID(raw, "buff_modifier_magnitude.owner_id")
		if err != nil {
			return err
		}
		entry := statics[id]
		entry.hasModifier = true
		statics[id] = entry
	}
	return rows.Err()
}

// maxBuffStaticTags bounds the tag paths kept for one buff row.
const maxBuffStaticTags = 8

func loadBuffGrantedTags(db *sql.DB, statics map[uint64]buffStaticParameters) error {
	exists, err := masterTableExists(db, "buff_granted_tags")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT owner_id, value FROM buff_granted_tags ORDER BY owner_id, ordinal")
	if err != nil {
		return fmt.Errorf("query Buff granted tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var tag sql.NullString
		if err := rows.Scan(&raw, &tag); err != nil {
			return fmt.Errorf("read Buff granted tag: %w", err)
		}
		id, err := parseMasterID(raw, "buff_granted_tags.owner_id")
		if err != nil {
			return err
		}
		entry := statics[id]
		if tag.Valid && tag.String != "" && len(entry.tags) < maxBuffStaticTags {
			entry.tags = append(entry.tags, tag.String)
			statics[id] = entry
		}
	}
	return rows.Err()
}

// loadSQLiteShieldDefinitions reads normalized shield.Id keys when the table exists.
func loadSQLiteShieldDefinitions(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "shield")
	if err != nil || !exists {
		return err
	}
	present, err := masterColumnExists(db, "shield", "id")
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("shield is missing required column id")
	}
	rows, err := db.Query("SELECT id FROM shield ORDER BY id")
	if err != nil {
		return fmt.Errorf("query Shield definitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read Shield definition: %w", err)
		}
		id, err := parseMasterID(raw, "shield.id")
		if err != nil {
			return err
		}
		if _, duplicate := master.shieldDefinitions[id]; duplicate {
			return fmt.Errorf("duplicate Shield definition Id %d", id)
		}
		master.shieldDefinitions[id] = struct{}{}
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

// loadSQLiteConcomitantVisionSummonSkills reads the normalized Type 1 Vision crosswalk when present.
func loadSQLiteConcomitantVisionSummonSkills(db *sql.DB, master *gameMaster) error {
	var viewCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='view' AND name='summon_skill_vision_ref'",
	).Scan(&viewCount); err != nil {
		return fmt.Errorf("check SummonSkillId Vision crosswalk: %w", err)
	}
	if viewCount == 0 {
		return nil
	}
	rows, err := db.Query(
		"SELECT summon_type, summon_skill_id, vision_id, phantom_skill_id " +
			"FROM summon_skill_vision_ref ORDER BY summon_type, summon_skill_id",
	)
	if err != nil {
		return fmt.Errorf("query SummonSkillId Vision crosswalk: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var summonType int64
		var summonRaw, visionRaw, phantomRaw string
		if err := rows.Scan(&summonType, &summonRaw, &visionRaw, &phantomRaw); err != nil {
			return fmt.Errorf("read SummonSkillId Vision crosswalk: %w", err)
		}
		if summonType != summonTypeConcomitantVision {
			return fmt.Errorf("unsupported SummonSkillId Vision crosswalk Type %d", summonType)
		}
		summonID, err := parseMasterID(summonRaw, "summon_skill_vision_ref.summon_skill_id")
		if err != nil {
			return err
		}
		visionID, err := parseMasterID(visionRaw, "summon_skill_vision_ref.vision_id")
		if err != nil {
			return err
		}
		phantomID, err := parseMasterID(phantomRaw, "summon_skill_vision_ref.phantom_skill_id")
		if err != nil {
			return err
		}
		if summonID != visionID || summonID != phantomID {
			return fmt.Errorf(
				"inconsistent SummonSkillId Vision crosswalk: summon=%d vision=%d phantom=%d",
				summonID, visionID, phantomID,
			)
		}
		if _, duplicate := master.concomitantVisionSummonSkills[summonID]; duplicate {
			return fmt.Errorf("duplicate ConcomitantVision SummonSkillId crosswalk %d", summonID)
		}
		master.concomitantVisionSummonSkills[summonID] = struct{}{}
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
	// damage.element is optional so an older master without the column still loads.
	hasElement, err := masterColumnExists(db, "damage", "element")
	if err != nil {
		return err
	}
	elementColumn := "NULL"
	if hasElement {
		elementColumn = "element"
	}
	rows, err := db.Query("SELECT id,calculate_type," + elementColumn + " FROM damage WHERE calculate_type IS NOT NULL ORDER BY id")
	if err != nil {
		return fmt.Errorf("query damage classifications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawID string
		var calculateType int32
		var element sql.NullInt32
		if err := rows.Scan(&rawID, &calculateType, &element); err != nil {
			return fmt.Errorf("read damage classification: %w", err)
		}
		id, err := parseMasterID(rawID, "damage")
		if err != nil {
			return err
		}
		if calculateType < 0 || calculateType > 2 {
			return fmt.Errorf("damage ID %d has unsupported CalculateType %d", id, calculateType)
		}
		master.damage[id] = damageMasterRecord{ID: id, CalculateType: calculateType, Element: element.Int32, HasElement: element.Valid, SourceHash: hash, SourceTable: "damage.id"}
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
	if err := rows.Err(); err != nil {
		return err
	}
	return loadSQLiteTemplateDevLabels(db, master)
}

// loadSQLiteTemplateDevLabels attaches the optional developer note of templates that have no official name.
// The master locale wins; the zh-CN original is the fallback.
func loadSQLiteTemplateDevLabels(db *sql.DB, master *gameMaster) error {
	rows, err := queryDevLabels(db, "templateconfig", master.locale)
	if err != nil || rows == nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("query template developer labels: %w", err)
	}
	defer rows.Close()
	chosen := make(map[uint64]*combatEventLabelTextJSONL)
	for rows.Next() {
		var raw string
		label := &combatEventLabelTextJSONL{Evidence: "dev_label_locale row for this TemplateConfig id; an internal developer note, not an official in-game name"}
		if err := rows.Scan(&raw, &label.Locale, &label.Text, &label.Kind); err != nil {
			return fmt.Errorf("read template developer label: %w", err)
		}
		id, err := parseMasterID(raw, "dev_label_locale.id")
		if err != nil {
			return err
		}
		if previous, ok := chosen[id]; !ok || label.Locale == master.locale && previous.Locale != master.locale {
			chosen[id] = label
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, label := range chosen {
		candidate, ok := master.sceneIdentity.templateCatalog[id]
		if !ok || candidate.Name != "" {
			continue
		}
		candidate.DevLabel = label
		master.sceneIdentity.templateCatalog[id] = candidate
	}
	return nil
}

func loadTemplateRelations(db *sql.DB, master *gameMaster) error {
	rows, err := db.Query("SELECT map_id,entity_id,template_id FROM map_entity_template_ref ORDER BY map_id,entity_id,ordinal")
	if err != nil {
		return fmt.Errorf("query scene template relations: %w", err)
	}
	defer rows.Close()
	// Almost every level entity has exactly one template, so entities of one template share one read-only slice instead of
	// each holding a copy of the candidate. A second template on a key appends to a fresh slice, never to a shared one.
	single := make(map[uint64][]sceneTemplateCandidate)
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
		existing, repeated := master.sceneIdentity.levelTemplates[key]
		if repeated {
			master.sceneIdentity.levelTemplates[key] = append(existing[:len(existing):len(existing)], candidate)
			continue
		}
		shared, known := single[templateID]
		if !known {
			shared = []sceneTemplateCandidate{candidate}
			single[templateID] = shared
		}
		master.sceneIdentity.levelTemplates[key] = shared
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

func loadSQLiteBlueprintEntityTypes(db *sql.DB, master *gameMaster) error {
	hasEntityType, err := masterColumnExists(db, "blueprintconfig", "entity_type")
	if err != nil || !hasEntityType {
		return err
	}
	rows, err := db.Query("SELECT blueprint_type,entity_type FROM blueprintconfig WHERE blueprint_type IS NOT NULL AND entity_type IS NOT NULL")
	if err != nil {
		return fmt.Errorf("query blueprint entity types: %w", err)
	}
	defer rows.Close()
	if master.blueprintEntityTypes == nil {
		master.blueprintEntityTypes = make(map[string]map[string]struct{})
	}
	for rows.Next() {
		var blueprintType, entityType string
		if err := rows.Scan(&blueprintType, &entityType); err != nil {
			return fmt.Errorf("read blueprint entity type: %w", err)
		}
		if blueprintType == "" || entityType == "" {
			continue
		}
		values := master.blueprintEntityTypes[blueprintType]
		if values == nil {
			values = make(map[string]struct{})
			master.blueprintEntityTypes[blueprintType] = values
		}
		values[entityType] = struct{}{}
	}
	return rows.Err()
}
