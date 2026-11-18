package history

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Histories that do not belong to this computer, and histories whose key record or database is missing.
//
// "This computer" is the synthetic motherboard every test of the package derives with (testMachine); "another computer" is
// a second synthetic motherboard (otherMachine) with the same synthetic protocol key, as a history copied from another PC
// would be. Every fixture keeps encounters only in its write-ahead log, so a refusal that let SQLite see a log with the wrong
// key would lose them. Every refusal is checked to leave every file byte for byte as it was (SHA-256), to offer a rebuild,
// and to be repeated by a retry, and every refused history is then shown to read in full where it belongs.

// anotherComputer is the key inputs of another PC: another motherboard, the same protocol key.
func anotherComputer() KeyInputs { return testKeyInputs(otherMachine(), syntheticProtocolKeyA) }

// derivedLogFixture returns a copy of a history made with inputs (KeyInputs{} for this computer) whose first checkpointed
// encounters are in the database and whose next inLog encounters only in its write-ahead log.
func derivedLogFixture(t *testing.T, inputs KeyInputs, checkpointed, inLog int) string {
	t.Helper()
	store, err := openWith(testPath(t), inputs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return logFixtureOf(t, store, checkpointed, inLog)
}

// historyPart is one file of the history at path: "" the database, "-wal" and "-shm" its logs, "record" its key record.
func historyPart(path, part string) string {
	if part == "record" {
		return keyRecordPath(path)
	}
	return path + part
}

// assemble builds a history directory from parts of other histories, as a user copying files between computers would.
func assemble(t *testing.T, parts map[string]string) string {
	t.Helper()
	path := testPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for part, from := range parts {
		writeFile(t, historyPart(path, part), readFile(t, historyPart(from, part)))
	}
	return path
}

// encountersWith opens the history at path with inputs and counts its encounters, checking its integrity.
func encountersWith(t *testing.T, path string, inputs KeyInputs) int {
	t.Helper()
	store, err := openWith(path, inputs)
	if err != nil {
		t.Fatalf("open: %+v (%v)", store.Status(), err)
	}
	defer store.Close()
	count := countRows(t, store, "encounter")
	assertIntegrity(t, store)
	return count
}

// refusedUntouched opens the history at path on this computer and expects it refused with reason and offered for a rebuild;
// a retry is refused the same way, and neither changes or creates a file.
func refusedUntouched(t *testing.T, path, reason string) {
	t.Helper()
	before := historyDigests(t, filepath.Dir(path))
	store, err := openWith(path, KeyInputs{})
	if err == nil {
		store.Close()
		t.Fatal("the history opened")
	}
	status := store.Status()
	if status.State != StateUnavailable || status.Reason != reason || !status.Rebuildable {
		store.Close()
		t.Fatalf("status = %+v, want %s and a rebuild offered (%v)", status, reason, err)
	}
	if _, err := store.BeginSession(liveStart); !errors.Is(err, ErrNotRecording) {
		store.Close()
		t.Fatalf("an unreadable history accepted a session: %v", err)
	}
	retried, err := store.Retry()
	if err == nil || retried.State != StateUnavailable || retried.Reason != reason {
		store.Close()
		t.Fatalf("retry = %+v (%v)", retried, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
}

// TestHistoryFromAnotherComputerIsRefusedUntouched covers every way a history of another PC reaches this one: its database
// alone, with its record, with its logs, its whole directory, and its database copied over this computer's history. Each
// is refused before SQLite sees it, as a history of another key (never as damage), and each reads in full again where it
// belongs: on the other computer, or here once this computer's database is put back.
func TestHistoryFromAnotherComputerIsRefusedUntouched(t *testing.T) {
	external := derivedLogFixture(t, anotherComputer(), 30, 12)
	local := derivedLogFixture(t, KeyInputs{}, 20, 5)
	if got := encountersWith(t, assemble(t, map[string]string{"": local, "-wal": local, "-shm": local, "record": local}), KeyInputs{}); got != 25 {
		t.Fatalf("this computer's own history holds %d encounters, want 25", got)
	}
	for _, c := range []struct {
		name  string
		parts map[string]string
		// owner reads the refused files back: the other computer, or this one after its own database is put back.
		owner KeyInputs
		local bool
		want  int
	}{
		{name: "database alone", parts: map[string]string{"": external}, owner: anotherComputer(), want: 30},
		{name: "database with its record", parts: map[string]string{"": external, "record": external}, owner: anotherComputer(), want: 30},
		{name: "database beside this computer's record", parts: map[string]string{"": external, "record": local}, owner: anotherComputer(), want: 30},
		{name: "database with its logs", parts: map[string]string{"": external, "-wal": external, "-shm": external}, owner: anotherComputer(), want: 42},
		{name: "whole history directory", parts: map[string]string{"": external, "-wal": external, "-shm": external, "record": external}, owner: anotherComputer(), want: 42},
		{name: "database copied over this computer's history", parts: map[string]string{"": external, "-wal": local, "-shm": local, "record": local}, local: true, want: 25},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := assemble(t, c.parts)
			refusedUntouched(t, path, ReasonKeyVerification)
			refusedUntouched(t, path, ReasonKeyVerification)
			owner := c.owner
			if c.local {
				writeFile(t, path, readFile(t, local))
				owner = KeyInputs{}
			}
			if got := encountersWith(t, path, owner); got != c.want {
				t.Fatalf("where it belongs the history holds %d encounters, want %d", got, c.want)
			}
		})
	}
}

// TestDamagedHistoryOfThisComputerIsNotTakenForAnotherComputers: a history of this computer whose first sector is damaged,
// or whose pages beyond it are, is refused as damaged, never as a history of another key, and left as it was.
func TestDamagedHistoryOfThisComputerIsNotTakenForAnotherComputers(t *testing.T) {
	local := derivedLogFixture(t, KeyInputs{}, 20, 5)
	t.Run("first sector", func(t *testing.T) {
		path := assemble(t, map[string]string{"": local, "-wal": local, "-shm": local, "record": local})
		damageFirstSector(t, path)
		refusedUntouched(t, path, ReasonCorrupt)
	})
	t.Run("first sector without a record", func(t *testing.T) {
		path := assemble(t, map[string]string{"": local})
		damageFirstSector(t, path)
		refusedUntouched(t, path, ReasonCorrupt)
	})
	t.Run("a page beyond the header", func(t *testing.T) {
		checkpointed := derivedLogFixture(t, KeyInputs{}, 40, 0)
		path := assemble(t, map[string]string{"": checkpointed, "record": checkpointed})
		data := readFile(t, path)
		pages := len(data) / 4096
		if pages < 8 {
			t.Fatalf("the fixture has %d pages", pages)
		}
		for page := 2; page < pages; page += 3 {
			data[page*4096+64] ^= 0xa5
		}
		writeFile(t, path, data)
		refusedUntouched(t, path, ReasonCorrupt)
	})
}

// damageFirstSector changes one byte of the block of the first sector that holds the database header's magic (XTS garbles
// only the 16-byte block of a changed ciphertext byte).
func damageFirstSector(t *testing.T, path string) {
	t.Helper()
	data := readFile(t, path)
	data[3] ^= 0x5a
	writeFile(t, path, data)
}

// TestRebuildReplacesAHistoryOfAnotherComputer: a refused history of another PC is replaced on request by a new history of
// this computer, which records, and which opens again after a restart.
func TestRebuildReplacesAHistoryOfAnotherComputer(t *testing.T) {
	external := derivedLogFixture(t, anotherComputer(), 30, 12)
	path := assemble(t, map[string]string{"": external, "-wal": external, "-shm": external, "record": external})
	foreignRecord := readFile(t, keyRecordPath(path))
	store, err := openWith(path, KeyInputs{})
	if err == nil || !store.Status().Rebuildable {
		t.Fatalf("the history of another computer: %+v (%v)", store.Status(), err)
	}
	status, err := store.Rebuild()
	if err != nil || status.State != StateReady {
		t.Fatalf("rebuild: %+v (%v)", status, err)
	}
	requireRebuilt(t, store, path)
	if bytes.Equal(readFile(t, keyRecordPath(path)), foreignRecord) {
		t.Fatal("the new history kept the other computer's record")
	}
	recordOneEncounter(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if got := encountersWith(t, path, KeyInputs{}); got != 1 {
		t.Fatalf("after a restart the new history holds %d encounters, want 1", got)
	}
}

// TestRecordProblemsNeverLoseTheHistory: a lost, damaged or foreign key record never costs a history this computer can
// open, including what is only in its write-ahead log, also when another process opens it; a record or a log without its
// database never makes a new history out of the log, and a record alone does not stop a new history.
func TestRecordProblemsNeverLoseTheHistory(t *testing.T) {
	local := derivedLogFixture(t, KeyInputs{}, 20, 5)
	external := derivedLogFixture(t, anotherComputer(), 3, 0)
	original := readFile(t, keyRecordPath(local))
	for _, c := range []struct {
		name   string
		parts  map[string]string
		damage func(t *testing.T, path string)
	}{
		{name: "record lost, log kept", parts: map[string]string{"": local, "-wal": local, "-shm": local}},
		{name: "record damaged, log kept", parts: map[string]string{"": local, "-wal": local, "-shm": local, "record": local},
			damage: func(t *testing.T, path string) { writeFile(t, keyRecordPath(path), []byte(`{"format": "wwlogs-hist`)) }},
		{name: "another computer's record", parts: map[string]string{"": local, "-wal": local, "-shm": local, "record": external}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := assemble(t, c.parts)
			if c.damage != nil {
				c.damage(t, path)
			}
			if got := encountersWith(t, path, KeyInputs{}); got != 25 {
				t.Fatalf("the history holds %d encounters, want 25", got)
			}
			if !bytes.Equal(readFile(t, keyRecordPath(path)), original) {
				t.Fatal("the record was not written again for the history's own key")
			}
		})
	}
	t.Run("record lost, opened by another process", func(t *testing.T) {
		path := assemble(t, map[string]string{"": local, "-wal": local, "-shm": local})
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		helper := exec.Command(executable, "-test.run=^TestRecordRecoveryHelperProcess$", "-test.count=1")
		helper.Env = append(os.Environ(), "WWLOGS_RECORD_HELPER_DB="+path, "TZ=America/St_Johns", "LANG=ko_KR.UTF-8")
		output, err := helper.CombinedOutput()
		if err != nil {
			t.Fatalf("helper: %v %s", err, output)
		}
		if !strings.Contains(string(output), "encounters 25\n") {
			t.Fatalf("the other process read: %s", output)
		}
		if !bytes.Equal(readFile(t, keyRecordPath(path)), original) {
			t.Fatal("the other process did not write the record again")
		}
	})
	t.Run("record without a database", func(t *testing.T) {
		path := assemble(t, map[string]string{"record": external})
		store, err := openWith(path, KeyInputs{})
		if err != nil {
			t.Fatalf("a new history beside a stale record: %+v (%v)", store.Status(), err)
		}
		if count := countRows(t, store, "encounter"); count != 0 {
			t.Fatalf("the new history holds %d encounters", count)
		}
		store.Close()
		if !bytes.Equal(readFile(t, keyRecordPath(path)), original) {
			t.Fatal("the stale record was not replaced by this computer's")
		}
	})
	for _, c := range []struct {
		name     string
		database []byte
	}{
		{name: "log without a database"},
		{name: "log beside an empty database", database: []byte{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := assemble(t, map[string]string{"-wal": local, "-shm": local, "record": local})
			if c.database != nil {
				writeFile(t, path, c.database)
			}
			refusedUntouched(t, path, ReasonWALIntegrity)
			// Put back, the database reads in full with the log that was kept for it.
			writeFile(t, path, readFile(t, local))
			if got := encountersWith(t, path, KeyInputs{}); got != 25 {
				t.Fatalf("with its database back the history holds %d encounters, want 25", got)
			}
		})
	}
}

// TestRecordRecoveryHelperProcess is not a test of its own: TestRecordProblemsNeverLoseTheHistory starts the test binary
// again with WWLOGS_RECORD_HELPER_DB set; it opens that history with the synthetic inputs of this computer and prints its
// encounter count.
func TestRecordRecoveryHelperProcess(t *testing.T) {
	path := os.Getenv("WWLOGS_RECORD_HELPER_DB")
	if path == "" {
		t.Skip("runs only as the helper process of another test")
	}
	store, err := openWith(path, KeyInputs{})
	if err != nil {
		t.Fatalf("open: %+v (%v)", store.Status(), err)
	}
	count := countRows(t, store, "encounter")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("encounters %s\n", strconv.Itoa(count))
}
