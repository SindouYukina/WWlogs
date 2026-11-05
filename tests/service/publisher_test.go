package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"wwlogs/internal/capture"
)

func testView(damage int64) *capture.ViewSnapshot {
	return &capture.ViewSnapshot{
		CaptureSessionID: 1, StatisticsIntervalID: 1,
		Statistics: capture.ViewStatistics{DamageDealt: damage},
		Roles:      []capture.ViewRole{{RoleID: 1409, DamageDealt: damage}},
	}
}

func TestPublisherSequenceAndNext(t *testing.T) {
	p := NewPublisher(Status{State: StateStopped})
	first := p.Latest()
	if first.Sequence != 1 || first.View != nil {
		t.Fatalf("initial snapshot = %+v", first)
	}
	p.publish(Status{State: StateRunning}, testView(10))
	next, err := p.Next(context.Background(), first.Sequence)
	if err != nil || next.Sequence != 2 || next.View.Statistics.DamageDealt != 10 {
		t.Fatalf("Next = %+v, %v", next, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	same, err := p.Next(ctx, next.Sequence)
	if err == nil || same.Sequence != next.Sequence {
		t.Fatalf("Next without a newer snapshot must return the latest and the context error, got %d, %v", same.Sequence, err)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		p.publish(Status{State: StateRunning}, testView(20))
	}()
	waited, err := p.Next(context.Background(), next.Sequence)
	if err != nil || waited.Sequence != 3 {
		t.Fatalf("Next did not wake on publish: %+v, %v", waited, err)
	}
}

func TestPublisherJSONIsAnIndependentCopy(t *testing.T) {
	p := NewPublisher(Status{State: StateStopped})
	snapshot := p.publish(Status{State: StateRunning}, testView(42))
	first, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		first[i] = 'x'
	}
	second, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Sequence uint64                `json:"sequence"`
		View     *capture.ViewSnapshot `json:"view"`
	}
	if err := json.Unmarshal(second, &decoded); err != nil {
		t.Fatalf("a reader changing its bytes changed the published JSON: %v", err)
	}
	if decoded.View.Statistics.DamageDealt != 42 || decoded.Sequence != snapshot.Sequence {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestPublisherNeverWaitsForReaders(t *testing.T) {
	p := NewPublisher(Status{State: StateStopped})
	// A reader that took one snapshot and never comes back must not slow publishing.
	stalled := p.Latest()
	_ = stalled
	started := time.Now()
	for i := 0; i < 100000; i++ {
		p.publish(Status{State: StateRunning}, testView(int64(i)))
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("100000 publishes took %s", elapsed)
	}
	if p.Latest().View.Statistics.DamageDealt != 99999 {
		t.Fatal("latest snapshot is not the newest")
	}
}

func TestConcurrentReadersSeeMonotonicSnapshots(t *testing.T) {
	p := NewPublisher(Status{State: StateStopped})
	const publishes = 2000
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func(slow bool) {
			defer wg.Done()
			var after uint64
			for {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				snapshot, err := p.Next(ctx, after)
				cancel()
				if err != nil {
					errs <- "reader timed out"
					return
				}
				if snapshot.Sequence <= after {
					errs <- "sequence went backwards"
					return
				}
				if snapshot.View != nil && snapshot.View.Statistics.DamageDealt != snapshot.View.Roles[0].DamageDealt {
					errs <- "a snapshot mixed two publishes"
					return
				}
				after = snapshot.Sequence
				if slow {
					time.Sleep(time.Millisecond)
				}
				if snapshot.View != nil && snapshot.View.Statistics.DamageDealt == publishes-1 {
					return
				}
			}
		}(reader%2 == 0)
	}
	for i := int64(0); i < publishes; i++ {
		p.publish(Status{State: StateRunning}, testView(i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
