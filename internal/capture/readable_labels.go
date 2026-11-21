package capture

import (
	"database/sql"
	"fmt"
	"strconv"
)

// LocalizedSkillLabel is one row of skill_dev_label_locale for the master locale (or the zh-CN original).
type LocalizedSkillLabel struct {
	Text   string
	Locale string
	Kind   string
}

// loadSQLiteSkillLabelText reads the skill_dev_label_locale table. Only the requested locale and the zh-CN original are
// kept.
func loadSQLiteSkillLabelText(db *sql.DB, locale string, master *GameMaster) error {
	rows, err := db.Query("SELECT skill_id,locale,label,label_kind FROM skill_dev_label_locale WHERE locale IN (?,?)", locale, "zh-CN")
	if err != nil {
		return fmt.Errorf("query skill developer label text: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var label LocalizedSkillLabel
		if err := rows.Scan(&raw, &label.Locale, &label.Text, &label.Kind); err != nil {
			return fmt.Errorf("read skill developer label text: %w", err)
		}
		id, err := parseMasterID(raw, "skill_dev_label_locale.skill_id")
		if err != nil {
			return err
		}
		byLocale := master.skillLabelText[id]
		if byLocale == nil {
			byLocale = make(map[string]LocalizedSkillLabel)
			master.skillLabelText[id] = byLocale
		}
		byLocale[label.Locale] = label
	}
	return rows.Err()
}

// queryDevLabels reads the dev_label_locale rows of one ID namespace for the master locale and the zh-CN original.
func queryDevLabels(db *sql.DB, namespace, locale string) (*sql.Rows, error) {
	rows, err := db.Query("SELECT id,locale,label,label_kind FROM dev_label_locale WHERE namespace=? AND locale IN (?,?)", namespace, locale, "zh-CN")
	if err != nil {
		return nil, fmt.Errorf("query %s developer labels: %w", namespace, err)
	}
	return rows, nil
}

// loadSQLiteVisionDevLabels reads the developer note text of every Vision row, so any summon that points at a Vision
// row is readable and not only the rows seen in a capture.
func loadSQLiteVisionDevLabels(db *sql.DB, locale string, master *GameMaster) error {
	return loadSQLiteDevLabelMap(db, "vision", locale, master.VisionDevLabels)
}

// loadSQLiteDevLabelMap fills target with the dev_label_locale text of one ID namespace. The master locale wins over
// the zh-CN original; the text is a developer note and never an official name.
func loadSQLiteDevLabelMap(db *sql.DB, namespace, locale string, target map[uint64]*CombatEventLabelTextJSONL) error {
	rows, err := queryDevLabels(db, namespace, locale)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		label := &CombatEventLabelTextJSONL{Evidence: "dev_label_locale row (" + namespace + ") for this id; an internal developer note, not an official in-game name"}
		if err := rows.Scan(&raw, &label.Locale, &label.Text, &label.Kind); err != nil {
			return fmt.Errorf("read %s developer label: %w", namespace, err)
		}
		id, err := parseMasterID(raw, "dev_label_locale.id")
		if err != nil {
			return err
		}
		if previous, ok := target[id]; !ok || label.Locale == locale && previous.Locale != locale {
			target[id] = label
		}
	}
	return rows.Err()
}

// loadSQLiteElementNames reads the localized element names (ElementInfo) for the master locale.
func loadSQLiteElementNames(db *sql.DB, locale string, master *GameMaster) error {
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
		master.ElementNames[int32(id)] = name
	}
	return rows.Err()
}

// ResolveSkillLabelText returns the display text of the developer label for a skill that has an
// unambiguous SkillInfo label. The master locale text wins; the zh-CN original is the fallback.
// The text is never an official name: Kind says whether it is the original note or an unofficial translation.
func ResolveSkillLabelText(masters *GameMaster, skillID uint64) *CombatEventLabelTextJSONL {
	if masters == nil {
		return nil
	}
	label := ResolveSkillLabel(masters, skillID)
	if label == nil || label.Status != ResolutionCorrelated {
		return nil
	}
	byLocale := masters.skillLabelText[skillID]
	chosen, ok := byLocale[masters.Locale]
	if !ok {
		chosen, ok = byLocale["zh-CN"]
	}
	if !ok || chosen.Text == "" {
		return nil
	}
	if chosen.Kind == "developer-original" && chosen.Text != label.Label {
		return nil
	}
	return &CombatEventLabelTextJSONL{Locale: chosen.Locale, Text: chosen.Text, Kind: chosen.Kind,
		Evidence: "skill_dev_label_locale row for this SkillInfo skill id; an internal developer note, not an official in-game name"}
}
