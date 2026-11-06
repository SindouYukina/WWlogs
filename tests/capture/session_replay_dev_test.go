//go:build wwdev

package capture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
	return v
}

type sessionReplaySummary struct {
	Name              string         `json:"name"`
	ProtocolSessions  int            `json:"protocolSessions"`
	Intervals         int            `json:"intervals"`
	CLI               intervalTotals `json:"cli"`
	HostIntervalsSum  intervalTotals `json:"hostIntervalsSum"`
	SessionFinal      intervalTotals `json:"sessionFinal"`
	Snapshots         int            `json:"snapshots"`
	FinalIntegrity    string         `json:"finalIntegrity"`
	FinalEncounterDPS string         `json:"finalEncounterDps"`
	Encounters        uint64         `json:"encounters"`
}

// TestSessionReplayCollector checks that the application host path changes no analysis result. For every capture:
// the host's per-protocol-session intervals add up to the CLI's single interval; and capture.Session (OpenSession and
// Run, as the desktop host uses them) ends with exactly the view the same processor state gives.
func TestSessionReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_SESSION_REPLAY_OUT")
	if out == "" {
		t.Skip("WWLOGS_SESSION_REPLAY_OUT is not set")
	}
	master, listPath := os.Getenv("WWLOGS_SESSION_REPLAY_MASTER_DB"), os.Getenv("WWLOGS_SESSION_REPLAY_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_SESSION_REPLAY_MASTER_DB and WWLOGS_SESSION_REPLAY_PCAPS are required")
	}
	key, err := loadEmbeddedPrivateKey()
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
			cli.finishCapture()
			_, cliTotals := engineTotals(cli.damageStatistics)

			last := map[uint64]intervalTotals{}
			host, _ := equivalenceReplay(t, inputs, masters.masters, key, capture.path, func(p *processor) {
				p.intervalPerProtocolSession = true
			}, func(_ int, p *processor) {
				id, totals := engineTotals(p.damageStatistics)
				last[id] = totals
			})
			host.finishCapture()
			id, totals := engineTotals(host.damageStatistics)
			last[id] = totals
			var sum intervalTotals
			for _, totals := range last {
				sum.add(totals)
			}
			if sum != cliTotals {
				t.Errorf("host intervals do not add up to the CLI interval: host=%+v cli=%+v", sum, cliTotals)
			}
			hostFinal := host.viewSnapshot()
			hostFinal.Final = true

			session, err := OpenSession(SessionConfig{LocalIPv4: inputs.localIP, ReplayPath: capture.path}, masters)
			if err != nil {
				t.Fatal(err)
			}
			var views []ViewSnapshot
			result, err := session.Run(context.Background(), func(v ViewSnapshot) { views = append(views, v) })
			if err != nil || result.StopReason != StopReasonReplayEnd {
				t.Fatalf("session run: %+v, %v", result, err)
			}
			final := views[len(views)-1]
			if !final.Final {
				t.Fatal("the last published view is not final")
			}
			if !reflect.DeepEqual(comparableView(final), comparableView(hostFinal)) {
				a, _ := json.Marshal(comparableView(final))
				b, _ := json.Marshal(comparableView(hostFinal))
				t.Errorf("session final view differs from the processor's view:\nsession=%s\nhost=%s", a, b)
			}
			if host.sessionNumber <= 1 && viewTotals(final) != cliTotals {
				t.Errorf("single protocol session: session totals %+v differ from the CLI %+v", viewTotals(final), cliTotals)
			}
			summary := sessionReplaySummary{
				Name: capture.name, ProtocolSessions: host.sessionNumber, Intervals: len(last), CLI: cliTotals, HostIntervalsSum: sum,
				SessionFinal: viewTotals(final), Snapshots: len(views), FinalIntegrity: string(final.Health.DataIntegrity),
				FinalEncounterDPS: string(final.Statistics.EncounterDPS.Status), Encounters: final.Encounters.CompletedTotal,
			}
			data, _ := json.MarshalIndent(summary, "", "  ")
			if err := os.WriteFile(filepath.Join(out, capture.name+".session.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
