package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// maxDamageOwnerRows bounds how many owners one damage event carries; ownerCount keeps the full number.
const maxDamageOwnerRows = 5

// Materialize the text keys once so SQLite can index the join without changing
// how IDs from different SQLite storage types compare.
const damageOwnerIDs = `WITH damage_ids(id) AS MATERIALIZED (SELECT CAST(id AS TEXT) FROM damage) `

// damageOwnerJSONL is one static owner of a DamageId: a skill that lists the DamageId in its damage references, or a
// Buff whose extra-effect parameters name it. The owner is a master-table reference, not the runtime attacker.
type damageOwnerJSONL struct {
	Kind     string `json:"kind"`
	ID       uint64 `json:"id"`
	RoleID   uint64 `json:"roleId,omitempty"`
	RoleName string `json:"roleName,omitempty"`
}

// damageOwnersJSONL is what a damage event carries for a DamageId that has at least one static owner.
type damageOwnersJSONL struct {
	Status     resolutionStatus   `json:"status"`
	OwnerCount int                `json:"ownerCount"`
	Owners     []damageOwnerJSONL `json:"owners"`
	Evidence   string             `json:"evidence"`
}

// loadSQLiteDamageOwners indexes, for every DamageId, the skills (skill_damage_ref) and Buffs (buff_extra_effect_parameters)
// that reference it, with the role that owns the skill group of each skill. Both tables are optional.
func loadSQLiteDamageOwners(db *sql.DB, locale string, master *gameMaster) error {
	exists, err := masterTableExists(db, "skill_damage_ref")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query(damageOwnerIDs + `SELECT CAST(d.damage_id AS TEXT),CAST(d.skill_id AS TEXT),COALESCE(CAST(r.id AS TEXT),'') FROM skill_damage_ref d
		JOIN damage_ids x ON x.id=CAST(d.damage_id AS TEXT)
		LEFT JOIN skill s ON CAST(s.id AS TEXT)=CAST(d.skill_id AS TEXT)
		LEFT JOIN roleinfo r ON CAST(r.skill_id AS TEXT)=CAST(s.skill_group_id AS TEXT)`)
	if err != nil {
		return fmt.Errorf("query skill damage owners: %w", err)
	}
	for rows.Next() {
		var damageRaw, skillRaw, roleRaw string
		if err := rows.Scan(&damageRaw, &skillRaw, &roleRaw); err != nil {
			rows.Close()
			return fmt.Errorf("read skill damage owner: %w", err)
		}
		damageID, err := parseMasterID(damageRaw, "skill_damage_ref.damage_id")
		if err != nil {
			rows.Close()
			return err
		}
		skillID, err := parseMasterID(skillRaw, "skill_damage_ref.skill_id")
		if err != nil {
			rows.Close()
			return err
		}
		owner := damageOwnerJSONL{Kind: "skill", ID: skillID}
		if roleRaw != "" {
			if roleID, err := parseMasterID(roleRaw, "roleinfo.id"); err == nil {
				owner.RoleID = roleID
				if record, ok := master.characters[roleID]; ok {
					owner.RoleName = record.Name
				}
			}
		}
		master.damageOwners[damageID] = append(master.damageOwners[damageID], owner)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	buffExists, err := masterTableExists(db, "buff_extra_effect_parameters")
	if err != nil || !buffExists {
		return err
	}
	buffRows, err := db.Query(damageOwnerIDs + `SELECT DISTINCT CAST(p.value AS TEXT),CAST(p.owner_id AS TEXT) FROM buff_extra_effect_parameters p
		JOIN damage_ids x ON x.id=CAST(p.value AS TEXT)`)
	if err != nil {
		return fmt.Errorf("query buff damage owners: %w", err)
	}
	defer buffRows.Close()
	for buffRows.Next() {
		var damageRaw, buffRaw string
		if err := buffRows.Scan(&damageRaw, &buffRaw); err != nil {
			return fmt.Errorf("read buff damage owner: %w", err)
		}
		damageID, err := parseMasterID(damageRaw, "buff_extra_effect_parameters.value")
		if err != nil {
			return err
		}
		buffID, err := parseMasterID(buffRaw, "buff_extra_effect_parameters.owner_id")
		if err != nil {
			return err
		}
		master.damageOwners[damageID] = append(master.damageOwners[damageID], damageOwnerJSONL{Kind: "buff", ID: buffID})
	}
	if err := buffRows.Err(); err != nil {
		return err
	}
	for _, list := range master.damageOwners {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Kind != list[j].Kind {
				return list[i].Kind < list[j].Kind
			}
			return list[i].ID < list[j].ID
		})
	}
	return nil
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
