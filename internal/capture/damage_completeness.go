package capture

// ViewDamageCompleteness states facts about the damage of one scope, the capture interval or one encounter. Each is decided
// on its own and none implies another, so no consumer has to merge them into one flag:
//
//   - Conserved: confirmed local damage and events equal the role rows, the vehicle row and the unassigned bucket.
//   - RoleAttributionComplete: no confirmed local hit is left without a role or a verified non-character contributor. A
//     vehicle is a contributor of its own, so its damage never makes attribution incomplete and is never credited to a role.
//     Negative-status damage the server executes through the scene's combat entity is credited to the role that forced or
//     owns the execution (negative_status_credit.go); what stays in the unassigned bucket (NegativeStatusDamage) had no
//     such role, so it makes attribution incomplete like any other unassigned hit.
//   - SkillBreakdownComplete: every contributor's hits carried a SkillId and fit its skill rows.
//   - UnknownOwnership: outgoing hits with an UNKNOWN endpoint. They are outside the confirmed totals, so they say nothing about
//     the three facts above; a non-zero value means the total shown does not include that damage.
type ViewDamageCompleteness struct {
	Conserved               bool   `json:"conserved"`
	RoleAttributionComplete bool   `json:"roleAttributionComplete"`
	UnassignedDamage        int64  `json:"unassignedDamage"`
	UnassignedEvents        uint64 `json:"unassignedEvents"`
	// NegativeStatusDamage and NegativeStatusEvents are the part of the unassigned bucket that is negative-status damage no
	// role could be credited with.
	NegativeStatusDamage   int64  `json:"negativeStatusDamage"`
	NegativeStatusEvents   uint64 `json:"negativeStatusEvents"`
	SkillBreakdownComplete bool   `json:"skillBreakdownComplete"`
	NoSkillDamage          int64  `json:"noSkillDamage"`
	NoSkillEvents          uint64 `json:"noSkillEvents"`
	UnknownOwnershipDamage int64  `json:"unknownOwnershipDamage"`
	UnknownOwnershipEvents uint64 `json:"unknownOwnershipEvents"`
}

// damageScope is the statistics of one scope that completeness is decided from.
type damageScope struct {
	damage, unknownDamage int64
	events, unknownEvents uint64
	roles                 []RoleDamageSnapshot
	vehicle               *VehicleDamageSnapshot
	unassigned            UnassignedRoleTotals
}

func damageCompleteness(scope damageScope) ViewDamageCompleteness {
	view := ViewDamageCompleteness{
		UnassignedDamage: scope.unassigned.DamageDealt, UnassignedEvents: scope.unassigned.OutgoingEvents,
		NegativeStatusDamage: scope.unassigned.NegativeStatusDamage, NegativeStatusEvents: scope.unassigned.NegativeStatusEvents,
		UnknownOwnershipDamage: scope.unknownDamage, UnknownOwnershipEvents: scope.unknownEvents,
	}
	damage, events := scope.unassigned.DamageDealt, scope.unassigned.OutgoingEvents
	addContributor := func(dealt int64, outgoing uint64, noSkill UnassignedSkillTotals) {
		damage += dealt
		events += outgoing
		view.NoSkillDamage += noSkill.DamageDealt
		view.NoSkillEvents += noSkill.OutgoingEvents
	}
	for _, role := range scope.roles {
		addContributor(role.DamageDealt, role.OutgoingEvents, role.UnassignedSkill)
	}
	if scope.vehicle != nil {
		addContributor(scope.vehicle.DamageDealt, scope.vehicle.OutgoingEvents, scope.vehicle.UnassignedSkill)
	}
	view.Conserved = damage == scope.damage && events == scope.events
	view.RoleAttributionComplete = view.UnassignedEvents == 0
	view.SkillBreakdownComplete = view.NoSkillEvents == 0
	return view
}

func intervalDamageCompleteness(statistics DamageStatisticsSnapshot) ViewDamageCompleteness {
	return damageCompleteness(damageScope{
		damage: statistics.DamageDealt, events: statistics.OutgoingEvents,
		unknownDamage: statistics.UnattributedOutgoingDamage, unknownEvents: statistics.UnattributedOutgoingEvents,
		roles: statistics.Roles, vehicle: statistics.Vehicle, unassigned: statistics.UnassignedRole,
	})
}

func encounterDamageCompleteness(encounter EncounterSnapshot) ViewDamageCompleteness {
	return damageCompleteness(damageScope{
		damage: encounter.Totals.DamageDealt, events: encounter.Totals.OutgoingEvents,
		unknownDamage: encounter.Totals.UnattributedOutgoingDamage, unknownEvents: encounter.Totals.UnattributedOutgoingEvents,
		roles: encounter.Roles, vehicle: encounter.Vehicle, unassigned: encounter.UnassignedRole,
	})
}
