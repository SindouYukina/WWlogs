package capture

import (
	"time"
)

func overlapRow(kind string, status ResolutionStatus, target, handle uint64, at time.Time) BuffLifecycleAuditRow {
	row := BuffLifecycleAuditRow{EventKind: kind, TargetStatus: status, SessionNumber: 1, Timestamp: at}
	row.TargetEntityID = &target
	row.HandleID = &handle
	return row
}
