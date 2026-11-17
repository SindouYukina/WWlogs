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

type GameFrame struct {
	Direction   string
	MessageType byte
	Sequence    uint32
	Token       uint16
	// CommandID is the command number on the wire, which the decoders read as it is (protocol_numbers.go).
	CommandID        uint16
	Compressed       bool
	CompressedLength uint32
	Body             []byte
	Timestamp        time.Time
}

type applicationSpan struct {
	length    int
	timestamp time.Time
}

type ProtoField struct {
	Number uint64
	Wire   uint64
	Value  uint64
	Data   []byte
}

type PendingDamageRequest struct {
	AttackerEntityID    uint64 // DamageExecuteRequest.AttackerEntityId
	HasAttackerEntityID bool
	TargetEntityID      uint64 // DamageExecuteRequest.TargetEntityId
	HasTargetEntityID   bool
	PartID              int32 // DamageExecuteRequest.PartId
	HasPartID           bool
	skillLevel          uint64 // DamageExecuteRequest.SkillLevel
	SkillID             uint64 // DamageContext.SkillId
	SkillMessageID      uint64 // DamageContext.SkillMessageId
	DamageID            uint64 // DamageExecuteRequest.DamageId
	HasDamageID         bool
	DamageSourceType    uint64 // DamageContext.SourceType
	HasDamageSourceType bool
	BulletID            int64 // DamageContext.BulletId
	HasBulletID         bool
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

type PendingActiveRequest struct {
	EntityID    uint64
	HasEntityID bool
}

type processorStats struct {
	HandshakeFrames                  int
	HandshakeOK                      int
	HandshakeErrors                  int
	DamageRequests                   int
	CandidateResponses               int
	standaloneNotifyResponses        int // of candidateResponses, those a standalone CombatReceivePackNotify carried
	Printed                          int
	UnpairedResponses                int
	InvalidResponses                 int
	DuplicateResponses               int // a further copy of a response already seen, byte for byte
	conflictingResponses             int // a further response with the RequestId of one already seen but other content
	reusedRequestIDs                 int // a completed RequestId taken by a later request after the counter wrapped
	timeStopPushesWithoutState       int // RTimeStopPush that stated neither a duration nor a dilation
	supersededRequests               int // an unanswered request whose RequestId a later request took (never answered)
	MissingAttackerEntityID          int
	EvictedPending                   int
	EntityAdds                       int
	EntityRemoves                    int
	EntityActiveRequests             int
	ObservedSourceEntity             int
	ObservedTargetEntity             int
	characterResolved                int
	VerifiedCharacters               int
	CorrelatedCharacters             int
	MonsterResolved                  int
	SkillResolved                    int
	UnknownDamageEvents              int
	DamageCandidateEvents            int
	DamageCandidateResult            int64
	HealingCandidateEvents           int
	OtherCandidateEvents             int
	OtherCandidateResult             int64
	UnclassifiedEvents               int
	UnclassifiedResult               int64
	ObservedBuffEvents               int
	VerifiedBuffEvents               int
	BuffSnapshotResponses            int
	ActiveResponseIdentityComponents int
	ActiveResponseSummonerComponents int
	ActiveResponseMonsterComponents  int
	ActiveResponseFollowerComponents int
	DecryptErrors                    int
	CompressedErrors                 int
	ParseErrors                      int
	otherFrameDirections             int
	MatchedCombatResultAbs           int64
	DecodedMatchedEvents             int
	DecodedCombatResultAbs           int64
	PlayerRelatedEvents              int
	IncludedUnresolvedRelations      int
	SuppressedUnrelatedEvents        int
	PlayerRoleReports                int
	PlayerRoleReportParseErrors      int
	HealingCandidateResult           int64
}

type Processor struct {
	// NotifyStats counts DamageExecuteNotify results; frameNotifyOrdinal numbers them within the frame being read.
	NotifyStats        DamageNotifyStats
	frameNotifyOrdinal int
	// statusTriggers remembers the InvokePeriod Buffs and instant status Buffs the server applied recently, for the
	// negative-status results that follow them (negative_status_credit.go).
	statusTriggers statusTriggerLog
	// statusCauses remembers the recent applications and removals of statuses without a period, and excludedStatuses the
	// status instances the effect tracker leaves out (negative_status_causes.go).
	statusCauses     statusCauseLog
	excludedStatuses excludedStatusIndex
	// ServerSequence follows the sequence of the server's frames, so a missing frame is known (negative_status_credit.go).
	ServerSequence    serverSequenceState
	RSA               *StaticRSA
	Session           []byte
	sessionGeneration uint64
	sessionHealth     sessionHealthState
	// Protocol is the wire protocol of the current protocol session: decoded only once it reports the current game version.
	Protocol ProtocolSession
	// protocolSessionStartedAt is the capture time of the latest handshake; retiredKCP holds the conversations of the
	// connections it superseded (retireSupersededStreams).
	protocolSessionStartedAt time.Time
	retiredKCP               retiredConversations
	Pending                  map[uint64]PendingDamageRequest
	pendingOrder             []pendingRef
	pendingHead              int
	pendingSequence          uint64
	consumed                 map[uint64]consumedResponse
	consumedOrder            []consumedRef
	consumedSequence         uint64
	requestCounter           combatRequestCounter
	Entities                 *EntityResolver
	SourceRoles              *sourceRoleResolver
	PlayerRelevance          *playerRelevanceResolver
	Masters                  *GameMaster
	CurrentFrameTime         time.Time
	ViewClockLead            time.Duration // how far past currentFrameTime the Effects view reads countdowns; only a live session sets it
	ViewClockStalled         bool          // a live session has seen no game frame for LiveFrameSilenceLimit
	ClientVersion            *ClientVersionReport
	// Diagnostic is the operational diagnostic stream (parse errors, session key readiness) in every build.
	Diagnostic             io.Writer
	BuffHandles            map[BuffHandleKey]buffHandleEntry
	buffHandleOrder        []BuffHandleKey
	serverBuffRequests     map[serverBuffRequestKey]serverBuffRequest
	serverBuffRequestOrder []serverBuffRequestKey
	activeRequests         map[uint16]PendingActiveRequest
	Stats                  processorStats
	DamageStatistics       *DamageStatistics
	Effects                *EffectTracker
	// Census is the Development command census (-command-census-out), nil in every other run; the frame parser and
	// the frame dispatcher report what they read to it.
	Census        CommandCensusObserver
	SessionNumber int
	// ScenePlayerIDs lists the players of the current scene; it anchors battle-state signals before the
	// explicit local-player identity does.
	ScenePlayerIDs []uint64
	// kcpStreams is the capture loop's KCP stream table, read for transport health snapshots.
	kcpStreams   map[string]*Stream
	transport    transportMonitor
	Context      contextValidity
	CaptureLayer captureLayerState
	pairing      rpcPairingState
	// IntervalPerProtocolSession starts a new statistics interval at every game handshake after the first, so one
	// interval never mixes protocol sessions. Application hosts set it; the CLI keeps one interval per capture.
	IntervalPerProtocolSession bool

	// observer receives the observation points (Observe); nil without one, which is every Public processor.
	observer Observer
}

// NewProcessor is a processor that decrypts with rsaKey and writes its operational diagnostics (parse errors, session
// key readiness) to diagnostic.
func NewProcessor(rsaKey *StaticRSA, diagnostic io.Writer) *Processor {
	statistics := NewDamageStatistics()
	return &Processor{
		RSA:              rsaKey,
		Pending:          make(map[uint64]PendingDamageRequest),
		consumed:         make(map[uint64]consumedResponse),
		activeRequests:   make(map[uint16]PendingActiveRequest),
		Entities:         NewEntityResolver(),
		SourceRoles:      newSourceRoleResolver(),
		PlayerRelevance:  newPlayerRelevanceResolver(),
		DamageStatistics: statistics,
		Effects:          newJournaledEffectTracker(statistics),
		Diagnostic:       diagnostic,
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
func parseFrames(buffer []byte, direction string, spans []applicationSpan, p *Processor) (int, int, int, int) {
	result := parseFrameStream(buffer, direction, spans, p, nil)
	return result.frames, result.badCRC, result.malformed, result.consumed
}

func parseFrameStream(buffer []byte, direction string, spans []applicationSpan, p *Processor, state *Stream) frameParseResult {
	var result frameParseResult
	applicationOffset := 0
	var previousValid *FrameBoundaryObservation
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
			if p.Census != nil {
				p.Census.Malformed("short_or_unaligned_frame_candidate", timestamp, applicationOffset, uint16(payloadLen), len(buffer))
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
			if p.Census != nil {
				p.Census.Malformed("truncated_command_header", timestamp, applicationOffset, uint16(payloadLen), total)
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
				if p.Census != nil {
					p.Census.Malformed("truncated_compression_length", timestamp, applicationOffset, uint16(payloadLen), total)
				}
				return desync(frameDesyncMalformed)
			}
			compressedLength = binary.LittleEndian.Uint32(frame[bodyStart : bodyStart+4])
			bodyStart += 4
		}
		computedCRC := crc32IEEE(frame[bodyStart:total])
		if computedCRC != receivedCRC {
			if p.Census != nil {
				var token uint16
				if messageType != 4 {
					token = binary.LittleEndian.Uint16(frame[8:10])
				}
				observation := CRCFailureObservation{
					Direction: direction, Timestamp: timestamp, Offset: applicationOffset,
					MessageType: messageType, Sequence: binary.LittleEndian.Uint32(frame[4:8]),
					Token: token, CommandID: commandID, DeclaredLength: uint16(payloadLen),
					FrameLength: total, BodyLength: total - bodyStart,
					ReceivedCRC: receivedCRC, ComputedCRC: computedCRC, RemainingBytes: len(buffer) - total,
					PreviousValid: previousValid,
				}
				if index := p.Census.FailedCRC(observation); index >= 0 {
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
		current := GameFrame{
			Direction:        direction,
			MessageType:      messageType,
			Sequence:         binary.LittleEndian.Uint32(frame[4:8]),
			Token:            token,
			CommandID:        commandID,
			Compressed:       compressed,
			CompressedLength: compressedLength,
			Body:             append([]byte(nil), frame[bodyStart:total]...),
			Timestamp:        applicationTimestampAt(spans, consumed),
		}
		boundary := FrameBoundaryObservation{Offset: applicationOffset, MessageType: messageType, Sequence: current.Sequence, Token: token, CommandID: commandID, FrameLength: total}
		if p.Census != nil {
			for _, index := range *pendingCRCIndexes {
				p.Census.SetNextValidFrame(index, boundary)
			}
		}
		*pendingCRCIndexes = nil
		if state != nil {
			state.lastFrameSequence, state.hasFrameSequence = current.Sequence, true
		}
		p.HandleFrame(current)
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

// IsImplementedCommand reports whether the outer command has a decoder path in
// handleFrame. Other commands are only counted and inspected by the census.
func IsImplementedCommand(commandID uint16) bool {
	switch commandID {
	case CmdLoginRequest, CmdCheckClientPush, CmdBasicInfoNotify, CmdCombatSendPackRequest, CmdCombatSendPackResponse, CmdCombatReceivePackNotify, cmdTimeCheckResponse, cmdTimeStopPush, cmdTimeCheckRequest, CmdUpdateSceneGroupDataNotify, CmdJoinSceneNotify, CmdEntityAddNotify, CmdEntityRemoveNotify, cmdUpdateTimerInfoNotify, CmdEntityActiveRequest, CmdEntityActiveResponse, CmdApplyGameplayEffectNotify, CmdRemoveGameplayEffectNotify, CmdRemoveGameplayEffectPush, CmdSimpleCombatEntityBuffUpdateNotify, CmdSimpleCombatEntityBuffLayerCountNotify, CmdBuffStackCountNotify, CmdBuffDurationNotify:
		return true
	}
	return false
}

func (p *Processor) HandleFrame(frame GameFrame) {
	if p.observer != nil {
		p.observer.ArchiveFrame(frame)
	}
	p.CurrentFrameTime = frame.Timestamp
	p.observeServerSequence(frame)
	p.ObservePairingClock(frame.Timestamp)
	p.observeEncounterClock(frame.Timestamp)
	p.sessionHealth.observeFrame(frame.Timestamp)
	var censusEntry CensusFrameObserver
	if p.Census != nil {
		censusEntry = p.Census.ObserveFrame(frame, p.SessionNumber)
	}
	if frame.CommandID == CmdProtoKeyResponse && frame.Direction == "in" {
		p.Stats.HandshakeFrames++
		if err := p.handleProtoKeyResponse(frame.Body); err != nil {
			p.Stats.HandshakeErrors++
			p.sessionHealth.lastHandshakeFailed = true
			if censusEntry != nil {
				censusEntry.Fail("ProtoKeyResponse decode failed")
			}
			return
		}
		p.Stats.HandshakeOK++
		p.sessionHealth.lastHandshakeFailed = false
		p.sessionHealth.consecutiveDecryptFailures = 0
		if censusEntry != nil {
			censusEntry.Decoded()
		}
		p.SessionNumber++
		p.protocolSessionStartedAt = frame.Timestamp
		// A new protocol session may come from another client build: its protocol is known only once it reports it.
		p.Protocol.BeginSession()
		p.PlayerRelevance.Reset()
		p.ScenePlayerIDs = nil
		p.observeContextSessionStart(frame.Timestamp)
		p.startProtocolSessionStatistics(frame.Timestamp)
		if p.Census != nil {
			p.Census.ObserveSession(p.SessionNumber)
		}
		return
	}
	decodable := p.Protocol.decodes(frame.CommandID)
	if decodable && censusEntry != nil {
		censusEntry.DescribeCurrentProtocol()
	}
	if !decodable || !IsImplementedCommand(frame.CommandID) {
		if censusEntry != nil {
			censusEntry.InspectUndecoded(frame, decodable)
		} else if decodable {
			if p.observer != nil {
				p.observer.ArchiveUndecodedSchema(frame)
			}
		}
		return
	}
	if ((frame.CommandID == CmdLoginRequest || frame.CommandID == CmdCheckClientPush || frame.CommandID == CmdCombatSendPackRequest || frame.CommandID == CmdEntityActiveRequest || frame.CommandID == cmdTimeStopPush || frame.CommandID == cmdTimeCheckRequest) && frame.Direction != "out") || ((frame.CommandID == CmdBasicInfoNotify || frame.CommandID == CmdCombatSendPackResponse || frame.CommandID == CmdCombatReceivePackNotify || frame.CommandID == cmdTimeCheckResponse || frame.CommandID == CmdUpdateSceneGroupDataNotify || frame.CommandID == CmdJoinSceneNotify || frame.CommandID == CmdEntityAddNotify || frame.CommandID == CmdEntityRemoveNotify || frame.CommandID == cmdUpdateTimerInfoNotify || frame.CommandID == CmdEntityActiveResponse || frame.CommandID == CmdApplyGameplayEffectNotify || frame.CommandID == CmdRemoveGameplayEffectNotify || frame.CommandID == CmdRemoveGameplayEffectPush || frame.CommandID == CmdSimpleCombatEntityBuffUpdateNotify || frame.CommandID == CmdSimpleCombatEntityBuffLayerCountNotify || frame.CommandID == CmdBuffStackCountNotify || frame.CommandID == CmdBuffDurationNotify) && frame.Direction != "in") {
		p.Stats.otherFrameDirections++
		if censusEntry != nil {
			censusEntry.Fail("direction differs from implemented schema")
		}
		return
	}
	if len(p.Session) != 32 {
		if censusEntry != nil {
			censusEntry.Fail("session key unavailable")
		}
		return
	}
	if len(frame.Body) == 0 {
		// Zero-length bodies carry no ciphertext; counting them as AES failures
		// would misreport transport health.
		if censusEntry != nil {
			censusEntry.Fail(EmptyBodyFailureReason)
		}
		if frame.CommandID == cmdTimeStopPush {
			// proto3 leaves a 0 dilation off the wire: an empty TimeStopPush pauses every effect.
			if err := p.observeTimeStopPush(frame, nil); err != nil {
				p.Stats.ParseErrors++
			}
		}
		return
	}
	plain, err := DecryptGameBody(frame.Sequence, frame.Body, p.Session)
	if err != nil {
		p.Stats.DecryptErrors++
		p.sessionHealth.consecutiveDecryptFailures++
		if censusEntry != nil {
			censusEntry.Fail("decrypt failed")
		}
		return
	}
	p.sessionHealth.consecutiveDecryptFailures = 0
	if censusEntry != nil {
		censusEntry.Decrypted()
	}
	if frame.Compressed {
		plain, err = DecompressBody(plain, frame.CompressedLength)
		if err != nil {
			p.Stats.CompressedErrors++
			if censusEntry != nil {
				censusEntry.Fail("decompression failed")
			}
			return
		}
	}
	if censusEntry != nil {
		censusEntry.ObserveKnownBodyWire(plain)
	}
	if p.observer != nil {
		p.observer.ArchiveSchemaDecode(frame, plain)
	}
	if p.observer != nil {
		p.observer.ArchiveDamageRPCSchema(frame, plain)
	}
	if p.observer != nil {
		p.observer.ArchiveHPStateFrame(frame, plain)
	}
	if p.observer != nil {
		p.observer.ArchiveCombatStateFrame(frame, plain)
	}
	if p.observer != nil {
		p.observer.ArchiveDamagePushFrame(frame, plain)
	}
	p.ObserveClientPushFrame(frame, plain)
	if p.observer != nil {
		p.observer.ArchiveBulletRefReceiveFrame(frame, plain)
	}
	if p.observer != nil {
		p.observer.ArchiveSummonOwnerFrame(frame, plain)
	}
	parseErrorsBefore := p.Stats.ParseErrors
	switch frame.CommandID {
	case CmdBasicInfoNotify:
		if err := p.observeLocalPlayerInfo(plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "local_player_info_parse_error=%v\n", err)
			return
		}
	case CmdLoginRequest, CmdCheckClientPush:
		if err := p.observeClientVersion(frame.CommandID, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "client_version_parse_error command=%d reason=%v\n", frame.CommandID, err)
			return
		}
	case CmdCombatSendPackRequest:
		p.HandleDamageRequests(frame, plain)
		if err := p.HandleClientEffectPushes(frame, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "client_effect_push_parse_error=%v\n", err)
		}
	case CmdCombatSendPackResponse, CmdCombatReceivePackNotify:
		if err := p.HandleCombatReceivePacks(frame, plain); err != nil {
			p.Stats.ParseErrors++
			label := "combat_receive_pack_parse_error"
			if frame.CommandID == CmdCombatSendPackResponse {
				label = "combat_response_notify_parse_error"
			}
			fmt.Fprintf(p.Diagnostic, "%s=%v\n", label, err)
			return
		}
	case cmdTimeStopPush:
		if err := p.observeTimeStopPush(frame, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "time_stop_push_parse_error=%v\n", err)
		}
	case cmdTimeCheckRequest:
		if err := p.observeTimeCheckRequest(frame, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "time_check_request_parse_error=%v\n", err)
		}
	case cmdTimeCheckResponse, cmdUpdateTimerInfoNotify:
		// TimeCheckResponse and UpdateTimerInfoNotify are interpreted by the
		// schema-directed observer to preserve their observed field provenance.
	case CmdUpdateSceneGroupDataNotify:
		if _, err := p.Entities.applyUpdateSceneGroupDataNotify(plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "scene_group_update_parse_error=%v\n", err)
			return
		}
		p.PlayerRelevance.ReconcileLocalPlayers(p.Entities)
		p.refreshEffectRelations()
	case CmdApplyGameplayEffectNotify, CmdRemoveGameplayEffectNotify, CmdRemoveGameplayEffectPush, CmdSimpleCombatEntityBuffUpdateNotify, CmdSimpleCombatEntityBuffLayerCountNotify, CmdBuffStackCountNotify, CmdBuffDurationNotify:
		if err := p.HandleBuffLifecycleMessage(frame, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "buff_lifecycle_parse_error=%v\n", err)
			return
		}
	case CmdJoinSceneNotify:
		if _, err := p.Entities.applyJoinSceneNotify(plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "join_scene_parse_error=%v\n", err)
			return
		}
		p.PlayerRelevance.ResetSceneEntities()
		p.observeContextSceneJoin(frame.Timestamp)
		p.observeSceneJoin(plain, frame.Timestamp)
		p.Effects.EndLiveStateFor(EffectUntrackedSceneChange)
		if p.observer != nil {
			p.observer.BackfillAvailableSummaryCredits()
		}
		p.SourceRoles.reset()
		if p.observer != nil {
			p.observer.ResetPendingSummaryCredits()
		}
	case CmdEntityAddNotify:
		added, err := p.Entities.ApplyEntityAddNotify(plain)
		if err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "entity_add_parse_error=%v\n", err)
			return
		}
		p.Stats.EntityAdds += added
		p.refreshEffectRelations()
	case CmdEntityRemoveNotify:
		if err := p.handleEntityRemoveNotify(plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "entity_remove_parse_error=%v\n", err)
			return
		}
	case CmdEntityActiveRequest:
		if err := p.HandleActiveRequest(frame.Token, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "entity_active_parse_error=%v\n", err)
			return
		}
		p.Stats.EntityActiveRequests++
	case CmdEntityActiveResponse:
		if err := p.HandleActiveResponse(frame, plain); err != nil {
			p.Stats.ParseErrors++
			fmt.Fprintf(p.Diagnostic, "entity_active_response_parse_error=%v\n", err)
			return
		}
	}
	if censusEntry != nil {
		if parseErrorsBefore == p.Stats.ParseErrors {
			censusEntry.Decoded()
		} else {
			censusEntry.Fail("protobuf or message schema decode failed")
		}
	}
}

// handleEntityRemoveNotify applies an EntityRemoveNotify. A held damage response may name an entity removed here, so the
// state an in-order pair would have seen is kept first (see rpc_pairing.go). On a parse error nothing but the registry
// removal of the IDs read before the fault is applied.
func (p *Processor) handleEntityRemoveNotify(body []byte) error {
	p.rememberRemovalsForHeldResponses(body)
	removed, removedIDs, err := p.Entities.ApplyEntityRemoveNotify(body)
	if err != nil {
		return err
	}
	p.Stats.EntityRemoves += removed
	for _, id := range removedIDs {
		p.Effects.ObserveHolderRemoved(p.SessionNumber, id, p.CurrentFrameTime)
		p.PlayerRelevance.RemoveEntity(id)
		p.settleRemovedSource(id)
		p.SourceRoles.remove(id)
	}
	return nil
}

// EmptyBodyFailureReason labels frames whose command body is zero-length.
const EmptyBodyFailureReason = "empty body; no payload to decrypt"

func (p *Processor) handleProtoKeyResponse(body []byte) error {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse ProtoKeyResponse: %w", err)
	}
	var responseType uint64
	var encrypted []byte
	for _, field := range fields {
		if field.Number == FieldProtoKeyResponseType && field.Wire == 0 {
			responseType = field.Value
		}
		if field.Number == FieldProtoKeyResponseKey && field.Wire == 2 {
			encrypted = field.Data
		}
	}
	if responseType != 2 {
		return fmt.Errorf("ProtoKeyResponse type %d is not AES mode", responseType)
	}
	key, err := p.RSA.decryptSessionKey(encrypted)
	if err != nil {
		return fmt.Errorf("unwrap ProtoKeyResponse key: %w", err)
	}
	if len(p.Session) > 0 {
		clear(p.Session)
	}
	p.Session = key
	p.Pending = make(map[uint64]PendingDamageRequest)
	p.pendingOrder = nil
	p.pendingHead = 0
	p.pendingSequence = 0
	p.consumed = make(map[uint64]consumedResponse)
	p.consumedOrder = nil
	p.consumedSequence = 0
	p.requestCounter = combatRequestCounter{}
	// RPC ids restart with the session: a held response can never pair with a request of the new session.
	p.discardHeldResponses(&p.pairing.sessionChange)
	p.Entities.reset()
	p.PlayerRelevance.Reset()
	p.Effects.EndLiveStateFor(EffectUntrackedSessionChange)
	if p.observer != nil {
		p.observer.BackfillAvailableSummaryCredits()
	}
	p.SourceRoles.reset()
	if p.observer != nil {
		p.observer.ResetPendingSummaryCredits()
	}
	p.activeRequests = make(map[uint16]PendingActiveRequest)
	if p.observer != nil {
		p.observer.BeginProtocolSession()
	}
	p.sessionGeneration++
	fmt.Fprintln(p.Diagnostic, "session_key_ready bytes=32 value=redacted")
	return nil
}

func (p *Processor) HandleDamageRequests(frame GameFrame, body []byte) {
	p.expireHeldResponses()
	outer, err := ParseProtoFields(body)
	if err != nil {
		p.Stats.ParseErrors++
		return
	}
	for _, field := range outer {
		if field.Number != FieldCombatSendPackRequestData || field.Wire != 2 { // sZn.VFs
			continue
		}
		variants, err := ParseProtoFields(field.Data)
		if err != nil {
			p.Stats.ParseErrors++
			continue
		}
		for _, variant := range variants {
			if variant.Number != FieldCombatSendDataRequest || variant.Wire != 2 { // VFs.FFs RequestData; field 5 is U3n
				continue
			}
			requestFields, err := ParseProtoFields(variant.Data)
			if err != nil {
				p.Stats.ParseErrors++
				continue
			}
			// An absent RequestId is the proto3 default 0, which the client's counter sends once per wrap.
			var rpcID uint64
			var u3n []byte
			for _, requestField := range requestFields {
				if requestField.Number == FieldCombatRequestDataRequestID && requestField.Wire == 0 {
					rpcID = requestField.Value
				}
				if requestField.Number == FieldCombatRequestDataDamageExecuteRequest && requestField.Wire == 2 {
					u3n = requestField.Data
				}
			}
			epoch := p.requestCounter.observe(rpcID)
			if u3n == nil {
				continue
			}
			damageFields, err := ParseProtoFields(u3n)
			if err != nil {
				p.Stats.ParseErrors++
				continue
			}
			var damageIDPresent bool
			identity := PendingDamageRequest{}
			for _, damageField := range damageFields {
				switch {
				case damageField.Number == FieldDamageExecuteRequestDamageID && damageField.Wire == 0: // U3n.Fjn DamageId
					damageIDPresent = true
					identity.DamageID = damageField.Value
					identity.HasDamageID = true
				case damageField.Number == FieldDamageExecuteRequestTargetEntityID && damageField.Wire == 0: // DamageExecuteRequest.TargetEntityId
					identity.TargetEntityID = damageField.Value
					identity.HasTargetEntityID = true
				case damageField.Number == FieldDamageExecuteRequestPartID && damageField.Wire == 0: // DamageExecuteRequest.PartId
					identity.PartID = int32(uint32(damageField.Value))
					identity.HasPartID = true
				case damageField.Number == fieldDamageExecuteRequestSkillLevel && damageField.Wire == 0: // DamageExecuteRequest.SkillLevel
					identity.skillLevel = damageField.Value
				case damageField.Number == FieldDamageExecuteRequestAttackerEntityID && damageField.Wire == 0: // DamageExecuteRequest.AttackerEntityId
					identity.AttackerEntityID = damageField.Value
					identity.HasAttackerEntityID = true
				case damageField.Number == fieldDamageExecuteRequestDamageContext && damageField.Wire == 2: // DamageExecuteRequest.DamageContext
					context, err := ParseProtoFields(damageField.Data)
					if err != nil {
						p.Stats.ParseErrors++
						continue
					}
					for _, contextField := range context {
						if contextField.Wire != 0 {
							continue
						}
						switch contextField.Number {
						case FieldDamageContextSourceType: // DamageContext.SourceType
							identity.DamageSourceType = contextField.Value
							identity.HasDamageSourceType = true
						case FieldDamageContextBulletID: // DamageContext.BulletId
							identity.BulletID = int64(contextField.Value)
							identity.HasBulletID = true
						case FieldDamageContextSkillID: // DamageContext.SkillId
							identity.SkillID = contextField.Value
						case FieldDamageContextSkillMessageID: // DamageContext.SkillMessageId
							identity.SkillMessageID = contextField.Value
						}
					}
				}
			}
			if !damageIDPresent {
				continue
			}
			if p.Census != nil {
				p.Census.ObserveInnerEntry("Client->Server DamageExecuteRequest")
			}
			p.Stats.DamageRequests++
			identity.requestEpoch = epoch
			if _, alreadyConsumed := p.consumed[rpcID]; alreadyConsumed {
				// The counter wrapped: the RequestId now names this request. The earlier result keeps its own extended id,
				// and this request's response is paired with this request instead of being taken for a copy.
				delete(p.consumed, rpcID)
				p.Stats.reusedRequestIDs++
				fmt.Fprintf(p.Diagnostic, "rpc_id_reused_after_completion rpc_id=%d epoch=%d\n", rpcID, epoch)
			}
			if _, exists := p.Pending[rpcID]; exists {
				// The earlier request with this RequestId was never answered; it stays unanswered, now superseded.
				p.Stats.supersededRequests++
				fmt.Fprintf(p.Diagnostic, "duplicate_pending_damage_rpc rpc_id=%d epoch=%d\n", rpcID, epoch)
			}
			if p.Masters != nil && identity.HasAttackerEntityID {
				if skill, ok := p.Masters.Skills[identity.SkillID]; ok {
					p.SourceRoles.Observe(identity.AttackerEntityID, skill)
				}
			}
			if p.observer != nil {
				p.observer.WritePartReference(frame, rpcID, identity)
			}
			if identity.HasAttackerEntityID || identity.HasTargetEntityID {
				p.Context.observeClientReference(identity.AttackerEntityID, identity.TargetEntityID)
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

func (p *Processor) rememberPending(rpcID uint64, request PendingDamageRequest) {
	p.pendingSequence++
	request.sequence = p.pendingSequence
	p.Pending[rpcID] = request
	p.pendingOrder = append(p.pendingOrder, pendingRef{rpcID: rpcID, sequence: request.sequence})
	for len(p.Pending) > maxPendingDamageRequests && p.pendingHead < len(p.pendingOrder) {
		ref := p.pendingOrder[p.pendingHead]
		p.pendingHead++
		if current, ok := p.Pending[ref.rpcID]; ok && current.sequence == ref.sequence {
			delete(p.Pending, ref.rpcID)
			p.Stats.EvictedPending++
		}
	}
	p.compactPendingOrder()
}

func (p *Processor) compactPendingOrder() {
	if p.pendingHead < 1024 || p.pendingHead*2 < len(p.pendingOrder) {
		return
	}
	p.pendingOrder = append([]pendingRef(nil), p.pendingOrder[p.pendingHead:]...)
	p.pendingHead = 0
}

func (p *Processor) rememberConsumed(rpcID, digest uint64) {
	p.consumedSequence++
	p.consumed[rpcID] = consumedResponse{sequence: p.consumedSequence, digest: digest, at: p.CurrentFrameTime}
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

// HandleOneDamageResult takes one server DamageExecuteResponse, from either container (handleCombatReceivePacks). A result
// is named by its RequestId within the protocol session and by the wrap of the client's RequestId counter it was sent in
// (extendedRequestID): a response is processed once, with the request of that RequestId, and every further copy is
// counted as a duplicate (same bytes) or a conflicting response (other bytes), never as a second result. Copies arrive
// within consumedCopyWindow of the response they repeat; a response with the RequestId of one processed earlier than that
// answers a later request that reused the RequestId. Equal values never make two responses one.
func (p *Processor) HandleOneDamageResult(rpcID uint64, payload []byte) {
	if consumed, already := p.consumed[rpcID]; already {
		if p.CurrentFrameTime.Sub(consumed.at) <= consumedCopyWindow {
			if consumed.digest == responseDigest(payload) {
				p.Stats.DuplicateResponses++
			} else {
				p.Stats.conflictingResponses++
			}
			return
		}
		// The response of a later request with this RequestId, ahead of its request: the earlier result keeps its own
		// extended id, and this one is held for its request like any response that arrives first.
		delete(p.consumed, rpcID)
		p.Stats.reusedRequestIDs++
	}
	request, matched := p.Pending[rpcID]
	if !matched {
		// The request may still be on its way on the other stream; hold the response instead of dropping it.
		p.holdDamageResponse(rpcID, payload)
		return
	}
	p.pairing.paired++
	fields, err := ParseProtoFields(payload)
	if err != nil {
		p.Stats.ParseErrors++
		return
	}
	var amount int32
	var hasAmount bool
	var changeLife int32
	var hasChangeLife bool
	var errorCode int32
	var immuneType int32
	for _, field := range fields {
		if field.Wire != 0 {
			continue
		}
		switch field.Number {
		case FieldDamageExecuteResponseErrorCode: // lis.Q4n
			errorCode = int32(uint32(field.Value))
		case FieldDamageExecuteResponseImmuneType: // lis.lAs
			immuneType = int32(uint32(field.Value))
		case FieldDamageExecuteResponseChangeLife: // lis.jQ_, client-mapped ChangeLife
			changeLife = int32(uint32(field.Value))
			hasChangeLife = true
		case FieldDamageExecuteResponseDamage: // lis.nAs
			amount = int32(uint32(field.Value))
			hasAmount = true
		}
	}
	if !hasAmount || errorCode != 0 || immuneType != 0 {
		p.Stats.InvalidResponses++
		p.rememberConsumed(rpcID, responseDigest(payload))
		delete(p.Pending, rpcID)
		if p.observer != nil {
			p.observer.WriteNoAmountEvent(rpcID, request, hasAmount, errorCode, immuneType, hasChangeLife, changeLife)
		}
		return
	}

	p.rememberConsumed(rpcID, responseDigest(payload))
	delete(p.Pending, rpcID)
	value := int64(amount)
	if value < 0 {
		value = -value
	}
	attackerStatus := ResolutionUnknown
	if request.HasAttackerEntityID && request.AttackerEntityID != 0 {
		_, attackerStatus = p.Entities.Resolve(request.AttackerEntityID)
		if attackerStatus == ResolutionVerified || attackerStatus == ResolutionCorrelated {
			p.Stats.ObservedSourceEntity++
		}
	}
	targetStatus := ResolutionUnknown
	if request.HasTargetEntityID && request.TargetEntityID != 0 {
		_, targetStatus = p.Entities.Resolve(request.TargetEntityID)
		if targetStatus == ResolutionVerified || targetStatus == ResolutionCorrelated {
			p.Stats.ObservedTargetEntity++
		}
	}
	if !request.HasAttackerEntityID || request.AttackerEntityID == 0 {
		p.Stats.MissingAttackerEntityID++
	}
	event := p.BuildDamageEvent(rpcID, request, amount, changeLife, hasChangeLife, value)
	switch event.EventTypeCandidate {
	case "DAMAGE_CANDIDATE":
		p.Stats.DamageCandidateEvents++
		p.Stats.DamageCandidateResult += value
	case "HEALING_CANDIDATE":
		p.Stats.HealingCandidateEvents++
		p.Stats.HealingCandidateResult += value
	case "OTHER_COMBAT_RESULT_CANDIDATE":
		p.Stats.OtherCandidateEvents++
		p.Stats.OtherCandidateResult += value
	default:
		p.Stats.UnclassifiedEvents++
		p.Stats.UnclassifiedResult += value
	}
	if event.CharacterStatus == ResolutionVerified {
		p.Stats.VerifiedCharacters++
		p.Stats.characterResolved++
	} else if event.CharacterStatus == ResolutionCorrelated {
		p.Stats.CorrelatedCharacters++
		p.Stats.characterResolved++
	}
	if event.Target.HasMonster {
		p.Stats.MonsterResolved++
	}
	if event.SkillStatus == ResolutionVerified || event.SkillStatus == ResolutionCorrelated {
		p.Stats.SkillResolved++
	}
	if event.CharacterStatus == ResolutionUnknown || event.Target.MonsterStatus == ResolutionUnknown || event.SkillStatus == ResolutionUnknown {
		p.Stats.UnknownDamageEvents++
	}
	p.Stats.DecodedMatchedEvents++
	p.Stats.DecodedCombatResultAbs += value
	if p.WriteDamageEvent(event) {
		if p.observer != nil {
			p.observer.ObserveEncounterSummary(event)
		}
	}
}

func DecryptGameBody(sequence uint32, body, sessionKey []byte) ([]byte, error) {
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

func DecompressBody(compressed []byte, expected uint32) ([]byte, error) {
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

func ParseProtoFields(data []byte) ([]ProtoField, error) {
	fields := make([]ProtoField, 0, 8)
	for offset := 0; offset < len(data); {
		tag, n := binary.Uvarint(data[offset:])
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid Protobuf tag")
		}
		offset += n
		field := ProtoField{Number: tag >> 3, Wire: tag & 7}
		switch field.Wire {
		case 0:
			value, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return nil, errors.New("invalid Protobuf varint")
			}
			field.Value = value
			offset += n
		case 1:
			if len(data)-offset < 8 {
				return nil, errors.New("truncated Protobuf fixed64")
			}
			field.Data = data[offset : offset+8]
			offset += 8
		case 2:
			length, n := binary.Uvarint(data[offset:])
			if n <= 0 || length > uint64(len(data)-offset-n) {
				return nil, errors.New("invalid Protobuf byte length")
			}
			offset += n
			field.Data = data[offset : offset+int(length)]
			offset += int(length)
		case 5:
			if len(data)-offset < 4 {
				return nil, errors.New("truncated Protobuf fixed32")
			}
			field.Data = data[offset : offset+4]
			offset += 4
		default:
			return nil, fmt.Errorf("unsupported Protobuf wire type %d", field.Wire)
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// startProtocolSessionStatistics closes the previous protocol session's encounters at a game handshake and, for an
// application host, starts a new statistics interval for the new protocol session.
func (p *Processor) startProtocolSessionStatistics(at time.Time) {
	if p.DamageStatistics == nil {
		return
	}
	p.DamageStatistics.observeSessionChange(at)
	if p.observer != nil {
		p.observer.FlushEncounterTransitions()
	}
	p.DamageStatistics.endProtocolSession()
	if p.IntervalPerProtocolSession && p.SessionNumber > 1 {
		p.DamageStatistics.Reset()
	}
}
