package capture

import (
	"time"
)

const maxTimerObservations = 256

type timerObservation struct {
	sessionNumber  int
	frameOrdinal   uint64
	timestamp      time.Time
	timerType      string
	nodeID         int64
	hasNodeID      bool
	endTime        int64
	hasEndTime     bool
	priorFlowTime  int64
	priorFlowFrame uint64
	nextFlowTime   int64
	nextFlowFrame  uint64
}
