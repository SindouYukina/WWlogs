package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

type listeningReplayReport struct {
	Name                string    `json:"name"`
	Frames              int       `json:"frames"`
	Handshakes          int       `json:"handshakes"`
	HandshakeErrors     int       `json:"handshakeErrors"`
	DecryptErrors       int       `json:"decryptErrors"`
	FramesBeforeKey     int       `json:"framesBeforeKey"`
	SecondsToKey        []float64 `json:"secondsFromFirstFrameToKey"`
	DurationSeconds     float64   `json:"durationSeconds"`
	GapP50Seconds       float64   `json:"gapP50Seconds"`
	GapP99Seconds       float64   `json:"gapP99Seconds"`
	GapMaxSeconds       float64   `json:"gapMaxSeconds"`
	GapsOver5Seconds    int       `json:"gapsOver5Seconds"`
	GapsOver15Seconds   int       `json:"gapsOver15Seconds"`
	GapsOver60Seconds   int       `json:"gapsOver60Seconds"`
	HandshakeGapSeconds []float64 `json:"precedingFrameGapAtHandshake"`
}

// TestListeningReplayCollector measures how long decoded game traffic can pause inside real captures. The Listening
// status thresholds are chosen from this distribution rather than from a guessed timeout.
func TestListeningReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_LISTENING_REPLAY_OUT")
	if out == "" {
		t.Skip("WWLOGS_LISTENING_REPLAY_OUT is not set")
	}
	master, listPath := os.Getenv("WWLOGS_SESSION_REPLAY_MASTER_DB"), os.Getenv("WWLOGS_SESSION_REPLAY_PCAPS")
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := LoadMasterData(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	defer masters.Close()
	reports := []listeningReplayReport{}
	for _, capture := range readSegmentDropList(t, listPath) {
		t.Run(capture.name, func(t *testing.T) {
			report := listeningReplayReport{Name: capture.name}
			var first, previous time.Time
			gaps := []float64{}
			lastOK := 0
			observe := func(p *Processor) {
				at := p.CurrentFrameTime
				if at.IsZero() || at.Equal(previous) {
					return
				}
				if first.IsZero() {
					first = at
				}
				if p.Stats.HandshakeOK == 0 {
					report.FramesBeforeKey++
				}
				if p.Stats.HandshakeOK != lastOK {
					lastOK = p.Stats.HandshakeOK
					report.SecondsToKey = append(report.SecondsToKey, at.Sub(first).Seconds())
					if !previous.IsZero() {
						report.HandshakeGapSeconds = append(report.HandshakeGapSeconds, at.Sub(previous).Seconds())
					}
				} else if !previous.IsZero() && p.Stats.HandshakeOK > 0 {
					gaps = append(gaps, at.Sub(previous).Seconds())
				}
				previous = at
			}
			p, stats := equivalenceReplay(t, equivalenceInputs{localIP: "192.168.1.2"}, masters.masters, key, capture.path, nil, func(_ int, p *Processor) { observe(p) })
			report.Frames = stats.Frames
			report.Handshakes = p.Stats.HandshakeOK
			report.HandshakeErrors = p.Stats.HandshakeErrors
			report.DecryptErrors = p.Stats.DecryptErrors
			report.DurationSeconds = previous.Sub(first).Seconds()
			sort.Float64s(gaps)
			if len(gaps) > 0 {
				report.GapP50Seconds = gaps[len(gaps)/2]
				report.GapP99Seconds = gaps[len(gaps)*99/100]
				report.GapMaxSeconds = gaps[len(gaps)-1]
			}
			for _, gap := range gaps {
				if gap > 5 {
					report.GapsOver5Seconds++
				}
				if gap > 15 {
					report.GapsOver15Seconds++
				}
				if gap > 60 {
					report.GapsOver60Seconds++
				}
			}
			reports = append(reports, report)
		})
	}
	data, err := json.MarshalIndent(reports, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "listening-replay.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
