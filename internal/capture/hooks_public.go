//go:build !wwdev

package capture

import "time"

// Public builds have no diagnostic outputs. Each function below is implemented in a wwdev file
// and is a no-op here so that shared code can call it unconditionally.

func (p *processor) writeStructuredEventJSONL(event any) {
}

func (p *processor) writeDamageEventOutputs(event *combatEvent, visible bool, sourceRelation, targetRelation playerRelation) {
}

func (p *processor) recordBuffSnapshotDiagnostics(frame gameFrame, record rawBuffSnapshot, request pendingActiveRequest, matched bool) {
}

func (p *processor) archiveClientPushResearch(frame gameFrame, plain []byte) {
}

func (p *processor) writeBattleStateSignal(at time.Time, container string, signal playerBattleStateSignal, anchor string) {
}

func (p *processor) writeCombatEventRecord(record CombatEventRecord, assignment string) {
}

func (p *processor) flushEncounterTransitions() {
}

func (p *processor) writeBuffLifecycleConsole(row buffLifecycleAuditRow) {
}

func (p *processor) writeBuffLifecycleRow(row *buffLifecycleAuditRow) {
}

func (p *processor) auditCombatNotify(frame gameFrame, notification protoField, commonEntityID, commonOriginator, commonMessageID *uint64) {
}

func (p *processor) observeBuffInterval(row buffLifecycleAuditRow) {
}

func (p *processor) archiveFrame(frame gameFrame) {
}

func (p *processor) archiveUndecodedSchema(frame gameFrame) {
}

func (p *processor) archiveSchemaDecode(frame gameFrame, plain []byte) {
}

func (p *processor) archiveDamageRPCSchema(frame gameFrame, plain []byte) {
}

func (p *processor) archiveHPStateFrame(frame gameFrame, plain []byte) {
}

func (p *processor) archiveCombatStateFrame(frame gameFrame, plain []byte) {
}

func (p *processor) archiveDamagePushFrame(frame gameFrame, plain []byte) {
}

func (p *processor) archiveBulletRefReceiveFrame(frame gameFrame, plain []byte) {
}

func (p *processor) archiveSummonOwnerFrame(frame gameFrame, plain []byte) {
}

func (p *processor) backfillAvailableSummaryCredits() {
}

func (p *processor) resetPendingSummaryCredits() {
}

func (p *processor) backfillEncounterSummary(sourceID, roleID uint64) {
}

func (p *processor) removePendingSummaryCredits(sourceID uint64) {
}

func (p *processor) writePartReference(frame gameFrame, rpcID uint64, request pendingDamageRequest) {
}

func (p *processor) writeNoAmountEvent(rpcID uint64, request pendingDamageRequest, hasAmount bool, errorCode, immuneType int32, hasChangeLife bool, changeLife int32) {
}

func (p *processor) observeEncounterSummary(event combatEvent) {
}
