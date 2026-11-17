package capture

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

type CaptureStats struct {
	UDPPackets  int
	KCPPush     int
	KCPControl  int
	NonKCPUDP   int
	InvalidKCP  int
	KCPOrdered  int
	KCPMessages int
	Frames      int
	BadCRC      int
	Malformed   int
	KernelDrops int
	// KCP delivery health. A filled gap is reordering or a captured retransmission; an open gap at the end of a
	// capture means every later segment of that stream was never delivered.
	KCPDuplicates        int // segments already delivered or already pending
	KCPOutOfOrder        int // segments buffered ahead of a missing sequence
	KCPGapsOpened        int
	KCPGapsFilled        int
	KCPMaxGapWait        time.Duration // longest capture-time wait of a filled gap
	KCPPendingPeak       int
	KCPPendingOverflow   int // segments dropped because the stream already held maxPendingKCPSegments
	KCPFragmentResets    int // a fragment that does not continue the message being assembled
	KCPFragmentTailDrops int // a final fragment that arrives before the message has all of its fragments
	// Recovery. transportClock is the newest capture time seen by any stream; gap timeouts run on it.
	transportClock            time.Time
	KCPGapsSkipped            int
	KCPSegmentsSkipped        int
	KCPFragmentsDiscarded     int
	KCPStreamsEvicted         int
	kcpEvictedPendingSegments int
	// Session replacement (retireSupersededStreams): streams of superseded connections, the segments they still held,
	// and later segments of their conversations that were ignored.
	KCPStreamsRetired         int
	KCPRetiredPendingSegments int
	KCPRetiredStragglers      int
	FrameDesyncs              int
	ResyncAttempts            int
	ResyncSuccesses           int
	ResyncFailures            int
	ResyncBytesDiscarded      int
	ResyncCandidatesChecked   int
	ResyncBudgetStops         int
}

func FindDeviceByIP(ip string) (string, error) {
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

func PacketDirection(packet gopacket.Packet, localIPv4 string, udp *layers.UDP, minPort, maxPort int) string {
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

func PromptLocalIPv4(input io.Reader, output io.Writer) (string, error) {
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

func Fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "Error (%s): %v\n", action, err)
	if info, statErr := os.Stdin.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Press Enter to close this window.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(1)
}
