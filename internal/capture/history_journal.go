package capture

import (
	"sort"
	"strconv"
	"time"
)

// Combat History journal.
//
// The engine notes what Combat History keeps while it makes its own decisions, under the statistics lock, in the order it
// makes them: an encounter opened, became real, finished or was discarded; a finished encounter's integrity changed after a
// late transport loss; a transport loss began, recovered or failed; and, at the event level, every accepted combat result
// and the encounter it joined. The capture goroutine drains the journal after every packet and hands it to a HistorySink
// that must never block (Session.Run). The journal repeats decisions; it never makes one, so statistics, encounter
// boundaries and views are the same whether it is on or off. Off (HistoryOff, the default) it records nothing.

// HistoryLevel selects how much of the engine's work the journal repeats.
type HistoryLevel uint8

const (
	// HistoryOff records nothing.
	HistoryOff HistoryLevel = iota
	// HistoryEncounters journals encounter lifecycles and results, integrity changes and transport losses: aggregates only.
	HistoryEncounters
	// HistoryEvents also journals every accepted combat result and the encounter it was assigned to.
	HistoryEvents
)

// HistoryEntryKind names one journal entry.
type HistoryEntryKind string

const (
	// HistoryEventAccepted is a combat result the statistics accepted as new (Event, Sequence). HistoryEvents only.
	HistoryEventAccepted HistoryEntryKind = "event_accepted"
	// HistoryEventAssigned puts an accepted result (Sequence) into an encounter (Encounter). HistoryEvents only. An
	// assignment is final unless the encounter is discarded.
	HistoryEventAssigned HistoryEntryKind = "event_assigned"
	// HistoryEncounterOpened is a new open encounter (Opened). It may still be discarded.
	HistoryEncounterOpened HistoryEntryKind = "encounter_opened"
	// HistoryEncounterActive is an open encounter's first confirmed local activity: from here on it cannot be discarded.
	HistoryEncounterActive HistoryEntryKind = "encounter_active"
	// HistoryEncounterFinished is a finished encounter's result (Finished).
	HistoryEncounterFinished HistoryEntryKind = "encounter_finished"
	// HistoryEncounterDiscarded is an encounter that ended without confirmed local activity; its assigned results go back
	// outside every encounter, as the statistics count them.
	HistoryEncounterDiscarded HistoryEntryKind = "encounter_discarded"
	// HistoryEncounterIntegrity is a finished encounter's integrity after a later transport report changed it (Integrity).
	HistoryEncounterIntegrity HistoryEntryKind = "encounter_integrity"
	// HistoryLossBegan, HistoryLossEnded and HistoryLossFailed follow one transport loss episode (Loss).
	HistoryLossBegan  HistoryEntryKind = "loss_began"
	HistoryLossEnded  HistoryEntryKind = "loss_ended"
	HistoryLossFailed HistoryEntryKind = "loss_failed"
)

// HistoryEntry is one journal entry. Only the fields of its kind are set; the entry holds no reference into engine state.
type HistoryEntry struct {
	Kind HistoryEntryKind
	// Encounter is the engine's encounter key, "<statistics interval>-<sequence>", unique within one capture session.
	Encounter string
	// Sequence numbers accepted combat results in acceptance order within the capture session, starting at 1.
	Sequence  uint64
	Event     *CombatEventRecord
	Opened    *HistoryEncounterOpening
	Finished  *HistoryEncounter
	Integrity *DataIntegrity
	Loss      *HistoryLoss
}

// HistoryEncounterOpening is an encounter as it opened. Its start may still move back by the pre-roll window, so the
// finished entry, not this one, holds the encounter's boundaries.
type HistoryEncounterOpening struct {
	ProtocolSession    int
	StatisticsInterval uint64
	Sequence           uint64
	StartedAt          time.Time
	StartBasis         string
	Scene              SceneContext
}

// HistoryEncounter is a finished encounter as the engine keeps it. Snapshot holds Encounter DPS, peak DPS and healing before
// data integrity is applied (as the engine stores a completed encounter) and Integrity as it stands at the end;
// ApplyEncounterIntegrity gives the values a view shows. Enemies and Scene are recorded for history only.
type HistoryEncounter struct {
	Snapshot        EncounterSnapshot
	Timeline        StoredTimeline
	Enemies         []EncounterEnemySnapshot
	EnemiesOverflow uint64
	Scene           SceneContext
}

// HistoryLoss is one transport loss episode: data of Direction after From was lost until To (zero while it lasts).
type HistoryLoss struct {
	ID        uint64
	Direction string
	From      time.Time
	To        time.Time
}

// HistorySink receives the journal of one capture session. Submit runs on the capture goroutine after every packet that
// produced entries and must return at once: it never waits for storage. dropped counts entries the journal itself had to
// refuse since the previous call because it reached its bound (maxHistoryJournalEntries); a sink must treat them as lost.
type HistorySink interface {
	Level() HistoryLevel
	Submit(entries []HistoryEntry, dropped uint64)
}

// SceneContext is the scene an encounter opened in: JoinSceneNotify.SceneInfo.InstanceId of the same protocol session.
// Known is false when no JoinSceneNotify was decoded before the encounter opened, for example when the capture started
// inside a scene.
type SceneContext struct {
	InstanceID uint64
	Known      bool
}

// CombatActorType is the static identity evidence of a non-player endpoint at event time. Neither ID is ever a guess:
// MonsterID is MonsterCaptureComponentPb.MonsterId or the unique scene crosswalk (identity.go), TemplateID the single
// TemplateConfig candidate. Both lookups are versioned Master links, so their status is at most CORRELATED.
type CombatActorType struct {
	MonsterID      uint64
	MonsterStatus  string
	TemplateID     uint64
	TemplateStatus string
}

// combatActorTypeOf reads the static identity of an endpoint for enemy grouping only.
func combatActorTypeOf(identity entityIdentity) *CombatActorType {
	actor := CombatActorType{MonsterStatus: string(resolutionUnknown), TemplateStatus: string(resolutionUnknown)}
	if identity.HasMonster && identity.Monster.ID != 0 && identity.MonsterStatus != resolutionUnknown {
		actor.MonsterID, actor.MonsterStatus = identity.Monster.ID, string(identity.MonsterStatus)
	}
	if identity.TemplateStatus == resolutionCorrelated && len(identity.TemplateCandidates) == 1 && identity.TemplateCandidates[0].TemplateID != 0 {
		actor.TemplateID, actor.TemplateStatus = identity.TemplateCandidates[0].TemplateID, string(resolutionCorrelated)
	}
	return &actor
}

// EncounterEnemySnapshot is one enemy instance of an encounter: a non-player entity (EntityId and its reuse generation in
// one protocol session) that took confirmed local damage or dealt confirmed damage to the local team. Two enemies of the
// same type are two instances. The instance's type is the strongest identity observed for it; a conflict leaves it unknown.
type EncounterEnemySnapshot struct {
	// Ordinal is the instance's place in the order the encounter first met it, starting at 0.
	Ordinal        int
	MonsterID      uint64
	MonsterStatus  string
	TemplateID     uint64
	TemplateStatus string
	// IdentityConflict marks an instance that reported two different monster or template IDs; both are then dropped.
	IdentityConflict bool
	// DamageTaken and HitsTaken are the confirmed local damage the instance received; DamageDealt and HitsDealt the
	// confirmed damage it dealt to the local team.
	DamageTaken int64
	HitsTaken   uint64
	DamageDealt int64
	HitsDealt   uint64
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// maxEncounterEnemies bounds the enemy instances one encounter keeps; later instances only count as overflow.
const maxEncounterEnemies = 256

type enemyKey struct {
	session    int
	entityID   uint64
	generation uint64
}

// enemyAggregate groups an encounter's confirmed local flows by enemy instance.
type enemyAggregate struct {
	entries  map[enemyKey]*EncounterEnemySnapshot
	overflow uint64
}

// add credits one confirmed local record to its non-player endpoint. Other flows have no confirmed enemy.
func (a *enemyAggregate) add(record CombatEventRecord) {
	var endpoint CombatEndpoint
	var actor *CombatActorType
	incoming := false
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		endpoint, actor = record.Target, record.TargetActor
	case CombatFlowLocalIncoming:
		endpoint, actor, incoming = record.Source, record.SourceActor, true
	default:
		return
	}
	key := enemyKey{session: record.Key.ProtocolSession, entityID: endpoint.EntityID, generation: endpoint.EntityGeneration}
	entry := a.entries[key]
	if entry == nil {
		if len(a.entries) >= maxEncounterEnemies {
			a.overflow++
			return
		}
		if a.entries == nil {
			a.entries = make(map[enemyKey]*EncounterEnemySnapshot)
		}
		entry = &EncounterEnemySnapshot{
			Ordinal: len(a.entries), MonsterStatus: string(resolutionUnknown), TemplateStatus: string(resolutionUnknown),
			FirstSeenAt: record.ObservedAt, LastSeenAt: record.ObservedAt,
		}
		a.entries[key] = entry
	}
	entry.observeType(actor)
	if incoming {
		entry.DamageDealt += record.Amount
		entry.HitsDealt++
	} else {
		entry.DamageTaken += record.Amount
		entry.HitsTaken++
	}
	if !record.ObservedAt.IsZero() {
		if entry.FirstSeenAt.IsZero() || record.ObservedAt.Before(entry.FirstSeenAt) {
			entry.FirstSeenAt = record.ObservedAt
		}
		if record.ObservedAt.After(entry.LastSeenAt) {
			entry.LastSeenAt = record.ObservedAt
		}
	}
}

// observeType keeps the strongest identity of the instance. A second, different ID is a conflict and clears both.
func (e *EncounterEnemySnapshot) observeType(actor *CombatActorType) {
	if actor == nil || e.IdentityConflict {
		return
	}
	if actor.MonsterID != 0 {
		switch {
		case e.MonsterID == 0:
			e.MonsterID, e.MonsterStatus = actor.MonsterID, actor.MonsterStatus
		case e.MonsterID != actor.MonsterID:
			e.clearType()
			return
		}
	}
	if actor.TemplateID != 0 {
		switch {
		case e.TemplateID == 0:
			e.TemplateID, e.TemplateStatus = actor.TemplateID, actor.TemplateStatus
		case e.TemplateID != actor.TemplateID:
			e.clearType()
		}
	}
}

func (e *EncounterEnemySnapshot) clearType() {
	e.IdentityConflict = true
	e.MonsterID, e.MonsterStatus = 0, string(resolutionUnknown)
	e.TemplateID, e.TemplateStatus = 0, string(resolutionUnknown)
}

// snapshot lists the instances in first-met order.
func (a *enemyAggregate) snapshot() []EncounterEnemySnapshot {
	out := make([]EncounterEnemySnapshot, 0, len(a.entries))
	for _, entry := range a.entries {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out
}

// maxHistoryJournalEntries bounds the entries waiting for a drain. The capture goroutine drains after every packet, so the
// bound is only reached when nothing drains; entries beyond it are counted, never kept.
const maxHistoryJournalEntries = 1 << 16

// historyJournal belongs to a DamageStatistics, so it outlives the statistics interval reset that replaces the encounter
// tracker. It is used under the statistics lock.
type historyJournal struct {
	level    HistoryLevel
	entries  []HistoryEntry
	dropped  uint64
	sequence uint64
}

func (j *historyJournal) on() bool { return j != nil && j.level != HistoryOff }

func (j *historyJournal) events() bool { return j != nil && j.level >= HistoryEvents }

func (j *historyJournal) add(entry HistoryEntry) {
	if !j.on() {
		return
	}
	if len(j.entries) >= maxHistoryJournalEntries {
		j.dropped++
		return
	}
	j.entries = append(j.entries, entry)
}

// accept numbers an accepted combat result and, at the event level, journals it.
func (j *historyJournal) accept(record *CombatEventRecord) {
	if !j.on() {
		return
	}
	j.sequence++
	record.historySequence = j.sequence
	if j.events() {
		copied := *record
		copied.EchoVisions = append([]uint64(nil), record.EchoVisions...)
		j.add(HistoryEntry{Kind: HistoryEventAccepted, Sequence: j.sequence, Event: &copied})
	}
}

// assign journals that an accepted result joined an encounter.
func (j *historyJournal) assign(sequence uint64, encounter string) {
	if j.events() && sequence != 0 {
		j.add(HistoryEntry{Kind: HistoryEventAssigned, Sequence: sequence, Encounter: encounter})
	}
}

func (j *historyJournal) take() ([]HistoryEntry, uint64) {
	if j == nil {
		return nil, 0
	}
	entries, dropped := j.entries, j.dropped
	j.entries, j.dropped = nil, 0
	return entries, dropped
}

// historyKey is the encounter key journal entries use: the engine's encounter ID without its process-local capture prefix.
func historyKey(intervalID, sequence uint64) string {
	return strconv.FormatUint(intervalID, 10) + "-" + strconv.FormatUint(sequence, 10)
}

// EnableHistory turns the journal on at level for this statistics owner; HistoryOff turns it off and forgets what waits.
func (s *DamageStatistics) EnableHistory(level HistoryLevel) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history.level = level
	if level == HistoryOff {
		s.history.entries, s.history.dropped = nil, 0
	}
}

// TakeHistory returns the journal entries recorded since the previous call and how many the bound refused.
func (s *DamageStatistics) TakeHistory() ([]HistoryEntry, uint64) {
	if s == nil {
		return nil, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.history.take()
}

// ApplyEncounterIntegrity sets an encounter's integrity and applies it to its Encounter DPS, peak DPS and healing exactly as
// the engine renders a completed encounter. snapshot must hold the values before integrity, as HistoryEncounter does.
func ApplyEncounterIntegrity(snapshot EncounterSnapshot, integrity DataIntegrity) EncounterSnapshot {
	snapshot.Integrity = cloneIntegrity(integrity)
	snapshot.EncounterDPS = applyIntegrityToDPS(snapshot.EncounterDPS, snapshot.Integrity)
	snapshot.PeakDPS = applyIntegrityToDPS(snapshot.PeakDPS, snapshot.Integrity)
	snapshot.Healing = applyIntegrityToHealing(snapshot.Healing, snapshot.Integrity)
	return snapshot
}

// integrityEqual compares two rendered integrities.
func integrityEqual(a, b DataIntegrity) bool {
	if a.Status != b.Status || a.TransportLosses != b.TransportLosses || a.UnrecoveredLosses != b.UnrecoveredLosses ||
		a.ContextDowngradedEvents != b.ContextDowngradedEvents || !a.FirstLossAt.Equal(b.FirstLossAt) || len(a.Risks) != len(b.Risks) {
		return false
	}
	for i := range a.Risks {
		if a.Risks[i] != b.Risks[i] {
			return false
		}
	}
	return true
}
