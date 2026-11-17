package capture

import "testing"

func completenessRole(id uint64, damage int64, events uint64) RoleDamageSnapshot {
	return RoleDamageSnapshot{RoleID: id, DamageDealt: damage, OutgoingEvents: events}
}

func TestDamageCompletenessKeepsTheFactsIndependent(t *testing.T) {
	vehicle := &VehicleDamageSnapshot{Kind: VehicleKindMotorcycle, DamageDealt: 400, OutgoingEvents: 4}
	noSkillRole := completenessRole(1, 600, 6)
	noSkillRole.UnassignedSkill = UnassignedSkillTotals{DamageDealt: 50, OutgoingEvents: 2, MissingSkillEvents: 2}
	noSkillVehicle := &VehicleDamageSnapshot{Kind: VehicleKindMotorcycle, DamageDealt: 400, OutgoingEvents: 4, UnassignedSkill: UnassignedSkillTotals{DamageDealt: 30, OutgoingEvents: 1, MissingSkillEvents: 1}}
	cases := []struct {
		name  string
		scope damageScope
		want  ViewDamageCompleteness
	}{
		{
			name:  "a verified vehicle is a contributor, not a gap in role attribution",
			scope: damageScope{damage: 1000, events: 10, roles: []RoleDamageSnapshot{completenessRole(1, 600, 6)}, vehicle: vehicle},
			want:  ViewDamageCompleteness{Conserved: true, RoleAttributionComplete: true, SkillBreakdownComplete: true},
		},
		{
			name:  "unassigned local damage is a gap in role attribution only",
			scope: damageScope{damage: 1000, events: 10, roles: []RoleDamageSnapshot{completenessRole(1, 900, 9)}, unassigned: UnassignedRoleTotals{DamageDealt: 100, OutgoingEvents: 1}},
			want:  ViewDamageCompleteness{Conserved: true, SkillBreakdownComplete: true, UnassignedDamage: 100, UnassignedEvents: 1},
		},
		{
			name:  "a hit without a SkillId is a gap in the skill split only",
			scope: damageScope{damage: 600, events: 6, roles: []RoleDamageSnapshot{noSkillRole}},
			want:  ViewDamageCompleteness{Conserved: true, RoleAttributionComplete: true, NoSkillDamage: 50, NoSkillEvents: 2},
		},
		{
			name:  "the skill split covers the vehicle as well",
			scope: damageScope{damage: 400, events: 4, vehicle: noSkillVehicle},
			want:  ViewDamageCompleteness{Conserved: true, RoleAttributionComplete: true, NoSkillDamage: 30, NoSkillEvents: 1},
		},
		{
			name:  "unknown ownership stays outside every confirmed fact",
			scope: damageScope{damage: 1000, events: 10, unknownDamage: 700, unknownEvents: 7, roles: []RoleDamageSnapshot{completenessRole(1, 1000, 10)}},
			want:  ViewDamageCompleteness{Conserved: true, RoleAttributionComplete: true, SkillBreakdownComplete: true, UnknownOwnershipDamage: 700, UnknownOwnershipEvents: 7},
		},
		{
			name:  "damage that no contributor explains is not conserved",
			scope: damageScope{damage: 1000, events: 10, roles: []RoleDamageSnapshot{completenessRole(1, 900, 10)}},
			want:  ViewDamageCompleteness{RoleAttributionComplete: true, SkillBreakdownComplete: true},
		},
		{
			name:  "events count toward conservation too",
			scope: damageScope{damage: 1000, events: 10, roles: []RoleDamageSnapshot{completenessRole(1, 1000, 9)}},
			want:  ViewDamageCompleteness{RoleAttributionComplete: true, SkillBreakdownComplete: true},
		},
		{
			name:  "an empty scope has no gaps",
			scope: damageScope{},
			want:  ViewDamageCompleteness{Conserved: true, RoleAttributionComplete: true, SkillBreakdownComplete: true},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := damageCompleteness(test.scope); got != test.want {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestDamageCompletenessFollowsItsOwnScope(t *testing.T) {
	clean := EncounterSnapshot{Totals: EncounterTotals{DamageDealt: 100, OutgoingEvents: 1}, Roles: []RoleDamageSnapshot{completenessRole(1, 100, 1)}}
	gap := EncounterSnapshot{
		Totals: EncounterTotals{DamageDealt: 100, OutgoingEvents: 1, UnattributedOutgoingDamage: 40, UnattributedOutgoingEvents: 2},
		Roles:  []RoleDamageSnapshot{completenessRole(1, 100, 1)},
	}
	if got := encounterDamageCompleteness(clean); got.UnknownOwnershipEvents != 0 {
		t.Fatalf("a clean encounter inherited another encounter's gap: %+v", got)
	}
	if got := encounterDamageCompleteness(gap); got.UnknownOwnershipEvents != 2 || got.UnknownOwnershipDamage != 40 || !got.RoleAttributionComplete {
		t.Fatalf("encounter with unknown ownership: %+v", got)
	}
	interval := DamageStatisticsSnapshot{DamageDealt: 200, OutgoingEvents: 2, UnattributedOutgoingDamage: 40, UnattributedOutgoingEvents: 2, Roles: []RoleDamageSnapshot{completenessRole(1, 200, 2)}}
	if got := intervalDamageCompleteness(interval); !got.Conserved || got.UnknownOwnershipEvents != 2 {
		t.Fatalf("interval: %+v", got)
	}
}
