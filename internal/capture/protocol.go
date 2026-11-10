package capture

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

type gameFrame struct {
	direction        string
	messageType      byte
	sequence         uint32
	token            uint16
	commandID        uint16
	compressed       bool
	compressedLength uint32
	body             []byte
	timestamp        time.Time
}

type applicationSpan struct {
	length    int
	timestamp time.Time
}

type protoField struct {
	number uint64
	wire   uint64
	value  uint64
	data   []byte
}

type pendingDamageRequest struct {
	attackerEntityID    uint64 // DamageExecuteRequest.AttackerEntityId, field 13
	hasAttackerEntityID bool
	targetEntityID      uint64 // DamageExecuteRequest.TargetEntityId, field 10
	hasTargetEntityID   bool
	partID              int32 // DamageExecuteRequest.PartId, field 5
	hasPartID           bool
	skillLevel          uint64 // DamageExecuteRequest.SkillLevel, field 12
	skillID             uint64 // DamageContext.SkillId, field 4
	skillMessageID      uint64 // DamageContext.SkillMessageId, field 5
	damageID            uint64 // DamageExecuteRequest.DamageId, field 9
	hasDamageID         bool
	damageSourceType    uint64 // DamageContext.SourceType, field 1
	hasDamageSourceType bool
	bulletID            int64 // DamageContext.BulletId, field 2
	hasBulletID         bool
	sequence            uint64
}

type pendingRef struct {
	rpcID    uint64
	sequence uint64
}

type consumedRef struct {
	rpcID    uint64
	sequence uint64
}

type pendingActiveRequest struct {
	entityID    uint64
	hasEntityID bool
}

type processorStats struct {
	handshakeFrames                  int
	handshakeOK                      int
	handshakeErrors                  int
	damageRequests                   int
	candidateResponses               int
	printed                          int
	unpairedResponses                int
	invalidResponses                 int
	duplicateResponses               int
	missingAttackerEntityID          int
	evictedPending                   int
	entityAdds                       int
	entityRemoves                    int
	entityActiveRequests             int
	observedSourceEntity             int
	observedTargetEntity             int
	characterResolved                int
	verifiedCharacters               int
	correlatedCharacters             int
	monsterResolved                  int
	skillResolved                    int
	unknownDamageEvents              int
	damageCandidateEvents            int
	damageCandidateResult            int64
	healingCandidateEvents           int
	otherCandidateEvents             int
	otherCandidateResult             int64
	unclassifiedEvents               int
	unclassifiedResult               int64
	observedBuffEvents               int
	verifiedBuffEvents               int
	buffSnapshotResponses            int
	activeResponseIdentityComponents int
	activeResponseSummonerComponents int
	activeResponseMonsterComponents  int
	activeResponseFollowerComponents int
	decryptErrors                    int
	compressedErrors                 int
	parseErrors                      int
	otherFrameDirections             int
	matchedCombatResultAbs           int64
	decodedMatchedEvents             int
	decodedCombatResultAbs           int64
	playerRelatedEvents              int
	includedUnresolvedRelations      int
	suppressedUnrelatedEvents        int
	playerRoleReports                int
	playerRoleReportParseErrors      int
	healingCandidateResult           int64
}

type processor struct {
	rsa                       *staticRSA
	session                   []byte
	sessionGeneration         uint64
	sessionHealth             sessionHealthState
	pending                   map[uint64]pendingDamageRequest
	pendingOrder              []pendingRef
	pendingHead               int
	pendingSequence           uint64
	consumed                  map[uint64]uint64
	consumedOrder             []consumedRef
	consumedSequence          uint64
	entities                  *entityResolver
	sourceRoles               *sourceRoleResolver
	playerRelevance           *playerRelevanceResolver
	masters                   *gameMaster
	research                  bool
	encounterSummaries        map[encounterSummaryKey]*encounterSummary
	pendingSummaryCredits     map[uint64][]pendingSummaryCredit
	pendingSummaryCreditCount int
	currentFrameTime          time.Time
	viewClockLead             time.Duration // how far past currentFrameTime the Effects view reads countdowns; only a live session sets it
	viewClockStalled          bool          // a live session has seen no game frame for LiveFrameSilenceLimit
	clientVersion             *clientVersionReport
	stdout                    io.Writer
	diagnostic                io.Writer
	showEffectEvents          bool
	showAllEffectEvents       bool
	audit                     io.Writer
	encounterSummary          diagWriter
	combatEventsJSONL         diagWriter
	buffSnapshots             diagWriter
	buffLifecycle             diagWriter
	combatNotifyAudit         diagWriter
	frameArchive              diagWriter
	frameOrdinal              uint64
	frameArchiveErrors        int
	protocolSchema            *protocolSchema
	schemaDecodeOut           diagWriter
	schemaDecodeErrors        int
	schemaDecodeSkipped       int
	partRefOut                diagWriter
	partRefErrors             int
	roleConfigOut             diagWriter
	roleConfigErrors          int
	eventOrderFrame           uint64
	auxEventOrders            map[string]*auxEventOrder
	eventOrder                int
	hpStateOut                diagWriter
	damagePushOut             diagWriter
	clientPushOut             diagWriter
	bulletRefOut              diagWriter
	bulletRefErrors           int
	shieldRefOut              diagWriter
	shieldRefErrors           int
	skillRefOut               diagWriter
	skillRefErrors            int
	bulletHandles             map[bulletHandleKey]bulletHandleEntry
	buffHandles               map[buffHandleKey]buffHandleEntry
	buffHandleOrder           []buffHandleKey
	serverBuffRequests        map[serverBuffRequestKey]serverBuffRequest
	serverBuffRequestOrder    []serverBuffRequestKey
	bulletHandleOrder         []bulletHandleKey
	combatActionRequests      map[combatActionRequestKey]combatActionRequest
	combatActionOrder         []combatActionRequestKey
	flowTime                  int64
	hasFlowTime               bool
	flowTimeFrame             uint64
	timerObservations         []timerObservation
	summonOwnerOut            diagWriter
	summonOwnerErrors         int
	clientPushErrors          int
	activeRoleID              int64
	hasActiveRole             bool
	damagePushErrors          int
	hpStateErrors             int
	combatStateOut            diagWriter
	combatStateErrors         int
	buffIntervals             []*buffInterval
	buffIntervalOpen          map[overlapKey]*buffInterval
	buffDuplicateApplies      int
	buffUnmatchedRemoves      int
	buffIntervalOverflow      int
	buffOverlapSkipped        int
	damageOverlap             []damageOverlapRecord
	damageOverlapOverflow     int
	activeRequests            map[uint16]pendingActiveRequest
	stats                     processorStats
	damageStatistics          *DamageStatistics
	effects                   *EffectTracker
	census                    *commandCensus
	sessionNumber             int
	encounterOut              diagWriter
	// scenePlayerIDs lists the players of the current scene; it anchors battle-state signals before the
	// explicit local-player identity does.
	scenePlayerIDs []uint64
	// kcpStreams is the capture loop's KCP stream table, read for transport health snapshots.
	kcpStreams   map[string]*stream
	transport    transportMonitor
	context      contextValidity
	captureLayer captureLayerState
	pairing      rpcPairingState
	// intervalPerProtocolSession starts a new statistics interval at every game handshake after the first, so one
	// interval never mixes protocol sessions. Application hosts set it; the CLI keeps one interval per capture.
	intervalPerProtocolSession bool
}

func newProcessor(rsaKey *staticRSA, stdout, diagnostic, audit io.Writer) *processor {
	statistics := NewDamageStatistics()
	return &processor{
		rsa:                  rsaKey,
		pending:              make(map[uint64]pendingDamageRequest),
		consumed:             make(map[uint64]uint64),
		activeRequests:       make(map[uint16]pendingActiveRequest),
		combatActionRequests: make(map[combatActionRequestKey]combatActionRequest),
		entities:             newEntityResolver(),
		sourceRoles:          newSourceRoleResolver(),
		playerRelevance:      newPlayerRelevanceResolver(),
		damageStatistics:     statistics,
		effects:              NewEffectTracker(statistics.captureSessionID),
		stdout:               stdout,
		diagnostic:           diagnostic,
		audit:                audit,
	}
}

// frameParseResult reports one pass over an application byte stream. Desync is set when a frame candidate failed its
// length, header, or CRC checks: the frame boundary can no longer be trusted, so parsing stops one byte past the bad
// candidate and the caller resynchronizes instead of trusting the declared length (see resynchronizeStream).
type frameParseResult struct {
	frames, badCRC, malformed, consumed int
	desync                              bool
	desyncReason                        string
}

// parseFrames parses complete frames from buffer. It stops at the first framing failure; see frameParseResult.
func parseFrames(buffer []byte, direction string, spans []applicationSpan, p *processor) (int, int, int, int) {
	result := parseFrameStream(buffer, direction, spans, p, nil)
	return result.frames, result.badCRC, result.malformed, result.consumed
}

func parseFrameStream(buffer []byte, direction string, spans []applicationSpan, p *processor, state *stream) frameParseResult {
	var result frameParseResult
	applicationOffset := 0
	var previousValid *frameBoundaryObservation
	var localCRCIndexes []int
	pendingCRCIndexes := &localCRCIndexes
	if state != nil {
		pendingCRCIndexes = &state.pendingCRCCensus
	}
	desync := func(reason string) frameParseResult {
		result.desync, result.desyncReason = true, reason
		result.consumed++
		return result
	}
	for len(buffer) >= 3 {
		consumed := result.consumed
		timestamp := applicationTimestampAt(spans, consumed)
		payloadLen := int(binary.LittleEndian.Uint16(buffer[:2]))
		total := payloadLen + 3
		if total < 14 {
			result.malformed++
			if p.census != nil {
				p.census.malformed("short_or_unaligned_frame_candidate", timestamp, applicationOffset, uint16(payloadLen), len(buffer))
			}
			return desync(frameDesyncMalformed)
		}
		if len(buffer) < total {
			break
		}
		frame := buffer[:total]
		messageType := frame[3] & 0x0f
		compressed := frame[3]&0x10 != 0
		offset := 8
		if messageType != 4 {
			offset += 2
		}
		if offset+6 > total {
			result.malformed++
			if p.census != nil {
				p.census.malformed("truncated_command_header", timestamp, applicationOffset, uint16(payloadLen), total)
			}
			return desync(frameDesyncMalformed)
		}
		commandID := binary.LittleEndian.Uint16(frame[offset : offset+2])
		receivedCRC := binary.LittleEndian.Uint32(frame[offset+2 : offset+6])
		bodyStart := offset + 6
		var compressedLength uint32
		if compressed {
			if bodyStart+4 > total {
				result.malformed++
				if p.census != nil {
					p.census.malformed("truncated_compression_length", timestamp, applicationOffset, uint16(payloadLen), total)
				}
				return desync(frameDesyncMalformed)
			}
			compressedLength = binary.LittleEndian.Uint32(frame[bodyStart : bodyStart+4])
			bodyStart += 4
		}
		computedCRC := crc32IEEE(frame[bodyStart:total])
		if computedCRC != receivedCRC {
			if p.census != nil {
				var token uint16
				if messageType != 4 {
					token = binary.LittleEndian.Uint16(frame[8:10])
				}
				observation := crcFailureObservation{
					Direction: direction, Timestamp: timestamp, Offset: applicationOffset,
					MessageType: messageType, Sequence: binary.LittleEndian.Uint32(frame[4:8]),
					Token: token, CommandID: commandID, DeclaredLength: uint16(payloadLen),
					FrameLength: total, BodyLength: total - bodyStart,
					ReceivedCRC: receivedCRC, ComputedCRC: computedCRC, RemainingBytes: len(buffer) - total,
					PreviousValid: previousValid,
				}
				if index := p.census.failedCRC(observation); index >= 0 {
					*pendingCRCIndexes = append(*pendingCRCIndexes, index)
				}
			}
			result.badCRC++
			return desync(frameDesyncCRC)
		}
		var token uint16
		if messageType != 4 {
			token = binary.LittleEndian.Uint16(frame[8:10])
		}
		current := gameFrame{
			direction:        direction,
			messageType:      messageType,
			sequence:         binary.LittleEndian.Uint32(frame[4:8]),
			token:            token,
			commandID:        commandID,
			compressed:       compressed,
			compressedLength: compressedLength,
			body:             append([]byte(nil), frame[bodyStart:total]...),
			timestamp:        applicationTimestampAt(spans, consumed),
		}
		boundary := frameBoundaryObservation{Offset: applicationOffset, MessageType: messageType, Sequence: current.sequence, Token: token, CommandID: commandID, FrameLength: total}
		if p.census != nil {
			for _, index := range *pendingCRCIndexes {
				p.census.setNextValidFrame(index, boundary)
			}
		}
		*pendingCRCIndexes = nil
		if state != nil {
			state.lastFrameSequence, state.hasFrameSequence = current.sequence, true
		}
		p.handleFrame(current)
		result.frames++
		previousValid = &boundary
		buffer = buffer[total:]
		result.consumed += total
		applicationOffset += total
	}
	return result
}

// Reasons a frame parse lost its boundary.
const (
	frameDesyncCRC       = "frame_crc_mismatch"
	frameDesyncMalformed = "frame_header_malformed"
)

func applicationTimestampAt(spans []applicationSpan, offset int) time.Time {
	for _, span := range spans {
		if offset < span.length {
			return span.timestamp
		}
		offset -= span.length
	}
	return time.Time{}
}

func crc32IEEE(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// isImplementedCommand reports whether the outer command has a decoder path in
// handleFrame. Other commands are only counted and inspected by the census.
func isImplementedCommand(commandID uint16) bool {
	switch commandID {
	case 103, 116, 15380, 19470, 20332, 25706, 17250, 17286, 18559, 25185, 17769, 28030, 28481, 26702, 25830, 18345, 20358, 17893, 22102, 29363, 16974:
		return true
	}
	return false
}

func (p *processor) handleFrame(frame gameFrame) {
	p.archiveFrame(frame)
	p.currentFrameTime = frame.timestamp
	p.observePairingClock(frame.timestamp)
	p.observeEncounterClock(frame.timestamp)
	p.sessionHealth.observeFrame(frame.timestamp)
	var censusEntry *commandCensusEntry
	if p.census != nil {
		censusEntry = p.census.observeFrame(frame, p.sessionNumber)
	}
	if frame.commandID == 112 && frame.direction == "in" {
		p.stats.handshakeFrames++
		if err := p.handleProtoKeyResponse(frame.body); err != nil {
			p.stats.handshakeErrors++
			p.sessionHealth.lastHandshakeFailed = true
			if censusEntry != nil {
				censusEntry.FailureReasons["ProtoKeyResponse decode failed"]++
			}
			return
		}
		p.stats.handshakeOK++
		p.sessionHealth.lastHandshakeFailed = false
		p.sessionHealth.consecutiveDecryptFailures = 0
		if censusEntry != nil {
			censusEntry.DecodedFrames++
		}
		p.sessionNumber++
		p.playerRelevance.reset()
		p.scenePlayerIDs = nil
		p.observeContextSessionStart(frame.timestamp)
		p.startProtocolSessionStatistics(frame.timestamp)
		if p.census != nil && p.sessionNumber > p.census.sessions {
			p.census.sessions = p.sessionNumber
		}
		return
	}
	if !isImplementedCommand(frame.commandID) {
		if censusEntry != nil {
			p.inspectUndecodedFrame(frame, censusEntry)
		} else {
			p.archiveUndecodedSchema(frame)
		}
		return
	}
	if ((frame.commandID == 103 || frame.commandID == 116 || frame.commandID == 19470 || frame.commandID == 28481) && frame.direction != "out") || ((frame.commandID == 15380 || frame.commandID == 20332 || frame.commandID == 25706 || frame.commandID == 17250 || frame.commandID == 17286 || frame.commandID == 18559 || frame.commandID == 25185 || frame.commandID == 17769 || frame.commandID == 28030 || frame.commandID == 26702 || frame.commandID == 25830 || frame.commandID == 18345 || frame.commandID == 20358 || frame.commandID == 17893 || frame.commandID == 22102 || frame.commandID == 29363 || frame.commandID == 16974) && frame.direction != "in") {
		p.stats.otherFrameDirections++
		if censusEntry != nil {
			censusEntry.FailureReasons["direction differs from implemented schema"]++
		}
		return
	}
	if len(p.session) != 32 {
		if censusEntry != nil {
			censusEntry.FailureReasons["session key unavailable"]++
		}
		return
	}
	if len(frame.body) == 0 {
		// Zero-length bodies carry no ciphertext; counting them as AES failures
		// would misreport transport health.
		if censusEntry != nil {
			censusEntry.FailureReasons[emptyBodyFailureReason]++
		}
		return
	}
	plain, err := decryptGameBody(frame.sequence, frame.body, p.session)
	if err != nil {
		p.stats.decryptErrors++
		p.sessionHealth.consecutiveDecryptFailures++
		if censusEntry != nil {
			censusEntry.FailureReasons["decrypt failed"]++
		}
		return
	}
	p.sessionHealth.consecutiveDecryptFailures = 0
	if censusEntry != nil {
		censusEntry.DecryptedFrames++
	}
	if frame.compressed {
		plain, err = decompressBody(plain, frame.compressedLength)
		if err != nil {
			p.stats.compressedErrors++
			if censusEntry != nil {
				censusEntry.FailureReasons["decompression failed"]++
			}
			return
		}
	}
	if censusEntry != nil {
		censusEntry.observeKnownBodyWire(plain)
	}
	p.archiveSchemaDecode(frame, plain)
	p.archiveDamageRPCSchema(frame, plain)
	p.archiveHPStateFrame(frame, plain)
	p.archiveCombatStateFrame(frame, plain)
	p.archiveDamagePushFrame(frame, plain)
	p.archiveClientPushFrame(frame, plain)
	p.archiveBulletRefReceiveFrame(frame, plain)
	p.archiveSummonOwnerFrame(frame, plain)
	parseErrorsBefore := p.stats.parseErrors
	switch frame.commandID {
	case 15380:
		if err := p.observeLocalPlayerInfo(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "local_player_info_parse_error=%v\n", err)
			return
		}
	case 103, 116:
		if err := p.observeClientVersion(frame.commandID, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "client_version_parse_error command=%d reason=%v\n", frame.commandID, err)
			return
		}
	case 19470:
		p.handleDamageRequests(frame, plain)
		if err := p.handleClientEffectPushes(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "client_effect_push_parse_error=%v\n", err)
		}
	case 20332:
		p.handleDamageResponses(plain)
		if err := p.handleEmbeddedCombatReceivePackNotify(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "combat_response_notify_parse_error=%v\n", err)
			return
		}
	case 17250, 28030:
		// TimeCheckResponse and UpdateTimerInfoNotify are interpreted by the
		// schema-directed observer to preserve their observed field provenance.
	case 25706:
		if err := p.handleCombatReceivePackNotify(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "combat_receive_pack_parse_error=%v\n", err)
			return
		}
	case 17286:
		if _, err := p.entities.applyUpdateSceneGroupDataNotify(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "scene_group_update_parse_error=%v\n", err)
			return
		}
		p.playerRelevance.reconcileLocalPlayers(p.entities)
		p.refreshEffectRelations()
	case 25830, 18345, 20358, 17893, 22102, 29363, 16974:
		if err := p.handleBuffLifecycleMessage(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "buff_lifecycle_parse_error=%v\n", err)
			return
		}
	case 18559:
		if _, err := p.entities.applyJoinSceneNotify(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "join_scene_parse_error=%v\n", err)
			return
		}
		p.playerRelevance.resetSceneEntities()
		p.observeContextSceneJoin(frame.timestamp)
		p.observeSceneJoin(plain, frame.timestamp)
		p.effects.EndLiveState()
		p.backfillAvailableSummaryCredits()
		p.sourceRoles.reset()
		p.resetPendingSummaryCredits()
	case 25185:
		added, err := p.entities.applyEntityAddNotify(plain)
		if err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_add_parse_error=%v\n", err)
			return
		}
		p.stats.entityAdds += added
		p.refreshEffectRelations()
	case 17769:
		if err := p.handleEntityRemoveNotify(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_remove_parse_error=%v\n", err)
			return
		}
	case 28481:
		if err := p.handleActiveRequest(frame.token, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_active_parse_error=%v\n", err)
			return
		}
		p.stats.entityActiveRequests++
	case 26702:
		if err := p.handleActiveResponse(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_active_response_parse_error=%v\n", err)
			return
		}
	}
	if censusEntry != nil {
		if parseErrorsBefore == p.stats.parseErrors {
			censusEntry.DecodedFrames++
		} else {
			censusEntry.FailureReasons["protobuf or message schema decode failed"]++
		}
	}
}

// handleEntityRemoveNotify applies an EntityRemoveNotify. A held damage response may name an entity removed here, so the
// state an in-order pair would have seen is kept first (see rpc_pairing.go). On a parse error nothing but the registry
// removal of the IDs read before the fault is applied.
func (p *processor) handleEntityRemoveNotify(body []byte) error {
	p.rememberRemovalsForHeldResponses(body)
	removed, removedIDs, err := p.entities.applyEntityRemoveNotify(body)
	if err != nil {
		return err
	}
	p.stats.entityRemoves += removed
	for _, id := range removedIDs {
		p.effects.ObserveHolderRemoved(p.sessionNumber, id, p.currentFrameTime)
		p.playerRelevance.removeEntity(id)
		p.settleRemovedSource(id)
		p.sourceRoles.remove(id)
	}
	return nil
}

// emptyBodyFailureReason labels frames whose command body is zero-length.
const emptyBodyFailureReason = "empty body; no payload to decrypt"

func (p *processor) inspectUndecodedFrame(frame gameFrame, entry *commandCensusEntry) {
	if len(p.session) != 32 {
		entry.FailureReasons["no implemented schema; session key unavailable"]++
		return
	}
	if len(frame.body) == 0 {
		entry.FailureReasons[emptyBodyFailureReason]++
		return
	}
	plain, err := decryptGameBody(frame.sequence, frame.body, p.session)
	if err != nil {
		entry.FailureReasons["no implemented schema; decrypt failed"]++
		return
	}
	entry.DecryptedFrames++
	if frame.compressed {
		plain, err = decompressBody(plain, frame.compressedLength)
		if err != nil {
			entry.FailureReasons["no implemented schema; decompression failed"]++
			return
		}
	}
	if fields, err := parseProtoFields(plain); err == nil {
		entry.GenericProtoValid++
		p.archiveSchemaDecode(frame, plain)
		entry.observeTopLevelFields(fields)
		entry.FailureReasons["message schema not implemented"]++
		if frame.commandID == 24756 {
			observation, err := extractMovePackageObservation(plain)
			if err != nil {
				entry.FailureReasons["MovePackagePush schema-field extraction failed"]++
			} else {
				p.census.observeMovePackage(observation)
			}
		} else if frame.commandID == 27918 {
			observation, err := extractPlayerFollowerObservation(plain)
			if err != nil {
				entry.FailureReasons["PlayerFollowerNotify schema-field extraction failed"]++
			} else {
				p.census.observePlayerFollower(observation)
			}
		} else if fieldNumber, ok := entityNotifyKeyField(frame.commandID); ok {
			values, err := extractVarintField(plain, fieldNumber)
			if err != nil {
				entry.FailureReasons["entity notify ID extraction failed"]++
			} else {
				p.census.observeEntityKey(frame.commandID, fieldNumber, values)
			}
		}
	} else {
		entry.FailureReasons["no implemented schema; generic protobuf parse failed"]++
	}
}

func entityNotifyKeyField(commandID uint16) (uint64, bool) {
	switch commandID {
	case 19212:
		return 5, true // EntityRemoveDynamicInteractNotify.EntityId
	case 19566:
		return 5, true // EntityCommonTagNotify.Id
	case 23517:
		return 1, true // EntityStateReadyNotify.EntityId
	default:
		return 0, false
	}
}

func (p *processor) handleProtoKeyResponse(body []byte) error {
	fields, err := parseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse ProtoKeyResponse: %w", err)
	}
	var responseType uint64
	var encrypted []byte
	for _, field := range fields {
		if field.number == 2 && field.wire == 0 {
			responseType = field.value
		}
		if field.number == 3 && field.wire == 2 {
			encrypted = field.data
		}
	}
	if responseType != 2 {
		return fmt.Errorf("ProtoKeyResponse type %d is not AES mode", responseType)
	}
	key, err := p.rsa.decryptSessionKey(encrypted)
	if err != nil {
		return fmt.Errorf("unwrap ProtoKeyResponse key: %w", err)
	}
	if len(p.session) > 0 {
		clear(p.session)
	}
	p.session = key
	p.pending = make(map[uint64]pendingDamageRequest)
	p.pendingOrder = nil
	p.pendingHead = 0
	p.pendingSequence = 0
	p.consumed = make(map[uint64]uint64)
	p.consumedOrder = nil
	p.consumedSequence = 0
	// RPC ids restart with the session: a held response can never pair with a request of the new session.
	p.discardHeldResponses(&p.pairing.sessionChange)
	p.entities.reset()
	p.playerRelevance.reset()
	p.effects.EndLiveState()
	p.backfillAvailableSummaryCredits()
	p.sourceRoles.reset()
	p.resetPendingSummaryCredits()
	p.activeRequests = make(map[uint16]pendingActiveRequest)
	p.flowTime = 0
	p.hasFlowTime = false
	p.flowTimeFrame = 0
	p.sessionGeneration++
	fmt.Fprintln(p.diagnostic, "session_key_ready bytes=32 value=redacted")
	return nil
}

func (p *processor) handleDamageRequests(frame gameFrame, body []byte) {
	p.expireHeldResponses()
	outer, err := parseProtoFields(body)
	if err != nil {
		p.stats.parseErrors++
		return
	}
	for _, field := range outer {
		if field.number != 14 || field.wire != 2 { // sZn.VFs
			continue
		}
		variants, err := parseProtoFields(field.data)
		if err != nil {
			p.stats.parseErrors++
			continue
		}
		for _, variant := range variants {
			if variant.number != 3 || variant.wire != 2 { // VFs.FFs RequestData; field 5 is U3n
				continue
			}
			requestFields, err := parseProtoFields(variant.data)
			if err != nil {
				p.stats.parseErrors++
				continue
			}
			var rpcID uint64
			var hasRPC bool
			var u3n []byte
			for _, requestField := range requestFields {
				if requestField.number == 2 && requestField.wire == 0 {
					rpcID, hasRPC = requestField.value, true
				}
				if requestField.number == 5 && requestField.wire == 2 {
					u3n = requestField.data
				}
			}
			if !hasRPC || u3n == nil {
				continue
			}
			damageFields, err := parseProtoFields(u3n)
			if err != nil {
				p.stats.parseErrors++
				continue
			}
			var damageIDPresent bool
			identity := pendingDamageRequest{}
			for _, damageField := range damageFields {
				switch {
				case damageField.number == 9 && damageField.wire == 0: // U3n.Fjn DamageId
					damageIDPresent = true
					identity.damageID = damageField.value
					identity.hasDamageID = true
				case damageField.number == 10 && damageField.wire == 0: // DamageExecuteRequest.TargetEntityId
					identity.targetEntityID = damageField.value
					identity.hasTargetEntityID = true
				case damageField.number == 5 && damageField.wire == 0: // DamageExecuteRequest.PartId
					identity.partID = int32(uint32(damageField.value))
					identity.hasPartID = true
				case damageField.number == 12 && damageField.wire == 0: // DamageExecuteRequest.SkillLevel
					identity.skillLevel = damageField.value
				case damageField.number == 13 && damageField.wire == 0: // DamageExecuteRequest.AttackerEntityId
					identity.attackerEntityID = damageField.value
					identity.hasAttackerEntityID = true
				case damageField.number == 15 && damageField.wire == 2: // DamageExecuteRequest.DamageContext
					context, err := parseProtoFields(damageField.data)
					if err != nil {
						p.stats.parseErrors++
						continue
					}
					for _, contextField := range context {
						if contextField.wire != 0 {
							continue
						}
						switch contextField.number {
						case 1: // DamageContext.SourceType
							identity.damageSourceType = contextField.value
							identity.hasDamageSourceType = true
						case 2: // DamageContext.BulletId
							identity.bulletID = int64(contextField.value)
							identity.hasBulletID = true
						case 4: // DamageContext.SkillId
							identity.skillID = contextField.value
						case 5: // DamageContext.SkillMessageId
							identity.skillMessageID = contextField.value
						}
					}
				}
			}
			if !damageIDPresent {
				continue
			}
			if p.census != nil {
				p.census.innerEntries["22022 Client->Server DamageExecuteRequest"]++
			}
			p.stats.damageRequests++
			if _, alreadyConsumed := p.consumed[rpcID]; alreadyConsumed {
				fmt.Fprintf(p.diagnostic, "rpc_id_reused_after_completion rpc_id=%d\n", rpcID)
			}
			if _, exists := p.pending[rpcID]; exists {
				fmt.Fprintf(p.diagnostic, "duplicate_pending_damage_rpc rpc_id=%d\n", rpcID)
			}
			if p.masters != nil && identity.hasAttackerEntityID {
				if skill, ok := p.masters.skills[identity.skillID]; ok {
					p.sourceRoles.observe(identity.attackerEntityID, skill)
				}
			}
			p.writePartReference(frame, rpcID, identity)
			if identity.hasAttackerEntityID || identity.hasTargetEntityID {
				p.context.observeClientReference(identity.attackerEntityID, identity.targetEntityID)
			}
			p.rememberPending(rpcID, identity)
			// A response that arrived before this request (the outbound stream was delayed) is paired now.
			p.pairHeldResponse(rpcID)
		}
	}
}

const (
	maxPendingDamageRequests = 4096
	maxConsumedRPCIDs        = 8192
)

func (p *processor) rememberPending(rpcID uint64, request pendingDamageRequest) {
	p.pendingSequence++
	request.sequence = p.pendingSequence
	p.pending[rpcID] = request
	p.pendingOrder = append(p.pendingOrder, pendingRef{rpcID: rpcID, sequence: request.sequence})
	for len(p.pending) > maxPendingDamageRequests && p.pendingHead < len(p.pendingOrder) {
		ref := p.pendingOrder[p.pendingHead]
		p.pendingHead++
		if current, ok := p.pending[ref.rpcID]; ok && current.sequence == ref.sequence {
			delete(p.pending, ref.rpcID)
			p.stats.evictedPending++
		}
	}
	p.compactPendingOrder()
}

func (p *processor) compactPendingOrder() {
	if p.pendingHead < 1024 || p.pendingHead*2 < len(p.pendingOrder) {
		return
	}
	p.pendingOrder = append([]pendingRef(nil), p.pendingOrder[p.pendingHead:]...)
	p.pendingHead = 0
}

func (p *processor) rememberConsumed(rpcID uint64) {
	p.consumedSequence++
	p.consumed[rpcID] = p.consumedSequence
	p.consumedOrder = append(p.consumedOrder, consumedRef{rpcID: rpcID, sequence: p.consumedSequence})
	for len(p.consumed) > maxConsumedRPCIDs && len(p.consumedOrder) > 0 {
		ref := p.consumedOrder[0]
		p.consumedOrder = p.consumedOrder[1:]
		if p.consumed[ref.rpcID] == ref.sequence {
			delete(p.consumed, ref.rpcID)
		}
	}
}

func (p *processor) handleDamageResponses(body []byte) {
	p.expireHeldResponses()
	outer, err := parseProtoFields(body)
	if err != nil {
		p.stats.parseErrors++
		return
	}
	for _, field := range outer {
		if field.number != 14 || field.wire != 2 { // aZn.hZn
			continue
		}
		batches, err := parseProtoFields(field.data)
		if err != nil {
			p.stats.parseErrors++
			continue
		}
		for _, batch := range batches {
			if batch.number != 5 || batch.wire != 2 { // hZn.$Fs batch
				continue
			}
			items, err := parseProtoFields(batch.data)
			if err != nil {
				p.stats.parseErrors++
				continue
			}
			for _, item := range items {
				if item.number != 3 || item.wire != 2 { // $Fs.QLs ResponseData
					continue
				}
				response, err := parseProtoFields(item.data)
				if err != nil {
					p.stats.parseErrors++
					continue
				}
				var rpcID uint64
				var hasRPC bool
				var lis []byte
				for _, responseField := range response {
					if responseField.number == 2 && responseField.wire == 0 {
						rpcID, hasRPC = responseField.value, true
					}
					if responseField.number == 5 && responseField.wire == 2 {
						lis = responseField.data
					}
				}
				if !hasRPC || lis == nil {
					continue
				}
				p.stats.candidateResponses++
				if p.census != nil {
					p.census.innerEntries["22022 Server->Client DamageExecuteResponse"]++
				}
				p.handleOneDamageResult(rpcID, lis)
			}
		}
	}
}

func (p *processor) handleOneDamageResult(rpcID uint64, payload []byte) {
	if _, already := p.consumed[rpcID]; already {
		p.stats.duplicateResponses++
		return
	}
	request, matched := p.pending[rpcID]
	if !matched {
		// The request may still be on its way on the other stream; hold the response instead of dropping it.
		p.holdDamageResponse(rpcID, payload)
		return
	}
	p.pairing.paired++
	fields, err := parseProtoFields(payload)
	if err != nil {
		p.stats.parseErrors++
		return
	}
	var amount int32
	var hasAmount bool
	var changeLife int32
	var hasChangeLife bool
	var errorCode int32
	var immuneType int32
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case 4: // lis.Q4n
			errorCode = int32(uint32(field.value))
		case 5: // lis.lAs
			immuneType = int32(uint32(field.value))
		case 7: // lis.jQ_, client-mapped ChangeLife
			changeLife = int32(uint32(field.value))
			hasChangeLife = true
		case 10: // lis.nAs
			amount = int32(uint32(field.value))
			hasAmount = true
		}
	}
	if !hasAmount || errorCode != 0 || immuneType != 0 {
		p.stats.invalidResponses++
		p.rememberConsumed(rpcID)
		delete(p.pending, rpcID)
		p.writeNoAmountEvent(rpcID, request, hasAmount, errorCode, immuneType, hasChangeLife, changeLife)
		return
	}

	p.rememberConsumed(rpcID)
	delete(p.pending, rpcID)
	value := int64(amount)
	if value < 0 {
		value = -value
	}
	attackerStatus := resolutionUnknown
	if request.hasAttackerEntityID && request.attackerEntityID != 0 {
		_, attackerStatus = p.entities.resolve(request.attackerEntityID)
		if attackerStatus == resolutionVerified || attackerStatus == resolutionCorrelated {
			p.stats.observedSourceEntity++
		}
	}
	targetStatus := resolutionUnknown
	if request.hasTargetEntityID && request.targetEntityID != 0 {
		_, targetStatus = p.entities.resolve(request.targetEntityID)
		if targetStatus == resolutionVerified || targetStatus == resolutionCorrelated {
			p.stats.observedTargetEntity++
		}
	}
	if !request.hasAttackerEntityID || request.attackerEntityID == 0 {
		p.stats.missingAttackerEntityID++
	}
	event := p.buildDamageEvent(rpcID, request, amount, changeLife, hasChangeLife, value)
	switch event.EventTypeCandidate {
	case "DAMAGE_CANDIDATE":
		p.stats.damageCandidateEvents++
		p.stats.damageCandidateResult += value
	case "HEALING_CANDIDATE":
		p.stats.healingCandidateEvents++
		p.stats.healingCandidateResult += value
	case "OTHER_COMBAT_RESULT_CANDIDATE":
		p.stats.otherCandidateEvents++
		p.stats.otherCandidateResult += value
	default:
		p.stats.unclassifiedEvents++
		p.stats.unclassifiedResult += value
	}
	if event.CharacterStatus == resolutionVerified {
		p.stats.verifiedCharacters++
		p.stats.characterResolved++
	} else if event.CharacterStatus == resolutionCorrelated {
		p.stats.correlatedCharacters++
		p.stats.characterResolved++
	}
	if event.Target.HasMonster {
		p.stats.monsterResolved++
	}
	if event.SkillStatus == resolutionVerified || event.SkillStatus == resolutionCorrelated {
		p.stats.skillResolved++
	}
	if event.CharacterStatus == resolutionUnknown || event.Target.MonsterStatus == resolutionUnknown || event.SkillStatus == resolutionUnknown {
		p.stats.unknownDamageEvents++
	}
	p.stats.decodedMatchedEvents++
	p.stats.decodedCombatResultAbs += value
	if p.writeDamageEvent(event) {
		p.observeEncounterSummary(event)
	}
}

func decryptGameBody(sequence uint32, body, sessionKey []byte) ([]byte, error) {
	if len(sessionKey) != 32 || len(body) == 0 || len(body)%16 != 0 {
		return nil, errors.New("invalid AES body or session key length")
	}
	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(body))
	for offset := 0; offset < len(body); offset += aes.BlockSize {
		block.Decrypt(plain[offset:offset+aes.BlockSize], body[offset:offset+aes.BlockSize])
	}
	if len(plain) == 0 {
		return nil, errors.New("empty decrypted payload")
	}
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		clear(plain)
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			clear(plain)
			return nil, errors.New("invalid PKCS#7 padding bytes")
		}
	}
	plain = plain[:len(plain)-pad]
	if len(plain) > 0 {
		plain[int(sequence%uint32(len(plain)))] ^= sessionKey[sequence&31]
	}
	return plain, nil
}

func decompressBody(compressed []byte, expected uint32) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	limit := int64(expected)
	if limit == 0 || limit > 64*1024*1024 {
		limit = 64 * 1024 * 1024
	}
	plain, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(plain)) > limit {
		return nil, errors.New("decompressed frame exceeds safety limit")
	}
	if expected != 0 && uint32(len(plain)) != expected {
		return nil, fmt.Errorf("decompressed length %d does not match frame length %d", len(plain), expected)
	}
	return plain, nil
}

func parseProtoFields(data []byte) ([]protoField, error) {
	fields := make([]protoField, 0, 8)
	for offset := 0; offset < len(data); {
		tag, n := binary.Uvarint(data[offset:])
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid Protobuf tag")
		}
		offset += n
		field := protoField{number: tag >> 3, wire: tag & 7}
		switch field.wire {
		case 0:
			value, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return nil, errors.New("invalid Protobuf varint")
			}
			field.value = value
			offset += n
		case 1:
			if len(data)-offset < 8 {
				return nil, errors.New("truncated Protobuf fixed64")
			}
			field.data = data[offset : offset+8]
			offset += 8
		case 2:
			length, n := binary.Uvarint(data[offset:])
			if n <= 0 || length > uint64(len(data)-offset-n) {
				return nil, errors.New("invalid Protobuf byte length")
			}
			offset += n
			field.data = data[offset : offset+int(length)]
			offset += int(length)
		case 5:
			if len(data)-offset < 4 {
				return nil, errors.New("truncated Protobuf fixed32")
			}
			field.data = data[offset : offset+4]
			offset += 4
		default:
			return nil, fmt.Errorf("unsupported Protobuf wire type %d", field.wire)
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// startProtocolSessionStatistics closes the previous protocol session's encounters at a game handshake and, for an
// application host, starts a new statistics interval for the new protocol session.
func (p *processor) startProtocolSessionStatistics(at time.Time) {
	if p.damageStatistics == nil {
		return
	}
	p.damageStatistics.observeSessionChange(at)
	p.flushEncounterTransitions()
	if p.intervalPerProtocolSession && p.sessionNumber > 1 {
		p.damageStatistics.Reset()
	}
}
