//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
)

func (p *processor) rememberBulletHandle(message *decodedMessage, record bulletRefRecord) {
	handle := firstChildMessage(message, "Handle")
	handleID, ok := scalarValue(handle, "HandleId")
	if !ok || handleID <= 0 {
		return
	}
	if p.dev.bulletHandles == nil {
		p.dev.bulletHandles = make(map[bulletHandleKey]bulletHandleEntry)
	}
	key := bulletHandleKey{p.sessionNumber, handleID}
	if _, exists := p.dev.bulletHandles[key]; !exists {
		p.dev.bulletHandleOrder = append(p.dev.bulletHandleOrder, key)
	}
	entry := bulletHandleEntry{bulletID: record.BulletID, status: record.Status, namespace: record.Namespace, actor: record.ActorBlueprint, via: record.Via, label: record.DevLabelZhCn}
	if record.SkillID != nil {
		entry.skillID, entry.hasSkill = *record.SkillID, true
	}
	p.dev.bulletHandles[key] = entry
	for len(p.dev.bulletHandleOrder) > maxBulletHandles {
		delete(p.dev.bulletHandles, p.dev.bulletHandleOrder[0])
		p.dev.bulletHandleOrder = p.dev.bulletHandleOrder[1:]
	}
}

// resolveBulletHandle translates a value that is a bullet handle id rather than a bullet row key.
// It requires an earlier CreateBullet in the same session under that handle id with the same SkillId.
func (p *processor) resolveBulletHandle(record *bulletRefRecord) {
	handleID := record.BulletID
	record.HandleID = &handleID
	entry, ok := p.dev.bulletHandles[bulletHandleKey{p.sessionNumber, handleID}]
	switch {
	case !ok:
		record.Evidence = "InterruptSkillInfo.BulletId is not the HandleId of any earlier CreateBullet in this session; the value is not a static bullet row key"
	case entry.hasSkill && record.SkillID != nil && entry.skillID != *record.SkillID:
		record.Evidence = "the handle id matches an earlier CreateBullet whose SkillId differs from this message; the match is rejected"
	default:
		resolved := entry.bulletID
		record.ResolvedBulletID = &resolved
		record.Status, record.Namespace, record.ActorBlueprint, record.DevLabelZhCn = entry.status, "ActiveBulletHandle.HandleId", entry.actor, entry.label
		record.Via = "InterruptSkillInfo.BulletId equals the Handle.HandleId of an earlier CreateBullet in this session"
		if entry.via != "" {
			record.Via += "; " + entry.via
		}
		record.Evidence = "the field holds a runtime bullet handle (identity VERIFIED by exact handle-id and SkillId match); the static bullet it points to inherits the CreateBullet translation status " + string(entry.status)
	}
}

// observeBulletReferences walks one CombatSendData for every BulletId field, at any
// depth, and resolves it through the source entity's actor chain. The source entity
// comes from a fixed per-message rule (see bulletSourceCandidates), not from trying
// several fields until one resolves.
func (p *processor) observeBulletReferences(frame gameFrame, data *decodedMessage, direction string) {
	p.walkBulletReferences(frame, data, "Data", nil, nil, direction)
}

// archiveBulletRefReceiveFrame handles standalone and response-embedded inbound combat notifications.
func (p *processor) archiveBulletRefReceiveFrame(frame gameFrame, plain []byte) {
	if (p.dev.bulletRefOut == nil && p.dev.skillRefOut == nil && p.dev.shieldRefOut == nil) || p.dev.protocolSchema == nil || (frame.commandID != cmdCombatReceivePackNotify && frame.commandID != cmdCombatSendPackResponse) {
		return
	}
	payloads, err := receivePackNotifyBodies(frame.commandID, plain)
	if err != nil {
		if p.dev.bulletRefOut != nil {
			p.dev.bulletRefErrors++
		}
		if p.dev.skillRefOut != nil {
			p.dev.skillRefErrors++
		}
		if p.dev.shieldRefOut != nil {
			p.dev.shieldRefErrors++
		}
		return
	}
	for _, payload := range payloads {
		decoded, err := decodeSchemaMessage(p.dev.protocolSchema, "CombatReceivePackNotify", payload, 0)
		if err != nil {
			if p.dev.bulletRefOut != nil {
				p.dev.bulletRefErrors++
			}
			if p.dev.skillRefOut != nil {
				p.dev.skillRefErrors++
			}
			if p.dev.shieldRefOut != nil {
				p.dev.shieldRefErrors++
			}
			continue
		}
		for _, data := range childMessages(decoded, "Data") {
			if p.dev.bulletRefOut != nil {
				p.observeBulletReferences(frame, data, "in")
			}
			if p.dev.skillRefOut != nil {
				p.observeSkillReferences(frame, data, "in")
			}
			if p.dev.shieldRefOut != nil {
				p.observeShieldReferences(frame, data, "in")
			}
		}
	}
}

func (p *processor) walkBulletReferences(frame gameFrame, message *decodedMessage, path string, parent, common *decodedMessage, direction string) {
	if message == nil {
		return
	}
	if own := firstChildMessage(message, "CombatCommon"); own != nil {
		common = own
	}
	if bulletID, ok := scalarValue(message, "BulletId"); ok && bulletID > 0 {
		p.writeBulletReference(frame, message, parent, path+".BulletId", bulletID, common, direction)
	}
	for _, field := range message.Fields {
		if field.Message != nil {
			p.walkBulletReferences(frame, field.Message, path+"."+field.Name, message, common, direction)
		}
	}
}

// bulletSourceCandidates returns the ordered entity-id sources for the message that holds a
// BulletId. The rule is fixed by message type: CreateBulletPush names its OwnerEntityId (else the
// sender in CombatCommon), DodgeInfoPush names BulletOwnerId, DamageContext sits under a
// DamageExecuteRequest or DamageExecuteNotify whose AttackerEntityId fired the bullet, and HitInformation.Id is the
// hitting entity (it equals the paired DamageExecuteRequest.AttackerEntityId in 1,563 of 1,815
// saved pairs; the rest differ and are therefore CORRELATED, not VERIFIED). Any other holder
// falls back to the enclosing CombatCommon.EntityId and says so in the basis.
func bulletSourceCandidates(message, parent, common *decodedMessage) []bulletSourceCandidate {
	var out []bulletSourceCandidate
	add := func(basis string, source *decodedMessage, name string) {
		if value, ok := scalarValue(source, name); ok && value > 0 {
			out = append(out, bulletSourceCandidate{basis, value})
		}
	}
	switch message.Type {
	case "CreateBulletPush", "CreateBulletRequest", "CreateBulletNotify":
		add(message.Type+".OwnerEntityId", message, "OwnerEntityId")
		add("CombatCommon.EntityId", common, "EntityId")
	case "DamageRecordNotify":
		add("DamageRecordNotify.Attacker", message, "Attacker")
	case "InterruptSkillInfo", "AwakeDynamicPortalRequest", "TargetGearHitRequest":
		add(message.Type+".EntityId", message, "EntityId")
	case "DodgeInfoPush":
		add("DodgeInfoPush.BulletOwnerId", message, "BulletOwnerId")
	case "DamageContext":
		if parent != nil {
			add(parent.Type+".AttackerEntityId", parent, "AttackerEntityId")
		}
	case "HitInformation":
		add("HitInformation.Id", message, "Id")
	default:
		add("CombatCommon.EntityId (default)", common, "EntityId")
	}
	return out
}

func (p *processor) writeBulletReference(frame gameFrame, message, parent *decodedMessage, fieldPath string, bulletID int64, common *decodedMessage, direction string) {
	record := bulletRefRecord{
		SchemaVersion: bulletRefSchemaVersion,
		FrameOrdinal:  p.dev.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Timestamp:     formatTimestamp(frame.timestamp),
		Direction:     direction,
		FieldPath:     strings.TrimPrefix(fieldPath, "Data."),
		BulletID:      bulletID,
		Status:        resolutionUnknown,
	}
	var skillID uint64
	if value, ok := scalarValue(message, "SkillId"); ok && value > 0 {
		record.SkillID = &value
		skillID = uint64(value)
	}
	if value, ok := scalarValue(message, "DtType"); ok {
		record.DtType = &value
	}
	candidates := bulletSourceCandidates(message, parent, common)
	var identity entityIdentity
	found := false
	if len(candidates) != 0 {
		candidate := candidates[0]
		id := candidate.id
		record.SourceEntityID, record.SourceBasis = &id, candidate.basis
		resolved := resolveEntityIdentity(p.entities, p.masters, uint64(candidate.id))
		if resolved.RuntimeStatus != resolutionUnknown {
			identity, found = resolved, true
		}
	}
	switch {
	case message.Type == "InterruptSkillInfo":
		p.resolveBulletHandle(&record)
	case p.masters == nil:
		record.Evidence = "no master database loaded"
	default:
		source := bulletSource{SkillID: skillID}
		if found {
			source = newBulletSource(identity, skillID, p.entities.visionSummonConfigs(uint64(*record.SourceEntityID)))
		}
		if resolution := resolveBullet(p.masters, uint64(bulletID), source); resolution != nil {
			record.Status, record.Namespace, record.ActorBlueprint, record.Via = resolution.Status, resolution.Namespace, resolution.ActorBlueprint, resolution.Via
			record.DevLabelZhCn, record.Evidence = resolution.DevLabelZh, resolution.Proof
			record.DevLabel = resolution.DevLabel
		}
	}
	if message.Type == "CreateBulletPush" || message.Type == "CreateBulletNotify" {
		p.rememberBulletHandle(message, record)
	}
	line, err := json.Marshal(record)
	if err != nil {
		p.dev.bulletRefErrors++
		return
	}
	if _, err := p.dev.bulletRefOut.Write(append(line, '\n')); err != nil {
		p.dev.bulletRefErrors++
	}
}
