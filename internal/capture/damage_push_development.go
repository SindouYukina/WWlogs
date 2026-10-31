//go:build wwdev

package capture

import (
	"encoding/json"
)

// archiveDamagePushes writes DamageExecuteNotify pushes from one decoded
// CombatReceivePackNotify. It reads the schema tree only and does not change the
// typed damage results.
func (p *processor) archiveDamagePushes(frame gameFrame, decoded *decodedMessage) {
	if (p.damagePushOut == nil && p.combatEventsJSONL == nil) || decoded == nil {
		return
	}
	for _, data := range childMessages(decoded, "Data") {
		for _, notify := range childMessages(data, "CombatNotifyData") {
			for _, push := range childMessages(notify, "DamageExecuteNotify") {
				record := damagePushRecord{
					SchemaVersion: damagePushSchemaVersion,
					FrameOrdinal:  p.frameOrdinal - 1,
					SessionNumber: p.sessionNumber,
					Timestamp:     formatTimestamp(frame.timestamp),
					Stream:        "DAMAGE_EXECUTE_NOTIFY_PUSH",
					Evidence:      "pushed DamageExecuteNotify; not paired with a damage RPC request; attacker identity as observed",
				}
				record.DamageID, _ = scalarValue(push, "DamageId")
				record.AttackerID, _ = scalarValue(push, "AttackerEntityId")
				record.TargetID, _ = scalarValue(push, "TargetEntityId")
				record.Damage, _ = scalarValue(push, "Damage")
				record.ChangeLife, _ = scalarValue(push, "ChangeLife")
				record.ElementType, _ = scalarValue(push, "ElementType")
				record.PartID, _ = scalarValue(push, "PartId")
				if p.masters != nil {
					record.ElementName = p.masters.elementNames[int32(record.ElementType)]
					record.DamageElement = resolveDamageElement(p.masters, uint64(record.DamageID), record.DamageID > 0)
					classified := combatEvent{DamageID: uint64(record.DamageID), HasDamageID: record.DamageID > 0}
					classified.resolveEventType(p.masters)
					record.ClassCandidate, record.ClassStatus = classified.EventTypeCandidate, classified.EventTypeStatus
					record.CalculateType = optionalInt32(classified.DamageCalculateType, classified.HasCalculateType)
				}
				crit, _ := scalarValue(push, "IsCrit")
				record.IsCrit = crit != 0
				killed, _ := scalarValue(push, "KilledTarget")
				record.KilledTarget = killed != 0
				p.writePushDamageEvent(record)
				if p.damagePushOut == nil {
					continue
				}
				line, err := json.Marshal(record)
				if err != nil {
					p.damagePushErrors++
					continue
				}
				if _, err := p.damagePushOut.Write(append(line, '\n')); err != nil {
					p.damagePushErrors++
				}
			}
		}
	}
}

// archiveDamagePushFrame decodes a combat frame for the push stream when the output
// is enabled. It shares the plaintext with the other schema observers.
func (p *processor) archiveDamagePushFrame(frame gameFrame, plain []byte) {
	if (p.damagePushOut == nil && p.combatEventsJSONL == nil) || p.protocolSchema == nil || (frame.commandID != 25706 && frame.commandID != 20332) {
		return
	}
	payloads, err := receivePackNotifyBodies(frame.commandID, plain)
	if err != nil {
		p.damagePushErrors++
		return
	}
	for _, payload := range payloads {
		decoded, err := decodeSchemaMessage(p.protocolSchema, "CombatReceivePackNotify", payload, 0)
		if err != nil {
			p.damagePushErrors++
			continue
		}
		p.archiveDamagePushes(frame, decoded)
	}
}
