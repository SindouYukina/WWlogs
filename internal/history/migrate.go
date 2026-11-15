package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Moving a v0.7.0 history to a derived key.
//
// A history WWlogs v0.7.0 made has a random key that DPAPI seals (keys.go). The first time this build opens one for
// recording, it copies the whole history into a new file encrypted with the derived key (keyderive.go), proves the copy holds
// exactly the same rows, and only then puts the copy in the history's place and removes the old file and its key file:
//
//  1. The v0.7.0 history is opened with its own key, which the preflight proved, and passes quick_check.
//  2. VACUUM INTO writes a consistent snapshot of it, its committed write-ahead log included, to combat-history.migrating.db
//     through the xts VFS with the derived key: no copy is ever written unencrypted.
//  3. The copy is proven: the preflight with the derived key, integrity_check, foreign_key_check, the same schema and, table
//     by table, the same row count and the same SHA-256 over every row in primary key order.
//  4. The v0.7.0 history is closed, which checkpoints its write-ahead log into its file and removes the log.
//  5. combat-history.migration records that the files are being switched, with the record of the copy's key; the old file
//     moves to combat-history.legacy.db, the copy to the history's name, and the record of the derived key (keyformat.go) is
//     written beside it.
//  6. The history opens with the derived key, as every derived history does. Only then are combat-history.legacy.db, the
//     key file and the migration record removed.
//
// Nothing of the history changes before step 5, and every step of 5 and 6 is a durable rename or a write of its own, so an
// interrupted migration (power loss, a killed process, a full disk) is finished or undone by the next open from the files it
// left (resumeMigration): until the copy is in the history's place the old history is restored as it was, and once it is,
// the migration finishes. A migration that cannot run (no derived key on this computer, no room for the copy, a copy that
// differs) changes nothing: the history stays readable and writable with its v0.7.0 key for this run, and the next open
// tries again. Replaying captures is never a substitute for the stored history.

const (
	migratingSuffix = ".migrating.db"
	legacySuffix    = ".legacy.db"
	migrationSuffix = ".migration"
)

// Migration of the open history (Status.Migration). They are internal facts, never shown as such.
const (
	// MigrationDone: this open moved a v0.7.0 history to a derived key.
	MigrationDone = "migrated"
	// MigrationFailed: a v0.7.0 history could not be moved and stays on its v0.7.0 key until the next open.
	MigrationFailed = "failed"
)

// ReasonMigration: an interrupted migration could be neither finished nor undone, so neither the old history nor its copy
// is opened; both are kept for the next open.
const ReasonMigration = "migration_failed"

// Migration stages in combat-history.migration.
const (
	stageSwitch  = "switch"
	stageCleanup = "cleanup"
)

// migrationStep runs after each named step of a migration; tests make it fail or end the process there.
var migrationStep = func(step string) error { return nil }

// historyFile names a file of the history at path: combat-history.db with ".legacy.db" is combat-history.legacy.db.
func historyFile(path, suffix string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + suffix
}

func (s *Store) migratingPath() string { return historyFile(s.path, migratingSuffix) }
func (s *Store) legacyPath() string    { return historyFile(s.path, legacySuffix) }
func (s *Store) migrationPath() string { return historyFile(s.path, migrationSuffix) }

// databaseFiles are a database and the logs SQLite keeps beside it.
func databaseFiles(path string) []string {
	return []string{path, path + "-wal", path + "-shm", path + "-journal"}
}

// removeDatabaseFiles removes a database and its logs; a file that does not exist is not an error.
func removeDatabaseFiles(path string) error {
	var failed []error
	for _, file := range databaseFiles(path) {
		if err := removeIfPresent(file); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// migrationMarker is combat-history.migration: the stage of a migration that is switching files, and the record of the copy's
// key, so the next open tells the copy from the old history without opening either.
type migrationMarker struct {
	Stage  string    `json:"stage"`
	Record keyRecord `json:"record"`
}

func readMigrationMarker(path string) (migrationMarker, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return migrationMarker{}, false, nil
	}
	if err != nil {
		return migrationMarker{}, false, fmt.Errorf("read migration record: %w", err)
	}
	var marker migrationMarker
	if json.Unmarshal(data, &marker) != nil || (marker.Stage != stageSwitch && marker.Stage != stageCleanup) || !marker.Record.wellFormed() {
		return migrationMarker{}, true, errors.New("the migration record is damaged")
	}
	return marker, true, nil
}

func writeMigrationMarker(path string, marker migrationMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if err := writeFileDurably(path, append(data, '\n'), true); err != nil {
		return fmt.Errorf("write migration record: %w", err)
	}
	return nil
}

// migrateLegacy moves the v0.7.0 history open in s.db to a derived key and leaves s.db open on the moved history. When the
// move cannot be made, the v0.7.0 history is open again as it was and Status.Migration says it failed; only an error that
// leaves neither history open is returned.
func (s *Store) migrateLegacy(ctx context.Context) error {
	derived, record, err := newHistoryKey(s.inputs)
	if err != nil {
		s.status.Migration = MigrationFailed
		return nil
	}
	target := s.migratingPath()
	fail := func() error {
		clear(derived.key)
		_ = removeDatabaseFiles(target)
		s.status.Migration = MigrationFailed
		if s.db != nil {
			return nil
		}
		return s.reopenLegacy(ctx)
	}
	if removeDatabaseFiles(target) != nil {
		return fail()
	}
	if err := s.copyHistory(ctx, target, derived.key); err != nil {
		return fail()
	}
	// The copy is proven; the old history is closed, which checkpoints its write-ahead log into its file.
	if err := s.db.Close(); err != nil {
		s.db = nil
		return fail()
	}
	s.db = nil
	for _, log := range []string{s.path + "-wal", s.path + "-journal"} {
		if info, err := os.Stat(log); err == nil && info.Size() > 0 {
			return fail()
		}
	}
	if migrationStep("closed") != nil {
		return fail()
	}
	marker := migrationMarker{Stage: stageSwitch, Record: record}
	if writeMigrationMarker(s.migrationPath(), marker) != nil || migrationStep("marked") != nil {
		_ = removeIfPresent(s.migrationPath())
		return fail()
	}
	if moveDurably(s.path, s.legacyPath(), false) != nil || migrationStep("moved-old") != nil {
		return s.abandonSwitch(ctx, derived.key, fail)
	}
	if moveDurably(target, s.path, false) != nil || migrationStep("moved-copy") != nil {
		return s.abandonSwitch(ctx, derived.key, fail)
	}
	if writeKeyRecord(s.path, record, true) != nil || migrationStep("recorded") != nil {
		return s.abandonSwitch(ctx, derived.key, fail)
	}
	marker.Stage = stageCleanup
	if writeMigrationMarker(s.migrationPath(), marker) != nil || migrationStep("cleanup-marked") != nil {
		return s.abandonSwitch(ctx, derived.key, fail)
	}
	legacyKey := s.key
	s.key, s.protection = derived.key, keyProtectionDerived
	if err := s.openMigrated(ctx); err != nil || migrationStep("opened") != nil {
		s.closeDatabase()
		s.key, s.protection = legacyKey, s.provider.ID()
		return s.abandonSwitch(ctx, derived.key, fail)
	}
	clear(legacyKey)
	s.finishMigration = true
	s.status.Migration = MigrationDone
	return nil
}

// copyHistory writes the open history to target encrypted with key and proves the copy holds the same rows. It never
// touches the open history's files.
func (s *Store) copyHistory(ctx context.Context, target string, key []byte) error {
	uri := fileURI(target, url.Values{"vfs": {encryptionVFS}, "hexkey": {hex.EncodeToString(key)}})
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", uri); err != nil {
		return fmt.Errorf("copy history: %w", err)
	}
	if err := migrationStep("copied"); err != nil {
		return err
	}
	if outcome, err := preflight(target, key, true); err != nil || outcome != preflightReady {
		return errors.Join(err, fmt.Errorf("the copy does not decrypt with its key (%s)", outcome))
	}
	source, err := readHistoryTables(ctx, s.db)
	if err != nil {
		return err
	}
	copied, err := openEncrypted(target, key, false)
	if err != nil {
		return err
	}
	err = proveCopy(ctx, copied, source)
	if closeErr := copied.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return migrationStep("verified")
}

// proveCopy checks the copy on its own and against the content of the history it was made from.
func proveCopy(ctx context.Context, copied *sql.DB, source historyTables) error {
	var integrity string
	if err := copied.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.Join(err, fmt.Errorf("the copy fails its integrity check: %s", integrity))
	}
	if err := noForeignKeyViolations(ctx, copied); err != nil {
		return err
	}
	identity, err := readIdentity(ctx, copied)
	if err != nil {
		return err
	}
	if err := checkIdentity(identity); err != nil {
		return err
	}
	content, err := readHistoryTables(ctx, copied)
	if err != nil {
		return err
	}
	if !content.equal(source) {
		return errors.New("the copy does not hold the same rows as the history")
	}
	return nil
}

func noForeignKeyViolations(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("the copy holds foreign key violations")
	}
	return rows.Err()
}

// openMigrated opens the moved history with the derived key, as the preflight and the store check every history.
func (s *Store) openMigrated(ctx context.Context) error {
	if outcome, err := preflight(s.path, s.key, true); err != nil || outcome != preflightReady {
		return errors.Join(err, fmt.Errorf("the moved history does not decrypt with its key (%s)", outcome))
	}
	db, err := openEncrypted(s.path, s.key, false)
	if err != nil {
		return err
	}
	s.db = db
	identity, err := readIdentity(ctx, db)
	if err == nil {
		err = checkIdentity(identity)
	}
	if err == nil {
		err = quickCheck(ctx, db)
	}
	return err
}

func (s *Store) closeDatabase() {
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
}

// abandonSwitch undoes a switch that did not complete: the copy goes back aside, the old history back in place, the record
// and the migration record are removed, and the old history opens again. When the files cannot be put back, the migration
// record stays for the next open and the store is unavailable.
func (s *Store) abandonSwitch(ctx context.Context, key []byte, fail func() error) error {
	if err := s.undoSwitch(); err != nil {
		clear(key)
		s.status.Migration = MigrationFailed
		return s.refuse(ReasonMigration, fmt.Errorf("undo history migration: %w", err))
	}
	return fail()
}

// undoSwitch puts the old history back in its place from whatever stage the switch reached. When the old file is aside, the
// file in the history's place is the copy: it goes back aside and is removed with its record. A copy beside the history is
// removed, then the migration record.
func (s *Store) undoSwitch() error {
	legacy, err := fileExists(s.legacyPath())
	if err != nil {
		return err
	}
	if legacy {
		current, err := fileExists(s.path)
		if err != nil {
			return err
		}
		if current {
			if err := removeDatabaseFiles(s.migratingPath()); err != nil {
				return err
			}
			if err := moveDurably(s.path, s.migratingPath(), false); err != nil {
				return err
			}
		}
		// The logs of the copy, should it have been opened; the old file was closed without any.
		for _, log := range []string{"-wal", "-shm", "-journal"} {
			if err := removeIfPresent(s.path + log); err != nil {
				return err
			}
		}
		if err := moveDurably(s.legacyPath(), s.path, false); err != nil {
			return err
		}
		if err := removeIfPresent(keyRecordPath(s.path)); err != nil {
			return err
		}
	}
	if err := removeDatabaseFiles(s.migratingPath()); err != nil {
		return err
	}
	return removeIfPresent(s.migrationPath())
}

// reopenLegacy opens the v0.7.0 history again with its key, after a migration that did not happen.
func (s *Store) reopenLegacy(ctx context.Context) error {
	if outcome, err := preflight(s.path, s.key, true); err != nil || outcome != preflightReady {
		return s.refuse(ReasonMigration, errors.Join(err, fmt.Errorf("reopen history after migration: %s", outcome)))
	}
	db, err := openEncrypted(s.path, s.key, false)
	if err != nil {
		return s.refuse(ReasonMigration, err)
	}
	s.db = db
	identity, err := readIdentity(ctx, db)
	if err == nil {
		err = checkIdentity(identity)
	}
	if err != nil {
		return s.refuse(ReasonMigration, err)
	}
	return nil
}

// resumeMigration finishes or undoes a migration an earlier run left between its steps, before the history's key is looked
// for. Without a migration record, a copy beside the history is an unfinished, unproven one and is removed: the old history
// is still in its place. With one, the copy keeps the history's place only when it is there and opens with the key its record
// names; otherwise the old history is put back as it was.
func (s *Store) resumeMigration() error {
	marker, present, err := readMigrationMarker(s.migrationPath())
	switch {
	case err != nil && !present:
		return s.refuse(ReasonOpenFailed, err)
	case !present:
		if err := removeDatabaseFiles(s.migratingPath()); err != nil {
			return s.refuse(ReasonOpenFailed, fmt.Errorf("remove an unfinished history copy: %w", err))
		}
		return nil
	}
	legacy, legacyErr := fileExists(s.legacyPath())
	current, currentErr := fileExists(s.path)
	if legacyErr != nil || currentErr != nil {
		return s.refuse(ReasonOpenFailed, errors.Join(legacyErr, currentErr))
	}
	if err == nil && legacy && !current {
		// The old file moved aside but the copy never took its place.
		if copied, _ := fileExists(s.migratingPath()); copied {
			if err := moveDurably(s.migratingPath(), s.path, false); err != nil {
				return s.refuse(ReasonMigration, err)
			}
			current = true
		}
	}
	switch {
	case err == nil && legacy && current && s.copyOpens(marker.Record):
		if err := writeKeyRecord(s.path, marker.Record, true); err != nil {
			return s.refuse(ReasonMigration, err)
		}
		marker.Stage = stageCleanup
		if err := writeMigrationMarker(s.migrationPath(), marker); err != nil {
			return s.refuse(ReasonMigration, err)
		}
		s.finishMigration = true
	case err == nil && !legacy && marker.Stage == stageCleanup:
		// Only the clean-up was left: the old file is gone, the copy is the history.
		s.finishMigration = true
	default:
		if err := s.undoSwitch(); err != nil {
			return s.refuse(ReasonMigration, fmt.Errorf("undo history migration: %w", err))
		}
	}
	return nil
}

// copyOpens reports whether the file in the history's place decrypts with the derived key record names.
func (s *Store) copyOpens(record keyRecord) bool {
	keys, _, err := existingHistoryKeys(s.inputs, record.Source)
	if err != nil {
		return false
	}
	defer wipeKeys(keys)
	match := matchingKey(keys, record)
	if match == nil {
		return false
	}
	outcome, err := preflight(s.path, match.key, true)
	return err == nil && outcome == preflightReady
}

// cleanUpMigration removes what a finished migration leaves: the old file, the v0.7.0 key file and the migration record, in
// that order, so an interrupted clean-up is finished by the next open. A failure only leaves them for then.
func (s *Store) cleanUpMigration() {
	if removeDatabaseFiles(s.legacyPath()) != nil || migrationStep("removed-old") != nil || removeIfPresent(keyPath(s.path)) != nil {
		return
	}
	_ = removeIfPresent(s.migrationPath())
}

// historyTables is what a history holds: its schema and, table by table, the row count and a SHA-256 over every row in
// primary key order. Two histories with equal tables hold the same rows.
type historyTables struct {
	schema string
	tables map[string]tableContent
}

type tableContent struct {
	rows   int64
	digest [sha256.Size]byte
}

func (h historyTables) equal(other historyTables) bool {
	if h.schema != other.schema || len(h.tables) != len(other.tables) {
		return false
	}
	for name, content := range h.tables {
		if other.tables[name] != content {
			return false
		}
	}
	return true
}

// readHistoryTables reads the tables of the history open in db.
func readHistoryTables(ctx context.Context, db *sql.DB) (historyTables, error) {
	content := historyTables{tables: map[string]tableContent{}}
	rows, err := db.QueryContext(ctx, "SELECT type, name, tbl_name, coalesce(sql, '') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		return content, fmt.Errorf("read history schema: %w", err)
	}
	var schema strings.Builder
	var tables []string
	for rows.Next() {
		var kind, name, table, text string
		if err := rows.Scan(&kind, &name, &table, &text); err != nil {
			rows.Close()
			return content, err
		}
		fmt.Fprintf(&schema, "%s\x00%s\x00%s\x00%s\x00", kind, name, table, text)
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return content, err
	}
	content.schema = schema.String()
	for _, table := range tables {
		digest, err := tableDigest(ctx, db, table)
		if err != nil {
			return content, err
		}
		content.tables[table] = digest
	}
	return content, nil
}

// tableDigest reads every row of table in primary key order (or, without one, ordered by every column) and hashes each
// value with its storage class.
func tableDigest(ctx context.Context, db *sql.DB, table string) (tableContent, error) {
	quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
	info, err := db.QueryContext(ctx, "SELECT cid, pk FROM pragma_table_info(?)", table)
	if err != nil {
		return tableContent{}, err
	}
	type column struct{ index, key int }
	var columns []column
	for info.Next() {
		var c column
		if err := info.Scan(&c.index, &c.key); err != nil {
			info.Close()
			return tableContent{}, err
		}
		columns = append(columns, c)
	}
	info.Close()
	if len(columns) == 0 {
		return tableContent{}, fmt.Errorf("table %s has no columns", table)
	}
	var order []string
	keyed := append([]column(nil), columns...)
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	for _, c := range keyed {
		if c.key > 0 {
			order = append(order, strconv.Itoa(c.index+1))
		}
	}
	if len(order) == 0 {
		for _, c := range columns {
			order = append(order, strconv.Itoa(c.index+1))
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+quoted+" ORDER BY "+strings.Join(order, ", "))
	if err != nil {
		return tableContent{}, err
	}
	defer rows.Close()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	digest := sha256.New()
	var content tableContent
	var buffer [9]byte
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return tableContent{}, err
		}
		for _, value := range values {
			switch v := value.(type) {
			case nil:
				digest.Write([]byte{'N'})
			case int64:
				buffer[0] = 'I'
				binary.BigEndian.PutUint64(buffer[1:], uint64(v))
				digest.Write(buffer[:])
			case float64:
				buffer[0] = 'R'
				binary.BigEndian.PutUint64(buffer[1:], math.Float64bits(v))
				digest.Write(buffer[:])
			case string:
				buffer[0] = 'T'
				binary.BigEndian.PutUint64(buffer[1:], uint64(len(v)))
				digest.Write(buffer[:])
				digest.Write([]byte(v))
			case []byte:
				buffer[0] = 'B'
				binary.BigEndian.PutUint64(buffer[1:], uint64(len(v)))
				digest.Write(buffer[:])
				digest.Write(v)
			default:
				return tableContent{}, fmt.Errorf("table %s holds a %T", table, value)
			}
		}
		content.rows++
	}
	if err := rows.Err(); err != nil {
		return tableContent{}, err
	}
	copy(content.digest[:], digest.Sum(nil))
	return content, nil
}
