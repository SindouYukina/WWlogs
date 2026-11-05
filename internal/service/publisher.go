// Package service hosts the capture pipeline for an application: a CaptureService that owns the capture session
// lifecycle and a Publisher that hands immutable snapshots to any number of readers. It does not depend on Wails or
// on any UI, so the core can be tested on its own.
package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"wwlogs/internal/capture"
)

// Snapshot is one published state: the service status and, once a session has produced one, the capture view. It is
// never modified after it is published. Readers must treat View as read-only; UI readers receive JSON copies.
type Snapshot struct {
	Sequence    uint64                `json:"sequence"`
	PublishedAt time.Time             `json:"publishedAt"`
	Service     Status                `json:"service"`
	View        *capture.ViewSnapshot `json:"view"`

	encodeOnce sync.Once
	encoded    []byte
	encodeErr  error
}

// JSON returns the snapshot encoded once and copied for the caller, so a reader can keep or change its bytes freely.
func (s *Snapshot) JSON() (json.RawMessage, error) {
	s.encodeOnce.Do(func() {
		s.encoded, s.encodeErr = json.Marshal(s)
	})
	if s.encodeErr != nil {
		return nil, s.encodeErr
	}
	return append(json.RawMessage(nil), s.encoded...), nil
}

// Publisher keeps the latest Snapshot. Publishing replaces it and wakes waiting readers; it never blocks and never
// queues, so a slow reader only skips intermediate snapshots. Readers poll Latest or wait with Next.
type Publisher struct {
	mu       sync.Mutex
	latest   *Snapshot
	sequence uint64
	changed  chan struct{}
}

// NewPublisher returns a Publisher holding an initial stopped snapshot.
func NewPublisher(initial Status) *Publisher {
	p := &Publisher{changed: make(chan struct{})}
	p.publish(initial, nil)
	return p
}

// publish stores a new snapshot built from status and view. view must not be shared with anything that changes it.
func (p *Publisher) publish(status Status, view *capture.ViewSnapshot) *Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sequence++
	snapshot := &Snapshot{Sequence: p.sequence, PublishedAt: time.Now(), Service: status, View: view}
	p.latest = snapshot
	if p.changed != nil {
		close(p.changed)
	}
	p.changed = make(chan struct{})
	return snapshot
}

// Latest returns the newest snapshot.
func (p *Publisher) Latest() *Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest
}

// Next returns the newest snapshot once its sequence is greater than after, waiting until one is published or ctx
// ends. When ctx ends first it returns the newest snapshot (same sequence) and ctx's error, so a reader can use the
// call as a heartbeat.
func (p *Publisher) Next(ctx context.Context, after uint64) (*Snapshot, error) {
	for {
		p.mu.Lock()
		latest, changed := p.latest, p.changed
		p.mu.Unlock()
		if latest.Sequence > after {
			return latest, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return latest, ctx.Err()
		}
	}
}
