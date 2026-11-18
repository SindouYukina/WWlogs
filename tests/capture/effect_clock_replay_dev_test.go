package capture

import (
	"os"
	"testing"
	"time"
)

// TestEffectClockReplayCollector replays real captures and reports, every 250 ms of capture time, how many HUD statuses
// the Effects Overlay would have hidden as predicted_expired while the tracker still held them: once on the effect clock
// the product now uses (effect_clock.go), and once by the earlier rule that counted down on the capture clock. It is a
// Replay Observation of the effect clock, not a Golden: the independent check of the model is the research record
// research/runs/2026-11-14/v0.7.0-effect-time-semantics.
//
// It is skipped unless WWLOGS_EFFECT_CLOCK_REPLAY_PCAPS is set and reads only paths supplied through the environment.
//
// Environment:
//
//	WWLOGS_EFFECT_CLOCK_REPLAY_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_EFFECT_CLOCK_REPLAY_MASTER_DB  Master SQLite database
func TestEffectClockReplayCollector(t *testing.T) {
	list := os.Getenv("WWLOGS_EFFECT_CLOCK_REPLAY_PCAPS")
	if list == "" {
		t.Skip("WWLOGS_EFFECT_CLOCK_REPLAY_PCAPS is not set")
	}
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	masters, err := LoadGameMasterDatabase(os.Getenv("WWLOGS_EFFECT_CLOCK_REPLAY_MASTER_DB"), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	const step = 250 * time.Millisecond
	for _, capture := range readReplayPCAPList(t, list) {
		capture := capture
		t.Run(capture.name, func(t *testing.T) {
			var effectHidden, wallHidden, samples int
			hiddenOnWall := map[effectKey]bool{}
			hiddenOnEffect := map[effectKey]bool{}
			last := time.Time{}
			p, _ := equivalenceReplay(t, equivalenceInputs{localIP: "192.168.1.2"}, masters, key, capture.path, nil, func(index int, p *Processor) {
				at := p.CurrentFrameTime
				if at.IsZero() || (!last.IsZero() && at.Sub(last) < step) {
					return
				}
				last = at
				samples++
				tracker := p.Effects
				tracker.mu.RLock()
				defer tracker.mu.RUnlock()
				query := CaptureQueryTime{CaptureSessionID: tracker.CaptureSessionID, Timestamp: at}
				for key, instance := range tracker.instances {
					if !instance.timeKnown {
						continue
					}
					snapshot := tracker.instanceSnapshotLocked(instance, query, "")
					display := EffectDisplayFor(snapshot)
					if display.Eligibility == EffectDisplayPredictedExpired {
						effectHidden++
						hiddenOnEffect[key] = true
					}
					// The capture-clock rule: the same instance, counted down from its basis on the capture clock.
					if display.Eligibility != EffectDisplayEligible && display.Eligibility != EffectDisplayPredictedExpired {
						continue
					}
					wallOverdue := at.Sub(instance.basisAt).Seconds() - instance.basisRemaining
					if wallOverdue > EffectDisplayExpiryGrace.Seconds() {
						wallHidden++
						hiddenOnWall[key] = true
					}
				}
			})
			p.FinishCapture()
			counters := p.Effects.SnapshotAt(CaptureQueryTime{CaptureSessionID: p.Effects.CaptureSessionID, Timestamp: p.CurrentFrameTime}).Counters.Clock
			t.Logf("samples %d (every %s)", samples, step)
			t.Logf("predicted_expired HUD instance-seconds: capture clock %.1f s over %d instances, effect clock %.1f s over %d instances",
				float64(wallHidden)*step.Seconds(), len(hiddenOnWall), float64(effectHidden)*step.Seconds(), len(hiddenOnEffect))
			t.Logf("effect clock: %+v", counters)
			if counters.StopsBegun == 0 && counters.DilationChanges == 0 {
				t.Log("the capture has no time stop or global dilation")
			}
		})
	}
}
