package history

import (
	"context"
	"crypto/aes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/xts"

	"wwlogs/internal/capture"
)

// The preflight (preflight.go) and the logs it protects.
//
// Every fixture here is a history as v0.7.0 writes it: AES-256-XTS with a random key that DPAPI seals for the current user
// (keys.go). A fixture "with a log" holds committed encounters that exist only in its write-ahead log, as a crash, a forced
// termination or a copy taken while WWlogs runs leaves it. Every refused open must leave every file of the history byte for
// byte as it was (SHA-256 before and after), and the history must then read in full once the cause is removed.

// recordEncounters stores n finished encounters in one capture session, numbered from first.
func recordEncounters(t *testing.T, store *Store, start time.Time, first, n int) {
	t.Helper()
	batches := make([][]capture.HistoryEntry, 0, n)
	for i := 0; i < n; i++ {
		batches = append(batches, finishedEncounter(1, uint64(first+i+1), start.Add(time.Duration(i)*time.Minute), 30, int64(1000+first+i), capture.IntegrityIntact))
	}
	record(t, store, liveStart, batches...)
}

// historyFileSuffixes are the files of a history at a path: the database, its logs and its key file.
var historyFileSuffixes = []string{"", "-wal", "-shm", "-journal"}

// copyHistoryFiles copies the database, its logs, its key file and its key record from the history path from to the history
// path to, while the store that writes from may still hold them open.
func copyHistoryFiles(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{{keyPath(from), keyPath(to)}, {keyRecordPath(from), keyRecordPath(to)}}
	for _, suffix := range historyFileSuffixes {
		pairs = append(pairs, [2]string{from + suffix, to + suffix})
	}
	for _, pair := range pairs {
		data, err := os.ReadFile(pair[0])
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pair[1], data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// logFixture returns a copy of a v0.7.0 history whose first checkpointed encounters are in the database file and whose next
// inLog encounters were committed only to its write-ahead log. The copy is taken while the store that wrote it is still open.
func logFixture(t *testing.T, checkpointed, inLog int) string {
	t.Helper()
	path := testPath(t)
	return logFixtureOf(t, legacyStore(t, path), checkpointed, inLog)
}

// logFixtureOf records into store as logFixture does and returns the copy.
func logFixtureOf(t *testing.T, store *Store, checkpointed, inLog int) string {
	t.Helper()
	path := store.Path()
	start := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	if checkpointed > 0 {
		recordEncounters(t, store, start, 0, checkpointed)
	}
	if _, err := store.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if inLog > 0 {
		recordEncounters(t, store, start.Add(24*time.Hour), checkpointed, inLog)
	}
	copied := filepath.Join(t.TempDir(), "copy", "combat-history.db")
	copyHistoryFiles(t, path, copied)
	if inLog > 0 {
		if info, err := os.Stat(copied + "-wal"); err != nil || info.Size() == 0 {
			t.Fatalf("the fixture has no write-ahead log (%v)", err)
		}
	}
	return copied
}

// historyDigests is the SHA-256 of every file in directory but the writer lock, which the store creates empty and locks.
func historyDigests(t *testing.T, directory string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	digests := map[string][32]byte{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		digests[entry.Name()] = sha256.Sum256(data)
	}
	return digests
}

func requireSameFiles(t *testing.T, before, after map[string][32]byte) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("files before %v, after %v", fileNames(before), fileNames(after))
	}
	for name, digest := range before {
		if after[name] != digest {
			t.Fatalf("%s changed (files after: %v)", name, fileNames(after))
		}
	}
}

func fileNames(digests map[string][32]byte) []string {
	var names []string
	for name := range digests {
		names = append(names, name)
	}
	return names
}

// refusedKeepingFiles opens the history at path, expects it to be refused with reason, and checks that the open and the
// close after it changed no file of the history and created none.
func refusedKeepingFiles(t *testing.T, path string, provider KeyProvider, reason string) {
	t.Helper()
	before := historyDigests(t, filepath.Dir(path))
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, KeyProvider: provider})
	if err == nil {
		store.Close()
		t.Fatal("the history opened")
	}
	if status := store.Status(); status.State != StateUnavailable || status.Reason != reason {
		store.Close()
		t.Fatalf("status = %+v, want %s (%v)", status, reason, err)
	}
	if _, err := store.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("an unreadable history accepted a session: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
}

// encountersIn opens the history at path and counts its encounters, checking the file's integrity.
func encountersIn(t *testing.T, path string) int {
	t.Helper()
	store := openTestStore(t, path, nil)
	count := countRows(t, store, "encounter")
	assertIntegrity(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return count
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestWrongKeyNeverDiscardsTheWriteAheadLog reproduces the loss of the v0.7.0 open order and shows the store no longer causes
// it. A history keeps 100 encounters in its file and 40 more only in its write-ahead log; it is opened with the key file of
// another history of the same user, which DPAPI unseals, so the key is wrong rather than missing.
//
//   - The v0.7.0 order (the key goes to SQLite, readIdentity finds the mismatch, the connections close) loses the 40: SQLite
//     reads the log's header as noise, recovers nothing and deletes the log on close.
//   - The store refuses the key before SQLite sees it and leaves every file byte for byte as it was; with its own key the
//     history then reads all 140.
func TestWrongKeyNeverDiscardsTheWriteAheadLog(t *testing.T) {
	const checkpointed, inLog = 100, 40
	original := logFixture(t, checkpointed, inLog)
	ownKey := readFile(t, keyPath(original))
	other, otherPath := recordedLegacyStore(t)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	foreignKey := readFile(t, keyPath(otherPath))

	// The database file alone holds the first 100: the other 40 are only in the log.
	fileOnly := filepath.Join(t.TempDir(), "file-only", "combat-history.db")
	if err := os.MkdirAll(filepath.Dir(fileOnly), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, fileOnly, readFile(t, original))
	writeFile(t, keyPath(fileOnly), ownKey)
	if got := encountersIn(t, fileOnly); got != checkpointed {
		t.Fatalf("the database file alone holds %d encounters, want %d", got, checkpointed)
	}
	whole := filepath.Join(t.TempDir(), "whole", "combat-history.db")
	copyHistoryFiles(t, original, whole)
	if got := encountersIn(t, whole); got != checkpointed+inLog {
		t.Fatalf("the history with its log holds %d encounters, want %d", got, checkpointed+inLog)
	}

	// The v0.7.0 open order.
	naive := filepath.Join(t.TempDir(), "naive", "combat-history.db")
	copyHistoryFiles(t, original, naive)
	writeFile(t, keyPath(naive), foreignKey)
	wrongKey, err := loadKey(naive, defaultKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	db, err := openEncrypted(naive, wrongKey, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentity(context.Background(), db); !isNotADatabase(err) {
		t.Fatalf("the wrong key read the history: %v", err)
	}
	_ = db.Close()
	_, walErr := os.Stat(naive + "-wal")
	writeFile(t, keyPath(naive), ownKey)
	naiveKept := encountersIn(t, naive)
	if naiveKept != checkpointed || !errors.Is(walErr, os.ErrNotExist) {
		t.Fatalf("the v0.7.0 order kept %d of %d encounters (log after close: %v); the reproduction expects the %d of the log lost", naiveKept, checkpointed+inLog, walErr, inLog)
	}

	// The store.
	fixed := filepath.Join(t.TempDir(), "fixed", "combat-history.db")
	copyHistoryFiles(t, original, fixed)
	writeFile(t, keyPath(fixed), foreignKey)
	refusedKeepingFiles(t, fixed, nil, ReasonKeyMismatch)
	writeFile(t, keyPath(fixed), ownKey)
	fixedKept := encountersIn(t, fixed)
	if fixedKept != checkpointed+inLog {
		t.Fatalf("after a refused wrong key the history holds %d of %d encounters", fixedKept, checkpointed+inLog)
	}
	t.Logf("wrong key with %d encounters only in the log: v0.7.0 order kept %d of %d; preflight changed no file and kept %d of %d",
		inLog, naiveKept, checkpointed+inLog, fixedKept, checkpointed+inLog)
}

// TestReadableHistoriesPassThePreflight opens legacy histories the preflight must accept, with and without committed
// encounters in their logs, and checks that every one of them is read.
func TestReadableHistoriesPassThePreflight(t *testing.T) {
	for _, c := range []struct {
		name                string
		checkpointed, inLog int
	}{
		{"closed cleanly, no log", 50, 0},
		{"encounters only in the log", 100, 40},
		{"committed but never checkpointed", 0, 40},
		{"a large log", 100, 600},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := logFixture(t, c.checkpointed, c.inLog)
			outcome, err := preflight(path, mustLoadKey(t, path), true)
			if err != nil || outcome != preflightReady {
				t.Fatalf("preflight = %s (%v)", outcome, err)
			}
			if got := encountersIn(t, path); got != c.checkpointed+c.inLog {
				t.Fatalf("%d encounters, want %d", got, c.checkpointed+c.inLog)
			}
		})
	}
}

func mustLoadKey(t *testing.T, path string) []byte {
	t.Helper()
	key, err := loadKey(path, defaultKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clear(key) })
	return key
}

// encryptSector encrypts one sector as the xts VFS writes it at sector 0 of a file of the history at path.
func encryptSector(t *testing.T, path string, plaintext []byte) []byte {
	t.Helper()
	cipher, err := xts.NewCipher(aes.NewCipher, mustLoadKey(t, path))
	if err != nil {
		t.Fatal(err)
	}
	sector := make([]byte, sectorSize)
	copy(sector, plaintext)
	cipher.Encrypt(sector, sector, 0)
	return sector
}

// TestUnreadableHistoriesAreLeftAsTheyWere refuses histories whose key, logs or header the preflight cannot accept. Each
// refusal changes no file; removing the cause then reads the history in full.
func TestUnreadableHistoriesAreLeftAsTheyWere(t *testing.T) {
	const checkpointed, inLog = 100, 40
	const all = checkpointed + inLog
	foreign := logFixture(t, 10, 5)
	foreignKey := readFile(t, keyPath(foreign))
	// XTS encrypts each 16-byte block of a sector on its own, so a changed ciphertext byte garbles only its block: byte 3
	// lies in the block that holds the magic of a database or a log header.
	flipFirstSector := func(t *testing.T, path string) {
		data := readFile(t, path)
		data[3] ^= 0x5a
		writeFile(t, path, data)
	}
	cases := []struct {
		name     string
		provider KeyProvider
		reason   string
		damage   func(t *testing.T, path string)
		repair   func(t *testing.T, path string, saved map[string][]byte)
	}{
		{
			name: "another history's key", reason: ReasonKeyMismatch,
			damage: func(t *testing.T, path string) { writeFile(t, keyPath(path), foreignKey) },
		},
		{
			name: "another history's log beside it", reason: ReasonWALIntegrity,
			damage: func(t *testing.T, path string) {
				writeFile(t, path+"-wal", readFile(t, foreign+"-wal"))
				writeFile(t, path+"-shm", readFile(t, foreign+"-shm"))
			},
		},
		{
			name: "damaged log header", reason: ReasonWALIntegrity,
			damage: func(t *testing.T, path string) { flipFirstSector(t, path+"-wal") },
		},
		{
			name: "damaged database header", reason: ReasonKeyMismatch,
			damage: func(t *testing.T, path string) { flipFirstSector(t, path) },
		},
		{
			name: "damaged rollback journal", reason: ReasonCorrupt,
			damage: func(t *testing.T, path string) {
				writeFile(t, path+"-journal", encryptSector(t, path, []byte("not a journal header")))
			},
			repair: func(t *testing.T, path string, saved map[string][]byte) {
				if err := os.Remove(path + "-journal"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "key that does not unseal", provider: failingProvider{}, reason: ReasonKeyUnavailable,
			damage: func(t *testing.T, path string) {},
		},
		{
			name: "missing key file", reason: ReasonKeyVerification,
			damage: func(t *testing.T, path string) {
				if err := os.Remove(keyPath(path)); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := logFixture(t, checkpointed, inLog)
			saved := map[string][]byte{}
			for _, name := range append([]string{keyPath(path)}, path, path+"-wal", path+"-shm") {
				saved[name] = readFile(t, name)
			}
			c.damage(t, path)
			refusedKeepingFiles(t, path, c.provider, c.reason)
			// A second refusal is just as harmless.
			refusedKeepingFiles(t, path, c.provider, c.reason)
			if c.repair != nil {
				c.repair(t, path, saved)
			} else {
				for name, data := range saved {
					writeFile(t, name, data)
				}
			}
			if got := encountersIn(t, path); got != all {
				t.Fatalf("after the cause was removed the history holds %d of %d encounters", got, all)
			}
		})
	}
}

// TestAJournalThatIsNotHotIsAccepted: a rollback journal whose first byte is zero is one SQLite ignores, so it neither blocks
// the history nor changes what is read.
func TestAJournalThatIsNotHotIsAccepted(t *testing.T) {
	path := logFixture(t, 20, 10)
	writeFile(t, path+"-journal", encryptSector(t, path, nil))
	outcome, err := preflight(path, mustLoadKey(t, path), true)
	if err != nil || outcome != preflightReady {
		t.Fatalf("preflight = %s (%v)", outcome, err)
	}
	if got := encountersIn(t, path); got != 30 {
		t.Fatalf("%d encounters, want 30", got)
	}
}

// TestInaccessibleHistoryPathCreatesNothing: a history path that cannot be a file is refused as open_failed, and nothing is
// created or changed beside it.
func TestInaccessibleHistoryPathCreatesNothing(t *testing.T) {
	t.Run("parent is a file", func(t *testing.T) {
		directory := t.TempDir()
		blocker := filepath.Join(directory, "history")
		writeFile(t, blocker, []byte("not a directory"))
		store, err := Open(Options{Path: filepath.Join(blocker, "combat-history.db"), Location: time.UTC, Build: &testBuild})
		if err == nil || store.Status().State != StateUnavailable || store.Status().Reason != ReasonOpenFailed {
			t.Fatalf("status = %+v (%v)", store.Status(), err)
		}
		store.Close()
		if entries, _ := os.ReadDir(directory); len(entries) != 1 || string(readFile(t, blocker)) != "not a directory" {
			t.Fatalf("the directory now holds %v", entries)
		}
	})
	t.Run("history path is a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "history", "combat-history.db")
		if err := os.MkdirAll(filepath.Join(path, "inside"), 0o700); err != nil {
			t.Fatal(err)
		}
		store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
		if err == nil || store.Status().State != StateUnavailable || store.Status().Reason != ReasonOpenFailed {
			t.Fatalf("status = %+v (%v)", store.Status(), err)
		}
		store.Close()
		if entries, _ := os.ReadDir(path); len(entries) != 1 || entries[0].Name() != "inside" {
			t.Fatalf("the directory at the history path now holds %v", entries)
		}
		if _, err := os.Stat(keyPath(path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a key file was created for a history path that is a directory (%v)", err)
		}
	})
}

// TestPreflightChecksumIsSQLites pins the write-ahead log checksum against a log SQLite itself wrote: a header the
// preflight accepts, and the same header with one changed bit refused.
func TestPreflightChecksumIsSQLites(t *testing.T) {
	path := logFixture(t, 5, 5)
	cipher, err := xts.NewCipher(aes.NewCipher, mustLoadKey(t, path))
	if err != nil {
		t.Fatal(err)
	}
	sector, err := decryptFirstSector(path+"-wal", cipher)
	if err != nil {
		t.Fatal(err)
	}
	header, err := decryptFirstSector(path, cipher)
	if err != nil {
		t.Fatal(err)
	}
	pageSize, ok := databaseHeader(header)
	if !ok || !walHeader(sector, pageSize) {
		t.Fatalf("SQLite's own log header was refused (page size %d, database header %t)", pageSize, ok)
	}
	for _, offset := range []int{0, 4, 8, 12, 16, 20, 24, 28} {
		changed := append([]byte(nil), sector...)
		changed[offset+3] ^= 1
		if walHeader(changed, pageSize) {
			t.Fatalf("a log header changed at byte %d was accepted", offset+3)
		}
	}
	if walHeader(sector, pageSize*2) {
		t.Fatal("a log of another page size was accepted")
	}
}
