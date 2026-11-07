package capture

import (
	"database/sql"
	"fmt"
	"sort"
)

// Display names of a contributor's skill rows (one row per DamageContext.SkillId).
//
// The official name of a runtime skill is its own SkillDescription row: SkillDescription.Id is the runtime SkillId and
// SkillLevelGroupId names the UI skill (Skill.Id) whose text it shows (loadSQLiteSkills). Runtime skills that live only
// in SkillInfo DataTables have no SkillDescription row and therefore no official name of their own. Two static Master
// relations still identify, without inventing text, the player-visible record such a row belongs to:
//
//   - damage_list: Skill.DamageList (skill_damage_ref) lists the DamageIds that belong to a UI skill. A row is named by
//     UI skill U when every hit of the row carried a DamageId, every one of those DamageIds is listed by exactly one UI
//     skill and that skill is U for all of them, and U's skill group belongs to the row's runtime role. Requests carry
//     DamageContext.SkillId and DamageId together, so the row's DamageIds are protocol facts, not timing guesses.
//   - echo: Vision.SkillId or Vision.BirthCastSkillId equals the SkillId, and Vision.Id is the PhantomItem.SkillId key
//     space. A row is named by the Echo when every PhantomItem of every such Vision carries one official name per locale.
//
// Both are DERIVED: the text is an official name of a related record, never the runtime skill's own name, so the UI
// marks them. A row whose hits split across UI skills, carry an unlisted DamageId, or belong to another role keeps no
// name. Names never change a row, its totals, its owner, or the order of rows.

const (
	skillNameSourceOfficial   = "official"
	skillNameSourceDamageList = "damage_list"
	skillNameSourceEcho       = "echo"
	// skillNameStatusDerived marks a name taken from a related record (see the file comment).
	skillNameStatusDerived = "DERIVED"
)

// skillNameMaster holds the static relations behind derived skill-row names.
type skillNameMaster struct {
	// damageListSkills maps a DamageId to the only UI skill (Skill.Id) whose DamageList names it. DamageIds that more
	// than one UI skill lists are left out, so no row through them can be named.
	damageListSkills map[uint64]uint64
	// uiSkillRoles maps a UI skill to the roles of its skill group (skill.skill_group_id -> role_skill_group_ref).
	uiSkillRoles map[uint64]map[uint64]struct{}
	// uiSkillNames holds the official UI skill names of the master locale (skill_locale, en-US fallback).
	uiSkillNames map[uint64]string
	// echoItems maps a Vision skill key to one PhantomItem.Id whose official name every candidate Echo shares.
	echoItems map[uint64]uint64
	// echoNames holds the official PhantomItem names of the master locale (phantomitem_locale, en-US fallback).
	echoNames map[uint64]string
}

func newSkillNameMaster() *skillNameMaster {
	return &skillNameMaster{damageListSkills: map[uint64]uint64{}, uiSkillRoles: map[uint64]map[uint64]struct{}{},
		uiSkillNames: map[uint64]string{}, echoItems: map[uint64]uint64{}, echoNames: map[uint64]string{}}
}

// skillRowName names one skill row. roleID is zero for a vehicle, which has no skill group; damage_list never applies
// to it. status is VERIFIED for an official name, DERIVED for a related record's name, UNKNOWN otherwise.
func (m *gameMaster) skillRowName(roleID uint64, skill SkillDamageSnapshot) (name, status, source string, sourceID uint64) {
	if m == nil {
		return "", string(resolutionUnknown), "", 0
	}
	if record, ok := m.damageSkill(skill.SkillID); ok && record.Name != "" && record.NameStatus == string(resolutionVerified) {
		return record.Name, record.NameStatus, skillNameSourceOfficial, 0
	}
	names := m.skillNames
	if names == nil {
		return "", string(resolutionUnknown), "", 0
	}
	if uiSkill, ok := names.damageListSkill(roleID, skill); ok {
		if text := names.uiSkillNames[uiSkill]; text != "" {
			return text, skillNameStatusDerived, skillNameSourceDamageList, uiSkill
		}
	}
	if item, ok := names.echoItems[skill.SkillID]; ok {
		if text := names.echoNames[item]; text != "" {
			return text, skillNameStatusDerived, skillNameSourceEcho, item
		}
	}
	return "", string(resolutionUnknown), "", 0
}

// damageListSkill returns the single UI skill that lists every DamageId of the row, when it belongs to roleID.
func (m *skillNameMaster) damageListSkill(roleID uint64, skill SkillDamageSnapshot) (uint64, bool) {
	if roleID == 0 || skill.DamageIDsIncomplete || len(skill.DamageIDs) == 0 {
		return 0, false
	}
	var uiSkill uint64
	for _, damageID := range skill.DamageIDs {
		listed, ok := m.damageListSkills[damageID]
		if !ok || (uiSkill != 0 && listed != uiSkill) {
			return 0, false
		}
		uiSkill = listed
	}
	if _, ok := m.uiSkillRoles[uiSkill][roleID]; !ok {
		return 0, false
	}
	return uiSkill, true
}

// loadSQLiteSkillNameSources reads the relations of the file comment. Every table is optional; a Master without them
// names nothing beyond the official SkillDescription rows.
func loadSQLiteSkillNameSources(db *sql.DB, locale string, master *gameMaster) error {
	names := newSkillNameMaster()
	master.skillNames = names
	exists := func(tables ...string) (bool, error) {
		for _, table := range tables {
			ok, err := masterTableExists(db, table)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	pairs := func(query string, read func(a, b uint64) error, args ...any) error {
		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("query skill name sources: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rawA, rawB string
			if err := rows.Scan(&rawA, &rawB); err != nil {
				return fmt.Errorf("read skill name sources: %w", err)
			}
			a, err := parseMasterID(rawA, "skill name source")
			if err != nil {
				return err
			}
			b, err := parseMasterID(rawB, "skill name source")
			if err != nil {
				return err
			}
			if err := read(a, b); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if ok, err := exists("skill_damage_ref", "skill", "role_skill_group_ref", "skill_locale"); err != nil {
		return err
	} else if ok {
		listedBy := map[uint64]map[uint64]struct{}{}
		if err := pairs(`SELECT CAST(damage_id AS TEXT),CAST(skill_id AS TEXT) FROM skill_damage_ref`, func(damageID, uiSkill uint64) error {
			if listedBy[damageID] == nil {
				listedBy[damageID] = map[uint64]struct{}{}
			}
			listedBy[damageID][uiSkill] = struct{}{}
			return nil
		}); err != nil {
			return err
		}
		for damageID, skills := range listedBy {
			if len(skills) == 1 {
				for uiSkill := range skills {
					names.damageListSkills[damageID] = uiSkill
				}
			}
		}
		if err := pairs(`SELECT CAST(s.id AS TEXT),CAST(r.role_id AS TEXT) FROM skill s
			JOIN role_skill_group_ref r ON CAST(r.skill_group_id AS TEXT)=CAST(s.skill_group_id AS TEXT)`, func(uiSkill, roleID uint64) error {
			if names.uiSkillRoles[uiSkill] == nil {
				names.uiSkillRoles[uiSkill] = map[uint64]struct{}{}
			}
			names.uiSkillRoles[uiSkill][roleID] = struct{}{}
			return nil
		}); err != nil {
			return err
		}
		if err := loadLocalizedNames(db, "skill_locale", locale, names.uiSkillNames); err != nil {
			return err
		}
	}
	if ok, err := exists("vision", "phantomitem", "phantomitem_locale"); err != nil {
		return err
	} else if ok {
		if err := loadSQLiteEchoSkillItems(db, names); err != nil {
			return err
		}
		if err := loadLocalizedNames(db, "phantomitem_locale", locale, names.echoNames); err != nil {
			return err
		}
	}
	return nil
}

// loadSQLiteEchoSkillItems keeps a Vision skill key only when every PhantomItem reachable from it has the same official
// name in every locale; the representative item is the smallest PhantomItem.Id that carries all of those names.
func loadSQLiteEchoSkillItems(db *sql.DB, names *skillNameMaster) error {
	hasCast, err := masterColumnExists(db, "vision", "birth_cast_skill_id")
	if err != nil {
		return err
	}
	keys := `SELECT CAST(id AS TEXT) AS vision_id, CAST(skill_id AS TEXT) AS skill_key FROM vision WHERE skill_id IS NOT NULL AND CAST(skill_id AS TEXT)<>''`
	if hasCast {
		keys += ` UNION SELECT CAST(id AS TEXT), CAST(birth_cast_skill_id AS TEXT) FROM vision WHERE birth_cast_skill_id IS NOT NULL AND CAST(birth_cast_skill_id AS TEXT)<>''`
	}
	rows, err := db.Query(`SELECT k.skill_key, CAST(i.id AS TEXT), l.locale, l.name FROM (` + keys + `) k
		JOIN phantomitem i ON CAST(i.skill_id AS TEXT)=k.vision_id
		JOIN phantomitem_locale l ON CAST(l.id AS TEXT)=CAST(i.id AS TEXT) AND l.name<>''`)
	if err != nil {
		return fmt.Errorf("query echo skill names: %w", err)
	}
	defer rows.Close()
	type candidate struct {
		names map[string]map[string]struct{}
		items map[uint64]map[string]struct{}
	}
	candidates := map[uint64]*candidate{}
	for rows.Next() {
		var rawSkill, rawItem, locale, name string
		if err := rows.Scan(&rawSkill, &rawItem, &locale, &name); err != nil {
			return fmt.Errorf("read echo skill names: %w", err)
		}
		skill, err := parseMasterID(rawSkill, "vision skill key")
		if err != nil {
			return err
		}
		item, err := parseMasterID(rawItem, "phantomitem.id")
		if err != nil {
			return err
		}
		entry := candidates[skill]
		if entry == nil {
			entry = &candidate{names: map[string]map[string]struct{}{}, items: map[uint64]map[string]struct{}{}}
			candidates[skill] = entry
		}
		if entry.names[locale] == nil {
			entry.names[locale] = map[string]struct{}{}
		}
		entry.names[locale][name] = struct{}{}
		if entry.items[item] == nil {
			entry.items[item] = map[string]struct{}{}
		}
		entry.items[item][locale] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for skill, entry := range candidates {
		unique := true
		for _, set := range entry.names {
			if len(set) != 1 {
				unique = false
				break
			}
		}
		if !unique {
			continue
		}
		items := make([]uint64, 0, len(entry.items))
		for item, locales := range entry.items {
			if len(locales) == len(entry.names) {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
		names.echoItems[skill] = items[0]
	}
	return nil
}

// loadLocalizedNames fills id -> name from a *_locale table for locale, using en-US where locale has no name.
func loadLocalizedNames(db *sql.DB, table, locale string, into map[uint64]string) error {
	for _, candidate := range []string{"en-US", locale} {
		if err := func() error {
			rows, err := db.Query(`SELECT CAST(id AS TEXT),name FROM `+table+` WHERE locale=? AND name<>''`, candidate)
			if err != nil {
				return fmt.Errorf("query %s: %w", table, err)
			}
			defer rows.Close()
			for rows.Next() {
				var raw, name string
				if err := rows.Scan(&raw, &name); err != nil {
					return fmt.Errorf("read %s: %w", table, err)
				}
				id, err := parseMasterID(raw, table+".id")
				if err != nil {
					return err
				}
				into[id] = name
			}
			return rows.Err()
		}(); err != nil {
			return err
		}
	}
	return nil
}
