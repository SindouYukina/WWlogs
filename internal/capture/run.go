package capture

import (
	"bufio"
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
	// Recovery. transportClock is the newest capture time seen by any stream; gap timeouts run on it.
	transportClock            time.Time
	kcpGapsSkipped            int
	kcpSegmentsSkipped        int
	kcpFragmentsDiscarded     int
	kcpStreamsEvicted         int
	kcpEvictedPendingSegments int
	frameDesyncs              int
	resyncAttempts            int
	resyncSuccesses           int
	resyncFailures            int
	resyncBytesDiscarded      int
	resyncCandidatesChecked   int
	resyncBudgetStops         int
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
	// DiagnosticDroppedRecords counts development diagnostic records dropped because a sink queue was full or its
	// writer failed. It is unrelated to packet loss.
	DiagnosticDroppedRecords uint64 `json:"diagnosticDroppedRecords"`
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
