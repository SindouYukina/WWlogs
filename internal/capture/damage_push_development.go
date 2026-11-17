//go:build wwdev

package capture

import ()

// damagePushSchemaVersion identifies the layout of damagePushRecord.
const damagePushSchemaVersion = 1

// damagePushRecord is one DamageExecuteNotify pushed inside a combat notify. It is
// a separate damage stream from the damage RPC: the push carries its own attacker
// and target identifiers and is not paired with a request. Attacker identity is
// reported as observed; no character or summoner attribution is inferred.
type damagePushRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrameOrdinal  uint64 `json:"frameOrdinal"`
	SessionNumber int    `json:"sessionNumber"`
	Timestamp     string `json:"timestamp"`
	DamageID      int64  `json:"damageId"`
	AttackerID    int64  `json:"attackerEntityId"`
	TargetID      int64  `json:"targetEntityId"`
	Damage        int64  `json:"damage"`
	ChangeLife    int64  `json:"changeLife"`
	ElementType   int64  `json:"elementType"`
	PartID        int64  `json:"partId"`
	IsCrit        bool   `json:"isCrit"`
	KilledTarget  bool   `json:"killedTarget"`
	Stream        string `json:"stream"`
	Evidence      string `json:"evidence"`
	// ElementName names the protocol ElementType in the master locale; DamageElement and ClassCandidate come from the DamageId row.
	ElementName    string                   `json:"elementName,omitempty"`
	DamageElement  *combatEventElementJSONL `json:"damageElement,omitempty"`
	ClassCandidate string                   `json:"classCandidate,omitempty"`
	ClassStatus    ResolutionStatus         `json:"classStatus,omitempty"`
	CalculateType  *int32                   `json:"calculateType,omitempty"`
}

// archiveDamagePushes writes DamageExecuteNotify pushes from one decoded
// CombatReceivePackNotify. It reads the schema tree only and does not change the
// typed damage results.
func (p *Processor) archiveDamagePushes(frame GameFrame, decoded *decodedMessage) {
	if p.Dev.damagePushOut == nil || decoded == nil {
		return
	}
	for _, data := range childMessages(decoded, "Data") {
		for _, notify := range childMessages(data, "CombatNotifyData") {
			for _, push := range childMessages(notify, "DamageExecuteNotify") {
				record := damagePushRecord{
					SchemaVersion: damagePushSchemaVersion,
					FrameOrdinal:  p.Dev.frameOrdinal - 1,
					SessionNumber: p.SessionNumber,
					Timestamp:     formatTimestamp(frame.Timestamp),
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
				if p.Masters != nil {
					record.ElementName = p.Masters.ElementNames[int32(record.ElementType)]
					record.DamageElement = resolveDamageElement(p.Masters, uint64(record.DamageID), record.DamageID > 0)
					classified := CombatEvent{DamageID: uint64(record.DamageID), HasDamageID: record.DamageID > 0}
					classified.ResolveEventType(p.Masters)
					record.ClassCandidate, record.ClassStatus = classified.EventTypeCandidate, classified.EventTypeStatus
					record.CalculateType = optionalInt32(classified.DamageCalculateType, classified.HasCalculateType)
				}
				crit, _ := scalarValue(push, "IsCrit")
				record.IsCrit = crit != 0
				killed, _ := scalarValue(push, "KilledTarget")
				record.KilledTarget = killed != 0
				writeResearchRecord(p.Dev.damagePushOut, record, &p.Dev.damagePushErrors)
			}
		}
	}
}

// ArchiveDamagePushFrame decodes a combat frame for the push stream when the output
// is enabled. It shares the plaintext with the other schema observers.
func (p *Processor) ArchiveDamagePushFrame(frame GameFrame, plain []byte) {
	if p.Dev.damagePushOut == nil || p.Dev.protocolSchema == nil || (frame.CommandID != CmdCombatReceivePackNotify && frame.CommandID != CmdCombatSendPackResponse) {
		return
	}
	payloads, err := ReceivePackNotifyBodies(frame.CommandID, plain)
	if err != nil {
		p.Dev.damagePushErrors++
		return
	}
	for _, payload := range payloads {
		decoded, err := decodeSchemaMessage(p.Dev.protocolSchema, "CombatReceivePackNotify", payload, 0)
		if err != nil {
			p.Dev.damagePushErrors++
			continue
		}
		p.archiveDamagePushes(frame, decoded)
	}
}
