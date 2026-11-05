package capture

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDisplayResolverFallbackAndBoundedCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"roleinfo_locale", "skill_locale", "monsterinfo_locale", "term_locale"} {
		if _, err = db.Exec("CREATE TABLE " + table + " (id TEXT,locale TEXT,name TEXT,PRIMARY KEY(id,locale))"); err != nil {
			t.Fatal(err)
		}
		for _, row := range [][3]string{{"42", "en-US", "Example"}, {"42", "zh-TW", "測試"}, {"42", "zh-CN", "测试"}, {"42", "ko-KR", "테스트"}} {
			if _, err = db.Exec("INSERT INTO "+table+" VALUES (?,?,?)", row[0], row[1], row[2]); err != nil {
				t.Fatal(err)
			}
		}
	}
	db.Close()
	resolver, err := OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	ctx := context.Background()
	for _, kind := range []string{"role", "skill", "monster", "term"} {
		name, err := resolver.Resolve(ctx, kind, 42, "zh-TW")
		if err != nil || name.Name != "測試" || name.Status != "VERIFIED" {
			t.Fatalf("localized name: %+v %v", name, err)
		}
		name, err = resolver.Resolve(ctx, kind, 42, "ja-JP")
		if err != nil || name.Name != "Example" || name.Locale != "en-US" {
			t.Fatalf("English fallback: %+v %v", name, err)
		}
		name, err = resolver.Resolve(ctx, kind, 999, "ja-JP")
		if err != nil || name.Name != "" || name.Status != "UNKNOWN" {
			t.Fatalf("unknown: %+v %v", name, err)
		}
	}
	if _, err = resolver.Resolve(ctx, "entity", 42, "en-US"); err == nil {
		t.Fatal("unapproved lookup kind accepted")
	}
	resolver.mu.Lock()
	resolver.names = map[string]DisplayName{}
	resolver.order = nil
	for i := 0; i < maxDisplayNames; i++ {
		key := string(rune(i + 100))
		resolver.names[key] = DisplayName{}
		resolver.order = append(resolver.order, key)
	}
	resolver.mu.Unlock()
	if _, err = resolver.Resolve(ctx, "role", 42, "en-US"); err != nil {
		t.Fatal(err)
	}
	if len(resolver.names) != maxDisplayNames || len(resolver.order) != maxDisplayNames {
		t.Fatal("unbounded cache")
	}
	resolver.db.Close()
	if name, err := resolver.Resolve(ctx, "role", 42, "en-US"); err != nil || name.Name != "Example" {
		t.Fatal("unchanged names were queried again instead of cached")
	}
}

// This optional integration check reads only a caller-supplied, reviewed Master database.
func TestDisplayResolverMasterLocales(t *testing.T) {
	path := os.Getenv("WWLOGS_DISPLAY_MASTER_DB")
	if path == "" {
		t.Skip("WWLOGS_DISPLAY_MASTER_DB is not supplied")
	}
	resolver, err := OpenDisplayResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	for kind, table := range map[string]string{"role": "roleinfo_locale", "skill": "skill_locale", "monster": "monsterinfo_locale", "term": "term_locale"} {
		var raw string
		if err := resolver.db.QueryRow("SELECT id FROM " + table + " WHERE locale='en-US' AND name<>'' ORDER BY id LIMIT 1").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		for _, locale := range []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"} {
			var expected string
			err := resolver.db.QueryRow("SELECT name FROM "+table+" WHERE id=? AND locale=?", raw, locale).Scan(&expected)
			if err == sql.ErrNoRows || expected == "" {
				if err := resolver.db.QueryRow("SELECT name FROM "+table+" WHERE id=? AND locale='en-US'", raw).Scan(&expected); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			name, err := resolver.Resolve(context.Background(), kind, id, locale)
			if err != nil || name.Name != expected || name.Status != "VERIFIED" {
				t.Fatalf("Master locale mismatch: kind=%s locale=%s", kind, locale)
			}
		}
	}
}
