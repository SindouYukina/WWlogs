package devcapture

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"wwlogs/internal/capture"
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
	capture.CombatEventRecord
	Assignment string `json:"assignment"`
}

type encounterTransitionLine struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	capture.EncounterTransition
}

type encounterFinalLine struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Type          string                           `json:"type"`
	Encounters    capture.EncounterTrackerSnapshot `json:"encounters"`
}

func (p *processor) writeEncounterLine(value any) {
	if p.dev.encounterOut == nil {
		return
	}
	line, err := json.Marshal(value)
	if err == nil {
		line = append(line, '\n')
		_, err = p.dev.encounterOut.Write(line)
	}
	if err != nil {
		fmt.Fprintf(p.Diagnostic, "encounter_diagnostic_disabled error=%v\n", err)
		p.dev.encounterOut = nil
		p.DamageStatistics.ObserveEncounterTransitions(nil)
		p.dev.encounterTransitions = nil
	}
}

func (p *processor) WriteBattleStateSignal(at time.Time, container string, signal capture.PlayerBattleStateSignal, anchor string) {
	p.writeEncounterLine(battleStateSignalLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "BATTLE_STATE_SIGNAL", ObservedAt: at,
		ProtocolSession: p.SessionNumber, Container: container, InBattle: signal.InBattle, Anchor: anchor, Accepted: anchor != "",
	})
}

func (p *processor) WriteCombatEventRecord(record capture.CombatEventRecord, assignment string) {
	p.writeEncounterLine(combatEventRecordLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "COMBAT_EVENT_RECORD", CombatEventRecord: record, Assignment: assignment,
	})
}

func (p *processor) FlushEncounterTransitions() {
	if p.dev.encounterOut == nil || p.dev.encounterTransitions == nil {
		return
	}
	for _, transition := range p.dev.encounterTransitions.take() {
		p.writeEncounterLine(encounterTransitionLine{
			SchemaVersion: encounterDiagnosticSchemaVersion, Type: "ENCOUNTER_TRANSITION", EncounterTransition: transition,
		})
	}
}

// writeEncounterFinal writes the closing encounter view after finishCapture.
func (p *processor) writeEncounterFinal() {
	if p.dev.encounterOut == nil || p.DamageStatistics == nil {
		return
	}
	p.writeEncounterLine(encounterFinalLine{
		SchemaVersion: encounterDiagnosticSchemaVersion, Type: "ENCOUNTER_FINAL", Encounters: p.DamageStatistics.Snapshot().Encounters,
	})
}

// recordEncounterTransitions keeps every transition of the encounter state machine for the encounter output
// (-encounter-out); flushEncounterTransitions writes them at the next output point.
func (p *processor) recordEncounterTransitions() {
	log := &encounterTransitionLog{}
	p.dev.encounterTransitions = log
	p.DamageStatistics.ObserveEncounterTransitions(log.add)
}

// encounterTransitionLog holds encounter transitions until the next encounter output point. The statistics report a
// transition while they hold their own lock, from whichever goroutine advanced the encounters, so the log has its own.
type encounterTransitionLog struct {
	mu      sync.Mutex
	pending []capture.EncounterTransition
}

func (l *encounterTransitionLog) add(transition capture.EncounterTransition) {
	l.mu.Lock()
	l.pending = append(l.pending, transition)
	l.mu.Unlock()
}

func (l *encounterTransitionLog) take() []capture.EncounterTransition {
	l.mu.Lock()
	defer l.mu.Unlock()
	taken := l.pending
	l.pending = nil
	return taken
}
