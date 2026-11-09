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
	// DamageIDs are the distinct DamageIds (ascending) that the same requests' DamageContext reported for this row's
	// hits. They are protocol facts only: they never change the row, its totals, or its owner. The view may derive a
	// display name from them (skill_names.go).
	DamageIDs []uint64 `json:"damageIds,omitempty"`
	// DamageIDsIncomplete marks a row with a hit that carried no DamageId, or with more distinct DamageIds than
	// maxDamageIDsPerSkill; DamageIDs then does not describe every hit.
	DamageIDsIncomplete bool `json:"damageIdsIncomplete,omitempty"`
	// EchoVisions are the distinct Vision ids (ascending) of the ConcomitantVision entities the casting role held when these
	// hits landed. They are runtime evidence for the display name only and are not part of the statistics output.
	EchoVisions []uint64 `json:"-"`
}

// UnassignedSkillTotals is a contributor's confirmed damage that has no skill row.
type UnassignedSkillTotals struct {
	DamageDealt    int64  `json:"damageDealt"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	// MissingSkillEvents had no DamageContext.SkillId; OverflowEvents had one but arrived after
	// maxSkillsPerContributor rows were held. Both are included in OutgoingEvents.
	MissingSkillEvents uint64 `json:"missingSkillEvents"`
	OverflowEvents     uint64 `json:"overflowEvents"`
	// DamageIDs are the distinct DamageIds of the hits without a SkillId, as on a skill row. They never move a hit out of
	// this bucket; the view may name the Echo behind them (skill_names.go).
	DamageIDs           []uint64 `json:"damageIds,omitempty"`
	DamageIDsIncomplete bool     `json:"damageIdsIncomplete,omitempty"`
}

// maxSkillsPerContributor bounds one contributor's skill table. Archived encounters show at most a few dozen SkillIds
// per role, so the bound only stops pathological input; overflow keeps its damage in the unassigned-skill bucket.
const maxSkillsPerContributor = 512

// maxDamageIDsPerSkill bounds the distinct DamageIds one skill row keeps. Archived rows hold at most ten.
const maxDamageIDsPerSkill = 32

// maxEchoVisionsPerSkill bounds the Echo identities one skill row keeps. A role holds one Echo at a time; a row can see
// more only across a loadout change, and more than a few would not name anything anyway.
const maxEchoVisionsPerSkill = 4

type skillAggregate struct {
	skills     map[uint64]*SkillDamageSnapshot
	unassigned UnassignedSkillTotals
}

func (a *skillAggregate) add(skillID, damageID uint64, amount int64, echoVisions []uint64) {
	if skillID == 0 {
		a.unassigned.DamageDealt += amount
		a.unassigned.OutgoingEvents++
		a.unassigned.MissingSkillEvents++
		a.unassigned.DamageIDs, a.unassigned.DamageIDsIncomplete = insertDamageID(a.unassigned.DamageIDs, a.unassigned.DamageIDsIncomplete, damageID)
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
	entry.recordDamageID(damageID)
	for _, vision := range echoVisions {
		entry.recordEchoVision(vision)
	}
}

// recordEchoVision keeps the row's distinct Echo identities sorted and bounded.
func (entry *SkillDamageSnapshot) recordEchoVision(vision uint64) {
	index := sort.Search(len(entry.EchoVisions), func(i int) bool { return entry.EchoVisions[i] >= vision })
	if index < len(entry.EchoVisions) && entry.EchoVisions[index] == vision {
		return
	}
	if len(entry.EchoVisions) >= maxEchoVisionsPerSkill {
		return
	}
	entry.EchoVisions = append(entry.EchoVisions, 0)
	copy(entry.EchoVisions[index+1:], entry.EchoVisions[index:])
	entry.EchoVisions[index] = vision
}

// recordDamageID keeps the row's distinct DamageIds sorted and bounded.
func (entry *SkillDamageSnapshot) recordDamageID(damageID uint64) {
	entry.DamageIDs, entry.DamageIDsIncomplete = insertDamageID(entry.DamageIDs, entry.DamageIDsIncomplete, damageID)
}

// insertDamageID adds a DamageId to a sorted bounded list. A hit without one, or one past the bound, marks the list
// incomplete.
func insertDamageID(ids []uint64, incomplete bool, damageID uint64) ([]uint64, bool) {
	if damageID == 0 {
		return ids, true
	}
	index := sort.Search(len(ids), func(i int) bool { return ids[i] >= damageID })
	if index < len(ids) && ids[index] == damageID {
		return ids, incomplete
	}
	if len(ids) >= maxDamageIDsPerSkill {
		return ids, true
	}
	ids = append(ids, 0)
	copy(ids[index+1:], ids[index:])
	ids[index] = damageID
	return ids, incomplete
}

// snapshot returns an independent copy ordered by damage, highest first; equal damage orders by SkillId so the order
// is reproducible.
func (a *skillAggregate) snapshot() ([]SkillDamageSnapshot, UnassignedSkillTotals) {
	skills := make([]SkillDamageSnapshot, 0, len(a.skills))
	for _, entry := range a.skills {
		copied := *entry
		copied.DamageIDs = append([]uint64(nil), entry.DamageIDs...)
		copied.EchoVisions = append([]uint64(nil), entry.EchoVisions...)
		skills = append(skills, copied)
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].DamageDealt != skills[j].DamageDealt {
			return skills[i].DamageDealt > skills[j].DamageDealt
		}
		return skills[i].SkillID < skills[j].SkillID
	})
	unassigned := a.unassigned
	unassigned.DamageIDs = append([]uint64(nil), a.unassigned.DamageIDs...)
	return skills, unassigned
}
