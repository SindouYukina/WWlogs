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
	return &DisplayResolver{db: db, names: map[string]DisplayName{}}, nil
}

func (r *DisplayResolver) Close() error { return r.db.Close() }

// Resolve uses official localized rows and then English. An effect term lookup requires an existing term link;
// looking up a raw effect ID as a glossary ID is never allowed by the projection.
func (r *DisplayResolver) Resolve(ctx context.Context, kind string, id uint64, locale string) (DisplayName, error) {
	tables := map[string]string{"role": "roleinfo_locale", "skill": "skill_locale", "monster": "monsterinfo_locale", "term": "term_locale"}
	table, ok := tables[kind]
	if !ok {
		return DisplayName{}, errors.New("unsupported Master name kind")
	}
	key := kind + ":" + strconv.FormatUint(id, 10) + ":" + locale
	r.mu.Lock()
	defer r.mu.Unlock()
	if name, ok := r.names[key]; ok {
		return name, nil
	}
	result := DisplayName{Status: "UNKNOWN"}
	for _, candidate := range []string{locale, "en-US"} {
		var name string
		err := r.db.QueryRowContext(ctx, "SELECT name FROM "+table+" WHERE id=? AND locale=?", strconv.FormatUint(id, 10), candidate).Scan(&name)
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
	if err := r.db.QueryRowContext(ctx, "SELECT element_id FROM roleinfo WHERE id=?", strconv.FormatUint(roleID, 10)).Scan(&text); err != nil {
		text = sql.NullString{}
	}
	r.remember(key, DisplayName{Name: text.String})
	element, _ := strconv.ParseUint(text.String, 10, 64)
	return element
}
