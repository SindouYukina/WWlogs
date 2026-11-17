//go:build !wwdev

package capture

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// publicReportInterval spaces the live console statistics lines.
const publicReportInterval = 5 * time.Second

// Main runs the public profile: live capture or offline replay that reports aggregate statistics on
// the console and writes nothing to disk. Raw diagnostic outputs exist only in development builds.
func Main() {
	ExitIfVersionRequested()
	ip := flag.String("ip", "", "Local network interface IPv4 address; if omitted, list available interfaces for selection")
	minPort := flag.Int("min-port", 13100, "Lower bound of the UDP server port range")
	maxPort := flag.Int("max-port", 13120, "Upper bound of the UDP server port range")
	duration := flag.Duration("duration", 0, "Live capture duration; 0 runs until Ctrl+C is pressed")
	pcapIn := flag.String("pcap-in", "", "Replay the specified PCAP file without live capture")
	masterLocale := flag.String("locale", "zh-TW", "Locale for Master display names: zh-TW, zh-CN, en-US, ja-JP, or ko-KR")
	filter := flag.String("filter", "", "Optionally specify a BPF filter; defaults to the configured UDP port range")
	stopFile := flag.String("stop-file", "", "Stop live capture cleanly when the specified file appears")
	preflight := flag.Bool("preflight", false, "Check whether the specified Npcap interface can be opened and filtered")
	flag.Bool("version", false, "Print the WWlogs version and build identity")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "WWlogs\nUsage: WWlogs [options]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	fmt.Println("WWlogs")
	if *ip == "" && *pcapIn == "" {
		selectedIP, err := PromptLocalIPv4(os.Stdin, os.Stdout)
		if err != nil {
			Fatal("select network interface", err)
		}
		*ip = selectedIP
	}
	if *minPort < 1 || *maxPort > 65535 || *minPort > *maxPort {
		Fatal("port range", fmt.Errorf("invalid range %d-%d", *minPort, *maxPort))
	}
	if *stopFile != "" && *pcapIn != "" {
		Fatal("live capture controls", fmt.Errorf("-stop-file requires live capture"))
	}
	filterExpression := *filter
	if filterExpression == "" {
		filterExpression = fmt.Sprintf("udp and portrange %d-%d", *minPort, *maxPort)
	}
	masters, err := LoadGameMasterDatabase("", *masterLocale)
	if err != nil {
		Fatal("load combat master SQLite", err)
	}
	if *preflight {
		device, err := FindDeviceByIP(*ip)
		if err != nil {
			Fatal("find Npcap adapter", err)
		}
		handle, applied, err := OpenLiveCapture(device, DefaultLiveCaptureConfig())
		if err != nil {
			Fatal("open Npcap adapter", err)
		}
		defer handle.Close()
		if err := handle.SetBPFFilter(filterExpression); err != nil {
			Fatal("set capture filter", err)
		}
		fmt.Fprintf(os.Stderr, "capture_preflight=PASS adapter=%s local_ipv4=%s filter=%q %s\n", device, *ip, filterExpression, applied)
		return
	}
	rsaKey, err := LoadEmbeddedPrivateKey()
	if err != nil {
		Fatal("load embedded RSA key", err)
	}
	stdout := bufio.NewWriter(os.Stdout)
	defer stdout.Flush()
	// The processor's own console and audit sinks are discarded: per-event lines are a development feature.
	processor := NewProcessor(rsaKey, io.Discard, os.Stderr, nil)
	processor.Masters = masters

	var handle *pcap.Handle
	if *pcapIn != "" {
		handle, err = pcap.OpenOffline(*pcapIn)
		if err != nil {
			Fatal("open offline PCAP", err)
		}
	} else {
		device, err := FindDeviceByIP(*ip)
		if err != nil {
			Fatal("find Npcap adapter", err)
		}
		var applied LiveCaptureApplied
		handle, applied, err = OpenLiveCapture(device, DefaultLiveCaptureConfig())
		if err != nil {
			Fatal("open Npcap adapter", err)
		}
		fmt.Fprintf(os.Stderr, "capture_handle %s\n", applied)
	}
	defer handle.Close()
	if err := handle.SetBPFFilter(filterExpression); err != nil {
		Fatal("set capture filter", err)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	processor.ObserveCaptureSource(*pcapIn == "")
	streams := make(map[string]*Stream)
	stats := CaptureStats{}
	started := time.Now()
	stop := make(chan os.Signal, 1)
	if *pcapIn == "" {
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(stop)
	}
	lastReportAt := time.Time{}
	lastReported := ""
	report := func(now time.Time, final bool) {
		if !final && (*pcapIn != "" || (!lastReportAt.IsZero() && now.Sub(lastReportAt) < publicReportInterval)) {
			return
		}
		line := publicStatisticsLine(processor)
		if !final && line == lastReported {
			return
		}
		lastReportAt, lastReported = now, line
		label := "statistics"
		if final {
			label = "statistics_final"
		}
		fmt.Fprintf(stdout, "%s %s\n", label, line)
		_ = stdout.Flush()
	}
	stopReason := "deadline"
loop:
	for *pcapIn != "" || *duration == 0 || time.Since(started) < *duration {
		if *pcapIn == "" {
			if *stopFile != "" {
				if _, err := os.Stat(*stopFile); err == nil {
					stopReason = "requested"
					break loop
				}
			}
			select {
			case <-stop:
				stopReason = "signal"
				break loop
			default:
			}
		}
		packet, packetErr := packetSource.NextPacket()
		if packetErr != nil {
			if packetErr == pcap.NextErrorTimeoutExpired {
				processor.SampleCaptureStats(handle, time.Now(), false)
				report(time.Now(), false)
				continue
			}
			if *pcapIn != "" && packetErr == io.EOF {
				break
			}
			if *pcapIn == "" {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			fmt.Fprintf(os.Stderr, "offline packet read warning: %v\n", packetErr)
			continue
		}
		receivedAt := time.Now()
		processor.ObservePacketRead()
		processor.SampleCaptureStats(handle, receivedAt, false)
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		stats.UDPPackets++
		direction := PacketDirection(packet, *ip, udp, *minPort, *maxPort)
		capturedAt := packet.Metadata().CaptureInfo.Timestamp
		ConsumeDatagram(udp.Payload, direction, capturedAt, streams, processor, &stats)
		processor.ObservePacketTiming(capturedAt, receivedAt, time.Now())
		report(time.Now(), false)
	}
	FinishTransport(streams, processor, &stats)
	processor.SampleCaptureStats(handle, time.Now(), true)
	processor.FinishCapture()
	report(time.Now(), true)
	fmt.Fprintf(os.Stderr, "capture_finished reason=%s udp=%d duration=%s\n", stopReason, stats.UDPPackets, time.Since(started).Round(time.Millisecond))
}

// publicStatisticsLine formats confirmed aggregates only: no Entity, Handle, or RPC identifiers.
func publicStatisticsLine(p *Processor) string {
	return p.CurrentStatisticsReport().String()
}
