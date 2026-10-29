//go:build wwdev

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTimerRemainingUsesAdjacentFlowTimeSamples(t *testing.T) {
	p := &processor{sessionNumber: 1, encounterSummary: &bytes.Buffer{}}
	p.frameOrdinal = 20
	p.observeTimerSchema(gameFrame{}, "TimeCheckResponse", &decodedMessage{Fields: []decodedField{{Name: "FlowTime", Value: int64(1000)}}})
	p.frameOrdinal = 25
	timer := &decodedMessage{Fields: []decodedField{{Name: "TimerInfo", Message: &decodedMessage{Fields: []decodedField{
		{Name: "TimerType", Value: "CountDownChallenge"},
		{Name: "NodeId", Value: int64(28)},
		{Name: "EndTime", Value: int64(301000)},
	}}}}}
	p.observeTimerSchema(gameFrame{timestamp: time.Unix(100, 0)}, "UpdateTimerInfoNotify", timer)
	p.frameOrdinal = 31
	p.observeTimerSchema(gameFrame{}, "TimeCheckResponse", &decodedMessage{Fields: []decodedField{{Name: "FlowTime", Value: int64(4000)}}})

	var out bytes.Buffer
	if err := writeTimerObservations(&out, p.timerObservations); err != nil {
		t.Fatalf("write timer report: %v", err)
	}
	if !strings.Contains(out.String(), "approximately 297.000-300.000 seconds") {
		t.Fatalf("timer range missing from report: %s", out.String())
	}
}
