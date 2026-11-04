//go:build wwdev

package capture

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// This development-profile, test-only collector removes one KCP PUSH segment at a time from archived captures and
// replays them through the product datagram entry point. For every dropped segment it checks that:
//   - every frame decoded after the drop is a frame of the unmodified replay (no false frame after resynchronization),
//   - no damage is invented: confirmed and unattributed totals never exceed the unmodified replay,
//   - the dropped direction reports exactly one loss that recovered, and nothing is left unrecovered,
//   - an encounter overlapping the loss is not reported INTACT,
//   - one repeated drop replays identically.
//
// It writes one JSON summary per capture. It is skipped unless WWLOGS_SEGMENT_DROP_OUT is set, reads only paths
// supplied through the environment, and needs the embedded key payload (scripts/embed-key.go -test-binary).
//
// Environment:
//
//	WWLOGS_SEGMENT_DROP_OUT        output directory (required to run)
//	WWLOGS_SEGMENT_DROP_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_SEGMENT_DROP_MASTER_DB  Master SQLite database
//	WWLOGS_SEGMENT_DROP_SAMPLES    dropped segments per stream (default 8)

type segmentDropKey struct {
	conv      uint32
	direction string
	sequence  uint32
}

type segmentDropCase struct {
	Direction          string `json:"direction"`
	Sequence           uint32 `json:"sequence"`
	FramesLost         int    `json:"framesLost"`
	MidFrame           bool   `json:"midFrame"`
	ResyncBytes        int    `json:"resyncBytesDiscarded"`
	EstimatedLost      uint64 `json:"estimatedLostFrames"`
	LostFramesUnknown  uint64 `json:"lostFramesUnknown"`
	DamageDealtLost    int64  `json:"damageDealtLost"`
	DowngradedEvents   uint64 `json:"downgradedEvents"`
	TransportState     string `json:"transportState"`
	IntervalIntegrity  string `json:"intervalIntegrity"`
	NonIntactEncounter int    `json:"nonIntactEncounters"`
}

type segmentDropSummary struct {
	Name             string            `json:"name"`
	BaselineFrames   int               `json:"baselineFrames"`
	BaselineDamage   int64             `json:"baselineDamageDealt"`
	BaselineSkips    int               `json:"baselineGapsSkipped"`
	Cases            []segmentDropCase `json:"cases"`
	MaxFramesLost    int               `json:"maxFramesLost"`
	MidFrameCases    int               `json:"midFrameCases"`
	BoundaryCases    int               `json:"boundaryCases"`
	OutboundExactAll bool              `json:"outboundLostFrameEstimateExact"`
}

func TestSegmentDropReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_SEGMENT_DROP_OUT")
	if out == "" {
		t.Skip("WWLOGS_SEGMENT_DROP_OUT is not set")
	}
	master := os.Getenv("WWLOGS_SEGMENT_DROP_MASTER_DB")
	listPath := os.Getenv("WWLOGS_SEGMENT_DROP_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_SEGMENT_DROP_MASTER_DB and WWLOGS_SEGMENT_DROP_PCAPS are required")
	}
	samples := 8
	if value := os.Getenv("WWLOGS_SEGMENT_DROP_SAMPLES"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("invalid WWLOGS_SEGMENT_DROP_SAMPLES %q", value)
		}
		samples = parsed
	}
	pcaps := readSegmentDropList(t, listPath)
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
			summary := runSegmentDrops(t, masters, key, capture, samples)
			data, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, capture.name+".segment-drop.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func readSegmentDropList(t *testing.T, path string) []equivalencePCAP {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var pcaps []equivalencePCAP
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		pcaps = append(pcaps, equivalencePCAP{name: name, path: path})
	}
	if len(pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	return pcaps
}

type segmentDropRun struct {
	frames     []string
	statistics DamageStatisticsSnapshot
	transport  TransportHealthSnapshot
	context    ContextValiditySnapshot
	stats      captureStats
}

func runSegmentDrops(t *testing.T, masters *gameMaster, key *staticRSA, capture equivalencePCAP, samples int) segmentDropSummary {
	t.Helper()
	baseline, pushes := segmentDropReplay(t, masters, key, capture.path, nil)
	summary := segmentDropSummary{
		Name: capture.name, BaselineFrames: len(baseline.frames), BaselineDamage: baseline.statistics.DamageDealt,
		BaselineSkips: baseline.stats.kcpGapsSkipped, OutboundExactAll: true,
	}
	known := make(map[string]int, len(baseline.frames))
	for _, frame := range baseline.frames {
		known[frame]++
	}
	var drops []segmentDropKey
	for _, stream := range sortedSegmentDropStreams(pushes) {
		sequences := pushes[stream]
		if len(sequences) < 20 {
			continue
		}
		for i := 0; i < samples; i++ {
			index := 5 + (len(sequences)-10)*i/samples
			drops = append(drops, segmentDropKey{conv: stream.conv, direction: stream.direction, sequence: sequences[index]})
		}
	}
	for i, drop := range drops {
		run, _ := segmentDropReplay(t, masters, key, capture.path, map[segmentDropKey]bool{drop: true})
		if i == 0 {
			again, _ := segmentDropReplay(t, masters, key, capture.path, map[segmentDropKey]bool{drop: true})
			if !reflect.DeepEqual(run.frames, again.frames) || run.statistics.DamageDealt != again.statistics.DamageDealt {
				t.Errorf("drop %+v does not replay deterministically", drop)
			}
		}
		remaining := make(map[string]int, len(known))
		for frame, count := range known {
			remaining[frame] = count
		}
		for _, frame := range run.frames {
			if remaining[frame] == 0 {
				t.Errorf("drop %+v decoded a frame that the unmodified replay never produced: %s", drop, frame)
				break
			}
			remaining[frame]--
		}
		s, b := run.statistics, baseline.statistics
		if s.DamageDealt > b.DamageDealt || s.DamageTaken > b.DamageTaken || s.OutgoingEvents > b.OutgoingEvents ||
			s.DamageDealt+s.UnattributedOutgoingDamage > b.DamageDealt+b.UnattributedOutgoingDamage {
			t.Errorf("drop %+v invented damage: dealt %d/%d taken %d/%d", drop, s.DamageDealt, b.DamageDealt, s.DamageTaken, b.DamageTaken)
		}
		var health TransportDirectionHealth
		for _, direction := range run.transport.Directions {
			if direction.Direction == drop.direction {
				health = direction
			}
		}
		baselineLosses := uint64(0)
		for _, direction := range baseline.transport.Directions {
			if direction.Direction == drop.direction {
				baselineLosses = direction.Losses
			}
		}
		if health.Losses != baselineLosses+1 || health.Recoveries < health.Losses || health.Unrecovered != 0 {
			t.Errorf("drop %+v transport health = %+v", drop, health)
		}
		nonIntact := 0
		for _, encounter := range s.Encounters.Completed {
			if encounter.Integrity.Status != IntegrityIntact {
				nonIntact++
			}
		}
		if s.Integrity.Status == IntegrityIntact {
			t.Errorf("drop %+v left the interval INTACT", drop)
		}
		lost := len(baseline.frames) - len(run.frames)
		mid := run.stats.resyncBytesDiscarded > baseline.stats.resyncBytesDiscarded
		entry := segmentDropCase{
			Direction: drop.direction, Sequence: drop.sequence, FramesLost: lost, MidFrame: mid,
			ResyncBytes: run.stats.resyncBytesDiscarded - baseline.stats.resyncBytesDiscarded, EstimatedLost: health.EstimatedLostFrames,
			LostFramesUnknown: health.LostFramesUnknown, DamageDealtLost: b.DamageDealt - s.DamageDealt,
			DowngradedEvents: run.context.DowngradedEvents, TransportState: string(run.transport.State),
			IntervalIntegrity: string(s.Integrity.Status), NonIntactEncounter: nonIntact,
		}
		if drop.direction == "out" && baselineLosses == 0 && (health.LostFramesUnknown != 0 || int(health.EstimatedLostFrames) != lost) {
			summary.OutboundExactAll = false
		}
		if lost > summary.MaxFramesLost {
			summary.MaxFramesLost = lost
		}
		if mid {
			summary.MidFrameCases++
		} else {
			summary.BoundaryCases++
		}
		summary.Cases = append(summary.Cases, entry)
	}
	return summary
}

type segmentDropStream struct {
	conv      uint32
	direction string
}

func sortedSegmentDropStreams(pushes map[segmentDropStream][]uint32) []segmentDropStream {
	streams := make([]segmentDropStream, 0, len(pushes))
	for stream := range pushes {
		streams = append(streams, stream)
	}
	sort.Slice(streams, func(i, j int) bool {
		if streams[i].conv != streams[j].conv {
			return streams[i].conv < streams[j].conv
		}
		return streams[i].direction < streams[j].direction
	})
	return streams
}

// segmentDropReplay replays a capture, removing the KCP PUSH segments in drop. It returns the decoded frames as
// identity strings, the final snapshots, and the distinct PUSH sequences seen per stream.
func segmentDropReplay(t *testing.T, masters *gameMaster, key *staticRSA, path string, drop map[segmentDropKey]bool) (segmentDropRun, map[segmentDropStream][]uint32) {
	t.Helper()
	var archive bytes.Buffer
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters
	p.frameArchive = &archive
	handle, err := pcap.OpenOffline(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer handle.Close()
	if err := handle.SetBPFFilter(equivalenceFilter); err != nil {
		t.Fatal(err)
	}
	source := gopacket.NewPacketSource(handle, handle.LinkType())
	streams := make(map[string]*stream)
	stats := captureStats{}
	seen := map[segmentDropStream]map[uint32]bool{}
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
		stats.udpPackets++
		direction := packetDirection(packet, "192.168.1.2", udp, 13100, 13120)
		payload := filterKCPSegments(udp.Payload, direction, drop, seen)
		consumeDatagram(payload, direction, packet.Metadata().CaptureInfo.Timestamp, streams, p, &stats)
	}
	finishTransport(streams, p, &stats)
	p.finishCapture()
	var frames []string
	for _, line := range bytes.Split(archive.Bytes(), []byte{'\n'}) {
		var record frameArchiveRecord
		if len(line) == 0 || json.Unmarshal(line, &record) != nil {
			continue
		}
		frames = append(frames, fmt.Sprintf("%s/%d/%d/%d/%s/%s", record.Direction, record.SessionNumber, record.Sequence, record.CommandID, record.BodySHA256, record.Timestamp))
	}
	statistics := p.damageStatisticsSnapshot()
	pushes := map[segmentDropStream][]uint32{}
	for stream, sequences := range seen {
		for sequence := range sequences {
			pushes[stream] = append(pushes[stream], sequence)
		}
		sort.Slice(pushes[stream], func(i, j int) bool { return pushes[stream][i] < pushes[stream][j] })
	}
	return segmentDropRun{frames: frames, statistics: statistics, transport: p.transportHealthSnapshot(), context: p.context.snapshot(), stats: stats}, pushes
}

// filterKCPSegments returns payload without the PUSH segments listed in drop and records the PUSH sequences seen.
func filterKCPSegments(payload []byte, direction string, drop map[segmentDropKey]bool, seen map[segmentDropStream]map[uint32]bool) []byte {
	var kept []byte
	rest := payload
	for len(rest) >= 24 {
		length := int(binary.LittleEndian.Uint32(rest[20:24]))
		if 24+length > len(rest) || (rest[4] != 81 && rest[4] != 82 && rest[4] != 83 && rest[4] != 84) {
			return append(kept, rest...)
		}
		segment := rest[:24+length]
		rest = rest[24+length:]
		if segment[4] == 81 {
			conv, sequence := binary.LittleEndian.Uint32(segment[:4]), binary.LittleEndian.Uint32(segment[12:16])
			stream := segmentDropStream{conv: conv, direction: direction}
			if seen[stream] == nil {
				seen[stream] = map[uint32]bool{}
			}
			seen[stream][sequence] = true
			if drop[segmentDropKey{conv: conv, direction: direction, sequence: sequence}] {
				continue
			}
		}
		kept = append(kept, segment...)
	}
	return append(kept, rest...)
}
