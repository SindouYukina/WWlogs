package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestKilledWritersLogSurvivesAWrongKey kills a writer process while it records (TerminateProcess, as a crash ends it), so
// the transactions it committed since the last checkpoint are only in the write-ahead log. Opening that history on another
// motherboard, whose derived key is not the history's, is refused and changes no file; on its own machine the history then
// holds every transaction the writer reported committed, whole.
func TestKilledWritersLogSurvivesAWrongKey(t *testing.T) {
	path := testPath(t)
	writer := startHistoryWriterProcess(t, path)
	span := writer.span.Load()
	time.Sleep(stressDuration(time.Second))
	reported := writer.kill(t)
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the killed writer left no write-ahead log (%v)", err)
	}
	before := historyDigests(t, filepath.Dir(path))
	refused, err := openWith(path, testKeyInputs(otherMachine(), syntheticProtocolKeyA))
	if err == nil || refused.Status().Reason != ReasonKeyVerification {
		t.Fatalf("another motherboard: %+v (%v)", refused.Status(), err)
	}
	refused.Close()
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
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
	t.Logf("%s: writer killed after %d committed rows; another machine's key changed no file; %d rows read on the history's own machine", driverVersion(), reported, stored)
}
