//go:build wwdev

package capture

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
	"wwlogs/internal/versioninfo"
)

func (p *processor) liveDiagnosticSnapshot(sampledAt time.Time) liveDiagnosticSnapshot {
	statistics := p.damageStatisticsSnapshot()
	captureObservedAt := p.currentFrameTime
	if !captureObservedAt.IsZero() {
		statistics = p.damageStatisticsSnapshotAt(CaptureQueryTime{
			CaptureSessionID: statistics.CaptureSessionID,
			Timestamp:        captureObservedAt,
		})
	}
	effects := p.effectSnapshotAtLastFrame()
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
	exitIfVersionRequested()
	ip := flag.String("ip", "", "Local network interface IPv4 address; if omitted, list available interfaces for selection")
	minPort := flag.Int("min-port", 13100, "Lower bound of the UDP server port range")
	maxPort := flag.Int("max-port", 13120, "Upper bound of the UDP server port range")
	duration := flag.Duration("duration", 0, "Live capture duration; 0 runs until Ctrl+C is pressed")
	pcapIn := flag.String("pcap-in", "", "Replay the specified PCAP file without live capture")
	pcapOut := flag.String("pcap-out", "", "Optionally write captured UDP packets to a PCAP file")
	masterDatabase := flag.String("master-db", defaultMasterDatabasePath(), "Read-only combat ID Master SQLite database")
	showVersion := flag.Bool("version", false, "Print the WWlogs version")
	verifyBaseline := flag.Bool("verify-baseline", false, "Verify the decoding and player-output regression baseline for the Locking PCAP")
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
		selectedIP, err := promptLocalIPv4(os.Stdin, os.Stdout)
		if err != nil {
			fatal("select network interface", err)
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
			fatal("create default live capture output", err)
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
		fatal("port range", fmt.Errorf("invalid range %d-%d", *minPort, *maxPort))
	}
	if *pcapIn != "" && *pcapOut != "" {
		fatal("PCAP mode", fmt.Errorf("-pcap-in and -pcap-out cannot be combined"))
	}
	if *liveDiagnosticOut != "" && *pcapIn != "" {
		fatal("live diagnostics", fmt.Errorf("-live-diagnostic-out requires live capture"))
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
	masters, err := loadGameMasterDatabase(*masterDatabase, *masterLocale)
	if err != nil {
		fatal("load combat master SQLite", err)
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
		fmt.Fprintf(os.Stderr, "capture_preflight=PASS adapter=%s local_ipv4=%s filter=%q master_sha256=%s locale=%s\n", device, *ip, filterExpression, masters.manifest.SourceHash, masters.locale)
		return
	}
	if (*stopFile != "" || *statusFile != "" || *maxPCAPBytes > 0) && (*pcapIn != "" || *pcapOut == "") {
		fatal("live capture controls", fmt.Errorf("-stop-file, -status-file, and -max-pcap-bytes require live capture with -pcap-out"))
	}
	rsaKey, err := loadEmbeddedPrivateKey()
	if err != nil {
		fatal("load embedded RSA key", err)
	}
	var consoleLog *os.File
	var stdoutTarget io.Writer = os.Stdout
	var diagnosticTarget io.Writer = os.Stderr
	if consoleLogPath != "" {
		consoleLog, err = os.OpenFile(consoleLogPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			fatal("create console log", err)
		}
		defer consoleLog.Close()
		stdoutTarget = io.MultiWriter(os.Stdout, consoleLog)
		diagnosticTarget = io.MultiWriter(os.Stderr, consoleLog)
		fmt.Fprintf(consoleLog, "Live capture output directory: %s\n", defaultOutputDirectory)
	}
	stdout := bufio.NewWriter(stdoutTarget)
	defer stdout.Flush()
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
	var frameArchiveFile *os.File
	if *frameArchiveOut != "" {
		if dir := filepath.Dir(*frameArchiveOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create frame archive directory", err)
			}
		}
		frameArchiveFile, err = os.Create(*frameArchiveOut)
		if err != nil {
			fatal("create frame archive", err)
		}
		defer frameArchiveFile.Close()
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
	processor := newProcessor(rsaKey, stdout, diagnosticTarget, auditWriter)
	processor.showEffectEvents = *showEffectEvents && *pcapIn == ""
	processor.showAllEffectEvents = *showAllEffectEvents
	processor.masters = masters
	processor.research = *research
	if buffSnapshotFile != nil {
		processor.buffSnapshots = buffSnapshotFile
	}
	if buffEventFile != nil {
		processor.buffLifecycle = buffEventFile
	}
	if *encounterOut != "" {
		if dir := filepath.Dir(*encounterOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create encounter output directory", err)
			}
		}
		encounterFile, err := os.Create(*encounterOut)
		if err != nil {
			fatal("create encounter output", err)
		}
		defer encounterFile.Close()
		encounterWriter := bufio.NewWriter(encounterFile)
		defer encounterWriter.Flush()
		processor.encounterOut = encounterWriter
		processor.damageStatistics.recordEncounterTransitions(true)
	}
	if encounterSummaryFile != nil {
		processor.encounterSummary = encounterSummaryFile
	}
	processor.combatEventsJSONL = optionalFileWriter(combatEventsJSONLFile)
	processor.combatNotifyAudit = optionalFileWriter(combatNotifyAuditFile)
	if *protocolSchemaPath != "" {
		schema, err := loadProtocolSchema(*protocolSchemaPath)
		if err != nil {
			fatal("load protocol schema", err)
		}
		processor.protocolSchema = schema
	}
	if *summonOwnerOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("summon owner output", fmt.Errorf("-summon-owner-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*summonOwnerOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create summon owner directory", err)
			}
		}
		ownerFile, err := os.Create(*summonOwnerOutPath)
		if err != nil {
			fatal("create summon owner output", err)
		}
		defer ownerFile.Close()
		ownerWriter := bufio.NewWriter(ownerFile)
		processor.summonOwnerOut = ownerWriter
		defer func() {
			if err := ownerWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "summon owner flush error: %v\n", err)
			}
		}()
	}
	if *skillRefOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("skill reference output", fmt.Errorf("-skill-ref-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*skillRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create skill reference directory", err)
			}
		}
		skillFile, err := os.Create(*skillRefOutPath)
		if err != nil {
			fatal("create skill reference output", err)
		}
		defer skillFile.Close()
		skillWriter := bufio.NewWriter(skillFile)
		processor.skillRefOut = skillWriter
		defer func() {
			if err := skillWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "skill reference flush error: %v\n", err)
			}
		}()
	}
	if *bulletRefOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("bullet reference output", fmt.Errorf("-bullet-ref-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*bulletRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create bullet reference directory", err)
			}
		}
		bulletFile, err := os.Create(*bulletRefOutPath)
		if err != nil {
			fatal("create bullet reference output", err)
		}
		defer bulletFile.Close()
		bulletWriter := bufio.NewWriter(bulletFile)
		processor.bulletRefOut = bulletWriter
		defer func() {
			if err := bulletWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "bullet reference flush error: %v\n", err)
			}
		}()
	}
	if *shieldRefOutPath != "" {
		if processor.protocolSchema == nil || masters == nil {
			fatal("shield reference output", fmt.Errorf("-shield-ref-out requires -protocol-schema and -master-db"))
		}
		if dir := filepath.Dir(*shieldRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create shield reference directory", err)
			}
		}
		shieldFile, err := os.Create(*shieldRefOutPath)
		if err != nil {
			fatal("create shield reference output", err)
		}
		defer shieldFile.Close()
		shieldWriter := bufio.NewWriter(shieldFile)
		processor.shieldRefOut = shieldWriter
		defer func() {
			if err := shieldWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "shield reference flush error: %v\n", err)
			}
		}()
	}
	if *partRefOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("part reference output", fmt.Errorf("-part-ref-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*partRefOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create part reference directory", err)
			}
		}
		partFile, err := os.Create(*partRefOutPath)
		if err != nil {
			fatal("create part reference output", err)
		}
		defer partFile.Close()
		partWriter := bufio.NewWriter(partFile)
		processor.partRefOut = partWriter
		defer func() {
			if err := partWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "part reference flush error: %v\n", err)
			}
		}()
	}
	if *clientPushOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("client push output", fmt.Errorf("-client-push-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*clientPushOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create client push directory", err)
			}
		}
		clientFile, err := os.Create(*clientPushOutPath)
		if err != nil {
			fatal("create client push output", err)
		}
		defer clientFile.Close()
		clientWriter := bufio.NewWriter(clientFile)
		processor.clientPushOut = clientWriter
		defer func() {
			if err := clientWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "client push flush error: %v\n", err)
			}
		}()
	}
	if *damagePushOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("damage push output", fmt.Errorf("-damage-push-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*damagePushOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create damage push directory", err)
			}
		}
		pushFile, err := os.Create(*damagePushOutPath)
		if err != nil {
			fatal("create damage push output", err)
		}
		defer pushFile.Close()
		pushWriter := bufio.NewWriter(pushFile)
		processor.damagePushOut = pushWriter
		defer func() {
			if err := pushWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "damage push flush error: %v\n", err)
			}
		}()
	}
	if *combatStateOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("combat state output", fmt.Errorf("-combat-state-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*combatStateOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create combat state directory", err)
			}
		}
		stateFile, err := os.Create(*combatStateOutPath)
		if err != nil {
			fatal("create combat state output", err)
		}
		defer stateFile.Close()
		stateWriter := bufio.NewWriter(stateFile)
		processor.combatStateOut = stateWriter
		defer func() {
			if err := stateWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "combat state flush error: %v\n", err)
			}
		}()
	}
	if *hpStateOutPath != "" {
		if processor.protocolSchema == nil {
			fatal("hp state output", fmt.Errorf("-hp-state-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*hpStateOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create hp state directory", err)
			}
		}
		hpFile, err := os.Create(*hpStateOutPath)
		if err != nil {
			fatal("create hp state output", err)
		}
		defer hpFile.Close()
		hpWriter := bufio.NewWriter(hpFile)
		processor.hpStateOut = hpWriter
		defer func() {
			if err := hpWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "hp state flush error: %v\n", err)
			}
		}()
	}
	if *schemaDecodeOut != "" {
		if processor.protocolSchema == nil {
			fatal("schema decode output", fmt.Errorf("-schema-decode-out requires -protocol-schema"))
		}
		if dir := filepath.Dir(*schemaDecodeOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create schema decode directory", err)
			}
		}
		schemaFile, err := os.Create(*schemaDecodeOut)
		if err != nil {
			fatal("create schema decode output", err)
		}
		defer schemaFile.Close()
		schemaWriter := bufio.NewWriter(schemaFile)
		processor.schemaDecodeOut = schemaWriter
		defer func() {
			if err := schemaWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "schema decode flush error: %v\n", err)
			}
		}()
	}
	if *roleConfigOutPath != "" {
		if processor.protocolSchema == nil || masters == nil {
			fatal("role config output", fmt.Errorf("-role-config-out requires -protocol-schema and -master-db"))
		}
		if dir := filepath.Dir(*roleConfigOutPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create role config directory", err)
			}
		}
		roleConfigFile, err := os.Create(*roleConfigOutPath)
		if err != nil {
			fatal("create role config output", err)
		}
		defer roleConfigFile.Close()
		roleConfigWriter := bufio.NewWriter(roleConfigFile)
		processor.roleConfigOut = roleConfigWriter
		defer func() {
			if err := roleConfigWriter.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "role config flush error: %v\n", err)
			}
		}()
	}
	var frameArchiveWriter *bufio.Writer
	if frameArchiveFile != nil {
		frameArchiveWriter = bufio.NewWriter(frameArchiveFile)
		processor.frameArchive = frameArchiveWriter
	}
	if *commandCensusOut != "" {
		processor.census = newCommandCensus()
	}
	// Every per-event diagnostic sink now writes from its own goroutine; the capture loop only queues records.
	diagnostics := newDiagnosticPipeline(*pcapIn == "")
	flushers := map[io.Writer]func() error{stdout: stdout.Flush}
	if frameArchiveWriter != nil {
		flushers[frameArchiveWriter] = frameArchiveWriter.Flush
	}
	restoreDiagnostics := wrapProcessorDiagnostics(processor, diagnostics, flushers)
	fmt.Fprintf(os.Stderr, "master_data_loaded database=%s sha256=%s locale=%s characters=%d monsters=%d skills=%d damage=%d\n",
		masters.databasePath, masters.manifest.SourceHash, masters.locale,
		len(masters.characters), len(masters.monsters), len(masters.skills), len(masters.damage))

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
	var pcapSink io.Writer
	if *pcapOut != "" {
		if dir := filepath.Dir(*pcapOut); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fatal("create PCAP directory", err)
			}
		}
		pcapFile, err = os.OpenFile(*pcapOut, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			fatal("create PCAP", err)
		}
		defer pcapFile.Close()
		pcapWriter = pcapgo.NewWriter(pcapFile)
		if err := pcapWriter.WriteFileHeader(65535, handle.LinkType()); err != nil {
			fatal("write PCAP header", err)
		}
		// Packet records are queued whole; a write failure disables the PCAP without stopping the analysis.
		pcapSink = diagnostics.wrap("pcap", pcapFile, nil, diagnosticPCAPCapacityBytes)
	}

	if *pcapIn == "" {
		fmt.Fprintf(os.Stderr, "WWLOGS live capture started adapter=%s filter=%q\n", device, filterExpression)
	} else {
		fmt.Fprintf(os.Stderr, "WWLOGS offline replay started file=%s filter=%q\n", *pcapIn, filterExpression)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	processor.observeCaptureSource(*pcapIn == "")
	processor.captureLayer.diagnosticDropCount = diagnostics.droppedRecords
	streams := make(map[string]*stream)
	stats := captureStats{}
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
			stats.kernelDrops = statsResult.PacketsDropped
		}
		status.State = state
		status.StopReason = reason
		status.UpdatedAt = now
		lastStatusWrite = now
		status.UDPPackets = stats.udpPackets
		status.KernelDrops = stats.kernelDrops
		status.PCAPBytes = currentPCAPBytes
		status.DiagnosticDroppedRecords = diagnostics.droppedRecords()
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
				processor.sampleCaptureStats(handle, time.Now(), false)
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
		if pcapWriter != nil {
			recordBytes := int64(16 + len(packet.Data()))
			if *maxPCAPBytes > 0 && currentPCAPBytes+recordBytes > *maxPCAPBytes {
				stopReason = "size_limit"
				goto finished
			}
			_, _ = pcapSink.Write(pcapPacketRecord(packet.Metadata().CaptureInfo, packet.Data()))
			currentPCAPBytes += recordBytes
		}
		processor.observePacketRead()
		processor.sampleCaptureStats(handle, time.Now(), false)
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		stats.udpPackets++
		direction := packetDirection(packet, *ip, udp, *minPort, *maxPort)
		consumeDatagram(udp.Payload, direction, packet.Metadata().CaptureInfo.Timestamp, streams, processor, &stats)
		writeLiveDiagnostic(time.Now(), false)
		writeStatus("RUNNING", "")
	}

finished:
	finishTransport(streams, processor, &stats)
	processor.sampleCaptureStats(handle, time.Now(), true)
	processor.finishCapture()
	processor.writeEncounterFinal()
	writeLiveDiagnostic(time.Now(), true)
	// Drain the asynchronous sinks before the synchronous end-of-run summaries use the same files directly.
	diagnostics.finishCapture()
	pipelineStats := diagnostics.close(diagnosticFlushTimeout)
	restoreDiagnostics()
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
	if frameArchiveWriter != nil {
		if err := frameArchiveWriter.Flush(); err != nil {
			fatal("flush frame archive", err)
		}
		fmt.Fprintf(os.Stderr, "frame_archive frames=%d write_errors=%d\n", processor.frameOrdinal, processor.frameArchiveErrors)
	}
	if *buffOverlapOut != "" {
		if dir := filepath.Dir(*buffOverlapOut); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fatal("create buff overlap directory", err)
			}
		}
		overlapFile, err := os.Create(*buffOverlapOut)
		if err != nil {
			fatal("create buff overlap output", err)
		}
		overlapWriter := bufio.NewWriter(overlapFile)
		if err := processor.writeBuffOverlap(overlapWriter); err != nil {
			fatal("write buff overlap", err)
		}
		if err := overlapWriter.Flush(); err != nil {
			fatal("flush buff overlap", err)
		}
		overlapFile.Close()
		fmt.Fprintf(os.Stderr, "buff_overlap damage_results=%d intervals=%d duplicate_applies=%d unmatched_removes=%d skipped=%d interval_overflow=%d damage_overflow=%d\n", len(processor.damageOverlap), len(processor.buffIntervals), processor.buffDuplicateApplies, processor.buffUnmatchedRemoves, processor.buffOverlapSkipped, processor.buffIntervalOverflow, processor.damageOverlapOverflow)
	}
	if *effectStateOut != "" {
		if dir := filepath.Dir(*effectStateOut); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fatal("create effect state directory", err)
			}
		}
		effectFile, err := os.Create(*effectStateOut)
		if err != nil {
			fatal("create effect state output", err)
		}
		if err := processor.writeEffectState(effectFile); err != nil {
			fatal("write effect state", err)
		}
		if err := effectFile.Close(); err != nil {
			fatal("close effect state output", err)
		}
		counters := processor.effects.Snapshot().Counters
		fmt.Fprintf(os.Stderr, "effect_state applies=%d duplicate_applies=%d instant_applies=%d updates=%d updates_without_apply=%d removes=%d removes_without_instance=%d holder_removals=%d excluded_unrelated=%d\n", counters.Applies, counters.DuplicateApplies, counters.InstantApplies, counters.Updates, counters.UpdatesWithoutApply, counters.Removes, counters.RemovesWithoutInstance, counters.HolderRemovals, counters.ExcludedUnrelated)
	}
	fmt.Fprintf(os.Stderr, "schema_decode errors=%d skipped_without_schema=%d\n", processor.schemaDecodeErrors, processor.schemaDecodeSkipped)
	if processor.roleConfigOut != nil {
		fmt.Fprintf(os.Stderr, "role_config errors=%d\n", processor.roleConfigErrors)
	}
	if processor.shieldRefOut != nil {
		fmt.Fprintf(os.Stderr, "shield_reference errors=%d\n", processor.shieldRefErrors)
	}
	processor.printSummary(diagnosticTarget)
	fmt.Fprintf(diagnosticTarget, "statistics_final %s\n", processor.currentStatisticsReport())
	fmt.Fprintln(diagnosticTarget, transportHealthLine(streams, stats))
	fmt.Fprintln(diagnosticTarget, diagnosticPipelineLine(pipelineStats))
	fmt.Fprintf(diagnosticTarget, "capture_finished udp=%d kcp_push=%d kcp_ordered=%d kcp_messages=%d protocol_frames=%d bad_crc=%d malformed_frames=%d kernel_drops=%d duration=%s\n",
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
