package history

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// plaintextMarkers are strings every stored history contains; none of them may appear in a file on disk.
var plaintextMarkers = [][]byte{[]byte("SQLite format 3"), []byte("capture_session"), []byte("effect_event"), []byte("test-master"), []byte("battle_state_enter"), []byte("0123456789abcdef0123456789abcdef01234567")}

// requireNoPlaintext checks that no file of the history directory reads as plaintext history.
func requireNoPlaintext(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		// The writer lock file is empty and locked by the open store.
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		names = append(names, entry.Name())
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range plaintextMarkers {
			if bytes.Contains(data, marker) {
				t.Fatalf("%s contains the plaintext %q", entry.Name(), marker)
			}
		}
	}
	return names
}

func recordedStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := testPath(t)
	store := openTestStore(t, path, nil)
	entries := finishedEncounter(1, 1, time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC), 30, 1234, capture.IntegrityIntact)
	entries = append(entries, effectChange(1, 1, 0, capture.EffectChangeApply, capture.EffectObserved, time.Date(2026, 11, 11, 5, 0, 1, 0, time.UTC).Local(), nil))
	record(t, store, liveStart, entries)
	return store, path
}

func TestHistoryIsEncryptedOnDiskWhileOpenAndAfterClose(t *testing.T) {
	store, path := recordedStore(t)
	// While the store is open the newest pages may still be in the write-ahead log: it is encrypted too.
	names := requireNoPlaintext(t, filepath.Dir(path))
	if !containsName(names, "combat-history.db") || !containsName(names, "combat-history.key") {
		t.Fatalf("files = %v", names)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	requireNoPlaintext(t, filepath.Dir(path))
	// The file is not an SQLite database to anything without the key: neither to plain SQLite (modernc.org/sqlite, as an
	// earlier build or any SQLite tool reads it) nor to the history driver without the encrypting VFS.
	absolute, _ := filepath.Abs(path)
	plainReadOnly := "file:/" + filepath.ToSlash(absolute) + "?mode=ro"
	modernc, err := sql.Open("sqlite", plainReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	unkeyed, err := openPlain(path, true)
	if err != nil {
		t.Fatal(err)
	}
	for name, db := range map[string]*sql.DB{"modernc.org/sqlite": modernc, "ncruces without the VFS": unkeyed} {
		var count int
		err := db.QueryRow("SELECT count(*) FROM capture_session").Scan(&count)
		db.Close()
		if err == nil || !strings.Contains(err.Error(), "not a database") {
			t.Fatalf("%s read the encrypted file without its key: %v", name, err)
		}
	}
	// The same key reopens it.
	reopened := openTestStore(t, path, nil)
	if year, err := reopened.Year(context.Background(), 2026); err != nil || year.Total != 1 {
		t.Fatalf("reopened year = %+v (%v)", year, err)
	}
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// failingProvider stands in for a key provider that cannot unseal, such as DPAPI for another user.
type failingProvider struct{}

func (failingProvider) ID() string                    { return "dpapi-user" }
func (failingProvider) Seal([]byte) ([]byte, error)   { return nil, errors.New("sealing refused") }
func (failingProvider) Unseal([]byte) ([]byte, error) { return nil, errors.New("unsealing refused") }

func reopenFails(t *testing.T, path string, provider KeyProvider, reason string) {
	t.Helper()
	before := fileDigest(t, path)
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, KeyProvider: provider})
	if err == nil {
		t.Fatal("the history opened")
	}
	status := store.Status()
	if status.State != StateUnavailable || status.Reason != reason {
		t.Fatalf("status = %+v, want %s", status, reason)
	}
	if _, err := store.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("an unreadable history accepted a session: %v", err)
	}
	store.Close()
	if fileDigest(t, path) != before {
		t.Fatal("the history file was modified")
	}
}

func TestUnavailableKeyLeavesTheHistoryUntouched(t *testing.T) {
	store, path := recordedStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopenFails(t, path, failingProvider{}, ReasonKeyUnavailable)
	// A damaged sealed key is refused by DPAPI itself.
	key, err := os.ReadFile(keyPath(path))
	if err != nil {
		t.Fatal(err)
	}
	damaged := append([]byte(nil), key...)
	damaged[len(damaged)-5] ^= 0x40
	if err := os.WriteFile(keyPath(path), damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	reopenFails(t, path, nil, ReasonKeyUnavailable)
	if err := os.WriteFile(keyPath(path), key, 0o600); err != nil {
		t.Fatal(err)
	}
	openTestStore(t, path, nil)
}

func TestMissingKeyNeverGetsANewKey(t *testing.T) {
	store, path := recordedStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath(path)); err != nil {
		t.Fatal(err)
	}
	reopenFails(t, path, nil, ReasonKeyMissing)
	if _, err := os.Stat(keyPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a new key was created for an existing encrypted history")
	}
	if err := os.WriteFile(keyPath(path), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if year, err := openTestStore(t, path, nil).Year(context.Background(), 2026); err != nil || year.Total != 1 {
		t.Fatalf("restored key: %+v (%v)", year, err)
	}
}

func TestAnotherKeyDoesNotOpenTheHistory(t *testing.T) {
	store, path := recordedStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	other, otherPath := recordedStore(t)
	other.Close()
	foreignKey, err := os.ReadFile(keyPath(otherPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath(path), foreignKey, 0o600); err != nil {
		t.Fatal(err)
	}
	reopenFails(t, path, nil, ReasonKeyMismatch)
}

func TestSchemaMigrationCopyIsEncrypted(t *testing.T) {
	path := testPath(t)
	createSchemaTwo(t, path)
	store := openTestStore(t, path, nil)
	if status := store.Status(); status.PlaintextMigration != PlaintextConverted || !status.Encrypted {
		t.Fatalf("status = %+v", status)
	}
	backups, _ := filepath.Glob(path + ".schema-2-*.bak")
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if kind, err := inspectFile(backups[0]); err != nil || kind != fileOpaque {
		t.Fatalf("the pre-migration copy is %v (%v)", kind, err)
	}
	copyDB, err := openEncrypted(backups[0], store.key, true)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var version, sessions int
	if err := copyDB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("copy schema = %d (%v)", version, err)
	}
	if err := copyDB.QueryRow("SELECT count(*) FROM capture_session").Scan(&sessions); err != nil || sessions != 2 {
		t.Fatalf("copy sessions = %d (%v)", sessions, err)
	}
	store.Close()
	requireNoPlaintext(t, filepath.Dir(path))
}

// copyFile copies a file byte for byte.
func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPlaintextHistoryIsConvertedWithItsWriteAheadLog(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	createSchemaTwo(t, source)
	// An earlier build that ended abnormally leaves committed rows only in its write-ahead log: copy the files while the
	// writer is still open, so the copy has a log that was never checkpointed.
	writer, err := sql.Open("sqlite", plainDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO capture_session (id, session_uid, source, status, opened_at_ns, utc_offset_s, zone_name, history_level, app_version, app_build, app_commit, app_dirty, app_profile, protocol_schema, master_sha256)
		VALUES (3, 'only-in-the-log', 'live', 'open', 1700000200000000000, 0, 'UTC', 'events', 'v0.6.0', 0, 'c', 'false', 'public', 'p', 'test-master')`); err != nil {
		t.Fatal(err)
	}
	path := testPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, source, path)
	copyFile(t, source+"-wal", path+"-wal")
	writer.Close()
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the copied write-ahead log is empty: %v", err)
	}
	// A plaintext pre-migration copy of the earlier build and an interrupted earlier conversion lie beside it.
	copyFile(t, source, path+".schema-1-20261101T000000Z.bak")
	if err := os.WriteFile(path+".encrypting", []byte("an interrupted conversion"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, path, nil)
	status := store.Status()
	if status.State != StateReady || !status.Encrypted || status.PlaintextMigration != PlaintextConverted || status.PlaintextBackups != 0 || status.RecoveredSessions != 1 {
		t.Fatalf("status = %+v", status)
	}
	var uid, version string
	if err := store.db.QueryRow("SELECT session_uid, app_version FROM capture_session WHERE id = 3").Scan(&uid, &version); err != nil || uid != "only-in-the-log" || version != "v0.7.0" {
		t.Fatalf("the row that was only in the write-ahead log = %q %q (%v)", uid, version, err)
	}
	store.Close()
	names := requireNoPlaintext(t, filepath.Dir(path))
	for _, name := range names {
		if strings.HasSuffix(name, ".encrypting") || strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
			t.Fatalf("a conversion leftover remains: %v", names)
		}
	}
}

func TestFailedConversionLeavesThePlaintextHistoryUntouched(t *testing.T) {
	path := testPath(t)
	createSchemaTwo(t, path)
	before := fileDigest(t, path)
	// The conversion cannot write its copy: a directory occupies the copy's name.
	blocker := path + ".encrypting"
	if err := os.MkdirAll(filepath.Join(blocker, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if err == nil {
		t.Fatal("a failed conversion opened without an error")
	}
	status := store.Status()
	if status.State != StateUnavailable || status.Reason != ReasonEncryptionMigration || status.PlaintextMigration != PlaintextFailed || status.MigrationFailure != MigrationCopyFailed || status.Encrypted {
		t.Fatalf("status = %+v", status)
	}
	if _, err := store.Day(context.Background(), "2023-11-15", 0, 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unconverted history answered a query: %v", err)
	}
	if _, err := store.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("a plaintext history accepted a session: %v", err)
	}
	store.Close()
	if fileDigest(t, path) != before {
		t.Fatal("the plaintext history was modified by a failed conversion")
	}
	// Once the obstacle is gone the next open converts it.
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	if status := openTestStore(t, path, nil).Status(); status.State != StateReady || status.PlaintextMigration != PlaintextConverted {
		t.Fatalf("retry status = %+v", status)
	}
}

func TestConversionNeverFillsTheDisk(t *testing.T) {
	path := testPath(t)
	createSchemaTwo(t, path)
	before := fileDigest(t, path)
	availableSpace = func(string) (uint64, error) { return 1 << 20, nil }
	defer func() { availableSpace = freeSpace }()
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if err == nil || store.Status().MigrationFailure != MigrationInsufficientSpace || store.Status().State != StateUnavailable {
		t.Fatalf("status = %+v (%v)", store.Status(), err)
	}
	store.Close()
	if fileDigest(t, path) != before {
		t.Fatal("the plaintext history was modified")
	}
	if _, err := os.Stat(path + ".encrypting"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a partial copy was left behind")
	}
}

func TestKeyFileFormatIsVersioned(t *testing.T) {
	_, path := recordedStore(t)
	data, err := os.ReadFile(keyPath(path))
	if err != nil {
		t.Fatal(err)
	}
	id, sealed, err := parseKeyFile(data)
	if err != nil || id != "dpapi-user" || len(sealed) < historyKeySize {
		t.Fatalf("key file = %q, %d sealed bytes (%v)", id, len(sealed), err)
	}
	if bytes.Contains(data, []byte(keyFileMagic+"\x00")) {
		t.Fatal("the key file has no provider")
	}
	if err := writeKeyFile(keyPath(path), id, sealed); err == nil {
		t.Fatal("an existing key file was replaced")
	}
}
