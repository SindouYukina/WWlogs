package capture

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Role credit of negative-status damage (Aero Erosion, Spectro Frazzle and the other AbnormalDamageConfig statuses).
//
// The server executes a negative status on an enemy and names the scene's combat entity as attacker, so the damage result
// itself names no character (damage_notify.go). Research on the Global 3.8 client and the four 3.8.9 captures
// (research/runs/2026-11-14/v0.7.0-causal-damage-attribution) established who causes each execution:
//
//   - A status on an enemy is one shared instance (StackingType 2: BaseBuffComponent.GetStackableBuff returns the holder's
//     instance whoever adds a stack). Its InstigatorId is set once, when the instance is created (ActiveBuff.AU), and never
//     changes; later stacks report their own instigator only in BuffStackCountNotify. That InstigatorId is the effect owner.
//   - The instance executes on its own period timer, and once right after its creation (bExecutePeriodicEffectOnApplication).
//     Every execution also runs the status's related extra effects with the instance as the preceding Buff
//     (ExecuteAddBuffByStackCount -> AddIterativeBuff(..., this.Buff)), and the server reports the Buff they add (Aero
//     Erosion: 10040002) with the instance's InstigatorId in the same receive pack: the server names the executing
//     instance's owner at every execution.
//   - An InvokePeriod Buff (extra effect type 67) that a character applies to the holder forces an immediate execution of
//     the named statuses (InvokePeriod.OnExecute -> ApplyPeriodExecution) without touching their timers. The server
//     reports it as an instant ApplyGameplayEffectNotify (no handle) with its own InstigatorId, earlier in the same receive
//     pack as the forced result, or at the start of a following one when the server closed the pack after the trigger.
//
// Product rule (owner policy, research §6): a forced execution is credited to the role that triggered it (the trigger's
// InstigatorId); any other execution to the role that owns the executing instance. Both need runtime evidence that the
// entity is a local role or a summon of one (localRoleForEntity). The executing entity, the scene combat entity, stays the
// record's source. When the live instances on the target have different owners, an instance has no owner, or the credited
// entity is no local role, the damage stays the local team's but no role's (UnassignedRoleTotals.NegativeStatusDamage).

const (
	// roleBasisNegativeStatusTrigger credits a forced negative-status execution to the role whose InvokePeriod forced it.
	roleBasisNegativeStatusTrigger = "negative_status_trigger"
	// roleBasisNegativeStatusOwner credits a negative-status execution to the role that owns the executing status instance.
	roleBasisNegativeStatusOwner = "negative_status_owner"
	// RoleBasisNegativeStatusTrigger and RoleBasisNegativeStatusOwner are the stored forms of the two credit bases.
	RoleBasisNegativeStatusTrigger = roleBasisNegativeStatusTrigger
	RoleBasisNegativeStatusOwner   = roleBasisNegativeStatusOwner
)

// invokePeriodExtraEffect is the client's ExtraEffectDefine type of InvokePeriod (getBuffExecutionClass(67)).
const invokePeriodExtraEffect = 67

// statusTriggerCarry is how long after an InvokePeriod its forced result may still arrive. In the captures the result
// followed in the same receive pack or in the next one within 0.12 s; nothing else ties a later result to the trigger.
const statusTriggerCarry = 150 * time.Millisecond

// maxStatusTriggers bounds the remembered triggers. Each lives at most statusTriggerCarry; a burst of a full team on many
// enemies stays far below this, so the bound only stops pathological input.
const maxStatusTriggers = 256

// negativeStatusTerm names the status behind a negative-status DamageId.
type negativeStatusTerm struct {
	termID uint64
	name   string
	// iconBuff is the lowest executing status Buff that carries the term; the status row shows its Buff icon.
	iconBuff uint64
}

// statusTrigger is one InvokePeriod the server applied on a holder.
type statusTrigger struct {
	session            int
	holder, generation uint64
	at                 time.Time
	effectID           uint64
	source             uint64
	hasSource          bool
	// used lists the handles of the status instances whose forced execution this trigger already explained.
	used []uint64
}

// statusTriggerLog keeps the InvokePeriod triggers of the last statusTriggerCarry in arrival order.
type statusTriggerLog struct {
	entries []statusTrigger
	dropped uint64
}

// add remembers a trigger and forgets those too old to explain a result now.
func (l *statusTriggerLog) add(trigger statusTrigger) {
	l.prune(trigger.at)
	if len(l.entries) >= maxStatusTriggers {
		l.entries = l.entries[1:]
		l.dropped++
	}
	l.entries = append(l.entries, trigger)
}

func (l *statusTriggerLog) prune(at time.Time) {
	kept := l.entries[:0]
	for _, entry := range l.entries {
		if !at.IsZero() && at.Sub(entry.at) > statusTriggerCarry {
			continue
		}
		kept = append(kept, entry)
	}
	for i := len(kept); i < len(l.entries); i++ {
		l.entries[i] = statusTrigger{}
	}
	l.entries = kept
}

// liveStatusInstance is one live status instance on a holder that executes a negative-status DamageId.
type liveStatusInstance struct {
	handle    uint64
	effectID  uint64
	source    uint64
	hasSource bool
}

// negativeStatusExecution is what the server's own messages say about one negative-status result: which instance
// executed, whether an InvokePeriod forced it, and whose action that was.
type negativeStatusExecution struct {
	// CreditEntity is the runtime entity the result is credited to (trigger or owner); zero when none qualifies.
	CreditEntity uint64
	// CreditBasis is roleBasisNegativeStatusTrigger or roleBasisNegativeStatusOwner, empty without a credit entity.
	CreditBasis string
	// StatusEffectID is the executing status Buff, when one instance can be named.
	StatusEffectID uint64
	// TriggerEffectID is the InvokePeriod Buff that forced the execution; zero for the instance's own execution.
	TriggerEffectID uint64
	// Owner is the executing instance's InstigatorId; zero when the live instances disagree or name none.
	Owner uint64
	// Reason says why the result has no credit entity; empty with one.
	Reason string
}

// recordStatusTrigger remembers an instant InvokePeriod the server applied, for the forced result that follows it.
func (p *processor) recordStatusTrigger(row buffLifecycleAuditRow, holder uint64) {
	if p == nil || row.ID == nil || row.HandleID == nil || validEffectHandle(*row.HandleID) {
		return
	}
	if len(p.masters.invokePeriodStatuses(*row.ID)) == 0 {
		return
	}
	trigger := statusTrigger{session: row.SessionNumber, holder: holder, generation: p.effectHolderGeneration(holder), at: row.Timestamp, effectID: *row.ID}
	if row.InstigatorID != nil && *row.InstigatorID != 0 {
		trigger.source, trigger.hasSource = *row.InstigatorID, true
	}
	p.statusTriggers.add(trigger)
	p.notifyStats.statusTriggers++
}

// matchNegativeStatusExecution explains one negative-status result on target: a remembered InvokePeriod that names a live
// executing instance and has not explained that instance's execution yet forces it, oldest first; otherwise the result is
// the instance's own execution, credited to its owner when every live executing instance has the same one. It runs for
// every such result, counted or not, so a trigger explains at most one execution of each instance it names.
func (p *processor) matchNegativeStatusExecution(target, damageID uint64, at time.Time) negativeStatusExecution {
	generation := p.effectHolderGeneration(target)
	executes := func(effectID uint64) bool { return p.masters.negativeStatusExecutes(effectID, damageID) }
	live := p.effects.liveStatusInstances(p.sessionNumber, target, generation, executes, at, negativeStatusLiveSlack)
	if len(live) == 0 {
		return negativeStatusExecution{Reason: "no live status instance on the target executes this DamageId"}
	}
	p.statusTriggers.prune(at)
	for i := range p.statusTriggers.entries {
		trigger := &p.statusTriggers.entries[i]
		if trigger.session != p.sessionNumber || trigger.holder != target || trigger.generation != generation || at.Before(trigger.at) {
			continue
		}
		named := p.masters.invokePeriodStatuses(trigger.effectID)
		for _, instance := range live {
			if !containsID(named, instance.effectID) || containsID(trigger.used, instance.handle) {
				continue
			}
			trigger.used = insertSortedID(trigger.used, instance.handle)
			execution := negativeStatusExecution{StatusEffectID: instance.effectID, TriggerEffectID: trigger.effectID}
			if instance.hasSource {
				execution.Owner = instance.source
			}
			if !trigger.hasSource {
				execution.Reason = "the InvokePeriod that forced the execution names no instigator"
				return execution
			}
			execution.CreditEntity, execution.CreditBasis = trigger.source, roleBasisNegativeStatusTrigger
			return execution
		}
	}
	execution := negativeStatusExecution{StatusEffectID: live[0].effectID}
	for _, instance := range live {
		if !instance.hasSource {
			execution.Reason = "a live status instance on the target names no instigator"
			return execution
		}
		if instance.source != live[0].source {
			execution.Reason = "the live status instances on the target have different owners"
			execution.StatusEffectID = 0
			return execution
		}
		if instance.effectID != execution.StatusEffectID {
			execution.StatusEffectID = 0
		}
	}
	execution.Owner = live[0].source
	execution.CreditEntity, execution.CreditBasis = live[0].source, roleBasisNegativeStatusOwner
	return execution
}

// negativeStatusRole resolves the credit entity of a local negative-status result to its local role, with the credit basis
// in place of the entity's own role basis. Nil when the entity is no local role or summon of one.
func (p *processor) negativeStatusRole(execution negativeStatusExecution) *CombatRole {
	if execution.CreditEntity == 0 {
		return nil
	}
	role := p.localRoleForEntity(execution.CreditEntity, entityIdentity{})
	if role == nil {
		return nil
	}
	credited := *role
	credited.Basis = execution.CreditBasis
	return &credited
}

// isNegativeStatusRoleBasis reports a role credit given through negativeStatusExecution.
func isNegativeStatusRoleBasis(basis string) bool {
	return basis == roleBasisNegativeStatusTrigger || basis == roleBasisNegativeStatusOwner
}

// IsNegativeStatusRoleBasis reports a stored role basis that credits a negative-status result to a role; Combat History
// reads the status split of its stored results with it.
func IsNegativeStatusRoleBasis(basis string) bool {
	return isNegativeStatusRoleBasis(basis)
}

// liveStatusInstances lists the live instances on one holder whose effect id satisfies executes, ordered by handle. An
// instance past its estimated expiry by more than slack is no longer live (the rule of liveStatusSources).
func (t *EffectTracker) liveStatusInstances(session int, holder, generation uint64, executes func(uint64) bool, at time.Time, slack time.Duration) []liveStatusInstance {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var live []liveStatusInstance
	for key, instance := range t.instances {
		if key.session != session || key.holder != holder || key.generation != generation || !instance.hasEffectID || !executes(instance.effectID) {
			continue
		}
		if instance.timeKnown && !at.IsZero() && t.overdueLocked(instance, at) > slack.Seconds() {
			continue
		}
		live = append(live, liveStatusInstance{handle: key.handle, effectID: instance.effectID, source: instance.source, hasSource: instance.hasSource})
	}
	sort.Slice(live, func(i, j int) bool { return live[i].handle < live[j].handle })
	return live
}

// insertSortedID adds id to a sorted ID list once.
func insertSortedID(ids []uint64, id uint64) []uint64 {
	index := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	if index < len(ids) && ids[index] == id {
		return ids
	}
	ids = append(ids, 0)
	copy(ids[index+1:], ids[index:])
	ids[index] = id
	return ids
}

// invokePeriodStatuses returns the status Buffs an InvokePeriod Buff forces, sorted; nil for any other Buff.
func (m *gameMaster) invokePeriodStatuses(effectID uint64) []uint64 {
	if m == nil {
		return nil
	}
	return m.invokePeriodTargets[effectID]
}

// NegativeStatusTerm returns the glossary name of the status behind a negative-status DamageId and the Buff whose icon
// shows it, or false when the DamageId is no negative status or its status Buffs name no single term.
func (m *gameMaster) negativeStatusTerm(damageID uint64) (negativeStatusTerm, bool) {
	if m == nil {
		return negativeStatusTerm{}, false
	}
	term, ok := m.negativeStatusTerms[damageID]
	return term, ok
}

// loadSQLiteNegativeStatusTriggers reads the InvokePeriod Buffs (buff.extra_effect_id 67) and the status Buffs their first
// extra-effect parameter names ("10041000#10051000"), keeping only names that are negative-status executors. It also names
// each negative-status DamageId by the glossary term its executing Buffs share. Both tables are optional.
func loadSQLiteNegativeStatusTriggers(db *sql.DB, master *gameMaster) error {
	defer nameNegativeStatuses(master)
	for _, table := range []string{"buff", "buff_extra_effect_parameters"} {
		exists, err := masterTableExists(db, table)
		if err != nil || !exists {
			return err
		}
	}
	rows, err := db.Query(`SELECT CAST(b.id AS TEXT), CAST(p.value AS TEXT) FROM buff b JOIN buff_extra_effect_parameters p
		ON CAST(p.owner_id AS TEXT) = CAST(b.id AS TEXT) AND p.ordinal = 0 WHERE CAST(b.extra_effect_id AS TEXT) = ?`, fmt.Sprint(invokePeriodExtraEffect))
	if err != nil {
		return fmt.Errorf("query InvokePeriod Buffs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawBuff, rawValue string
		if err := rows.Scan(&rawBuff, &rawValue); err != nil {
			return fmt.Errorf("read InvokePeriod Buff: %w", err)
		}
		buffID, err := parseMasterID(rawBuff, "buff.id")
		if err != nil {
			return err
		}
		var statuses []uint64
		for _, part := range strings.Split(rawValue, "#") {
			status, err := parseMasterID(strings.TrimSpace(part), "buff_extra_effect_parameters.value")
			if err != nil {
				continue // not a Buff id: InvokePeriod reads every part with Number(), a non-number names nothing
			}
			if _, executes := master.negativeStatusExecutors[status]; executes {
				statuses = insertSortedID(statuses, status)
			}
		}
		if len(statuses) > 0 {
			master.invokePeriodTargets[buffID] = statuses
		}
	}
	return rows.Err()
}

// nameNegativeStatuses names each negative-status DamageId by the one glossary term its executing status Buffs carry.
func nameNegativeStatuses(master *gameMaster) {
	type candidate struct {
		terms map[uint64]string
		icon  uint64
	}
	byDamage := map[uint64]*candidate{}
	for buffID, damageID := range master.negativeStatusExecutors {
		term, ok := master.buffTerms[buffID]
		if !ok || term.name == "" {
			continue
		}
		found := byDamage[damageID]
		if found == nil {
			found = &candidate{terms: map[uint64]string{}}
			byDamage[damageID] = found
		}
		found.terms[term.termID] = term.name
		if found.icon == 0 || buffID < found.icon {
			found.icon = buffID
		}
	}
	for damageID, found := range byDamage {
		if len(found.terms) != 1 {
			continue
		}
		for termID, name := range found.terms {
			master.negativeStatusTerms[damageID] = negativeStatusTerm{termID: termID, name: name, iconBuff: found.icon}
		}
	}
}
