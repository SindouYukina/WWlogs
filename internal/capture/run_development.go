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

func optionalFileWriter(file *os.File) io.Writer {
	if file == nil {
		return nil
	}
	return file
}

// Main parses the capture command flags and runs live capture or PCAP replay.
func Main() {
	exitIfVersionRequested()
	ip := flag.String("ip", "", "本機網路介面的 IPv4 位址；未提供時會列出介面供選擇")
	minPort := flag.Int("min-port", 13100, "UDP 伺服器連接埠範圍下限")
	maxPort := flag.Int("max-port", 13120, "UDP 伺服器連接埠範圍上限")
	duration := flag.Duration("duration", 0, "即時擷取時間；0 表示持續執行直到按下 Ctrl+C")
	pcapIn := flag.String("pcap-in", "", "重播指定的 PCAP 檔案，不進行即時擷取")
	pcapOut := flag.String("pcap-out", "", "選擇性地將擷取到的 UDP 封包寫入 PCAP 檔案")
	masterDatabase := flag.String("master-db", defaultMasterDatabasePath(), "唯讀戰鬥 ID Master SQLite 資料庫")
	showVersion := flag.Bool("version", false, "顯示 WWlogs 版本")
	verifyBaseline := flag.Bool("verify-baseline", false, "驗證 Locking PCAP 的解碼與玩家輸出回歸基線")
	auditOut := flag.String("audit-out", "", "選擇性輸出不含祕密的 RPC、entity 與傷害配對 TSV")
	buffSnapshotOut := flag.String("buff-snapshot-out", "", "選擇性輸出 EntityActiveResponse Buff 快照 JSONL")
	buffEventOut := flag.String("buff-event-out", "", "選擇性輸出目前支援的 Buff 生命週期觀察 JSONL")
	combatNotifyAuditOut := flag.String("combat-notify-audit-out", "", "僅供離線使用：輸出已去識別化的 CombatNotifyData 欄位稽核 JSONL")
	encounterSummaryOut := flag.String("encounter-summary-out", "", "選擇性輸出附有證據標籤的 encounter 與角色摘要 Markdown")
	combatEventsJSONLOut := flag.String("event-jsonl-out", "", "選擇性輸出已配對且附證據的戰鬥與 Buff 觀察 JSONL")
	protocolSchemaPath := flag.String("protocol-schema", "", "選擇性指定本機 protocol schema JSON 檔案")

	schemaDecodeOut := flag.String("schema-decode-out", "", "選擇性輸出依 schema 解碼外層訊息的 JSONL；需要 -protocol-schema")
	roleConfigOutPath := flag.String("role-config-out", "", "選擇性輸出透過 Master 解析的 RoleConfigInfoNotify 角色 ID JSONL；需要 -protocol-schema 與 -master-db")

	buffOverlapOut := flag.String("buff-overlap-out", "", "選擇性輸出每筆傷害事件當下目標身上觀察到的 Buff 實例")
	masterLocale := flag.String("locale", "zh-TW", "Master 名稱語言：zh-TW、zh-CN、en-US、ja-JP 或 ko-KR")
	combatStateOutPath := flag.String("combat-state-out", "", "選擇性輸出屬性變化、回復值及狀態機轉換 JSONL；需要 -protocol-schema")
	hpStateOutPath := flag.String("hp-state-out", "", "選擇性輸出各 entity 的 Life 與 LifeMax JSONL；需要 -protocol-schema")
	damagePushOutPath := flag.String("damage-push-out", "", "選擇性輸出獨立於 damage RPC stream 的 DamageExecuteNotify JSONL；需要 -protocol-schema")
	skillRefOutPath := flag.String("skill-ref-out", "", "選擇性輸出戰鬥訊息中的 SkillId 與 EntitySkillPreload namespace 查核 JSONL；需要 -protocol-schema")
	bulletRefOutPath := flag.String("bullet-ref-out", "", "選擇性輸出收發戰鬥訊息中的 BulletId、namespace 與解析證據 JSONL；需要 -protocol-schema")
	shieldRefOutPath := flag.String("shield-ref-out", "", "選擇性以 shield.Id 解析 ShieldUpdateInfo.ConfigId JSONL；需要 -protocol-schema 與 -master-db")
	partRefOutPath := flag.String("part-ref-out", "", "選擇性以目標 PartComponent 解析 DamageExecuteRequest.PartId JSONL；需要 -protocol-schema")
	clientPushOutPath := flag.String("client-push-out", "", "選擇性輸出技能請求、角色回報及技能結束 push JSONL；需要 -protocol-schema")
	summonOwnerOutPath := flag.String("summon-owner-out", "", "選擇性輸出 join 與 entity-add 訊息中的 summoner、concomitant 及 dependence 事實 JSONL；需要 -protocol-schema")
	frameArchiveOut := flag.String("frame-archive-out", "", "選擇性為每個重組 frame 輸出本機 JSONL；僅保存 digest，不含金鑰或明文")
	research := flag.Bool("research", false, "將逐事件證據細節寫至標準錯誤")
	commandCensusOut := flag.String("command-census-out", "", "輸出本機 protocol command 與解碼結果 JSON")
	clientVersionOut := flag.String("client-version-out", "", "輸出首次 handshake 後觀察到的版本 metadata")
	preflight := flag.Bool("preflight", false, "檢查指定 Npcap 介面是否可開啟並套用 filter")
	filter := flag.String("filter", "", "選擇性指定 BPF filter；預設使用設定的 UDP 連接埠範圍")
	stopFile := flag.String("stop-file", "", "指定檔案出現時正常停止即時擷取")
	statusFile := flag.String("status-file", "", "將即時擷取狀態 JSON 寫入指定路徑")
	maxPCAPBytes := flag.Int64("max-pcap-bytes", 0, "限制即時 PCAP 大小；達到上限前停止")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "WWlogs %s\n用法：WWlogs [選項]\n\n選項：\n", versioninfo.Current().Product.DevelopmentLine)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Printf("WWlogs 版本：%s\n", versioninfo.Current().Product.DevelopmentLine)
		return
	}
	fmt.Printf("WWlogs %s\n", versioninfo.Current().Product.DevelopmentLine)
	announceBuildIdentity(os.Stderr)
	if *ip == "" && *pcapIn == "" {
		selectedIP, err := promptLocalIPv4(os.Stdin, os.Stdout)
		if err != nil {
			fatal("選擇網路介面", err)
		}
		*ip = selectedIP
	}

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
	fmt.Fprintf(os.Stderr, "schema_decode errors=%d skipped_without_schema=%d\n", processor.schemaDecodeErrors, processor.schemaDecodeSkipped)
	if processor.roleConfigOut != nil {
		fmt.Fprintf(os.Stderr, "role_config errors=%d\n", processor.roleConfigErrors)
	}
	if processor.shieldRefOut != nil {
		fmt.Fprintf(os.Stderr, "shield_reference errors=%d\n", processor.shieldRefErrors)
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
