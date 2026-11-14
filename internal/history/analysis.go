package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"wwlogs/internal/capture"
)

// Combat analysis of one stored encounter.
//
// The History analysis workspace explores a finished encounter by time range, contributor and enemy. Every combat number is
// rebuilt from the encounter's stored combat results with the engine's own rules: the contributor a result is credited to
// (role_aggregation.go), the skill row it joins (skill_breakdown.go), the healing members of the encounter (healing.go), and
// the 1-second DPS timeline with its clamping (encounter_timeline.go). The full range without filters therefore reproduces
// the stored engine result exactly; a model checks that when it is built and reports it (Consistent), so a record whose
// stored results are incomplete never presents range numbers as if they added up. A range is a part of the encounter's own
// time base, [start, end], and every DPS of a range uses the engine's definition: confirmed damage over the range's real
// duration, with the encounter's data integrity applied by the engine's own function.
//
// Skill hits are DamageContext.SkillId hits, never casts: no build stores a server-confirmed skill cast.

// Analysis bounds. An encounter beyond them is not analysed rather than analysed in part.
const (
	maxAnalysisCombatEvents = 200000
	maxAnalysisEffectEvents = 400000
	// maxAnalysisTimelinePoints bounds the DPS timeline of an analysis: one-second points up to ten minutes, wider points
	// (2, 5, 10 s...) beyond, each an exact sum of stored seconds.
	maxAnalysisTimelinePoints = 600
	// analysisSkillBins is the number of equal parts of the encounter a skill's damage distribution is counted in.
	analysisSkillBins = 48
	// maxCachedModels bounds the encounter models kept for repeated queries.
	maxCachedModels = 3
)

// Analysis states.
const (
	AnalysisReady = "ready"
	// AnalysisNotFinished: only a finished encounter has a final result its stored results can be checked against.
	AnalysisNotFinished = "not_finished"
	// AnalysisTooLarge: the encounter holds more stored results than one analysis reads.
	AnalysisTooLarge = "too_large"
)

// AnalysisQuery selects part of one stored encounter.
type AnalysisQuery struct {
	// FromMs and ToMs bound the range in milliseconds from the encounter start; ToMs <= 0 means the end. Both are clamped
	// to the encounter.
	FromMs int64 `json:"fromMs"`
	ToMs   int64 `json:"toMs"`
	// Contributor is a contributor key (role-<player>-<role> or vehicle-<kind>), or empty for the whole team.
	Contributor string `json:"contributor"`
	// Enemy is an enemy instance ordinal, or -1 for every enemy.
	Enemy int `json:"enemy"`
	// Effects is "hud" (effects the game draws in a HUD buff bar, on or from the local team) or "all".
	Effects string `json:"effects"`
	// SkillRow selects one skill row ("<contributor>:<SkillId>" or "<contributor>:unassigned") whose hits the timeline marks.
	SkillRow string `json:"skillRow"`
}

// AnalysisRange is the range a result describes, in milliseconds from the encounter start.
type AnalysisRange struct {
	FromMs     float64 `json:"fromMs"`
	ToMs       float64 `json:"toMs"`
	DurationMs float64 `json:"durationMs"`
	// Full is set when the range is the whole encounter.
	Full bool `json:"full"`
}

// analysisContributor is one stored contributor and the naming evidence of its skill rows.
type analysisContributor struct {
	key        string
	vehicle    bool
	player     uint64
	role       uint64
	vehicleOf  capture.VehicleDamageSnapshot
	skills     map[uint64]capture.SkillDamageSnapshot
	unassigned capture.UnassignedSkillTotals
}

type entityKey struct {
	session    int
	entity     uint64
	generation uint64
}

type analysisEndpoint struct {
	key      entityKey
	relation string
	player   sql.NullInt64
	role     sql.NullInt64
	basis    sql.NullString
	monster  uint64
	template uint64
}

// analysisEvent is one stored combat result with what the engine's rules derive from it.
type analysisEvent struct {
	sequence     int64
	at           int64 // observed time (UTC ns); 0 without a time
	rel          int64 // ns from the encounter start, clamped to [0, duration]; -1 without a time
	healing      bool
	flow         capture.CombatFlow
	confidence   string
	evidence     string
	amount       int64
	applied      int64
	inconsistent bool
	skill        uint64
	damage       uint64
	vehicle      string
	downgrade    string
	source       analysisEndpoint
	target       analysisEndpoint
	// contributor is the outgoing contributor index, -1 for unassigned outgoing damage; overflow marks a role the engine's
	// role bound left out, which it counted as unassigned.
	contributor int
	overflow    bool
	summon      bool
	// taken is the contributor index of the target role of incoming damage, -1 when unassigned.
	taken int
	// enemy is the enemy ordinal of a confirmed local flow's non-player endpoint, -1 when there is none or it was not mapped.
	enemy int
}

// encounterModel is everything an analysis of one encounter reads, loaded once.
type encounterModel struct {
	id           int64
	session      int64
	protocol     int
	start        time.Time
	end          time.Time
	duration     time.Duration
	stored       capture.EncounterSnapshot
	timeline     *capture.StoredTimeline
	integrity    capture.DataIntegrity
	healingBase  capture.HealingResult
	contributors []analysisContributor
	byKey        map[string]int
	events       []analysisEvent
	enemies      []capture.EncounterEnemySnapshot
	// enemyKeys maps each mapped enemy ordinal to its instance; unmapped ordinals are absent.
	enemyKeys map[int]entityKey
	enemyOf   map[entityKey]int
	untimed   int
	// consistent is set when the full range rebuilt from the stored results equals the stored engine result.
	consistent bool
	mismatch   string
	effects    *effectModel
	gaps       []analysisGap
	rejected   int64
	cacheable  bool
}

type analysisGap struct {
	from, to  int64 // UTC ns; to is the encounter end when the loss lasted
	direction string
	failed    bool
	open      bool
}

// modelCache keeps the models of recently analysed encounters. Only encounters whose capture session is no longer recording
// are kept: nothing changes them afterwards except a deletion, which clears the cache.
//
// Opening an encounter asks for its analysis, effect lanes and skill hits at once, and each needs the same model, so requests
// for a model that is not cached share one load (load): the encounter is read and rebuilt once, not once per request.
type modelCache struct {
	mu      sync.Mutex
	models  map[int64]*encounterModel
	order   []int64
	loading map[int64]*modelLoad
	// generation counts clears: a load that began before a clear may have read what the deletion removed, so it is never
	// kept.
	generation uint64
}

// modelLoad is one running load of a model; done closes once model and err are set.
type modelLoad struct {
	done  chan struct{}
	model *encounterModel
	err   error
}

// load returns the cached model of id, joins the load of it already running, or runs read once and keeps a cacheable result.
// A request that joins a running load stops waiting when its own ctx ends.
func (c *modelCache) load(ctx context.Context, id int64, read func() (*encounterModel, error)) (*encounterModel, error) {
	c.mu.Lock()
	if model := c.models[id]; model != nil {
		c.mu.Unlock()
		return model, nil
	}
	if running := c.loading[id]; running != nil {
		c.mu.Unlock()
		select {
		case <-running.done:
			return running.model, running.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.loading == nil {
		c.loading = map[int64]*modelLoad{}
	}
	running := &modelLoad{done: make(chan struct{})}
	c.loading[id] = running
	generation := c.generation
	c.mu.Unlock()
	running.model, running.err = read()
	c.mu.Lock()
	delete(c.loading, id)
	if running.err == nil && running.model.cacheable && generation == c.generation {
		c.putLocked(running.model)
	}
	c.mu.Unlock()
	close(running.done)
	return running.model, running.err
}

func (c *modelCache) putLocked(model *encounterModel) {
	if c.models == nil {
		c.models = map[int64]*encounterModel{}
	}
	if _, ok := c.models[model.id]; !ok {
		c.order = append(c.order, model.id)
	}
	c.models[model.id] = model
	for len(c.order) > maxCachedModels {
		delete(c.models, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *modelCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models, c.order = nil, nil
	c.generation++
}

// errAnalysisState carries a state other than ready.
type errAnalysisState struct{ state string }

func (e errAnalysisState) Error() string { return "the encounter cannot be analysed: " + e.state }

// model loads or reuses the analysis model of one listed encounter.
func (s *Store) model(ctx context.Context, id int64, masters *capture.MasterData) (*encounterModel, error) {
	if !s.readable() {
		return nil, ErrUnavailable
	}
	var state, sessionStatus string
	err := s.db.QueryRowContext(ctx, "SELECT e.state, s.status FROM encounter e JOIN capture_session s ON s.id = e.capture_session_id WHERE e.id = ? AND e.state IN "+listedStates, id).Scan(&state, &sessionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read history analysis: %w", err)
	}
	if state != "finished" {
		return nil, errAnalysisState{AnalysisNotFinished}
	}
	return s.models.load(ctx, id, func() (*encounterModel, error) {
		model, err := s.loadModel(ctx, id, masters)
		if err != nil {
			return nil, err
		}
		model.cacheable = sessionStatus != "open"
		return model, nil
	})
}

func (s *Store) loadModel(ctx context.Context, id int64, masters *capture.MasterData) (*encounterModel, error) {
	detail, err := s.encounter(ctx, id, time.UTC, false)
	if err != nil {
		return nil, err
	}
	if detail.Snapshot == nil || detail.Summary.EndedAt == nil {
		return nil, errAnalysisState{AnalysisNotFinished}
	}
	model := &encounterModel{
		id: id, stored: *detail.Snapshot, timeline: detail.Timeline, integrity: detail.Snapshot.Integrity, enemies: detail.Enemies,
		rejected: detail.Session.EffectsRejected, byKey: map[string]int{},
		enemyKeys: map[int]entityKey{}, enemyOf: map[entityKey]int{},
	}
	model.start, model.end = detail.Snapshot.StartedAt, detail.Snapshot.EndedAt
	model.duration = model.end.Sub(model.start)
	if model.duration < 0 {
		model.duration = 0
	}
	var healingStatus, healingReason sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT capture_session_id, protocol_session, healing_status, healing_reason FROM encounter WHERE id = ?", id).Scan(&model.session, &model.protocol, &healingStatus, &healingReason); err != nil {
		return nil, fmt.Errorf("read history analysis: %w", err)
	}
	// The stored healing before integrity: a lower bound from the engine's own evidence (an inconsistent result or its
	// capacity) stays one for every range.
	model.healingBase = capture.HealingResult{Status: capture.DPSStatus(healingStatus.String), Reason: healingReason.String}
	for _, role := range model.stored.Roles {
		model.addContributor(analysisContributor{key: roleKey(role.PlayerID, role.RoleID), player: role.PlayerID, role: role.RoleID, skills: skillEvidence(role.Skills), unassigned: role.UnassignedSkill})
	}
	if vehicle := model.stored.Vehicle; vehicle != nil {
		model.addContributor(analysisContributor{key: "vehicle-" + vehicle.Kind, vehicle: true, vehicleOf: *vehicle, skills: skillEvidence(vehicle.Skills), unassigned: vehicle.UnassignedSkill})
	}
	if err := s.loadCombatEvents(ctx, model); err != nil {
		return nil, err
	}
	model.mapEnemies()
	model.check()
	if err := s.loadGaps(ctx, model); err != nil {
		return nil, err
	}
	effects, err := s.loadEffects(ctx, model, masters)
	if err != nil {
		return nil, err
	}
	model.effects = effects
	return model, nil
}

func roleKey(player, role uint64) string {
	return "role-" + strconv.FormatUint(player, 10) + "-" + strconv.FormatUint(role, 10)
}

func skillEvidence(skills []capture.SkillDamageSnapshot) map[uint64]capture.SkillDamageSnapshot {
	out := make(map[uint64]capture.SkillDamageSnapshot, len(skills))
	for _, skill := range skills {
		out[skill.SkillID] = skill
	}
	return out
}

func (m *encounterModel) hasContributor(key string) bool {
	_, ok := m.byKey[key]
	return ok
}

func (m *encounterModel) addContributor(contributor analysisContributor) {
	m.byKey[contributor.key] = len(m.contributors)
	m.contributors = append(m.contributors, contributor)
}

func nullUint(value sql.NullInt64) uint64 {
	if !value.Valid {
		return 0
	}
	return uint64(value.Int64)
}

// loadCombatEvents reads the encounter's stored results in acceptance order and credits each with the engine's rules.
func (s *Store) loadCombatEvents(ctx context.Context, model *encounterModel) error {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence, observed_at_ns, kind, flow, confidence, evidence, amount, applied_healing, healing_inconsistent,
		damage_id, skill_id, vehicle_kind, context_downgrade, protocol_session,
		source_entity_id, source_generation, source_relation, source_player_id, source_role_id, source_role_basis, source_monster_id, source_template_id,
		target_entity_id, target_generation, target_relation, target_player_id, target_role_id, target_role_basis, target_monster_id, target_template_id
		FROM combat_event WHERE encounter_id = ? ORDER BY capture_session_id, sequence LIMIT ?`, model.id, maxAnalysisCombatEvents+1)
	if err != nil {
		return fmt.Errorf("read history analysis events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var event analysisEvent
		var observed sql.NullInt64
		var kind, flow string
		var inconsistent, session int
		var damageID, skillID, sourceEntity, sourceGeneration, targetEntity, targetGeneration, sourceMonster, sourceTemplate, targetMonster, targetTemplate int64
		if err := rows.Scan(&event.sequence, &observed, &kind, &flow, &event.confidence, &event.evidence, &event.amount, &event.applied, &inconsistent,
			&damageID, &skillID, &event.vehicle, &event.downgrade, &session,
			&sourceEntity, &sourceGeneration, &event.source.relation, &event.source.player, &event.source.role, &event.source.basis, &sourceMonster, &sourceTemplate,
			&targetEntity, &targetGeneration, &event.target.relation, &event.target.player, &event.target.role, &event.target.basis, &targetMonster, &targetTemplate); err != nil {
			return fmt.Errorf("read history analysis events: %w", err)
		}
		if len(model.events) == maxAnalysisCombatEvents {
			return errAnalysisState{AnalysisTooLarge}
		}
		event.healing, event.flow, event.inconsistent = kind == string(capture.CombatEventHealing), capture.CombatFlow(flow), inconsistent == 1
		event.skill, event.damage = uint64(skillID), uint64(damageID)
		event.source.key = entityKey{session: session, entity: uint64(sourceEntity), generation: uint64(sourceGeneration)}
		event.target.key = entityKey{session: session, entity: uint64(targetEntity), generation: uint64(targetGeneration)}
		event.source.monster, event.source.template = uint64(sourceMonster), uint64(sourceTemplate)
		event.target.monster, event.target.template = uint64(targetMonster), uint64(targetTemplate)
		event.rel, event.contributor, event.taken, event.enemy = -1, -1, -1, -1
		if observed.Valid {
			event.at = observed.Int64
			event.rel = min(max(0, event.at-model.start.UnixNano()), int64(model.duration))
		} else {
			model.untimed++
		}
		model.credit(&event)
		model.events = append(model.events, event)
	}
	return rows.Err()
}

// credit applies roleAggregate.add to one stored result: a motorcycle result is the vehicle's, a result with a role is that
// role's (a role the engine's bound left out is unassigned, as the engine counted it), anything else is unassigned.
func (m *encounterModel) credit(event *analysisEvent) {
	if event.healing {
		return
	}
	switch event.flow {
	case capture.CombatFlowLocalOutgoing:
		switch {
		case event.vehicle != "":
			if index, ok := m.byKey["vehicle-"+event.vehicle]; ok {
				event.contributor = index
			}
		case event.source.role.Valid:
			if index, ok := m.byKey[roleKey(nullUint(event.source.player), nullUint(event.source.role))]; ok {
				event.contributor = index
				event.summon = event.source.basis.String == "summoner_chain"
			} else {
				event.overflow = true
			}
		}
	case capture.CombatFlowLocalIncoming:
		if event.target.role.Valid {
			if index, ok := m.byKey[roleKey(nullUint(event.target.player), nullUint(event.target.role))]; ok {
				event.taken = index
			} else {
				event.overflow = true
			}
		}
	}
}

// enemyEndpoint is the non-player endpoint of a confirmed local flow, the instance enemyAggregate.add credits.
func (e *analysisEvent) enemyEndpoint() (analysisEndpoint, bool) {
	if e.healing {
		return analysisEndpoint{}, false
	}
	switch e.flow {
	case capture.CombatFlowLocalOutgoing:
		return e.target, true
	case capture.CombatFlowLocalIncoming:
		return e.source, true
	}
	return analysisEndpoint{}, false
}

// mapEnemies ties the stored enemy instances to their entity instances. The stored rows keep no entity identity, so each row
// is matched to the one instance whose stored results add up to exactly its damage, hits and first and last times, as
// enemyAggregate.add summed them. A row without exactly one such instance is not filtered by, rather than guessed.
func (m *encounterModel) mapEnemies() {
	type tally struct {
		taken, dealt         int64
		hitsTaken, hitsDealt uint64
		first, last          int64
	}
	instances := map[entityKey]*tally{}
	var order []entityKey
	for _, event := range m.events {
		endpoint, ok := event.enemyEndpoint()
		if !ok {
			continue
		}
		t := instances[endpoint.key]
		if t == nil {
			t = &tally{}
			instances[endpoint.key] = t
			order = append(order, endpoint.key)
		}
		if event.flow == capture.CombatFlowLocalIncoming {
			t.dealt += event.amount
			t.hitsDealt++
		} else {
			t.taken += event.amount
			t.hitsTaken++
		}
		if event.at != 0 {
			if t.first == 0 || event.at < t.first {
				t.first = event.at
			}
			if event.at > t.last {
				t.last = event.at
			}
		}
	}
	nanos := func(at time.Time) int64 {
		if at.IsZero() {
			return 0
		}
		return at.UnixNano()
	}
	for _, enemy := range m.enemies {
		var match []entityKey
		for _, key := range order {
			t := instances[key]
			if t.taken == enemy.DamageTaken && t.hitsTaken == enemy.HitsTaken && t.dealt == enemy.DamageDealt && t.hitsDealt == enemy.HitsDealt &&
				t.first == nanos(enemy.FirstSeenAt) && t.last == nanos(enemy.LastSeenAt) {
				if _, taken := m.enemyOf[key]; !taken {
					match = append(match, key)
				}
			}
		}
		if len(match) == 1 {
			m.enemyKeys[enemy.Ordinal] = match[0]
			m.enemyOf[match[0]] = enemy.Ordinal
		}
	}
	for i := range m.events {
		if endpoint, ok := m.events[i].enemyEndpoint(); ok {
			if ordinal, mapped := m.enemyOf[endpoint.key]; mapped {
				m.events[i].enemy = ordinal
			}
		}
	}
}

// check rebuilds the full range and compares it with the stored engine result.
func (m *encounterModel) check() {
	full := m.rangeSnapshot(m.resolve(AnalysisQuery{Enemy: -1}))
	stored := m.stored
	problems := []string{}
	if full.Totals != stored.Totals {
		problems = append(problems, "totals")
	}
	if full.UnassignedRole != stored.UnassignedRole {
		problems = append(problems, "unassigned")
	}
	if !sameRoles(full.Roles, stored.Roles) {
		problems = append(problems, "roles")
	}
	if (full.Vehicle == nil) != (stored.Vehicle == nil) || full.Vehicle != nil && !sameContributor(full.Vehicle.DamageDealt, full.Vehicle.OutgoingEvents, full.Vehicle.Skills, full.Vehicle.UnassignedSkill,
		stored.Vehicle.DamageDealt, stored.Vehicle.OutgoingEvents, stored.Vehicle.Skills, stored.Vehicle.UnassignedSkill) {
		problems = append(problems, "vehicle")
	}
	if !sameHealing(full.Healing.HealingTotals, stored.Healing.HealingTotals) {
		problems = append(problems, "healing")
	}
	// The range functions return the stored values for the full range, so the rebuilt parts are compared directly.
	if stored.EncounterDPS.Status != capture.DPSStatusUnavailable && full.Totals.DamageDealt != stored.EncounterDPS.Damage || stored.EncounterDPS.Duration != 0 && stored.EncounterDPS.Duration != m.duration {
		problems = append(problems, "dps")
	}
	if peak := m.bucketPeak(resolvedQuery{full: true, contributor: -1, AnalysisQuery: AnalysisQuery{Enemy: -1}}); peak.Damage != stored.PeakDPS.Damage && stored.PeakDPS.Damage != 0 {
		problems = append(problems, "peak")
	}
	if m.timeline != nil && !m.sameTimeline() {
		problems = append(problems, "timeline")
	}
	m.consistent = len(problems) == 0
	if !m.consistent {
		m.mismatch = fmt.Sprint(problems)
	}
}

// sameTimeline compares the full range's rebuilt intervals with the stored timeline, interval by interval and contributor by
// contributor, so the enemy-filtered timeline is drawn from the same grid the stored one was.
func (m *encounterModel) sameTimeline() bool {
	rebuilt, ok := m.rangeBuckets(resolvedQuery{full: true, contributor: -1, AnalysisQuery: AnalysisQuery{Enemy: -1}})
	if !ok || rebuilt.ScaleSeconds != m.timeline.ScaleSeconds {
		return false
	}
	type cell struct {
		index       int64
		contributor capture.TimelineContributor
	}
	sums := func(timeline capture.StoredTimeline) map[cell]int64 {
		out := map[cell]int64{}
		for _, bucket := range timeline.Buckets {
			if bucket.Contributor >= 0 && bucket.Contributor < len(timeline.Contributors) {
				out[cell{bucket.Index, timeline.Contributors[bucket.Contributor]}] += bucket.Damage
			}
		}
		return out
	}
	a, b := sums(rebuilt), sums(*m.timeline)
	if len(a) != len(b) {
		return false
	}
	for key, damage := range a {
		if b[key] != damage {
			return false
		}
	}
	return true
}

func sameRoles(a, b []capture.RoleDamageSnapshot) bool {
	stored := map[string]capture.RoleDamageSnapshot{}
	for _, role := range b {
		stored[roleKey(role.PlayerID, role.RoleID)] = role
	}
	for _, role := range a {
		other, ok := stored[roleKey(role.PlayerID, role.RoleID)]
		if !ok || role.DirectDamage != other.DirectDamage || role.SummonDamage != other.SummonDamage || role.DamageTaken != other.DamageTaken || role.IncomingEvents != other.IncomingEvents ||
			!sameContributor(role.DamageDealt, role.OutgoingEvents, role.Skills, role.UnassignedSkill, other.DamageDealt, other.OutgoingEvents, other.Skills, other.UnassignedSkill) {
			return false
		}
		delete(stored, roleKey(role.PlayerID, role.RoleID))
	}
	return len(stored) == 0
}

func sameContributor(damage int64, events uint64, skills []capture.SkillDamageSnapshot, unassigned capture.UnassignedSkillTotals,
	otherDamage int64, otherEvents uint64, otherSkills []capture.SkillDamageSnapshot, otherUnassigned capture.UnassignedSkillTotals) bool {
	if damage != otherDamage || events != otherEvents || unassigned.DamageDealt != otherUnassigned.DamageDealt || unassigned.OutgoingEvents != otherUnassigned.OutgoingEvents ||
		unassigned.MissingSkillEvents != otherUnassigned.MissingSkillEvents || unassigned.OverflowEvents != otherUnassigned.OverflowEvents {
		return false
	}
	stored := map[uint64]capture.SkillDamageSnapshot{}
	for _, skill := range otherSkills {
		stored[skill.SkillID] = skill
	}
	for _, skill := range skills {
		if skill.OutgoingEvents == 0 {
			continue
		}
		other, ok := stored[skill.SkillID]
		if !ok || other.DamageDealt != skill.DamageDealt || other.OutgoingEvents != skill.OutgoingEvents {
			return false
		}
		delete(stored, skill.SkillID)
	}
	return len(stored) == 0
}

func sameHealing(a, b capture.HealingTotals) bool {
	if a.Effective != b.Effective || a.Reported != b.Reported || a.Overheal != b.Overheal || a.Events != b.Events ||
		a.UnknownHealerEffective != b.UnknownHealerEffective || a.UnknownHealerEvents != b.UnknownHealerEvents || len(a.ByHealer) != len(b.ByHealer) {
		return false
	}
	for i := range a.ByHealer {
		if a.ByHealer[i] != b.ByHealer[i] {
			return false
		}
	}
	return true
}

// resolvedQuery is a query with its range in nanoseconds from the encounter start.
type resolvedQuery struct {
	AnalysisQuery
	from, to    int64
	full        bool
	contributor int // -1 the team
}

func (m *encounterModel) resolve(query AnalysisQuery) resolvedQuery {
	duration := int64(m.duration)
	from := min(max(0, query.FromMs*int64(time.Millisecond)), duration)
	to := duration
	if query.ToMs > 0 {
		to = min(max(from, query.ToMs*int64(time.Millisecond)), duration)
	}
	resolved := resolvedQuery{AnalysisQuery: query, from: from, to: to, contributor: -1}
	resolved.full = from == 0 && to == duration
	if index, ok := m.byKey[query.Contributor]; ok {
		resolved.contributor = index
	} else {
		resolved.Contributor = ""
	}
	if _, mapped := m.enemyKeys[query.Enemy]; !mapped {
		resolved.Enemy = -1
	}
	if query.Effects != "all" {
		resolved.Effects = "hud"
	}
	return resolved
}

// inRange reports whether a result belongs to the range: the full range holds every result, including one without a time;
// a part holds the results whose clamped time lies in [from, to), and the last part also holds those at the end.
func (q resolvedQuery) inRange(event analysisEvent, duration int64) bool {
	if q.full {
		return true
	}
	if event.rel < 0 {
		return false
	}
	return event.rel >= q.from && (event.rel < q.to || q.to == duration && event.rel == duration)
}

// matchesEnemy reports whether a result belongs to the enemy filter: a confirmed local flow with that enemy instance.
func (q resolvedQuery) matchesEnemy(event analysisEvent) bool {
	return q.Enemy < 0 || event.enemy == q.Enemy
}

// rangeSnapshot rebuilds the encounter for a range and an enemy filter as the engine would have rendered it from those
// results: totals, the role and vehicle split with each skill row, unassigned damage, healing, Encounter DPS over the range
// and the peak 1-second interval within it, with the encounter's integrity applied. The contributor filter does not change
// it; every contributor stays listed so the ranking of the range can be compared.
func (m *encounterModel) rangeSnapshot(q resolvedQuery) capture.EncounterSnapshot {
	snapshot := m.stored
	snapshot.Totals = capture.EncounterTotals{}
	snapshot.UnassignedRole = capture.UnassignedRoleTotals{}
	type split struct {
		role       capture.RoleDamageSnapshot
		skills     map[uint64]*capture.SkillDamageSnapshot
		unassigned capture.UnassignedSkillTotals
	}
	splits := make([]split, len(m.contributors))
	for i, contributor := range m.contributors {
		splits[i] = split{role: capture.RoleDamageSnapshot{PlayerID: contributor.player, RoleID: contributor.role}, skills: map[uint64]*capture.SkillDamageSnapshot{}}
		evidence := contributor.unassigned
		splits[i].unassigned = capture.UnassignedSkillTotals{DamageIDs: evidence.DamageIDs, DamageIDsIncomplete: evidence.DamageIDsIncomplete, EchoVisions: evidence.EchoVisions}
	}
	var healing capture.HealingTotals
	healers := map[[2]uint64]*capture.HealerTotals{}
	inconsistent := false
	duration := int64(m.duration)
	for _, event := range m.events {
		if !q.inRange(event, duration) {
			continue
		}
		if event.healing {
			if q.Enemy >= 0 || event.flow != capture.CombatFlowLocalInternal || event.downgrade != "" {
				continue
			}
			inconsistent = inconsistent || event.inconsistent
			healing.Effective += event.applied
			healing.Reported += event.amount
			healing.Overheal += max(0, event.amount-event.applied)
			healing.Events++
			role := nullUint(event.source.role)
			if !event.source.role.Valid || role == 0 {
				healing.UnknownHealerEffective += event.applied
				healing.UnknownHealerEvents++
				continue
			}
			key := [2]uint64{nullUint(event.source.player), role}
			healer := healers[key]
			if healer == nil {
				healer = &capture.HealerTotals{PlayerID: key[0], RoleID: key[1]}
				healers[key] = healer
			}
			healer.Effective += event.applied
			healer.Reported += event.amount
			healer.Events++
			continue
		}
		if !q.matchesEnemy(event) {
			continue
		}
		totals := &snapshot.Totals
		switch event.flow {
		case capture.CombatFlowLocalOutgoing:
			totals.DamageDealt += event.amount
			totals.OutgoingEvents++
			if event.contributor < 0 {
				snapshot.UnassignedRole.DamageDealt += event.amount
				snapshot.UnassignedRole.OutgoingEvents++
				if event.overflow {
					snapshot.UnassignedRole.OverflowEvents++
				}
				if event.evidence == capture.CombatEvidenceNegativeStatusNotify {
					snapshot.UnassignedRole.NegativeStatusDamage += event.amount
					snapshot.UnassignedRole.NegativeStatusEvents++
				}
				continue
			}
			s := &splits[event.contributor]
			s.role.DamageDealt += event.amount
			s.role.OutgoingEvents++
			if event.summon {
				s.role.SummonDamage += event.amount
			} else if !m.contributors[event.contributor].vehicle {
				s.role.DirectDamage += event.amount
			}
			stored, known := m.contributors[event.contributor].skills[event.skill]
			switch {
			case event.skill == 0:
				s.unassigned.DamageDealt += event.amount
				s.unassigned.OutgoingEvents++
				s.unassigned.MissingSkillEvents++
			case !known:
				// The engine's skill bound left this SkillId out of the rows; its hits stay unassigned.
				s.unassigned.DamageDealt += event.amount
				s.unassigned.OutgoingEvents++
				s.unassigned.OverflowEvents++
			default:
				row := s.skills[event.skill]
				if row == nil {
					row = &capture.SkillDamageSnapshot{SkillID: event.skill, DamageIDs: stored.DamageIDs, DamageIDsIncomplete: stored.DamageIDsIncomplete, EchoVisions: stored.EchoVisions}
					s.skills[event.skill] = row
				}
				row.DamageDealt += event.amount
				row.OutgoingEvents++
			}
		case capture.CombatFlowLocalIncoming:
			totals.DamageTaken += event.amount
			totals.IncomingEvents++
			if event.taken < 0 {
				snapshot.UnassignedRole.DamageTaken += event.amount
				snapshot.UnassignedRole.IncomingEvents++
				if event.overflow {
					snapshot.UnassignedRole.OverflowEvents++
				}
				continue
			}
			splits[event.taken].role.DamageTaken += event.amount
			splits[event.taken].role.IncomingEvents++
		case capture.CombatFlowUnattributedOutgoing:
			if q.Enemy < 0 {
				totals.UnattributedOutgoingDamage += event.amount
				totals.UnattributedOutgoingEvents++
			}
		case capture.CombatFlowUnattributedIncoming:
			if q.Enemy < 0 {
				totals.UnattributedIncomingDamage += event.amount
				totals.UnattributedIncomingEvents++
			}
		}
	}
	snapshot.Roles = []capture.RoleDamageSnapshot{}
	snapshot.Vehicle = nil
	for i, contributor := range m.contributors {
		s := splits[i]
		skills := make([]capture.SkillDamageSnapshot, 0, len(s.skills))
		for _, row := range s.skills {
			skills = append(skills, *row)
		}
		sort.Slice(skills, func(a, b int) bool {
			if skills[a].DamageDealt != skills[b].DamageDealt {
				return skills[a].DamageDealt > skills[b].DamageDealt
			}
			return skills[a].SkillID < skills[b].SkillID
		})
		if contributor.vehicle {
			vehicle := contributor.vehicleOf
			vehicle.DamageDealt, vehicle.OutgoingEvents, vehicle.Skills, vehicle.UnassignedSkill = s.role.DamageDealt, s.role.OutgoingEvents, skills, s.unassigned
			snapshot.Vehicle = &vehicle
			continue
		}
		role := s.role
		role.Skills, role.UnassignedSkill = skills, s.unassigned
		snapshot.Roles = append(snapshot.Roles, role)
	}
	healing.ByHealer = make([]capture.HealerTotals, 0, len(healers))
	for _, healer := range healers {
		healing.ByHealer = append(healing.ByHealer, *healer)
	}
	sort.Slice(healing.ByHealer, func(a, b int) bool {
		if healing.ByHealer[a].PlayerID != healing.ByHealer[b].PlayerID {
			return healing.ByHealer[a].PlayerID < healing.ByHealer[b].PlayerID
		}
		return healing.ByHealer[a].RoleID < healing.ByHealer[b].RoleID
	})
	snapshot.Healing = m.rangeHealing(q, healing, inconsistent)
	snapshot.EncounterDPS = m.rangeDPS(q, snapshot.Totals.DamageDealt)
	snapshot.PeakDPS = m.rangePeak(q)
	return capture.ApplyEncounterIntegrity(snapshot, m.integrity)
}

// rangeHealing keeps the engine's own lower bounds: an inconsistent result in the range, or the engine's capacity for the
// encounter. With an enemy filter there is no healing to show: healing belongs to no enemy.
func (m *encounterModel) rangeHealing(q resolvedQuery, totals capture.HealingTotals, inconsistent bool) capture.HealingResult {
	result := capture.HealingResult{Status: capture.DPSStatusAvailable, HealingTotals: totals}
	switch {
	case q.Enemy >= 0:
		result.Status, result.Reason = capture.DPSStatusUnavailable, "enemy_filter"
	case inconsistent:
		result.Status, result.Reason = capture.DPSStatusLowerBound, "healing_result_lowered_hp"
	case m.healingBase.Reason == "healing_capacity_exceeded":
		result.Status, result.Reason = capture.DPSStatusLowerBound, m.healingBase.Reason
	}
	return result
}

// rangeDPS is encounterDPS over the range: confirmed damage over the range's real duration. The full range is the stored
// Encounter DPS itself; applying the integrity again leaves a result the engine already applied it to unchanged.
func (m *encounterModel) rangeDPS(q resolvedQuery, damage int64) capture.DPSResult {
	if q.full && q.Enemy < 0 {
		return m.stored.EncounterDPS
	}
	result := capture.DPSResult{Damage: damage, Confidence: m.stored.EncounterDPS.Confidence,
		StartObservedAt: m.start.Add(time.Duration(q.from)), EndObservedAt: m.start.Add(time.Duration(q.to)), Duration: time.Duration(q.to - q.from)}
	if result.Duration <= 0 {
		result.Status, result.Reason = capture.DPSStatusInvalidDuration, "non_positive_range_duration"
		return result
	}
	value := float64(damage) / result.Duration.Seconds()
	if math.IsNaN(value) || math.IsInf(value, 0) {
		result.Status, result.Reason = capture.DPSStatusUnavailable, "non_finite_result"
		return result
	}
	result.Status, result.Value = capture.DPSStatusAvailable, value
	return result
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// timelineGrid is the anchor and interval length of the encounter's stored timeline, and the first and last interval its
// span covers (encounterTimeline.span): an end exactly on a boundary closes the previous interval.
func (m *encounterModel) timelineGrid() (anchor time.Time, scale int64, first, last int64) {
	anchor, scale = m.start, 1
	if m.timeline != nil {
		anchor, scale = m.timeline.Anchor, m.timeline.ScaleSeconds
	}
	if scale < 1 {
		return anchor, scale, 0, -1
	}
	unit := int64(time.Second) * scale
	first, last = floorDiv(m.start.Sub(anchor).Nanoseconds(), unit), floorDiv(m.end.Sub(anchor).Nanoseconds(), unit)
	if last > first && anchor.Add(time.Duration(last*unit)).Equal(m.end) {
		last--
	}
	return anchor, scale, first, last
}

// rangeBuckets sums the range's confirmed local outgoing damage into the encounter timeline's stored intervals, per timeline
// contributor, as encounterTimeline.add does: by observed time on the encounter's own grid, unclamped. Rendering and the
// peak clamp an interval outside the span into the first or last one, as the engine does.
func (m *encounterModel) rangeBuckets(q resolvedQuery) (capture.StoredTimeline, bool) {
	anchor, scale, _, _ := m.timelineGrid()
	stored := capture.StoredTimeline{Anchor: anchor, ScaleSeconds: scale}
	if scale < 1 {
		return stored, false
	}
	unit := int64(time.Second) * scale
	positions := map[capture.TimelineContributor]int{}
	sums := map[[2]int64]int64{}
	duration := int64(m.duration)
	for _, event := range m.events {
		if event.healing || event.flow != capture.CombatFlowLocalOutgoing || event.at == 0 || event.amount <= 0 || !q.inRange(event, duration) || !q.matchesEnemy(event) {
			continue
		}
		contributor := capture.TimelineContributor{Kind: capture.TimelineContributorUnassigned}
		if event.contributor >= 0 {
			c := m.contributors[event.contributor]
			if c.vehicle {
				contributor = capture.TimelineContributor{Kind: capture.TimelineContributorVehicle, VehicleKind: c.vehicleOf.Kind}
			} else {
				contributor = capture.TimelineContributor{Kind: capture.TimelineContributorRole, PlayerID: c.player, RoleID: c.role}
			}
		}
		position, ok := positions[contributor]
		if !ok {
			position = len(stored.Contributors)
			positions[contributor] = position
			stored.Contributors = append(stored.Contributors, contributor)
		}
		sums[[2]int64{floorDiv(event.at-anchor.UnixNano(), unit), int64(position)}] += event.amount
	}
	for key, damage := range sums {
		stored.Buckets = append(stored.Buckets, capture.StoredTimelineBucket{Index: key[0], Contributor: int(key[1]), Damage: damage})
	}
	sort.Slice(stored.Buckets, func(a, b int) bool {
		if stored.Buckets[a].Index != stored.Buckets[b].Index {
			return stored.Buckets[a].Index < stored.Buckets[b].Index
		}
		return stored.Buckets[a].Contributor < stored.Buckets[b].Contributor
	})
	return stored, true
}

// rangePeak is encounterPeakDPS within the range: the stored interval with the most of the range's damage, over its nominal
// length, so a short first or last interval is never scaled up. The full range is the stored peak itself.
func (m *encounterModel) rangePeak(q resolvedQuery) capture.DPSResult {
	if q.full && q.Enemy < 0 {
		return m.stored.PeakDPS
	}
	return m.bucketPeak(q)
}

func (m *encounterModel) bucketPeak(q resolvedQuery) capture.DPSResult {
	result := capture.DPSResult{Confidence: m.stored.EncounterDPS.Confidence}
	buckets, ok := m.rangeBuckets(q)
	if !ok {
		result.Status, result.Reason = capture.DPSStatusUnavailable, "timeline_unavailable"
		return result
	}
	_, _, first, last := m.timelineGrid()
	sums := map[int64]int64{}
	for _, bucket := range buckets.Buckets {
		sums[min(max(bucket.Index, first), last)] += bucket.Damage
	}
	best, damage := int64(0), int64(0)
	for index, sum := range sums {
		if sum > damage || (sum == damage && sum > 0 && index < best) {
			best, damage = index, sum
		}
	}
	if damage <= 0 {
		result.Status, result.Reason = capture.DPSStatusUnavailable, "no_confirmed_damage"
		return result
	}
	length := time.Duration(buckets.ScaleSeconds) * time.Second
	begin := buckets.Anchor.Add(time.Duration(best) * length)
	if begin.Before(m.start) {
		begin = m.start
	}
	end := begin.Add(length)
	if end.After(m.end) {
		end = m.end
	}
	result.Damage, result.Duration, result.StartObservedAt, result.EndObservedAt = damage, length, begin, end
	result.Status, result.Value = capture.DPSStatusAvailable, float64(damage)/length.Seconds()
	return result
}

// SelectionTotals are the selected contributor's numbers in the range, or the team's without a contributor.
type SelectionTotals struct {
	Contributor string          `json:"contributor,omitempty"`
	DPS         capture.ViewDPS `json:"dps"`
	Damage      int64           `json:"damage"`
	Hits        uint64          `json:"hits"`
	Taken       int64           `json:"taken"`
	TakenHits   uint64          `json:"takenHits"`
	// Healing is the selected role's healing as a healer (both values), or the team's.
	Healing capture.ViewHealing `json:"healing"`
}

// SkillStats extends one skill row of the range with what only the stored hits tell: the largest hit, the average hit and
// when its damage landed. SkillID 0 is the contributor's unassigned row. Hits are damage hits, never casts.
type SkillStats struct {
	Contributor string  `json:"contributor"`
	SkillID     uint64  `json:"skillId"`
	Damage      int64   `json:"damage"`
	Hits        uint64  `json:"hits"`
	MaxHit      int64   `json:"maxHit"`
	AverageHit  float64 `json:"averageHit"`
	// Bins splits the row's damage over the whole encounter in analysisSkillBins equal parts, with the enemy filter applied
	// but not the range, so the range can be read against the whole fight.
	Bins []int64 `json:"bins"`
}

// EnemyRange is one enemy instance's confirmed local flows in the range.
type EnemyRange struct {
	Ordinal     int    `json:"ordinal"`
	Mapped      bool   `json:"mapped"`
	DamageTaken int64  `json:"damageTaken"`
	HitsTaken   uint64 `json:"hitsTaken"`
	DamageDealt int64  `json:"damageDealt"`
	HitsDealt   uint64 `json:"hitsDealt"`
}

// GapRef is a transport loss episode of the encounter's capture, in milliseconds from the encounter start (clamped to it).
type GapRef struct {
	FromMs    float64 `json:"fromMs"`
	ToMs      float64 `json:"toMs"`
	Direction string  `json:"direction"`
	Failed    bool    `json:"failed,omitempty"`
	// Open is set when the loss lasted past the encounter's end.
	Open bool `json:"open,omitempty"`
}

// EncounterAnalysis answers one analysis query.
type EncounterAnalysis struct {
	State string        `json:"state"`
	Range AnalysisRange `json:"range"`
	// Query is the query as applied: an unknown contributor or an unmapped enemy is dropped, not guessed.
	Query AnalysisQuery `json:"query"`
	// Consistent is set when the stored results reproduce the stored encounter result; Mismatch names what does not.
	Consistent bool   `json:"consistent"`
	Mismatch   string `json:"mismatch,omitempty"`
	// Snapshot is the range rebuilt as the engine renders an encounter; the host projects it like a live encounter.
	Snapshot *capture.EncounterSnapshot `json:"-"`
	// Timeline is the encounter's DPS timeline with the enemy filter applied (the whole encounter, not only the range).
	Timeline      *capture.EncounterTimelineSnapshot `json:"timeline,omitempty"`
	TimelineValid bool                               `json:"timelineValid"`
	Selection     SelectionTotals                    `json:"selection"`
	Skills        []SkillStats                       `json:"skills"`
	SkillBins     int                                `json:"skillBins"`
	Enemies       []EnemyRange                       `json:"enemies"`
	Gaps          []GapRef                           `json:"gaps"`
	// UntimedEvents counts stored results without an observed time: only the full range holds them.
	UntimedEvents int              `json:"untimedEvents"`
	Effects       EffectCoverageOf `json:"effects"`
	// Hits are the selected skill row's hits over the whole encounter (enemy filter applied), for the timeline; HitsTruncated
	// is set when there were more than one answer marks.
	Hits          []SkillHit `json:"hits"`
	HitsTruncated bool       `json:"hitsTruncated,omitempty"`
}

// Analysis answers one query of the analysis workspace for a finished stored encounter. masters classifies effect
// definitions (HUD polarity and icon) with the effect tracker's own rule.
func (s *Store) Analysis(ctx context.Context, id int64, query AnalysisQuery, masters *capture.MasterData) (EncounterAnalysis, error) {
	model, err := s.model(ctx, id, masters)
	var state errAnalysisState
	if errors.As(err, &state) {
		return EncounterAnalysis{State: state.state, Skills: []SkillStats{}, Enemies: []EnemyRange{}, Gaps: []GapRef{}, Hits: []SkillHit{}, Effects: EffectCoverageOf{Rows: []EffectCoverage{}}}, nil
	}
	if err != nil {
		return EncounterAnalysis{}, err
	}
	q := model.resolve(query)
	snapshot := model.rangeSnapshot(q)
	out := EncounterAnalysis{
		State: AnalysisReady, Query: q.AnalysisQuery, Consistent: model.consistent, Mismatch: model.mismatch, Snapshot: &snapshot,
		Range:     AnalysisRange{FromMs: nsToMs(q.from), ToMs: nsToMs(q.to), DurationMs: nsToMs(int64(model.duration)), Full: q.full},
		SkillBins: analysisSkillBins, UntimedEvents: model.untimed, Gaps: model.gapRefs(),
	}
	if q.Enemy < 0 && model.timeline != nil {
		// Without an enemy filter the timeline is the stored one: exactly what the Dashboard drew.
		timeline, valid := model.timeline.Render(model.timelineID(), model.start, model.end, maxAnalysisTimelinePoints)
		out.Timeline, out.TimelineValid = &timeline, valid
	} else if buckets, ok := model.rangeBuckets(resolvedQuery{AnalysisQuery: q.AnalysisQuery, full: true, contributor: -1}); ok {
		timeline, valid := buckets.Render(model.timelineID(), model.start, model.end, maxAnalysisTimelinePoints)
		out.Timeline, out.TimelineValid = &timeline, valid
	}
	out.Selection = model.selection(q, snapshot)
	out.Skills = model.skillStats(q)
	out.Enemies = model.enemyRanges(q)
	out.Effects = model.effectCoverage(q)
	out.Hits, out.HitsTruncated = model.skillHits(q)
	if out.Hits == nil {
		out.Hits = []SkillHit{}
	}
	return out, nil
}

func (m *encounterModel) timelineID() string { return "history-" + strconv.FormatInt(m.id, 10) }

func nsToMs(ns int64) float64 { return float64(ns) / float64(time.Millisecond) }

// wholeMs is a time for drawing and reading at millisecond precision; the analysis itself keeps nanoseconds.
func wholeMs(ns int64) float64 { return math.Round(float64(ns) / float64(time.Millisecond)) }

// selection takes the selected contributor's numbers from the range snapshot and the healing it did as a healer.
func (m *encounterModel) selection(q resolvedQuery, snapshot capture.EncounterSnapshot) SelectionTotals {
	healing := snapshot.Healing
	out := SelectionTotals{Contributor: q.Contributor}
	if q.contributor < 0 {
		out.Damage, out.Hits, out.Taken, out.TakenHits = snapshot.Totals.DamageDealt, snapshot.Totals.OutgoingEvents, snapshot.Totals.DamageTaken, snapshot.Totals.IncomingEvents
		out.DPS = viewDPS(snapshot.EncounterDPS)
		out.Healing = viewHealing(healing)
		return out
	}
	contributor := m.contributors[q.contributor]
	if contributor.vehicle {
		if snapshot.Vehicle != nil {
			out.Damage, out.Hits = snapshot.Vehicle.DamageDealt, snapshot.Vehicle.OutgoingEvents
		}
		healing = capture.HealingResult{Status: capture.DPSStatusUnavailable, Reason: "vehicle_heals_nothing"}
	} else {
		for _, role := range snapshot.Roles {
			if role.PlayerID == contributor.player && role.RoleID == contributor.role {
				out.Damage, out.Hits, out.Taken, out.TakenHits = role.DamageDealt, role.OutgoingEvents, role.DamageTaken, role.IncomingEvents
			}
		}
		var own capture.HealingTotals
		for _, healer := range snapshot.Healing.ByHealer {
			if healer.PlayerID == contributor.player && healer.RoleID == contributor.role {
				own.Effective, own.Reported, own.Events = healer.Effective, healer.Reported, healer.Events
				own.Overheal = max(0, healer.Reported-healer.Effective)
			}
		}
		healing.HealingTotals = own
	}
	// The contributor's DPS uses the range's duration, as rankViewContributors does for its row.
	dps := snapshot.EncounterDPS
	dps.Damage = out.Damage
	if dps.Status == capture.DPSStatusAvailable || dps.Status == capture.DPSStatusLowerBound {
		dps.Value = float64(out.Damage) / dps.Duration.Seconds()
	}
	out.DPS = viewDPS(dps)
	out.Healing = viewHealing(healing)
	return out
}

func viewDPS(result capture.DPSResult) capture.ViewDPS {
	return capture.ViewDPS{Status: result.Status, Reason: result.Reason, Damage: result.Damage, DurationSeconds: result.Duration.Seconds(), Value: result.Value, Confidence: result.Confidence}
}

func viewHealing(result capture.HealingResult) capture.ViewHealing {
	return capture.ViewHealing{Status: result.Status, Reason: result.Reason, Effective: result.Effective, Reported: result.Reported, Overheal: result.Overheal,
		Events: result.Events, UnknownHealerEffective: result.UnknownHealerEffective, UnknownHealerEvents: result.UnknownHealerEvents}
}

// skillStats is every skill row of the range with its largest and average hit, and each row's damage over the whole
// encounter in equal parts.
func (m *encounterModel) skillStats(q resolvedQuery) []SkillStats {
	type key struct {
		contributor int
		skill       uint64
	}
	rows := map[key]*SkillStats{}
	var order []key
	duration := int64(m.duration)
	row := func(k key) *SkillStats {
		r := rows[k]
		if r == nil {
			r = &SkillStats{Contributor: m.contributors[k.contributor].key, SkillID: k.skill, Bins: make([]int64, analysisSkillBins)}
			rows[k] = r
			order = append(order, k)
		}
		return r
	}
	for _, event := range m.events {
		if event.healing || event.flow != capture.CombatFlowLocalOutgoing || event.contributor < 0 || !q.matchesEnemy(event) {
			continue
		}
		skill := event.skill
		if _, known := m.contributors[event.contributor].skills[skill]; !known {
			skill = 0
		}
		r := row(key{event.contributor, skill})
		if event.rel >= 0 && duration > 0 {
			bin := int(event.rel * analysisSkillBins / duration)
			r.Bins[min(bin, analysisSkillBins-1)] += event.amount
		}
		if !q.inRange(event, duration) {
			continue
		}
		r.Damage += event.amount
		r.Hits++
		r.MaxHit = max(r.MaxHit, event.amount)
	}
	out := make([]SkillStats, 0, len(order))
	for _, k := range order {
		r := rows[k]
		if r.Hits > 0 {
			r.AverageHit = float64(r.Damage) / float64(r.Hits)
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Contributor != out[b].Contributor {
			return out[a].Contributor < out[b].Contributor
		}
		return out[a].SkillID < out[b].SkillID
	})
	return out
}

// enemyRanges is each stored enemy instance's flows in the range. An instance that could not be tied to its results keeps
// its stored totals only for the full range.
func (m *encounterModel) enemyRanges(q resolvedQuery) []EnemyRange {
	out := make([]EnemyRange, 0, len(m.enemies))
	index := map[int]int{}
	for _, enemy := range m.enemies {
		_, mapped := m.enemyKeys[enemy.Ordinal]
		index[enemy.Ordinal] = len(out)
		row := EnemyRange{Ordinal: enemy.Ordinal, Mapped: mapped}
		if !mapped && q.full {
			row.DamageTaken, row.HitsTaken, row.DamageDealt, row.HitsDealt = enemy.DamageTaken, enemy.HitsTaken, enemy.DamageDealt, enemy.HitsDealt
		}
		out = append(out, row)
	}
	duration := int64(m.duration)
	for _, event := range m.events {
		if event.enemy < 0 || !q.inRange(event, duration) {
			continue
		}
		position, ok := index[event.enemy]
		if !ok {
			continue
		}
		if event.flow == capture.CombatFlowLocalIncoming {
			out[position].DamageDealt += event.amount
			out[position].HitsDealt++
		} else {
			out[position].DamageTaken += event.amount
			out[position].HitsTaken++
		}
	}
	return out
}

// loadGaps reads the transport losses of the encounter's capture that overlap it.
func (s *Store) loadGaps(ctx context.Context, model *encounterModel) error {
	rows, err := s.db.QueryContext(ctx, "SELECT direction, from_ns, to_ns, failed FROM capture_gap WHERE capture_session_id = ? AND from_ns <= ? AND (to_ns IS NULL OR to_ns >= ?) ORDER BY from_ns",
		model.session, model.end.UnixNano(), model.start.UnixNano())
	if err != nil {
		return fmt.Errorf("read history analysis gaps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var gap analysisGap
		var to sql.NullInt64
		var failed int
		if err := rows.Scan(&gap.direction, &gap.from, &to, &failed); err != nil {
			return fmt.Errorf("read history analysis gaps: %w", err)
		}
		gap.failed, gap.to, gap.open = failed == 1, to.Int64, !to.Valid || to.Int64 > model.end.UnixNano()
		if !to.Valid || gap.to > model.end.UnixNano() {
			gap.to = model.end.UnixNano()
		}
		model.gaps = append(model.gaps, gap)
	}
	return rows.Err()
}

func (m *encounterModel) gapRefs() []GapRef {
	out := make([]GapRef, 0, len(m.gaps))
	start := m.start.UnixNano()
	for _, gap := range m.gaps {
		from := max(gap.from, start)
		out = append(out, GapRef{FromMs: nsToMs(from - start), ToMs: nsToMs(gap.to - start), Direction: gap.direction, Failed: gap.failed, Open: gap.open})
	}
	return out
}
