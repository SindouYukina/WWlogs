//go:build wwdev

package capture

// observeTimerSchema keeps challenge timer fields separate from the decoded
// payload and correlates them only with the observed TimeCheckResponse FlowTime.
func (p *processor) observeTimerSchema(frame gameFrame, messageName string, decoded *decodedMessage) {
	frameOrdinal := uint64(0)
	if p.frameOrdinal > 0 {
		frameOrdinal = p.frameOrdinal - 1
	}
	switch messageName {
	case "TimeCheckResponse":
		flowTime, ok := scalarValue(decoded, "FlowTime")
		if !ok || flowTime <= 0 {
			return
		}
		for i := range p.timerObservations {
			observation := &p.timerObservations[i]
			if observation.sessionNumber != p.sessionNumber || observation.nextFlowFrame != 0 || observation.frameOrdinal >= frameOrdinal {
				continue
			}
			observation.nextFlowTime = flowTime
			observation.nextFlowFrame = frameOrdinal
		}
		p.flowTime = flowTime
		p.hasFlowTime = true
		p.flowTimeFrame = frameOrdinal
	case "UpdateTimerInfoNotify":
		info := firstChildMessage(decoded, "TimerInfo")
		if info == nil {
			return
		}
		observation := timerObservation{
			sessionNumber: p.sessionNumber,
			frameOrdinal:  frameOrdinal,
			timestamp:     frame.timestamp,
			timerType:     stringScalarValue(info, "TimerType"),
		}
		if nodeID, ok := scalarValue(info, "NodeId"); ok {
			observation.nodeID, observation.hasNodeID = nodeID, true
		}
		if endTime, ok := scalarValue(info, "EndTime"); ok {
			observation.endTime, observation.hasEndTime = endTime, true
		}
		if p.hasFlowTime {
			observation.priorFlowTime = p.flowTime
			observation.priorFlowFrame = p.flowTimeFrame
		}
		if len(p.timerObservations) == maxTimerObservations {
			copy(p.timerObservations, p.timerObservations[1:])
			p.timerObservations = p.timerObservations[:len(p.timerObservations)-1]
		}
		p.timerObservations = append(p.timerObservations, observation)
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
