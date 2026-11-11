package history

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/util/vfsutil"
	"github.com/ncruces/go-sqlite3/vfs"
	"github.com/ncruces/go-sqlite3/vfs/xts"

	"wwlogs/internal/capture"
)

// Torn writes, made deterministic.
//
// A read that overlaps a write of the same bytes is not atomic on every operating system and file system: it can return a
// mix of old and new bytes. SQLite never reads what another connection is writing, but the xts VFS rewrites whole 512-byte
// sectors, so a reader of the unchanged part of a sector can get a torn ciphertext that decrypts to noise. Racing the real
// file system hits that window rarely; tornVFS (the technique of upstream's own regression test, vfs/tests/torn_test.go of
// github.com/ncruces/go-sqlite3 v0.37.0) makes every write of a database or write-ahead log file first write a mix of the
// old and new bytes (alternating every 8 bytes), wait, and only then write the new bytes, so any overlapping read sees the
// mix. The history then runs over "xts on tornVFS" with its own pool, pragmas and key handling.

type tornVFS struct{ vfs.VFS }

func (v tornVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	return tornWrap(vfsutil.WrapOpen(v.VFS, name, flags))
}

func (v tornVFS) OpenFilename(name *vfs.Filename, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	return tornWrap(vfsutil.WrapOpenFilename(v.VFS, name, flags))
}

func tornWrap(file vfs.File, flags vfs.OpenFlag, err error) (vfs.File, vfs.OpenFlag, error) {
	if err != nil || flags&(vfs.OPEN_MAIN_DB|vfs.OPEN_WAL) == 0 {
		return file, flags, err
	}
	return &tornFile{File: file}, flags, nil
}

type tornFile struct{ vfs.File }

func (f *tornFile) WriteAt(p []byte, off int64) (int, error) {
	mixed := make([]byte, len(p))
	_, _ = f.File.ReadAt(mixed, off) // bytes past the end stay zero
	for i := range mixed {
		if i/8%2 != 0 {
			mixed[i] = p[i]
		}
	}
	if _, err := f.File.WriteAt(mixed, off); err != nil {
		return 0, err
	}
	time.Sleep(100 * time.Microsecond)
	return f.File.WriteAt(p, off)
}

func (f *tornFile) Unwrap() vfs.File { return f.File }

func (f *tornFile) SharedMemory() vfs.SharedMemory { return vfsutil.WrapSharedMemory(f.File) }

var registerTornXTS = sync.OnceFunc(func() {
	vfs.Register("wwlogs-torn", tornVFS{vfs.Find("")})
	vfs.Register("wwlogs-torn-xts", xts.Wrap(vfs.Find("wwlogs-torn"), nil))
})

// TestTornWritesNeverReachHistoryReaders commits one row per transaction while readers on their own connections read the
// newest rows, with and without a connection that keeps checkpointing (a torn read by the checkpointer would copy noise into
// the file). No read may return a value that was never written, and the file must pass its integrity check afterwards.
func TestTornWritesNeverReachHistoryReaders(t *testing.T) {
	registerTornXTS()
	saved := encryptionVFS
	encryptionVFS = "wwlogs-torn-xts"
	t.Cleanup(func() { encryptionVFS = saved })
	for _, checkpoint := range []bool{false, true} {
		name := "readers"
		if checkpoint {
			name = "readers and checkpoints"
		}
		t.Run(name, func(t *testing.T) { tornHistoryRun(t, checkpoint) })
	}
}

func tornHistoryRun(t *testing.T, checkpoint bool) {
	const commits, readers = 200, 4
	path := testPath(t)
	store := openTestStore(t, path, nil)
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		t.Fatal(err)
	}
	span := recorder.SessionID()
	ctx := context.Background()
	var last, reads atomic.Int64
	failures := &failureLog{}
	var done atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(conn *sql.Conn) {
			defer wg.Done()
			defer conn.Close()
			for !done.Load() {
				if _, err := stressCheckRange(ctx, conn, span, max(0, last.Load()-64), 64); err != nil {
					failures.add("reader", err)
				}
				reads.Add(1)
			}
		}(conn)
	}
	if checkpoint {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			for !done.Load() {
				var busy, frames, copied int
				if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
					failures.add("checkpoint", err)
				}
			}
		}()
	}
	writer, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := stressWrite(ctx, writer, span, func(int64) int64 { return 1 }, func() bool { return last.Load() >= commits }, func(_, n int64, _ time.Duration) { last.Store(n) })
	_ = writer.Close()
	done.Store(true)
	wg.Wait()
	if writeErr != nil {
		t.Fatalf("writer: %v", writeErr)
	}
	if err := recorder.Finish(ctx, capture.StopReasonRequested); err != nil {
		t.Fatal(err)
	}
	counts, messages, failed := failures.snapshot()
	t.Logf("%s: %d commits, %d reads, %d failed %v", driverVersion(), commits, reads.Load(), failed, counts)
	if failed > 0 {
		t.Errorf("%d reads returned what was never written: %v", failed, messages)
	}
	// Persistent damage shows on a fresh connection to the closed and reopened file.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path, nil)
	if got, err := stressCheckRange(ctx, reopened.db, span, 0, commits); err != nil || got != commits {
		t.Fatalf("stored rows end at %d (%v)", got, err)
	}
	assertIntegrity(t, reopened)
}

// TestTornWritesNeverReachAnotherProcess records through tornVFS in another process while this one, read-only beside it
// (the second WWlogs the writer lock allows), reads the newest rows on its own connections. The locks that keep a torn
// sector from readers must hold across processes too.
func TestTornWritesNeverReachAnotherProcess(t *testing.T) {
	registerTornXTS()
	saved := encryptionVFS
	encryptionVFS = "wwlogs-torn-xts"
	t.Cleanup(func() { encryptionVFS = saved })
	const readers = 4
	duration := stressDuration(3 * time.Second)
	path := testPath(t)
	writer := startHistoryWriterProcess(t, path, "WWLOGS_HISTORY_HELPER_TORN=1")
	span := writer.span.Load()
	reader, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if !errors.Is(err, errLocked) || reader.Status().State != StateReadOnly {
		t.Fatalf("open beside the recording process: %v, %+v", err, reader.Status())
	}
	ctx := context.Background()
	failures := &failureLog{}
	var reads atomic.Int64
	var done atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		conn, err := reader.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(conn *sql.Conn) {
			defer wg.Done()
			defer conn.Close()
			for !done.Load() {
				if _, err := stressCheckRange(ctx, conn, span, max(0, writer.committed.Load()-64), 64); err != nil {
					failures.add("reader", err)
				}
				reads.Add(1)
			}
		}(conn)
	}
	time.Sleep(duration)
	reported := writer.kill(t)
	done.Store(true)
	wg.Wait()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	counts, messages, failed := failures.snapshot()
	t.Logf("%s: writer process reported %d rows, %d reads in this process, %d failed %v", driverVersion(), reported, reads.Load(), failed, counts)
	if failed > 0 {
		t.Errorf("%d reads returned what was never written: %v", failed, messages)
	}
	store := openTestStore(t, path, nil)
	ledger := &stressLedger{size: func(int64) int64 { return 1 }}
	stored, err := stressCheckTotals(ctx, store.db, span, ledger)
	if err != nil || stored < reported {
		t.Fatalf("%d rows stored, %d reported committed (%v)", stored, reported, err)
	}
	stressVerifyAll(t, store.db, span, stored, ledger)
	assertIntegrity(t, store)
}
