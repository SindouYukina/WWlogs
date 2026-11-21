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
	db *sql.DB
	mu sync.Mutex
	// names, modes and contents cache the answers of Resolve (and the lookups built on it), InstanceMode and
	// InstanceContent, each within maxDisplayNames.
	names    displayCache[DisplayName]
	modes    displayCache[InstanceMode]
	contents displayCache[InstanceContent]
}

const maxDisplayNames = 2048

// displayCache is a bounded first-in, first-out cache of display answers.
type displayCache[V any] struct {
	values map[string]V
	order  []string
}

func newDisplayCache[V any]() displayCache[V] { return displayCache[V]{values: map[string]V{}} }

func (c *displayCache[V]) get(key string) (V, bool) {
	value, ok := c.values[key]
	return value, ok
}

func (c *displayCache[V]) put(key string, value V) {
	if _, ok := c.values[key]; !ok {
		if len(c.order) >= maxDisplayNames {
			delete(c.values, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
	}
	c.values[key] = value
}

// OpenDisplayResolver opens a Master that provides the runtime schema (checkMasterRuntimeSchema) for display lookups and
// refuses any other, as the loader does.
func OpenDisplayResolver(path string) (*DisplayResolver, error) {
	db, _, _, err := openMasterDatabase(path)
	if err != nil {
		return nil, err
	}
	if err := checkMasterRuntimeSchema(db); err != nil {
		_ = db.Close()
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
	return &DisplayResolver{db: db, names: newDisplayCache[DisplayName](), modes: newDisplayCache[InstanceMode](), contents: newDisplayCache[InstanceContent]()}, nil
}

func (r *DisplayResolver) Close() error { return r.db.Close() }

// Resolve uses official localized rows and then English; a placeholder row is no name (masterOfficialText). An effect
// term lookup requires an existing term link; looking up a raw effect ID as a glossary ID is never allowed by the
// projection.
//
// "skill" is the UI skill-tree namespace (Skill.Id). "combat_skill" is the runtime namespace of DamageContext.SkillId,
// whose official names are SkillDescription rows; the two ID spaces are different and must not be mixed. "echo" is
// PhantomItem.Id; a skill row reaches "skill" or "echo" only through the derived sources of skill_names.go. "instance" is
// InstanceDungeon.Id, the namespace of JoinSceneNotify.SceneInfo.InstanceId; "template" is TemplateConfig.Id.
func (r *DisplayResolver) Resolve(ctx context.Context, kind string, id uint64, locale string) (DisplayName, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolveLocked(ctx, kind, id, locale)
}

func (r *DisplayResolver) resolveLocked(ctx context.Context, kind string, id uint64, locale string) (DisplayName, error) {
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
	if name, ok := r.names.get(key); ok {
		return name, nil
	}
	lookup := id
	if kind == "role" {
		static, err := r.staticRoleLocked(ctx, id)
		if err != nil {
			return DisplayName{Status: "UNKNOWN"}, err
		}
		lookup = static
	}
	result := DisplayName{Status: "UNKNOWN"}
	for _, candidate := range []string{locale, "en-US"} {
		var name string
		err := r.db.QueryRowContext(ctx, query, strconv.FormatUint(lookup, 10), candidate).Scan(&name)
		if err != nil && err != sql.ErrNoRows {
			return result, errors.New("Master display names could not be read")
		}
		if err == nil && masterOfficialText(name) {
			result = DisplayName{Name: name, Status: "VERIFIED", Locale: candidate}
			break
		}
	}
	r.names.put(key, result)
	return result, nil
}

// Role lookups that the Master cannot answer. A RoleId that trialroleinfo or roleinfo does not list is not one of them: it
// is not a trial, or has no element. Neither error is cached, so a later lookup reads the tables again.
var (
	// errRoleTables is a failed read of trialroleinfo or roleinfo (SQLite, or the caller's context ending).
	errRoleTables = errors.New("Master role tables could not be read")
	// errRoleValue is a trialroleinfo parent or roleinfo element that is not a number; the loader refuses such a parent.
	errRoleValue = errors.New("Master role tables hold a value that is not an ID")
)

// staticRoleLocked is the RoleInfo a runtime RoleId reads its name and element from: a trial role's parent
// (trialroleinfo.parent_role_id, see GameMaster.StaticRoleID), else the RoleId itself. A failed lookup returns the RoleId
// with errRoleTables or errRoleValue. The caller holds mu.
func (r *DisplayResolver) staticRoleLocked(ctx context.Context, roleID uint64) (uint64, error) {
	key := "static-role:" + strconv.FormatUint(roleID, 10)
	if cached, ok := r.names.get(key); ok {
		parent, _ := strconv.ParseUint(cached.Name, 10, 64)
		return parent, nil
	}
	parent := roleID
	var text sql.NullString
	switch err := r.db.QueryRowContext(ctx, "SELECT parent_role_id FROM trialroleinfo WHERE id=?", strconv.FormatUint(roleID, 10)).Scan(&text); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return roleID, errRoleTables
	default:
		// The same rule as loadSQLiteTrialRoleParents (parseMasterID): a trial row names a non-zero parent.
		value, err := strconv.ParseUint(text.String, 10, 64)
		if !text.Valid || err != nil || value == 0 {
			return roleID, errRoleValue
		}
		parent = value
	}
	r.names.put(key, DisplayName{Name: strconv.FormatUint(parent, 10)})
	return parent, nil
}

// RoleElement returns a role's static element ID from Master. A role the Master does not list, or lists without an
// element, has element zero, and the UI then uses its neutral series color. A failed lookup returns zero with
// errRoleTables or errRoleValue, which the caller reports like a failed name lookup.
func (r *DisplayResolver) RoleElement(ctx context.Context, roleID uint64) (uint64, error) {
	key := "element:" + strconv.FormatUint(roleID, 10)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.names.get(key); ok {
		element, _ := strconv.ParseUint(cached.Name, 10, 64)
		return element, nil
	}
	static, err := r.staticRoleLocked(ctx, roleID)
	if err != nil {
		return 0, err
	}
	var element uint64
	var text sql.NullString
	switch err := r.db.QueryRowContext(ctx, "SELECT element_id FROM roleinfo WHERE id=?", strconv.FormatUint(static, 10)).Scan(&text); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, errRoleTables
	case text.Valid && text.String != "":
		value, err := strconv.ParseUint(text.String, 10, 64)
		if err != nil {
			return 0, errRoleValue
		}
		element = value
	}
	r.names.put(key, DisplayName{Name: strconv.FormatUint(element, 10)})
	return element, nil
}

// StaticRole returns the RoleId whose static data and character art describe a runtime RoleId: a trial role's parent,
// else the RoleId itself. Images are addressed by it, so a trial character shows its parent's avatar and portrait the way
// it shows its parent's name and element. It selects images only and never affects statistics. A failed lookup returns
// the RoleId itself with errRoleTables or errRoleValue.
func (r *DisplayResolver) StaticRole(ctx context.Context, roleID uint64) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.staticRoleLocked(ctx, roleID)
}
