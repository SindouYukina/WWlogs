//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// This development-profile, test-only collector replays archived captures with controlled, in-memory perturbations of
// the datagram stream and checks the product's invariants after each one. The archived files are only read; every
// perturbed input is derived from the original packet list in memory and described by its case name.
//
// Cases:
//   - duplicate: every UDP datagram is delivered twice with the same timestamp (network duplication or a capture
//     that saw a packet twice). Damage, events, encounters, roles, skills and timelines must equal the unmodified
//     replay exactly, and no response may be applied twice.
//   - swap_same_direction: every second pair of adjacent datagrams of one direction is delivered in reverse order, each
//     keeping its own timestamp (UDP reordering). Confirmed totals, events and role and skill splits must equal the
//     unmodified replay.
//   - swap_cross_direction: the same for pairs of opposite directions. It reverses two observations the capture made at
//     different times, so attribution may change; confirmed local damage (attributed plus unattributed) may not.
//   - truncate_N: the capture stops after N% of its datagrams (capture stopped mid-battle). Nothing may exceed the
//     unmodified replay, and an encounter still open at the cut must not be reported as confirmed-ended.
//   - start_N: the capture starts at N% (capture started mid-session). Without the session handshake nothing can be
//     decrypted, so no damage, event or effect may be reported.
//
// Every case also checks that roles, skills and the unassigned bucket reconcile with the totals, that every retained
// encounter's timeline adds up to it, and that every published view accounts for each tracked effect exactly once.
//
// Environment:
//
//	WWLOGS_PERTURBATION_OUT        output directory (required to run)
//	WWLOGS_PERTURBATION_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_PERTURBATION_MASTER_DB  Master SQLite database

type perturbationPacket struct {
	payload   []byte
	direction string
	at        time.Time
}

type perturbationResult struct {
	Case              string `json:"case"`
	Datagrams         int    `json:"datagrams"`
	DamageDealt       int64  `json:"damageDealt"`
	DamageTaken       int64  `json:"damageTaken"`
	OutgoingEvents    uint64 `json:"outgoingEvents"`
	IncomingEvents    uint64 `json:"incomingEvents"`
	DuplicateEvents   uint64 `json:"duplicateEvents"`
	Encounters        int    `json:"encounters"`
	ConfirmedEnds     int    `json:"confirmedEnds"`
	OpenAtEnd         bool   `json:"openAtEnd"`
	Integrity         string `json:"integrity"`
	TransportState    string `json:"transportState"`
	KCPDuplicates     int    `json:"kcpDuplicateSegments"`
	GapsSkipped       int    `json:"kcpGapsSkipped"`
	Handshakes        int    `json:"handshakes"`
	EffectsTracked    int    `json:"effectsTracked"`
	EffectsEligible   int    `json:"effectsEligible"`
	ViewsChecked      int    `json:"viewsChecked"`
	MatchesBaseline   *bool  `json:"matchesBaseline,omitempty"`
	BaselineDifferent string `json:"baselineDifference,omitempty"`
}

func TestTransportPerturbationCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_PERTURBATION_OUT")
	if out == "" {
		t.Skip("WWLOGS_PERTURBATION_OUT is not set")
	}
	master := os.Getenv("WWLOGS_PERTURBATION_MASTER_DB")
	listPath := os.Getenv("WWLOGS_PERTURBATION_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_PERTURBATION_MASTER_DB and WWLOGS_PERTURBATION_PCAPS are required")
	}
	pcaps := readReplayPCAPList(t, listPath)
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := loadGameMasterDatabase(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, capture := range pcaps {
		t.Run(capture.name, func(t *testing.T) {
			packets := readPerturbationPackets(t, capture.path)
			results := runPerturbations(t, masters, key, packets)
			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, capture.name+".perturbation.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func readPerturbationPackets(t *testing.T, path string) []perturbationPacket {
	t.Helper()
	handle, err := pcap.OpenOffline(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer handle.Close()
	if err := handle.SetBPFFilter(equivalenceFilter); err != nil {
		t.Fatal(err)
	}
	source := gopacket.NewPacketSource(handle, handle.LinkType())
	var packets []perturbationPacket
	for {
		packet, err := source.NextPacket()
		if err != nil {
			break
		}
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		packets = append(packets, perturbationPacket{
			payload:   append([]byte(nil), udp.Payload...),
			direction: packetDirection(packet, "192.168.1.2", udp, 13100, 13120),
			at:        packet.Metadata().CaptureInfo.Timestamp,
		})
	}
	return packets
}

type perturbationRun struct {
	p          *processor
	stats      captureStats
	statistics DamageStatisticsSnapshot
	timelines  []EncounterTimelineSnapshot
	views      int
}

// replayPerturbation feeds packets through the product datagram entry point and publishes a view every 37 datagrams,
// as the host does, checking the effect count partition each time.
func replayPerturbation(t *testing.T, masters *gameMaster, key *staticRSA, packets []perturbationPacket) perturbationRun {
	t.Helper()
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters
	streams := make(map[string]*stream)
	stats := captureStats{}
	run := perturbationRun{p: p}
	check := func() {
		view := p.viewSnapshot()
		if err := view.Effects.Counts.accountingError(len(view.Effects.Active)); err != nil {
			t.Fatalf("effect view counts after %d datagrams: %v", stats.udpPackets, err)
		}
		run.views++
	}
	for i, packet := range packets {
		stats.udpPackets++
		consumeDatagram(packet.payload, packet.direction, packet.at, streams, p, &stats)
		if i%37 == 0 {
			check()
		}
	}
	finishTransport(streams, p, &stats)
	p.finishCapture()
	check()
	run.stats = stats
	// Capture-session numbers are process-local; normalize them so that runs compare.
	normalizer := newEquivalenceNormalizer(p)
	run.statistics = normalizer.damage(p.damageStatisticsSnapshot())
	normalizeEncounterIDs(&run.statistics, normalizer.base)
	run.timelines = p.damageStatistics.encounterTimelines()
	for i := range run.timelines {
		if _, rest, found := strings.Cut(run.timelines[i].EncounterID, "-"); found {
			run.timelines[i].EncounterID = "1-" + rest
		}
	}
	return run
}

func perturbationSummary(name string, datagrams int, run perturbationRun) perturbationResult {
	s := run.statistics
	result := perturbationResult{
		Case: name, Datagrams: datagrams, DamageDealt: s.DamageDealt, DamageTaken: s.DamageTaken,
		OutgoingEvents: s.OutgoingEvents, IncomingEvents: s.IncomingEvents, DuplicateEvents: s.DuplicateEvents,
		Encounters: len(s.Encounters.Completed), OpenAtEnd: s.Encounters.Current != nil, Integrity: string(s.Integrity.Status),
		TransportState: string(run.p.transportHealthSnapshot().State), KCPDuplicates: run.stats.kcpDuplicates,
		GapsSkipped: run.stats.kcpGapsSkipped, Handshakes: run.p.sessionNumber, ViewsChecked: run.views,
	}
	for _, encounter := range s.Encounters.Completed {
		if encounter.Outcome == EncounterOutcomeConfirmed {
			result.ConfirmedEnds++
		}
	}
	view := run.p.viewSnapshot()
	result.EffectsTracked, result.EffectsEligible = view.Effects.Counts.Tracked, view.Effects.Counts.Eligible
	return result
}

// assertPerturbationInvariants checks what must hold for any input: the splits reconcile and every timeline adds up.
func assertPerturbationInvariants(t *testing.T, name string, run perturbationRun) {
	t.Helper()
	assertRoleTotalsReconcile(t, run.statistics)
	assertEncounterTotalsConserved(t, run.statistics)
	encounters := append([]EncounterSnapshot{}, run.statistics.Encounters.Completed...)
	if run.statistics.Encounters.Current != nil {
		encounters = append(encounters, *run.statistics.Encounters.Current)
	}
	if len(encounters) != len(run.timelines) {
		t.Fatalf("%s: %d encounters but %d timelines", name, len(encounters), len(run.timelines))
	}
	for i, encounter := range encounters {
		if err := timelineConservationError(run.timelines[i], encounter); err != nil {
			t.Errorf("%s: encounter %s: %v", name, encounter.EncounterID, err)
		}
	}
}

// perturbationTotals is the part of the statistics that must not change when only delivery changes.
type perturbationTotals struct {
	DamageDealt, DamageTaken, Unattributed int64
	Outgoing, Incoming, Duplicates         uint64
	Roles                                  string
	Vehicle                                string
	Unassigned                             string
}

func totalsOf(t *testing.T, s DamageStatisticsSnapshot) perturbationTotals {
	t.Helper()
	encode := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	return perturbationTotals{
		DamageDealt: s.DamageDealt, DamageTaken: s.DamageTaken, Unattributed: s.UnattributedOutgoingDamage,
		Outgoing: s.OutgoingEvents, Incoming: s.IncomingEvents, Duplicates: s.DuplicateEvents,
		Roles: encode(s.Roles), Vehicle: encode(s.Vehicle), Unassigned: encode(s.UnassignedRole),
	}
}

func runPerturbations(t *testing.T, masters *gameMaster, key *staticRSA, packets []perturbationPacket) []perturbationResult {
	t.Helper()
	baseline := replayPerturbation(t, masters, key, packets)
	assertPerturbationInvariants(t, "baseline", baseline)
	results := []perturbationResult{perturbationSummary("baseline", len(packets), baseline)}
	baseTotals := totalsOf(t, baseline.statistics)
	same := func(result *perturbationResult, equal bool, difference string) {
		result.MatchesBaseline = &equal
		if !equal {
			result.BaselineDifferent = difference
		}
	}

	// duplicate: identical delivery twice must change nothing that is counted.
	duplicated := make([]perturbationPacket, 0, 2*len(packets))
	for _, packet := range packets {
		duplicated = append(duplicated, packet, packet)
	}
	run := replayPerturbation(t, masters, key, duplicated)
	assertPerturbationInvariants(t, "duplicate", run)
	result := perturbationSummary("duplicate", len(duplicated), run)
	statisticsEqual := encodeEqual(t, run.statistics, baseline.statistics) && encodeEqual(t, run.timelines, baseline.timelines)
	same(&result, statisticsEqual, "statistics or timelines differ from the unmodified replay")
	if !statisticsEqual {
		t.Errorf("duplicate delivery changed the statistics or timelines")
	}
	results = append(results, result)

	// swap: reversing adjacent datagrams of one direction (UDP reordering, which KCP reassembles) must not change
	// confirmed totals or splits.
	swapped := append([]perturbationPacket(nil), packets...)
	swaps := 0
	for i := 0; i+1 < len(swapped); i += 4 {
		if swapped[i].direction == swapped[i+1].direction {
			swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
			swaps++
		}
	}
	run = replayPerturbation(t, masters, key, swapped)
	assertPerturbationInvariants(t, "swap_same_direction", run)
	result = perturbationSummary(fmt.Sprintf("swap_same_direction(%d)", swaps), len(swapped), run)
	swapTotals := totalsOf(t, run.statistics)
	same(&result, swapTotals == baseTotals, fmt.Sprintf("totals %+v", swapTotals))
	if swapTotals != baseTotals {
		t.Errorf("same-direction reordering changed confirmed totals: %+v vs %+v", swapTotals, baseTotals)
	}
	results = append(results, result)

	// swap_cross_direction: reversing an outbound and an inbound datagram reverses the order of two events the capture
	// saw at different times. This is not a transport fault the product can detect; the case records how attribution
	// depends on that order and checks only that confirmed local damage is neither lost nor invented.
	crossed := append([]perturbationPacket(nil), packets...)
	swaps = 0
	for i := 0; i+1 < len(crossed); i += 4 {
		if crossed[i].direction != crossed[i+1].direction {
			crossed[i], crossed[i+1] = crossed[i+1], crossed[i]
			swaps++
		}
	}
	run = replayPerturbation(t, masters, key, crossed)
	assertPerturbationInvariants(t, "swap_cross_direction", run)
	result = perturbationSummary(fmt.Sprintf("swap_cross_direction(%d)", swaps), len(crossed), run)
	crossTotals := totalsOf(t, run.statistics)
	same(&result, crossTotals == baseTotals, fmt.Sprintf("dealt %d unattributed %d outgoing %d", crossTotals.DamageDealt, crossTotals.Unattributed, crossTotals.Outgoing))
	if crossTotals.DamageDealt+crossTotals.Unattributed != baseTotals.DamageDealt+baseTotals.Unattributed || crossTotals.DamageTaken != baseTotals.DamageTaken {
		t.Errorf("cross-direction reordering lost or invented confirmed damage: %+v vs %+v", crossTotals, baseTotals)
	}
	results = append(results, result)

	for _, percent := range []int{30, 60, 90} {
		cut := len(packets) * percent / 100
		name := fmt.Sprintf("truncate_%d", percent)
		run = replayPerturbation(t, masters, key, packets[:cut])
		assertPerturbationInvariants(t, name, run)
		result = perturbationSummary(name, cut, run)
		s, b := run.statistics, baseline.statistics
		if s.DamageDealt > b.DamageDealt || s.DamageTaken > b.DamageTaken || s.OutgoingEvents > b.OutgoingEvents || s.IncomingEvents > b.IncomingEvents {
			t.Errorf("%s reports more than the whole capture: dealt %d/%d taken %d/%d", name, s.DamageDealt, b.DamageDealt, s.DamageTaken, b.DamageTaken)
		}
		// The cut is not a battle exit: an encounter confirmed-ended in the truncated replay must end before the cut.
		if cut > 0 {
			last := packets[cut-1].at
			for _, encounter := range s.Encounters.Completed {
				if encounter.Outcome == EncounterOutcomeConfirmed && encounter.EndedAt.After(last) {
					t.Errorf("%s confirmed encounter %s ends after the cut", name, encounter.EncounterID)
				}
			}
		}
		results = append(results, result)
	}

	for _, percent := range []int{25, 50} {
		start := len(packets) * percent / 100
		name := fmt.Sprintf("start_%d", percent)
		run = replayPerturbation(t, masters, key, packets[start:])
		assertPerturbationInvariants(t, name, run)
		result = perturbationSummary(name, len(packets)-start, run)
		// Data after a handshake inside the remaining datagrams is legitimately decodable; without one nothing is.
		if run.p.sessionNumber == 0 && (run.statistics.OutgoingEvents != 0 || run.statistics.IncomingEvents != 0 || result.EffectsTracked != 0) {
			t.Errorf("%s decoded combat data without a session handshake: %+v", name, result)
		}
		if run.statistics.DamageDealt > baseline.statistics.DamageDealt {
			t.Errorf("%s reports more damage than the whole capture", name)
		}
		results = append(results, result)
	}
	return results
}

func encodeEqual(t *testing.T, a, b any) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(left) == string(right)
}
