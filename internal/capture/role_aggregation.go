package capture

import "sort"

// Role-level damage aggregation.
//
// Identities kept apart:
//   - EntityId: a runtime object in one scene of one protocol session. Role entities get new EntityIds at every scene
//     (no local role EntityId survived any of 73 archived scene joins), so EntityId is not a role identity.
//   - RoleId: the character configuration of a team slot, from the server's PlayerSceneGroup FightRoleInfos.
//   - PlayerId: the account owning the team; only the anchored local PlayerId is aggregated.
//   - CaptureSessionID / StatisticsIntervalID: the statistics owner and its interval; role totals live inside one.
//
// An event is credited to a role only by runtime evidence: its local endpoint is a role entity whose PlayerSceneGroup
// mapping names the local player and a RoleId, or it is a summon whose verified SummonerId chain ends at such an
// entity. Negative-status damage, which the server executes through the scene combat entity, is credited to the role
// whose InvokePeriod forced the execution or that owns the executing status instance, by the server's own messages
// (negative_status_credit.go); it is that role's StatusDamage, split by DamageId in Statuses, never a skill row. Static
// skill-to-role tables, names, and fixed IDs are never used. Confirmed local damage without that evidence is a separate
// vehicle contributor when its runtime vehicle chain is known; otherwise it stays in the unassigned bucket, which also
// counts apart the negative-status damage no role could be credited with (UnassignedRoleTotals.NegativeStatusDamage).
// Unattributed and excluded damage never enters this aggregation.
//
// Invariants (checked by tests and by the encounter replay collector):
//   sum(Roles.DamageDealt) + Vehicle.DamageDealt + UnassignedRole.DamageDealt == DamageDealt (confirmed local outgoing), events likewise;
//   sum(Roles.DamageTaken) + UnassignedRole.DamageTaken == DamageTaken (confirmed local incoming), events likewise;
//   per role, DirectDamage + SummonDamage + StatusDamage == DamageDealt;
//   per role and for the vehicle, sum(Skills) + UnassignedSkill + sum(Statuses) == DamageDealt, events likewise
//   (skill_breakdown.go; a vehicle has no Statuses);
//   the same holds per encounter against its Totals.

// Role credit bases.
const (
	roleBasisRoleEntity    = "player_scene_group_role"
	roleBasisSummonerChain = "summoner_chain"
)

// CombatRole is the local role an event endpoint is credited to.
type CombatRole struct {
	PlayerID uint64 `json:"playerId"`
	RoleID   uint64 `json:"roleId"`
	Basis    string `json:"basis"`
}

// localRoleForEntity resolves the local role behind an entity, or nil without runtime evidence.
//
// The evidence is the role mapping's own provenance (hasPlayerSceneRoleMapping: a PlayerSceneGroup FightRoleInfos
// RoleId, without conflict), the same test the local-flow classifier applies. The resolver's per-record status is not
// used: it reflects the last message that touched the record, and an EntityActiveRequest update marks a role entity
// CORRELATED (seen for every local role entity in the archived captures) without changing its mapping. A record that
// does not exist (UNKNOWN) never credits a role.
func (p *processor) localRoleForEntity(entityID uint64, identity entityIdentity) *CombatRole {
	if p == nil || p.entities == nil || p.playerRelevance == nil || entityID == 0 {
		return nil
	}
	current, ok := identity.Runtime, identity.RuntimeStatus != resolutionUnknown && identity.Runtime.entityID == entityID
	if !ok {
		resolved, status := p.entities.resolve(entityID)
		if status == resolutionUnknown || resolved.entityID != entityID {
			return nil
		}
		current = resolved
	}
	seen := make(map[uint64]struct{}, maxSummonerEntityDepth)
	for depth := 0; depth < maxSummonerEntityDepth; depth++ {
		if hasPlayerSceneRoleMapping(current) && p.playerRelevance.isLocalPlayerRoleEntity(current) {
			basis := roleBasisRoleEntity
			if depth > 0 {
				basis = roleBasisSummonerChain
			}
			return &CombatRole{PlayerID: current.playerID, RoleID: current.roleID, Basis: basis}
		}
		if !current.hasSummonerID || current.summonerID == 0 {
			return nil
		}
		parentID := current.summonerID
		if _, loop := seen[parentID]; loop {
			return nil
		}
		seen[parentID] = struct{}{}
		parent, status := p.entities.resolve(parentID)
		if status == resolutionUnknown || parent.entityID != parentID {
			return nil
		}
		current = parent
	}
	return nil
}

// RoleDamageSnapshot is the confirmed local damage credited to one role.
type RoleDamageSnapshot struct {
	PlayerID       uint64 `json:"playerId"`
	RoleID         uint64 `json:"roleId"`
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	// DirectDamage came from the role entity itself, SummonDamage through a verified summoner chain, StatusDamage from
	// negative statuses the server executed for the role (negative_status_credit.go).
	DirectDamage   int64  `json:"directDamage"`
	SummonDamage   int64  `json:"summonDamage"`
	StatusDamage   int64  `json:"statusDamage"`
	DamageTaken    int64  `json:"damageTaken"`
	IncomingEvents uint64 `json:"incomingEvents"`
	// Skills and UnassignedSkill split DamageDealt by DamageContext.SkillId; Statuses holds StatusDamage by DamageId.
	Skills          []SkillDamageSnapshot  `json:"skills"`
	UnassignedSkill UnassignedSkillTotals  `json:"unassignedSkill"`
	Statuses        []StatusDamageSnapshot `json:"statuses,omitempty"`
}

// StatusDamageSnapshot is the negative-status damage of one DamageId credited to a role. TriggerEvents and TriggerDamage
// are the part credited through an InvokePeriod of the role; the rest was credited because the role owns the executing
// status instance.
type StatusDamageSnapshot struct {
	DamageID       uint64 `json:"damageId"`
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	TriggerEvents  uint64 `json:"triggerEvents"`
	TriggerDamage  int64  `json:"triggerDamage"`
}

// maxStatusesPerRole bounds the status rows of one role; there are six negative statuses.
const maxStatusesPerRole = 16

// VehicleDamageSnapshot is a local motorcycle contributor, not a character or a replacement of raw source identity.
type VehicleDamageSnapshot struct {
	Kind           string `json:"kind"`
	Evidence       string `json:"evidence"`
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	// Skills and UnassignedSkill split DamageDealt by DamageContext.SkillId.
	Skills          []SkillDamageSnapshot `json:"skills"`
	UnassignedSkill UnassignedSkillTotals `json:"unassignedSkill"`
}

// UnassignedRoleTotals is confirmed local damage without a role or recognized vehicle contributor.
type UnassignedRoleTotals struct {
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	DamageTaken    int64  `json:"damageTaken"`
	IncomingEvents uint64 `json:"incomingEvents"`
	// OverflowEvents counts events that had a role but arrived after maxAggregatedRoles roles were held.
	OverflowEvents uint64 `json:"overflowEvents"`
	// NegativeStatusDamage and NegativeStatusEvents are the part of DamageDealt and OutgoingEvents that is the local team's
	// negative-status damage the server executed through the scene's combat entity (damage_notify.go) and that no role
	// could be credited with: the live status instances had different owners or none, or the trigger or owner was no local
	// role (negative_status_credit.go). Damage credited to a role is the StatusDamage of that role instead.
	NegativeStatusDamage int64  `json:"negativeStatusDamage"`
	NegativeStatusEvents uint64 `json:"negativeStatusEvents"`
}

// maxAggregatedRoles bounds the role table; a team has four slots, so this only stops pathological input.
const maxAggregatedRoles = 256

type roleKey struct{ playerID, roleID uint64 }

type roleAggregate struct {
	vehicle       VehicleDamageSnapshot
	vehicleSkills skillAggregate
	roles         map[roleKey]*roleEntry
	unassigned    UnassignedRoleTotals
}

// roleEntry holds the totals of one role with its skill and status splits; Skills and Statuses on the embedded snapshot
// stay nil until snapshot().
type roleEntry struct {
	RoleDamageSnapshot
	skills   skillAggregate
	statuses map[uint64]*StatusDamageSnapshot
}

// addStatus credits one negative-status result to the status row of its DamageId.
func (e *roleEntry) addStatus(record CombatEventRecord) {
	row := e.statuses[record.DamageID]
	if row == nil {
		if len(e.statuses) >= maxStatusesPerRole {
			// Pathological input only: the damage stays with the role, in its unassigned-skill bucket.
			e.DirectDamage += record.Amount
			e.skills.add(0, record.DamageID, record.Amount, nil)
			return
		}
		if e.statuses == nil {
			e.statuses = make(map[uint64]*StatusDamageSnapshot)
		}
		row = &StatusDamageSnapshot{DamageID: record.DamageID}
		e.statuses[record.DamageID] = row
	}
	e.StatusDamage += record.Amount
	row.DamageDealt += record.Amount
	row.OutgoingEvents++
	if record.SourceRole.Basis == roleBasisNegativeStatusTrigger {
		row.TriggerEvents++
		row.TriggerDamage += record.Amount
	}
}

// statusSnapshot returns the status rows ordered by DamageId.
func (e *roleEntry) statusSnapshot() []StatusDamageSnapshot {
	if len(e.statuses) == 0 {
		return nil
	}
	rows := make([]StatusDamageSnapshot, 0, len(e.statuses))
	for _, row := range e.statuses {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].DamageID < rows[j].DamageID })
	return rows
}

func (a *roleAggregate) entry(role *CombatRole) *roleEntry {
	if role == nil {
		return nil
	}
	key := roleKey{role.PlayerID, role.RoleID}
	if entry, ok := a.roles[key]; ok {
		return entry
	}
	if len(a.roles) >= maxAggregatedRoles {
		a.unassigned.OverflowEvents++
		return nil
	}
	if a.roles == nil {
		a.roles = make(map[roleKey]*roleEntry)
	}
	entry := &roleEntry{RoleDamageSnapshot: RoleDamageSnapshot{PlayerID: role.PlayerID, RoleID: role.RoleID}}
	a.roles[key] = entry
	return entry
}

// add credits one statistics record. Only confirmed local flows are aggregated. For confirmed local outgoing damage
// it returns the contributor it credited, so the DPS timeline splits damage exactly as these totals do.
func (a *roleAggregate) add(record CombatEventRecord) (TimelineContributor, bool) {
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		if record.VehicleKind == vehicleKindMotorcycle {
			a.vehicle.Kind = vehicleKindMotorcycle
			a.vehicle.Evidence = string(resolutionCorrelated)
			a.vehicle.DamageDealt += record.Amount
			a.vehicle.OutgoingEvents++
			a.vehicleSkills.add(record.SkillID, record.DamageID, record.Amount, nil)
			return TimelineContributor{Kind: TimelineContributorVehicle, VehicleKind: vehicleKindMotorcycle}, true
		}
		entry := a.entry(record.SourceRole)
		if entry == nil {
			a.unassigned.DamageDealt += record.Amount
			a.unassigned.OutgoingEvents++
			if record.Evidence == combatEvidenceNegativeStatusNotify {
				a.unassigned.NegativeStatusDamage += record.Amount
				a.unassigned.NegativeStatusEvents++
			}
			return TimelineContributor{Kind: TimelineContributorUnassigned}, true
		}
		entry.DamageDealt += record.Amount
		entry.OutgoingEvents++
		if record.Evidence == combatEvidenceNegativeStatusNotify && isNegativeStatusRoleBasis(record.SourceRole.Basis) {
			entry.addStatus(record)
			return TimelineContributor{Kind: TimelineContributorRole, PlayerID: entry.PlayerID, RoleID: entry.RoleID}, true
		}
		entry.skills.add(record.SkillID, record.DamageID, record.Amount, record.EchoVisions)
		if record.SourceRole.Basis == roleBasisSummonerChain {
			entry.SummonDamage += record.Amount
		} else {
			entry.DirectDamage += record.Amount
		}
		return TimelineContributor{Kind: TimelineContributorRole, PlayerID: entry.PlayerID, RoleID: entry.RoleID}, true
	case CombatFlowLocalIncoming:
		entry := a.entry(record.TargetRole)
		if entry == nil {
			a.unassigned.DamageTaken += record.Amount
			a.unassigned.IncomingEvents++
			return TimelineContributor{}, false
		}
		entry.DamageTaken += record.Amount
		entry.IncomingEvents++
	}
	return TimelineContributor{}, false
}

// snapshot returns an independent copy, ordered by PlayerId then RoleId.
func (a *roleAggregate) snapshot() ([]RoleDamageSnapshot, UnassignedRoleTotals) {
	roles := make([]RoleDamageSnapshot, 0, len(a.roles))
	for _, entry := range a.roles {
		role := entry.RoleDamageSnapshot
		role.Skills, role.UnassignedSkill = entry.skills.snapshot()
		role.Statuses = entry.statusSnapshot()
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool {
		if roles[i].PlayerID != roles[j].PlayerID {
			return roles[i].PlayerID < roles[j].PlayerID
		}
		return roles[i].RoleID < roles[j].RoleID
	})
	return roles, a.unassigned
}

// vehicleSnapshot returns a copy scoped to the same interval or encounter as the role aggregate.
func (a *roleAggregate) vehicleSnapshot() *VehicleDamageSnapshot {
	if a.vehicle.OutgoingEvents == 0 {
		return nil
	}
	copied := a.vehicle
	copied.Skills, copied.UnassignedSkill = a.vehicleSkills.snapshot()
	return &copied
}
