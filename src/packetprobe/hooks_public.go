//go:build !wwdev

package main

import (
	"io"
)

// Public builds have no diagnostic outputs. Each function below is implemented in a wwdev file
// and is a no-op here so that shared code can call it unconditionally.

func (p *processor) writeStructuredEventJSONL(event any) {
}

func writeCombatNotifyAudit(w io.Writer, frame gameFrame, notification protoField, commonEntityID, commonOriginator, commonMessageID *uint64, sessionNumber int) error {
	var r0 error
	return r0
}

func (p *processor) observeBuffSnapshot(row buffSnapshotAuditRow) {
}

func (event combatEvent) jsonlView(masters *gameMaster) combatEventJSONL {
	var r0 combatEventJSONL
	return r0
}

func (event combatEvent) formatNormal() string {
	var r0 string
	return r0
}

func marshalRuntimeIdentity(entity runtimeEntity) string {
	var r0 string
	return r0
}

func optionalDamageSource(value uint64, present bool) string {
	var r0 string
	return r0
}

func (p *processor) backfillAvailableSummaryCredits() {
}

func (p *processor) resetPendingSummaryCredits() {
}

func (p *processor) backfillEncounterSummary(sourceID, roleID uint64) {
}

func (p *processor) removePendingSummaryCredits(sourceID uint64) {
}

func (p *processor) observeEncounterSummary(event combatEvent) {
}
