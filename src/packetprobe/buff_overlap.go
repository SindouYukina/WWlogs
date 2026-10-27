package main

import (
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
