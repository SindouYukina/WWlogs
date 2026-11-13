package history

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"wwlogs/internal/capture"
)

// The History Library: every listed encounter of the history at once, filtered and sorted, and the evidence-based class
// each record has for totals and personal records (Career uses the same index, career.go).
//
// Classes are read from stored fields only, never from the build that recorded a row:
//
//   - complete: finished, every journal entry stored, integrity INTACT, and no known defect of its analysis.
//   - degraded: finished and complete, but transport losses touched it (integrity DEGRADED): totals are lower bounds.
//   - known_affected: finished and complete as stored, but its protocol session's stored result keys restart below the
//     client's RequestId counter after reaching its top: the analyser keyed results by the raw RequestId, so after the
//     counter wrapped it dropped every result whose RequestId had been used before (fixed in v0.7.0 Build 32, which keys
//     results by the RequestId continued past the wrap). Its totals may be low; how much is unknown from the history.
//   - incomplete: finished, but entries of it never reached storage.
//   - unreliable: integrity UNRELIABLE: a boundary or the extent of a loss is unknown, so no DPS is shown.
//   - unfinished: still recording (open) or ended with the application (abandoned): the values are a checkpoint.
//
// Totals count complete, degraded and known_affected records (the last two as lower bounds); personal records count
// complete records only.

// Record classes.
const (
	ClassComplete      = "complete"
	ClassDegraded      = "degraded"
	ClassKnownAffected = "known_affected"
	ClassIncomplete    = "incomplete"
	ClassUnreliable    = "unreliable"
	ClassUnfinished    = "unfinished"
)

// recordClasses lists the classes in report order.
var recordClasses = []string{ClassComplete, ClassDegraded, ClassKnownAffected, ClassIncomplete, ClassUnreliable, ClassUnfinished}

const (
	// requestCounterTop is near the top of the client's combat RequestId counter (1..32767, then 0): a stored key that
	// falls far below a key at least this high means the counter wrapped and the analyser did not continue it.
	requestCounterTop = 30000
	// requestCounterDrop is the fall that marks a restart: half the counter's space, as the engine's own wrap rule uses.
	requestCounterDrop = 16384
	// MaxLibraryPage bounds one page of the library.
	MaxLibraryPage = 200
)

// indexedRole is a role contributor of an encounter: its runtime RoleId (a trial role keeps its own) and its damage.
type indexedRole struct {
	RoleID uint64 `json:"roleId"`
	Damage int64  `json:"damage"`
}

// indexedEncounter is the compact stored form of one listed encounter that libraries and careers read.
type indexedEncounter struct {
	ID              int64
	Span            int64
	ProtocolSession int64
	StartedNs       int64
	EndedNs         int64 // 0 when not ended
	State           string
	Complete        bool
	Integrity       string
	DPSStatus       string
	DPSValue        float64
	DPSDurationNs   int64
	PeakStatus      string
	PeakValue       float64
	Damage          int64
	HasDamage       bool
	Instance        uint64
	InstanceKnown   bool
	GameVersion     string
	Source          string
	Roles           []indexedRole
	Vehicle         bool
	Affected        bool
}

// Class is the record class of an encounter.
func (e *indexedEncounter) Class() string {
	switch {
	case e.State != "finished":
		return ClassUnfinished
	case e.Integrity == string(capture.IntegrityUnreliable):
		return ClassUnreliable
	case !e.Complete:
		return ClassIncomplete
	case e.Affected:
		return ClassKnownAffected
	case e.Integrity == string(capture.IntegrityDegraded):
		return ClassDegraded
	}
	return ClassComplete
}

// counted reports whether the record enters totals; recordEligible whether it may hold a personal record.
func (e *indexedEncounter) counted() bool {
	switch e.Class() {
	case ClassComplete, ClassDegraded, ClassKnownAffected:
		return true
	}
	return false
}

func (e *indexedEncounter) recordEligible() bool { return e.Class() == ClassComplete }

// lowerBound reports a counted record whose values may be low.
func (e *indexedEncounter) lowerBound() bool {
	class := e.Class()
	return class == ClassDegraded || class == ClassKnownAffected
}

// durationSeconds is the encounter's wall-clock duration as the Encounter DPS uses it (dps_duration_ns), or its stored
// span when the DPS had none; 0 when unknown.
func (e *indexedEncounter) durationSeconds() float64 {
	if e.DPSDurationNs > 0 {
		return float64(e.DPSDurationNs) / 1e9
	}
	if e.EndedNs > e.StartedNs {
		return float64(e.EndedNs-e.StartedNs) / 1e9
	}
	return 0
}

// dps is the Encounter DPS shown for a finished record with a usable DPS: confirmed damage over the wall-clock duration.
func (e *indexedEncounter) dps() (float64, bool) {
	if e.State != "finished" || e.Integrity == string(capture.IntegrityUnreliable) {
		return 0, false
	}
	switch capture.DPSStatus(e.DPSStatus) {
	case capture.DPSStatusAvailable, capture.DPSStatusLowerBound:
		return e.DPSValue, e.DPSValue > 0
	}
	return 0, false
}

// indexCache keeps the index of one history state and the per-encounter facts read from its results.
type indexCache struct {
	mu          sync.Mutex
	fingerprint string
	encounters  []indexedEncounter
	// maxHits holds, per finished encounter, its largest confirmed outgoing hit per runtime role (0: no role).
	maxHits map[int64]map[uint64]maxHit
}

type maxHit struct {
	Amount  int64
	SkillID uint64
}

func (c *indexCache) clear() {
	c.mu.Lock()
	c.fingerprint, c.encounters, c.maxHits = "", nil, nil
	c.mu.Unlock()
}

// fingerprint changes with every stored change an index reads: an encounter added, removed, finished or checkpointed.
func (s *Store) fingerprint(ctx context.Context) (string, error) {
	var count, maxID int64
	var ended, checkpoints, damage sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(max(id), 0), total(ended_at_ns), total(checkpoint_at_ns), total(damage_dealt)
		FROM encounter WHERE state IN `+listedStates).Scan(&count, &maxID, &ended, &checkpoints, &damage)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d/%d/%.0f/%.0f/%.0f", count, maxID, ended.Float64, checkpoints.Float64, damage.Float64), nil
}

// index returns every listed encounter in start order, read once per history state.
func (s *Store) index(ctx context.Context) ([]indexedEncounter, error) {
	if !s.readable() {
		return nil, ErrUnavailable
	}
	current, err := s.fingerprint(ctx)
	if err != nil {
		return nil, fmt.Errorf("read history index: %w", err)
	}
	s.library.mu.Lock()
	defer s.library.mu.Unlock()
	if s.library.encounters != nil && s.library.fingerprint == current {
		return s.library.encounters, nil
	}
	encounters, err := s.readIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("read history index: %w", err)
	}
	if s.library.maxHits == nil {
		s.library.maxHits = map[int64]map[uint64]maxHit{}
	}
	s.library.fingerprint, s.library.encounters = current, encounters
	return encounters, nil
}

func (s *Store) readIndex(ctx context.Context) ([]indexedEncounter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.capture_session_id, e.protocol_session, e.started_at_ns, coalesce(e.ended_at_ns, 0), e.state, e.complete,
		coalesce(e.integrity_status, ''), coalesce(e.dps_status, ''), coalesce(e.dps_value, 0), coalesce(e.dps_duration_ns, 0), coalesce(e.peak_status, ''),
		coalesce(e.peak_value, 0), e.damage_dealt, e.scene_instance_id, p.game_version, s.source`+summaryFrom+` WHERE e.state IN `+listedStates+`
		ORDER BY e.started_at_ns, e.id`)
	if err != nil {
		return nil, err
	}
	var out []indexedEncounter
	position := map[int64]int{}
	for rows.Next() {
		var e indexedEncounter
		var complete int
		var damage, instance sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Span, &e.ProtocolSession, &e.StartedNs, &e.EndedNs, &e.State, &complete, &e.Integrity, &e.DPSStatus, &e.DPSValue,
			&e.DPSDurationNs, &e.PeakStatus, &e.PeakValue, &damage, &instance, &e.GameVersion, &e.Source); err != nil {
			rows.Close()
			return nil, err
		}
		e.Complete = complete == 1
		e.Damage, e.HasDamage = damage.Int64, damage.Valid
		if instance.Valid {
			e.Instance, e.InstanceKnown = uint64(instance.Int64), true
		}
		position[e.ID] = len(out)
		out = append(out, e)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	contributors, err := s.db.QueryContext(ctx, `SELECT encounter_id, kind, role_id, damage_dealt FROM encounter_contributor ORDER BY encounter_id, ordinal`)
	if err != nil {
		return nil, err
	}
	for contributors.Next() {
		var id, role, damage int64
		var kind string
		if err := contributors.Scan(&id, &kind, &role, &damage); err != nil {
			contributors.Close()
			return nil, err
		}
		at, ok := position[id]
		if !ok {
			continue
		}
		switch kind {
		case "role":
			out[at].Roles = append(out[at].Roles, indexedRole{RoleID: uint64(role), Damage: damage})
		case "vehicle":
			out[at].Vehicle = true
		}
	}
	if err := contributors.Close(); err != nil {
		return nil, err
	}
	affected, err := s.affectedEncounters(ctx)
	if err != nil {
		return nil, err
	}
	for id := range affected {
		if at, ok := position[id]; ok {
			out[at].Affected = true
		}
	}
	return out, nil
}

// affectedEncounters finds the encounters recorded after an unhandled wrap of the client's RequestId counter: in their
// protocol session a stored result key falls more than half the counter's space below a key at or near its top.
func (s *Store) affectedEncounters(ctx context.Context) (map[int64]bool, error) {
	sessions, err := s.db.QueryContext(ctx, `SELECT capture_session_id, protocol_session FROM combat_event WHERE rpc_id != 0
		GROUP BY capture_session_id, protocol_session HAVING max(rpc_id) >= ? AND max(rpc_id) < ?`, requestCounterTop, 32768)
	if err != nil {
		return nil, err
	}
	type key struct{ span, protocol int64 }
	var candidates []key
	for sessions.Next() {
		var k key
		if err := sessions.Scan(&k.span, &k.protocol); err != nil {
			sessions.Close()
			return nil, err
		}
		candidates = append(candidates, k)
	}
	if err := sessions.Close(); err != nil {
		return nil, err
	}
	affected := map[int64]bool{}
	for _, k := range candidates {
		var restart sql.NullInt64
		var restartAt sql.NullInt64
		err := s.db.QueryRowContext(ctx, `SELECT sequence, observed_at_ns FROM (
			SELECT sequence, observed_at_ns, rpc_id, max(rpc_id) OVER (ORDER BY sequence ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS prior
			FROM combat_event WHERE capture_session_id = ? AND protocol_session = ? AND rpc_id != 0)
			WHERE prior >= ? AND rpc_id + ? < prior ORDER BY sequence LIMIT 1`, k.span, k.protocol, requestCounterTop, requestCounterDrop).Scan(&restart, &restartAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		// The encounter holding the first restarted key and every later encounter of the protocol session.
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM encounter WHERE capture_session_id = ? AND protocol_session = ? AND (
			id IN (SELECT encounter_id FROM combat_event WHERE capture_session_id = ? AND sequence >= ? AND encounter_id IS NOT NULL)
			OR coalesce(ended_at_ns, checkpoint_at_ns, last_activity_at_ns, started_at_ns) >= ?)`, k.span, k.protocol, k.span, restart.Int64, restartAt.Int64)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			affected[id] = true
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return affected, nil
}

// maxHits returns, for each of the given finished encounters, its largest confirmed outgoing hit per runtime role (role
// 0: hits credited to no role, such as a vehicle's). Results of a finished encounter never change, so each is read once.
func (s *Store) maxHits(ctx context.Context, ids []int64) (map[int64]map[uint64]maxHit, error) {
	s.library.mu.Lock()
	defer s.library.mu.Unlock()
	if s.library.maxHits == nil {
		s.library.maxHits = map[int64]map[uint64]maxHit{}
	}
	var missing []string
	for _, id := range ids {
		if _, ok := s.library.maxHits[id]; !ok {
			missing = append(missing, strconv.FormatInt(id, 10))
		}
	}
	for start := 0; start < len(missing); start += 500 {
		end := min(start+500, len(missing))
		chunk := missing[start:end]
		// SQLite returns the other columns of the row that holds the max().
		rows, err := s.db.QueryContext(ctx, `SELECT encounter_id, coalesce(source_role_id, 0), max(amount), skill_id FROM combat_event
			WHERE encounter_id IN (`+strings.Join(chunk, ",")+`) AND kind = 'damage' AND flow = ? GROUP BY encounter_id, coalesce(source_role_id, 0)`,
			string(capture.CombatFlowLocalOutgoing))
		if err != nil {
			return nil, fmt.Errorf("read largest hits: %w", err)
		}
		for _, raw := range chunk {
			id, _ := strconv.ParseInt(raw, 10, 64)
			s.library.maxHits[id] = map[uint64]maxHit{}
		}
		for rows.Next() {
			var id, role, amount, skill int64
			if err := rows.Scan(&id, &role, &amount, &skill); err != nil {
				rows.Close()
				return nil, fmt.Errorf("read largest hits: %w", err)
			}
			s.library.maxHits[id][uint64(role)] = maxHit{Amount: amount, SkillID: uint64(skill)}
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("read largest hits: %w", err)
		}
	}
	out := make(map[int64]map[uint64]maxHit, len(ids))
	for _, id := range ids {
		out[id] = s.library.maxHits[id]
	}
	return out, nil
}

// LibraryQuery filters the library. Dates are calendar dates of the view's zone, inclusive. Instances and RoleIDs are set
// by the host from Master-resolved filters (mode, stage, character, text); nil means any.
type LibraryQuery struct {
	From         string   `json:"from,omitempty"`
	To           string   `json:"to,omitempty"`
	GameVersions []string `json:"gameVersions,omitempty"`
	Classes      []string `json:"classes,omitempty"`
	Source       string   `json:"source,omitempty"`
	MinSeconds   float64  `json:"minSeconds,omitempty"`
	MaxSeconds   float64  `json:"maxSeconds,omitempty"`
	Sort         string   `json:"sort,omitempty"`
	Offset       int      `json:"offset,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Instances    []uint64 `json:"-"`
	RoleIDs      []uint64 `json:"-"`
	// MatchInstances and MatchRoleIDs are a text search resolved by the host: an encounter matches when its instance or one
	// of its roles is listed. Both nil: no search.
	MatchInstances []uint64 `json:"-"`
	MatchRoleIDs   []uint64 `json:"-"`
}

// Library sorts.
const (
	SortStartDesc    = "start_desc"
	SortStartAsc     = "start_asc"
	SortDPSDesc      = "dps_desc"
	SortDamageDesc   = "damage_desc"
	SortDurationDesc = "duration_desc"
)

// LibraryEntry is one listed encounter with its class and role contributors (runtime RoleIds, in the engine's order).
type LibraryEntry struct {
	EncounterSummary
	Class   string        `json:"class"`
	Roles   []indexedRole `json:"roles"`
	Vehicle bool          `json:"vehicle,omitempty"`
}

// LibraryCount is a facet value with how many encounters of the dated selection have it.
type LibraryCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// LibraryFacets describe the selection before the facet filters themselves apply, so a filter can be changed back.
type LibraryFacets struct {
	GameVersions []LibraryCount `json:"gameVersions"`
	Classes      []LibraryCount `json:"classes"`
	Sources      []LibraryCount `json:"sources"`
	Instances    map[uint64]int `json:"-"`
	Roles        map[uint64]int `json:"-"`
	FirstDate    string         `json:"firstDate,omitempty"`
	LastDate     string         `json:"lastDate,omitempty"`
}

// LibraryView is one page of the filtered library.
type LibraryView struct {
	Total      int            `json:"total"`
	Offset     int            `json:"offset"`
	Encounters []LibraryEntry `json:"encounters"`
	Facets     LibraryFacets  `json:"facets"`
}

// dateIn reports whether an instant's date in zone lies in [from, to] (empty bounds are open).
func dateIn(ns int64, zone *time.Location, from, to string) (string, bool) {
	date := time.Unix(0, ns).In(zone).Format(dateLayout)
	return date, (from == "" || date >= from) && (to == "" || date <= to)
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func idSet(values []uint64) map[uint64]bool {
	if values == nil {
		return nil
	}
	set := make(map[uint64]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// Library lists one page of the encounters the query selects, with the facets of its dated selection.
func (s *Store) Library(ctx context.Context, query LibraryQuery, zone *time.Location) (LibraryView, error) {
	view := LibraryView{Encounters: []LibraryEntry{}, Facets: LibraryFacets{GameVersions: []LibraryCount{}, Classes: []LibraryCount{}, Sources: []LibraryCount{}}}
	zone = viewLocation(zone)
	index, err := s.index(ctx)
	if err != nil {
		return view, err
	}
	instances, roles := idSet(query.Instances), idSet(query.RoleIDs)
	searching := query.MatchInstances != nil || query.MatchRoleIDs != nil
	matchInstances, matchRoles := idSet(query.MatchInstances), idSet(query.MatchRoleIDs)
	versions, classes, sources := map[string]int{}, map[string]int{}, map[string]int{}
	view.Facets.Instances, view.Facets.Roles = map[uint64]int{}, map[uint64]int{}
	var selected []*indexedEncounter
	for i := range index {
		e := &index[i]
		date, in := dateIn(e.StartedNs, zone, query.From, query.To)
		if view.Facets.FirstDate == "" || date < view.Facets.FirstDate {
			view.Facets.FirstDate = date
		}
		if date > view.Facets.LastDate {
			view.Facets.LastDate = date
		}
		if !in {
			continue
		}
		versions[e.GameVersion]++
		classes[e.Class()]++
		sources[e.Source]++
		if e.InstanceKnown {
			view.Facets.Instances[e.Instance]++
		}
		for _, role := range e.Roles {
			view.Facets.Roles[role.RoleID]++
		}
		switch {
		case len(query.GameVersions) > 0 && !containsString(query.GameVersions, e.GameVersion):
			continue
		case len(query.Classes) > 0 && !containsString(query.Classes, e.Class()):
			continue
		case query.Source != "" && query.Source != e.Source:
			continue
		case instances != nil && (!e.InstanceKnown || !instances[e.Instance]):
			continue
		}
		if roles != nil {
			found := false
			for _, role := range e.Roles {
				found = found || roles[role.RoleID]
			}
			if !found {
				continue
			}
		}
		if searching {
			found := e.InstanceKnown && matchInstances[e.Instance]
			for _, role := range e.Roles {
				found = found || matchRoles[role.RoleID]
			}
			if !found {
				continue
			}
		}
		seconds := e.durationSeconds()
		if query.MinSeconds > 0 && seconds < query.MinSeconds || query.MaxSeconds > 0 && (seconds == 0 || seconds > query.MaxSeconds) {
			continue
		}
		selected = append(selected, e)
	}
	view.Facets.GameVersions = counts(versions)
	view.Facets.Classes = make([]LibraryCount, 0, len(recordClasses))
	for _, class := range recordClasses {
		if classes[class] > 0 {
			view.Facets.Classes = append(view.Facets.Classes, LibraryCount{Value: class, Count: classes[class]})
		}
	}
	view.Facets.Sources = counts(sources)
	sortLibrary(selected, query.Sort)
	view.Total = len(selected)
	offset, limit := max(query.Offset, 0), query.Limit
	if limit <= 0 || limit > MaxLibraryPage {
		limit = MaxLibraryPage
	}
	view.Offset = offset
	if offset >= len(selected) {
		return view, nil
	}
	page := selected[offset:min(offset+limit, len(selected))]
	ids := make([]string, 0, len(page))
	for _, e := range page {
		ids = append(ids, strconv.FormatInt(e.ID, 10))
	}
	summaries, err := s.summaries(ctx, ids, zone)
	if err != nil {
		return view, err
	}
	for _, e := range page {
		summary, ok := summaries[e.ID]
		if !ok {
			continue
		}
		view.Encounters = append(view.Encounters, LibraryEntry{EncounterSummary: summary, Class: e.Class(), Roles: append([]indexedRole{}, e.Roles...), Vehicle: e.Vehicle})
	}
	return view, nil
}

func counts(values map[string]int) []LibraryCount {
	out := make([]LibraryCount, 0, len(values))
	for value, count := range values {
		out = append(out, LibraryCount{Value: value, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func sortLibrary(selected []*indexedEncounter, by string) {
	key := func(e *indexedEncounter) float64 {
		switch by {
		case SortDPSDesc:
			value, _ := e.dps()
			return value
		case SortDamageDesc:
			if e.counted() || e.HasDamage {
				return float64(e.Damage)
			}
			return 0
		case SortDurationDesc:
			return e.durationSeconds()
		}
		return float64(e.StartedNs)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		if by == SortStartAsc {
			return a.StartedNs < b.StartedNs || a.StartedNs == b.StartedNs && a.ID < b.ID
		}
		ka, kb := key(a), key(b)
		if ka != kb {
			return ka > kb
		}
		return a.StartedNs > b.StartedNs || a.StartedNs == b.StartedNs && a.ID > b.ID
	})
}

// summaries reads the full list summaries of the given encounters, with their main enemies.
func (s *Store) summaries(ctx context.Context, ids []string, zone *time.Location) (map[int64]EncounterSummary, error) {
	out := map[int64]EncounterSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+summaryColumns+summaryFrom+" WHERE e.id IN ("+strings.Join(ids, ",")+")")
	if err != nil {
		return nil, fmt.Errorf("read history library: %w", err)
	}
	for rows.Next() {
		row, err := scanSummary(rows, zone)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("read history library: %w", err)
		}
		out[row.summary.ID] = row.summary
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read history library: %w", err)
	}
	enemies, err := s.enemiesOf(ctx, "encounter_id IN ("+strings.Join(ids, ",")+")")
	if err != nil {
		return nil, err
	}
	for id, list := range enemies {
		if summary, ok := out[id]; ok {
			summary.Enemies, summary.MainEnemy = len(list), mainEnemy(list)
			out[id] = summary
		}
	}
	return out, nil
}
