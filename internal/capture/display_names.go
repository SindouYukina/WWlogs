package capture

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
)

// DisplayName preserves the evidence level and the actual locale used for a display-only lookup.
type DisplayName struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Locale string `json:"locale"`
}

// DisplayResolver is a read-only, bounded cache of names. It never changes the capture Master's maps or statistics.
type DisplayResolver struct {
	db    *sql.DB
	mu    sync.Mutex
	names map[string]DisplayName
	order []string
	// modes caches InstanceMode answers within the same bound; modeTables says once whether the Master has the tables.
	modes      map[string]InstanceMode
	modeOrder  []string
	modeTables *bool
}

const maxDisplayNames = 2048

func OpenDisplayResolver(path string) (*DisplayResolver, error) {
	db, _, _, err := openMasterDatabase(path)
	if err != nil {
		return nil, err
	}
	resolver, err := newDisplayResolver(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return resolver, nil
}

func newDisplayResolver(db *sql.DB) (*DisplayResolver, error) {
	if err := db.Ping(); err != nil {
		return nil, errors.New("the display Master database is unavailable")
	}
	return &DisplayResolver{db: db, names: map[string]DisplayName{}, modes: map[string]InstanceMode{}}, nil
}

func (r *DisplayResolver) Close() error { return r.db.Close() }

// Resolve uses official localized rows and then English. An effect term lookup requires an existing term link;
// looking up a raw effect ID as a glossary ID is never allowed by the projection.
//
// "skill" is the UI skill-tree namespace (Skill.Id). "combat_skill" is the runtime namespace of DamageContext.SkillId,
// whose official names are SkillDescription rows; the two ID spaces are different and must not be mixed. "echo" is
// PhantomItem.Id; a skill row reaches "skill" or "echo" only through the derived sources of skill_names.go. "instance" is
// InstanceDungeon.Id, the namespace of JoinSceneNotify.SceneInfo.InstanceId; "template" is TemplateConfig.Id.
func (r *DisplayResolver) Resolve(ctx context.Context, kind string, id uint64, locale string) (DisplayName, error) {
	tables := map[string]string{"role": "roleinfo_locale", "skill": "skill_locale", "combat_skill": "skilldescription_locale", "echo": "phantomitem_locale", "monster": "monsterinfo_locale", "term": "term_locale", "instance": "instancedungeon_locale"}
	query := ""
	if table, ok := tables[kind]; ok {
		query = "SELECT name FROM " + table + " WHERE id=? AND locale=?"
	} else if kind == "template" {
		// A template's name is the locale row of its own TidName key, as the engine reads template candidates.
		query = "SELECT l.name FROM templateconfig t JOIN templateconfig_locale l ON l.id=t.id AND l.name_text_key=t.tid_name WHERE t.id=? AND l.locale=?"
	} else {
		return DisplayName{}, errors.New("unsupported Master name kind")
	}
	key := kind + ":" + strconv.FormatUint(id, 10) + ":" + locale
	r.mu.Lock()
	defer r.mu.Unlock()
	if name, ok := r.names[key]; ok {
		return name, nil
	}
	lookup := id
	if kind == "role" {
		lookup = r.staticRoleLocked(ctx, id)
	}
	result := DisplayName{Status: "UNKNOWN"}
	for _, candidate := range []string{locale, "en-US"} {
		var name string
		err := r.db.QueryRowContext(ctx, query, strconv.FormatUint(lookup, 10), candidate).Scan(&name)
		if err != nil && err != sql.ErrNoRows {
			return result, errors.New("Master display names could not be read")
		}
		if err == nil && name != "" {
			result = DisplayName{Name: name, Status: "VERIFIED", Locale: candidate}
			break
		}
	}
	r.remember(key, result)
	return result, nil
}

// staticRoleLocked is the RoleInfo a runtime RoleId reads its name and element from: a trial role's parent
// (trialroleinfo.parent_role_id, see gameMaster.staticRoleID), else the RoleId itself. A Master without the table
// has no trial roles. The caller holds mu.
func (r *DisplayResolver) staticRoleLocked(ctx context.Context, roleID uint64) uint64 {
	key := "static-role:" + strconv.FormatUint(roleID, 10)
	if cached, ok := r.names[key]; ok {
		parent, _ := strconv.ParseUint(cached.Name, 10, 64)
		return parent
	}
	parent := roleID
	var text string
	if err := r.db.QueryRowContext(ctx, "SELECT parent_role_id FROM trialroleinfo WHERE id=?", strconv.FormatUint(roleID, 10)).Scan(&text); err == nil {
		if value, err := strconv.ParseUint(text, 10, 64); err == nil && value != 0 {
			parent = value
		}
	}
	r.remember(key, DisplayName{Name: strconv.FormatUint(parent, 10)})
	return parent
}

// remember caches a lookup within the bound. The caller holds mu.
func (r *DisplayResolver) remember(key string, value DisplayName) {
	if len(r.order) >= maxDisplayNames {
		delete(r.names, r.order[0])
		r.order = r.order[1:]
	}
	r.names[key] = value
	r.order = append(r.order, key)
}

// RoleElement returns a role's static element ID from Master, or zero when it is unknown. It selects a display color
// only, so a failed lookup is not an error: the UI then uses its neutral series color.
func (r *DisplayResolver) RoleElement(ctx context.Context, roleID uint64) uint64 {
	key := "element:" + strconv.FormatUint(roleID, 10)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.names[key]; ok {
		element, _ := strconv.ParseUint(cached.Name, 10, 64)
		return element
	}
	var text sql.NullString
	if err := r.db.QueryRowContext(ctx, "SELECT element_id FROM roleinfo WHERE id=?", strconv.FormatUint(r.staticRoleLocked(ctx, roleID), 10)).Scan(&text); err != nil {
		text = sql.NullString{}
	}
	r.remember(key, DisplayName{Name: text.String})
	element, _ := strconv.ParseUint(text.String, 10, 64)
	return element
}

// StaticRole returns the RoleId whose static data and character art describe a runtime RoleId: a trial role's parent,
// else the RoleId itself. Images are addressed by it, so a trial character shows its parent's avatar and portrait the way
// it shows its parent's name and element. It selects images only and never affects statistics.
func (r *DisplayResolver) StaticRole(ctx context.Context, roleID uint64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.staticRoleLocked(ctx, roleID)
}
