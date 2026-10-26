package main

import (
	"time"
)

type encounterSummaryKey struct {
	session       int
	sceneInstance uint64
	hasScene      bool
	scopeConflict bool
	overflow      bool
}

const (
	maxEncounterSummaryGroups        = 512
	maxSummarySkillsPerCharacter     = 2048
	maxSummaryOwnerCandidates        = 512
	maxSummaryBossCandidates         = 2048
	maxSummaryClassificationEvidence = 64
)

type encounterSummary struct {
	key                           encounterSummaryKey
	startedAt                     time.Time
	endedAt                       time.Time
	eventCount                    int
	combatResultAbs               int64
	damageCandidates              int64
	healCandidates                int64
	otherCandidates               int64
	unclassified                  int64
	unknownCredit                 int
	unknownCreditAbs              int64
	identityBackfilledEvents      int
	identityBackfillEventsOmitted int
	classificationEvidence        map[string]struct{}
	classificationEvidenceOmitted int
	ownerCandidatesOmitted        int
	bossCandidatesOmitted         int
	characters                    map[uint64]*characterSummary
	ownerCandidates               map[uint64]*ownerCandidateSummary
	bossCandidates                map[bossCandidateKey]bossCandidateSummary
}

type pendingSummaryCredit struct {
	key   encounterSummaryKey
	event combatEvent
}

const maxPendingSummaryCreditEvents = 16384

type characterSummary struct {
	id               uint64
	name             string
	nameStatus       string
	status           resolutionStatus
	proofs           map[string]struct{}
	damage           int64
	healingCandidate int64
	other            int64
	eventCount       int
	skillRowsOmitted int
	skills           map[skillSummaryKey]*skillSummary
}

type skillSummaryKey struct {
	id        uint64
	eventType string
}

type skillSummary struct {
	id        uint64
	name      string
	nameState resolutionStatus
	proofs    map[string]struct{}
	eventType string
	amount    int64
	events    int
}

type ownerCandidateSummary struct {
	id     uint64
	name   string
	status resolutionStatus
	proof  string
	amount int64
	events int
}

type bossCandidateKey struct {
	monsterID uint64
	entityID  uint64
}

type bossCandidateSummary struct {
	monsterID uint64
	entityID  uint64
	name      string
	status    resolutionStatus
	proof     string
}
