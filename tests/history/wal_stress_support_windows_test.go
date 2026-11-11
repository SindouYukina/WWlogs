package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Support for the encrypted write-ahead log stress tests: deterministic row content that every reader can check on its own,
// latency summaries, and process measurements. WWLOGS_HISTORY_STRESS_SECONDS sets how long a stress test runs,
// WWLOGS_HISTORY_STRESS_READERS how many readers it starts, and WWLOGS_HISTORY_STRESS_OUT names a directory for its
// measurements as JSON (used to compare driver versions under the same load).

func stressDuration(fallback time.Duration) time.Duration {
	if value, err := strconv.ParseFloat(os.Getenv("WWLOGS_HISTORY_STRESS_SECONDS"), 64); err == nil && value > 0 {
		return time.Duration(value * float64(time.Second))
	}
	return fallback
}

func stressReaders(fallback int) int {
	if value, err := strconv.Atoi(os.Getenv("WWLOGS_HISTORY_STRESS_READERS")); err == nil && value > 0 {
		return value
	}
	return fallback
}

func writeStressReport(t *testing.T, name string, report any) {
	t.Helper()
	directory := os.Getenv("WWLOGS_HISTORY_STRESS_OUT")
	if directory == "" {
		return
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// driverVersion is the version of github.com/ncruces/go-sqlite3 the test binary was built with.
func driverVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/ncruces/go-sqlite3" {
				return dep.Version
			}
		}
	}
	return "unknown"
}

// Deterministic content. The n-th transaction (from 1) commits stressTxSize(n) rows with consecutive sequences; row seq
// holds stressAmount(seq) and stressEvidence(seq), 2 to 8,700 bytes, so commits rewrite pages of very different sizes and
// some rows spill into overflow pages.

func stressTxSize(n int64) int64 {
	if n%25 == 0 {
		return 300
	}
	return 1 + n*7%48
}

func stressAmount(sequence int64) int64 { return sequence*7919%1000003 + 1 }

func stressEvidence(sequence int64) string {
	size := int(sequence * 2654435761 % 1500)
	if sequence%61 == 0 {
		size += 7000
	}
	var b strings.Builder
	b.Grow(size + 24)
	b.WriteString(strconv.FormatInt(sequence, 10))
	b.WriteByte(':')
	for i := int64(0); b.Len() < size+12; i++ {
		b.WriteByte(byte('a' + (sequence+i)%26))
	}
	return b.String()
}

const stressInsert = `INSERT INTO combat_event (capture_session_id, sequence, protocol_session, rpc_id, observed_at_ns, kind, flow, confidence,
	evidence, amount, source_entity_id, source_generation, source_relation, target_entity_id, target_generation, target_relation)
	VALUES (?, ?, 1, ?, ?, 'damage', 'local_outgoing', 'confirmed', ?, ?, 10, 1, 'local_role', 20, 1, 'monster')`

// beginner is the store's pool or one connection of it.
type beginner interface {
	BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error)
}

// querier is the store's pool or one connection of it.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// stressWrite commits transactions of sizeOf(n) rows into span's combat events until stop returns true, the way the
// recorder writes: BeginTx (BEGIN IMMEDIATE), one insert per row, Commit. After each commit it calls committed with the
// transaction number, the last sequence and the commit latency.
func stressWrite(ctx context.Context, db beginner, span int64, sizeOf func(int64) int64, stop func() bool, committed func(tx, last int64, latency time.Duration)) error {
	var last int64
	for n := int64(1); !stop(); n++ {
		size := sizeOf(n)
		started := time.Now()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction %d: %w", n, err)
		}
		for sequence := last + 1; sequence <= last+size; sequence++ {
			if _, err := tx.ExecContext(ctx, stressInsert, span, sequence, sequence, sequence*1000, stressEvidence(sequence), stressAmount(sequence)); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("insert %d: %w", sequence, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit transaction %d: %w", n, err)
		}
		last += size
		committed(n, last, time.Since(started))
	}
	return nil
}

// stressLedger knows, for any last sequence, whether it ends a transaction and the sum of every amount up to it. size is
// the writer's transaction size (stressTxSize when nil).
type stressLedger struct {
	size func(int64) int64
	mu   sync.Mutex
	ends []int64
	sums []int64
}

func (l *stressLedger) at(last int64) (sum int64, boundary bool) {
	if last == 0 {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	sizeOf := l.size
	if sizeOf == nil {
		sizeOf = stressTxSize
	}
	for len(l.ends) == 0 || l.ends[len(l.ends)-1] < last {
		var end, total int64
		if count := len(l.ends); count > 0 {
			end, total = l.ends[count-1], l.sums[count-1]
		}
		for sequence, stop := end+1, end+sizeOf(int64(len(l.ends)+1)); sequence <= stop; sequence++ {
			total += stressAmount(sequence)
			end = sequence
		}
		l.ends, l.sums = append(l.ends, end), append(l.sums, total)
	}
	i := sort.Search(len(l.ends), func(i int) bool { return l.ends[i] >= last })
	if l.ends[i] != last {
		return 0, false
	}
	return l.sums[i], true
}

// errStressMismatch marks a read that returned a value other than the one written: a torn or corrupt page.
var errStressMismatch = errors.New("read a value that was never written")

// stressCheckRange reads at most limit rows after sequence after and checks each against its content and that sequences
// follow one another. It returns the last sequence it read (after when it read none).
func stressCheckRange(ctx context.Context, db querier, span, after, limit int64) (int64, error) {
	rows, err := db.QueryContext(ctx, "SELECT sequence, rpc_id, amount, evidence FROM combat_event WHERE capture_session_id = ? AND sequence > ? ORDER BY sequence LIMIT ?", span, after, limit)
	if err != nil {
		return after, err
	}
	defer rows.Close()
	expect := after + 1
	for rows.Next() {
		var sequence, rpc, amount int64
		var evidence string
		if err := rows.Scan(&sequence, &rpc, &amount, &evidence); err != nil {
			return after, err
		}
		if sequence != expect {
			return after, fmt.Errorf("%w: sequence %d where %d follows", errStressMismatch, sequence, expect)
		}
		if rpc != sequence || amount != stressAmount(sequence) || evidence != stressEvidence(sequence) {
			return after, fmt.Errorf("%w: row %d (rpc %d, amount %d, evidence %d bytes)", errStressMismatch, sequence, rpc, amount, len(evidence))
		}
		expect++
	}
	return expect - 1, rows.Err()
}

// stressCheckTotals checks that the rows a reader sees are whole transactions and sum to what was written.
func stressCheckTotals(ctx context.Context, db querier, span int64, ledger *stressLedger) (int64, error) {
	var count, total, last int64
	if err := db.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(amount), 0), coalesce(max(sequence), 0) FROM combat_event WHERE capture_session_id = ?", span).Scan(&count, &total, &last); err != nil {
		return 0, err
	}
	sum, boundary := ledger.at(last)
	if count != last || !boundary || total != sum {
		return last, fmt.Errorf("%w: %d rows up to %d summing %d (transaction boundary %v, expected sum %d)", errStressMismatch, count, last, total, boundary, sum)
	}
	return last, nil
}

// stressVerifyAll checks every stored row of span and the totals; it is the final, single-reader check.
func stressVerifyAll(t *testing.T, db *sql.DB, span, want int64, ledger *stressLedger) {
	t.Helper()
	ctx := context.Background()
	var last int64
	for {
		next, err := stressCheckRange(ctx, db, span, last, 2000)
		if err != nil {
			t.Fatalf("rows after %d: %v", last, err)
		}
		if next == last {
			break
		}
		last = next
	}
	if last != want {
		t.Fatalf("stored rows end at %d, %d were committed", last, want)
	}
	if got, err := stressCheckTotals(ctx, db, span, ledger); err != nil || got != want {
		t.Fatalf("totals end at %d (%v)", got, err)
	}
}

// latencies collects durations from many goroutines.
type latencies struct {
	mu     sync.Mutex
	values []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.values = append(l.values, d)
	l.mu.Unlock()
}

type latencySummary struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50Ms"`
	P95   float64 `json:"p95Ms"`
	P99   float64 `json:"p99Ms"`
	Max   float64 `json:"maxMs"`
	Mean  float64 `json:"meanMs"`
}

func (l *latencies) summary() latencySummary {
	l.mu.Lock()
	values := slices.Clone(l.values)
	l.mu.Unlock()
	if len(values) == 0 {
		return latencySummary{}
	}
	slices.Sort(values)
	at := func(q float64) float64 {
		return float64(values[min(len(values)-1, int(q*float64(len(values))))].Microseconds()) / 1000
	}
	var total time.Duration
	for _, v := range values {
		total += v
	}
	return latencySummary{Count: len(values), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: float64(values[len(values)-1].Microseconds()) / 1000,
		Mean: float64(total.Microseconds()) / 1000 / float64(len(values))}
}

// failureLog counts failed reads by kind and keeps the first few messages.
type failureLog struct {
	mu       sync.Mutex
	counts   map[string]int
	messages []string
}

func (f *failureLog) add(kind string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.counts == nil {
		f.counts = map[string]int{}
	}
	class := "error"
	switch {
	case errors.Is(err, errStressMismatch):
		class = "mismatch"
	case strings.Contains(err.Error(), "malformed"):
		class = "malformed"
	case strings.Contains(err.Error(), "busy") || strings.Contains(err.Error(), "locked"):
		class = "busy"
	}
	f.counts[kind+"/"+class]++
	if len(f.messages) < 12 {
		f.messages = append(f.messages, kind+": "+err.Error())
	}
}

func (f *failureLog) snapshot() (map[string]int, []string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.counts {
		total += n
	}
	counts := make(map[string]int, len(f.counts))
	for kind, n := range f.counts {
		counts[kind] = n
	}
	return counts, slices.Clone(f.messages), total
}

// Process measurements.

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

func processCPU() time.Duration {
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user) != nil {
		return 0
	}
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(kernel) + ticks(user)) * 100)
}

func processMemory() (private, workingSet uint64) {
	var counters processMemoryCounters
	counters.cb = uint32(unsafe.Sizeof(counters))
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb)); r == 0 {
		return 0, 0
	}
	return uint64(counters.PrivateUsage), uint64(counters.WorkingSetSize)
}

// sampler records the largest write-ahead log, private bytes, working set and Go heap while a stress test runs, and the
// process CPU time it used.
type sampler struct {
	stop         chan struct{}
	done         chan struct{}
	startedCPU   time.Duration
	started      time.Time
	walMax       int64
	privateMax   uint64
	privateStart uint64
	workingMax   uint64
	heapMax      uint64
}

type samplerReport struct {
	Seconds          float64 `json:"seconds"`
	CPUSeconds       float64 `json:"cpuSeconds"`
	CPUPercentOfCore float64 `json:"cpuPercentOfOneCore"`
	WALMaxBytes      int64   `json:"walMaxBytes"`
	PrivateStartMB   float64 `json:"privateStartMB"`
	PrivateMaxMB     float64 `json:"privateMaxMB"`
	WorkingSetMaxMB  float64 `json:"workingSetMaxMB"`
	GoHeapMaxMB      float64 `json:"goHeapInuseMaxMB"`
}

func startSampler(historyPath string) *sampler {
	s := &sampler{stop: make(chan struct{}), done: make(chan struct{}), startedCPU: processCPU(), started: time.Now()}
	s.privateStart, _ = processMemory()
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var stats runtime.MemStats
		for {
			if info, err := os.Stat(historyPath + "-wal"); err == nil {
				s.walMax = max(s.walMax, info.Size())
			}
			private, working := processMemory()
			s.privateMax, s.workingMax = max(s.privateMax, private), max(s.workingMax, working)
			runtime.ReadMemStats(&stats)
			s.heapMax = max(s.heapMax, stats.HeapInuse)
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

func (s *sampler) finish() samplerReport {
	close(s.stop)
	<-s.done
	elapsed := time.Since(s.started)
	cpu := processCPU() - s.startedCPU
	const mb = 1 << 20
	return samplerReport{Seconds: elapsed.Seconds(), CPUSeconds: cpu.Seconds(), CPUPercentOfCore: 100 * cpu.Seconds() / elapsed.Seconds(), WALMaxBytes: s.walMax,
		PrivateStartMB: float64(s.privateStart) / mb, PrivateMaxMB: float64(s.privateMax) / mb, WorkingSetMaxMB: float64(s.workingMax) / mb, GoHeapMaxMB: float64(s.heapMax) / mb}
}
