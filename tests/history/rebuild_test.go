package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

// Retrying and rebuilding a history that cannot be opened (rebuild.go). The history the tests give up on was made on the
// synthetic default machine; another motherboard (otherMachine) cannot open it, which is what makes it rebuildable there.

// neighbours places files a rebuild must never touch beside the history at path: the settings, another history and a
// capture spool file, all in the history's parent directories. It returns their digests.
func neighbours(t *testing.T, path string) map[string][32]byte {
	t.Helper()
	dir := filepath.Dir(path)
	files := map[string][]byte{
		filepath.Join(filepath.Dir(dir), "settings.json"):                      []byte(`{"version":1}`),
		filepath.Join(dir, "other-history.db"):                                 []byte("another history"),
		filepath.Join(filepath.Dir(dir), "capture-spool", "session", "a.pcap"): []byte("capture"),
	}
	digests := map[string][32]byte{}
	for file, data := range files {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, file, data)
		digests[file] = fileDigest(t, file)
	}
	return digests
}

func requireNeighbours(t *testing.T, digests map[string][32]byte) {
	t.Helper()
	for file, digest := range digests {
		if fileDigest(t, file) != digest {
			t.Fatalf("%s was changed by the rebuild", file)
		}
	}
}

// requireRebuilt checks that store is open on a new, empty history of machine and that nothing of the old one is left.
func requireRebuilt(t *testing.T, store *Store, path string) {
	t.Helper()
	if status := store.Status(); status.State != StateReady || status.KeyProtection != keyProtectionDerived || status.Rebuildable {
		t.Fatalf("status after the rebuild = %+v", status)
	}
	if count := countRows(t, store, "encounter"); count != 0 {
		t.Fatalf("the new history holds %d encounters", count)
	}
	assertIntegrity(t, store)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, discardedSuffix), strings.HasSuffix(name, rebuildSuffix), name == filepath.Base(keyPath(path)):
			t.Fatalf("the rebuild left %s", name)
		}
	}
	if _, err := os.Stat(keyRecordPath(path)); err != nil {
		t.Fatalf("the new history has no record (%v)", err)
	}
}

// unreadableHistory is a history with 40 encounters only in its write-ahead log, opened on another motherboard.
func unreadableHistory(t *testing.T) (*Store, string, KeyInputs) {
	t.Helper()
	path := logFixtureOf(t, openTestStore(t, testPath(t), nil), 100, 40)
	other := testKeyInputs(otherMachine(), syntheticProtocolKeyA)
	store, err := openWith(path, other)
	if err == nil || store.Status().Reason != ReasonKeyVerification || !store.Status().Rebuildable {
		t.Fatalf("the history opened on another motherboard: %+v (%v)", store.Status(), err)
	}
	t.Cleanup(func() { store.Close() })
	return store, path, other
}

// TestUnreadableHistoryIsRebuiltOnRequest deletes a history no key of this computer opens, with its logs and record, and
// creates a new, empty one in its place; the files beside it stay as they were.
func TestUnreadableHistoryIsRebuiltOnRequest(t *testing.T) {
	store, path, _ := unreadableHistory(t)
	kept := neighbours(t, path)
	status, err := store.Rebuild()
	if err != nil {
		t.Fatalf("rebuild: %v (%+v)", err, status)
	}
	requireRebuilt(t, store, path)
	requireNeighbours(t, kept)
	// The new history records like any other, and the store stays open on it.
	recordOneEncounter(t, store)
	if year, err := store.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("year after recording = %+v (%v)", year, err)
	}
	if _, err := store.Rebuild(); !errors.Is(err, ErrNotRebuildable) {
		t.Fatalf("an open history was rebuilt: %v", err)
	}
}

// TestRebuildClearsAV070History: a v0.7.0 history whose DPAPI key no longer unseals is rebuilt as a derived history; its key
// file goes with it, and no DPAPI key is made.
func TestRebuildClearsAV070History(t *testing.T) {
	legacy, path := recordedLegacyStore(t)
	legacy.Close()
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, KeyProvider: failingProvider{}})
	if err == nil || store.Status().Reason != ReasonKeyUnavailable || !store.Status().Rebuildable {
		t.Fatalf("status = %+v (%v)", store.Status(), err)
	}
	defer store.Close()
	if _, err := store.Rebuild(); err != nil {
		t.Fatal(err)
	}
	requireRebuilt(t, store, path)
}

// TestRebuildIsOfferedOnlyWhereANewHistorySolvesIt: a history that is open, used by another WWlogs, or unreadable only for a
// while (no motherboard identity, no protocol key) is never offered for deletion, and a rebuild of it is refused without
// touching a file.
func TestRebuildIsOfferedOnlyWhereANewHistorySolvesIt(t *testing.T) {
	failing := errors.New("synthetic failure")
	recorded, path := recordedStore(t)
	if _, err := recorded.Rebuild(); !errors.Is(err, ErrNotRebuildable) {
		t.Fatalf("an open history: %v", err)
	}
	second, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if !errors.Is(err, errLocked) || second.Status().Rebuildable {
		t.Fatalf("a history another WWlogs records into: %+v (%v)", second.Status(), err)
	}
	if _, err := second.Rebuild(); !errors.Is(err, ErrNotRebuildable) {
		t.Fatalf("a read-only history was rebuilt: %v", err)
	}
	second.Close()
	recorded.Close()
	before := historyDigests(t, filepath.Dir(path))
	for name, inputs := range map[string]KeyInputs{
		"no motherboard identity": {Machine: func() (Machine, error) { return Machine{}, failing }, ProtocolKey: platformKeyInputs.ProtocolKey},
		"no protocol key":         {Machine: platformKeyInputs.Machine, ProtocolKey: func() ([]byte, error) { return nil, failing }},
	} {
		store, err := openWith(path, inputs)
		if err == nil || store.Status().Rebuildable {
			t.Fatalf("%s: %+v (%v)", name, store.Status(), err)
		}
		if _, err := store.Rebuild(); !errors.Is(err, ErrNotRebuildable) {
			t.Fatalf("%s: rebuilt: %v", name, err)
		}
		store.Close()
	}
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
	// A damaged history on a computer that can no longer create one (its UUID has since been published as a placeholder and
	// it reports no serial) is not offered for deletion either: deleting it would leave no history at all.
	uuidOnly := testKeyInputs(Machine{SystemUUID: testUUID}, syntheticProtocolKeyA)
	other, err := openWith(testPath(t), uuidOnly)
	if err != nil {
		t.Fatal(err)
	}
	otherPath := other.Path()
	other.Close()
	flipped := readFile(t, otherPath)
	flipped[3] ^= 0x5a
	writeFile(t, otherPath, flipped)
	savedDefaults := knownDefaultUUIDs
	t.Cleanup(func() { knownDefaultUUIDs = savedDefaults })
	knownDefaultUUIDs = append(append([]string(nil), savedDefaults...), uuidDisplay(uuidByteOrderSwapped(testUUID)))
	store, err := openWith(otherPath, uuidOnly)
	if err == nil || store.Status().Reason != ReasonCorrupt || store.Status().Rebuildable {
		t.Fatalf("no new history possible: %+v (%v)", store.Status(), err)
	}
	store.Close()
}

// TestRetryOpensTheHistoryOnceTheCauseIsGone: a history that could not be opened for a while opens on a retry, with every
// encounter, once the cause has passed; a retry of an open history changes nothing.
func TestRetryOpensTheHistoryOnceTheCauseIsGone(t *testing.T) {
	recorded, path := recordedStore(t)
	recorded.Close()
	var available atomic.Bool
	inputs := KeyInputs{
		Machine: func() (Machine, error) {
			if !available.Load() {
				return Machine{}, errors.New("the firmware table cannot be read now")
			}
			return testMachine(), nil
		},
		ProtocolKey: platformKeyInputs.ProtocolKey,
	}
	store, err := openWith(path, inputs)
	if err == nil || store.Status().Reason != ReasonHardwareIdentity || store.Status().Rebuildable {
		t.Fatalf("status = %+v (%v)", store.Status(), err)
	}
	defer store.Close()
	if status, _ := store.Retry(); status.State != StateUnavailable {
		t.Fatalf("a retry before the cause passed: %+v", status)
	}
	available.Store(true)
	status, err := store.Retry()
	if err != nil || status.State != StateReady {
		t.Fatalf("retry: %+v (%v)", status, err)
	}
	if year, err := store.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("year after the retry = %+v (%v)", year, err)
	}
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatalf("the reopened history does not record: %v", err)
	}
	if err := recorder.Finish(context.Background(), capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	if again, err := store.Retry(); err != nil || again.State != StateReady {
		t.Fatalf("a retry of an open history: %+v (%v)", again, err)
	}
}

// rebuildSteps are the steps a rebuild names, in order.
var rebuildSteps = []string{"discarded-file", "discarded", "created-file", "created"}

// TestInterruptedRebuildIsCarriedOut ends a rebuilding process at every step, as a crash would leave the files: the next open
// carries the rebuild out, since the user decided it, and deletes the old files only once the new history opened.
func TestInterruptedRebuildIsCarriedOut(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range rebuildSteps {
		t.Run(step, func(t *testing.T) {
			store, path, other := unreadableHistory(t)
			store.Close()
			kept := neighbours(t, path)
			helper := exec.Command(executable, "-test.run=^TestRebuildHelperProcess$", "-test.count=1")
			helper.Env = append(os.Environ(), "WWLOGS_REBUILD_HELPER=1", "WWLOGS_REBUILD_DB="+path, "WWLOGS_REBUILD_STOP="+step)
			output, err := helper.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("the rebuilding process did not stop at %s: %v\n%s", step, err, output)
			}
			reopened, err := openWith(path, other)
			if err != nil {
				t.Fatalf("open after the interrupted rebuild: %v", err)
			}
			defer reopened.Close()
			requireRebuilt(t, reopened, path)
			requireNeighbours(t, kept)
		})
	}
}

// TestRebuildHelperProcess is not a test of its own: TestInterruptedRebuildIsCarriedOut starts the test binary again with
// WWLOGS_REBUILD_HELPER set; it then opens the history WWLOGS_REBUILD_DB names on the other synthetic motherboard, rebuilds
// it, and ends the process at once when the rebuild reaches the step WWLOGS_REBUILD_STOP names.
func TestRebuildHelperProcess(t *testing.T) {
	if os.Getenv("WWLOGS_REBUILD_HELPER") != "1" {
		t.Skip("runs only as the helper process of another test")
	}
	stop := os.Getenv("WWLOGS_REBUILD_STOP")
	rebuildStep = func(step string) error {
		if step == stop {
			os.Exit(7)
		}
		return nil
	}
	store, _ := openWith(os.Getenv("WWLOGS_REBUILD_DB"), testKeyInputs(otherMachine(), syntheticProtocolKeyA))
	status, err := store.Rebuild()
	fmt.Println("helper: the rebuild never reached", stop, status, err)
	os.Exit(5)
}

// TestFailedRebuildPutsTheOldHistoryBack: when the new history cannot be created, every old file is back, byte for byte,
// and the history is unavailable as before.
func TestFailedRebuildPutsTheOldHistoryBack(t *testing.T) {
	store, path, _ := unreadableHistory(t)
	before := historyDigests(t, filepath.Dir(path))
	saved := rebuildStep
	t.Cleanup(func() { rebuildStep = saved })
	rebuildStep = func(step string) error {
		if step == "created-file" {
			return errors.New("synthetic failure")
		}
		return nil
	}
	status, err := store.Rebuild()
	if err == nil || status.State != StateUnavailable || status.Reason != ReasonKeyVerification || !status.Rebuildable {
		t.Fatalf("a failed rebuild: %+v (%v)", status, err)
	}
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
	rebuildStep = saved
	if _, err := store.Rebuild(); err != nil {
		t.Fatalf("the rebuild after the failure: %v", err)
	}
	requireRebuilt(t, store, path)
}

// TestDamagedRebuildRecordPutsTheOldHistoryBack: a rebuild record that cannot be read records no decision for sure, so the
// discarded files are put back instead of deleted.
func TestDamagedRebuildRecordPutsTheOldHistoryBack(t *testing.T) {
	store, path, other := unreadableHistory(t)
	store.Close()
	before := historyDigests(t, filepath.Dir(path))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(executable, "-test.run=^TestRebuildHelperProcess$", "-test.count=1")
	helper.Env = append(os.Environ(), "WWLOGS_REBUILD_HELPER=1", "WWLOGS_REBUILD_DB="+path, "WWLOGS_REBUILD_STOP=discarded")
	if output, err := helper.CombinedOutput(); err == nil {
		t.Fatalf("the helper did not stop: %s", output)
	}
	writeFile(t, historyFile(path, rebuildSuffix), []byte("{\"stage\": \"dis"))
	reopened, err := openWith(path, other)
	if err == nil || reopened.Status().Reason != ReasonKeyVerification {
		t.Fatalf("open after a damaged rebuild record: %+v (%v)", reopened.Status(), err)
	}
	reopened.Close()
	after := historyDigests(t, filepath.Dir(path))
	requireSameFiles(t, before, after)
}
