package history

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A second WWlogs process and a writer that dies.
//
// The writer lock lets one process record; another one opens the same history read-only (ReasonLocked) and reads while the
// first records, so both processes touch the same encrypted write-ahead log. A process that is killed leaves its committed
// transactions in the write-ahead log, not yet in the file.

// TestHistoryHelperProcess is not a test of its own: the tests below start the test binary again with WWLOGS_HISTORY_HELPER
// set, and it then records into the history named by WWLOGS_HISTORY_HELPER_DB as another process until it is killed. It
// prints "span <id>" once and "committed <transaction> <last sequence>" after every commit. With WWLOGS_HISTORY_HELPER_TORN
// set it writes through tornVFS, one row per transaction.
func TestHistoryHelperProcess(t *testing.T) {
	path := os.Getenv("WWLOGS_HISTORY_HELPER_DB")
	if os.Getenv("WWLOGS_HISTORY_HELPER") != "writer" || path == "" {
		t.Skip("runs only as the helper process of another test")
	}
	sizeOf := stressTxSize
	if os.Getenv("WWLOGS_HISTORY_HELPER_TORN") != "" {
		// One row per transaction: every commit opens a window in which the previous frame's sector is torn.
		registerTornXTS()
		encryptionVFS = "wwlogs-torn-xts"
		sizeOf = func(int64) int64 { return 1 }
	}
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
	if err != nil {
		fmt.Println("helper: open:", err)
		os.Exit(3)
	}
	recorder, err := store.BeginSession(liveStart)
	if err != nil {
		fmt.Println("helper: begin session:", err)
		os.Exit(3)
	}
	fmt.Printf("span %d\n", recorder.SessionID())
	err = stressWrite(context.Background(), store.db, recorder.SessionID(), sizeOf, func() bool { return false }, func(n, last int64, _ time.Duration) {
		fmt.Printf("committed %d %d\n", n, last)
	})
	fmt.Println("helper: writer stopped:", err)
	os.Exit(4)
}

// historyWriterProcess is a running helper process and what it last reported.
type historyWriterProcess struct {
	cmd       *exec.Cmd
	span      atomic.Int64
	committed atomic.Int64
	ended     chan struct{}
}

func startHistoryWriterProcess(t *testing.T, path string, environment ...string) *historyWriterProcess {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &historyWriterProcess{ended: make(chan struct{})}
	p.cmd = exec.Command(executable, "-test.run=^TestHistoryHelperProcess$", "-test.count=1")
	p.cmd.Env = append(append(os.Environ(), "WWLOGS_HISTORY_HELPER=writer", "WWLOGS_HISTORY_HELPER_DB="+path), environment...)
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(p.ended)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			switch {
			case len(fields) == 2 && fields[0] == "span":
				id, _ := strconv.ParseInt(fields[1], 10, 64)
				p.span.Store(id)
			case len(fields) == 3 && fields[0] == "committed":
				last, _ := strconv.ParseInt(fields[2], 10, 64)
				p.committed.Store(last)
			}
		}
	}()
	t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
	deadline := time.Now().Add(60 * time.Second)
	for p.committed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the writer process never committed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return p
}

// kill ends the process as a crash would (TerminateProcess): no shutdown, the history is never closed.
func (p *historyWriterProcess) kill(t *testing.T) int64 {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.cmd.Wait()
	<-p.ended
	return p.committed.Load()
}

// TestWriterKilledWhileAnotherProcessReads runs a writer in another process, reads the same history read-only from this one
// while it commits, kills the writer while this process keeps reading, and then opens the history to record: every
// transaction the writer reported committed is there, whole, although it was only in the write-ahead log.
func TestWriterKilledWhileAnotherProcessReads(t *testing.T) {
	duration := stressDuration(3 * time.Second)
	readers := stressReaders(4)
	path := testPath(t)
	writer := startHistoryWriterProcess(t, path)
	span := writer.span.Load()
	reader, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild})
	if !errors.Is(err, errLocked) {
		t.Fatalf("open beside a recording process: %v", err)
	}
	if status := reader.Status(); status.State != StateReadOnly || status.Reason != ReasonLocked || !status.Encrypted {
		t.Fatalf("status = %+v", status)
	}
	ctx := context.Background()
	ledger := &stressLedger{}
	failures := &failureLog{}
	readLatency := &latencies{}
	var reads atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			random := rand.New(rand.NewPCG(seed, 0x57574c48))
			for {
				select {
				case <-stop:
					return
				default:
				}
				last := writer.committed.Load()
				started := time.Now()
				var err error
				switch pick := random.IntN(10); {
				case pick < 6:
					_, err = stressCheckRange(ctx, reader.db, span, max(0, last-48), 96)
				case pick < 9:
					_, err = stressCheckRange(ctx, reader.db, span, random.Int64N(last+1), 400)
				default:
					_, err = stressCheckTotals(ctx, reader.db, span, ledger)
				}
				readLatency.add(time.Since(started))
				reads.Add(1)
				if err != nil {
					failures.add("reader", err)
				}
			}
		}(uint64(i + 1))
	}
	time.Sleep(duration)
	reported := writer.kill(t)
	// The writer is gone mid-transaction; reading goes on.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	info, err := os.Stat(path + "-wal")
	if err != nil || info.Size() == 0 {
		t.Fatalf("the killed writer left no write-ahead log (%v)", err)
	}
	walBytes := info.Size()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, path, nil)
	if status := store.Status(); status.State != StateReady || status.RecoveredSessions != 1 {
		t.Fatalf("status after the writer died = %+v", status)
	}
	stored, err := stressCheckTotals(ctx, store.db, span, ledger)
	if err != nil || stored < reported {
		t.Fatalf("%d rows stored, the writer reported %d committed (%v)", stored, reported, err)
	}
	stressVerifyAll(t, store.db, span, stored, ledger)
	assertIntegrity(t, store)
	counts, messages, failed := failures.snapshot()
	writeStressReport(t, "process", map[string]any{
		"driver": driverVersion(), "readers": readers, "reads": reads.Load(), "readLatency": readLatency.summary(), "reportedCommitted": reported,
		"stored": stored, "walBytesAtKill": walBytes, "failures": counts, "failureMessages": messages,
	})
	t.Logf("%s: %d reads by %d readers in this process, writer killed at %d (stored %d, write-ahead log %d bytes), %d failed reads %v", driverVersion(), reads.Load(), readers, reported, stored, walBytes, failed, counts)
	if failed > 0 {
		t.Fatalf("%d reads failed: %v", failed, messages)
	}
}
