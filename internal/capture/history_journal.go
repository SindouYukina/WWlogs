package capture

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Combat History journal.
//
// The engine notes what Combat History keeps while it makes its own decisions, in the order it makes them: an encounter
// opened, became real, finished or was discarded; a finished encounter's integrity changed after a late transport loss; a
// transport loss began, recovered or failed; and, at the event level, every accepted combat result and the encounter it
// joined, and every effect lifecycle change the effect tracker accepted (history_effects.go). The statistics journal under
// the statistics lock and the effect tracker under its own; the journal's lock is a leaf that neither waits on. The capture
// goroutine drains the journal after every packet and hands it to a HistorySink (Session.Run): a live capture never waits
// for it, a replay waits for room. The journal repeats decisions; it never makes one, so statistics, encounter boundaries,
// effect lifecycles and views are the same whether it is on or off. Off (HistoryOff, the default) it records nothing.
//
// Recording can be switched off and on while a capture runs (SetHistoryRecording). Each recorded span starts with
// HistoryRecordingStarted and ends with HistoryRecordingStopped or the end of the capture. A span never starts inside an
// encounter: a start waits for a clean boundary (historyBoundary), so a span holds whole encounters only. Stopping is
// immediate: the encounter in progress is named in the stop entry and stays unfinished in the history, never complete.

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
	// HistoryRecordingStarted opens a recorded span (Recording.Reason): the entries up to the next HistoryRecordingStopped,
	// or the end of the capture, belong to it.
	HistoryRecordingStarted HistoryEntryKind = "recording_started"
	// HistoryRecordingStopped closes the span (Recording). Recording.Open names the encounter still in progress: it was
	// not observed to its end and must never be kept as finished.
	HistoryRecordingStopped HistoryEntryKind = "recording_stopped"
	// HistoryEncounterCheckpoint is the encounter in progress as the engine renders it at a checkpoint (Finished, with the
	// outcome OPEN). A later checkpoint or the finished entry replaces it; it never finishes the encounter.
	HistoryEncounterCheckpoint HistoryEntryKind = "encounter_checkpoint"
	// HistoryEffectChanged is one effect lifecycle change the effect tracker accepted (Effect). HistoryEvents only.
	HistoryEffectChanged HistoryEntryKind = "effect_changed"
	// HistoryEffectRejected is an effect APPLY the tracker could not keep at its capacity bound: its instance and later
	// changes are missing from the history. HistoryEvents only.
	HistoryEffectRejected HistoryEntryKind = "effect_rejected"
	// HistoryProtocolConfirmed is a protocol session that reported the game version this build decodes (Protocol). Nothing
	// of a protocol session is decoded before it, so everything a span holds of that session follows it: it is journaled
	// when the session reports its version and again right after the start of every later span while the session lasts.
	HistoryProtocolConfirmed HistoryEntryKind = "protocol_confirmed"
)

// Reasons a recorded span starts or stops.
const (
	// HistoryStartCapture is a span that starts with the capture.
	HistoryStartCapture = "capture_start"
	// HistoryStartEnabled is a span that starts after recording was switched on during the capture.
	HistoryStartEnabled = "recording_enabled"
	// HistoryStopDisabled is a span that ends because recording was switched off.
	HistoryStopDisabled = "recording_disabled"
)

// HistoryRecordingChange is a recorded span starting or stopping.
type HistoryRecordingChange struct {
	Reason string
	// Open lists the encounter keys still in progress when the span stopped.
	Open []string
}

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
	Recording *HistoryRecordingChange
	Effect    *HistoryEffect
	Protocol  *HistoryProtocolSession
}

// HistoryProtocolSession is one protocol session of the capture (its number, as CombatEventKey.ProtocolSession and the
// encounters name it) with the major.minor game version it reported.
type HistoryProtocolSession struct {
	Number      int
	GameVersion string
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
	// CheckpointAt is the capture time a checkpoint describes; zero for a finished encounter.
	CheckpointAt time.Time
}

// HistoryLoss is one transport loss episode: data of Direction after From was lost until To (zero while it lasts).
type HistoryLoss struct {
	ID        uint64
	Direction string
	From      time.Time
	To        time.Time
}

// HistorySink receives the journal of one capture session on the capture goroutine, after every packet that produced
// entries. The producer's source decides the flow control:
//
//   - A live capture hands each drain to Submit, which must return at once. The game server never waits for WWlogs, so
//     storage never holds up packet reception or transport recovery; what a sink cannot keep it counts as lost and marks
//     the encounters it belonged to incomplete.
//   - A replay hands each drain to SubmitWait, which waits until the sink has room (backpressure): the file is then read
//     only as fast as the history stores it, and nothing is dropped for lack of room. It returns ctx's error when ctx ends
//     before the entries were accepted. Waiting changes no engine decision, since the engine runs on capture time.
//
// dropped counts entries the journal itself had to refuse since the previous call because it reached its bound
// (maxHistoryJournalEntries); a sink must treat them as lost.
type HistorySink interface {
	Level() HistoryLevel
	Submit(entries []HistoryEntry, dropped uint64)
	SubmitWait(ctx context.Context, entries []HistoryEntry, dropped uint64) error
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
func combatActorTypeOf(identity EntityIdentity) *CombatActorType {
	actor := CombatActorType{MonsterStatus: string(ResolutionUnknown), TemplateStatus: string(ResolutionUnknown)}
	if identity.HasMonster && identity.Monster.ID != 0 && identity.MonsterStatus != ResolutionUnknown {
		actor.MonsterID, actor.MonsterStatus = identity.Monster.ID, string(identity.MonsterStatus)
	}
	if identity.TemplateStatus == ResolutionCorrelated && len(identity.TemplateCandidates) == 1 && identity.TemplateCandidates[0].TemplateID != 0 {
		actor.TemplateID, actor.TemplateStatus = identity.TemplateCandidates[0].TemplateID, string(ResolutionCorrelated)
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
			Ordinal: len(a.entries), MonsterStatus: string(ResolutionUnknown), TemplateStatus: string(ResolutionUnknown),
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
	e.MonsterID, e.MonsterStatus = 0, string(ResolutionUnknown)
	e.TemplateID, e.TemplateStatus = 0, string(ResolutionUnknown)
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
// tracker, and the processor shares it with the effect tracker. Its lock is a leaf: nothing is locked while it is held, so
// the statistics (under their lock) and the effect tracker (under its own) both journal into it in processing order.
type historyJournal struct {
	mu    sync.Mutex
	level HistoryLevel
	// want is the host's recording setting; recording says whether entries are kept now. A start waits for a clean
	// encounter boundary (DamageStatistics.TakeHistory), so want can be on while recording is still off.
	want      bool
	recording bool
	entries   []HistoryEntry
	dropped   uint64
	// sequence numbers accepted combat results, effectSequence journaled effect changes and effectInstances the effect
	// instances they belong to; all three only grow within the capture session.
	sequence        uint64
	effectSequence  uint64
	effectInstances uint64
	// protocol is the protocol session decoded now, once it confirmed its game version; zero before that.
	protocol HistoryProtocolSession
}

func (j *historyJournal) onLocked() bool { return j.level != HistoryOff && j.recording }

func (j *historyJournal) eventsLocked() bool { return j.level >= HistoryEvents && j.recording }

// on reports whether the journal records now.
func (j *historyJournal) on() bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.onLocked()
}

// events reports whether the journal records event-level entries now.
func (j *historyJournal) events() bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.eventsLocked()
}

func (j *historyJournal) add(entry HistoryEntry) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.onLocked() {
		j.addLocked(entry)
	}
}

// addRecorded journals an entry about an encounter a span already recorded, such as a finished encounter's later integrity
// change. It is kept whenever the journal has a level, even between spans, so a recorded encounter never keeps a stale result.
func (j *historyJournal) addRecorded(entry HistoryEntry) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.level != HistoryOff {
		j.addLocked(entry)
	}
}

func (j *historyJournal) addLocked(entry HistoryEntry) {
	if len(j.entries) >= maxHistoryJournalEntries {
		j.dropped++
		return
	}
	j.entries = append(j.entries, entry)
}

// accept numbers an accepted combat result and, at the event level, journals it.
func (j *historyJournal) accept(record *CombatEventRecord) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.onLocked() {
		return
	}
	j.sequence++
	record.historySequence = j.sequence
	if j.eventsLocked() {
		copied := *record
		copied.EchoVisions = append([]uint64(nil), record.EchoVisions...)
		j.addLocked(HistoryEntry{Kind: HistoryEventAccepted, Sequence: j.sequence, Event: &copied})
	}
}

// assign journals that an accepted result joined an encounter.
func (j *historyJournal) assign(sequence uint64, encounter string) {
	if j == nil || sequence == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.eventsLocked() {
		j.addLocked(HistoryEntry{Kind: HistoryEventAssigned, Sequence: sequence, Encounter: encounter})
	}
}

// startLocked opens a recorded span and names the protocol session it starts in. Span markers and protocol sessions are
// never refused at the bound: without them a sink could not tell which span, and which protocol session, an entry belongs
// to.
func (j *historyJournal) startLocked(reason string) {
	j.recording = true
	j.entries = append(j.entries, HistoryEntry{Kind: HistoryRecordingStarted, Recording: &HistoryRecordingChange{Reason: reason}})
	j.protocolLocked()
}

// protocolLocked journals the protocol session decoded now, when it confirmed its game version.
func (j *historyJournal) protocolLocked() {
	if j.protocol.Number == 0 {
		return
	}
	protocol := j.protocol
	j.entries = append(j.entries, HistoryEntry{Kind: HistoryProtocolConfirmed, Protocol: &protocol})
}

// confirmProtocol notes that protocol session number reported gameVersion, the version this build decodes, and journals it
// while a span records.
func (j *historyJournal) confirmProtocol(number int, gameVersion string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.protocol = HistoryProtocolSession{Number: number, GameVersion: gameVersion}
	if j.onLocked() {
		j.protocolLocked()
	}
}

// endProtocol forgets the protocol session at a new handshake: the next one is decoded only once it confirms its version.
func (j *historyJournal) endProtocol() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.protocol = HistoryProtocolSession{}
}

func (j *historyJournal) stopLocked(reason string, open []string) {
	j.entries = append(j.entries, HistoryEntry{Kind: HistoryRecordingStopped, Recording: &HistoryRecordingChange{Reason: reason, Open: open}})
	j.recording = false
}

func (j *historyJournal) takeLocked() ([]HistoryEntry, uint64) {
	entries, dropped := j.entries, j.dropped
	j.entries, j.dropped = nil, 0
	return entries, dropped
}

// historyKey is the encounter key journal entries use: the engine's encounter ID without its process-local capture prefix.
func historyKey(intervalID, sequence uint64) string {
	return strconv.FormatUint(intervalID, 10) + "-" + strconv.FormatUint(sequence, 10)
}

// confirmProtocolSession tells the journal that protocol session number reported gameVersion, the game version this build
// decodes (protocol_session.go); endProtocolSession that a new handshake ended it.
func (s *DamageStatistics) confirmProtocolSession(number int, gameVersion string) {
	if s == nil {
		return
	}
	s.history.confirmProtocol(number, gameVersion)
}

func (s *DamageStatistics) endProtocolSession() {
	if s == nil {
		return
	}
	s.history.endProtocol()
}

// EnableHistory turns the journal on at level and records from now on; HistoryOff turns it off and forgets what waits.
func (s *DamageStatistics) EnableHistory(level HistoryLevel) {
	s.ConfigureHistory(level, true)
}

// ConfigureHistory sets the journal level and the recording setting. A capture that keeps history calls it before its
// first packet: with recording on, the first span starts at once (HistoryStartCapture), since nothing is in progress yet.
func (s *DamageStatistics) ConfigureHistory(level HistoryLevel, recording bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.history
	j.mu.Lock()
	defer j.mu.Unlock()
	j.level = level
	if level == HistoryOff {
		j.entries, j.dropped, j.want, j.recording = nil, 0, false, false
		return
	}
	j.want = recording
	if recording && !j.recording && s.Encounters.historyBoundary() {
		j.startLocked(HistoryStartCapture)
	}
}

// SetHistoryRecording switches recording while a capture runs; it is safe to call from any goroutine. Off stops the span at
// once and names the encounter in progress, which therefore never becomes a finished history encounter. On starts a span
// at once at a clean boundary, otherwise at the first one TakeHistory sees, so a span never holds part of an encounter.
func (s *DamageStatistics) SetHistoryRecording(on bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.history
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.level == HistoryOff {
		return
	}
	j.want = on
	switch {
	case on && !j.recording && s.Encounters.historyBoundary():
		j.startLocked(HistoryStartEnabled)
	case !on && j.recording:
		j.stopLocked(HistoryStopDisabled, s.Encounters.historyInProgress())
	}
}

// HistoryRecordingState reports the recording setting, whether a span records now, and whether the span is inside an
// encounter it will keep.
type HistoryRecordingState struct {
	Enabled    bool `json:"enabled"`
	Recording  bool `json:"recording"`
	InProgress bool `json:"inProgress"`
}

// HistoryRecording returns the journal's recording state.
func (s *DamageStatistics) HistoryRecording() HistoryRecordingState {
	if s == nil {
		return HistoryRecordingState{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	j := s.history
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.level == HistoryOff {
		return HistoryRecordingState{}
	}
	return HistoryRecordingState{Enabled: j.want, Recording: j.recording, InProgress: j.recording && len(s.Encounters.historyInProgress()) > 0}
}

// TakeHistory returns the journal entries recorded since the previous call and how many the bound refused. It also starts a
// span that waits for a clean boundary once the engine reached one.
func (s *DamageStatistics) TakeHistory() ([]HistoryEntry, uint64) {
	if s == nil {
		return nil, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.history
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.level != HistoryOff && j.want && !j.recording && s.Encounters.historyBoundary() {
		j.startLocked(HistoryStartEnabled)
	}
	return j.takeLocked()
}

// CheckpointHistory journals the encounter in progress as the engine renders it now (HistoryEncounterCheckpoint), so an
// application that ends abnormally still leaves its observed and stored part. It journals nothing unless the encounter
// had confirmed activity and changed since its last checkpoint. The host calls it at a low wall-clock rate.
func (s *DamageStatistics) CheckpointHistory() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Encounters.checkpoint()
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

// historyBoundary reports a clean boundary for a recorded span to start at: no encounter is open, no event waits in the
// prelude, and no healing result is recent enough to join an encounter that opens later. A span that starts here therefore
// sees every entry of each encounter it records.
func (t *encounterTracker) historyBoundary() bool {
	if t.Current != nil || len(t.prelude) > 0 {
		return false
	}
	for _, event := range t.healing {
		if !event.at.Before(t.highWater.Add(-encounterPreRollWindow)) {
			return false
		}
	}
	return true
}

// historyInProgress lists the encounter key a stopping span leaves unfinished.
func (t *encounterTracker) historyInProgress() []string {
	if t.Current == nil {
		return nil
	}
	return []string{historyKey(t.intervalID, t.Current.sequence)}
}

// checkpoint journals the encounter in progress the way finish journals a finished one: the engine's own rendering, with
// Encounter DPS, peak DPS and healing before data integrity is applied, evaluated at the newest capture time (live) or at
// the candidate end. It is skipped before confirmed activity and when nothing changed since the last checkpoint.
func (t *encounterTracker) checkpoint() {
	record := t.Current
	if record == nil || !record.historyActive || !t.history.on() {
		return
	}
	events := record.totals.OutgoingEvents + record.totals.IncomingEvents
	if record.checkpointed && record.checkpointEvents == events && record.checkpointState == record.State {
		return
	}
	end, confidence := t.highWater, "live"
	if record.State == EncounterEndingCandidate {
		end, confidence = record.endAt, "ending_candidate"
	}
	if end.Before(record.lastActivity) {
		end = record.lastActivity
	}
	snapshot := t.snapshotOf(record, EncounterOutcomeOpen, end)
	base := snapshot
	base.EncounterDPS = encounterDPS(record, end, confidence)
	base.PeakDPS = encounterPeakDPS(record, end, confidence)
	base.Healing = t.encounterHealing(record, end)
	record.checkpointed, record.checkpointEvents, record.checkpointState = true, events, record.State
	t.history.add(HistoryEntry{Kind: HistoryEncounterCheckpoint, Encounter: historyKey(t.intervalID, record.sequence), Finished: &HistoryEncounter{
		Snapshot: base, Timeline: record.timeline.store(), Enemies: record.enemies.snapshot(), EnemiesOverflow: record.enemies.overflow,
		Scene: record.scene, CheckpointAt: end,
	}})
}
