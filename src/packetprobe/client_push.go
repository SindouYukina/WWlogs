package main

// clientPushSchemaVersion identifies the layout of clientPushRecord.
const clientPushSchemaVersion = 1

// clientPushRecord is one client-reported push that is not a server notify. The
// client reports its own current role (ClientCurrentRoleReportPush) and the end
// of its skills (EndSkillPush). These are the observable evidence for character
// switches and skill use; the server does not send a separate switch notify in
// the captures examined so far.
type clientPushRecord struct {
	SchemaVersion int      `json:"schemaVersion"`
	FrameOrdinal  uint64   `json:"frameOrdinal"`
	SessionNumber int      `json:"sessionNumber"`
	Timestamp     string   `json:"timestamp"`
	PushName      string   `json:"pushName"`
	ActiveRoleID  *int64   `json:"activeRoleId,omitempty"`
	CurrentRoleID *int64   `json:"currentRoleId,omitempty"`
	EntityID      *int64   `json:"currentEntityId,omitempty"`
	SkillID       *int64   `json:"skillId,omitempty"`
	SkillTime     *float64 `json:"clientSkillTime,omitempty"`
	SkillRoleKey  *int64   `json:"skillRolePrefix,omitempty"`
	Evidence      string   `json:"evidence"`
}

// archiveClientPushFrame decodes a combat send frame for the client-push output.
// It runs on the same plaintext as the other observers.
func (p *processor) archiveClientPushFrame(frame gameFrame, plain []byte) {
	if p.clientPushOut == nil || p.protocolSchema == nil || frame.commandID != 19470 {
		return
	}
	decoded, err := decodeSchemaMessage(p.protocolSchema, "CombatSendPackRequest", plain, 0)
	if err != nil {
		p.clientPushErrors++
		return
	}
	p.archiveClientPushes(frame, decoded)
}
