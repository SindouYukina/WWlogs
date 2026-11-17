//go:build wwdev

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

// observeTimerSchema keeps challenge timer fields separate from the decoded
// payload and correlates them only with the observed TimeCheckResponse FlowTime.
func (p *Processor) observeTimerSchema(frame GameFrame, messageName string, decoded *decodedMessage) {
	frameOrdinal := uint64(0)
	if p.Dev.frameOrdinal > 0 {
		frameOrdinal = p.Dev.frameOrdinal - 1
	}
	switch messageName {
	case "TimeCheckResponse":
		flowTime, ok := scalarValue(decoded, "FlowTime")
		if !ok || flowTime <= 0 {
			return
		}
		for i := range p.Dev.timerObservations {
			observation := &p.Dev.timerObservations[i]
			if observation.sessionNumber != p.SessionNumber || observation.nextFlowFrame != 0 || observation.frameOrdinal >= frameOrdinal {
				continue
			}
			observation.nextFlowTime = flowTime
			observation.nextFlowFrame = frameOrdinal
		}
		p.Dev.flowTime = flowTime
		p.Dev.hasFlowTime = true
		p.Dev.flowTimeFrame = frameOrdinal
	case "UpdateTimerInfoNotify":
		info := firstChildMessage(decoded, "TimerInfo")
		if info == nil {
			return
		}
		observation := timerObservation{
			sessionNumber: p.SessionNumber,
			frameOrdinal:  frameOrdinal,
			timestamp:     frame.Timestamp,
			timerType:     stringScalarValue(info, "TimerType"),
		}
		if nodeID, ok := scalarValue(info, "NodeId"); ok {
			observation.nodeID, observation.hasNodeID = nodeID, true
		}
		if endTime, ok := scalarValue(info, "EndTime"); ok {
			observation.endTime, observation.hasEndTime = endTime, true
		}
		if p.Dev.hasFlowTime {
			observation.priorFlowTime = p.Dev.flowTime
			observation.priorFlowFrame = p.Dev.flowTimeFrame
		}
		if len(p.Dev.timerObservations) == maxTimerObservations {
			copy(p.Dev.timerObservations, p.Dev.timerObservations[1:])
			p.Dev.timerObservations = p.Dev.timerObservations[:len(p.Dev.timerObservations)-1]
		}
		p.Dev.timerObservations = append(p.Dev.timerObservations, observation)
	}
}

func stringScalarValue(message *decodedMessage, name string) string {
	if message == nil {
		return ""
	}
	for _, field := range message.Fields {
		if field.Name == name {
			if value, ok := field.Value.(string); ok {
				return value
			}
		}
	}
	return ""
}
