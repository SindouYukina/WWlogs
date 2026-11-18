package devcapture

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
	_ "modernc.org/sqlite"

	"wwlogs/internal/buildinfo"
	"wwlogs/internal/capture"
	"wwlogs/internal/versioninfo"
)

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
	// RunID identifies the run; the PCAP integrity record and the diagnostic pipeline line carry the same ID.
	RunID string `json:"runId,omitempty"`
}

type liveDiagnosticSnapshot struct {
	Type                             string                           `json:"type"`
	Final                            bool                             `json:"final"`
	SampledAt                        time.Time                        `json:"sampledAt"`
	CaptureObservedAt                time.Time                        `json:"captureObservedAt,omitempty"`
	ClockDifferenceSeconds           *float64                         `json:"clockDifferenceSeconds,omitempty"`
	LocalSampleIntervalSeconds       *float64                         `json:"localSampleIntervalSeconds,omitempty"`
	CaptureTimestampAdvanceSeconds   *float64                         `json:"captureTimestampAdvanceSeconds,omitempty"`
	CaptureTimeAdvanceInterpretation string                           `json:"captureTimeAdvanceInterpretation"`
	ClockDifferenceInterpretation    string                           `json:"clockDifferenceInterpretation"`
	DamageStatistics                 capture.DamageStatisticsSnapshot `json:"damageStatistics"`
	Effects                          capture.EffectSnapshot           `json:"effects"`
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

func masterDatabaseSourceLabel(databasePath string) string {
	if databasePath == "embedded" {
		return "embedded"
	}
	return "external test override"
}

func (p *processor) liveDiagnosticSnapshot(sampledAt time.Time) liveDiagnosticSnapshot {
	statistics := p.DamageStatisticsSnapshot()
	captureObservedAt := p.CurrentFrameTime
	if !captureObservedAt.IsZero() {
		statistics = p.DamageStatisticsSnapshotAt(capture.CaptureQueryTime{
			CaptureSessionID: statistics.CaptureSessionID,
			Timestamp:        captureObservedAt,
		})
	}
	effects := p.EffectSnapshotAtLastFrame()
	var clockDifference *float64
	if !sampledAt.IsZero() && !captureObservedAt.IsZero() {
		difference := sampledAt.Sub(captureObservedAt).Seconds()
		clockDifference = &difference
	}
	return liveDiagnosticSnapshot{
		Type:                             "LIVE_DIAGNOSTIC_SNAPSHOT",
		SampledAt:                        sampledAt,
		CaptureObservedAt:                captureObservedAt,
		ClockDifferenceSeconds:           clockDifference,
		CaptureTimeAdvanceInterpretation: "A flat packet timestamp may mean an idle capture or a game-time pause; this field is not proof of either.",
		ClockDifferenceInterpretation:    "Diagnostic comparison only; clock-domain equivalence is not established. DPS uses capture-observed timestamps only.",
		DamageStatistics:                 statistics,
		Effects:                          effects,
	}
}

func writeLiveDiagnosticSnapshot(w io.Writer, snapshot liveDiagnosticSnapshot) error {
	return json.NewEncoder(w).Encode(snapshot)
}

func optionalFileWriter(file *os.File) io.Writer {
	if file == nil {
		return nil
	}
	return file
}

func defaultLiveCaptureOutputBundle(localAppData string, now time.Time) (liveCaptureOutputBundle, error) {
	if localAppData == "" {
		var err error
		localAppData, err = os.UserCacheDir()
		if err != nil {
			return liveCaptureOutputBundle{}, fmt.Errorf("resolve local capture directory: %w", err)
		}
	}
	spoolRoot := filepath.Join(localAppData, "WWlogs", "capture-spool")
	if err := os.MkdirAll(spoolRoot, 0o700); err != nil {
		return liveCaptureOutputBundle{}, fmt.Errorf("create local capture spool: %w", err)
	}
	baseRunID := "LIVE_" + now.Local().Format("20060102_150405")
	var directory string
	for suffix := 0; ; suffix++ {
		runID := baseRunID
		if suffix > 0 {
			runID = fmt.Sprintf("%s_%d", baseRunID, suffix)
		}
		directory = filepath.Join(spoolRoot, runID)
		if err := os.Mkdir(directory, 0o700); err == nil {
			break
		} else if !os.IsExist(err) {
			return liveCaptureOutputBundle{}, fmt.Errorf("create local capture directory: %w", err)
		}
	}
	return liveCaptureOutputBundle{
		directory:        directory,
		pcap:             filepath.Join(directory, "capture.pcap"),
		console:          filepath.Join(directory, "console.log"),
		combatEvents:     filepath.Join(directory, "combat-events.jsonl"),
		buffEvents:       filepath.Join(directory, "buff-events.jsonl"),
		buffSnapshots:    filepath.Join(directory, "buff-snapshots.jsonl"),
		liveDiagnostics:  filepath.Join(directory, "dps-effects.jsonl"),
		effectState:      filepath.Join(directory, "effect-state-final.json"),
		status:           filepath.Join(directory, "status.json"),
		encounterSummary: filepath.Join(directory, "encounter-summary.md"),
		encounterEvents:  filepath.Join(directory, "encounter-events.jsonl"),
	}, nil
}

func hasAnyOutputPath(paths ...string) bool {
	for _, path := range paths {
		if path != "" {
			return true
		}
	}
	return false
}

func flagWasSet(name string) bool {
	set := false
	flag.Visit(func(current *flag.Flag) {
		if current.Name == name {
			set = true
		}
	})
	return set
}

// Main parses the capture command flags and runs live capture or PCAP replay.
func Main() {
	capture.ExitIfVersionRequested(PrintVersion)
	ip := flag.String("ip", "", "Local network interface IPv4 address; if omitted, list available interfaces for selection")
	minPort := flag.Int("min-port", 13100, "Lower bound of the UDP server port range")
	maxPort := flag.Int("max-port", 13120, "Upper bound of the UDP server port range")
	duration := flag.Duration("duration", 0, "Live capture duration; 0 runs until Ctrl+C is pressed")
	pcapIn := flag.String("pcap-in", "", "Replay the specified PCAP file without live capture")
	pcapOut := flag.String("pcap-out", "", "Optionally write captured UDP packets to a PCAP file")
	masterDatabase := flag.String("master-db", "", "Development: use an explicit read-only Master SQLite database for testing (default: embedded)")
	showVersion := flag.Bool("version", false, "Print the WWlogs version")
	auditOut := flag.String("audit-out", "", "Optionally write RPC, entity, and damage-pairing TSV without secrets")
	buffSnapshotOut := flag.String("buff-snapshot-out", "", "Optionally write EntityActiveResponse Buff snapshots as JSONL")
	buffEventOut := flag.String("buff-event-out", "", "Optionally write supported Buff lifecycle observations as JSONL")
	combatNotifyAuditOut := flag.String("combat-notify-audit-out", "", "Offline only: write de-identified CombatNotifyData field audit records as JSONL")
	encounterSummaryOut := flag.String("encounter-summary-out", "", "Optionally write an evidence-labeled encounter and character summary in Markdown")
	combatEventsJSONLOut := flag.String("event-jsonl-out", "", "Optionally write paired, evidence-labeled combat and Buff observations as JSONL")
	protocolSchemaPath := flag.String("protocol-schema", "", "Optionally specify a local protocol schema JSON file")

	schemaDecodeOut := flag.String("schema-decode-out", "", "Optionally write schema-decoded outer messages as JSONL; requires -protocol-schema")
	roleConfigOutPath := flag.String("role-config-out", "", "Optionally write RoleConfigInfoNotify character IDs resolved through Master as JSONL; requires -protocol-schema and -master-db")

	buffOverlapOut := flag.String("buff-overlap-out", "", "Optionally write Buff instances observed on each damage target at event time")
	effectStateOut := flag.String("effect-state-out", "", "Optionally write the final gameplay-effect state snapshot, with remaining-time estimates at the last frame time, as one JSON line")
	masterLocale := flag.String("locale", "zh-TW", "Locale for Master display names: zh-TW, zh-CN, en-US, ja-JP, or ko-KR")
	combatStateOutPath := flag.String("combat-state-out", "", "Optionally write attribute changes, recovery values, and state transitions as JSONL; requires -protocol-schema")
	hpStateOutPath := flag.String("hp-state-out", "", "Optionally write each entity's Life and LifeMax as JSONL; requires -protocol-schema")
	damagePushOutPath := flag.String("damage-push-out", "", "Optionally write DamageExecuteNotify records outside the damage RPC stream as JSONL; requires -protocol-schema")
	skillRefOutPath := flag.String("skill-ref-out", "", "Optionally audit SkillId values in combat messages against the EntitySkillPreload namespace as JSONL; requires -protocol-schema")
	bulletRefOutPath := flag.String("bullet-ref-out", "", "Optionally write BulletId, namespace, and resolution evidence from sent and received combat messages as JSONL; requires -protocol-schema")
	shieldRefOutPath := flag.String("shield-ref-out", "", "Optionally resolve ShieldUpdateInfo.ConfigId through shield.Id as JSONL; requires -protocol-schema and -master-db")
	partRefOutPath := flag.String("part-ref-out", "", "Optionally resolve DamageExecuteRequest.PartId through the target PartComponent as JSONL; requires -protocol-schema")
	clientPushOutPath := flag.String("client-push-out", "", "Optionally write skill requests, character reports, and skill-end push records as JSONL; requires -protocol-schema")
	summonOwnerOutPath := flag.String("summon-owner-out", "", "Optionally write summoner, concomitant, and dependence facts from join and entity-add messages as JSONL; requires -protocol-schema")
	frameArchiveOut := flag.String("frame-archive-out", "", "Optionally write a local JSONL archive for each reassembled frame; stores digests only, without keys or plaintext")
	research := flag.Bool("research", false, "Write per-event evidence details to standard error")
	commandCensusOut := flag.String("command-census-out", "", "Write local protocol commands and decode results as JSON")
	clientVersionOut := flag.String("client-version-out", "", "Write version metadata observed after the first handshake")
	preflight := flag.Bool("preflight", false, "Check whether the specified Npcap interface can be opened and filtered")
	captureImmediate := flag.Bool("capture-immediate", true, "Open the live Npcap handle in immediate mode; false restores batched delivery for latency comparison")
	filter := flag.String("filter", "", "Optionally specify a BPF filter; defaults to the configured UDP port range")
	stopFile := flag.String("stop-file", "", "Stop live capture cleanly when the specified file appears")
	statusFile := flag.String("status-file", "", "Write live capture status as JSON to the specified path")
	maxPCAPBytes := flag.Int64("max-pcap-bytes", 0, "Limit live PCAP size and stop before exceeding the limit")
	encounterOut := flag.String("encounter-out", "", "Optionally write battle-state signals, normalized combat records with their encounter assignment, and encounter transitions as JSONL")
	liveDiagnosticOut := flag.String("live-diagnostic-out", "", "Optionally write one bounded-rate JSONL damage and effect snapshot per second")
	showEffectEvents := flag.Bool("show-effect-events", true, "Print combat gameplay-effect lifecycle events during live capture")
	showAllEffectEvents := flag.Bool("show-all-effect-events", false, "Diagnostic: print every decoded gameplay-effect message during live capture, including unknown and non-combat effects, instant effects, requests and refreshes")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "WWlogs %s\nUsage: WWlogs [options]\n\nOptions:\n", versioninfo.Current().Product.DevelopmentLine)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Printf("WWlogs version: %s\n", versioninfo.Current().Product.DevelopmentLine)
		return
	}
	fmt.Printf("WWlogs %s\n", versioninfo.Current().Product.DevelopmentLine)
	announceBuildIdentity(os.Stderr)
	if *ip == "" && *pcapIn == "" {
		selectedIP, err := capture.PromptLocalIPv4(os.Stdin, os.Stdout)
		if err != nil {
			capture.Fatal("select network interface", err)
		}
		*ip = selectedIP
	}
	var defaultOutputDirectory, consoleLogPath string
	if *pcapIn == "" && !*preflight && !hasAnyOutputPath(
		*pcapOut, *auditOut, *buffSnapshotOut, *buffEventOut, *combatNotifyAuditOut,
		*encounterSummaryOut, *combatEventsJSONLOut, *schemaDecodeOut, *roleConfigOutPath,
		*buffOverlapOut, *effectStateOut, *combatStateOutPath, *hpStateOutPath, *damagePushOutPath,
		*skillRefOutPath, *bulletRefOutPath, *shieldRefOutPath, *partRefOutPath, *clientPushOutPath,
		*summonOwnerOutPath, *frameArchiveOut, *commandCensusOut, *clientVersionOut, *statusFile,
		*liveDiagnosticOut, *encounterOut,
	) {
		bundle, err := defaultLiveCaptureOutputBundle(os.Getenv("LOCALAPPDATA"), time.Now())
		if err != nil {
			capture.Fatal("create default live capture output", err)
		}
		defaultOutputDirectory, consoleLogPath = bundle.directory, bundle.console
		*pcapOut = bundle.pcap
		*combatEventsJSONLOut = bundle.combatEvents
		*buffEventOut = bundle.buffEvents
		*buffSnapshotOut = bundle.buffSnapshots
		*liveDiagnosticOut = bundle.liveDiagnostics
		*effectStateOut = bundle.effectState
		*statusFile = bundle.status
		*encounterSummaryOut = bundle.encounterSummary
		*encounterOut = bundle.encounterEvents
		if !flagWasSet("duration") {
			*duration = 20 * time.Minute
		}
		if !flagWasSet("max-pcap-bytes") {
			*maxPCAPBytes = 2 << 30
		}
		fmt.Fprintf(os.Stdout, "Live capture output directory: %s\n", defaultOutputDirectory)
	}

	if *minPort < 1 || *maxPort > 65535 || *minPort > *maxPort {
		capture.Fatal("port range", fmt.Errorf("invalid range %d-%d", *minPort, *maxPort))
	}
	if *pcapIn != "" && *pcapOut != "" {
		capture.Fatal("PCAP mode", fmt.Errorf("-pcap-in and -pcap-out cannot be combined"))
	}
	if *liveDiagnosticOut != "" && *pcapIn != "" {
		capture.Fatal("live diagnostics", fmt.Errorf("-live-diagnostic-out requires live capture"))
	}
	if *combatNotifyAuditOut != "" && *pcapIn == "" {
		capture.Fatal("CombatNotifyData audit", fmt.Errorf("-combat-notify-audit-out requires -pcap-in"))
	}
	if *maxPCAPBytes > 0 && *maxPCAPBytes < 24 {
		capture.Fatal("PCAP size limit", fmt.Errorf("-max-pcap-bytes must be at least 24"))
	}
	filterExpression := *filter
	if filterExpression == "" {
		filterExpression = fmt.Sprintf("udp and portrange %d-%d", *minPort, *maxPort)
	}
	captureConfig := capture.DefaultLiveCaptureConfig()
	captureConfig.Immediate = *captureImmediate
	masters, err := capture.LoadGameMasterDatabase(*masterDatabase, *masterLocale)
	if err != nil {
		capture.Fatal("load combat master SQLite", err)
	}
	if *preflight {
		device, err := capture.FindDeviceByIP(*ip)
		if err != nil {
			capture.Fatal("find Npcap adapter", err)
		}
		handle, applied, err := capture.OpenLiveCapture(device, captureConfig)
		if err != nil {
			capture.Fatal("open Npcap adapter", err)
		}
		defer handle.Close()
		if err := handle.SetBPFFilter(filterExpression); err != nil {
			capture.Fatal("set capture filter", err)
		}
		fmt.Fprintf(os.Stderr, "capture_preflight=PASS adapter=%s local_ipv4=%s filter=%q master_source=%q master_sha256=%s locale=%s %s\n", device, *ip, filterExpression, masterDatabaseSourceLabel(masters.DatabasePath), masters.Manifest.SourceHash, masters.Locale, applied)
		return
	}
	if (*stopFile != "" || *statusFile != "" || *maxPCAPBytes > 0) && (*pcapIn != "" || *pcapOut == "") {
		capture.Fatal("live capture controls", fmt.Errorf("-stop-file, -status-file, and -max-pcap-bytes require live capture with -pcap-out"))
	}
	rsaKey, err := capture.LoadEmbeddedPrivateKey()
	if err != nil {
		capture.Fatal("load embedded RSA key", err)
	}
	var consoleLog *os.File
	var stdoutTarget io.Writer = os.Stdout
	var diagnosticTarget io.Writer = os.Stderr
	if consoleLogPath != "" {
		consoleLog, err = os.OpenFile(consoleLogPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			capture.Fatal("create console log", err)
		}
		defer consoleLog.Close()
		stdoutTarget = io.MultiWriter(os.Stdout, consoleLog)
		diagnosticTarget = io.MultiWriter(os.Stderr, consoleLog)
		fmt.Fprintf(consoleLog, "Live capture output directory: %s\n", defaultOutputDirectory)
	}
	stdout := bufio.NewWriter(stdoutTarget)
	// diagnostics owns the per-event sinks once the capture starts. A target whose writer was abandoned at stop is
	// never flushed or written here, so a stuck writer and this goroutine never share a buffer.
	var diagnostics *diagnosticPipeline
	runID := newDiagnosticRunID(time.Now())
	defer func() {
		if diagnostics.released(stdout) {
			stdout.Flush()
		}
	}()
	var auditFile *os.File
	var auditWriter io.Writer
	if *auditOut != "" {
		auditFile, err = os.Create(*auditOut)
		if err != nil {
			capture.Fatal("create event audit", err)
		}
		defer auditFile.Close()
		if _, err := fmt.Fprintln(auditFile, "timestamp\trpc_id\tattacker_entity_id\ttarget_entity_id\tattacker_runtime_status\tattacker_runtime_evidence\ttarget_runtime_status\ttarget_runtime_evidence\tcharacter_id\tcharacter_name\tcharacter_status\tcharacter_evidence\tskill_id\tskill_name\tskill_status\tskill_evidence\tskill_message_id\tdamage_id\tmonster_id\ttarget_name\ttarget_monster_status\ttarget_monster_evidence\tsummoner_id\tsummoner_character_id\tsummoner_character_name\tsummoner_character_status\tsummoner_character_evidence\tsummoner_entity_chain\tplayer_entity_id\tdamage\tdamage_source_type\tbullet_id\tattacker_runtime_fields_json\ttarget_runtime_fields_json\tserver_result_raw\tchange_life_raw\tdamage_calculate_type\tevent_type_candidate\tevent_type_status\tevent_type_evidence\tcharacter_name_status\tskill_name_status\tclient_build\tcheck_client_version_fields_json\tclient_version_source_commands"); err != nil {
			capture.Fatal("write event audit header", err)
		}
		auditWriter = auditFile
	}
	var buffSnapshotFile *os.File
	if *buffSnapshotOut != "" {
		if dir := filepath.Dir(*buffSnapshotOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create Buff snapshot directory", err)
			}
		}
		buffSnapshotFile, err = os.Create(*buffSnapshotOut)
		if err != nil {
			capture.Fatal("create Buff snapshot JSONL", err)
		}
		defer buffSnapshotFile.Close()
	}
	var buffEventFile *os.File
	if *buffEventOut != "" {
		if dir := filepath.Dir(*buffEventOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create Buff lifecycle directory", err)
			}
		}
		buffEventFile, err = os.Create(*buffEventOut)
		if err != nil {
			capture.Fatal("create Buff lifecycle JSONL", err)
		}
		defer buffEventFile.Close()
	}
	var encounterSummaryFile *os.File
	if *encounterSummaryOut != "" {
		if dir := filepath.Dir(*encounterSummaryOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create encounter summary directory", err)
			}
		}
		encounterSummaryFile, err = os.Create(*encounterSummaryOut)
		if err != nil {
			capture.Fatal("create encounter summary", err)
		}
		defer encounterSummaryFile.Close()
	}
	var combatEventsJSONLFile *os.File
	if *combatEventsJSONLOut != "" {
		if dir := filepath.Dir(*combatEventsJSONLOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create combat event JSONL directory", err)
			}
		}
		combatEventsJSONLFile, err = os.Create(*combatEventsJSONLOut)
		if err != nil {
			capture.Fatal("create combat event JSONL", err)
		}
		defer combatEventsJSONLFile.Close()
	}
	var frameArchiveFile *os.File
	if *frameArchiveOut != "" {
		if dir := filepath.Dir(*frameArchiveOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create frame archive directory", err)
			}
		}
		frameArchiveFile, err = os.Create(*frameArchiveOut)
		if err != nil {
			capture.Fatal("create frame archive", err)
		}
		defer frameArchiveFile.Close()
	}
	var combatNotifyAuditFile *os.File
	if *combatNotifyAuditOut != "" {
		if dir := filepath.Dir(*combatNotifyAuditOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create CombatNotifyData audit directory", err)
			}
		}
		combatNotifyAuditFile, err = os.OpenFile(*combatNotifyAuditOut, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			capture.Fatal("create CombatNotifyData audit", err)
		}
		defer combatNotifyAuditFile.Close()
	}
	processor := newProcessor(rsaKey, stdout, diagnosticTarget, auditWriter)
	processor.dev.showEffectEvents = *showEffectEvents && *pcapIn == ""
	processor.dev.showAllEffectEvents = *showAllEffectEvents
	processor.Masters = masters
	processor.dev.research = *research
	if buffSnapshotFile != nil {
		processor.dev.buffSnapshots = buffSnapshotFile
	}
	if buffEventFile != nil {
		processor.dev.buffLifecycle = buffEventFile
	}
	if *encounterOut != "" {
		if dir := filepath.Dir(*encounterOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create encounter output directory", err)
			}
		}
		encounterFile, err := os.Create(*encounterOut)
		if err != nil {
			capture.Fatal("create encounter output", err)
		}
		defer encounterFile.Close()
		encounterWriter := bufio.NewWriter(encounterFile)
		defer func() {
			if diagnostics.released(encounterWriter) {
				encounterWriter.Flush()
			}
		}()
		processor.dev.encounterOut = encounterWriter
		processor.recordEncounterTransitions()
	}
	if encounterSummaryFile != nil {
		processor.dev.encounterSummary = encounterSummaryFile
	}
	processor.dev.combatEventsJSONL = optionalFileWriter(combatEventsJSONLFile)
	processor.dev.combatNotifyAudit = optionalFileWriter(combatNotifyAuditFile)
	if *protocolSchemaPath != "" {
		schema, err := loadProtocolSchema(*protocolSchemaPath)
		if err != nil {
			capture.Fatal("load protocol schema", err)
		}
		processor.dev.protocolSchema = schema
	}
	if *summonOwnerOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("summon owner output", fmt.Errorf("-summon-owner-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*summonOwnerOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create summon owner directory", err)
			}
		}
		ownerFile, err := os.Create(*summonOwnerOutPath)
		if err != nil {
			capture.Fatal("create summon owner output", err)
		}
		defer ownerFile.Close()
		ownerWriter := bufio.NewWriter(ownerFile)
		processor.dev.summonOwnerOut = ownerWriter
		defer func() {
			if !diagnostics.released(ownerWriter) {
				return
			}
			if err := ownerWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "summon owner flush error: %v\n", err)
			}
		}()
	}
	if *skillRefOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("skill reference output", fmt.Errorf("-skill-ref-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*skillRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create skill reference directory", err)
			}
		}
		skillFile, err := os.Create(*skillRefOutPath)
		if err != nil {
			capture.Fatal("create skill reference output", err)
		}
		defer skillFile.Close()
		skillWriter := bufio.NewWriter(skillFile)
		processor.dev.skillRefOut = skillWriter
		defer func() {
			if !diagnostics.released(skillWriter) {
				return
			}
			if err := skillWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "skill reference flush error: %v\n", err)
			}
		}()
	}
	if *bulletRefOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("bullet reference output", fmt.Errorf("-bullet-ref-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*bulletRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create bullet reference directory", err)
			}
		}
		bulletFile, err := os.Create(*bulletRefOutPath)
		if err != nil {
			capture.Fatal("create bullet reference output", err)
		}
		defer bulletFile.Close()
		bulletWriter := bufio.NewWriter(bulletFile)
		processor.dev.bulletRefOut = bulletWriter
		defer func() {
			if !diagnostics.released(bulletWriter) {
				return
			}
			if err := bulletWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "bullet reference flush error: %v\n", err)
			}
		}()
	}
	if *shieldRefOutPath != "" {
		if processor.dev.protocolSchema == nil || masters == nil {
			capture.Fatal("shield reference output", fmt.Errorf("-shield-ref-out requires -protocol-schema and -master-db"))
		}
		if dir := filepath.Dir(*shieldRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create shield reference directory", err)
			}
		}
		shieldFile, err := os.Create(*shieldRefOutPath)
		if err != nil {
			capture.Fatal("create shield reference output", err)
		}
		defer shieldFile.Close()
		shieldWriter := bufio.NewWriter(shieldFile)
		processor.dev.shieldRefOut = shieldWriter
		defer func() {
			if !diagnostics.released(shieldWriter) {
				return
			}
			if err := shieldWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "shield reference flush error: %v\n", err)
			}
		}()
	}
	if *partRefOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("part reference output", fmt.Errorf("-part-ref-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*partRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create part reference directory", err)
			}
		}
		partFile, err := os.Create(*partRefOutPath)
		if err != nil {
			capture.Fatal("create part reference output", err)
		}
		defer partFile.Close()
		partWriter := bufio.NewWriter(partFile)
		processor.dev.partRefOut = partWriter
		defer func() {
			if !diagnostics.released(partWriter) {
				return
			}
			if err := partWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "part reference flush error: %v\n", err)
			}
		}()
	}
	if *clientPushOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("client push output", fmt.Errorf("-client-push-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*clientPushOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create client push directory", err)
			}
		}
		clientFile, err := os.Create(*clientPushOutPath)
		if err != nil {
			capture.Fatal("create client push output", err)
		}
		defer clientFile.Close()
		clientWriter := bufio.NewWriter(clientFile)
		processor.dev.clientPushOut = clientWriter
		defer func() {
			if !diagnostics.released(clientWriter) {
				return
			}
			if err := clientWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "client push flush error: %v\n", err)
			}
		}()
	}
	if *damagePushOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("damage push output", fmt.Errorf("-damage-push-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*damagePushOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create damage push directory", err)
			}
		}
		pushFile, err := os.Create(*damagePushOutPath)
		if err != nil {
			capture.Fatal("create damage push output", err)
		}
		defer pushFile.Close()
		pushWriter := bufio.NewWriter(pushFile)
		processor.dev.damagePushOut = pushWriter
		defer func() {
			if !diagnostics.released(pushWriter) {
				return
			}
			if err := pushWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "damage push flush error: %v\n", err)
			}
		}()
	}
	if *combatStateOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("combat state output", fmt.Errorf("-combat-state-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*combatStateOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create combat state directory", err)
			}
		}
		stateFile, err := os.Create(*combatStateOutPath)
		if err != nil {
			capture.Fatal("create combat state output", err)
		}
		defer stateFile.Close()
		stateWriter := bufio.NewWriter(stateFile)
		processor.dev.combatStateOut = stateWriter
		defer func() {
			if !diagnostics.released(stateWriter) {
				return
			}
			if err := stateWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "combat state flush error: %v\n", err)
			}
		}()
	}
	if *hpStateOutPath != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("hp state output", fmt.Errorf("-hp-state-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*hpStateOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create hp state directory", err)
			}
		}
		hpFile, err := os.Create(*hpStateOutPath)
		if err != nil {
			capture.Fatal("create hp state output", err)
		}
		defer hpFile.Close()
		hpWriter := bufio.NewWriter(hpFile)
		processor.dev.hpStateOut = hpWriter
		defer func() {
			if !diagnostics.released(hpWriter) {
				return
			}
			if err := hpWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "hp state flush error: %v\n", err)
			}
		}()
	}
	if *schemaDecodeOut != "" {
		if processor.dev.protocolSchema == nil {
			capture.Fatal("schema decode output", fmt.Errorf("-schema-decode-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*schemaDecodeOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create schema decode directory", err)
			}
		}
		schemaFile, err := os.Create(*schemaDecodeOut)
		if err != nil {
			capture.Fatal("create schema decode output", err)
		}
		defer schemaFile.Close()
		schemaWriter := bufio.NewWriter(schemaFile)
		processor.dev.schemaDecodeOut = schemaWriter
		defer func() {
			if !diagnostics.released(schemaWriter) {
				return
			}
			if err := schemaWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "schema decode flush error: %v\n", err)
			}
		}()
	}
	if *roleConfigOutPath != "" {
		if processor.dev.protocolSchema == nil || masters == nil {
			capture.Fatal("role config output", fmt.Errorf("-role-config-out requires -protocol-schema and -master-db"))
		}
		if dir := filepath.Dir(*roleConfigOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create role config directory", err)
			}
		}
		roleConfigFile, err := os.Create(*roleConfigOutPath)
		if err != nil {
			capture.Fatal("create role config output", err)
		}
		defer roleConfigFile.Close()
		roleConfigWriter := bufio.NewWriter(roleConfigFile)
		processor.dev.roleConfigOut = roleConfigWriter
		defer func() {
			if !diagnostics.released(roleConfigWriter) {
				return
			}
			if err := roleConfigWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "role config flush error: %v\n", err)
			}
		}()
	}
	var frameArchiveWriter *bufio.Writer
	if frameArchiveFile != nil {
		frameArchiveWriter = bufio.NewWriter(frameArchiveFile)
		processor.dev.frameArchive = frameArchiveWriter
	}
	var census *commandCensus
	if *commandCensusOut != "" {
		census = newCommandCensus(processor)
		processor.Census = census
	}
	// Every per-event diagnostic sink now writes from its own goroutine; the capture loop only queues records.
	diagnostics = newDiagnosticPipeline(*pcapIn == "")
	flushers := map[io.Writer]func() error{stdout: stdout.Flush}
	if frameArchiveWriter != nil {
		flushers[frameArchiveWriter] = frameArchiveWriter.Flush
	}
	restoreDiagnostics := wrapProcessorDiagnostics(processor, diagnostics, flushers)
	fmt.Fprintf(os.Stderr, "master_data_loaded source=%q sha256=%s locale=%s characters=%d monsters=%d skills=%d damage=%d\n",
		masterDatabaseSourceLabel(masters.DatabasePath), masters.Manifest.SourceHash, masters.Locale,
		len(masters.Characters), len(masters.Monsters), len(masters.Skills), len(masters.Damage))

	var device string
	var handle *pcap.Handle
	if *pcapIn != "" {
		handle, err = pcap.OpenOffline(*pcapIn)
		if err != nil {
			capture.Fatal("open offline PCAP", err)
		}
		device = "offline:" + *pcapIn
	} else {
		device, err = capture.FindDeviceByIP(*ip)
		if err != nil {
			capture.Fatal("find Npcap adapter", err)
		}
		var applied capture.LiveCaptureApplied
		handle, applied, err = capture.OpenLiveCapture(device, captureConfig)
		if err != nil {
			capture.Fatal("open Npcap adapter", err)
		}
		fmt.Fprintf(os.Stderr, "capture_handle %s\n", applied)
	}
	defer handle.Close()

	if err := handle.SetBPFFilter(filterExpression); err != nil {
		capture.Fatal("set capture filter", err)
	}

	// The development PCAP queues packet records whole; a write failure disables it without stopping the analysis, and
	// its integrity record says whether the file is complete.
	var developmentPCAP *diagnosticPCAP
	if *pcapOut != "" {
		developmentPCAP, err = openDiagnosticPCAP(*pcapOut, handle.LinkType(), runID, diagnostics, time.Now(), nil)
		if err != nil {
			capture.Fatal("create PCAP", err)
		}
	}

	if *pcapIn == "" {
		fmt.Fprintf(os.Stderr, "WWLOGS live capture started adapter=%s filter=%q\n", device, filterExpression)
	} else {
		fmt.Fprintf(os.Stderr, "WWLOGS offline replay started file=%s filter=%q\n", *pcapIn, filterExpression)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	processor.ObserveCaptureSource(*pcapIn == "")
	processor.CaptureLayer.DiagnosticDropCount = diagnostics.droppedRecords
	streams := make(map[string]*capture.Stream)
	stats := capture.CaptureStats{}
	started := time.Now()
	var liveDiagnosticFile *os.File
	if *liveDiagnosticOut != "" && *pcapIn == "" {
		if dir := filepath.Dir(*liveDiagnosticOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fmt.Fprintf(os.Stderr, "live_diagnostic_disabled error=%v\n", err)
			}
		}
		if _, err := os.Stat(filepath.Dir(*liveDiagnosticOut)); err == nil {
			liveDiagnosticFile, err = os.OpenFile(*liveDiagnosticOut, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				fmt.Fprintf(os.Stderr, "live_diagnostic_disabled error=%v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "live_diagnostic_disabled error=%v\n", err)
		}
	}
	var liveDiagnosticSink io.Writer
	if liveDiagnosticFile != nil {
		defer liveDiagnosticFile.Close()
		liveDiagnosticSink = diagnostics.wrap("live-diagnostics", liveDiagnosticFile, nil, diagnosticSinkCapacityBytes)
	}
	lastLiveDiagnostic := time.Time{}
	lastDiagnosticCaptureTime := time.Time{}
	writeLiveDiagnostic := func(now time.Time, final bool) {
		if liveDiagnosticSink == nil || (!final && !lastLiveDiagnostic.IsZero() && now.Sub(lastLiveDiagnostic) < liveDiagnosticInterval) {
			return
		}
		previousSampleAt := lastLiveDiagnostic
		previousCaptureAt := lastDiagnosticCaptureTime
		lastLiveDiagnostic = now
		snapshot := processor.liveDiagnosticSnapshot(now)
		snapshot.Final = final
		if !previousSampleAt.IsZero() {
			localInterval := now.Sub(previousSampleAt).Seconds()
			snapshot.LocalSampleIntervalSeconds = &localInterval
		}
		if !previousCaptureAt.IsZero() && !snapshot.CaptureObservedAt.IsZero() {
			captureAdvance := snapshot.CaptureObservedAt.Sub(previousCaptureAt).Seconds()
			snapshot.CaptureTimestampAdvanceSeconds = &captureAdvance
		}
		lastDiagnosticCaptureTime = snapshot.CaptureObservedAt
		if err := writeLiveDiagnosticSnapshot(liveDiagnosticSink, snapshot); err != nil {
			fmt.Fprintf(os.Stderr, "live_diagnostic_disabled error=%v\n", err)
			liveDiagnosticSink = nil
			return
		}
	}
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
	var statusWriter *latestFileWriter
	if *statusFile != "" {
		statusWriter = newLatestFileWriter(*statusFile)
	}
	writeStatus := func(state, reason string) {
		if *statusFile == "" {
			return
		}
		now := time.Now()
		if state == "RUNNING" && !lastStatusWrite.IsZero() && now.Sub(lastStatusWrite) < time.Second {
			return
		}
		if statsResult, statsErr := handle.Stats(); statsErr == nil && statsResult != nil {
			stats.KernelDrops = statsResult.PacketsDropped
		}
		status.State = state
		status.StopReason = reason
		status.UpdatedAt = now
		lastStatusWrite = now
		status.UDPPackets = stats.UDPPackets
		status.KernelDrops = stats.KernelDrops
		status.PCAPBytes = currentPCAPBytes
		status.DiagnosticDroppedRecords = diagnostics.droppedRecords()
		status.RunID = runID
		data, err := json.Marshal(status)
		if err != nil {
			fmt.Fprintf(os.Stderr, "capture_status_write_error=%v\n", err)
			return
		}
		if state == "RUNNING" {
			statusWriter.submit(data)
			return
		}
		// The final status is written once the run is over, so waiting for it costs no packets.
		if err := statusWriter.close(data, diagnosticFlushTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "capture_status_write_error=%v\n", err)
		}
		if count, last := statusWriter.errors(); count > 0 {
			fmt.Fprintf(os.Stderr, "capture_status_write_errors=%d last=%q\n", count, last)
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
				processor.SampleCaptureStats(handle, time.Now(), false)
				writeStatus("RUNNING", "")
				writeLiveDiagnostic(time.Now(), false)
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
		if developmentPCAP != nil {
			recordBytes := int64(16 + len(packet.Data()))
			if *maxPCAPBytes > 0 && currentPCAPBytes+recordBytes > *maxPCAPBytes {
				stopReason = "size_limit"
				goto finished
			}
			developmentPCAP.writePacket(packet.Metadata().CaptureInfo, packet.Data())
			currentPCAPBytes += recordBytes
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
		direction := capture.PacketDirection(packet, *ip, udp, *minPort, *maxPort)
		capturedAt := packet.Metadata().CaptureInfo.Timestamp
		capture.ConsumeDatagram(udp.Payload, direction, capturedAt, streams, processor.Processor, &stats)
		processor.ObservePacketTiming(capturedAt, receivedAt, time.Now())
		writeLiveDiagnostic(time.Now(), false)
		writeStatus("RUNNING", "")
	}

finished:
	capture.FinishTransport(streams, processor.Processor, &stats)
	processor.SampleCaptureStats(handle, time.Now(), true)
	processor.FinishCapture()
	processor.writeEncounterFinal()
	writeLiveDiagnostic(time.Now(), true)
	// Drain the asynchronous sinks before the synchronous end-of-run summaries use the same files directly.
	diagnostics.finishCapture()
	pipelineStats := diagnostics.close(diagnosticFlushTimeout)
	restoreDiagnostics()
	if statsResult, statsErr := handle.Stats(); statsErr == nil && statsResult != nil {
		stats.KernelDrops = statsResult.PacketsDropped
	}
	var pcapIntegrity *diagnosticPCAPIntegrity
	if developmentPCAP != nil {
		record := developmentPCAP.finish(time.Now(), stopReason, processor.CaptureLayerHealth().KernelDropped)
		pcapIntegrity = &record
	}
	writeStatus("STOPPED", stopReason)
	if diagnostics.released(stdout) {
		if err := stdout.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "stdout flush warning: %v\n", err)
		}
	}
	if err := processor.writeEncounterSummary(); err != nil {
		capture.Fatal("write encounter summary", err)
	}
	if frameArchiveWriter != nil && diagnostics.released(frameArchiveWriter) {
		if err := frameArchiveWriter.Flush(); err != nil {
			capture.Fatal("flush frame archive", err)
		}
		fmt.Fprintf(os.Stderr, "frame_archive frames=%d write_errors=%d\n", processor.dev.frameOrdinal, processor.dev.frameArchiveErrors)
	}
	if *buffOverlapOut != "" {
		if dir := filepath.Dir(*buffOverlapOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create buff overlap directory", err)
			}
		}
		overlapFile, err := os.Create(*buffOverlapOut)
		if err != nil {
			capture.Fatal("create buff overlap output", err)
		}
		overlapWriter := bufio.NewWriter(overlapFile)
		if err := processor.writeBuffOverlap(overlapWriter); err != nil {
			capture.Fatal("write buff overlap", err)
		}
		if err := overlapWriter.Flush(); err != nil {
			capture.Fatal("flush buff overlap", err)
		}
		overlapFile.Close()
		fmt.Fprintf(os.Stderr, "buff_overlap damage_results=%d intervals=%d duplicate_applies=%d unmatched_removes=%d skipped=%d interval_overflow=%d damage_overflow=%d\n", len(processor.dev.damageOverlap), len(processor.dev.buffIntervals), processor.dev.buffDuplicateApplies, processor.dev.buffUnmatchedRemoves, processor.dev.buffOverlapSkipped, processor.dev.buffIntervalOverflow, processor.dev.damageOverlapOverflow)
	}
	if *effectStateOut != "" {
		if dir := filepath.Dir(*effectStateOut); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				capture.Fatal("create effect state directory", err)
			}
		}
		effectFile, err := os.Create(*effectStateOut)
		if err != nil {
			capture.Fatal("create effect state output", err)
		}
		if err := processor.writeEffectState(effectFile); err != nil {
			capture.Fatal("write effect state", err)
		}
		if err := effectFile.Close(); err != nil {
			capture.Fatal("close effect state output", err)
		}
		counters := processor.Effects.Snapshot().Counters
		fmt.Fprintf(os.Stderr, "effect_state applies=%d duplicate_applies=%d instant_applies=%d updates=%d updates_without_apply=%d removes=%d removes_without_instance=%d holder_removals=%d excluded_unrelated=%d\n", counters.Applies, counters.DuplicateApplies, counters.InstantApplies, counters.Updates, counters.UpdatesWithoutApply, counters.Removes, counters.RemovesWithoutInstance, counters.HolderRemovals, counters.ExcludedUnrelated)
	}
	fmt.Fprintf(os.Stderr, "schema_decode errors=%d skipped_without_schema=%d\n", processor.dev.schemaDecodeErrors, processor.dev.schemaDecodeSkipped)
	fmt.Fprintln(os.Stderr, protocolReportOf(&processor.Protocol).line())
	for _, line := range processor.researchOutputErrorLines() {
		fmt.Fprintln(os.Stderr, line)
	}
	processor.printSummary(diagnosticTarget)
	if line := processor.diagnosticFailureLine(); line != "" {
		fmt.Fprintln(diagnosticTarget, line)
	}
	fmt.Fprintf(diagnosticTarget, "statistics_final %s\n", processor.CurrentStatisticsReport())
	fmt.Fprintln(diagnosticTarget, transportHealthLine(streams, stats))
	fmt.Fprintln(diagnosticTarget, rpcPairingLine(processor.RPCPairingSnapshot()))
	fmt.Fprintln(diagnosticTarget, damageNotifyLine(processor.NotifyStats, processor.ServerSequence.Total))
	fmt.Fprintln(diagnosticTarget, diagnosticPipelineLine(runID, pipelineStats))
	fmt.Fprintln(diagnosticTarget, captureLatencyLine(processor.CaptureLayerHealth()))
	if pcapIntegrity != nil {
		fmt.Fprintln(diagnosticTarget, pcapIntegrity.line())
	}
	fmt.Fprintf(diagnosticTarget, "capture_finished udp=%d kcp_push=%d kcp_ordered=%d kcp_messages=%d protocol_frames=%d bad_crc=%d malformed_frames=%d kernel_drops=%d duration=%s\n",
		stats.UDPPackets, stats.KCPPush, stats.KCPOrdered, stats.KCPMessages, stats.Frames, stats.BadCRC, stats.Malformed, stats.KernelDrops, time.Since(started).Round(time.Millisecond))
	if *commandCensusOut != "" {
		if dir := filepath.Dir(*commandCensusOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create command census directory", err)
			}
		}
		if err := census.write(*commandCensusOut, stats, processor.ClientVersion, protocolReportOf(&processor.Protocol)); err != nil {
			capture.Fatal("write command census", err)
		}
	}
	if *clientVersionOut != "" && processor.ClientVersion != nil {
		if dir := filepath.Dir(*clientVersionOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				capture.Fatal("create client version directory", err)
			}
		}
		data, err := json.MarshalIndent(processor.ClientVersion, "", "  ")
		if err != nil {
			capture.Fatal("marshal client version metadata", err)
		}
		if err := os.WriteFile(*clientVersionOut, data, 0o600); err != nil {
			capture.Fatal("write client version metadata", err)
		}
	}
}
