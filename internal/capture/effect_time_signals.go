package capture

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// The local client's time signals that drive the effect clock (effect_clock.go). All three are messages the client sends
// to the server, decoded with the current protocol's field numbers in both build profiles:
//
//   - RTimeStopPush (a CombatPushData of CombatSendPackRequest): a TimeStopRequest begins with its declared Duration in
//     milliseconds and its scale as Dilation (100 x scale, absent for the default pause), and ends with Dilation 100 and
//     no Duration; CombatCommon.EntityId is the caster.
//   - TimeStopPush (a top-level message): the new global time dilation as a float; an empty body is 0.
//   - TimeCheckRequest (a top-level message the client sends every 3 s): its FlowTimeDilation and TimeDilation then; an
//     absent field is 0.

// rTimeStopPushFullDilation is RTimeStopPush.Dilation of an end: 100 percent.
const rTimeStopPushFullDilation = 100

// rTimeStop is one decoded RTimeStopPush.
type rTimeStop struct {
	begin    bool
	duration time.Duration
	// scale is a begin's rate lock: 0 pauses effects, a value in (0, 1) slows them (Dilation / 100).
	scale float64
	// known is false for a push that states neither a duration nor a dilation, which names no begin or end.
	known bool
}

func decodeRTimeStopPush(body []byte) (rTimeStop, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return rTimeStop{}, fmt.Errorf("parse RTimeStopPush: %w", err)
	}
	var duration, dilation uint64
	var hasDuration, hasDilation bool
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case fieldRTimeStopPushDuration:
			duration, hasDuration = field.value, true
		case fieldRTimeStopPushDilation:
			dilation, hasDilation = field.value, true
		}
	}
	durationMs := int64(int32(uint32(duration)))
	scale := 0.0
	if signed := int64(int32(uint32(dilation))); hasDilation && signed > 0 {
		scale = math.Min(float64(signed)/rTimeStopPushFullDilation, 1)
	}
	switch {
	case hasDuration && durationMs > 0:
		return rTimeStop{begin: true, duration: time.Duration(durationMs) * time.Millisecond, scale: scale, known: true}, nil
	case hasDilation && dilation == rTimeStopPushFullDilation:
		return rTimeStop{known: true}, nil
	case hasDilation:
		// A begin without a declared duration: the stop runs effects at its scale (CharacterBuffComponent.AddPauseLock).
		return rTimeStop{begin: true, scale: scale, known: true}, nil
	}
	return rTimeStop{}, nil
}

// observeRTimeStopPush feeds one RTimeStopPush of the local client to the effect clock.
func (p *processor) observeRTimeStopPush(frame gameFrame, body []byte, common combatCommonContext) error {
	stop, err := decodeRTimeStopPush(body)
	if err != nil {
		return err
	}
	if !stop.known {
		p.stats.timeStopPushesWithoutState++
		return nil
	}
	var caster uint64
	if common.entityID != nil {
		caster = *common.entityID
	}
	p.effects.ObserveTimeStop(p.sessionNumber, caster, stop.begin, stop.duration, stop.scale, frame.timestamp)
	return nil
}

// decodeFloatFields reads the float32 fields of a message by number; an absent field reads 0, as proto3 leaves it off.
func decodeFloatFields(body []byte, numbers ...uint64) ([]float64, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return nil, err
	}
	values := make([]float64, len(numbers))
	for _, field := range fields {
		if field.wire != 5 || len(field.data) != 4 {
			continue
		}
		for i, number := range numbers {
			if field.number == number {
				values[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(field.data)))
			}
		}
	}
	return values, nil
}

// observeTimeStopPush feeds the local client's new global time dilation to the effect clock.
func (p *processor) observeTimeStopPush(frame gameFrame, body []byte) error {
	if frame.direction != "out" {
		return nil
	}
	values, err := decodeFloatFields(body, fieldTimeStopPushTimeDilation)
	if err != nil {
		return fmt.Errorf("parse TimeStopPush: %w", err)
	}
	p.effects.ObserveTimeDilation(p.sessionNumber, values[0], frame.timestamp)
	return nil
}

// observeTimeCheckRequest reconciles the effect clock with the local client's periodic time check.
func (p *processor) observeTimeCheckRequest(frame gameFrame, body []byte) error {
	if frame.direction != "out" {
		return nil
	}
	values, err := decodeFloatFields(body, fieldTimeCheckRequestFlowTimeDilation, fieldTimeCheckRequestTimeDilation)
	if err != nil {
		return fmt.Errorf("parse TimeCheckRequest: %w", err)
	}
	p.effects.ObserveTimeCheck(p.sessionNumber, values[0], values[1], frame.timestamp)
	return nil
}
