package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// sessionReplayIntervals are the user-selectable UI update intervals (Settings, General). The session runs once per
// interval and once switching between them while it runs; every run must end with the same view.
var sessionReplayIntervals = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond, 1000 * time.Millisecond}

// sessionViewChecks name what the collector verifies about the views of every session run (checkSessionRun). The list
// goes into the summary, so comparing two summaries also compares what was verified.
var sessionViewChecks = []string{
	"initial-before-first-packet",
	"view-equals-processor-after-same-packets",
	"packet-order",
	"one-final-after-every-packet",
	"final-equals-processor-final",
	"at-most-one-view-per-interval",
	"no-silence-over-4-intervals",
}

// sessionViewSilenceIntervals bounds the wall time between two views of one run, in publish intervals. A replay without
// pacing offers the session a publish decision after every packet, so a longer silence means it stopped publishing.
const sessionViewSilenceIntervals = 4

// sessionWallClockFields are the summary fields that depend on wall time. A replay without pacing publishes one view per
// interval of wall time, so a faster executable publishes fewer views from the same packets: these fields are samples,
// never results, and comparisons of two summaries must check them against the publish contract instead of for equality.
var sessionWallClockFields = []string{"snapshots", "snapshotsByInterval", "publication"}

// intervalTotals are the statistics totals of one interval, read directly from the engine.
type intervalTotals struct {
	DamageDealt, DamageTaken                               int64
	OutgoingEvents, IncomingEvents                         uint64
	UnattributedOutgoingDamage, UnattributedIncomingDamage int64
	UnattributedOutgoingEvents, UnattributedIncomingEvents uint64
	DuplicateEvents                                        uint64
}

func (a *intervalTotals) add(b intervalTotals) {
	a.DamageDealt += b.DamageDealt
	a.DamageTaken += b.DamageTaken
	a.OutgoingEvents += b.OutgoingEvents
	a.IncomingEvents += b.IncomingEvents
	a.UnattributedOutgoingDamage += b.UnattributedOutgoingDamage
	a.UnattributedIncomingDamage += b.UnattributedIncomingDamage
	a.UnattributedOutgoingEvents += b.UnattributedOutgoingEvents
	a.UnattributedIncomingEvents += b.UnattributedIncomingEvents
	a.DuplicateEvents += b.DuplicateEvents
}

func engineTotals(s *DamageStatistics) (uint64, intervalTotals) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statisticsIntervalID, intervalTotals{
		DamageDealt: s.damageDealt, DamageTaken: s.damageTaken, OutgoingEvents: s.outgoingEvents, IncomingEvents: s.incomingEvents,
		UnattributedOutgoingDamage: s.unattributedOutgoingDamage, UnattributedIncomingDamage: s.unattributedIncomingDamage,
		UnattributedOutgoingEvents: s.unattributedOutgoingEvents, UnattributedIncomingEvents: s.unattributedIncomingEvents,
		DuplicateEvents: s.duplicateEvents,
	}
}

func viewTotals(v ViewSnapshot) intervalTotals {
	s := v.Statistics
	return intervalTotals{
		DamageDealt: s.DamageDealt, DamageTaken: s.DamageTaken, OutgoingEvents: s.OutgoingEvents, IncomingEvents: s.IncomingEvents,
		UnattributedOutgoingDamage: s.Unattributed.OutgoingDamage, UnattributedIncomingDamage: s.Unattributed.IncomingDamage,
		UnattributedOutgoingEvents: s.Unattributed.OutgoingEvents, UnattributedIncomingEvents: s.Unattributed.IncomingEvents,
		DuplicateEvents: s.DuplicateEvents,
	}
}

// comparableView drops the process-local capture session sequence and the capture-layer counters that only the
// executable loop maintains (packets read); everything computed from the packets is kept.
func comparableView(v ViewSnapshot) ViewSnapshot {
	v.CaptureSessionID = 0
	v.Health.Capture = ViewCaptureHealth{}
	// Frame ages depend on the clock of the capture loop, not on decoding. Every other session field is still compared.
	v.Health.Session.FirstFrameAgeSeconds, v.Health.Session.FrameIdleSeconds = nil, nil
	fix := func(e *ViewEncounter) {
		if _, rest, ok := strings.Cut(e.EncounterID, "-"); ok {
			e.EncounterID = rest
		}
	}
	if v.Encounters.Current != nil {
		current := *v.Encounters.Current
		fix(&current)
		v.Encounters.Current = &current
	}
	recent := append([]ViewEncounter(nil), v.Encounters.Recent...)
	for i := range recent {
		fix(&recent[i])
	}
	v.Encounters.Recent = recent
	// The timeline names its encounter with the same process-local prefix; everything else in it is compared.
	if v.Timeline != nil {
		timeline := *v.Timeline
		if _, rest, ok := strings.Cut(timeline.EncounterID, "-"); ok {
			timeline.EncounterID = rest
		}
		v.Timeline = &timeline
	}
	return v
}

// viewPosition is the number of packets the session had read when it built the view.
func viewPosition(v ViewSnapshot) uint64 {
	return v.Health.Capture.PacketsRead
}

// sessionRun is what one Session.Run published: every view, the wall time its publish call started, and the publish
// interval in effect after it (the switching run changes it after every view).
type sessionRun struct {
	views     []ViewSnapshot
	at        []time.Time
	intervals []time.Duration
	// elapsed is the wall time from before Run to after it returned; packets is SessionResult.UDPPackets.
	elapsed time.Duration
	packets int
}

// checkSessionRun checks the views one session run published against the publish contract (Session.Run) and the
// processor. reference holds the processor's comparable view after each packet count a view reported (0 is before the
// first packet), final is the processor's comparable final view, and packets is the capture's UDP datagram count. Only
// the wall-clock bounds read run.at and run.elapsed; their verdicts hold at any execution speed.
func checkSessionRun(run sessionRun, reference map[uint64]ViewSnapshot, final ViewSnapshot, packets int) []string {
	views := run.views
	if len(views) < 2 {
		return []string{fmt.Sprintf("published %d views: a run publishes an initial and a final view", len(views))}
	}
	if len(run.at) != len(views) || len(run.intervals) != len(views) {
		return []string{fmt.Sprintf("recorded %d views, %d times and %d intervals", len(views), len(run.at), len(run.intervals))}
	}
	var problems []string
	last := len(views) - 1
	if position := viewPosition(views[0]); position != 0 {
		problems = append(problems, fmt.Sprintf("the initial view was built after %d packets", position))
	}
	for i, view := range views[:last] {
		if view.Final {
			problems = append(problems, fmt.Sprintf("view %d of %d is final before the capture ended", i+1, len(views)))
		}
	}
	if !views[last].Final {
		problems = append(problems, "the last view is not final")
	}
	for i := 1; i < len(views); i++ {
		if before, now := viewPosition(views[i-1]), viewPosition(views[i]); now < before {
			problems = append(problems, fmt.Sprintf("view %d (after %d packets) follows view %d (after %d packets)", i+1, now, i, before))
		}
	}
	if position := viewPosition(views[last]); position != uint64(packets) || run.packets != packets {
		problems = append(problems, fmt.Sprintf("the final view was built after %d packets and the run read %d; the capture has %d", position, run.packets, packets))
	}
	for i, view := range views[:last] {
		want, ok := reference[viewPosition(view)]
		if !ok {
			problems = append(problems, fmt.Sprintf("no processor view after %d packets for view %d", viewPosition(view), i+1))
			continue
		}
		if got := comparableView(view); !reflect.DeepEqual(got, want) {
			problems = append(problems, fmt.Sprintf("view %d (after %d packets) differs from the processor: %s", i+1, viewPosition(view), viewDifference(want, got)))
		}
	}
	if got := comparableView(views[last]); !reflect.DeepEqual(got, final) {
		problems = append(problems, "the final view differs from the processor's final view: "+viewDifference(final, got))
	}
	// Rate limit: a periodic view is published at least the interval in effect after the previous view, so the
	// intervals before the periodic views add up to no more than the run took.
	var minimum time.Duration
	for _, interval := range run.intervals[:last-1] {
		minimum += interval
	}
	if minimum > run.elapsed {
		problems = append(problems, fmt.Sprintf("%d periodic views in %v need at least %v at the publish intervals", last-1, run.elapsed, minimum))
	}
	for i := 1; i < len(views); i++ {
		if gap, limit := run.at[i].Sub(run.at[i-1]), sessionViewSilenceIntervals*run.intervals[i-1]; gap > limit {
			problems = append(problems, fmt.Sprintf("no view for %v after view %d (publish interval %v)", gap.Round(time.Millisecond), i, run.intervals[i-1]))
		}
	}
	return problems
}

// viewDifference names the first field in which two views differ, as JSON paths.
func viewDifference(want, got ViewSnapshot) string {
	decode := func(v ViewSnapshot) any {
		data, err := json.Marshal(v)
		if err != nil {
			return err.Error()
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return err.Error()
		}
		return value
	}
	if path := firstJSONDifference(decode(want), decode(got), "$"); path != "" {
		return path
	}
	return "a field JSON does not show"
}

func firstJSONDifference(a, b any, path string) string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return path
		}
		keys := make([]string, 0, len(x)+len(y))
		for key := range x {
			keys = append(keys, key)
		}
		for key := range y {
			if _, ok := x[key]; !ok {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			if found := firstJSONDifference(x[key], y[key], path+"."+key); found != "" {
				return found
			}
		}
		return ""
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return path
		}
		for i := range x {
			if found := firstJSONDifference(x[i], y[i], fmt.Sprintf("%s[%d]", path, i)); found != "" {
				return found
			}
		}
		return ""
	default:
		if !reflect.DeepEqual(a, b) {
			return path
		}
		return ""
	}
}

// sessionViewSummary is what the collector verified: deterministic, the same at any execution speed.
type sessionViewSummary struct {
	Runs   []string `json:"runs"`
	Checks []string `json:"checks"`
}

// sessionRunSample is one run's wall-clock sample: how many views it published, how long it took, and its longest gap
// between two views. A sample describes this execution only.
type sessionRunSample struct {
	Views            int   `json:"views"`
	ElapsedMillis    int64 `json:"elapsedMs"`
	LongestGapMillis int64 `json:"longestGapMs"`
}

type sessionReplaySummary struct {
	Name             string         `json:"name"`
	ProtocolSessions int            `json:"protocolSessions"`
	Intervals        int            `json:"intervals"`
	CLI              intervalTotals `json:"cli"`
	HostIntervalsSum intervalTotals `json:"hostIntervalsSum"`
	SessionFinal     intervalTotals `json:"sessionFinal"`
	// Snapshots and SnapshotsByInterval count the views the default interval's run and each run published
	// ("switching" changes the interval while running). They are wall-clock samples (sessionWallClockFields).
	Snapshots           int            `json:"snapshots"`
	SnapshotsByInterval map[string]int `json:"snapshotsByInterval"`
	FinalIntegrity      string         `json:"finalIntegrity"`
	FinalEncounterDPS   string         `json:"finalEncounterDps"`
	Encounters          uint64         `json:"encounters"`
	// ViewChecks lists the runs and the checks every one of their views passed; FinalViewSHA256 is the SHA-256 of the
	// processor's comparable final view as JSON, which every run's final view equals. Both are deterministic.
	ViewChecks      sessionViewSummary `json:"viewChecks"`
	FinalViewSHA256 string             `json:"finalViewSha256"`
	// Publication holds each run's wall-clock sample; WallClockFields names every field of this summary that is one.
	Publication     map[string]sessionRunSample `json:"publication"`
	WallClockFields []string                    `json:"wallClockFields"`
}

// TestSessionReplayCollector checks that the application host path changes no analysis result. For every capture:
// the host's per-protocol-session intervals add up to the CLI's single interval; capture.Session (OpenSession and Run,
// as the desktop host uses them) ends with exactly the view the same processor state gives; and every view a session
// run published is the processor's view after the same packets, in packet order, within the publish contract
// (checkSessionRun). How many views a run publishes depends on wall time and is reported as a sample only.
func TestSessionReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_SESSION_REPLAY_OUT")
	if out == "" {
		t.Skip("WWLOGS_SESSION_REPLAY_OUT is not set")
	}
	master, listPath := os.Getenv("WWLOGS_SESSION_REPLAY_MASTER_DB"), os.Getenv("WWLOGS_SESSION_REPLAY_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_SESSION_REPLAY_MASTER_DB and WWLOGS_SESSION_REPLAY_PCAPS are required")
	}
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := LoadMasterData(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	defer masters.Close()
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := equivalenceInputs{localIP: "192.168.1.2"}
	for _, capture := range readSegmentDropList(t, listPath) {
		t.Run(capture.name, func(t *testing.T) {
			cli, _ := equivalenceReplay(t, inputs, masters.masters, key, capture.path, nil, nil)
			cli.FinishCapture()
			_, cliTotals := engineTotals(cli.DamageStatistics)

			// The update interval only decides when views are built; the final view must not depend on it.
			runSession := func(interval time.Duration, switching bool) sessionRun {
				session, err := OpenSession(SessionConfig{LocalIPv4: inputs.localIP, ReplayPath: capture.path, PublishInterval: interval}, masters)
				if err != nil {
					t.Fatal(err)
				}
				var run sessionRun
				started := time.Now()
				result, err := session.Run(context.Background(), func(v ViewSnapshot) {
					run.at = append(run.at, time.Now())
					run.views = append(run.views, v)
					next := interval
					if switching {
						next = sessionReplayIntervals[len(run.views)%len(sessionReplayIntervals)]
						session.SetPublishInterval(next)
					}
					run.intervals = append(run.intervals, next)
				})
				run.elapsed, run.packets = time.Since(started), result.UDPPackets
				if err != nil || result.StopReason != StopReasonReplayEnd {
					t.Fatalf("session run: %+v, %v", result, err)
				}
				return run
			}
			labels := make([]string, 0, len(sessionReplayIntervals)+1)
			runs := map[string]sessionRun{}
			for _, interval := range sessionReplayIntervals {
				labels = append(labels, interval.String())
				runs[interval.String()] = runSession(interval, false)
			}
			labels = append(labels, "switching")
			runs["switching"] = runSession(sessionReplayIntervals[0], true)

			// One replay of the processor configured like the host's gives the interval totals and the reference view
			// after every packet count a published view reported.
			positions := map[uint64]bool{}
			for _, run := range runs {
				for _, view := range run.views {
					positions[viewPosition(view)] = true
				}
			}
			reference := map[uint64]ViewSnapshot{}
			last := map[uint64]intervalTotals{}
			host, stats := equivalenceReplay(t, inputs, masters.masters, key, capture.path, func(p *Processor) {
				p.IntervalPerProtocolSession = true
				if positions[0] {
					reference[0] = comparableView(p.ViewSnapshot())
				}
			}, func(index int, p *Processor) {
				id, totals := engineTotals(p.DamageStatistics)
				last[id] = totals
				if positions[uint64(index)] {
					reference[uint64(index)] = comparableView(p.ViewSnapshot())
				}
			})
			host.FinishCapture()
			id, totals := engineTotals(host.DamageStatistics)
			last[id] = totals
			var sum intervalTotals
			for _, totals := range last {
				sum.add(totals)
			}
			if sum != cliTotals {
				t.Errorf("host intervals do not add up to the CLI interval: host=%+v cli=%+v", sum, cliTotals)
			}
			hostFinal := host.ViewSnapshot()
			hostFinal.Final = true
			comparableFinal := comparableView(hostFinal)

			byInterval := map[string]int{}
			publication := map[string]sessionRunSample{}
			for _, label := range labels {
				run := runs[label]
				if problems := checkSessionRun(run, reference, comparableFinal, stats.UDPPackets); len(problems) > 0 {
					t.Errorf("%s run: %s", label, strings.Join(problems, "; "))
				}
				var longest time.Duration
				for i := 1; i < len(run.at); i++ {
					longest = max(longest, run.at[i].Sub(run.at[i-1]))
				}
				byInterval[label] = len(run.views)
				publication[label] = sessionRunSample{Views: len(run.views), ElapsedMillis: run.elapsed.Milliseconds(), LongestGapMillis: longest.Milliseconds()}
			}
			views := runs[DefaultPublishInterval.String()].views
			final := views[len(views)-1]
			if host.SessionNumber <= 1 && viewTotals(final) != cliTotals {
				t.Errorf("single protocol session: session totals %+v differ from the CLI %+v", viewTotals(final), cliTotals)
			}
			finalJSON, err := json.Marshal(comparableFinal)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(finalJSON)
			summary := sessionReplaySummary{
				Name: capture.name, ProtocolSessions: host.SessionNumber, Intervals: len(last), CLI: cliTotals, HostIntervalsSum: sum,
				SessionFinal: viewTotals(final), Snapshots: len(views), SnapshotsByInterval: byInterval, FinalIntegrity: string(final.Health.DataIntegrity),
				FinalEncounterDPS: string(final.Statistics.EncounterDPS.Status), Encounters: final.Encounters.CompletedTotal,
				ViewChecks: sessionViewSummary{Runs: labels, Checks: sessionViewChecks}, FinalViewSHA256: hex.EncodeToString(digest[:]),
				Publication: publication, WallClockFields: sessionWallClockFields,
			}
			data, _ := json.MarshalIndent(summary, "", "  ")
			if err := os.WriteFile(filepath.Join(out, capture.name+".session.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestCheckSessionRunDetectsViewDefects feeds checkSessionRun one valid run and runs with one defect each: every defect
// must be reported, so the collector cannot pass a run whose views are wrong, out of order, or outside the contract.
func TestCheckSessionRunDetectsViewDefects(t *testing.T) {
	const packets = 30
	view := func(position uint64, final bool, damage int64) ViewSnapshot {
		var v ViewSnapshot
		v.Health.Capture.PacketsRead = position
		v.Final = final
		v.Statistics.DamageDealt = damage
		return v
	}
	reference := map[uint64]ViewSnapshot{0: comparableView(view(0, false, 0)), 10: comparableView(view(10, false, 100)), 20: comparableView(view(20, false, 200))}
	final := comparableView(view(packets, true, 300))
	start := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	interval := 250 * time.Millisecond
	valid := func() sessionRun {
		return sessionRun{
			views:     []ViewSnapshot{view(0, false, 0), view(10, false, 100), view(20, false, 200), view(packets, true, 300)},
			at:        []time.Time{start, start.Add(260 * time.Millisecond), start.Add(520 * time.Millisecond), start.Add(600 * time.Millisecond)},
			intervals: []time.Duration{interval, interval, interval, interval},
			elapsed:   610 * time.Millisecond,
			packets:   packets,
		}
	}
	if problems := checkSessionRun(valid(), reference, final, packets); len(problems) > 0 {
		t.Fatalf("a valid run is reported: %v", problems)
	}
	cases := []struct {
		name   string
		change func(*sessionRun)
		want   string
	}{
		{"one view", func(r *sessionRun) { r.views, r.at, r.intervals = r.views[3:], r.at[3:], r.intervals[3:] }, "published 1 views"},
		{"initial after packets", func(r *sessionRun) { r.views[0] = view(10, false, 100) }, "initial view was built after 10 packets"},
		{"final too early", func(r *sessionRun) { r.views[1].Final = true }, "view 2 of 4 is final before the capture ended"},
		{"no final", func(r *sessionRun) { r.views[3].Final = false }, "the last view is not final"},
		{"out of packet order", func(r *sessionRun) { r.views[1], r.views[2] = r.views[2], r.views[1] }, "view 3 (after 10 packets) follows view 2 (after 20 packets)"},
		{"final before the end", func(r *sessionRun) { r.views[3].Health.Capture.PacketsRead = 25 }, "the final view was built after 25 packets"},
		{"packets lost by the run", func(r *sessionRun) { r.packets = 29 }, "the run read 29"},
		{"wrong content", func(r *sessionRun) { r.views[2].Statistics.DamageDealt = 201 }, "view 3 (after 20 packets) differs from the processor: $.statistics.damageDealt"},
		{"no reference", func(r *sessionRun) { r.views[2].Health.Capture.PacketsRead = 15 }, "no processor view after 15 packets"},
		{"wrong final", func(r *sessionRun) { r.views[3].Statistics.DamageDealt = 299 }, "the final view differs from the processor's final view: $.statistics.damageDealt"},
		{"rate limit", func(r *sessionRun) { r.elapsed = 400 * time.Millisecond }, "2 periodic views in 400ms need at least 500ms"},
		{"silence", func(r *sessionRun) {
			r.at[3] = start.Add(1600 * time.Millisecond)
			r.elapsed = 1700 * time.Millisecond
		}, "no view for 1.08s after view 3"},
		{"switching interval", func(r *sessionRun) { r.intervals[0] = time.Second }, "need at least 1.25s"},
	}
	for _, c := range cases {
		run := valid()
		c.change(&run)
		problems := checkSessionRun(run, reference, final, packets)
		if !strings.Contains(strings.Join(problems, "; "), c.want) {
			t.Errorf("%s: want a problem containing %q, got %v", c.name, c.want, problems)
		}
	}
}
