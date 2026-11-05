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
// entity. Static skill-to-role tables, names, and fixed IDs are never used. Confirmed local damage without that
// evidence is a separate vehicle contributor when its runtime vehicle chain is known; otherwise it stays in the
// unassigned bucket. Unattributed and excluded damage never enters this aggregation.
//
// Invariants (checked by tests and by the encounter replay collector):
//   sum(Roles.DamageDealt) + Vehicle.DamageDealt + UnassignedRole.DamageDealt == DamageDealt (confirmed local outgoing), events likewise;
//   sum(Roles.DamageTaken) + UnassignedRole.DamageTaken == DamageTaken (confirmed local incoming), events likewise;
//   per role, DirectDamage + SummonDamage == DamageDealt;
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
	// DirectDamage came from the role entity itself, SummonDamage through a verified summoner chain.
	DirectDamage   int64  `json:"directDamage"`
	SummonDamage   int64  `json:"summonDamage"`
	DamageTaken    int64  `json:"damageTaken"`
	IncomingEvents uint64 `json:"incomingEvents"`
}

// VehicleDamageSnapshot is a local motorcycle contributor, not a character or a replacement of raw source identity.
type VehicleDamageSnapshot struct {
	Kind           string `json:"kind"`
	Evidence       string `json:"evidence"`
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
}

// UnassignedRoleTotals is confirmed local damage without a role or recognized vehicle contributor.
type UnassignedRoleTotals struct {
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	DamageTaken    int64  `json:"damageTaken"`
	IncomingEvents uint64 `json:"incomingEvents"`
	// OverflowEvents counts events that had a role but arrived after maxAggregatedRoles roles were held.
	OverflowEvents uint64 `json:"overflowEvents"`
}

// maxAggregatedRoles bounds the role table; a team has four slots, so this only stops pathological input.
const maxAggregatedRoles = 256

type roleKey struct{ playerID, roleID uint64 }

type roleAggregate struct {
	vehicle    VehicleDamageSnapshot
	roles      map[roleKey]*RoleDamageSnapshot
	unassigned UnassignedRoleTotals
}

func (a *roleAggregate) entry(role *CombatRole) *RoleDamageSnapshot {
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
		a.roles = make(map[roleKey]*RoleDamageSnapshot)
	}
	entry := &RoleDamageSnapshot{PlayerID: role.PlayerID, RoleID: role.RoleID}
	a.roles[key] = entry
	return entry
}

// add credits one statistics record. Only confirmed local flows are aggregated.
func (a *roleAggregate) add(record CombatEventRecord) {
	switch record.Flow {
	case CombatFlowLocalOutgoing:
		if record.VehicleKind == vehicleKindMotorcycle {
			a.vehicle.Kind = vehicleKindMotorcycle
			a.vehicle.Evidence = string(resolutionCorrelated)
			a.vehicle.DamageDealt += record.Amount
			a.vehicle.OutgoingEvents++
			return
		}
		entry := a.entry(record.SourceRole)
		if entry == nil {
			a.unassigned.DamageDealt += record.Amount
			a.unassigned.OutgoingEvents++
			return
		}
		entry.DamageDealt += record.Amount
		entry.OutgoingEvents++
		if record.SourceRole.Basis == roleBasisSummonerChain {
			entry.SummonDamage += record.Amount
		} else {
			entry.DirectDamage += record.Amount
		}
	case CombatFlowLocalIncoming:
		entry := a.entry(record.TargetRole)
		if entry == nil {
			a.unassigned.DamageTaken += record.Amount
			a.unassigned.IncomingEvents++
			return
		}
		entry.DamageTaken += record.Amount
		entry.IncomingEvents++
	}
}

// snapshot returns an independent copy, ordered by PlayerId then RoleId.
func (a *roleAggregate) snapshot() ([]RoleDamageSnapshot, UnassignedRoleTotals) {
	roles := make([]RoleDamageSnapshot, 0, len(a.roles))
	for _, entry := range a.roles {
		roles = append(roles, *entry)
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
	return &copied
}
