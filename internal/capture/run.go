package capture

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"

	"wwlogs/internal/buildinfo"
)

type segment struct {
	data      []byte
	fragments byte
	timestamp time.Time
}

type stream struct {
	next              uint32
	initialized       bool
	pending           map[uint32]segment
	assembling        [][]byte
	assemblingAt      time.Time
	want              byte
	applicationBuffer []byte
	applicationSpans  []applicationSpan
	// gapOpen is set while a later segment waits for the missing sequence next; gapSince is the capture time of the
	// segment that first revealed the gap. Delivery stays strictly in order: a gap that never fills stalls the stream.
	gapOpen  bool
	gapSince time.Time
}

// maxPendingKCPSegments bounds the out-of-order segments one KCP stream holds while it waits for a missing sequence.
// Contiguous captures never held more than a handful; a stream past the bound is stalled behind an unfilled gap, and
// further segments are counted and dropped instead of growing memory for the rest of the capture.
const maxPendingKCPSegments = 4096

type captureStats struct {
	udpPackets  int
	kcpPush     int
	kcpControl  int
	nonKCPUDP   int
	invalidKCP  int
	kcpOrdered  int
	kcpMessages int
	frames      int
	badCRC      int
	malformed   int
	kernelDrops int
	// KCP delivery health. A filled gap is reordering or a captured retransmission; an open gap at the end of a
	// capture means every later segment of that stream was never delivered.
	kcpDuplicates        int // segments already delivered or already pending
	kcpOutOfOrder        int // segments buffered ahead of a missing sequence
	kcpGapsOpened        int
	kcpGapsFilled        int
	kcpMaxGapWait        time.Duration // longest capture-time wait of a filled gap
	kcpPendingPeak       int
	kcpPendingOverflow   int // segments dropped because the stream already held maxPendingKCPSegments
	kcpFragmentResets    int // a fragment that does not continue the message being assembled
	kcpFragmentTailDrops int // a final fragment that arrives before the message has all of its fragments
}

// kcpSequenceBefore compares KCP sequence numbers with 32-bit wraparound, as KCP itself does.
func kcpSequenceBefore(a, b uint32) bool {
	return int32(a-b) < 0
}

type captureRunStatus struct {
	Build        buildinfo.Record `json:"build"`
	PID          int              `json:"pid"`
	State        string           `json:"state"`
	StopReason   string           `json:"stopReason,omitempty"`
	StartedAt    time.Time        `json:"startedAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	DeadlineAt   time.Time        `json:"deadlineAt,omitempty"`
	Adapter      string           `json:"adapter"`
	LocalIPv4    string           `json:"localIPv4"`
	Filter       string           `json:"filter"`
	PCAPPath     string           `json:"pcapPath"`
	PCAPBytes    int64            `json:"pcapBytes"`
	MaxPCAPBytes int64            `json:"maxPcapBytes"`
	UDPPackets   int              `json:"udpPackets"`
	KernelDrops  int              `json:"kernelDrops"`
}

type liveDiagnosticSnapshot struct {
	Type                             string                   `json:"type"`
	Final                            bool                     `json:"final"`
	SampledAt                        time.Time                `json:"sampledAt"`
	CaptureObservedAt                time.Time                `json:"captureObservedAt,omitempty"`
	ClockDifferenceSeconds           *float64                 `json:"clockDifferenceSeconds,omitempty"`
	LocalSampleIntervalSeconds       *float64                 `json:"localSampleIntervalSeconds,omitempty"`
	CaptureTimestampAdvanceSeconds   *float64                 `json:"captureTimestampAdvanceSeconds,omitempty"`
	CaptureTimeAdvanceInterpretation string                   `json:"captureTimeAdvanceInterpretation"`
	ClockDifferenceInterpretation    string                   `json:"clockDifferenceInterpretation"`
	DamageStatistics                 DamageStatisticsSnapshot `json:"damageStatistics"`
	Effects                          EffectSnapshot           `json:"effects"`
}

const liveDiagnosticInterval = time.Second

type liveCaptureOutputBundle struct {
	directory        string
	pcap             string
	console          string
	combatEvents     string
	buffEvents       string
	buffSnapshots    string
	liveDiagnostics  string
	effectState      string
	status           string
	encounterSummary string
	encounterEvents  string
}

func findDeviceByIP(ip string) (string, error) {
	devices, err := pcap.FindAllDevs()
	if err != nil {
		return "", err
	}
	for _, candidate := range devices {
		for _, address := range candidate.Addresses {
			if address.IP != nil && address.IP.String() == ip {
				return candidate.Name, nil
			}
		}
	}
	var names []string
	for _, candidate := range devices {
		for _, address := range candidate.Addresses {
			if address.IP != nil {
				names = append(names, candidate.Name+"="+address.IP.String())
			}
		}
	}
	return "", fmt.Errorf("no device has IPv4 %s; available: %s", ip, strings.Join(names, ", "))
}

func packetDirection(packet gopacket.Packet, localIPv4 string, udp *layers.UDP, minPort, maxPort int) string {
	if network := packet.Layer(layers.LayerTypeIPv4); network != nil {
		ipv4 := network.(*layers.IPv4)
		if ipv4.SrcIP.String() == localIPv4 {
			return "out"
		}
		if ipv4.DstIP.String() == localIPv4 {
			return "in"
		}
	}
	if int(udp.SrcPort) >= minPort && int(udp.SrcPort) <= maxPort {
		return "in"
	}
	return "out"
}

func consumeDatagram(payload []byte, direction string, capturedAt time.Time, streams map[string]*stream, processor *processor, stats *captureStats) {
	hasKCP := false
	for len(payload) >= 24 {
		dataLen := int(binary.LittleEndian.Uint32(payload[20:24]))
		cmd := payload[4]
		if cmd != 81 && cmd != 82 && cmd != 83 && cmd != 84 {
			if !hasKCP {
				stats.nonKCPUDP++
			} else {
				stats.invalidKCP++
			}
			return
		}
		if dataLen < 0 || 24+dataLen > len(payload) {
			stats.invalidKCP++
			return
		}
		hasKCP = true
		conv := binary.LittleEndian.Uint32(payload[:4])
		fragments := payload[5]
		seq := binary.LittleEndian.Uint32(payload[12:16])
		data := append([]byte(nil), payload[24:24+dataLen]...)
		payload = payload[24+dataLen:]
		if cmd != 81 { // KCP PUSH
			stats.kcpControl++
			continue
		}
		stats.kcpPush++
		key := fmt.Sprintf("%08x/%s", conv, direction)
		state, ok := streams[key]
		if !ok {
			state = &stream{pending: make(map[uint32]segment)}
			streams[key] = state
		}
		if !state.initialized {
			state.initialized = true
			state.next = seq
		}
		if kcpSequenceBefore(seq, state.next) {
			stats.kcpDuplicates++
			continue
		}
		if _, exists := state.pending[seq]; exists {
			stats.kcpDuplicates++
			continue
		}
		if seq != state.next {
			if len(state.pending) >= maxPendingKCPSegments {
				stats.kcpPendingOverflow++
				continue
			}
			stats.kcpOutOfOrder++
			if !state.gapOpen {
				state.gapOpen, state.gapSince = true, capturedAt
				stats.kcpGapsOpened++
			}
		}
		state.pending[seq] = segment{data: data, fragments: fragments, timestamp: capturedAt}
		if len(state.pending) > stats.kcpPendingPeak {
			stats.kcpPendingPeak = len(state.pending)
		}
		if seq == state.next && state.gapOpen {
			// The missing sequence arrived: the gap was reordering or a captured retransmission.
			stats.kcpGapsFilled++
			if wait := capturedAt.Sub(state.gapSince); wait > stats.kcpMaxGapWait {
				stats.kcpMaxGapWait = wait
			}
			state.gapOpen, state.gapSince = false, time.Time{}
		}
		for {
			part, found := state.pending[state.next]
			if !found {
				break
			}
			delete(state.pending, state.next)
			state.next++
			stats.kcpOrdered++
			if part.fragments > 0 {
				switch {
				case len(state.assembling) == 0:
					state.assembling = append(state.assembling, part.data)
					state.assemblingAt = part.timestamp
				case part.fragments == state.want:
					state.assembling = append(state.assembling, part.data)
				default:
					// The message being assembled cannot complete; this fragment can only begin another one.
					stats.kcpFragmentResets++
					state.assembling = [][]byte{part.data}
					state.assemblingAt = part.timestamp
				}
				state.want = part.fragments - 1
				continue
			}
			if len(state.assembling) > 0 && state.want != 0 {
				// A final fragment while earlier fragments are still expected belongs to an incomplete message.
				stats.kcpFragmentTailDrops++
				state.assembling = nil
				state.assemblingAt = time.Time{}
				continue
			}
			app := part.data
			appTimestamp := part.timestamp
			if len(state.assembling) > 0 {
				state.assembling = append(state.assembling, part.data)
				app = make([]byte, 0)
				for _, fragment := range state.assembling {
					app = append(app, fragment...)
				}
				appTimestamp = state.assemblingAt
				state.assembling = nil
				state.assemblingAt = time.Time{}
			}
			stats.kcpMessages++
			processApplicationData(app, appTimestamp, direction, streams, state, processor, stats)
		}
		if len(state.pending) > 0 && !state.gapOpen {
			// Delivery stopped at another missing sequence; it is a new gap, open since its earliest waiting segment.
			state.gapOpen, state.gapSince = true, earliestPendingTimestamp(state.pending)
			stats.kcpGapsOpened++
		}
	}
	if !hasKCP && len(payload) > 0 {
		stats.nonKCPUDP++
	}
}

func earliestPendingTimestamp(pending map[uint32]segment) time.Time {
	var earliest time.Time
	for _, part := range pending {
		if earliest.IsZero() || part.timestamp.Before(earliest) {
			earliest = part.timestamp
		}
	}
	return earliest
}

// kcpTransportHealth summarizes KCP delivery at one moment. OpenGaps counts streams stalled behind a missing
// sequence; their later segments are not delivered, so events after OldestGapSince are absent from every statistic.
type kcpTransportHealth struct {
	Streams         int
	OpenGaps        int
	PendingSegments int
	OldestGapSince  time.Time
}

func summarizeKCPTransport(streams map[string]*stream) kcpTransportHealth {
	var health kcpTransportHealth
	for _, state := range streams {
		health.Streams++
		health.PendingSegments += len(state.pending)
		if !state.gapOpen {
			continue
		}
		health.OpenGaps++
		if health.OldestGapSince.IsZero() || state.gapSince.Before(health.OldestGapSince) {
			health.OldestGapSince = state.gapSince
		}
	}
	return health
}

// transportHealthLine formats KCP delivery counters for development diagnostics. It carries counts and one capture
// time only: no conversation IDs, sequences, or payload bytes.
func transportHealthLine(streams map[string]*stream, stats captureStats) string {
	health := summarizeKCPTransport(streams)
	oldest := "none"
	if !health.OldestGapSince.IsZero() {
		oldest = health.OldestGapSince.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("transport_health kcp_streams=%d kcp_open_gaps=%d kcp_oldest_open_gap=%s kcp_pending_segments=%d kcp_pending_peak=%d kcp_pending_overflow=%d kcp_out_of_order=%d kcp_duplicates=%d kcp_gaps_opened=%d kcp_gaps_filled=%d kcp_max_gap_wait=%s kcp_fragment_resets=%d kcp_fragment_tail_drops=%d",
		health.Streams, health.OpenGaps, oldest, health.PendingSegments, stats.kcpPendingPeak, stats.kcpPendingOverflow,
		stats.kcpOutOfOrder, stats.kcpDuplicates, stats.kcpGapsOpened, stats.kcpGapsFilled, stats.kcpMaxGapWait,
		stats.kcpFragmentResets, stats.kcpFragmentTailDrops)
}

const maxApplicationFrameLength = int(^uint16(0)) + 3

func processApplicationData(data []byte, capturedAt time.Time, direction string, streams map[string]*stream, state *stream, processor *processor, stats *captureStats) {
	if len(data) == 0 {
		return
	}
	state.applicationBuffer = append(state.applicationBuffer, data...)
	if spanCount := len(state.applicationSpans); spanCount > 0 && state.applicationSpans[spanCount-1].timestamp.Equal(capturedAt) {
		state.applicationSpans[spanCount-1].length += len(data)
	} else {
		state.applicationSpans = append(state.applicationSpans, applicationSpan{length: len(data), timestamp: capturedAt})
	}

	previousSession := processor.sessionGeneration
	frameCount, crcErrors, malformed, consumed := parseFrames(state.applicationBuffer, direction, state.applicationSpans, processor)
	stats.frames += frameCount
	stats.badCRC += crcErrors
	stats.malformed += malformed
	if consumed > 0 {
		state.applicationBuffer = append([]byte(nil), state.applicationBuffer[consumed:]...)
		state.applicationSpans = consumeApplicationSpans(state.applicationSpans, consumed)
	}
	if len(state.applicationBuffer) > maxApplicationFrameLength-1 {
		state.applicationBuffer = nil
		state.applicationSpans = nil
		stats.malformed++
	}
	if processor.sessionGeneration != previousSession {
		clearOtherApplicationBuffers(streams, state)
	}
}

func clearOtherApplicationBuffers(streams map[string]*stream, current *stream) {
	for _, state := range streams {
		if state != current {
			state.applicationBuffer = nil
			state.applicationSpans = nil
		}
	}
}

func consumeApplicationSpans(spans []applicationSpan, consumed int) []applicationSpan {
	for consumed > 0 && len(spans) > 0 {
		if consumed >= spans[0].length {
			consumed -= spans[0].length
			spans = spans[1:]
			continue
		}
		spans[0].length -= consumed
		consumed = 0
	}
	return spans
}

func defaultMasterDatabasePath() string {
	return defaultRuntimePath(filepath.Join("data", "master_data.db"))
}

func defaultRuntimePath(relativePath string) string {
	executable, _ := os.Executable()
	workingDirectory, _ := os.Getwd()
	return resolveRuntimePath(relativePath, executable, workingDirectory)
}

func resolveRuntimePath(relativePath, executablePath, workingDirectory string) string {
	var candidates []string
	if executablePath != "" {
		directory := filepath.Dir(executablePath)
		// Search only a few executable ancestors so nested development builds can find repository data.
		for depth := 0; depth < 3; depth++ {
			candidates = append(candidates, filepath.Join(directory, relativePath))
			parentDirectory := filepath.Dir(directory)
			if parentDirectory == directory {
				break
			}
			directory = parentDirectory
		}
	}
	if workingDirectory != "" {
		candidates = append(candidates, filepath.Join(workingDirectory, relativePath))
	} else {
		candidates = append(candidates, relativePath)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return relativePath
}

func promptLocalIPv4(input io.Reader, output io.Writer) (string, error) {
	devices, err := pcap.FindAllDevs()
	if err != nil {
		return "", fmt.Errorf("unable to list Npcap network interfaces: %w", err)
	}
	type adapterAddress struct {
		description string
		ip          string
	}
	var addresses []adapterAddress
	for _, device := range devices {
		for _, address := range device.Addresses {
			if address.IP == nil || address.IP.To4() == nil {
				continue
			}
			addresses = append(addresses, adapterAddress{
				description: device.Description,
				ip:          address.IP.String(),
			})
		}
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("no usable IPv4 network interface found; verify that Npcap is installed or specify an address with -ip")
	}
	fmt.Fprintln(output, "Select the network interface used for game traffic:")
	for index, address := range addresses {
		fmt.Fprintf(output, "  %d. %s (%s)\n", index+1, address.ip, address.description)
	}
	fmt.Fprint(output, "Enter selection: ")
	var choice int
	if _, err := fmt.Fscan(input, &choice); err != nil {
		return "", fmt.Errorf("failed to read interface selection: %w", err)
	}
	if choice < 1 || choice > len(addresses) {
		return "", fmt.Errorf("interface selection is out of range: 1-%d", len(addresses))
	}
	return addresses[choice-1].ip, nil
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "Error (%s): %v\n", action, err)
	if info, statErr := os.Stdin.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Press Enter to close this window.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(1)
}
