package history

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/driver"

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
	unkeyed, err := driver.Open(fileURI(path, url.Values{"mode": {"ro"}}))
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
	if year, err := reopened.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
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
	if year, err := openTestStore(t, path, nil).Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
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
