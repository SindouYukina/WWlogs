package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

var (
	benchmarkMasterSource      *gameMaster
	benchmarkMasterRecord      masterRecord
	benchmarkMasterDamage      damageMasterRecord
	benchmarkMasterText        string
	benchmarkMasterFound       bool
	benchmarkMasterDisplayName DisplayName
)

func TestEmbeddedMasterMatchesTrackedReadOnlyDatabase(t *testing.T) {
	sourcePath := filepath.Join("..", "..", "data", "master_data.db")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.Sum256(source)
	wantHash := hex.EncodeToString(sourceHash[:])
	if got := sha256.Sum256(embeddedMasterDatabase); got != sourceHash {
		t.Fatal("embedded Master bytes differ from the tracked database")
	}

	embedded, err := loadGameMasterDatabase("", "zh-TW")
	if err != nil {
		t.Fatalf("load embedded Master through SQLite: %v", err)
	}
	external, err := loadGameMasterDatabase(sourcePath, "zh-TW")
	if err != nil {
		t.Fatalf("load explicit read-only test override: %v", err)
	}
	if embedded.databasePath != "embedded" || embedded.manifest.SourceHash != wantHash {
		t.Fatalf("embedded source identity = %q, %q; want embedded and %s", embedded.databasePath, embedded.manifest.SourceHash, wantHash)
	}
	if external.manifest.SourceHash != wantHash {
		t.Fatalf("external override hash = %s, want %s", external.manifest.SourceHash, wantHash)
	}
	if _, ok := embedded.characters[1409]; !ok {
		t.Fatal("embedded Master did not resolve known Character ID 1409")
	}
	if _, ok := embedded.monsters[310000010]; !ok {
		t.Fatal("embedded Master did not resolve known Monster ID 310000010")
	}
	if len(embedded.skills) == 0 || len(embedded.buffDefinitions) == 0 || len(embedded.commonBullets) == 0 || len(embedded.damage) == 0 {
		t.Fatalf("embedded Master omitted a required lookup family: skills=%d buffs=%d bullets=%d damage=%d", len(embedded.skills), len(embedded.buffDefinitions), len(embedded.commonBullets), len(embedded.damage))
	}
	if embedded.locale != external.locale || embedded.manifest != external.manifest {
		t.Fatalf("embedded and external Master metadata differ: embedded=%+v external=%+v", embedded.manifest, external.manifest)
	}
	// The paths intentionally differ; all Master lookup data and its schema-derived maps must match.
	external.databasePath = embedded.databasePath
	if !reflect.DeepEqual(embedded, external) {
		t.Fatal("embedded SQLite queries and explicit file override produced different Master lookups")
	}
}

func TestEmbeddedMasterSQLiteIsReadOnlyAndUsesOneConnection(t *testing.T) {
	db, err := openEmbeddedMasterDatabase()
	if err != nil {
		t.Fatalf("open embedded Master SQLite database: %v", err)
	}
	defer db.Close()
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount == 0 {
		t.Fatal("embedded Master has no SQLite tables")
	}
	if _, err := db.Exec("CREATE TABLE should_not_exist(id INTEGER)"); err == nil {
		t.Fatal("embedded Master accepted a write")
	}
	if got := db.Stats().OpenConnections; got > 1 {
		t.Fatalf("embedded Master opened %d SQLite connections, want at most one", got)
	}
}

func TestDesktopMasterAndDisplayResolverShareOneSQLiteConnection(t *testing.T) {
	masters, err := LoadMasterData("", "zh-TW")
	if err != nil {
		t.Fatalf("load embedded Desktop Master: %v", err)
	}
	t.Cleanup(func() { _ = masters.Close() })
	display := masters.DisplayResolver()
	if display == nil {
		t.Fatal("Desktop Master has no display resolver")
	}
	resolved, err := display.Resolve(context.Background(), "role", 1409, "zh-TW")
	if err != nil {
		t.Fatalf("query localized name through shared SQLite connection: %v", err)
	}
	if resolved.Name == "" || resolved.Name != masters.masters.characters[1409].Name {
		t.Fatalf("display lookup = %+v, loaded Master record = %+v", resolved, masters.masters.characters[1409])
	}
	if got := display.db.Stats().OpenConnections; got != 1 {
		t.Fatalf("Desktop Master has %d open SQLite connections, want exactly one", got)
	}
	locales := []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}
	errorsFound := make(chan error, 20)
	var workers sync.WaitGroup
	for index := range 20 {
		locale := locales[index%len(locales)]
		workers.Add(1)
		go func() {
			defer workers.Done()
			name, err := display.Resolve(context.Background(), "role", 1409, locale)
			if err != nil {
				errorsFound <- err
				return
			}
			if name.Name == "" {
				errorsFound <- fmt.Errorf("locale %s returned an empty role name", locale)
			}
		}()
	}
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if got := display.db.Stats().OpenConnections; got != 1 {
		t.Fatalf("concurrent lookups opened %d SQLite connections, want one shared connection", got)
	}
	if err := masters.Close(); err != nil {
		t.Fatal(err)
	}
	if got := display.db.Stats().OpenConnections; got != 0 {
		t.Fatalf("closed Desktop Master retains %d open SQLite connections", got)
	}
}

func BenchmarkLoadMasterDatabase(b *testing.B) {
	filePath := filepath.Join("..", "..", "data", "master_data.db")
	for _, source := range []struct {
		name string
		path string
	}{{name: "embedded", path: ""}, {name: "external_file", path: filePath}} {
		b.Run(source.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				master, err := loadGameMasterDatabase(source.path, "zh-TW")
				if err != nil {
					b.Fatal(err)
				}
				benchmarkMasterSource = master
			}
		})
	}
}

func BenchmarkMasterMapLookups(b *testing.B) {
	master, err := loadGameMasterDatabase("", "zh-TW")
	if err != nil {
		b.Fatal(err)
	}
	characterID := uint64(1409)
	skillID := minimumMasterID(master.skills)
	buffID := minimumMasterID(master.buffDefinitions)
	bulletID := minimumMasterID(master.commonBullets)
	damageID := minimumMasterID(master.damage)
	b.Run("character", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkMasterRecord, benchmarkMasterFound = master.characters[characterID]
		}
	})
	b.Run("skill", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkMasterRecord, benchmarkMasterFound = master.skills[skillID]
		}
	})
	b.Run("buff", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_, benchmarkMasterFound = master.buffDefinitions[buffID]
		}
	})
	b.Run("bullet", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkMasterText, benchmarkMasterFound = master.commonBullets[bulletID]
		}
	})
	b.Run("damage", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkMasterDamage, benchmarkMasterFound = master.damage[damageID]
		}
	})
}

func BenchmarkMasterDisplaySQLiteLookup(b *testing.B) {
	masters, err := LoadMasterData("", "zh-TW")
	if err != nil {
		b.Fatal(err)
	}
	defer masters.Close()
	display := masters.DisplayResolver()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		display.mu.Lock()
		clear(display.names)
		display.order = display.order[:0]
		display.mu.Unlock()
		benchmarkMasterDisplayName, err = display.Resolve(context.Background(), "role", 1409, "zh-TW")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func minimumMasterID[V any](values map[uint64]V) uint64 {
	var minimum uint64
	for id := range values {
		if minimum == 0 || id < minimum {
			minimum = id
		}
	}
	return minimum
}
