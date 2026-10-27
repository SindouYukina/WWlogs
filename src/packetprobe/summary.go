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
	maxSummaryHealingAttributions    = 4096
	maxSummaryOwnerCandidates        = 512
	maxSummaryBossCandidates         = 2048
	maxSummaryBuffObservations       = 4096
	maxSummaryClassificationEvidence = 64
	maxSummaryUncreditedRows         = 512
)

type encounterSummary struct {
	key                             encounterSummaryKey
	startedAt                       time.Time
	endedAt                         time.Time
	eventCount                      int
	combatResultAbs                 int64
	damageCandidates                int64
	healCandidates                  int64
	otherCandidates                 int64
	unclassified                    int64
	unknownCredit                   int
	unknownCreditAbs                int64
	identityBackfilledEvents        int
	identityBackfillEventsOmitted   int
	classificationEvidence          map[string]struct{}
	classificationEvidenceOmitted   int
	ownerCandidatesOmitted          int
	healingAttributions             map[healingAttributionKey]*healingAttributionSummary
	healingAttributionUnresolved    int
	healingAttributionUnresolvedAbs int64
	healingAttributionsOmitted      int
	bossCandidatesOmitted           int
	buffObservationsOmitted         int
	characters                      map[uint64]*characterSummary
	ownerCandidates                 map[uint64]*ownerCandidateSummary
	bossCandidates                  map[bossCandidateKey]bossCandidateSummary
	buffObservations                map[buffObservationKey]int
	uncreditedEvents                map[uncreditedEventKey]*uncreditedEventSummary
	uncreditedEventsOmitted         int
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

type healingAttributionKey struct {
	healerID    uint64
	targetID    uint64
	skillID     uint64
	damageID    uint64
	hasSkillID  bool
	hasDamageID bool
}

type healingAttributionSummary struct {
	key                  healingAttributionKey
	healerName           string
	healerNameStatus     resolutionStatus
	healerStatus         resolutionStatus
	healerProofs         map[string]struct{}
	targetName           string
	targetNameStatus     resolutionStatus
	targetStatus         resolutionStatus
	targetProofs         map[string]struct{}
	skillName            string
	skillNameStatus      resolutionStatus
	skillStatus          resolutionStatus
	skillProofs          map[string]struct{}
	classificationStatus resolutionStatus
	classificationProofs map[string]struct{}
	reportedMagnitudeAbs int64
	events               int
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

type buffObservationKey struct {
	buffID      uint64
	hasBuffID   bool
	targetID    uint64
	hasTargetID bool
}

type uncreditedEventKey struct {
	sessionNumber   int
	sourceSceneID   uint64
	hasSourceScene  bool
	targetSceneID   uint64
	hasTargetScene  bool
	eventType       string
	skillID         uint64
	damageID        uint64
	hasDamageID     bool
	bulletID        int64
	hasBulletID     bool
	sourceEntityID  uint64
	hasSourceEntity bool
	targetEntityID  uint64
	hasTargetEntity bool
}

type uncreditedEventSummary struct {
	key       uncreditedEventKey
	events    int
	magnitude int64
}
