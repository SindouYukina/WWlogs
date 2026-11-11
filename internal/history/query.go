package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"wwlogs/internal/capture"
)

// Queries answer the History view. The counting unit is the encounter, never the capture session: a listed encounter is one
// the engine segmented and that had confirmed local activity (state open, finished or abandoned). Each query reads only
// what its view shows; no query loads a year of encounters or any per-event row.

// listedStates are the history states a view lists. A pending encounter may still be discarded and is never listed.
const listedStates = "('open', 'finished', 'abandoned')"

// Query bounds.
const (
	// MaxDayPage bounds one page of a day's encounters.
	MaxDayPage = 200
	// firstListedYear and lastListedYear bound the year a query accepts.
	firstListedYear = 2000
	lastListedYear  = 2999
)

// ErrNotFound reports an encounter the history does not list.
var ErrNotFound = errors.New("no such encounter in the combat history")

// ErrUnavailable reports a history that cannot be read.
var ErrUnavailable = errors.New("the combat history cannot be read")

// DayCount is one local date's encounter count.
type DayCount struct {
	Date       string `json:"date"`
	Encounters int    `json:"encounters"`
}

// YearView is one calendar year of local dates.
type YearView struct {
	Year int `json:"year"`
	// Days lists the dates with at least one encounter, ascending. Dates are local to where each encounter started.
	Days  []DayCount `json:"days"`
	Total int        `json:"total"`
	// FirstYear and LastYear bound the years with any encounter; both are zero for an empty history.
	FirstYear int `json:"firstYear"`
	LastYear  int `json:"lastYear"`
}

// Year counts the encounters of every local date in year.
func (s *Store) Year(ctx context.Context, year int) (YearView, error) {
	view := YearView{Year: year, Days: []DayCount{}}
	if !s.readable() {
		return view, ErrUnavailable
	}
	if year < firstListedYear || year > lastListedYear {
		return view, fmt.Errorf("year %d is outside the supported range", year)
	}
	from, to := fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year)
	rows, err := s.db.QueryContext(ctx, "SELECT local_date, count(*) FROM encounter WHERE state IN "+listedStates+" AND local_date BETWEEN ? AND ? GROUP BY local_date ORDER BY local_date", from, to)
	if err != nil {
		return view, fmt.Errorf("read history year: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day DayCount
		if err := rows.Scan(&day.Date, &day.Encounters); err != nil {
			return view, fmt.Errorf("read history year: %w", err)
		}
		view.Days = append(view.Days, day)
		view.Total += day.Encounters
	}
	if err := rows.Err(); err != nil {
		return view, fmt.Errorf("read history year: %w", err)
	}
	var first, last sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT min(local_date), max(local_date) FROM encounter WHERE state IN "+listedStates).Scan(&first, &last); err != nil {
		return view, fmt.Errorf("read history years: %w", err)
	}
	view.FirstYear, view.LastYear = yearOf(first.String), yearOf(last.String)
	return view, nil
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

// SceneRef is where an encounter opened. Recorded is false for rows stored before scenes were recorded (schema 1); Known is
// false when no JoinSceneNotify of the protocol session preceded the encounter. A known InstanceID is a decoded protocol
// field (VERIFIED); its Master name is a CORRELATED link the view resolves.
type SceneRef struct {
	Recorded   bool   `json:"recorded"`
	Known      bool   `json:"known"`
	InstanceID uint64 `json:"instanceId,omitempty"`
}

// EnemyRef is an encounter's main enemy: the instance that took the most confirmed local damage (then the one that dealt
// the most, then the first met). SameType counts the instances of the same known type (monster, else template), itself
// included; an instance of unknown type is only itself.
type EnemyRef struct {
	Ordinal          int    `json:"ordinal"`
	MonsterID        uint64 `json:"monsterId,omitempty"`
	TemplateID       uint64 `json:"templateId,omitempty"`
	IdentityConflict bool   `json:"identityConflict,omitempty"`
	DamageTaken      int64  `json:"damageTaken"`
	SameType         int    `json:"sameType"`
}

// EncounterSummary is one row of a day's list.
type EncounterSummary struct {
	ID int64 `json:"id"`
	// StartedAt is UTC; UTCOffsetSeconds is the host's offset where the encounter started, LocalDate the date there.
	StartedAt        time.Time  `json:"startedAt"`
	UTCOffsetSeconds int        `json:"utcOffsetSeconds"`
	LocalDate        string     `json:"localDate"`
	EndedAt          *time.Time `json:"endedAt,omitempty"`
	// DurationSeconds is the confirmed elapsed time, shown only where the view would show it (not for UNRELIABLE data).
	DurationSeconds *float64 `json:"durationSeconds,omitempty"`
	// State is the history state: open (still running), finished, or abandoned (the application ended first).
	State   string `json:"state"`
	Outcome string `json:"outcome"`
	// Complete is false when part of the encounter's history never reached storage.
	Complete  bool   `json:"complete"`
	Source    string `json:"source"`
	Integrity string `json:"integrity,omitempty"`
	// DPS is the Encounter DPS with the stored integrity applied, as the live view would show it.
	DPS             capture.ViewDPS `json:"dps"`
	DamageDealt     *int64          `json:"damageDealt,omitempty"`
	Scene           SceneRef        `json:"scene"`
	MainEnemy       *EnemyRef       `json:"mainEnemy,omitempty"`
	Enemies         int             `json:"enemies"`
	EnemiesRecorded bool            `json:"enemiesRecorded"`
}

// DayView is one page of a local date's encounters in start order.
type DayView struct {
	Date       string             `json:"date"`
	Total      int                `json:"total"`
	Offset     int                `json:"offset"`
	Encounters []EncounterSummary `json:"encounters"`
}

const summaryColumns = `e.id, e.started_at_ns, e.utc_offset_s, e.local_date, e.ended_at_ns, e.state, e.outcome, e.complete, s.source, e.integrity_status,
	e.dps_status, e.dps_reason, e.dps_value, e.dps_damage, e.dps_duration_ns, e.dps_confidence, e.damage_dealt,
	e.scene_instance_id, e.scene_recorded, e.enemies_recorded, e.capture_session_id`

type summaryRow struct {
	summary   EncounterSummary
	sessionID int64
}

func scanSummary(scanner interface{ Scan(...any) error }) (summaryRow, error) {
	var row summaryRow
	var started int64
	var ended, dpsDamage, dpsDuration, damage, scene sql.NullInt64
	var integrity, dpsStatus, dpsReason, dpsConfidence sql.NullString
	var dpsValue sql.NullFloat64
	var complete, sceneRecorded, enemiesRecorded int
	s := &row.summary
	if err := scanner.Scan(&s.ID, &started, &s.UTCOffsetSeconds, &s.LocalDate, &ended, &s.State, &s.Outcome, &complete, &s.Source, &integrity,
		&dpsStatus, &dpsReason, &dpsValue, &dpsDamage, &dpsDuration, &dpsConfidence, &damage, &scene, &sceneRecorded, &enemiesRecorded, &row.sessionID); err != nil {
		return row, err
	}
	s.StartedAt = time.Unix(0, started).UTC()
	s.Complete, s.EnemiesRecorded = complete == 1, enemiesRecorded == 1
	s.Integrity = integrity.String
	s.Scene = SceneRef{Recorded: sceneRecorded == 1, Known: scene.Valid}
	if scene.Valid {
		s.Scene.InstanceID = uint64(scene.Int64)
	}
	if ended.Valid {
		end := time.Unix(0, ended.Int64).UTC()
		s.EndedAt = &end
	}
	if damage.Valid {
		value := damage.Int64
		s.DamageDealt = &value
	}
	if s.State != "finished" || !dpsStatus.Valid {
		// Only a finished encounter has an Encounter DPS; an open or abandoned one never shows a number.
		s.DPS = capture.ViewDPS{Status: capture.DPSStatusUnavailable, Reason: "encounter_not_finished"}
		return row, nil
	}
	base := capture.EncounterSnapshot{EncounterDPS: capture.DPSResult{
		Status: capture.DPSStatus(dpsStatus.String), Reason: dpsReason.String, Value: dpsValue.Float64, Damage: dpsDamage.Int64,
		Duration: time.Duration(dpsDuration.Int64), Confidence: dpsConfidence.String,
	}}
	shown := capture.ApplyEncounterIntegrity(base, capture.DataIntegrity{Status: capture.IntegrityStatus(s.Integrity)})
	dps := shown.EncounterDPS
	s.DPS = capture.ViewDPS{Status: dps.Status, Reason: dps.Reason, Damage: dps.Damage, DurationSeconds: dps.Duration.Seconds(), Value: dps.Value, Confidence: dps.Confidence}
	if s.EndedAt != nil && s.Integrity != string(capture.IntegrityUnreliable) && !s.EndedAt.Before(s.StartedAt) {
		elapsed := s.EndedAt.Sub(s.StartedAt).Seconds()
		s.DurationSeconds = &elapsed
	}
	return row, nil
}

// Day lists one page of a local date's encounters in start order with each one's main enemy.
func (s *Store) Day(ctx context.Context, date string, offset, limit int) (DayView, error) {
	view := DayView{Date: date, Offset: offset, Encounters: []EncounterSummary{}}
	if !s.readable() {
		return view, ErrUnavailable
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return view, fmt.Errorf("date %q is not a calendar date", date)
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > MaxDayPage {
		limit = MaxDayPage
	}
	view.Offset = offset
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM encounter WHERE local_date = ? AND state IN "+listedStates, date).Scan(&view.Total); err != nil {
		return view, fmt.Errorf("read history day: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+summaryColumns+" FROM encounter e JOIN capture_session s ON s.id = e.capture_session_id WHERE e.local_date = ? AND e.state IN "+listedStates+
		" ORDER BY e.started_at_ns, e.id LIMIT ? OFFSET ?", date, limit, offset)
	if err != nil {
		return view, fmt.Errorf("read history day: %w", err)
	}
	defer rows.Close()
	index := map[int64]int{}
	for rows.Next() {
		row, err := scanSummary(rows)
		if err != nil {
			return view, fmt.Errorf("read history day: %w", err)
		}
		index[row.summary.ID] = len(view.Encounters)
		view.Encounters = append(view.Encounters, row.summary)
	}
	if err := rows.Err(); err != nil {
		return view, fmt.Errorf("read history day: %w", err)
	}
	rows.Close()
	if len(view.Encounters) == 0 {
		return view, nil
	}
	ids := make([]string, 0, len(view.Encounters))
	for _, encounter := range view.Encounters {
		ids = append(ids, strconv.FormatInt(encounter.ID, 10))
	}
	enemies, err := s.enemiesOf(ctx, "encounter_id IN ("+strings.Join(ids, ",")+")")
	if err != nil {
		return view, err
	}
	for id, list := range enemies {
		if position, ok := index[id]; ok {
			view.Encounters[position].Enemies = len(list)
			view.Encounters[position].MainEnemy = mainEnemy(list)
		}
	}
	return view, nil
}

// enemiesOf reads the enemy instances of the encounters where selects, by encounter in first-met order.
func (s *Store) enemiesOf(ctx context.Context, where string) (map[int64][]capture.EncounterEnemySnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT encounter_id, ordinal, monster_id, template_id, identity_conflict, damage_taken, hits_taken, damage_dealt, hits_dealt,
		first_seen_ns, last_seen_ns FROM encounter_enemy WHERE `+where+` ORDER BY encounter_id, ordinal`)
	if err != nil {
		return nil, fmt.Errorf("read history enemies: %w", err)
	}
	defer rows.Close()
	out := map[int64][]capture.EncounterEnemySnapshot{}
	for rows.Next() {
		var id, monster, template, hitsTaken, hitsDealt int64
		var conflict int
		var first, last sql.NullInt64
		enemy := capture.EncounterEnemySnapshot{}
		if err := rows.Scan(&id, &enemy.Ordinal, &monster, &template, &conflict, &enemy.DamageTaken, &hitsTaken, &enemy.DamageDealt, &hitsDealt, &first, &last); err != nil {
			return nil, fmt.Errorf("read history enemies: %w", err)
		}
		enemy.MonsterID, enemy.TemplateID, enemy.IdentityConflict = uint64(monster), uint64(template), conflict == 1
		enemy.MonsterStatus, enemy.TemplateStatus = linkStatus(enemy.MonsterID), linkStatus(enemy.TemplateID)
		enemy.HitsTaken, enemy.HitsDealt = uint64(hitsTaken), uint64(hitsDealt)
		enemy.FirstSeenAt, enemy.LastSeenAt = timeFromNanos(first), timeFromNanos(last)
		out[id] = append(out[id], enemy)
	}
	return out, rows.Err()
}

// linkStatus is the evidence of a stored Master link: every stored monster or template ID came from a CORRELATED lookup.
func linkStatus(id uint64) string {
	if id == 0 {
		return "UNKNOWN"
	}
	return "CORRELATED"
}

// mainEnemy picks the instance the encounter was mostly fought against.
func mainEnemy(enemies []capture.EncounterEnemySnapshot) *EnemyRef {
	if len(enemies) == 0 {
		return nil
	}
	ordered := append([]capture.EncounterEnemySnapshot(nil), enemies...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.DamageTaken != b.DamageTaken {
			return a.DamageTaken > b.DamageTaken
		}
		if a.DamageDealt != b.DamageDealt {
			return a.DamageDealt > b.DamageDealt
		}
		return a.Ordinal < b.Ordinal
	})
	top := ordered[0]
	ref := &EnemyRef{Ordinal: top.Ordinal, MonsterID: top.MonsterID, TemplateID: top.TemplateID, IdentityConflict: top.IdentityConflict, DamageTaken: top.DamageTaken, SameType: 1}
	if top.MonsterID != 0 || top.TemplateID != 0 {
		ref.SameType = 0
		for _, enemy := range enemies {
			if enemy.MonsterID == top.MonsterID && enemy.TemplateID == top.TemplateID {
				ref.SameType++
			}
		}
	}
	return ref
}

// SessionInfo is the capture session an encounter belongs to: what recorded it and whether its history is complete.
type SessionInfo struct {
	Source         string    `json:"source"`
	Status         string    `json:"status"`
	OpenedAt       time.Time `json:"openedAt"`
	HistoryLevel   string    `json:"historyLevel"`
	JournalDropped int64     `json:"journalDropped"`
	QueueDropped   int64     `json:"queueDropped"`
	WriteFailed    int64     `json:"writeFailed"`
	TransportGaps  int       `json:"transportGaps"`
	AppVersion     string    `json:"appVersion"`
	AppBuild       int64     `json:"appBuild"`
	AppProfile     string    `json:"appProfile"`
	AppCommit      string    `json:"appCommit"`
	ProtocolSchema string    `json:"protocolSchema"`
	MasterSHA256   string    `json:"masterSha256"`
}

// EncounterDetail is one stored encounter. Snapshot is set for a finished encounter only: it is the engine's finished
// encounter rebuilt from storage with the stored integrity applied, ready for capture.MasterData.ProjectEncounter. Timeline
// is its stored DPS timeline.
type EncounterDetail struct {
	Summary  EncounterSummary                 `json:"summary"`
	Session  SessionInfo                      `json:"session"`
	Snapshot *capture.EncounterSnapshot       `json:"-"`
	Timeline *capture.StoredTimeline          `json:"-"`
	Enemies  []capture.EncounterEnemySnapshot `json:"-"`
}

// Encounter reads one listed encounter.
func (s *Store) Encounter(ctx context.Context, id int64) (EncounterDetail, error) {
	var detail EncounterDetail
	if !s.readable() {
		return detail, ErrUnavailable
	}
	row, err := scanSummary(s.db.QueryRowContext(ctx, "SELECT "+summaryColumns+" FROM encounter e JOIN capture_session s ON s.id = e.capture_session_id WHERE e.id = ? AND e.state IN "+listedStates, id))
	if errors.Is(err, sql.ErrNoRows) {
		return detail, ErrNotFound
	}
	if err != nil {
		return detail, fmt.Errorf("read history encounter: %w", err)
	}
	detail.Summary = row.summary
	if detail.Session, err = s.session(ctx, row.sessionID); err != nil {
		return detail, err
	}
	enemies, err := s.enemiesOf(ctx, "encounter_id = "+strconv.FormatInt(id, 10))
	if err != nil {
		return detail, err
	}
	detail.Enemies = enemies[id]
	if detail.Enemies == nil {
		detail.Enemies = []capture.EncounterEnemySnapshot{}
	}
	detail.Summary.Enemies, detail.Summary.MainEnemy = len(detail.Enemies), mainEnemy(detail.Enemies)
	if detail.Summary.State != "finished" {
		return detail, nil
	}
	snapshot, err := s.snapshot(ctx, id)
	if err != nil {
		return detail, err
	}
	detail.Snapshot = &snapshot
	timeline, found, err := s.timeline(ctx, id)
	if err != nil {
		return detail, err
	}
	if found {
		detail.Timeline = &timeline
	}
	return detail, nil
}

func (s *Store) session(ctx context.Context, id int64) (SessionInfo, error) {
	var info SessionInfo
	var opened int64
	err := s.db.QueryRowContext(ctx, `SELECT source, status, opened_at_ns, history_level, journal_dropped, queue_dropped, write_failed,
		app_version, app_build, app_profile, app_commit, protocol_schema, master_sha256,
		(SELECT count(*) FROM capture_gap g WHERE g.capture_session_id = capture_session.id)
		FROM capture_session WHERE id = ?`, id).Scan(&info.Source, &info.Status, &opened, &info.HistoryLevel, &info.JournalDropped, &info.QueueDropped, &info.WriteFailed,
		&info.AppVersion, &info.AppBuild, &info.AppProfile, &info.AppCommit, &info.ProtocolSchema, &info.MasterSHA256, &info.TransportGaps)
	if err != nil {
		return info, fmt.Errorf("read history session: %w", err)
	}
	info.OpenedAt = time.Unix(0, opened).UTC()
	return info, nil
}

// snapshot rebuilds a finished encounter as the engine kept it and applies its stored integrity.
func (s *Store) snapshot(ctx context.Context, id int64) (capture.EncounterSnapshot, error) {
	var e capture.EncounterSnapshot
	var interval, sequence, reentries int64
	var started int64
	var startConfirmed int
	var battle, ended, decided, firstActivity, lastActivity, firstLoss sql.NullInt64
	var endBasis sql.NullString
	var integrityStatus string
	var outgoing, incoming, unattributedOut, unattributedIn int64
	var dps, peak capture.DPSResult
	var dpsStatus, peakStatus, healingStatus string
	var dpsDuration, peakDuration int64
	var dpsStart, dpsEnd, peakStart, peakEnd sql.NullInt64
	var healingEvents, healingUnknownEvents, unassignedOut, unassignedIn, unassignedOverflow, downgraded int64
	err := s.db.QueryRowContext(ctx, `SELECT statistics_interval, sequence, protocol_session, outcome, started_at_ns, start_basis, start_confirmed, battle_entered_at_ns,
		ended_at_ns, end_basis, end_decided_at_ns, first_activity_at_ns, last_activity_at_ns, reentries,
		damage_dealt, damage_taken, outgoing_events, incoming_events, unattributed_outgoing_damage, unattributed_incoming_damage, unattributed_outgoing_events, unattributed_incoming_events,
		integrity_status, transport_losses, unrecovered_losses, context_downgraded_events, first_loss_at_ns,
		dps_status, dps_reason, dps_value, dps_damage, dps_duration_ns, dps_start_ns, dps_end_ns, dps_confidence,
		peak_status, peak_reason, peak_value, peak_damage, peak_duration_ns, peak_start_ns, peak_end_ns, peak_confidence,
		healing_status, healing_reason, healing_effective, healing_reported, healing_overheal, healing_events, healing_unknown_effective, healing_unknown_events,
		unassigned_damage_dealt, unassigned_outgoing_events, unassigned_damage_taken, unassigned_incoming_events, unassigned_overflow_events
		FROM encounter WHERE id = ? AND state = 'finished'`, id).Scan(
		&interval, &sequence, &e.ProtocolSession, &e.Outcome, &started, &e.StartBasis, &startConfirmed, &battle,
		&ended, &endBasis, &decided, &firstActivity, &lastActivity, &reentries,
		&e.Totals.DamageDealt, &e.Totals.DamageTaken, &outgoing, &incoming, &e.Totals.UnattributedOutgoingDamage, &e.Totals.UnattributedIncomingDamage, &unattributedOut, &unattributedIn,
		&integrityStatus, &e.Integrity.TransportLosses, &e.Integrity.UnrecoveredLosses, &downgraded, &firstLoss,
		&dpsStatus, &dps.Reason, &dps.Value, &dps.Damage, &dpsDuration, &dpsStart, &dpsEnd, &dps.Confidence,
		&peakStatus, &peak.Reason, &peak.Value, &peak.Damage, &peakDuration, &peakStart, &peakEnd, &peak.Confidence,
		&healingStatus, &e.Healing.Reason, &e.Healing.Effective, &e.Healing.Reported, &e.Healing.Overheal, &healingEvents, &e.Healing.UnknownHealerEffective, &healingUnknownEvents,
		&e.UnassignedRole.DamageDealt, &unassignedOut, &e.UnassignedRole.DamageTaken, &unassignedIn, &unassignedOverflow)
	if err != nil {
		return e, fmt.Errorf("read history encounter: %w", err)
	}
	e.EncounterID = "history-" + strconv.FormatInt(id, 10)
	e.StatisticsIntervalID, e.Sequence, e.State = uint64(interval), uint64(sequence), capture.EncounterFinished
	e.StartedAt, e.StartConfirmed = time.Unix(0, started), startConfirmed == 1
	e.BattleEntered, e.EndedAt, e.EndBasis, e.EndDecidedAt = timeFromNanos(battle), timeFromNanos(ended), endBasis.String, timeFromNanos(decided)
	e.FirstActivityAt, e.LastActivityAt, e.Reentries = timeFromNanos(firstActivity), timeFromNanos(lastActivity), uint64(reentries)
	e.Totals.OutgoingEvents, e.Totals.IncomingEvents = uint64(outgoing), uint64(incoming)
	e.Totals.UnattributedOutgoingEvents, e.Totals.UnattributedIncomingEvents = uint64(unattributedOut), uint64(unattributedIn)
	e.Integrity.Status, e.Integrity.ContextDowngradedEvents, e.Integrity.FirstLossAt = capture.IntegrityStatus(integrityStatus), uint64(downgraded), timeFromNanos(firstLoss)
	dps.Status, dps.Duration, dps.StartObservedAt, dps.EndObservedAt = capture.DPSStatus(dpsStatus), time.Duration(dpsDuration), timeFromNanos(dpsStart), timeFromNanos(dpsEnd)
	peak.Status, peak.Duration, peak.StartObservedAt, peak.EndObservedAt = capture.DPSStatus(peakStatus), time.Duration(peakDuration), timeFromNanos(peakStart), timeFromNanos(peakEnd)
	e.EncounterDPS, e.PeakDPS = dps, peak
	e.Healing.Status, e.Healing.Events, e.Healing.UnknownHealerEvents = capture.DPSStatus(healingStatus), uint64(healingEvents), uint64(healingUnknownEvents)
	e.UnassignedRole.OutgoingEvents, e.UnassignedRole.IncomingEvents, e.UnassignedRole.OverflowEvents = uint64(unassignedOut), uint64(unassignedIn), uint64(unassignedOverflow)
	if e.Integrity.Risks, err = s.strings(ctx, "SELECT risk FROM encounter_risk WHERE encounter_id = ? ORDER BY risk", id); err != nil {
		return e, err
	}
	if e.Healing.ByHealer, err = s.healers(ctx, id); err != nil {
		return e, err
	}
	if err := s.contributors(ctx, id, &e); err != nil {
		return e, err
	}
	return capture.ApplyEncounterIntegrity(e, e.Integrity), nil
}

func (s *Store) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read history encounter: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) healers(ctx context.Context, id int64) ([]capture.HealerTotals, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT player_id, role_id, effective, reported, events FROM encounter_healer WHERE encounter_id = ? ORDER BY player_id, role_id", id)
	if err != nil {
		return nil, fmt.Errorf("read history healers: %w", err)
	}
	defer rows.Close()
	out := []capture.HealerTotals{}
	for rows.Next() {
		var player, role, events int64
		var healer capture.HealerTotals
		if err := rows.Scan(&player, &role, &healer.Effective, &healer.Reported, &events); err != nil {
			return nil, err
		}
		healer.PlayerID, healer.RoleID, healer.Events = uint64(player), uint64(role), uint64(events)
		out = append(out, healer)
	}
	return out, rows.Err()
}

type contributorKey struct {
	ordinal int
	skillID uint64
}

// contributors rebuilds the role and vehicle split with each skill row and its naming evidence, in the engine's order.
func (s *Store) contributors(ctx context.Context, id int64, e *capture.EncounterSnapshot) error {
	evidence := map[contributorKey]map[string][]uint64{}
	rows, err := s.db.QueryContext(ctx, "SELECT contributor_ordinal, skill_id, kind, value FROM encounter_skill_evidence WHERE encounter_id = ? ORDER BY contributor_ordinal, skill_id, kind, value", id)
	if err != nil {
		return fmt.Errorf("read history skill evidence: %w", err)
	}
	for rows.Next() {
		var ordinal int
		var skill, value int64
		var kind string
		if err := rows.Scan(&ordinal, &skill, &kind, &value); err != nil {
			rows.Close()
			return err
		}
		key := contributorKey{ordinal: ordinal, skillID: uint64(skill)}
		if evidence[key] == nil {
			evidence[key] = map[string][]uint64{}
		}
		evidence[key][kind] = append(evidence[key][kind], uint64(value))
	}
	rows.Close()
	skills := map[int][]capture.SkillDamageSnapshot{}
	rows, err = s.db.QueryContext(ctx, "SELECT contributor_ordinal, skill_id, damage_dealt, outgoing_events, damage_ids_incomplete FROM encounter_skill WHERE encounter_id = ? ORDER BY contributor_ordinal, damage_dealt DESC, skill_id", id)
	if err != nil {
		return fmt.Errorf("read history skills: %w", err)
	}
	for rows.Next() {
		var ordinal, incomplete int
		var skill, events int64
		var row capture.SkillDamageSnapshot
		if err := rows.Scan(&ordinal, &skill, &row.DamageDealt, &events, &incomplete); err != nil {
			rows.Close()
			return err
		}
		row.SkillID, row.OutgoingEvents, row.DamageIDsIncomplete = uint64(skill), uint64(events), incomplete == 1
		found := evidence[contributorKey{ordinal: ordinal, skillID: row.SkillID}]
		row.DamageIDs, row.EchoVisions = found["damage_id"], found["echo_vision"]
		skills[ordinal] = append(skills[ordinal], row)
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT ordinal, kind, player_id, role_id, vehicle_kind, vehicle_evidence, damage_dealt, outgoing_events, direct_damage, summon_damage,
		damage_taken, incoming_events, unassigned_damage, unassigned_events, unassigned_missing_skill_events, unassigned_overflow_events, unassigned_damage_ids_incomplete
		FROM encounter_contributor WHERE encounter_id = ? ORDER BY ordinal`, id)
	if err != nil {
		return fmt.Errorf("read history contributors: %w", err)
	}
	defer rows.Close()
	e.Roles = []capture.RoleDamageSnapshot{}
	for rows.Next() {
		var ordinal, incomplete int
		var kind, vehicleKind, vehicleEvidence string
		var player, role, outgoing, incoming, unassignedEvents, missing, overflow int64
		var damage, direct, summon, taken int64
		var unassigned capture.UnassignedSkillTotals
		if err := rows.Scan(&ordinal, &kind, &player, &role, &vehicleKind, &vehicleEvidence, &damage, &outgoing, &direct, &summon, &taken, &incoming,
			&unassigned.DamageDealt, &unassignedEvents, &missing, &overflow, &incomplete); err != nil {
			return err
		}
		unassigned.OutgoingEvents, unassigned.MissingSkillEvents, unassigned.OverflowEvents, unassigned.DamageIDsIncomplete = uint64(unassignedEvents), uint64(missing), uint64(overflow), incomplete == 1
		found := evidence[contributorKey{ordinal: ordinal}]
		unassigned.DamageIDs, unassigned.EchoVisions = found["damage_id"], found["echo_vision"]
		rowSkills := skills[ordinal]
		if rowSkills == nil {
			rowSkills = []capture.SkillDamageSnapshot{}
		}
		switch kind {
		case "role":
			e.Roles = append(e.Roles, capture.RoleDamageSnapshot{
				PlayerID: uint64(player), RoleID: uint64(role), DamageDealt: damage, OutgoingEvents: uint64(outgoing), DirectDamage: direct, SummonDamage: summon,
				DamageTaken: taken, IncomingEvents: uint64(incoming), Skills: rowSkills, UnassignedSkill: unassigned,
			})
		case "vehicle":
			e.Vehicle = &capture.VehicleDamageSnapshot{
				Kind: vehicleKind, Evidence: vehicleEvidence, DamageDealt: damage, OutgoingEvents: uint64(outgoing), Skills: rowSkills, UnassignedSkill: unassigned,
			}
		}
	}
	return rows.Err()
}

// timeline reads an encounter's stored DPS timeline; found is false when none was stored.
func (s *Store) timeline(ctx context.Context, id int64) (capture.StoredTimeline, bool, error) {
	var stored capture.StoredTimeline
	var anchor int64
	err := s.db.QueryRowContext(ctx, "SELECT anchor_ns, scale_s FROM encounter_timeline WHERE encounter_id = ?", id).Scan(&anchor, &stored.ScaleSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, false, nil
	}
	if err != nil {
		return stored, false, fmt.Errorf("read history timeline: %w", err)
	}
	stored.Anchor = time.Unix(0, anchor)
	rows, err := s.db.QueryContext(ctx, "SELECT kind, player_id, role_id, vehicle_kind FROM encounter_timeline_contributor WHERE encounter_id = ? ORDER BY position", id)
	if err != nil {
		return stored, false, fmt.Errorf("read history timeline: %w", err)
	}
	for rows.Next() {
		var contributor capture.TimelineContributor
		var player, role int64
		if err := rows.Scan(&contributor.Kind, &player, &role, &contributor.VehicleKind); err != nil {
			rows.Close()
			return stored, false, err
		}
		contributor.PlayerID, contributor.RoleID = uint64(player), uint64(role)
		stored.Contributors = append(stored.Contributors, contributor)
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, "SELECT bucket, position, damage FROM encounter_timeline_bucket WHERE encounter_id = ? ORDER BY bucket, position", id)
	if err != nil {
		return stored, false, fmt.Errorf("read history timeline: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket capture.StoredTimelineBucket
		if err := rows.Scan(&bucket.Index, &bucket.Contributor, &bucket.Damage); err != nil {
			return stored, false, err
		}
		stored.Buckets = append(stored.Buckets, bucket)
	}
	return stored, true, rows.Err()
}
