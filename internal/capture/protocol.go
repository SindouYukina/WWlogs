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
	direction   string
	messageType byte
	sequence    uint32
	token       uint16
	// commandID is the command number on the wire, which the decoders read as it is (protocol_numbers.go).
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
	attackerEntityID    uint64 // DamageExecuteRequest.AttackerEntityId
	hasAttackerEntityID bool
	targetEntityID      uint64 // DamageExecuteRequest.TargetEntityId
	hasTargetEntityID   bool
	partID              int32 // DamageExecuteRequest.PartId
	hasPartID           bool
	skillLevel          uint64 // DamageExecuteRequest.SkillLevel
	skillID             uint64 // DamageContext.SkillId
	skillMessageID      uint64 // DamageContext.SkillMessageId
	damageID            uint64 // DamageExecuteRequest.DamageId
	hasDamageID         bool
	damageSourceType    uint64 // DamageContext.SourceType
	hasDamageSourceType bool
	bulletID            int64 // DamageContext.BulletId
	hasBulletID         bool
	// requestEpoch is how many times the combat RequestId counter had wrapped in this protocol session when the request
	// was sent (combatRequestCounter); with the RequestId it names the result (extendedRequestID).
	requestEpoch uint64
	sequence     uint64
}

// combatRequestIDSpace is the number of RequestId values of combat requests. The client numbers every CombatRequestData
// with CombatNet.GenerateRpcId, which counts 1..32767 and then 0 again (CombatMessage.js, Global 3.8.0 base, unchanged by
// the 3.8.9 hotfix); a capture of Build 31 shows 1..32767, 0 (absent on the wire, the proto3 default), 1 after 46,399
// combat requests. A long protocol session therefore uses one RequestId for several requests.
const combatRequestIDSpace = 32768

// combatRequestCounter follows the client's combat RequestId counter through its wraps. Every CombatRequestData of the
// outbound stream advances it in arrival order, damage or not; the outbound stream is one ordered KCP stream, and a step
// down by more than half the space is a wrap (requests sent in one frame may be out of order by a few).
type combatRequestCounter struct {
	last  uint64
	seen  bool
	epoch uint64
}

// observe records one RequestId and returns the epoch it belongs to.
func (c *combatRequestCounter) observe(requestID uint64) uint64 {
	if c.seen && requestID+combatRequestIDSpace/2 < c.last {
		c.epoch++
	}
	c.last, c.seen = requestID, true
	return c.epoch
}

// extendedRequestID names one combat result within its protocol session: the RequestId continued past each wrap of the
// client's counter. It equals the RequestId until the first wrap, so results of shorter sessions keep their RequestId.
func extendedRequestID(requestID, epoch uint64) uint64 {
	return epoch*combatRequestIDSpace + requestID
}

type pendingRef struct {
	rpcID    uint64
	sequence uint64
}

type consumedRef struct {
	rpcID    uint64
	sequence uint64
}

// consumedResponse remembers a processed response: its place in the bounded FIFO, a digest of its payload, so that a
// later copy with the same RequestId is told apart as an identical duplicate or as a conflicting one, and the capture
// time it was processed at, so that a later response with that RequestId is known to be a copy only while copies can
// still arrive (consumedCopyWindow).
type consumedResponse struct {
	sequence uint64
	digest   uint64
	at       time.Time
}

// consumedCopyWindow is how long after a response further responses with its RequestId are taken as copies of it. The
// server sends a response's copies in the same or the next frames; the client reuses a RequestId only after 32,767
// further combat requests, many minutes later. A response with the RequestId of one processed longer ago belongs to a
// later request that reused it, whose request may still be on its way, so it is held like any response without its
// request.
const consumedCopyWindow = damageResponseHoldWindow

// responseDigest is the 64-bit FNV-1a hash of a DamageExecuteResponse payload. It only compares copies of one response.
func responseDigest(payload []byte) uint64 {
	hash := uint64(14695981039346656037)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return hash
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
	standaloneNotifyResponses        int // of candidateResponses, those a standalone CombatReceivePackNotify carried
	printed                          int
	unpairedResponses                int
	invalidResponses                 int
	duplicateResponses               int // a further copy of a response already seen, byte for byte
	conflictingResponses             int // a further response with the RequestId of one already seen but other content
	reusedRequestIDs                 int // a completed RequestId taken by a later request after the counter wrapped
	timeStopPushesWithoutState       int // RTimeStopPush that stated neither a duration nor a dilation
	supersededRequests               int // an unanswered request whose RequestId a later request took (never answered)
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
	// notifyStats counts DamageExecuteNotify results; frameNotifyOrdinal numbers them within the frame being read.
	notifyStats        damageNotifyStats
	frameNotifyOrdinal int
	rsa                *staticRSA
	session            []byte
	sessionGeneration  uint64
	sessionHealth      sessionHealthState
	// protocol is the wire protocol of the current protocol session: decoded only once it reports the current game version.
	protocol protocolSession
	// protocolSessionStartedAt is the capture time of the latest handshake; retiredKCP holds the conversations of the
	// connections it superseded (retireSupersededStreams).
	protocolSessionStartedAt  time.Time
	retiredKCP                retiredConversations
	pending                   map[uint64]pendingDamageRequest
	pendingOrder              []pendingRef
	pendingHead               int
	pendingSequence           uint64
	consumed                  map[uint64]consumedResponse
	consumedOrder             []consumedRef
	consumedSequence          uint64
	requestCounter            combatRequestCounter
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
		consumed:             make(map[uint64]consumedResponse),
		activeRequests:       make(map[uint16]pendingActiveRequest),
		combatActionRequests: make(map[combatActionRequestKey]combatActionRequest),
		entities:             newEntityResolver(),
		sourceRoles:          newSourceRoleResolver(),
		playerRelevance:      newPlayerRelevanceResolver(),
		damageStatistics:     statistics,
		effects:              newJournaledEffectTracker(statistics),
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
	case cmdLoginRequest, cmdCheckClientPush, cmdBasicInfoNotify, cmdCombatSendPackRequest, cmdCombatSendPackResponse, cmdCombatReceivePackNotify, cmdTimeCheckResponse, cmdTimeStopPush, cmdTimeCheckRequest, cmdUpdateSceneGroupDataNotify, cmdJoinSceneNotify, cmdEntityAddNotify, cmdEntityRemoveNotify, cmdUpdateTimerInfoNotify, cmdEntityActiveRequest, cmdEntityActiveResponse, cmdApplyGameplayEffectNotify, cmdRemoveGameplayEffectNotify, cmdRemoveGameplayEffectPush, cmdSimpleCombatEntityBuffUpdateNotify, cmdSimpleCombatEntityBuffLayerCountNotify, cmdBuffStackCountNotify, cmdBuffDurationNotify:
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
	if frame.commandID == cmdProtoKeyResponse && frame.direction == "in" {
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
		p.protocolSessionStartedAt = frame.timestamp
		// A new protocol session may come from another client build: its protocol is known only once it reports it.
		p.protocol.beginSession()
		p.playerRelevance.reset()
		p.scenePlayerIDs = nil
		p.observeContextSessionStart(frame.timestamp)
		p.startProtocolSessionStatistics(frame.timestamp)
		if p.census != nil && p.sessionNumber > p.census.sessions {
			p.census.sessions = p.sessionNumber
		}
		return
	}
	decodable := p.protocol.decodes(frame.commandID)
	if decodable && censusEntry != nil {
		censusEntry.describeCurrentProtocol()
	}
	if !decodable || !isImplementedCommand(frame.commandID) {
		if censusEntry != nil {
			p.inspectUndecodedFrame(frame, decodable, censusEntry)
		} else if decodable {
			p.archiveUndecodedSchema(frame)
		}
		return
	}
	if ((frame.commandID == cmdLoginRequest || frame.commandID == cmdCheckClientPush || frame.commandID == cmdCombatSendPackRequest || frame.commandID == cmdEntityActiveRequest || frame.commandID == cmdTimeStopPush || frame.commandID == cmdTimeCheckRequest) && frame.direction != "out") || ((frame.commandID == cmdBasicInfoNotify || frame.commandID == cmdCombatSendPackResponse || frame.commandID == cmdCombatReceivePackNotify || frame.commandID == cmdTimeCheckResponse || frame.commandID == cmdUpdateSceneGroupDataNotify || frame.commandID == cmdJoinSceneNotify || frame.commandID == cmdEntityAddNotify || frame.commandID == cmdEntityRemoveNotify || frame.commandID == cmdUpdateTimerInfoNotify || frame.commandID == cmdEntityActiveResponse || frame.commandID == cmdApplyGameplayEffectNotify || frame.commandID == cmdRemoveGameplayEffectNotify || frame.commandID == cmdRemoveGameplayEffectPush || frame.commandID == cmdSimpleCombatEntityBuffUpdateNotify || frame.commandID == cmdSimpleCombatEntityBuffLayerCountNotify || frame.commandID == cmdBuffStackCountNotify || frame.commandID == cmdBuffDurationNotify) && frame.direction != "in") {
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
		if frame.commandID == cmdTimeStopPush {
			// proto3 leaves a 0 dilation off the wire: an empty TimeStopPush pauses every effect.
			if err := p.observeTimeStopPush(frame, nil); err != nil {
				p.stats.parseErrors++
			}
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
	case cmdBasicInfoNotify:
		if err := p.observeLocalPlayerInfo(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "local_player_info_parse_error=%v\n", err)
			return
		}
	case cmdLoginRequest, cmdCheckClientPush:
		if err := p.observeClientVersion(frame.commandID, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "client_version_parse_error command=%d reason=%v\n", frame.commandID, err)
			return
		}
	case cmdCombatSendPackRequest:
		p.handleDamageRequests(frame, plain)
		if err := p.handleClientEffectPushes(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "client_effect_push_parse_error=%v\n", err)
		}
	case cmdCombatSendPackResponse, cmdCombatReceivePackNotify:
		if err := p.handleCombatReceivePacks(frame, plain); err != nil {
			p.stats.parseErrors++
			label := "combat_receive_pack_parse_error"
			if frame.commandID == cmdCombatSendPackResponse {
				label = "combat_response_notify_parse_error"
			}
			fmt.Fprintf(p.diagnostic, "%s=%v\n", label, err)
			return
		}
	case cmdTimeStopPush:
		if err := p.observeTimeStopPush(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "time_stop_push_parse_error=%v\n", err)
		}
	case cmdTimeCheckRequest:
		if err := p.observeTimeCheckRequest(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "time_check_request_parse_error=%v\n", err)
		}
	case cmdTimeCheckResponse, cmdUpdateTimerInfoNotify:
		// TimeCheckResponse and UpdateTimerInfoNotify are interpreted by the
		// schema-directed observer to preserve their observed field provenance.
	case cmdUpdateSceneGroupDataNotify:
		if _, err := p.entities.applyUpdateSceneGroupDataNotify(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "scene_group_update_parse_error=%v\n", err)
			return
		}
		p.playerRelevance.reconcileLocalPlayers(p.entities)
		p.refreshEffectRelations()
	case cmdApplyGameplayEffectNotify, cmdRemoveGameplayEffectNotify, cmdRemoveGameplayEffectPush, cmdSimpleCombatEntityBuffUpdateNotify, cmdSimpleCombatEntityBuffLayerCountNotify, cmdBuffStackCountNotify, cmdBuffDurationNotify:
		if err := p.handleBuffLifecycleMessage(frame, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "buff_lifecycle_parse_error=%v\n", err)
			return
		}
	case cmdJoinSceneNotify:
		if _, err := p.entities.applyJoinSceneNotify(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "join_scene_parse_error=%v\n", err)
			return
		}
		p.playerRelevance.resetSceneEntities()
		p.observeContextSceneJoin(frame.timestamp)
		p.observeSceneJoin(plain, frame.timestamp)
		p.effects.EndLiveStateFor(EffectUntrackedSceneChange)
		p.backfillAvailableSummaryCredits()
		p.sourceRoles.reset()
		p.resetPendingSummaryCredits()
	case cmdEntityAddNotify:
		added, err := p.entities.applyEntityAddNotify(plain)
		if err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_add_parse_error=%v\n", err)
			return
		}
		p.stats.entityAdds += added
		p.refreshEffectRelations()
	case cmdEntityRemoveNotify:
		if err := p.handleEntityRemoveNotify(plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_remove_parse_error=%v\n", err)
			return
		}
	case cmdEntityActiveRequest:
		if err := p.handleActiveRequest(frame.token, plain); err != nil {
			p.stats.parseErrors++
			fmt.Fprintf(p.diagnostic, "entity_active_parse_error=%v\n", err)
			return
		}
		p.stats.entityActiveRequests++
	case cmdEntityActiveResponse:
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

// Census reasons for frames the current protocol session cannot hand to a decoder.
const (
	protocolNoHandshakeReason        = "no handshake in this capture; protocol unknown"
	protocolPendingFailureReason     = "client version not reported yet; protocol unknown"
	protocolUnsupportedFailureReason = "not the current game version; not decoded"
)

// inspectUndecodedFrame counts what the census can learn about a frame no decoder reads. decodable says whether the
// protocol session reads its numbering (protocolSession.decodes); otherwise only the top-level wire shape is counted.
func (p *processor) inspectUndecodedFrame(frame gameFrame, decodable bool, entry *commandCensusEntry) {
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
	if !decodable {
		if fields, err := parseProtoFields(plain); err == nil {
			entry.GenericProtoValid++
			entry.observeTopLevelFields(fields)
		}
		entry.FailureReasons[p.protocol.undecodableReason()]++
		return
	}
	if fields, err := parseProtoFields(plain); err == nil {
		entry.GenericProtoValid++
		// The optional external schema describes the current protocol.
		p.archiveSchemaDecode(frame, plain)
		entry.observeTopLevelFields(fields)
		entry.FailureReasons["message schema not implemented"]++
		if frame.commandID == cmdMovePackagePush {
			observation, err := extractMovePackageObservation(plain)
			if err != nil {
				entry.FailureReasons["MovePackagePush schema-field extraction failed"]++
			} else {
				p.census.observeMovePackage(observation)
			}
		} else if frame.commandID == cmdPlayerFollowerNotify {
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
	case cmdEntityRemoveDynamicInteractNotify:
		return fieldEntityRemoveDynamicInteractNotifyEntityID, true // EntityRemoveDynamicInteractNotify.EntityId
	case cmdEntityCommonTagNotify:
		return fieldEntityCommonTagNotifyID, true // EntityCommonTagNotify.Id
	case cmdEntityStateReadyNotify:
		return fieldEntityStateReadyNotifyEntityID, true // EntityStateReadyNotify.EntityId
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
		if field.number == fieldProtoKeyResponseType && field.wire == 0 {
			responseType = field.value
		}
		if field.number == fieldProtoKeyResponseKey && field.wire == 2 {
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
	p.consumed = make(map[uint64]consumedResponse)
	p.consumedOrder = nil
	p.consumedSequence = 0
	p.requestCounter = combatRequestCounter{}
	// RPC ids restart with the session: a held response can never pair with a request of the new session.
	p.discardHeldResponses(&p.pairing.sessionChange)
	p.entities.reset()
	p.playerRelevance.reset()
	p.effects.EndLiveStateFor(EffectUntrackedSessionChange)
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
		if field.number != fieldCombatSendPackRequestData || field.wire != 2 { // sZn.VFs
			continue
		}
		variants, err := parseProtoFields(field.data)
		if err != nil {
			p.stats.parseErrors++
			continue
		}
		for _, variant := range variants {
			if variant.number != fieldCombatSendDataRequest || variant.wire != 2 { // VFs.FFs RequestData; field 5 is U3n
				continue
			}
			requestFields, err := parseProtoFields(variant.data)
			if err != nil {
				p.stats.parseErrors++
				continue
			}
			// An absent RequestId is the proto3 default 0, which the client's counter sends once per wrap.
			var rpcID uint64
			var u3n []byte
			for _, requestField := range requestFields {
				if requestField.number == fieldCombatRequestDataRequestID && requestField.wire == 0 {
					rpcID = requestField.value
				}
				if requestField.number == fieldCombatRequestDataDamageExecuteRequest && requestField.wire == 2 {
					u3n = requestField.data
				}
			}
			epoch := p.requestCounter.observe(rpcID)
			if u3n == nil {
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
				case damageField.number == fieldDamageExecuteRequestDamageID && damageField.wire == 0: // U3n.Fjn DamageId
					damageIDPresent = true
					identity.damageID = damageField.value
					identity.hasDamageID = true
				case damageField.number == fieldDamageExecuteRequestTargetEntityID && damageField.wire == 0: // DamageExecuteRequest.TargetEntityId
					identity.targetEntityID = damageField.value
					identity.hasTargetEntityID = true
				case damageField.number == fieldDamageExecuteRequestPartID && damageField.wire == 0: // DamageExecuteRequest.PartId
					identity.partID = int32(uint32(damageField.value))
					identity.hasPartID = true
				case damageField.number == fieldDamageExecuteRequestSkillLevel && damageField.wire == 0: // DamageExecuteRequest.SkillLevel
					identity.skillLevel = damageField.value
				case damageField.number == fieldDamageExecuteRequestAttackerEntityID && damageField.wire == 0: // DamageExecuteRequest.AttackerEntityId
					identity.attackerEntityID = damageField.value
					identity.hasAttackerEntityID = true
				case damageField.number == fieldDamageExecuteRequestDamageContext && damageField.wire == 2: // DamageExecuteRequest.DamageContext
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
						case fieldDamageContextSourceType: // DamageContext.SourceType
							identity.damageSourceType = contextField.value
							identity.hasDamageSourceType = true
						case fieldDamageContextBulletID: // DamageContext.BulletId
							identity.bulletID = int64(contextField.value)
							identity.hasBulletID = true
						case fieldDamageContextSkillID: // DamageContext.SkillId
							identity.skillID = contextField.value
						case fieldDamageContextSkillMessageID: // DamageContext.SkillMessageId
							identity.skillMessageID = contextField.value
						}
					}
				}
			}
			if !damageIDPresent {
				continue
			}
			if p.census != nil {
				p.census.innerEntries["Client->Server DamageExecuteRequest"]++
			}
			p.stats.damageRequests++
			identity.requestEpoch = epoch
			if _, alreadyConsumed := p.consumed[rpcID]; alreadyConsumed {
				// The counter wrapped: the RequestId now names this request. The earlier result keeps its own extended id,
				// and this request's response is paired with this request instead of being taken for a copy.
				delete(p.consumed, rpcID)
				p.stats.reusedRequestIDs++
				fmt.Fprintf(p.diagnostic, "rpc_id_reused_after_completion rpc_id=%d epoch=%d\n", rpcID, epoch)
			}
			if _, exists := p.pending[rpcID]; exists {
				// The earlier request with this RequestId was never answered; it stays unanswered, now superseded.
				p.stats.supersededRequests++
				fmt.Fprintf(p.diagnostic, "duplicate_pending_damage_rpc rpc_id=%d epoch=%d\n", rpcID, epoch)
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

func (p *processor) rememberConsumed(rpcID, digest uint64) {
	p.consumedSequence++
	p.consumed[rpcID] = consumedResponse{sequence: p.consumedSequence, digest: digest, at: p.currentFrameTime}
	p.consumedOrder = append(p.consumedOrder, consumedRef{rpcID: rpcID, sequence: p.consumedSequence})
	for len(p.consumed) > maxConsumedRPCIDs && len(p.consumedOrder) > 0 {
		ref := p.consumedOrder[0]
		p.consumedOrder = p.consumedOrder[1:]
		if p.consumed[ref.rpcID].sequence == ref.sequence {
			delete(p.consumed, ref.rpcID)
		}
	}
	// A reused RequestId leaves its entry's place behind (the map entry is gone); keep the order bounded as well.
	if len(p.consumedOrder) > 2*maxConsumedRPCIDs {
		live := make([]consumedRef, 0, len(p.consumed))
		for _, ref := range p.consumedOrder {
			if p.consumed[ref.rpcID].sequence == ref.sequence {
				live = append(live, ref)
			}
		}
		p.consumedOrder = live
	}
}

// handleOneDamageResult takes one server DamageExecuteResponse, from either container (handleCombatReceivePacks). A result
// is named by its RequestId within the protocol session and by the wrap of the client's RequestId counter it was sent in
// (extendedRequestID): a response is processed once, with the request of that RequestId, and every further copy is
// counted as a duplicate (same bytes) or a conflicting response (other bytes), never as a second result. Copies arrive
// within consumedCopyWindow of the response they repeat; a response with the RequestId of one processed earlier than that
// answers a later request that reused the RequestId. Equal values never make two responses one.
func (p *processor) handleOneDamageResult(rpcID uint64, payload []byte) {
	if consumed, already := p.consumed[rpcID]; already {
		if p.currentFrameTime.Sub(consumed.at) <= consumedCopyWindow {
			if consumed.digest == responseDigest(payload) {
				p.stats.duplicateResponses++
			} else {
				p.stats.conflictingResponses++
			}
			return
		}
		// The response of a later request with this RequestId, ahead of its request: the earlier result keeps its own
		// extended id, and this one is held for its request like any response that arrives first.
		delete(p.consumed, rpcID)
		p.stats.reusedRequestIDs++
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
		case fieldDamageExecuteResponseErrorCode: // lis.Q4n
			errorCode = int32(uint32(field.value))
		case fieldDamageExecuteResponseImmuneType: // lis.lAs
			immuneType = int32(uint32(field.value))
		case fieldDamageExecuteResponseChangeLife: // lis.jQ_, client-mapped ChangeLife
			changeLife = int32(uint32(field.value))
			hasChangeLife = true
		case fieldDamageExecuteResponseDamage: // lis.nAs
			amount = int32(uint32(field.value))
			hasAmount = true
		}
	}
	if !hasAmount || errorCode != 0 || immuneType != 0 {
		p.stats.invalidResponses++
		p.rememberConsumed(rpcID, responseDigest(payload))
		delete(p.pending, rpcID)
		p.writeNoAmountEvent(rpcID, request, hasAmount, errorCode, immuneType, hasChangeLife, changeLife)
		return
	}

	p.rememberConsumed(rpcID, responseDigest(payload))
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
	p.damageStatistics.endProtocolSession()
	if p.intervalPerProtocolSession && p.sessionNumber > 1 {
		p.damageStatistics.Reset()
	}
}
