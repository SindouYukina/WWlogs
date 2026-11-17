//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"sync"
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

func (p *Processor) writeEncounterLine(value any) {
	if p.Dev.encounterOut == nil {
		return
	}
	line, err := json.Marshal(value)
	if err == nil {
		line = append(line, '\n')
		_, err = p.Dev.encounterOut.Write(line)
	}
	if err != nil {
		fmt.Fprintf(p.Diagnostic, "encounter_diagnostic_disabled error=%v\n", err)
		p.Dev.encounterOut = nil
		p.DamageStatistics.ObserveEncounterTransitions(nil)
		p.Dev.encounterTransitions = nil
	}
}

func (p *Processor) WriteBattleStateSignal(at time.Time, container string, signal PlayerBattleStateSignal, anchor string) {
	p.writeEncounterLine(battleStateSignalLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "BATTLE_STATE_SIGNAL", ObservedAt: at,
		ProtocolSession: p.SessionNumber, Container: container, InBattle: signal.InBattle, Anchor: anchor, Accepted: anchor != "",
	})
}

func (p *Processor) WriteCombatEventRecord(record CombatEventRecord, assignment string) {
	p.writeEncounterLine(combatEventRecordLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "COMBAT_EVENT_RECORD", CombatEventRecord: record, Assignment: assignment,
	})
}

func (p *Processor) FlushEncounterTransitions() {
	if p.Dev.encounterOut == nil || p.Dev.encounterTransitions == nil {
		return
	}
	for _, transition := range p.Dev.encounterTransitions.take() {
		p.writeEncounterLine(encounterTransitionLine{
			SchemaVersion: encounterDiagnosticSchemaVersion, Type: "ENCOUNTER_TRANSITION", EncounterTransition: transition,
		})
	}
}

// writeEncounterFinal writes the closing encounter view after finishCapture.
func (p *Processor) writeEncounterFinal() {
	if p.Dev.encounterOut == nil || p.DamageStatistics == nil {
		return
	}
	p.writeEncounterLine(encounterFinalLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "ENCOUNTER_FINAL", Encounters: p.DamageStatistics.Snapshot().Encounters,
	})
}

// recordEncounterTransitions keeps every transition of the encounter state machine for the encounter output
// (-encounter-out); flushEncounterTransitions writes them at the next output point.
func (p *Processor) recordEncounterTransitions() {
	log := &encounterTransitionLog{}
	p.Dev.encounterTransitions = log
	p.DamageStatistics.ObserveEncounterTransitions(log.add)
}

// encounterTransitionLog holds encounter transitions until the next encounter output point. The statistics report a
// transition while they hold their own lock, from whichever goroutine advanced the encounters, so the log has its own.
type encounterTransitionLog struct {
	mu      sync.Mutex
	pending []EncounterTransition
}

func (l *encounterTransitionLog) add(transition EncounterTransition) {
	l.mu.Lock()
	l.pending = append(l.pending, transition)
	l.mu.Unlock()
}

func (l *encounterTransitionLog) take() []EncounterTransition {
	l.mu.Lock()
	defer l.mu.Unlock()
	taken := l.pending
	l.pending = nil
	return taken
}
