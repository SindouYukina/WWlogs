package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"

	"wwlogs/internal/capture"
)

// Effect lifecycles as intervals.
//
// A stored effect instance is a sequence of lifecycle facts (effect_event, in the tracker's order). Between two facts of the
// same live instance the effect was active: the later fact (a refresh, a stack or duration message, an observed REMOVE, or the
// holder's removal) shows it lasted until then. That is a confirmed interval. After the last fact nothing confirms the
// effect any more; while the time the server stated is still running it is active by that statement only (stated, partial
// evidence), and once that time has passed without a fact it is unknown, until the next fact, the end of tracking or the
// end of the encounter. An estimated expiry is never an end: it only moves an interval from stated to unknown.
//
// An instance whose first stored fact is not an APPLY (a snapshot of an effect already active, or a change of an instance
// tracked before the span) has an unconfirmed start: it was active at that fact, and when it began is unknown. A handle
// reused by a later APPLY ends the instance at or before that APPLY (superseded); an end of tracking (untracked) ends what
// is known. Instant effects are points without an interval.
//
// Coverage of a range is reported as confirmed, partial and unknown time, with capture gaps apart. It is exact only when
// the range holds no partial or unknown time, no unconfirmed start, no capture gap, and the capture tracked every effect it
// saw; otherwise only a lower bound (confirmed time) and, where one exists, an upper bound are given.

// Interval evidence.
const (
	EffectConfirmed = "confirmed"
	EffectStated    = "stated"
	EffectUnknown   = "unknown"
)

// Interval markers.
const (
	markerApplied          = "applied"
	markerStartUnconfirmed = "start_unconfirmed"
	markerRemoved          = "removed"
	markerHolderRemoved    = "holder_removed"
	markerSuperseded       = "superseded"
	markerUntracked        = "untracked"
	markerInstant          = "instant"
	markerContinues        = "continues"
)

const (
	// maxEffectRows bounds the rows one answer lists; more are counted.
	maxEffectRows = 600
	// maxRowSources bounds the distinct sources a row names.
	maxRowSources = 4
	// effectLookahead is how far past the encounter a later fact still confirms that an instance lasted to the end.
	effectLookahead int64 = 10 * 60 * 1e9
)

var terminalChanges = map[string]bool{"remove": true, "holder_removed": true, "superseded": true, "untracked": true, "instant": true}

type effectFact struct {
	sequence    int64
	afterCombat int64
	at          int64
	change      string
	observation string
	evidence    string
	reason      string
	stack       sql.NullInt64
	remaining   sql.NullFloat64
	basis       sql.NullInt64
}

// statedExpiry is the expiry the server's last statement implies, as an estimate: 0 when it stated no finite time.
func (f effectFact) statedExpiry() int64 {
	if !f.remaining.Valid || !f.basis.Valid || f.remaining.Float64 <= 0 {
		return 0
	}
	return f.basis.Int64 + int64(f.remaining.Float64*1e9)
}

type effectSegment struct {
	from, to int64
	evidence string
	stack    sql.NullInt64
}

type effectMarker struct {
	at   int64
	kind string
}

// ActorRef names an effect or combat endpoint in product terms: a local role (Contributor is its key when it is a contributor
// of the encounter), a summon of a role, the local player's own entity, the local team without an established role or
// vehicle, the vehicle, an enemy instance of the encounter, another non-player entity with its Master type, or an endpoint
// without established identity. It never carries a runtime entity or handle ID.
type ActorRef struct {
	Kind        string `json:"kind"`
	Contributor string `json:"contributor,omitempty"`
	RoleID      uint64 `json:"roleId,omitempty"`
	Enemy       *int   `json:"enemy,omitempty"`
	MonsterID   uint64 `json:"monsterId,omitempty"`
	TemplateID  uint64 `json:"templateId,omitempty"`
	Relation    string `json:"relation,omitempty"`
}

// Actor kinds.
const (
	ActorRole       = "role"
	ActorSummon     = "summon"
	ActorVehicle    = "vehicle"
	ActorTeam       = "team"
	ActorUnassigned = "team_unassigned"
	ActorEnemy      = "enemy"
	ActorOther      = "other"
	ActorUnknown    = "unknown"
)

func (a ActorRef) key() string {
	switch a.Kind {
	case ActorRole, ActorSummon:
		return a.Kind + ":" + strconv.FormatUint(a.RoleID, 10) + ":" + a.Contributor
	case ActorVehicle:
		return "vehicle:" + a.Contributor
	case ActorEnemy:
		return "enemy:" + strconv.Itoa(*a.Enemy)
	}
	return a.Kind + ":" + strconv.FormatUint(a.MonsterID, 10) + ":" + strconv.FormatUint(a.TemplateID, 10) + ":" + a.Relation
}

type effectInstanceModel struct {
	instance         int64
	effectID         sql.NullInt64
	definitionLinked bool
	holder           ActorRef
	source           ActorRef
	hasSource        bool
	facts            []effectFact
	segments         []effectSegment
	markers          []effectMarker
	startUnconfirmed bool
	firstFact        int64
	row              *effectRowModel
}

type effectRowModel struct {
	key              string
	effectID         sql.NullInt64
	definitionLinked bool
	definition       capture.EffectDefinition
	holder           ActorRef
	sources          []ActorRef
	hud              bool
	instances        []*effectInstanceModel
	contributors     map[string]bool
	enemies          map[int]bool
}

type effectModel struct {
	rows      []*effectRowModel
	byKey     map[string]*effectRowModel
	instances map[int64]*effectInstanceModel
	// window lists the facts observed inside the encounter, for the event browser, in the tracker's order.
	window []windowFact
	// truncated is set when the encounter holds more effect changes than one analysis reads.
	truncated bool
	// stale counts instances left out because their stated time ended before the encounter without a later change.
	stale int
}

type windowFact struct {
	fact     effectFact
	instance *effectInstanceModel
}

// loadEffects reads the effect instances of the encounter's capture span and protocol session that were alive at any time
// within the encounter: those with a change inside it, those whose last change before it left them alive, and the first
// confirming change shortly after it.
func (s *Store) loadEffects(ctx context.Context, model *encounterModel, masters *capture.MasterData) (*effectModel, error) {
	start, end := model.start.UnixNano(), model.end.UnixNano()
	effects := &effectModel{byKey: map[string]*effectRowModel{}, instances: map[int64]*effectInstanceModel{}}
	instanceFacts := map[int64][]effectFact{}
	scan := func(rows *sql.Rows) (int64, effectFact, error) {
		var instance int64
		var fact effectFact
		var at sql.NullInt64
		err := rows.Scan(&instance, &fact.sequence, &fact.afterCombat, &at, &fact.change, &fact.observation, &fact.evidence, &fact.reason, &fact.stack, &fact.remaining, &fact.basis)
		fact.at = at.Int64
		return instance, fact, err
	}
	const columns = "ev.instance, ev.sequence, ev.after_combat_sequence, ev.observed_at_ns, ev.change_kind, ev.observation, ev.evidence, ev.reason, ev.stack, ev.remaining_s, ev.basis_ns"
	// The last change before the encounter of each instance it may have found alive: one whose first change came before it and
	// that did not end before it (effect_instance_by_life). An instance that change left alive was active when the encounter
	// began. Only those instances are read, never the span's changes before the encounter: each one's last change is found
	// through its own key range (the unary + keeps the planner off the span-wide time index there).
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+`, ev.sequence FROM effect_instance i JOIN effect_event ev ON ev.capture_session_id = i.capture_session_id AND ev.instance = i.instance
		AND ev.sequence = (SELECT e.sequence FROM effect_event e WHERE e.capture_session_id = i.capture_session_id AND e.instance = i.instance AND +e.observed_at_ns < ? ORDER BY e.sequence DESC LIMIT 1)
		WHERE i.capture_session_id = ? AND i.first_change_ns < ? AND (i.ended_at_ns IS NULL OR i.ended_at_ns >= ?)`, start, model.session, start, start)
	if err != nil {
		return nil, fmt.Errorf("read history analysis effects: %w", err)
	}
	for rows.Next() {
		var instance int64
		var fact effectFact
		var at sql.NullInt64
		var last int64
		if err := rows.Scan(&instance, &fact.sequence, &fact.afterCombat, &at, &fact.change, &fact.observation, &fact.evidence, &fact.reason, &fact.stack, &fact.remaining, &fact.basis, &last); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read history analysis effects: %w", err)
		}
		fact.at = at.Int64
		if !terminalChanges[fact.change] {
			instanceFacts[instance] = append(instanceFacts[instance], fact)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read history analysis effects: %w", err)
	}
	rows.Close()
	// Every change inside the encounter.
	rows, err = s.db.QueryContext(ctx, "SELECT "+columns+" FROM effect_event ev WHERE ev.capture_session_id = ? AND ev.observed_at_ns BETWEEN ? AND ? ORDER BY ev.sequence LIMIT ?",
		model.session, start, end, maxAnalysisEffectEvents+1)
	if err != nil {
		return nil, fmt.Errorf("read history analysis effects: %w", err)
	}
	count := 0
	for rows.Next() {
		instance, fact, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("read history analysis effects: %w", err)
		}
		if count++; count > maxAnalysisEffectEvents {
			effects.truncated = true
			break
		}
		instanceFacts[instance] = append(instanceFacts[instance], fact)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read history analysis effects: %w", err)
	}
	rows.Close()
	if effects.truncated {
		return effects, nil
	}
	// The first change after the encounter of each instance still alive at its end, within the lookahead: a fact there
	// confirms that the instance lasted to the end.
	rows, err = s.db.QueryContext(ctx, "SELECT "+columns+", min(ev.sequence) FROM effect_event ev WHERE ev.capture_session_id = ? AND ev.observed_at_ns > ? AND ev.observed_at_ns <= ? AND ev.change_kind != 'untracked' GROUP BY ev.instance",
		model.session, end, end+effectLookahead)
	if err != nil {
		return nil, fmt.Errorf("read history analysis effects: %w", err)
	}
	for rows.Next() {
		var instance int64
		var fact effectFact
		var at sql.NullInt64
		var first int64
		if err := rows.Scan(&instance, &fact.sequence, &fact.afterCombat, &at, &fact.change, &fact.observation, &fact.evidence, &fact.reason, &fact.stack, &fact.remaining, &fact.basis, &first); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read history analysis effects: %w", err)
		}
		fact.at = at.Int64
		if facts, alive := instanceFacts[instance]; alive && !terminalChanges[facts[len(facts)-1].change] {
			instanceFacts[instance] = append(facts, fact)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read history analysis effects: %w", err)
	}
	rows.Close()
	if err := s.loadEffectInstances(ctx, model, effects, instanceFacts, masters); err != nil {
		return nil, err
	}
	for id, instance := range effects.instances {
		if instance.stale(start, end) {
			// Its own stated time ended before the encounter and nothing about it was observed during the encounter: the
			// tracker no longer shows it, and the encounter holds no evidence about it either way. It is counted, not drawn.
			effects.stale++
			instance.row.drop(instance)
			delete(effects.instances, id)
			continue
		}
		instance.layout(start, end)
	}
	effects.dropEmptyRows()
	for _, instance := range effects.sortedInstances() {
		for _, fact := range instance.facts {
			if fact.at >= start && fact.at <= end {
				effects.window = append(effects.window, windowFact{fact: fact, instance: instance})
			}
		}
	}
	sort.Slice(effects.window, func(a, b int) bool { return effects.window[a].fact.sequence < effects.window[b].fact.sequence })
	return effects, nil
}

// stale reports an instance that was alive before the encounter by its last change only, whose server-stated time ran out
// before the encounter began, and which has no change during the encounter or a confirming one after it.
func (i *effectInstanceModel) stale(start, end int64) bool {
	if len(i.facts) == 0 || i.facts[0].at >= start {
		return false
	}
	for _, fact := range i.facts[1:] {
		if fact.at >= start {
			return false
		}
	}
	expiry := i.facts[0].statedExpiry()
	return expiry != 0 && expiry < start
}

// drop removes an instance from its row.
func (r *effectRowModel) drop(instance *effectInstanceModel) {
	kept := r.instances[:0]
	for _, other := range r.instances {
		if other != instance {
			kept = append(kept, other)
		}
	}
	r.instances = kept
}

// dropEmptyRows forgets the rows every instance of which was dropped.
func (e *effectModel) dropEmptyRows() {
	kept := e.rows[:0]
	for _, row := range e.rows {
		if len(row.instances) == 0 {
			delete(e.byKey, row.key)
			continue
		}
		kept = append(kept, row)
	}
	e.rows = kept
}

func (e *effectModel) sortedInstances() []*effectInstanceModel {
	out := make([]*effectInstanceModel, 0, len(e.instances))
	for _, instance := range e.instances {
		out = append(out, instance)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].instance < out[b].instance })
	return out
}

// loadEffectInstances reads the instances of the protocol session the facts belong to, resolves their holder and source in
// the encounter's terms, and groups them into rows of one holder and one effect.
func (s *Store) loadEffectInstances(ctx context.Context, model *encounterModel, effects *effectModel, facts map[int64][]effectFact, masters *capture.MasterData) error {
	if len(facts) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(facts))
	for id := range facts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	// Bounded batches keep each statement's parameter list short.
	const batch = 400
	for first := 0; first < len(ids); first += batch {
		part := ids[first:min(first+batch, len(ids))]
		args := []any{model.session, model.protocol}
		marks := make([]byte, 0, len(part)*2)
		for i, id := range part {
			if i > 0 {
				marks = append(marks, ',')
			}
			marks = append(marks, '?')
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT instance, holder_entity_id, holder_generation, effect_id, definition_linked, source_entity_id, holder_relation, source_relation,
			holder_player_id, holder_role_id, holder_role_basis, holder_monster_id, holder_template_id, source_player_id, source_role_id, source_role_basis, source_monster_id, source_template_id,
			origin, first_observed_ns, start_recorded FROM effect_instance WHERE capture_session_id = ? AND protocol_session = ? AND instance IN (`+string(marks)+`)`, args...)
		if err != nil {
			return fmt.Errorf("read history analysis effect instances: %w", err)
		}
		for rows.Next() {
			instance := &effectInstanceModel{}
			var holderEntity, holderGeneration int64
			var sourceEntity sql.NullInt64
			var linked, startRecorded int
			var holderRelation, sourceRelation, origin string
			var holderPlayer, holderRole, sourcePlayer, sourceRole sql.NullInt64
			var holderBasis, sourceBasis sql.NullString
			var holderMonster, holderTemplate, sourceMonster, sourceTemplate int64
			var firstObserved sql.NullInt64
			if err := rows.Scan(&instance.instance, &holderEntity, &holderGeneration, &instance.effectID, &linked, &sourceEntity, &holderRelation, &sourceRelation,
				&holderPlayer, &holderRole, &holderBasis, &holderMonster, &holderTemplate, &sourcePlayer, &sourceRole, &sourceBasis, &sourceMonster, &sourceTemplate,
				&origin, &firstObserved, &startRecorded); err != nil {
				rows.Close()
				return fmt.Errorf("read history analysis effect instances: %w", err)
			}
			instance.definitionLinked = linked == 1
			instance.facts = facts[instance.instance]
			sort.Slice(instance.facts, func(a, b int) bool { return instance.facts[a].sequence < instance.facts[b].sequence })
			holderKey := entityKey{session: model.protocol, entity: uint64(holderEntity), generation: uint64(holderGeneration)}
			instance.holder = model.effectActor(holderRelation, holderPlayer, holderRole, holderBasis, uint64(holderMonster), uint64(holderTemplate), &holderKey, 0)
			if sourceEntity.Valid {
				instance.hasSource = true
				instance.source = model.effectActor(sourceRelation, sourcePlayer, sourceRole, sourceBasis, uint64(sourceMonster), uint64(sourceTemplate), nil, uint64(sourceEntity.Int64))
			} else {
				instance.source = ActorRef{Kind: ActorUnknown, Relation: sourceRelation}
			}
			// Only an observed APPLY dates an instance's start: a snapshot shows an effect already active, and an instance whose
			// first change was not stored (start_recorded 0) began before what the span holds.
			instance.startUnconfirmed = startRecorded == 0 || origin != string(capture.EffectOriginApply)
			instance.firstFact = firstObserved.Int64
			effects.instances[instance.instance] = instance
			effects.addToRow(model, instance, holderKey, masters)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read history analysis effect instances: %w", err)
		}
		rows.Close()
	}
	sort.SliceStable(effects.rows, func(a, b int) bool { return effects.rows[a].less(effects.rows[b]) })
	return nil
}

// effectActor names an effect endpoint. A local role holder or source is that contributor, a summon's is its owner's summon,
// a non-player holder is the enemy instance of the encounter with the same entity instance, and a non-player source is that
// enemy when exactly one enemy instance of the encounter has its entity (sources keep no reuse generation).
func (m *encounterModel) effectActor(relation string, player, role sql.NullInt64, basis sql.NullString, monster, template uint64, holder *entityKey, sourceEntity uint64) ActorRef {
	switch relation {
	case string(capture.EffectRelationLocalRole), string(capture.EffectRelationLocalOwned):
		if role.Valid && nullUint(role) != 0 {
			actor := ActorRef{Kind: ActorRole, RoleID: nullUint(role), Relation: relation}
			if relation == string(capture.EffectRelationLocalOwned) || basis.String == "summoner_chain" {
				actor.Kind = ActorSummon
			}
			if key := roleKey(nullUint(player), nullUint(role)); m.hasContributor(key) {
				actor.Contributor = key
			}
			return actor
		}
		return ActorRef{Kind: ActorUnassigned, Relation: relation}
	case string(capture.EffectRelationLocalPlayer):
		return ActorRef{Kind: ActorTeam, Relation: relation}
	case string(capture.EffectRelationNonPlayer):
		if holder != nil {
			if ordinal, ok := m.enemyOf[*holder]; ok {
				return m.enemyActor(ordinal, relation)
			}
		} else {
			found, ordinal := 0, 0
			for key, candidate := range m.enemyOf {
				if key.session == m.protocol && key.entity == sourceEntity {
					found, ordinal = found+1, candidate
				}
			}
			if found == 1 {
				return m.enemyActor(ordinal, relation)
			}
		}
		return ActorRef{Kind: ActorOther, MonsterID: monster, TemplateID: template, Relation: relation}
	}
	return ActorRef{Kind: ActorUnknown, MonsterID: monster, TemplateID: template, Relation: relation}
}

// enemyActor names an enemy instance of the encounter with the type its stored row holds (none after an identity conflict).
func (m *encounterModel) enemyActor(ordinal int, relation string) ActorRef {
	actor := ActorRef{Kind: ActorEnemy, Enemy: &ordinal, Relation: relation}
	for _, enemy := range m.enemies {
		if enemy.Ordinal == ordinal && !enemy.IdentityConflict {
			actor.MonsterID, actor.TemplateID = enemy.MonsterID, enemy.TemplateID
		}
	}
	return actor
}

// addToRow puts an instance into the row of its holder and effect. Holders without an identity in the encounter keep their
// entity instance apart under an opaque key, so two unknown holders are never merged.
func (e *effectModel) addToRow(model *encounterModel, instance *effectInstanceModel, holder entityKey, masters *capture.MasterData) {
	holderKey := instance.holder.key()
	if instance.holder.Kind == ActorOther || instance.holder.Kind == ActorUnknown || instance.holder.Kind == ActorUnassigned {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(strconv.Itoa(holder.session) + ":" + strconv.FormatUint(holder.entity, 10) + ":" + strconv.FormatUint(holder.generation, 10)))
		holderKey = "x" + strconv.FormatUint(hash.Sum64(), 36)
	}
	effectKey := "none-" + strconv.FormatInt(instance.instance, 10)
	if instance.effectID.Valid {
		effectKey = strconv.FormatInt(instance.effectID.Int64, 10)
	}
	key := holderKey + "|" + effectKey
	row := e.byKey[key]
	if row == nil {
		row = &effectRowModel{key: key, effectID: instance.effectID, holder: instance.holder, contributors: map[string]bool{}, enemies: map[int]bool{}}
		if instance.effectID.Valid {
			row.definition = masters.EffectDefinition(uint64(instance.effectID.Int64))
		}
		e.byKey[key] = row
		e.rows = append(e.rows, row)
	}
	row.definitionLinked = row.definitionLinked || instance.definitionLinked
	row.instances = append(row.instances, instance)
	instance.row = row
	for _, actor := range []ActorRef{instance.holder, instance.source} {
		if actor.Contributor != "" {
			row.contributors[actor.Contributor] = true
		}
		if actor.Enemy != nil {
			row.enemies[*actor.Enemy] = true
		}
	}
	if instance.hasSource && len(row.sources) < maxRowSources {
		known := false
		for _, source := range row.sources {
			known = known || source.key() == instance.source.key()
		}
		if !known {
			row.sources = append(row.sources, instance.source)
		}
	}
	row.hud = row.hud || hudRow(row.definition, instance.holder, instance.source)
}

// hudRow is the History counterpart of the Overlay's display rule without its time parts: an effect whose definition the
// game draws in a HUD buff bar, with a BUFF or DEBUFF arrow or without an arrow (neutral), held by the local team, or held
// by an enemy and applied by the local team. A summon is on no HUD surface.
func hudRow(definition capture.EffectDefinition, holder, source ActorRef) bool {
	if !definition.HUDIcon || definition.Polarity == capture.EffectPolarityUnclassified {
		return false
	}
	switch holder.Kind {
	case ActorRole, ActorTeam:
		return true
	case ActorEnemy, ActorOther:
		return source.Kind == ActorRole || source.Kind == ActorSummon || source.Kind == ActorTeam || source.Kind == ActorUnassigned
	}
	return false
}

// rowClass orders rows: the team's effects, then enemies', then the rest; buffs, debuffs, statuses the HUD draws without
// an arrow, then unclassified ones.
func (r *effectRowModel) rowClass() int {
	holder := 2
	switch r.holder.Kind {
	case ActorRole, ActorTeam, ActorSummon, ActorUnassigned:
		holder = 0
	case ActorEnemy:
		holder = 1
	}
	polarity := 3
	switch r.definition.Polarity {
	case capture.EffectPolarityBuff:
		polarity = 0
	case capture.EffectPolarityDebuff:
		polarity = 1
	case capture.EffectPolarityNeutral:
		polarity = 2
	}
	return holder*4 + polarity
}

func (r *effectRowModel) less(other *effectRowModel) bool {
	if a, b := r.rowClass(), other.rowClass(); a != b {
		return a < b
	}
	if a, b := r.firstSeen(), other.firstSeen(); a != b {
		return a < b
	}
	return r.key < other.key
}

func (r *effectRowModel) firstSeen() int64 {
	first := int64(0)
	for _, instance := range r.instances {
		if len(instance.facts) > 0 && (first == 0 || instance.facts[0].at < first) {
			first = instance.facts[0].at
		}
	}
	return first
}

// layout turns the instance's facts into intervals within [start, end], following the rules at the top of this file.
func (i *effectInstanceModel) layout(start, end int64) {
	add := func(from, to int64, evidence string, stack sql.NullInt64) {
		from, to = max(from, start), min(to, end)
		if to > from {
			i.segments = append(i.segments, effectSegment{from: from, to: to, evidence: evidence, stack: stack})
		}
	}
	mark := func(at int64, kind string) {
		if at >= start && at <= end {
			i.markers = append(i.markers, effectMarker{at: at, kind: kind})
		}
	}
	// tail lays out what follows the last fact up to until: stated while the server's time runs, unknown after it.
	tail := func(last effectFact, until int64) {
		expiry := last.statedExpiry()
		if expiry == 0 || expiry >= until {
			add(last.at, until, EffectStated, last.stack)
			return
		}
		add(last.at, expiry, EffectStated, last.stack)
		add(max(expiry, last.at), until, EffectUnknown, last.stack)
	}
	// The instance's first observation marks its start: an observed APPLY, or a start known only to lie before it.
	if len(i.facts) > 0 && i.facts[0].change != "instant" && i.firstFact != 0 {
		if i.startUnconfirmed {
			mark(i.firstFact, markerStartUnconfirmed)
		} else {
			mark(i.firstFact, markerApplied)
		}
	}
	var alive *effectFact
	for index := range i.facts {
		fact := i.facts[index]
		if fact.at == 0 {
			continue
		}
		switch fact.change {
		case "instant":
			mark(fact.at, markerInstant)
			alive = nil
		case "remove", "holder_removed":
			if alive != nil {
				add(alive.at, fact.at, EffectConfirmed, alive.stack)
			}
			kind := markerRemoved
			if fact.change == "holder_removed" {
				kind = markerHolderRemoved
			}
			mark(fact.at, kind)
			alive = nil
		case "superseded", "untracked":
			if alive != nil {
				tail(*alive, fact.at)
			}
			kind := markerUntracked
			if fact.change == "superseded" {
				kind = markerSuperseded
			}
			mark(fact.at, kind)
			alive = nil
		default:
			if alive != nil {
				add(alive.at, fact.at, EffectConfirmed, alive.stack)
			}
			current := fact
			if !current.stack.Valid && alive != nil {
				current.stack = alive.stack
			}
			alive = &current
		}
	}
	if alive != nil {
		tail(*alive, end)
		if alive.at <= end {
			i.markers = append(i.markers, effectMarker{at: end, kind: markerContinues})
		}
	}
}

// EffectSegmentRef is one interval of an effect row, in milliseconds from the encounter start.
type EffectSegmentRef struct {
	FromMs   float64 `json:"fromMs"`
	ToMs     float64 `json:"toMs"`
	Evidence string  `json:"evidence"`
	Stack    *int64  `json:"stack,omitempty"`
}

// EffectMarkerRef is one point of an effect row: a start, an end and how it is known, or an instant effect.
type EffectMarkerRef struct {
	AtMs float64 `json:"atMs"`
	Kind string  `json:"kind"`
}

// EffectRow is one holder's one effect over the encounter.
type EffectRow struct {
	Key              string                 `json:"key"`
	EffectID         *int64                 `json:"effectId,omitempty"`
	DefinitionLinked bool                   `json:"definitionLinked"`
	Polarity         capture.EffectPolarity `json:"polarity"`
	HUDIcon          bool                   `json:"hudIcon"`
	ElementID        uint64                 `json:"elementId,omitempty"`
	// TermID and TermStatus link the effect to a glossary term (CORRELATED at most); the host resolves its name.
	TermID      uint64             `json:"termId,omitempty"`
	TermStatus  string             `json:"termStatus,omitempty"`
	HUD         bool               `json:"hud"`
	Holder      ActorRef           `json:"holder"`
	Sources     []ActorRef         `json:"sources"`
	Instances   int                `json:"instances"`
	Segments    []EffectSegmentRef `json:"segments"`
	Markers     []EffectMarkerRef  `json:"markers"`
	Unconfirmed bool               `json:"startUnconfirmed,omitempty"`
}

// EffectLanes lists the effect rows of an encounter for a scope and the contributor and enemy filters.
type EffectLanes struct {
	State      string      `json:"state"`
	Truncated  bool        `json:"truncated,omitempty"`
	Scope      string      `json:"scope"`
	DurationMs float64     `json:"durationMs"`
	Rows       []EffectRow `json:"rows"`
	// More counts rows beyond the bound; Hidden counts rows the scope left out.
	More   int `json:"more,omitempty"`
	Hidden int `json:"hidden"`
	// Stale counts instances alive before the encounter only by a statement whose time ran out before it, with nothing
	// observed about them during it: they are not drawn.
	Stale    int      `json:"stale"`
	Rejected int64    `json:"rejected"`
	Gaps     []GapRef `json:"gaps"`
}

// rowMatches applies the contributor and enemy filters to a row: its holder or one of its sources.
func (q resolvedQuery) rowMatches(row *effectRowModel) bool {
	if q.Contributor != "" && !row.contributors[q.Contributor] {
		return false
	}
	return q.Enemy < 0 || row.enemies[q.Enemy]
}

// Effects lists the effect rows of a finished encounter in the analysis scope.
func (s *Store) Effects(ctx context.Context, id int64, query AnalysisQuery, masters *capture.MasterData) (EffectLanes, error) {
	model, err := s.model(ctx, id, masters)
	var state errAnalysisState
	if err != nil && !errors.As(err, &state) {
		return EffectLanes{}, err
	}
	if err != nil {
		return EffectLanes{State: state.state, Rows: []EffectRow{}, Gaps: []GapRef{}}, nil
	}
	q := model.resolve(query)
	lanes := EffectLanes{State: AnalysisReady, Scope: q.Effects, DurationMs: nsToMs(int64(model.duration)), Rows: []EffectRow{}, Rejected: model.rejected, Gaps: model.gapRefs()}
	lanes.Truncated, lanes.Stale = model.effects.truncated, model.effects.stale
	start := model.start.UnixNano()
	for _, row := range model.effects.rows {
		if !q.rowMatches(row) {
			continue
		}
		if q.Effects == "hud" && !row.hud {
			lanes.Hidden++
			continue
		}
		if len(lanes.Rows) >= maxEffectRows {
			lanes.More++
			continue
		}
		lanes.Rows = append(lanes.Rows, row.lane(start))
	}
	return lanes, nil
}

func (r *effectRowModel) lane(start int64) EffectRow {
	out := EffectRow{Key: r.key, DefinitionLinked: r.definitionLinked, Polarity: r.definition.Polarity, HUDIcon: r.definition.HUDIcon, HUD: r.hud,
		Holder: r.holder, Sources: append([]ActorRef{}, r.sources...), Instances: len(r.instances), Segments: []EffectSegmentRef{}, Markers: []EffectMarkerRef{}}
	if out.Polarity == "" {
		out.Polarity = capture.EffectPolarityUnclassified
	}
	if r.effectID.Valid {
		id := r.effectID.Int64
		out.EffectID = &id
	}
	if static := r.definition.Static; static != nil {
		out.ElementID = static.ElementID
		if static.Term != nil {
			out.TermID, out.TermStatus = static.Term.TermID, static.Term.Status
		}
	}
	for _, instance := range r.instances {
		out.Unconfirmed = out.Unconfirmed || instance.startUnconfirmed
		for _, segment := range instance.segments {
			ref := EffectSegmentRef{FromMs: wholeMs(segment.from - start), ToMs: wholeMs(segment.to - start), Evidence: segment.evidence}
			if segment.stack.Valid {
				stack := segment.stack.Int64
				ref.Stack = &stack
			}
			out.Segments = append(out.Segments, ref)
		}
		for _, marker := range instance.markers {
			out.Markers = append(out.Markers, EffectMarkerRef{AtMs: wholeMs(marker.at - start), Kind: marker.kind})
		}
	}
	sort.SliceStable(out.Segments, func(a, b int) bool { return out.Segments[a].FromMs < out.Segments[b].FromMs })
	sort.SliceStable(out.Markers, func(a, b int) bool { return out.Markers[a].AtMs < out.Markers[b].AtMs })
	return out
}

// EffectCoverage is one effect row's time in a range. ConfirmedMs, PartialMs and UnknownMs never overlap; GapMs is the
// range's capture-gap time, of which any confirmed part is counted as partial. Ratio is the exact share of the range when
// one can be given; otherwise Lower is a lower bound and Upper, when known, an upper bound.
type EffectCoverage struct {
	Key              string   `json:"key"`
	ConfirmedMs      float64  `json:"confirmedMs"`
	PartialMs        float64  `json:"partialMs"`
	UnknownMs        float64  `json:"unknownMs"`
	GapMs            float64  `json:"gapMs"`
	Exact            bool     `json:"exact"`
	Ratio            *float64 `json:"ratio,omitempty"`
	Lower            float64  `json:"lower"`
	Upper            *float64 `json:"upper,omitempty"`
	MaxStack         *int64   `json:"maxStack,omitempty"`
	Applies          int      `json:"applies"`
	StartUnconfirmed bool     `json:"startUnconfirmed,omitempty"`
}

// EffectCoverageOf is the coverage of every row of the analysis scope in the range.
type EffectCoverageOf struct {
	Available bool             `json:"available"`
	Scope     string           `json:"scope"`
	Rejected  int64            `json:"rejected"`
	Rows      []EffectCoverage `json:"rows"`
}

type span struct{ from, to int64 }

// union merges intervals.
func union(spans []span) []span {
	sort.Slice(spans, func(a, b int) bool { return spans[a].from < spans[b].from })
	out := spans[:0]
	for _, s := range spans {
		if s.to <= s.from {
			continue
		}
		if n := len(out); n > 0 && s.from <= out[n-1].to {
			out[n-1].to = max(out[n-1].to, s.to)
			continue
		}
		out = append(out, s)
	}
	return out
}

// subtract removes b from a; both are unions.
func subtract(a, b []span) []span {
	var out []span
	for _, s := range a {
		from := s.from
		for _, cut := range b {
			if cut.to <= from || cut.from >= s.to {
				continue
			}
			if cut.from > from {
				out = append(out, span{from, cut.from})
			}
			from = max(from, cut.to)
		}
		if from < s.to {
			out = append(out, span{from, s.to})
		}
	}
	return out
}

func intersect(a, b []span) []span {
	var out []span
	for _, s := range a {
		for _, t := range b {
			if from, to := max(s.from, t.from), min(s.to, t.to); to > from {
				out = append(out, span{from, to})
			}
		}
	}
	return union(out)
}

func length(spans []span) int64 {
	total := int64(0)
	for _, s := range spans {
		total += s.to - s.from
	}
	return total
}

// effectCoverage measures every row of the scope in the query's range.
func (m *encounterModel) effectCoverage(q resolvedQuery) EffectCoverageOf {
	out := EffectCoverageOf{Scope: q.Effects, Rejected: m.rejected, Rows: []EffectCoverage{}}
	if m.effects == nil || m.effects.truncated {
		return out
	}
	out.Available = true
	start := m.start.UnixNano()
	rangeSpan := []span{{start + q.from, start + q.to}}
	rangeLength := q.to - q.from
	var gaps []span
	for _, gap := range m.gaps {
		gaps = append(gaps, span{gap.from, gap.to})
	}
	gaps = intersect(union(gaps), rangeSpan)
	for _, row := range m.effects.rows {
		if !q.rowMatches(row) || q.Effects == "hud" && !row.hud {
			continue
		}
		var confirmed, stated, unknown []span
		coverage := EffectCoverage{Key: row.key}
		for _, instance := range row.instances {
			for _, segment := range instance.segments {
				s := span{segment.from, segment.to}
				switch segment.evidence {
				case EffectConfirmed:
					confirmed = append(confirmed, s)
				case EffectStated:
					stated = append(stated, s)
				default:
					unknown = append(unknown, s)
				}
				if segment.stack.Valid && segment.to > start+q.from && segment.from < start+q.to && (coverage.MaxStack == nil || segment.stack.Int64 > *coverage.MaxStack) {
					stack := segment.stack.Int64
					coverage.MaxStack = &stack
				}
			}
			for _, marker := range instance.markers {
				if marker.at >= start+q.from && marker.at < start+q.to {
					if marker.kind == markerApplied {
						coverage.Applies++
					}
					if marker.kind == markerStartUnconfirmed && marker.at > start+q.from {
						coverage.StartUnconfirmed = true
					}
				}
			}
		}
		c := intersect(union(confirmed), rangeSpan)
		inGap := intersect(c, gaps)
		c = subtract(c, inGap)
		p := subtract(union(append(intersect(union(stated), rangeSpan), inGap...)), c)
		u := subtract(subtract(intersect(union(unknown), rangeSpan), c), p)
		coverage.ConfirmedMs, coverage.PartialMs, coverage.UnknownMs, coverage.GapMs = wholeMs(length(c)), wholeMs(length(p)), wholeMs(length(u)), wholeMs(length(gaps))
		if rangeLength > 0 {
			coverage.Lower = float64(length(c)) / float64(rangeLength)
			coverage.Exact = length(p) == 0 && length(u) == 0 && length(gaps) == 0 && !coverage.StartUnconfirmed && m.rejected == 0
			if coverage.Exact {
				ratio := coverage.Lower
				coverage.Ratio = &ratio
			}
			if !coverage.StartUnconfirmed {
				upper := float64(length(c)+length(p)+length(u)) / float64(rangeLength)
				coverage.Upper = &upper
			}
		}
		out.Rows = append(out.Rows, coverage)
	}
	return out
}
