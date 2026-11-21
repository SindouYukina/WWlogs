package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// DamageOwnerJSONL is one static owner of a DamageId: a skill that lists the DamageId in its damage references, or a
// Buff whose extra-effect parameters name it. The owner is a master-table reference, not the runtime attacker.
type DamageOwnerJSONL struct {
	Kind     string `json:"kind"`
	ID       uint64 `json:"id"`
	RoleID   uint64 `json:"roleId,omitempty"`
	RoleName string `json:"roleName,omitempty"`
}

// loadSQLiteDamageOwners indexes, for every DamageId, the skills (skill_damage_ref) and Buffs (buff_extra_effect_parameters)
// that reference it, with the role that owns the skill group of each skill. The runtime schema declares every key these
// joins compare as TEXT, so they compare the columns themselves and SQLite searches each key; a CAST would hide the key
// and compare every row.
//
// The roles of one skill group keep roleinfo's storage order (r.rowid, the client's order): the sort below orders owners
// by kind and id only, so that order decides which roles of a shared skill group the Development damageOwners output
// shows first. ORDER BY makes it independent of the query plan; the runtime schema requires roleinfo to be a rowid table.
func loadSQLiteDamageOwners(db *sql.DB, locale string, master *GameMaster) error {
	rows, err := db.Query(`SELECT d.damage_id,d.skill_id,COALESCE(r.id,'') FROM skill_damage_ref d
		JOIN damage x ON x.id=d.damage_id
		LEFT JOIN skill s ON s.id=d.skill_id
		LEFT JOIN roleinfo r ON r.skill_id=s.skill_group_id
		ORDER BY d.skill_id,r.rowid`)
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
	buffRows, err := db.Query(`SELECT DISTINCT p.value,p.owner_id FROM buff_extra_effect_parameters p
		JOIN damage x ON x.id=p.value`)
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
