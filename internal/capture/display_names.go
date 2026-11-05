package capture

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
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
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := filepath.ToSlash(absolute)
	if len(uri) > 1 && uri[1] == ':' {
		uri = "/" + uri
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: uri}).String()+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
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
	if len(r.order) >= maxDisplayNames {
		delete(r.names, r.order[0])
		r.order = r.order[1:]
	}
	r.names[key] = result
	r.order = append(r.order, key)
	return result, nil
}
