package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// TestHistoryCompatibilityCollector is a collector, not a regular test. With WWLOGS_HISTORY_COMPAT_FIXTURES naming a
// directory of history fixtures (one directory each, holding a combat-history.db as an earlier build left it: its key,
// write-ahead log and pre-migration copies beside it) and WWLOGS_HISTORY_COMPAT_OUT naming a JSON file, it reads every
// fixture as this build does and writes what it found, so two builds (two SQLite driver versions) can be compared on the
// same files. Every step works on a fresh copy; the fixtures are never changed.
func TestHistoryCompatibilityCollector(t *testing.T) {
	fixtures, out := os.Getenv("WWLOGS_HISTORY_COMPAT_FIXTURES"), os.Getenv("WWLOGS_HISTORY_COMPAT_OUT")
	if fixtures == "" || out == "" {
		t.Skip("set WWLOGS_HISTORY_COMPAT_FIXTURES and WWLOGS_HISTORY_COMPAT_OUT to compare history files across builds")
	}
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]*compatResult{}
	for _, entry := range entries {
		if entry.IsDir() {
			results[entry.Name()] = collectFixture(t, filepath.Join(fixtures, entry.Name()))
		}
	}
	data, err := json.MarshalIndent(map[string]any{"driver": driverVersion(), "fixtures": results}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type compatResult struct {
	Inputs   map[string]string        `json:"inputs"`
	Raw      *compatSnapshot          `json:"raw"`
	Opened   *compatOpened            `json:"opened"`
	Appended *compatAppended          `json:"appended,omitempty"`
	Backups  map[string]*compatOpened `json:"backups,omitempty"`
	Errors   []string                 `json:"errors,omitempty"`
}

type compatSnapshot struct {
	ApplicationID   int64                  `json:"applicationId"`
	UserVersion     int64                  `json:"userVersion"`
	Integrity       string                 `json:"integrity"`
	ForeignKeyCheck int                    `json:"foreignKeyViolations"`
	Tables          map[string]compatTable `json:"tables"`
}

type compatTable struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

type compatOpened struct {
	Status    Status                    `json:"status"`
	OpenMs    float64                   `json:"openMs"`
	Snapshot  *compatSnapshot           `json:"snapshot"`
	Views     *compatViews              `json:"views,omitempty"`
	Leftovers map[string]compatLeftover `json:"files"`
}

type compatLeftover struct {
	Bytes     int64 `json:"bytes"`
	Plaintext bool  `json:"plaintext"`
}

type compatViews struct {
	Years      int                       `json:"years"`
	Days       int                       `json:"days"`
	Encounters int                       `json:"encounters"`
	SHA256     string                    `json:"sha256"`
	Cold       map[string]latencySummary `json:"cold"`
	Warm       map[string]latencySummary `json:"warm"`
}

type compatAppended struct {
	TotalBefore int             `json:"totalBefore"`
	TotalAfter  int             `json:"totalAfter"`
	Reopened    Status          `json:"reopened"`
	Snapshot    *compatSnapshot `json:"snapshot"`
}

var compatNow = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

func compatOptions(path string) Options {
	return Options{Path: path, Location: time.UTC, Now: func() time.Time { return compatNow }, Build: &testBuild, ProtocolSchema: "test schema", MasterSHA256: "test-master"}
}

// copyFixture copies every file of a fixture except its lock file into a new directory; it returns the history path there.
func copyFixture(t *testing.T, from string, rename map[string]string) string {
	t.Helper()
	to := filepath.Join(t.TempDir(), "history")
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".json") {
			continue
		}
		target := name
		if renamed, ok := rename[name]; ok {
			target = renamed
		}
		copyFile(t, filepath.Join(from, name), filepath.Join(to, target))
	}
	return filepath.Join(to, "combat-history.db")
}

func collectFixture(t *testing.T, directory string) *compatResult {
	result := &compatResult{Inputs: map[string]string{}}
	fail := func(step string, err error) { result.Errors = append(result.Errors, step+": "+err.Error()) }
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if !entry.IsDir() {
			result.Inputs[entry.Name()] = fixtureDigest(filepath.Join(directory, entry.Name()))
		}
	}
	// What the file holds, read-only, before anything opens it to write.
	raw, err := rawSnapshot(copyFixture(t, directory, nil))
	if err != nil {
		fail("raw", err)
	}
	result.Raw = raw
	// The normal open: recovery, migration or conversion as this build does them, then every History view.
	path := copyFixture(t, directory, nil)
	opened, store := openFixture(t, path, true)
	result.Opened = opened
	if store != nil {
		appended, err := appendAndReopen(t, store, path)
		if err != nil {
			fail("append", err)
		}
		result.Appended = appended
	}
	// Pre-migration copies, opened as the history they back up.
	for _, entry := range entries {
		name := entry.Name()
		if !strings.Contains(name, ".schema-") || !strings.HasSuffix(name, ".bak") {
			continue
		}
		if result.Backups == nil {
			result.Backups = map[string]*compatOpened{}
		}
		backupPath := copyFixture(t, directory, map[string]string{name: "combat-history.db", "combat-history.db": "original.db"})
		_ = os.Remove(filepath.Join(filepath.Dir(backupPath), "original.db"))
		for _, leftover := range []string{"combat-history.db-wal", "combat-history.db-shm"} {
			_ = os.Remove(filepath.Join(filepath.Dir(backupPath), leftover))
		}
		backup, store := openFixture(t, backupPath, false)
		if store != nil {
			_ = store.Close()
			backup.Leftovers = listFiles(filepath.Dir(backupPath))
		}
		result.Backups[name] = backup
	}
	return result
}

func fixtureDigest(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "error: " + err.Error()
	}
	defer file.Close()
	h := sha256.New()
	_, _ = io.Copy(h, file)
	return hex.EncodeToString(h.Sum(nil))
}

// rawSnapshot reads a history file read-only: encrypted with its key, or a plaintext file of a build before encryption.
func rawSnapshot(path string) (*compatSnapshot, error) {
	kind, err := inspectFile(path)
	if err != nil {
		return nil, err
	}
	var db *sql.DB
	if kind == filePlain {
		db, err = openPlain(path, true)
	} else {
		var key []byte
		if key, err = loadKey(path, DPAPIKeyProvider(), false); err == nil {
			db, err = openEncrypted(path, key, true)
		}
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return snapshotOf(context.Background(), db)
}

// snapshotOf hashes every row of every table, in a fixed order, as the values SQLite returns: two files with the same
// snapshot hold the same rows whatever their bytes on disk.
func snapshotOf(ctx context.Context, db *sql.DB) (*compatSnapshot, error) {
	s := &compatSnapshot{Tables: map[string]compatTable{}}
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&s.ApplicationID); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&s.UserVersion); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&s.Integrity); err != nil {
		return nil, err
	}
	violations, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	for violations.Next() {
		s.ForeignKeyCheck++
	}
	violations.Close()
	tables, err := tableNames(ctx, db)
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+quoted)
		if err != nil {
			return nil, err
		}
		columns, _ := rows.Columns()
		var lines []string
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			var b strings.Builder
			for _, v := range values {
				b.WriteString(compatValue(v))
				b.WriteByte('\x1f')
			}
			lines = append(lines, b.String())
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		slices.Sort(lines)
		h := sha256.New()
		_, _ = io.WriteString(h, strings.Join(columns, ",")+"\n")
		for _, line := range lines {
			_, _ = io.WriteString(h, line+"\n")
		}
		s.Tables[table] = compatTable{Rows: len(lines), SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	return s, nil
}

func compatValue(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case int64:
		return "i" + strconv.FormatInt(v, 10)
	case float64:
		return "f" + strconv.FormatFloat(v, 'g', -1, 64)
	case string:
		return "s" + v
	case []byte:
		return "b" + hex.EncodeToString(v)
	case time.Time:
		return "t" + strconv.FormatInt(v.UnixNano(), 10)
	case bool:
		return "o" + strconv.FormatBool(v)
	default:
		return fmt.Sprintf("?%T:%v", v, v)
	}
}

func listFiles(directory string) map[string]compatLeftover {
	out := map[string]compatLeftover{}
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || entry.IsDir() {
			continue
		}
		kind, _ := inspectFile(filepath.Join(directory, entry.Name()))
		out[entry.Name()] = compatLeftover{Bytes: info.Size(), Plaintext: kind == filePlain}
	}
	return out
}

// openFixture opens a copied fixture as WWlogs does, snapshots it and, when views is set, reads every History view twice:
// first from a cold connection pool, then warm. The store stays open for the caller.
func openFixture(t *testing.T, path string, views bool) (*compatOpened, *Store) {
	started := time.Now()
	store, err := Open(compatOptions(path))
	opened := &compatOpened{Status: store.Status(), OpenMs: float64(time.Since(started).Microseconds()) / 1000}
	if err != nil {
		opened.Leftovers = listFiles(filepath.Dir(path))
		return opened, nil
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if snapshot, err := snapshotOf(ctx, store.db); err == nil {
		opened.Snapshot = snapshot
	}
	if views {
		cold, viewsResult := readViews(ctx, store)
		warm, _ := readViews(ctx, store)
		viewsResult.Cold, viewsResult.Warm = cold, warm
		opened.Views = viewsResult
	}
	opened.Leftovers = listFiles(filepath.Dir(path))
	return opened, store
}

// readViews reads every year, every day page and every encounter detail the History view can show and hashes them.
func readViews(ctx context.Context, store *Store) (map[string]latencySummary, *compatViews) {
	timing := map[string]*latencies{"year": {}, "day": {}, "encounter": {}}
	h := sha256.New()
	result := &compatViews{}
	write := func(h hash.Hash, value any) {
		data, err := json.Marshal(value)
		if err != nil {
			data = []byte("error: " + err.Error())
		}
		h.Write(data)
		h.Write([]byte{'\n'})
	}
	started := time.Now()
	first, err := store.Year(ctx, compatNow.Year())
	timing["year"].add(time.Since(started))
	if err != nil {
		write(h, "year error: "+err.Error())
	}
	for year := first.FirstYear; err == nil && year != 0 && year <= first.LastYear; year++ {
		started = time.Now()
		view, err := store.Year(ctx, year)
		timing["year"].add(time.Since(started))
		write(h, view)
		write(h, err)
		result.Years++
		for _, day := range view.Days {
			for offset := 0; ; offset += MaxDayPage {
				started = time.Now()
				page, err := store.Day(ctx, day.Date, offset, MaxDayPage)
				timing["day"].add(time.Since(started))
				write(h, page)
				write(h, err)
				result.Days++
				for _, summary := range page.Encounters {
					started = time.Now()
					detail, err := store.Encounter(ctx, summary.ID)
					timing["encounter"].add(time.Since(started))
					write(h, detail)
					write(h, detail.Snapshot)
					write(h, detail.Timeline)
					write(h, detail.Enemies)
					write(h, err)
					result.Encounters++
				}
				if err != nil || offset+MaxDayPage >= page.Total {
					break
				}
			}
		}
	}
	result.SHA256 = hex.EncodeToString(h.Sum(nil))
	summaries := map[string]latencySummary{}
	for kind, l := range timing {
		summaries[kind] = l.summary()
	}
	return summaries, result
}

// appendAndReopen records one more encounter into the opened fixture, closes it and opens it again.
func appendAndReopen(t *testing.T, store *Store, path string) (*compatAppended, error) {
	ctx := context.Background()
	before, err := store.Year(ctx, compatNow.Year())
	if err != nil {
		return nil, err
	}
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		return nil, err
	}
	recorder.Submit(finishedEncounter(9, 1, compatNow.Add(9*time.Hour), 30, 4321, capture.IntegrityIntact), 0)
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		return nil, err
	}
	after, err := store.Year(ctx, compatNow.Year())
	if err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	reopened, err := Open(compatOptions(path))
	if err != nil {
		return nil, err
	}
	defer reopened.Close()
	snapshot, err := snapshotOf(ctx, reopened.db)
	if err != nil {
		return nil, err
	}
	// The appended session's identity (session_uid) is random: that table compares by its row count only.
	if value, ok := snapshot.Tables["capture_session"]; ok {
		value.SHA256 = ""
		snapshot.Tables["capture_session"] = value
	}
	return &compatAppended{TotalBefore: before.Total, TotalAfter: after.Total, Reopened: reopened.Status(), Snapshot: snapshot}, nil
}
