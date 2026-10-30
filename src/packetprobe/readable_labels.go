package main

import (
	"database/sql"
	"fmt"
	"strconv"
)

// localizedSkillLabel is one row of skill_dev_label_locale for the master locale (or the zh-CN original).
type localizedSkillLabel struct {
	Text   string
	Locale string
	Kind   string
}

// loadSQLiteSkillLabelText reads the optional skill_dev_label_locale table. Only the requested locale and the
// zh-CN original are kept, so an older master without the table still loads.
func loadSQLiteSkillLabelText(db *sql.DB, locale string, master *gameMaster) error {
	exists, err := masterTableExists(db, "skill_dev_label_locale")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT skill_id,locale,label,label_kind FROM skill_dev_label_locale WHERE locale IN (?,?)", locale, "zh-CN")
	if err != nil {
		return fmt.Errorf("query skill developer label text: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var label localizedSkillLabel
		if err := rows.Scan(&raw, &label.Locale, &label.Text, &label.Kind); err != nil {
			return fmt.Errorf("read skill developer label text: %w", err)
		}
		id, err := parseMasterID(raw, "skill_dev_label_locale.skill_id")
		if err != nil {
			return err
		}
		byLocale := master.skillLabelText[id]
		if byLocale == nil {
			byLocale = make(map[string]localizedSkillLabel)
			master.skillLabelText[id] = byLocale
		}
		byLocale[label.Locale] = label
	}
	return rows.Err()
}

// loadSQLiteElementNames reads the localized element names (ElementInfo) for the master locale.
func loadSQLiteElementNames(db *sql.DB, locale string, master *gameMaster) error {
	exists, err := masterTableExists(db, "elementinfo_locale")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query("SELECT id,name FROM elementinfo_locale WHERE locale=? AND name IS NOT NULL AND name<>''", locale)
	if err != nil {
		return fmt.Errorf("query element names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, name string
		if err := rows.Scan(&raw, &name); err != nil {
			return fmt.Errorf("read element name: %w", err)
		}
		// Element 0 (physical) is a valid key, so the non-zero ID parser is not used.
		id, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid elementinfo_locale.id %q: %w", raw, err)
		}
		master.elementNames[int32(id)] = name
	}
	return rows.Err()
}

// resolveSkillLabelText returns the display text of the developer label for a skill that has an
// unambiguous SkillInfo label. The master locale text wins; the zh-CN original is the fallback.
// The text is never an official name: Kind says whether it is the original note or an unofficial translation.
func resolveSkillLabelText(masters *gameMaster, skillID uint64) *combatEventLabelTextJSONL {
	if masters == nil {
		return nil
	}
	label := resolveSkillLabel(masters, skillID)
	if label == nil || label.Status != resolutionCorrelated {
		return nil
	}
	byLocale := masters.skillLabelText[skillID]
	chosen, ok := byLocale[masters.locale]
	if !ok {
		chosen, ok = byLocale["zh-CN"]
	}
	if !ok || chosen.Text == "" {
		return nil
	}
	if chosen.Kind == "developer-original" && chosen.Text != label.Label {
		return nil
	}
	return &combatEventLabelTextJSONL{Locale: chosen.Locale, Text: chosen.Text, Kind: chosen.Kind,
		Evidence: "skill_dev_label_locale row for this SkillInfo skill id; an internal developer note, not an official in-game name"}
}

// resolveDamageElement names the element written in the damage master row. The row is looked up by DamageId,
// the same basis as the damage class; the protocol's ElementType field is not used here.
func resolveDamageElement(masters *gameMaster, damageID uint64, hasDamageID bool) *combatEventElementJSONL {
	if masters == nil || !hasDamageID {
		return nil
	}
	record, ok := masters.damage[damageID]
	if !ok || !record.HasElement {
		return nil
	}
	result := &combatEventElementJSONL{ID: record.Element}
	if name, found := masters.elementNames[record.Element]; found {
		result.Name = name
		result.Status = resolutionCorrelated
		result.Evidence = fmt.Sprintf("damage.element of the DamageId row -> elementinfo_locale (%s); client build observed, master compatibility unverified", masters.locale)
	} else {
		result.Status = resolutionUnknown
		result.Evidence = "damage.element has no elementinfo_locale name for the master locale; raw element id kept"
	}
	return result
}
