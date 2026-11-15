package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Collectors for the owner-operated acceptance of a build: they run only when their environment variables are set.

// TestLegacyFixtureCollector writes a WWlogs v0.7.0 history into the directory WWLOGS_LEGACY_FIXTURE_OUT: the encrypted
// database and its key file, the key sealed by DPAPI for the current Windows user as v0.7.0 sealed it, with
// WWLOGS_LEGACY_FIXTURE_ENCOUNTERS (default 30) made-up encounters two days ago. A real build migrates it on its first open,
// so an end-to-end run can watch the migration without a copy of anyone's history. Nothing derives from this computer.
func TestLegacyFixtureCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_LEGACY_FIXTURE_OUT")
	if out == "" {
		t.Skip("WWLOGS_LEGACY_FIXTURE_OUT is not set")
	}
	encounters := 30
	if value := os.Getenv("WWLOGS_LEGACY_FIXTURE_ENCOUNTERS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("WWLOGS_LEGACY_FIXTURE_ENCOUNTERS = %q", value)
		}
		encounters = parsed
	}
	path := filepath.Join(out, "combat-history.db")
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists already", path)
	}
	store := legacyStore(t, path)
	recordEncounters(t, store, time.Now().Add(-48*time.Hour).Truncate(time.Hour), 0, encounters)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".lock"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// TestHistoryCopyCollector opens a copy of the history directory WWLOGS_HISTORY_COPY_DIR with the real inputs of a build
// (this computer's SMBIOS table and the protocol key the build embeds) and writes WWLOGS_HISTORY_COPY_OUT: the store's state,
// reason and migration, its encounter count and the row count of every table. It shows that a backup of the owner's
// history opens where it was taken and holds what it should, without opening the history itself. It runs only in a test
// binary that carries the embedded key (scripts/embed-key.go -test-binary ./internal/history) and writes no key material.
func TestHistoryCopyCollector(t *testing.T) {
	dir, out := os.Getenv("WWLOGS_HISTORY_COPY_DIR"), os.Getenv("WWLOGS_HISTORY_COPY_OUT")
	if dir == "" || out == "" {
		t.Skip("WWLOGS_HISTORY_COPY_DIR and WWLOGS_HISTORY_COPY_OUT are not set")
	}
	material, err := capture.ProtocolKeyMaterial()
	if err != nil {
		t.Skip("this test binary carries no embedded protocol key")
	}
	clear(material)
	path := copyFixture(t, dir)
	store, openErr := openWith(path, KeyInputs{ProtocolKey: capture.ProtocolKeyMaterial})
	defer store.Close()
	status := store.Status()
	report := map[string]any{"state": status.State, "reason": status.Reason, "migration": status.Migration, "schemaVersion": status.SchemaVersion,
		"recoveredSessions": status.RecoveredSessions, "recoveredEncounters": status.RecoveredEncounters}
	if openErr == nil {
		ctx := context.Background()
		tables, err := readHistoryTables(ctx, store.db)
		if err != nil {
			t.Fatal(err)
		}
		rows, total := map[string]int64{}, int64(0)
		for name, table := range tables.tables {
			rows[name], total = table.rows, total+table.rows
		}
		var listed int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM encounter WHERE state IN "+listedStates).Scan(&listed); err != nil {
			t.Fatal(err)
		}
		var check string
		if err := store.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
			t.Fatal(err)
		}
		report["tables"], report["rows"], report["totalRows"], report["listedEncounters"], report["integrityCheck"] = len(rows), rows, total, listed, check
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", data)
}
