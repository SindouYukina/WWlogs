package main

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
	skillLevel          uint64 // DamageExecuteRequest.SkillLevel, field 12
	skillID             uint64 // DamageContext.SkillId, field 4
	skillMessageID      uint64 // DamageContext.SkillMessageId, field 5
	damageID            uint64 // DamageExecuteRequest.DamageId, field 9
}

type processorStats struct {
	handshakeFrames         int
	handshakeOK             int
	handshakeErrors         int
	damageRequests          int
	candidateResponses      int
	printed                 int
	unpairedResponses       int
	invalidResponses        int
	duplicateResponses      int
	missingAttackerEntityID int
	decryptErrors           int
	compressedErrors        int
	parseErrors             int
	otherFrameDirections    int
	sumAbsDamage            int64
}

type processor struct {
	rsa               *staticRSA
	session           []byte
	sessionGeneration uint64
	pending           map[uint64]pendingDamageRequest
	consumed          map[uint64]struct{}
	stdout            io.Writer
	diagnostic        io.Writer
	audit             io.Writer
	stats             processorStats
}

func newProcessor(rsaKey *staticRSA, stdout, diagnostic, audit io.Writer) *processor {
	return &processor{
		rsa:        rsaKey,
		pending:    make(map[uint64]pendingDamageRequest),
		consumed:   make(map[uint64]struct{}),
		stdout:     stdout,
		diagnostic: diagnostic,
		audit:      audit,
	}
}

func parseFrames(buffer []byte, direction string, spans []applicationSpan, p *processor) (int, int, int, int) {
	frames, badCRC, malformed := 0, 0, 0
	consumed := 0
	for len(buffer) >= 3 {
		payloadLen := int(binary.LittleEndian.Uint16(buffer[:2]))
		total := payloadLen + 3
		if total < 14 {
			buffer = buffer[1:]
			consumed++
			malformed++
			continue
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
			buffer = buffer[total:]
			consumed += total
			malformed++
			continue
		}
		commandID := binary.LittleEndian.Uint16(frame[offset : offset+2])
		receivedCRC := binary.LittleEndian.Uint32(frame[offset+2 : offset+6])
		bodyStart := offset + 6
		var compressedLength uint32
		if compressed {
			if bodyStart+4 > total {
				buffer = buffer[total:]
				consumed += total
				malformed++
				continue
			}
			compressedLength = binary.LittleEndian.Uint32(frame[bodyStart : bodyStart+4])
			bodyStart += 4
		}
		if crc32IEEE(frame[bodyStart:total]) != receivedCRC {
			buffer = buffer[total:]
			consumed += total
			badCRC++
			continue
		}
		if bodyStart > total {
			buffer = buffer[total:]
			consumed += total
			malformed++
			continue
		}
		current := gameFrame{
			direction:        direction,
			messageType:      messageType,
			sequence:         binary.LittleEndian.Uint32(frame[4:8]),
			commandID:        commandID,
			compressed:       compressed,
			compressedLength: compressedLength,
			body:             append([]byte(nil), frame[bodyStart:total]...),
			timestamp:        applicationTimestampAt(spans, consumed),
		}
		p.handleFrame(current)
		frames++
		buffer = buffer[total:]
		consumed += total
	}
	return frames, badCRC, malformed, consumed
}

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

func (p *processor) handleFrame(frame gameFrame) {
	if frame.commandID == 112 && frame.direction == "in" {
		p.stats.handshakeFrames++
		if err := p.handleProtoKeyResponse(frame.body); err != nil {
			p.stats.handshakeErrors++
			return
		}
		p.stats.handshakeOK++
		return
	}
	if frame.commandID != 19470 && frame.commandID != 20332 {
		return
	}
	if (frame.commandID == 19470 && frame.direction != "out") || (frame.commandID == 20332 && frame.direction != "in") {
		p.stats.otherFrameDirections++
		return
	}
	if len(p.session) != 32 {
		return
	}
	plain, err := decryptGameBody(frame.sequence, frame.body, p.session)
	if err != nil {
		p.stats.decryptErrors++
		return
	}
	if frame.compressed {
		plain, err = decompressBody(plain, frame.compressedLength)
		if err != nil {
			p.stats.compressedErrors++
			return
		}
	}
	if frame.commandID == 19470 {
		p.handleDamageRequests(plain)
	} else {
		p.handleDamageResponses(plain)
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
	p.sessionGeneration++
	fmt.Fprintln(p.diagnostic, "session_key_ready bytes=32 value=redacted")
	return nil
}

func (p *processor) handleDamageRequests(body []byte) {
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
				case damageField.number == 10 && damageField.wire == 0: // DamageExecuteRequest.TargetEntityId
					identity.targetEntityID = damageField.value
					identity.hasTargetEntityID = true
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
			p.stats.damageRequests++
			if _, alreadyConsumed := p.consumed[rpcID]; alreadyConsumed {
				fmt.Fprintf(p.diagnostic, "rpc_id_reused_after_completion rpc_id=%d\n", rpcID)
			}
			if _, exists := p.pending[rpcID]; exists {
				fmt.Fprintf(p.diagnostic, "duplicate_pending_damage_rpc rpc_id=%d\n", rpcID)
			}
			p.pending[rpcID] = identity
		}
	}
}

func (p *processor) handleDamageResponses(body []byte) {
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
		p.stats.unpairedResponses++
		return
	}
	fields, err := parseProtoFields(payload)
	if err != nil {
		p.stats.parseErrors++
		return
	}
	var amount int32
	var hasAmount bool
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
		case 10: // lis.nAs
			amount = int32(uint32(field.value))
			hasAmount = true
		}
	}
	if !hasAmount || errorCode != 0 || immuneType != 0 {
		p.stats.invalidResponses++
		return
	}

	p.consumed[rpcID] = struct{}{}
	delete(p.pending, rpcID)
	value := int64(amount)
	if value < 0 {
		value = -value
	}
	if !request.hasAttackerEntityID || request.attackerEntityID == 0 {
		p.stats.missingAttackerEntityID++
		if _, err := fmt.Fprintf(p.stdout, "Unknown [Char: ?] [Entity: ?] %d\n", value); err != nil {
			fmt.Fprintf(p.diagnostic, "stdout_write_error=%v\n", err)
			return
		}
	} else {
		if _, err := fmt.Fprintf(p.stdout, "Unknown [Char: ?] [Entity: %d] %d\n", request.attackerEntityID, value); err != nil {
			fmt.Fprintf(p.diagnostic, "stdout_write_error=%v\n", err)
			return
		}
	}
	if p.audit != nil {
		attackerEntity := "?"
		if request.hasAttackerEntityID && request.attackerEntityID != 0 {
			attackerEntity = fmt.Sprintf("%d", request.attackerEntityID)
		}
		targetEntity := "?"
		if request.hasTargetEntityID && request.targetEntityID != 0 {
			targetEntity = fmt.Sprintf("%d", request.targetEntityID)
		}
		if _, err := fmt.Fprintf(p.audit, "%d\t%s\t%s\t%d\t%d\t%d\t%d\t%d\n", rpcID, attackerEntity, targetEntity, request.skillLevel, request.skillID, request.skillMessageID, request.damageID, value); err != nil {
			fmt.Fprintf(p.diagnostic, "event_audit_write_error=%v\n", err)
		}
	}
	if flusher, ok := p.stdout.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			fmt.Fprintf(p.diagnostic, "stdout_flush_error=%v\n", err)
			return
		}
	}
	p.stats.printed++
	p.stats.sumAbsDamage += value
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
