//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Bounds keep offline replay memory finite. A capture that exceeds them keeps
// counting and reports the overflow instead of growing without limit.
const (
	maxBuffIntervals       = 65536
	maxOverlapDamageRecord = 262144
)

// overlapKey identifies one gameplay-effect instance. The handle is the server
// identifier for the instance, so the same handle on the same target is one
// instance until its REMOVE is observed.
type overlapKey struct {
	session int
	target  uint64
	handle  uint64
}

// buffInterval is an observed APPLY-to-REMOVE span on one target. An interval
// with no REMOVE in the capture stays open to the end of the capture and is
// labelled as open. A REMOVE without a matching APPLY creates no interval.
type buffInterval struct {
	key       overlapKey
	buffID    uint64
	hasBuffID bool
	start     time.Time
	end       time.Time
	closed    bool
}

// damageOverlapRecord is one typed damage result with the target and time needed
// to query the active Buff instances.
type damageOverlapRecord struct {
	session   int
	rpcID     uint64
	timestamp time.Time
	target    uint64
	damage    int64
}

// activeBuffJSONL is one Buff instance active on the target at a damage time.
type activeBuffJSONL struct {
	HandleID  uint64  `json:"handleId"`
	BuffID    *uint64 `json:"buffId,omitempty"`
	Start     string  `json:"start"`
	End       *string `json:"end,omitempty"`
	OpenAtEnd bool    `json:"openAtEnd"`
}

// overlapRowJSONL pairs one damage result with the Buff instances active on its
// target. Precision is packet-observed time: the state is observed from packet
// timestamps, not from server-side settlement order.
type overlapRowJSONL struct {
	SchemaVersion  int               `json:"schemaVersion"`
	SessionNumber  int               `json:"sessionNumber"`
	RPCID          uint64            `json:"rpcId"`
	Timestamp      string            `json:"timestamp"`
	TargetEntityID uint64            `json:"targetEntityId"`
	Damage         int64             `json:"damage"`
	Precision      string            `json:"precision"`
	ActiveBuffs    []activeBuffJSONL `json:"activeBuffs"`
}

// observeBuffInterval updates the Buff state from one lifecycle row. Only
// server-side gameplay-effect APPLY and REMOVE rows with a CORRELATED or VERIFIED
// target participate. Request notifies are not applied state.
func (p *processor) observeBuffInterval(row buffLifecycleAuditRow) {
	if row.EventKind != "GAMEPLAY_EFFECT_APPLY" && row.EventKind != "GAMEPLAY_EFFECT_REMOVE" {
		return
	}
	if row.TargetStatus != resolutionCorrelated && row.TargetStatus != resolutionVerified {
		p.dev.buffOverlapSkipped++
		return
	}
	if row.TargetEntityID == nil || row.HandleID == nil {
		p.dev.buffOverlapSkipped++
		return
	}
	key := overlapKey{session: row.SessionNumber, target: *row.TargetEntityID, handle: *row.HandleID}
	if p.dev.buffIntervalOpen == nil {
		p.dev.buffIntervalOpen = make(map[overlapKey]*buffInterval)
	}
	switch row.EventKind {
	case "GAMEPLAY_EFFECT_APPLY":
		if _, open := p.dev.buffIntervalOpen[key]; open {
			p.dev.buffDuplicateApplies++
			return
		}
		if len(p.dev.buffIntervals) >= maxBuffIntervals {
			p.dev.buffIntervalOverflow++
			return
		}
		interval := &buffInterval{key: key, start: row.Timestamp}
		if row.BuffID != nil {
			interval.buffID, interval.hasBuffID = *row.BuffID, true
		}
		p.dev.buffIntervals = append(p.dev.buffIntervals, interval)
		p.dev.buffIntervalOpen[key] = interval
	case "GAMEPLAY_EFFECT_REMOVE":
		interval, open := p.dev.buffIntervalOpen[key]
		if !open {
			p.dev.buffUnmatchedRemoves++
			return
		}
		interval.end = row.Timestamp
		interval.closed = true
		delete(p.dev.buffIntervalOpen, key)
	}
}

// recordDamageForOverlap stores a typed damage result for the overlap query. Only
// results with a resolved target runtime entity are recorded.
func (p *processor) recordDamageForOverlap(event combatEvent) {
	if event.Target.Runtime.entityID == 0 {
		return
	}
	if len(p.dev.damageOverlap) >= maxOverlapDamageRecord {
		p.dev.damageOverlapOverflow++
		return
	}
	p.dev.damageOverlap = append(p.dev.damageOverlap, damageOverlapRecord{
		session:   event.SessionNumber,
		rpcID:     event.RPCID,
		timestamp: event.Timestamp,
		target:    event.Target.Runtime.entityID,
		damage:    event.Damage,
	})
}

// writeBuffOverlap writes one row per recorded damage result. Results with no
// active instance still get a row, so a query can distinguish "no active Buff"
// from "not recorded".
func (p *processor) writeBuffOverlap(w io.Writer) error {
	const precision = "PACKET_OBSERVED_TIME"
	for _, record := range p.dev.damageOverlap {
		row := overlapRowJSONL{
			SchemaVersion:  1,
			SessionNumber:  record.session,
			RPCID:          record.rpcID,
			Timestamp:      formatTimestamp(record.timestamp),
			TargetEntityID: record.target,
			Damage:         record.damage,
			Precision:      precision,
			ActiveBuffs:    []activeBuffJSONL{},
		}
		for _, interval := range p.dev.buffIntervals {
			if interval.key.session != record.session || interval.key.target != record.target {
				continue
			}
			if record.timestamp.Before(interval.start) {
				continue
			}
			if interval.closed && !record.timestamp.Before(interval.end) {
				continue
			}
			active := activeBuffJSONL{HandleID: interval.key.handle, Start: formatTimestamp(interval.start), OpenAtEnd: !interval.closed}
			if interval.hasBuffID {
				id := interval.buffID
				active.BuffID = &id
			}
			if interval.closed {
				end := formatTimestamp(interval.end)
				active.End = &end
			}
			row.ActiveBuffs = append(row.ActiveBuffs, active)
		}
		line, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("encode buff overlap row: %w", err)
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write buff overlap row: %w", err)
		}
	}
	return nil
}
