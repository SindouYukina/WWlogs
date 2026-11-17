//go:build wwdev

package capture

import ()

// maxDamageOwnerRows bounds how many owners one damage event carries; ownerCount keeps the full number.
const maxDamageOwnerRows = 5

// damageOwnersJSONL is what a damage event carries for a DamageId that has at least one static owner.
type damageOwnersJSONL struct {
	Status     resolutionStatus   `json:"status"`
	OwnerCount int                `json:"ownerCount"`
	Owners     []damageOwnerJSONL `json:"owners"`
	Evidence   string             `json:"evidence"`
}

// resolveDamageOwners returns the static owners of a DamageId, or nil when the master lists none.
func resolveDamageOwners(masters *gameMaster, damageID uint64, hasDamageID bool) *damageOwnersJSONL {
	if masters == nil || !hasDamageID {
		return nil
	}
	list := masters.damageOwners[damageID]
	if len(list) == 0 {
		return nil
	}
	shown := list
	if len(shown) > maxDamageOwnerRows {
		shown = shown[:maxDamageOwnerRows]
	}
	return &damageOwnersJSONL{
		Status:     resolutionCorrelated,
		OwnerCount: len(list),
		Owners:     shown,
		Evidence: "the DamageId is listed by the skill's damage references or by a Buff's extra-effect parameters; the role is the owner of that skill group. " +
			"This is a static master reference and does not prove who dealt the hit at runtime; client build observed, master compatibility unverified",
	}
}
