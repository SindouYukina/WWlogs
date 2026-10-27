//go:build wwdev

package main

import (
	"bufio"
	"encoding/json"
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

	"wwlogs/internal/buildinfo"
)

func optionalFileWriter(file *os.File) io.Writer {
	if file == nil {
		return nil
	}
	return file
}

func main() {
	exitIfVersionRequested()
	ip := flag.String("ip", "", "local adapter IPv4 address (required for live capture)")
	minPort := flag.Int("min-port", 13100, "minimum UDP server port")
	maxPort := flag.Int("max-port", 13120, "maximum UDP server port")
	duration := flag.Duration("duration", 0, "live capture duration; 0 runs until Ctrl+C")
	pcapIn := flag.String("pcap-in", "", "replay a saved PCAP instead of live capture")
	pcapOut := flag.String("pcap-out", "", "optionally save captured UDP packets to a PCAP file")
	masterDir := flag.String("master-dir", filepath.Join("data", "global", "3.7.0", "zh-TW"), "local versioned Global master data directory")
	verifyBaseline := flag.Bool("verify-baseline", false, "assert the corrected Locking PCAP regression counts")
	auditOut := flag.String("audit-out", "", "optionally write matched non-secret RPC/entity/damage rows as TSV")
	buffSnapshotOut := flag.String("buff-snapshot-out", "", "optionally write EntityActiveResponse Buff snapshots as local JSONL")
	buffEventOut := flag.String("buff-event-out", "", "optionally write supported Buff lifecycle observations as local JSONL")
	combatNotifyAuditOut := flag.String("combat-notify-audit-out", "", "offline only: write sanitized CombatNotifyData field audit JSONL")
	encounterSummaryOut := flag.String("encounter-summary-out", "", "optionally write evidence-labeled encounter and character summaries as Markdown")
	combatEventsJSONLOut := flag.String("event-jsonl-out", "", "optionally write matched combat and Buff observations with evidence as JSONL")
	research := flag.Bool("research", false, "write per-event evidence details to stderr")
	commandCensusOut := flag.String("command-census-out", "", "write a local JSON census of protocol commands and decode outcomes")
	clientVersionOut := flag.String("client-version-out", "", "write version-only client metadata observed after the first handshake")
	preflight := flag.Bool("preflight", false, "verify that the selected Npcap device can be opened and filtered")
	filter := flag.String("filter", "", "optional BPF filter; defaults to the configured UDP port range")
	stopFile := flag.String("stop-file", "", "stop a live capture cleanly when this file appears")
	statusFile := flag.String("status-file", "", "write live capture status JSON to this path")
	maxPCAPBytes := flag.Int64("max-pcap-bytes", 0, "stop before a live PCAP would exceed this size")
	flag.Parse()

	if *minPort < 1 || *maxPort > 65535 || *minPort > *maxPort {
		fatal("port range", fmt.Errorf("invalid range %d-%d", *minPort, *maxPort))
	}
	if *pcapIn != "" && *pcapOut != "" {
		fatal("PCAP mode", fmt.Errorf("-pcap-in and -pcap-out cannot be combined"))
	}
	if *combatNotifyAuditOut != "" && *pcapIn == "" {
		fatal("CombatNotifyData audit", fmt.Errorf("-combat-notify-audit-out requires -pcap-in"))
	}
	if *verifyBaseline && *pcapIn == "" {
		fatal("baseline mode", fmt.Errorf("-verify-baseline requires -pcap-in"))
	}
	if *maxPCAPBytes > 0 && *maxPCAPBytes < 24 {
		fatal("PCAP size limit", fmt.Errorf("-max-pcap-bytes must be at least 24"))
	}
	filterExpression := *filter
	if filterExpression == "" {
		filterExpression = fmt.Sprintf("udp and portrange %d-%d", *minPort, *maxPort)
	}
	if *preflight {
		device, err := findDeviceByIP(*ip)
		if err != nil {
			fatal("find Npcap adapter", err)
		}
		handle, err := pcap.OpenLive(device, 65535, false, time.Second)
		if err != nil {
			fatal("open Npcap adapter", err)
		}
		defer handle.Close()
		if err := handle.SetBPFFilter(filterExpression); err != nil {
			fatal("set capture filter", err)
		}
		fmt.Fprintf(os.Stderr, "capture_preflight=PASS adapter=%s local_ipv4=%s filter=%q\n", device, *ip, filterExpression)
		return
	}
	if (*stopFile != "" || *statusFile != "" || *maxPCAPBytes > 0) && (*pcapIn != "" || *pcapOut == "") {
		fatal("live capture controls", fmt.Errorf("-stop-file, -status-file, and -max-pcap-bytes require live capture with -pcap-out"))
	}
	var masters *gameMaster
	var err error
	if _, statErr := os.Stat(*masterDir); statErr == nil {
		masters, err = loadGameMaster(*masterDir)
		if err != nil {
			fatal("load local master data", err)
		}
	} else if !os.IsNotExist(statErr) {
		fatal("inspect local master data", statErr)
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
		if _, err := fmt.Fprintln(auditFile, "timestamp\trpc_id\tattacker_entity_id\ttarget_entity_id\tattacker_runtime_status\tattacker_runtime_evidence\ttarget_runtime_status\ttarget_runtime_evidence\tcharacter_id\tcharacter_name\tcharacter_status\tcharacter_evidence\tskill_id\tskill_name\tskill_status\tskill_evidence\tskill_message_id\tdamage_id\tmonster_id\ttarget_name\ttarget_monster_status\ttarget_monster_evidence\tsummoner_id\tsummoner_character_id\tsummoner_character_name\tsummoner_character_status\tsummoner_character_evidence\tsummoner_entity_chain\tplayer_entity_id\tdamage\tdamage_source_type\tbullet_id\tattacker_runtime_fields_json\ttarget_runtime_fields_json\tserver_result_raw\tchange_life_raw\tdamage_calculate_type\tevent_type_candidate\tevent_type_status\tevent_type_evidence\tcharacter_name_status\tskill_name_status\tclient_build\tcheck_client_version_fields_json\tclient_version_source_commands"); err != nil {
			fatal("write event audit header", err)
		}
		auditWriter = auditFile
	}
	var buffSnapshotFile *os.File
	if *buffSnapshotOut != "" {
		if dir := filepath.Dir(*buffSnapshotOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create Buff snapshot directory", err)
			}
		}
		buffSnapshotFile, err = os.Create(*buffSnapshotOut)
		if err != nil {
			fatal("create Buff snapshot JSONL", err)
		}
		defer buffSnapshotFile.Close()
	}
	var buffEventFile *os.File
	if *buffEventOut != "" {
		if dir := filepath.Dir(*buffEventOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create Buff lifecycle directory", err)
			}
		}
		buffEventFile, err = os.Create(*buffEventOut)
		if err != nil {
			fatal("create Buff lifecycle JSONL", err)
		}
		defer buffEventFile.Close()
	}
	var encounterSummaryFile *os.File
	if *encounterSummaryOut != "" {
		if dir := filepath.Dir(*encounterSummaryOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create encounter summary directory", err)
			}
		}
		encounterSummaryFile, err = os.Create(*encounterSummaryOut)
		if err != nil {
			fatal("create encounter summary", err)
		}
		defer encounterSummaryFile.Close()
	}
	var combatEventsJSONLFile *os.File
	if *combatEventsJSONLOut != "" {
		if dir := filepath.Dir(*combatEventsJSONLOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create combat event JSONL directory", err)
			}
		}
		combatEventsJSONLFile, err = os.Create(*combatEventsJSONLOut)
		if err != nil {
			fatal("create combat event JSONL", err)
		}
		defer combatEventsJSONLFile.Close()
	}
	var combatNotifyAuditFile *os.File
	if *combatNotifyAuditOut != "" {
		if dir := filepath.Dir(*combatNotifyAuditOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create CombatNotifyData audit directory", err)
			}
		}
		combatNotifyAuditFile, err = os.OpenFile(*combatNotifyAuditOut, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			fatal("create CombatNotifyData audit", err)
		}
		defer combatNotifyAuditFile.Close()
	}
	processor := newProcessor(rsaKey, stdout, os.Stderr, auditWriter)
	processor.masters = masters
	processor.research = *research
	if buffSnapshotFile != nil {
		processor.buffSnapshots = buffSnapshotFile
	}
	if buffEventFile != nil {
		processor.buffLifecycle = buffEventFile
	}
	if encounterSummaryFile != nil {
		processor.encounterSummary = encounterSummaryFile
	}
	processor.combatEventsJSONL = optionalFileWriter(combatEventsJSONLFile)
	processor.combatNotifyAudit = optionalFileWriter(combatNotifyAuditFile)
	if *commandCensusOut != "" {
		processor.census = newCommandCensus()
	}
	if masters == nil {
		fmt.Fprintf(os.Stderr, "master_data_unavailable path=%s; names will remain unresolved\n", *masterDir)
	} else {
		fmt.Fprintf(os.Stderr, "master_data_loaded game=%s resource=%s region=%s language=%s characters=%d monsters=%d skills=%d damage=%d buffs=%s\n",
			masters.manifest.GameVersion, masters.manifest.ResourceVersion, masters.manifest.ClientRegion, masters.manifest.Language,
			len(masters.characters), len(masters.monsters), len(masters.skills), len(masters.damage), masters.manifest.BuffStatus)
	}

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

	if err := handle.SetBPFFilter(filterExpression); err != nil {
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
		fmt.Fprintf(os.Stderr, "WWLOGS live capture started adapter=%s filter=%q\n", device, filterExpression)
	} else {
		fmt.Fprintf(os.Stderr, "WWLOGS offline replay started file=%s filter=%q\n", *pcapIn, filterExpression)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	streams := make(map[string]*stream)
	stats := captureStats{}
	started := time.Now()
	stopReason := "deadline"
	currentPCAPBytes := int64(24)
	status := captureRunStatus{
		Build:        buildinfo.CurrentRecord(),
		PID:          os.Getpid(),
		State:        "RUNNING",
		StartedAt:    started,
		UpdatedAt:    started,
		Adapter:      device,
		LocalIPv4:    *ip,
		Filter:       filterExpression,
		PCAPPath:     *pcapOut,
		PCAPBytes:    currentPCAPBytes,
		MaxPCAPBytes: *maxPCAPBytes,
	}
	if *duration > 0 && *pcapIn == "" {
		status.DeadlineAt = started.Add(*duration)
	}
	lastStatusWrite := time.Time{}
	writeStatus := func(state, reason string) {
		if *statusFile == "" {
			return
		}
		now := time.Now()
		if state == "RUNNING" && !lastStatusWrite.IsZero() && now.Sub(lastStatusWrite) < time.Second {
			return
		}
		if statsResult, statsErr := handle.Stats(); statsErr == nil && statsResult != nil {
			stats.kernelDrops = statsResult.PacketsDropped
		}
		status.State = state
		status.StopReason = reason
		status.UpdatedAt = now
		lastStatusWrite = now
		status.UDPPackets = stats.udpPackets
		status.KernelDrops = stats.kernelDrops
		status.PCAPBytes = currentPCAPBytes
		data, err := json.Marshal(status)
		if err == nil {
			err = os.WriteFile(*statusFile, data, 0o600)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "capture_status_write_error=%v\n", err)
		}
	}
	writeStatus("RUNNING", "")
	stop := make(chan os.Signal, 1)
	if *pcapIn == "" {
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(stop)
	}
	for *pcapIn != "" || *duration == 0 || time.Since(started) < *duration {
		if *pcapIn == "" {
			if *stopFile != "" {
				if _, err := os.Stat(*stopFile); err == nil {
					stopReason = "requested"
					goto finished
				}
			}
			select {
			case <-stop:
				stopReason = "signal"
				goto finished
			default:
			}
		}
		packet, packetErr := packetSource.NextPacket()
		if packetErr != nil {
			if packetErr == pcap.NextErrorTimeoutExpired {
				writeStatus("RUNNING", "")
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
			recordBytes := int64(16 + len(packet.Data()))
			if *maxPCAPBytes > 0 && currentPCAPBytes+recordBytes > *maxPCAPBytes {
				stopReason = "size_limit"
				goto finished
			}
			if err := pcapWriter.WritePacket(packet.Metadata().CaptureInfo, packet.Data()); err != nil {
				fatal("write captured PCAP packet", err)
			}
			currentPCAPBytes += recordBytes
		}
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		stats.udpPackets++
		direction := packetDirection(packet, *ip, udp, *minPort, *maxPort)
		consumeDatagram(udp.Payload, direction, packet.Metadata().CaptureInfo.Timestamp, streams, processor, &stats)
		writeStatus("RUNNING", "")
	}

finished:
	if statsResult, statsErr := handle.Stats(); statsErr == nil && statsResult != nil {
		stats.kernelDrops = statsResult.PacketsDropped
	}
	if pcapFile != nil {
		if err := pcapFile.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "pcap_sync_error=%v\n", err)
		}
	}
	writeStatus("STOPPED", stopReason)
	if err := stdout.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "stdout flush warning: %v\n", err)
	}
	if err := processor.writeEncounterSummary(); err != nil {
		fatal("write encounter summary", err)
	}
	processor.printSummary(os.Stderr)
	fmt.Fprintf(os.Stderr, "capture_finished udp=%d kcp_push=%d kcp_ordered=%d kcp_messages=%d protocol_frames=%d bad_crc=%d malformed_frames=%d kernel_drops=%d duration=%s\n",
		stats.udpPackets, stats.kcpPush, stats.kcpOrdered, stats.kcpMessages, stats.frames, stats.badCRC, stats.malformed, stats.kernelDrops, time.Since(started).Round(time.Millisecond))
	if *commandCensusOut != "" {
		if dir := filepath.Dir(*commandCensusOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create command census directory", err)
			}
		}
		if err := processor.census.write(*commandCensusOut, stats, processor.clientVersion); err != nil {
			fatal("write command census", err)
		}
	}
	if *clientVersionOut != "" && processor.clientVersion != nil {
		if dir := filepath.Dir(*clientVersionOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create client version directory", err)
			}
		}
		data, err := json.MarshalIndent(processor.clientVersion, "", "  ")
		if err != nil {
			fatal("marshal client version metadata", err)
		}
		if err := os.WriteFile(*clientVersionOut, data, 0o600); err != nil {
			fatal("write client version metadata", err)
		}
	}
	if *verifyBaseline {
		if err := processor.verifyKnownBaseline(); err != nil {
			fatal("baseline verification", err)
		}
		fmt.Fprintln(os.Stderr, "baseline_verification=PASS")
	}
}
