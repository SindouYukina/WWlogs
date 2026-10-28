package main

import (
	"time"
)

func overlapRow(kind string, status resolutionStatus, target, handle uint64, at time.Time) buffLifecycleAuditRow {
	row := buffLifecycleAuditRow{EventKind: kind, TargetStatus: status, SessionNumber: 1, Timestamp: at}
	row.TargetEntityID = &target
	row.HandleID = &handle
	return row
}
