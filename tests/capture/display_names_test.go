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
		for _, row := range [][3]string{{"42", "en-US", "Example"}, {"42", "zh-TW", "測試"}, {"42", "zh-CN", "测试"}, {"42", "ko-KR", "테스트"},
			// The client's placeholder is no text: 43 falls back to English, 44 has no name, 45 only an empty row.
			{"43", "zh-CN", "dnt/测试"}, {"43", "ja-JP", " DNT/テスト"}, {"43", "en-US", "Placeholder fallback"},
			{"44", "zh-CN", "dnt/仅占位"}, {"44", "en-US", "dnt/placeholder"}, {"45", "zh-TW", ""}} {
			if _, err = db.Exec("INSERT INTO "+table+" VALUES (?,?,?)", row[0], row[1], row[2]); err != nil {
				t.Fatal(err)
			}
		}
	}
	db.Close()
	completeMasterFixture(t, path)
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
		for _, locale := range []string{"zh-CN", "ja-JP"} {
			if name, err := resolver.Resolve(ctx, kind, 43, locale); err != nil || name.Name != "Placeholder fallback" || name.Locale != "en-US" || name.Status != "VERIFIED" {
				t.Fatalf("%s %s placeholder: %+v %v, want the English name", kind, locale, name, err)
			}
		}
		for _, c := range []struct {
			id     uint64
			locale string
		}{{44, "zh-CN"}, {44, "en-US"}, {45, "zh-TW"}} {
			if name, err := resolver.Resolve(ctx, kind, c.id, c.locale); err != nil || name.Name != "" || name.Status != "UNKNOWN" {
				t.Fatalf("%s %d %s: %+v %v, want no name", kind, c.id, c.locale, name, err)
			}
		}
	}
	if _, err = resolver.Resolve(ctx, "entity", 42, "en-US"); err == nil {
		t.Fatal("unapproved lookup kind accepted")
	}
	resolver.mu.Lock()
	resolver.names = newDisplayCache[DisplayName]()
	for i := 0; i < maxDisplayNames; i++ {
		resolver.names.put(string(rune(i+100)), DisplayName{})
	}
	resolver.mu.Unlock()
	if _, err = resolver.Resolve(ctx, "role", 42, "en-US"); err != nil {
		t.Fatal(err)
	}
	if len(resolver.names.values) != maxDisplayNames || len(resolver.names.order) != maxDisplayNames {
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
	completeMasterFixture(t, path)
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
			if err == sql.ErrNoRows || !masterOfficialText(expected) {
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
