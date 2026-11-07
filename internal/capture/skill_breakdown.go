package capture

import "sort"

// Skill-level damage breakdown inside one damage contributor (a role or a vehicle).
//
// Damage ownership and skill attribution are separate facts:
//   - Ownership (which contributor) comes only from runtime role/vehicle evidence (role_aggregation.go).
//   - Skill attribution comes only from the paired DamageExecute request's DamageContext.SkillId, the protocol field
//     the client sends with that hit (Android Global 3.7.1 Njn.r5n, read as SkillId by ProcessRemoteDamage; Windows
//     Global DamageContext.SkillId field 4). The server-confirmed response then carries the amount.
//
// Nothing else assigns a skill: not timing, amount, element, the role's usual kit, the Master skill-to-role group, or a
// BulletId. A hit without a SkillId (wire value 0 or absent) keeps its confirmed owner and enters that owner's
// unassigned-skill bucket, so the owner's total never drops. Master supplies display names only; a SkillId without an
// official name is still its own row, shown by ID.
//
// Invariants (checked by tests and by the encounter replay collector), per contributor:
//   sum(Skills.DamageDealt) + UnassignedSkill.DamageDealt == contributor DamageDealt, events likewise.

// SkillDamageSnapshot is the confirmed local damage of one DamageContext.SkillId within one contributor.
type SkillDamageSnapshot struct {
	SkillID        uint64 `json:"skillId"`
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
}

// UnassignedSkillTotals is a contributor's confirmed damage that has no skill row.
type UnassignedSkillTotals struct {
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	// MissingSkillEvents had no DamageContext.SkillId; OverflowEvents had one but arrived after
	// maxSkillsPerContributor rows were held. Both are included in OutgoingEvents.
	MissingSkillEvents uint64 `json:"missingSkillEvents"`
	OverflowEvents     uint64 `json:"overflowEvents"`
}

// maxSkillsPerContributor bounds one contributor's skill table. Archived encounters show at most a few dozen SkillIds
// per role, so the bound only stops pathological input; overflow keeps its damage in the unassigned-skill bucket.
const maxSkillsPerContributor = 512

type skillAggregate struct {
	skills     map[uint64]*SkillDamageSnapshot
	unassigned UnassignedSkillTotals
}

func (a *skillAggregate) add(skillID uint64, amount int64) {
	if skillID == 0 {
		a.unassigned.DamageDealt += amount
		a.unassigned.OutgoingEvents++
		a.unassigned.MissingSkillEvents++
		return
	}
	entry, ok := a.skills[skillID]
	if !ok {
		if len(a.skills) >= maxSkillsPerContributor {
			a.unassigned.DamageDealt += amount
			a.unassigned.OutgoingEvents++
			a.unassigned.OverflowEvents++
			return
		}
		if a.skills == nil {
			a.skills = make(map[uint64]*SkillDamageSnapshot)
		}
		entry = &SkillDamageSnapshot{SkillID: skillID}
		a.skills[skillID] = entry
	}
	entry.DamageDealt += amount
	entry.OutgoingEvents++
}

// snapshot returns an independent copy ordered by damage, highest first; equal damage orders by SkillId so the order
// is reproducible.
func (a *skillAggregate) snapshot() ([]SkillDamageSnapshot, UnassignedSkillTotals) {
	skills := make([]SkillDamageSnapshot, 0, len(a.skills))
	for _, entry := range a.skills {
		skills = append(skills, *entry)
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].DamageDealt != skills[j].DamageDealt {
			return skills[i].DamageDealt > skills[j].DamageDealt
		}
		return skills[i].SkillID < skills[j].SkillID
	})
	return skills, a.unassigned
}
