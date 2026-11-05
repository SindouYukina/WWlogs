//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"time"
)

// encounterDiagnosticSchemaVersion identifies the layout of the encounter diagnostic JSONL lines.
const encounterDiagnosticSchemaVersion = 2

type battleStateSignalLine struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Type            string    `json:"type"`
	ObservedAt      time.Time `json:"observedAt"`
	ProtocolSession int       `json:"protocolSession"`
	Container       string    `json:"container"`
	InBattle        bool      `json:"inBattle"`
	// Anchor names why the signal counts as the local player's; empty means it was rejected. The PlayerId
	// itself is not written.
	Anchor   string `json:"anchor"`
	Accepted bool   `json:"accepted"`
}

type combatEventRecordLine struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	CombatEventRecord
	Assignment string `json:"assignment"`
}

type encounterTransitionLine struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	EncounterTransition
}

type encounterFinalLine struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Type          string                   `json:"type"`
	Encounters    EncounterTrackerSnapshot `json:"encounters"`
}

func (p *processor) writeEncounterLine(value any) {
	if p.encounterOut == nil {
		return
	}
	line, err := json.Marshal(value)
	if err == nil {
		line = append(line, '\n')
		_, err = p.encounterOut.Write(line)
	}
	if err != nil {
		fmt.Fprintf(p.diagnostic, "encounter_diagnostic_disabled error=%v\n", err)
		p.encounterOut = nil
		if p.damageStatistics != nil {
			p.damageStatistics.recordEncounterTransitions(false)
		}
	}
}

func (p *processor) writeBattleStateSignal(at time.Time, container string, signal playerBattleStateSignal, anchor string) {
	p.writeEncounterLine(battleStateSignalLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "BATTLE_STATE_SIGNAL", ObservedAt: at,
		ProtocolSession: p.sessionNumber, Container: container, InBattle: signal.inBattle, Anchor: anchor, Accepted: anchor != "",
	})
}

func (p *processor) writeCombatEventRecord(record CombatEventRecord, assignment string) {
	p.writeEncounterLine(combatEventRecordLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "COMBAT_EVENT_RECORD", CombatEventRecord: record, Assignment: assignment,
	})
}

func (p *processor) flushEncounterTransitions() {
	if p.encounterOut == nil || p.damageStatistics == nil {
		return
	}
	for _, transition := range p.damageStatistics.takeEncounterTransitions() {
		p.writeEncounterLine(encounterTransitionLine{
			SchemaVersion: encounterDiagnosticSchemaVersion, Type: "ENCOUNTER_TRANSITION", EncounterTransition: transition,
		})
	}
}

// writeEncounterFinal writes the closing encounter view after finishCapture.
func (p *processor) writeEncounterFinal() {
	if p.encounterOut == nil || p.damageStatistics == nil {
		return
	}
	p.writeEncounterLine(encounterFinalLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "ENCOUNTER_FINAL", Encounters: p.damageStatistics.Snapshot().Encounters,
	})
}
