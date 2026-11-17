//go:build !wwdev

package capture

import "time"

// Public builds have no diagnostic outputs. Each function below is implemented in a wwdev file
// and is a no-op here so that shared code can call it unconditionally.

func (p *Processor) WriteStructuredEventJSONL(event any) {
}

func (p *Processor) WriteDamageEventOutputs(event *CombatEvent, visible bool, sourceRelation, targetRelation PlayerRelation) {
}

func (p *Processor) RecordBuffSnapshotDiagnostics(frame GameFrame, record RawBuffSnapshot, request PendingActiveRequest, matched bool) {
}

func (p *Processor) ArchiveClientPushResearch(frame GameFrame, plain []byte) {
}

func (p *Processor) WriteBattleStateSignal(at time.Time, container string, signal PlayerBattleStateSignal, anchor string) {
}

func (p *Processor) WriteCombatEventRecord(record CombatEventRecord, assignment string) {
}

func (p *Processor) FlushEncounterTransitions() {
}

func (p *Processor) WriteBuffLifecycleConsole(row BuffLifecycleAuditRow) {
}

func (p *Processor) WriteBuffLifecycleRow(row *BuffLifecycleAuditRow) {
}

func (p *Processor) AuditCombatNotify(frame GameFrame, notification ProtoField, commonEntityID, commonOriginator, commonMessageID *uint64) {
}

func (p *Processor) ObserveBuffInterval(row BuffLifecycleAuditRow) {
}

func (p *Processor) ArchiveFrame(frame GameFrame) {
}

func (p *Processor) ArchiveUndecodedSchema(frame GameFrame) {
}

func (p *Processor) ArchiveSchemaDecode(frame GameFrame, plain []byte) {
}

func (p *Processor) ArchiveDamageRPCSchema(frame GameFrame, plain []byte) {
}

func (p *Processor) ArchiveHPStateFrame(frame GameFrame, plain []byte) {
}

func (p *Processor) ArchiveCombatStateFrame(frame GameFrame, plain []byte) {
}

func (p *Processor) ArchiveDamagePushFrame(frame GameFrame, plain []byte) {
}

func (p *Processor) ArchiveBulletRefReceiveFrame(frame GameFrame, plain []byte) {
}

func (p *Processor) ArchiveSummonOwnerFrame(frame GameFrame, plain []byte) {
}

func (p *Processor) BackfillAvailableSummaryCredits() {
}

func (p *Processor) ResetPendingSummaryCredits() {
}

func (p *Processor) BackfillEncounterSummary(sourceID, roleID uint64) {
}

func (p *Processor) RemovePendingSummaryCredits(sourceID uint64) {
}

func (p *Processor) WritePartReference(frame GameFrame, rpcID uint64, request PendingDamageRequest) {
}

func (p *Processor) WriteNoAmountEvent(rpcID uint64, request PendingDamageRequest, hasAmount bool, errorCode, immuneType int32, hasChangeLife bool, changeLife int32) {
}

func (p *Processor) ObserveEncounterSummary(event CombatEvent) {
}
