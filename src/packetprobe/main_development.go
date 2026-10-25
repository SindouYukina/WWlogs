//go:build wwdev

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/google/gopacket/pcapgo"
)

func main() {
	exitIfVersionRequested()
	ip := flag.String("ip", "", "local adapter IPv4 address (required for live capture)")
	minPort := flag.Int("min-port", 13100, "minimum UDP server port")
	maxPort := flag.Int("max-port", 13120, "maximum UDP server port")
	duration := flag.Duration("duration", 0, "live capture duration; 0 runs until Ctrl+C")
	pcapIn := flag.String("pcap-in", "", "replay a saved PCAP instead of live capture")
	pcapOut := flag.String("pcap-out", "", "optionally save captured UDP packets to a PCAP file")
	verifyBaseline := flag.Bool("verify-baseline", false, "assert the corrected v0.1.1 locking PCAP regression counts")
	auditOut := flag.String("audit-out", "", "optionally write matched non-secret RPC/entity/damage rows as TSV")
	flag.Parse()

	if *minPort < 1 || *maxPort > 65535 || *minPort > *maxPort {
		fatal("port range", fmt.Errorf("invalid range %d-%d", *minPort, *maxPort))
	}
	if *pcapIn != "" && *pcapOut != "" {
		fatal("PCAP mode", fmt.Errorf("-pcap-in and -pcap-out cannot be combined"))
	}
	if *verifyBaseline && *pcapIn == "" {
		fatal("baseline mode", fmt.Errorf("-verify-baseline requires -pcap-in"))
	}
	rsaKey, err := loadEmbeddedPrivateKey()
	if err != nil {
		fatal("load embedded RSA key", err)
	}
	stdout := bufio.NewWriter(os.Stdout)
	var auditFile *os.File
	var auditWriter io.Writer
	if *auditOut != "" {
		auditFile, err = os.Create(*auditOut)
		if err != nil {
			fatal("create event audit", err)
		}
		defer auditFile.Close()
		if _, err := fmt.Fprintln(auditFile, "rpc_id\tattacker_entity_id\ttarget_entity_id\tskill_level\tskill_id\tskill_message_id\tdamage_id\tdamage"); err != nil {
			fatal("write event audit header", err)
		}
		auditWriter = auditFile
	}
	processor := newProcessor(rsaKey, stdout, os.Stderr, auditWriter)

	var device string
	var handle *pcap.Handle
	if *pcapIn != "" {
		handle, err = pcap.OpenOffline(*pcapIn)
		if err != nil {
			fatal("open offline PCAP", err)
		}
		device = "offline:" + *pcapIn
	} else {
		device, err = findDeviceByIP(*ip)
		if err != nil {
			fatal("find Npcap adapter", err)
		}
		handle, err = pcap.OpenLive(device, 65535, false, time.Second)
		if err != nil {
			fatal("open Npcap adapter", err)
		}
	}
	defer handle.Close()

	filter := fmt.Sprintf("udp and portrange %d-%d", *minPort, *maxPort)
	if err := handle.SetBPFFilter(filter); err != nil {
		fatal("set capture filter", err)
	}

	var pcapFile *os.File
	var pcapWriter *pcapgo.Writer
	if *pcapOut != "" {
		if dir := filepath.Dir(*pcapOut); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fatal("create PCAP directory", err)
			}
		}
		pcapFile, err = os.Create(*pcapOut)
		if err != nil {
			fatal("create PCAP", err)
		}
		defer pcapFile.Close()
		pcapWriter = pcapgo.NewWriter(pcapFile)
		if err := pcapWriter.WriteFileHeader(65535, handle.LinkType()); err != nil {
			fatal("write PCAP header", err)
		}
	}

	if *pcapIn == "" {
		fmt.Fprintf(os.Stderr, "WWLOGS live capture started adapter=%s filter=%q; login and fight normally; Ctrl+C stops capture\n", device, filter)
	} else {
		fmt.Fprintf(os.Stderr, "WWLOGS offline replay started file=%s filter=%q\n", *pcapIn, filter)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	streams := make(map[string]*stream)
	stats := captureStats{}
	started := time.Now()
	stop := make(chan os.Signal, 1)
	if *pcapIn == "" {
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(stop)
	}
	for *pcapIn != "" || *duration == 0 || time.Since(started) < *duration {
		if *pcapIn == "" {
			select {
			case <-stop:
				goto finished
			default:
			}
		}
		packet, packetErr := packetSource.NextPacket()
		if packetErr != nil {
			if packetErr == pcap.NextErrorTimeoutExpired {
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
		if pcapWriter != nil {
			if err := pcapWriter.WritePacket(packet.Metadata().CaptureInfo, packet.Data()); err != nil {
				fatal("write captured PCAP packet", err)
			}
		}
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		stats.udpPackets++
		direction := "out"
		if int(udp.SrcPort) >= *minPort && int(udp.SrcPort) <= *maxPort {
			direction = "in"
		}
		consumeDatagram(udp.Payload, direction, packet.Metadata().CaptureInfo.Timestamp, streams, processor, &stats)
	}

finished:
	if statsResult, statsErr := handle.Stats(); statsErr == nil && statsResult != nil {
		stats.kernelDrops = statsResult.PacketsDropped
	}
	if err := stdout.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "stdout flush warning: %v\n", err)
	}
	processor.printSummary(os.Stderr)
	fmt.Fprintf(os.Stderr, "capture_finished udp=%d kcp_push=%d kcp_ordered=%d kcp_messages=%d protocol_frames=%d bad_crc=%d malformed_frames=%d kernel_drops=%d duration=%s\n",
		stats.udpPackets, stats.kcpPush, stats.kcpOrdered, stats.kcpMessages, stats.frames, stats.badCRC, stats.malformed, stats.kernelDrops, time.Since(started).Round(time.Millisecond))
	if *verifyBaseline {
		if err := processor.verifyKnownBaseline(); err != nil {
			fatal("baseline verification", err)
		}
		fmt.Fprintln(os.Stderr, "baseline_verification=PASS")
	}
}
