package history

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestKilledWritersLogSurvivesAWrongKey kills a writer process while it records (TerminateProcess, as a crash ends it), so
// the transactions it committed since the last checkpoint are only in the write-ahead log. Opening that history with the key
// file of another history is refused and changes no file; with its own key the history then holds every transaction the
// writer reported committed, whole.
func TestKilledWritersLogSurvivesAWrongKey(t *testing.T) {
	path := testPath(t)
	writer := startHistoryWriterProcess(t, path)
	span := writer.span.Load()
	time.Sleep(stressDuration(time.Second))
	reported := writer.kill(t)
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the killed writer left no write-ahead log (%v)", err)
	}
	other, otherPath := recordedStore(t)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	ownKey := readFile(t, keyPath(path))
	writeFile(t, keyPath(path), readFile(t, keyPath(otherPath)))
	refusedKeepingFiles(t, path, nil, ReasonKeyMismatch)
	writeFile(t, keyPath(path), ownKey)
	store := openTestStore(t, path, nil)
	if status := store.Status(); status.State != StateReady || status.RecoveredSessions != 1 {
		t.Fatalf("status after the writer died = %+v", status)
	}
	ledger := &stressLedger{}
	stored, err := stressCheckTotals(context.Background(), store.db, span, ledger)
	if err != nil || stored < reported {
		t.Fatalf("%d rows stored, the writer reported %d committed (%v)", stored, reported, err)
	}
	stressVerifyAll(t, store.db, span, stored, ledger)
	assertIntegrity(t, store)
	t.Logf("%s: writer killed after %d committed rows; a wrong key changed no file; %d rows read with the history's own key", driverVersion(), reported, stored)
}
