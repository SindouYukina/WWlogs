package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// Materialize the text keys once so SQLite can index the join without changing
// how IDs from different SQLite storage types compare.
const damageOwnerIDs = `WITH damage_ids(id) AS MATERIALIZED (SELECT CAST(id AS TEXT) FROM damage) `

// DamageOwnerJSONL is one static owner of a DamageId: a skill that lists the DamageId in its damage references, or a
// Buff whose extra-effect parameters name it. The owner is a master-table reference, not the runtime attacker.
type DamageOwnerJSONL struct {
	Kind     string `json:"kind"`
	ID       uint64 `json:"id"`
	RoleID   uint64 `json:"roleId,omitempty"`
	RoleName string `json:"roleName,omitempty"`
}

// loadSQLiteDamageOwners indexes, for every DamageId, the skills (skill_damage_ref) and Buffs (buff_extra_effect_parameters)
// that reference it, with the role that owns the skill group of each skill. Both tables are optional.
func loadSQLiteDamageOwners(db *sql.DB, locale string, master *GameMaster) error {
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
		owner := DamageOwnerJSONL{Kind: "skill", ID: skillID}
		if roleRaw != "" {
			if roleID, err := parseMasterID(roleRaw, "roleinfo.id"); err == nil {
				owner.RoleID = roleID
				if record, ok := master.Characters[roleID]; ok {
					owner.RoleName = record.Name
				}
			}
		}
		master.DamageOwners[damageID] = append(master.DamageOwners[damageID], owner)
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
		master.DamageOwners[damageID] = append(master.DamageOwners[damageID], DamageOwnerJSONL{Kind: "buff", ID: buffID})
	}
	if err := buffRows.Err(); err != nil {
		return err
	}
	for _, list := range master.DamageOwners {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Kind != list[j].Kind {
				return list[i].Kind < list[j].Kind
			}
			return list[i].ID < list[j].ID
		})
	}
	return nil
}
