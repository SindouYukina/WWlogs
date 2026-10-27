//go:build wwdev

package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// observeBuffInterval updates the Buff state from one lifecycle row. Only
// server-side gameplay-effect APPLY and REMOVE rows with a CORRELATED or VERIFIED
// target participate. Request notifies are not applied state.
func (p *processor) observeBuffInterval(row buffLifecycleAuditRow) {
	if row.EventKind != "GAMEPLAY_EFFECT_APPLY" && row.EventKind != "GAMEPLAY_EFFECT_REMOVE" {
		return
	}
	if row.TargetStatus != resolutionCorrelated && row.TargetStatus != resolutionVerified {
		p.buffOverlapSkipped++
		return
	}
	if row.TargetEntityID == nil || row.HandleID == nil {
		p.buffOverlapSkipped++
		return
	}
	key := overlapKey{session: row.SessionNumber, target: *row.TargetEntityID, handle: *row.HandleID}
	if p.buffIntervalOpen == nil {
		p.buffIntervalOpen = make(map[overlapKey]*buffInterval)
	}
	switch row.EventKind {
	case "GAMEPLAY_EFFECT_APPLY":
		if _, open := p.buffIntervalOpen[key]; open {
			p.buffDuplicateApplies++
			return
		}
		if len(p.buffIntervals) >= maxBuffIntervals {
			p.buffIntervalOverflow++
			return
		}
		interval := &buffInterval{key: key, start: row.Timestamp}
		if row.BuffID != nil {
			interval.buffID, interval.hasBuffID = *row.BuffID, true
		}
		p.buffIntervals = append(p.buffIntervals, interval)
		p.buffIntervalOpen[key] = interval
	case "GAMEPLAY_EFFECT_REMOVE":
		interval, open := p.buffIntervalOpen[key]
		if !open {
			p.buffUnmatchedRemoves++
			return
		}
		interval.end = row.Timestamp
		interval.closed = true
		delete(p.buffIntervalOpen, key)
	}
}

// recordDamageForOverlap stores a typed damage result for the overlap query. Only
// results with a resolved target runtime entity are recorded.
func (p *processor) recordDamageForOverlap(event combatEvent) {
	if event.Target.Runtime.entityID == 0 {
		return
	}
	if len(p.damageOverlap) >= maxOverlapDamageRecord {
		p.damageOverlapOverflow++
		return
	}
	p.damageOverlap = append(p.damageOverlap, damageOverlapRecord{
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
	for _, record := range p.damageOverlap {
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
		for _, interval := range p.buffIntervals {
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
