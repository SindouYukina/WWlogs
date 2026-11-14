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

// DayCount is one calendar date's encounter count in the view's zone.
type DayCount struct {
	Date       string `json:"date"`
	Encounters int    `json:"encounters"`
}

// YearView is one calendar year of dates in the view's zone.
type YearView struct {
	Year int `json:"year"`
	// Days lists the dates with at least one encounter, ascending: the date in the view's zone where each encounter started.
	Days  []DayCount `json:"days"`
	Total int        `json:"total"`
	// FirstYear and LastYear bound the years with any encounter in the view's zone; both are zero for an empty history.
	FirstYear int `json:"firstYear"`
	LastYear  int `json:"lastYear"`
	// Today is the current date in the view's zone.
	Today string `json:"today"`
}

// listedStart is one listed encounter's start instant.
type listedStart struct {
	id      int64
	startNs int64
}

// listedStarts reads the listed encounters starting in the superset dateBounds gives for [from, to), in start order.
func (s *Store) listedStarts(ctx context.Context, from, to time.Time) ([]listedStart, error) {
	fromNs, toNs, fromDate, toDate := dateBounds(from, to)
	rows, err := s.db.QueryContext(ctx, "SELECT id, started_at_ns FROM encounter WHERE state IN "+listedStates+
		" AND local_date BETWEEN ? AND ? AND started_at_ns >= ? AND started_at_ns < ? ORDER BY started_at_ns, id", fromDate, toDate, fromNs, toNs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []listedStart
	for rows.Next() {
		var row listedStart
		if err := rows.Scan(&row.id, &row.startNs); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Year counts the encounters of every calendar date of year in zone (nil: the zone Go read from Windows). Year 0 is the
// current year there. Each encounter counts on the date its UTC start falls on in zone, so the counts always add up to the
// day lists of the same zone.
func (s *Store) Year(ctx context.Context, year int, zone *time.Location) (YearView, error) {
	zone = viewLocation(zone)
	now := s.now().In(zone)
	if year == 0 {
		year = now.Year()
	}
	view := YearView{Year: year, Days: []DayCount{}, Today: now.Format(dateLayout)}
	if !s.readable() {
		return view, ErrUnavailable
	}
	if year < firstListedYear || year > lastListedYear {
		return view, fmt.Errorf("year %d is outside the supported range", year)
	}
	starts, err := s.listedStarts(ctx, time.Date(year, 1, 1, 0, 0, 0, 0, zone), time.Date(year+1, 1, 1, 0, 0, 0, 0, zone))
	if err != nil {
		return view, fmt.Errorf("read history year: %w", err)
	}
	// A clock set back across midnight can repeat a date, so dates are counted by key, not by run.
	counts := map[string]int{}
	for _, start := range starts {
		local := time.Unix(0, start.startNs).In(zone)
		if local.Year() != year {
			continue
		}
		date := local.Format(dateLayout)
		if counts[date] == 0 {
			view.Days = append(view.Days, DayCount{Date: date})
		}
		counts[date]++
		view.Total++
	}
	for i := range view.Days {
		view.Days[i].Encounters = counts[view.Days[i].Date]
	}
	sort.Slice(view.Days, func(i, j int) bool { return view.Days[i].Date < view.Days[j].Date })
	var first, last sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT min(started_at_ns), max(started_at_ns) FROM encounter WHERE state IN "+listedStates).Scan(&first, &last); err != nil {
		return view, fmt.Errorf("read history years: %w", err)
	}
	if first.Valid && last.Valid {
		view.FirstYear, view.LastYear = time.Unix(0, first.Int64).In(zone).Year(), time.Unix(0, last.Int64).In(zone).Year()
	}
	return view, nil
}

// SceneRef is where an encounter opened. Known is false when no JoinSceneNotify of the protocol session preceded the
// encounter. A known InstanceID is a decoded protocol field (VERIFIED); its Master name is a CORRELATED link the view resolves.
type SceneRef struct {
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
	// StartedAt is UTC. Start lays it out in the view's zone and LocalDate is Start's date: the day the view lists the
	// encounter on. End and Checkpoint are laid out the same way. Recorded is the date and offset where the encounter was
	// recorded, kept as provenance only.
	StartedAt time.Time    `json:"startedAt"`
	Start     LocalTime    `json:"start"`
	LocalDate string       `json:"localDate"`
	Recorded  RecordedZone `json:"recorded"`
	EndedAt   *time.Time   `json:"endedAt,omitempty"`
	End       *LocalTime   `json:"end,omitempty"`
	// DurationSeconds is the confirmed elapsed time, shown only where the view would show it (not for UNRELIABLE data).
	DurationSeconds *float64 `json:"durationSeconds,omitempty"`
	// GameVersion is the major.minor game version of the protocol session the encounter was fought in, as that session
	// reported it and the recording build decoded it.
	GameVersion string `json:"gameVersion"`
	// NotifiedDamageCounted is false for an encounter whose recording build did not count server-executed damage
	// (notifiedDamageCounted): its stored damage may be lower than the same fight analysed now. It is a fact about the
	// analyser, not a finding that this fight had such damage.
	NotifiedDamageCounted bool `json:"notifiedDamageCounted"`
	// State is the history state: open (still running), finished, or abandoned (the application ended first).
	State   string `json:"state"`
	Outcome string `json:"outcome"`
	// Complete is false when part of the encounter's history never reached storage.
	Complete  bool   `json:"complete"`
	Source    string `json:"source"`
	Integrity string `json:"integrity,omitempty"`
	// DPS is the Encounter DPS with the stored integrity applied, as the live view would show it.
	DPS         capture.ViewDPS `json:"dps"`
	DamageDealt *int64          `json:"damageDealt,omitempty"`
	Scene       SceneRef        `json:"scene"`
	MainEnemy   *EnemyRef       `json:"mainEnemy,omitempty"`
	Enemies     int             `json:"enemies"`
	// IncompleteReason says why Complete is false or the encounter was abandoned: entries_lost, application_exit,
	// recording_disabled or not_finished; empty when complete.
	IncompleteReason string `json:"incompleteReason,omitempty"`
	// CheckpointAt is the capture time the stored results of an unfinished encounter describe; nil without a checkpoint.
	CheckpointAt *time.Time `json:"checkpointAt,omitempty"`
	Checkpoint   *LocalTime `json:"checkpoint,omitempty"`
}

// DayView is one page of a local date's encounters in start order.
type DayView struct {
	Date       string             `json:"date"`
	Total      int                `json:"total"`
	Offset     int                `json:"offset"`
	Encounters []EncounterSummary `json:"encounters"`
}

// lastBuildWithoutNotifiedDamage is the last official v0.7.0 Build Number whose analyser counted only the damage responses
// to the client's requests and none of the server-executed DamageExecuteNotify results (capture/damage_notify.go). Build 36
// is the last official build before that accounting; every later official build has it.
const lastBuildWithoutNotifiedDamage = 36

// notifiedDamageCounted reports whether the analyser that recorded a span counted server-executed damage, from the identity
// the span stored: a product version before 0.7.0, or 0.7.0 with an official Build Number up to
// lastBuildWithoutNotifiedDamage, did not. An unofficial 0.7.0 build has no number to compare; it is a developer build of
// the current source and is taken as counting it. Stored results are never recomputed (schema.go).
func notifiedDamageCounted(version sql.NullString, build sql.NullInt64) bool {
	if !version.Valid {
		return false
	}
	switch compareProductVersion(version.String, "0.7.0") {
	case -1:
		return false
	case 0:
		return !build.Valid || build.Int64 > lastBuildWithoutNotifiedDamage
	}
	return true
}

// compareProductVersion orders two MAJOR.MINOR.PATCH versions (-1, 0, 1); an unparsable version sorts first.
func compareProductVersion(a, b string) int {
	parse := func(text string) ([3]int, bool) {
		var parts [3]int
		fields := strings.Split(text, ".")
		if len(fields) != 3 {
			return parts, false
		}
		for i, field := range fields {
			value, err := strconv.Atoi(field)
			if err != nil || value < 0 {
				return parts, false
			}
			parts[i] = value
		}
		return parts, true
	}
	left, okLeft := parse(a)
	right, okRight := parse(b)
	switch {
	case !okLeft && !okRight:
		return 0
	case !okLeft:
		return -1
	case !okRight:
		return 1
	}
	for i := range left {
		if left[i] != right[i] {
			if left[i] < right[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// summaryFrom joins an encounter to its span and to the protocol session its game version is recorded on; both are key
// lookups, so a page of a day's list is one query.
const summaryFrom = ` FROM encounter e JOIN capture_session s ON s.id = e.capture_session_id
	JOIN protocol_session p ON p.capture_session_id = e.capture_session_id AND p.protocol_session = e.protocol_session`

const summaryColumns = `e.id, e.started_at_ns, e.utc_offset_s, e.local_date, e.ended_at_ns, e.state, e.outcome, e.complete, s.source, e.integrity_status,
	e.dps_status, e.dps_reason, e.dps_value, e.dps_damage, e.dps_duration_ns, e.dps_confidence, e.damage_dealt,
	e.scene_instance_id, e.capture_session_id, e.protocol_session, p.game_version, e.incomplete_reason, e.checkpoint_at_ns,
	s.app_version, s.app_build`

type summaryRow struct {
	summary         EncounterSummary
	sessionID       int64
	protocolSession int
}

// scanSummary reads one summary row and lays its instants out in zone.
func scanSummary(scanner interface{ Scan(...any) error }, zone *time.Location) (summaryRow, error) {
	var row summaryRow
	var started int64
	var ended, dpsDamage, dpsDuration, damage, scene sql.NullInt64
	var integrity, dpsStatus, dpsReason, dpsConfidence, incompleteReason sql.NullString
	var dpsValue sql.NullFloat64
	var checkpoint, appBuild sql.NullInt64
	var appVersion sql.NullString
	var complete int
	s := &row.summary
	if err := scanner.Scan(&s.ID, &started, &s.Recorded.OffsetSeconds, &s.Recorded.Date, &ended, &s.State, &s.Outcome, &complete, &s.Source, &integrity,
		&dpsStatus, &dpsReason, &dpsValue, &dpsDamage, &dpsDuration, &dpsConfidence, &damage, &scene, &row.sessionID, &row.protocolSession, &s.GameVersion,
		&incompleteReason, &checkpoint, &appVersion, &appBuild); err != nil {
		return row, err
	}
	s.NotifiedDamageCounted = notifiedDamageCounted(appVersion, appBuild)
	zone = viewLocation(zone)
	s.IncompleteReason = incompleteReason.String
	if checkpoint.Valid && s.State != "finished" {
		at := time.Unix(0, checkpoint.Int64).UTC()
		local := localTimeOf(at, zone)
		s.CheckpointAt, s.Checkpoint = &at, &local
	}
	s.StartedAt = time.Unix(0, started).UTC()
	s.Start = localTimeOf(s.StartedAt, zone)
	s.LocalDate = s.Start.Date
	s.Complete = complete == 1
	s.Integrity = integrity.String
	s.Scene = SceneRef{Known: scene.Valid}
	if scene.Valid {
		s.Scene.InstanceID = uint64(scene.Int64)
	}
	if ended.Valid {
		end := time.Unix(0, ended.Int64).UTC()
		local := localTimeOf(end, zone)
		s.EndedAt, s.End = &end, &local
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

// Day lists one page of the encounters that started on date in zone (nil: the zone Go read from Windows), in start order,
// with each one's main enemy. A date is a calendar day of zone, so it may be 23 or 25 hours long across a DST change.
func (s *Store) Day(ctx context.Context, date string, offset, limit int, zone *time.Location) (DayView, error) {
	view := DayView{Date: date, Offset: offset, Encounters: []EncounterSummary{}}
	if !s.readable() {
		return view, ErrUnavailable
	}
	day, err := time.Parse(dateLayout, date)
	if err != nil {
		return view, fmt.Errorf("date %q is not a calendar date", date)
	}
	zone = viewLocation(zone)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > MaxDayPage {
		limit = MaxDayPage
	}
	view.Offset = offset
	starts, err := s.listedStarts(ctx, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone), time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, zone))
	if err != nil {
		return view, fmt.Errorf("read history day: %w", err)
	}
	var ids []string
	for _, start := range starts {
		if time.Unix(0, start.startNs).In(zone).Format(dateLayout) != date {
			continue
		}
		if view.Total >= offset && len(ids) < limit {
			ids = append(ids, strconv.FormatInt(start.id, 10))
		}
		view.Total++
	}
	if len(ids) == 0 {
		return view, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+summaryColumns+summaryFrom+" WHERE e.id IN ("+strings.Join(ids, ",")+") ORDER BY e.started_at_ns, e.id")
	if err != nil {
		return view, fmt.Errorf("read history day: %w", err)
	}
	defer rows.Close()
	index := map[int64]int{}
	for rows.Next() {
		row, err := scanSummary(rows, zone)
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

// SessionInfo is the recorded span an encounter belongs to: how it was recorded and whether its history is complete. The
// analyser identity (AppVersion as MAJOR.MINOR.PATCH, the official AppBuild, AppCommit; each empty or zero when the build
// did not carry it) says which WWlogs produced the stored results; the History view does not show it.
type SessionInfo struct {
	Source         string    `json:"source"`
	Status         string    `json:"status"`
	OpenedAt       time.Time `json:"openedAt"`
	JournalDropped int64     `json:"journalDropped"`
	QueueDropped   int64     `json:"queueDropped"`
	WriteFailed    int64     `json:"writeFailed"`
	TransportGaps  int       `json:"transportGaps"`
	AppVersion     string    `json:"appVersion,omitempty"`
	AppBuild       int64     `json:"appBuild,omitempty"`
	AppCommit      string    `json:"appCommit,omitempty"`
	AppProfile     string    `json:"appProfile"`
	MasterSHA256   string    `json:"masterSha256"`
	// StartReason is capture_start or recording_enabled; StopReason how the span ended (empty while it records).
	StartReason string `json:"startReason"`
	StopReason  string `json:"stopReason,omitempty"`
	// EffectsRejected counts effect APPLYs the tracker refused at its capacity bound during the span.
	EffectsRejected int64 `json:"effectsRejected"`
}

// EffectWindow is what the history holds of effect lifecycles during one encounter: the stored changes observed between its
// start and its end (or its checkpoint) in its protocol session, and the instances they belong to.
type EffectWindow struct {
	Changes   int `json:"changes"`
	Instances int `json:"instances"`
	// Observed, Derived and Untracked split Changes by how each change is known.
	Observed  int `json:"observed"`
	Derived   int `json:"derived"`
	Untracked int `json:"untracked"`
}

// EncounterDetail is one stored encounter. Snapshot is set for a finished encounter only: it is the engine's finished
// encounter rebuilt from storage with the stored integrity applied, ready for capture.MasterData.ProjectEncounter. Timeline
// is its stored DPS timeline.
type EncounterDetail struct {
	Summary  EncounterSummary                 `json:"summary"`
	Session  SessionInfo                      `json:"session"`
	Effects  EffectWindow                     `json:"effects"`
	Snapshot *capture.EncounterSnapshot       `json:"-"`
	Timeline *capture.StoredTimeline          `json:"-"`
	Enemies  []capture.EncounterEnemySnapshot `json:"-"`
}

// Encounter reads one listed encounter, with its instants laid out in zone (nil: the zone Go read from Windows).
func (s *Store) Encounter(ctx context.Context, id int64, zone *time.Location) (EncounterDetail, error) {
	return s.encounter(ctx, id, zone, true)
}

// encounter reads one listed encounter; withEffects also counts the effect changes stored during it (EffectWindow), which an
// analysis model, reading every change itself, does not need.
func (s *Store) encounter(ctx context.Context, id int64, zone *time.Location, withEffects bool) (EncounterDetail, error) {
	var detail EncounterDetail
	if !s.readable() {
		return detail, ErrUnavailable
	}
	row, err := scanSummary(s.db.QueryRowContext(ctx, "SELECT "+summaryColumns+summaryFrom+" WHERE e.id = ? AND e.state IN "+listedStates, id), zone)
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
	if withEffects {
		if detail.Effects, err = s.effectWindow(ctx, row, detail.Summary); err != nil {
			return detail, err
		}
	}
	if detail.Summary.State != "finished" && detail.Summary.CheckpointAt == nil {
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
	var stopReason, version, commit sql.NullString
	var build sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT source, status, opened_at_ns, journal_dropped, queue_dropped, write_failed,
		app_version, app_build, app_commit, app_profile, master_sha256,
		(SELECT count(*) FROM capture_gap g WHERE g.capture_session_id = capture_session.id),
		start_reason, stop_reason, effects_rejected
		FROM capture_session WHERE id = ?`, id).Scan(&info.Source, &info.Status, &opened, &info.JournalDropped, &info.QueueDropped, &info.WriteFailed,
		&version, &build, &commit, &info.AppProfile, &info.MasterSHA256, &info.TransportGaps,
		&info.StartReason, &stopReason, &info.EffectsRejected)
	if err != nil {
		return info, fmt.Errorf("read history session: %w", err)
	}
	info.OpenedAt = time.Unix(0, opened).UTC()
	info.StopReason, info.AppVersion, info.AppBuild, info.AppCommit = stopReason.String, version.String, build.Int64, commit.String
	return info, nil
}

// effectWindow counts the stored effect changes during one encounter: from its start to its end, or to its checkpoint when
// it never finished, in the encounter's own span and protocol session.
func (s *Store) effectWindow(ctx context.Context, row summaryRow, summary EncounterSummary) (EffectWindow, error) {
	var window EffectWindow
	from := summary.StartedAt.UnixNano()
	var to int64
	switch {
	case summary.EndedAt != nil:
		to = summary.EndedAt.UnixNano()
	case summary.CheckpointAt != nil:
		to = summary.CheckpointAt.UnixNano()
	default:
		return window, nil
	}
	const counts = `SELECT count(*), count(DISTINCT ev.instance),
		coalesce(sum(ev.observation = 'observed'), 0), coalesce(sum(ev.observation = 'derived'), 0), coalesce(sum(ev.observation = 'untracked'), 0)
		FROM effect_event ev`
	// A span of one protocol session holds only that session's changes, so its window is counted from the time index alone;
	// a span with more than one keeps the changes of the encounter's own session.
	var sessions int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM protocol_session WHERE capture_session_id = ?", row.sessionID).Scan(&sessions); err != nil {
		return window, fmt.Errorf("read history effects: %w", err)
	}
	var err error
	if sessions == 1 {
		err = s.db.QueryRowContext(ctx, counts+" WHERE ev.capture_session_id = ? AND ev.observed_at_ns BETWEEN ? AND ?", row.sessionID, from, to).
			Scan(&window.Changes, &window.Instances, &window.Observed, &window.Derived, &window.Untracked)
	} else {
		err = s.db.QueryRowContext(ctx, counts+` JOIN effect_instance i ON i.capture_session_id = ev.capture_session_id AND i.instance = ev.instance
			WHERE ev.capture_session_id = ? AND i.protocol_session = ? AND ev.observed_at_ns BETWEEN ? AND ?`, row.sessionID, row.protocolSession, from, to).
			Scan(&window.Changes, &window.Instances, &window.Observed, &window.Derived, &window.Untracked)
	}
	if err != nil {
		return window, fmt.Errorf("read history effects: %w", err)
	}
	return window, nil
}

// snapshot rebuilds a finished encounter as the engine kept it, or an unfinished one as its last checkpoint left it, and
// applies its stored integrity. A checkpoint keeps the outcome OPEN and the engine's live confidence: it is never a result.
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
	// The engine counted the negative-status damage it could credit to no role apart inside its unassigned bucket; the
	// stored events keep it by evidence and by having no source role (a credited one is the status damage of the role).
	var negativeStatusDamage, negativeStatusEvents int64
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT statistics_interval, sequence, protocol_session, outcome, started_at_ns, start_basis, start_confirmed, battle_entered_at_ns,
		ended_at_ns, end_basis, end_decided_at_ns, first_activity_at_ns, last_activity_at_ns, reentries,
		damage_dealt, damage_taken, outgoing_events, incoming_events, unattributed_outgoing_damage, unattributed_incoming_damage, unattributed_outgoing_events, unattributed_incoming_events,
		integrity_status, transport_losses, unrecovered_losses, context_downgraded_events, first_loss_at_ns,
		dps_status, dps_reason, dps_value, dps_damage, dps_duration_ns, dps_start_ns, dps_end_ns, dps_confidence,
		peak_status, peak_reason, peak_value, peak_damage, peak_duration_ns, peak_start_ns, peak_end_ns, peak_confidence,
		healing_status, healing_reason, healing_effective, healing_reported, healing_overheal, healing_events, healing_unknown_effective, healing_unknown_events,
		unassigned_damage_dealt, unassigned_outgoing_events, unassigned_damage_taken, unassigned_incoming_events, unassigned_overflow_events
		, state,
		(SELECT coalesce(sum(amount), 0) FROM combat_event WHERE encounter_id = encounter.id AND kind = 'damage' AND flow = 'local_outgoing' AND evidence = ? AND source_role_id IS NULL),
		(SELECT count(*) FROM combat_event WHERE encounter_id = encounter.id AND kind = 'damage' AND flow = 'local_outgoing' AND evidence = ? AND source_role_id IS NULL)
		FROM encounter WHERE id = ? AND (state = 'finished' OR checkpoint_at_ns IS NOT NULL)`,
		capture.CombatEvidenceNegativeStatusNotify, capture.CombatEvidenceNegativeStatusNotify, id).Scan(
		&interval, &sequence, &e.ProtocolSession, &e.Outcome, &started, &e.StartBasis, &startConfirmed, &battle,
		&ended, &endBasis, &decided, &firstActivity, &lastActivity, &reentries,
		&e.Totals.DamageDealt, &e.Totals.DamageTaken, &outgoing, &incoming, &e.Totals.UnattributedOutgoingDamage, &e.Totals.UnattributedIncomingDamage, &unattributedOut, &unattributedIn,
		&integrityStatus, &e.Integrity.TransportLosses, &e.Integrity.UnrecoveredLosses, &downgraded, &firstLoss,
		&dpsStatus, &dps.Reason, &dps.Value, &dps.Damage, &dpsDuration, &dpsStart, &dpsEnd, &dps.Confidence,
		&peakStatus, &peak.Reason, &peak.Value, &peak.Damage, &peakDuration, &peakStart, &peakEnd, &peak.Confidence,
		&healingStatus, &e.Healing.Reason, &e.Healing.Effective, &e.Healing.Reported, &e.Healing.Overheal, &healingEvents, &e.Healing.UnknownHealerEffective, &healingUnknownEvents,
		&e.UnassignedRole.DamageDealt, &unassignedOut, &e.UnassignedRole.DamageTaken, &unassignedIn, &unassignedOverflow, &state,
		&negativeStatusDamage, &negativeStatusEvents)
	if err != nil {
		return e, fmt.Errorf("read history encounter: %w", err)
	}
	e.EncounterID = "history-" + strconv.FormatInt(id, 10)
	e.StatisticsIntervalID, e.Sequence, e.State = uint64(interval), uint64(sequence), capture.EncounterFinished
	if state != "finished" {
		e.State = capture.EncounterActive
	}
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
	e.UnassignedRole.NegativeStatusDamage, e.UnassignedRole.NegativeStatusEvents = negativeStatusDamage, uint64(negativeStatusEvents)
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
	if err := rows.Err(); err != nil {
		return err
	}
	return s.roleStatuses(ctx, id, e.Roles)
}

// roleStatuses fills the negative-status damage the engine credited to each role. The engine stores it as the damage of
// the role (encounter_contributor.damage_dealt) outside the direct and summon split, and keeps its rows only on the
// events: each is a local negative-status result whose source role is the credited one (negative_status_credit.go).
// History written before the credit existed has no such events, so its roles keep no status damage.
func (s *Store) roleStatuses(ctx context.Context, id int64, roles []capture.RoleDamageSnapshot) error {
	if len(roles) == 0 {
		return nil
	}
	index := make(map[[2]uint64]int, len(roles))
	for i, role := range roles {
		index[[2]uint64{role.PlayerID, role.RoleID}] = i
	}
	rows, err := s.db.QueryContext(ctx, `SELECT coalesce(source_player_id, 0), source_role_id, damage_id, sum(amount), count(*),
		coalesce(sum(CASE WHEN source_role_basis = ? THEN amount END), 0), coalesce(sum(CASE WHEN source_role_basis = ? THEN 1 END), 0)
		FROM combat_event WHERE encounter_id = ? AND kind = 'damage' AND flow = ? AND evidence = ? AND source_role_id IS NOT NULL
		GROUP BY coalesce(source_player_id, 0), source_role_id, damage_id ORDER BY 1, 2, 3`,
		capture.RoleBasisNegativeStatusTrigger, capture.RoleBasisNegativeStatusTrigger, id, string(capture.CombatFlowLocalOutgoing), capture.CombatEvidenceNegativeStatusNotify)
	if err != nil {
		return fmt.Errorf("read history status damage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var player, role, damageID, amount, events, triggerDamage, triggerEvents int64
		if err := rows.Scan(&player, &role, &damageID, &amount, &events, &triggerDamage, &triggerEvents); err != nil {
			return fmt.Errorf("read history status damage: %w", err)
		}
		i, ok := index[[2]uint64{uint64(player), uint64(role)}]
		if !ok {
			continue // a credited role the engine left out of its bounded role table; its damage is unassigned there
		}
		roles[i].StatusDamage += amount
		roles[i].Statuses = append(roles[i].Statuses, capture.StatusDamageSnapshot{DamageID: uint64(damageID), DamageDealt: amount, OutgoingEvents: uint64(events),
			TriggerDamage: triggerDamage, TriggerEvents: uint64(triggerEvents)})
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
