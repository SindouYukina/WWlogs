package capture

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Display names and icons of a contributor's skill rows (one row per DamageContext.SkillId).
//
// The official name of a runtime skill is its own SkillDescription row: SkillDescription.Id is the runtime SkillId and
// SkillLevelGroupId names the UI skill (Skill.Id) whose text it shows (loadSQLiteSkills). Runtime skills that live only
// in SkillInfo DataTables have no SkillDescription row and therefore no official name of their own. Static Master
// relations still identify, without inventing text, the player-visible record such a row belongs to:
//
//   - damage_list: Skill.DamageList (skill_damage_ref) lists the DamageIds that belong to a UI skill. A row is named by
//     UI skill U when every hit of the row carried a DamageId, every one of those DamageIds is listed by exactly one UI
//     skill and that skill is U for all of them, and U's skill group belongs to the row's runtime role. Requests carry
//     DamageContext.SkillId and DamageId together, so the row's DamageIds are protocol facts, not timing guesses.
//   - skill_type: a row whose hits are listed by several UI skills of its role is named by the one whose Skill.SkillType
//     matches the row's SkillInfo.SkillGenre (skillGenreUITypes), when every other UI skill listing it is the role's Forte
//     Circuit, which also lists hits of other skills.
//   - tune_break: SkillInfo.SkillGenre 14 is the game's weakness-break skill genre, damage.type 11 is its damage, and
//     every role has one UI skill of Skill.SkillType 12 ("Tune Break"). A row is named by that UI skill when every row of
//     the SkillId has the genre, every DamageId of the row has the type, and the role has exactly one such UI skill.
//   - echo: the row reaches a Vision (Vision.Id is the PhantomItem.SkillId key space) by Vision.SkillId or
//     Vision.BirthCastSkillId; or by SkillInfo's developer label "召唤幻象-<Vision note>"; or by being preloaded only on
//     the Vision's own actor blueprint. It is named by the Echo when every PhantomItem of those Visions carries one
//     official name per locale, or when they are one family: the base items of one monster share a name and every other
//     candidate is a variant of that monster (ParentMonsterId). Two different Echoes keep the row unnamed.
//
// All of them are DERIVED: the text is an official name of a related record, never the runtime skill's own name, so the
// UI marks them. A developer label is used only to find a record, never as text. A row whose hits split across UI skills,
// carry an unlisted DamageId, or belong to another role keeps no name. Names never change a row, its totals, its owner,
// or the order of rows.
//
// A hit without a SkillId has no row and keeps its place in the contributor's unassigned bucket: SkillId zero means the client
// created the bullet without a skill context. Buff extra effects 3 (AddBulletTrigger) and 29 (ExecuteAddBulletByStackCount)
// fire bullets on the Buff's instigator with no SkillId (client ExtraEffect code), so no field of the hit names a skill. Such
// hits can still be named, DERIVED, but only by a relation that says what the hit is, tried in this order (unassignedSkillName):
//
//   - echo: every DamageId is one Vision's common bullet, and the attacker held that Vision's ConcomitantVision entity at every
//     one of the hits. The holder check is the runtime proof that the bullet is this role's Echo: the instigator of the Echo's
//     Buff is the bullet's owner, so the wearer is the attacker. A DamageId of a Vision the attacker does not hold stays
//     unnamed rather than credited to an Echo nothing ties to this role.
//   - damage_type: every DamageId is damage.type 7, the game's Outro Skill DMG (client DamageType requirement 7, whose official
//     Buff texts say Outro Skill DMG); every one is listed by a UI skill of the role, which proves the damage row is the role's
//     own; and the role has exactly one Outro UI skill (Skill.SkillType 11). The listing names the owner, not the skill: a role's
//     Outro damage can sit in another UI skill's list, which is why damage_list below refuses such a list.
//   - damage_list: as for a row, every DamageId is listed by exactly one UI skill of the role, except that a list holding Outro
//     damage under a UI skill of another type never names it.
//
// The hit stays no-SkillId in every total; the name is display evidence only, and it never enters the skill rows or their
// order. A SkillId is never invented.
//
// A row's icon is resolved separately from its name, because either can exist without the other: the name and the image
// of an official row come from different records, and a Vision's HUD icon is keyed by the Vision, not by the PhantomItem
// that names it.

const (
	skillNameSourceOfficial   = "official"
	skillNameSourceDamageList = "damage_list"
	skillNameSourceSkillType  = "skill_type"
	skillNameSourceTuneBreak  = "tune_break"
	skillNameSourceEcho       = "echo"
	skillNameSourceDamageType = "damage_type"
	// SkillNameStatusDerived marks a name taken from a related record (see the file comment).
	SkillNameStatusDerived = "DERIVED"
)

// Asset kinds a skill row may request. They are the host's route names (internal/desktop/preview.go).
const (
	skillIconKindSkill       = "skill"        // SkillDescription.Id
	skillIconKindSkillConfig = "skill-config" // Skill.Id
	skillIconKindEchoSkill   = "echo-skill"   // Vision.Id
)

const (
	// skillGenreBreak is SkillInfo.SkillGenre for a weakness-break skill.
	skillGenreBreak = 14
	// damageTypeBreak is damage.type for the hits of a weakness-break skill.
	damageTypeBreak = 11
	// uiSkillTypeBreak is Skill.SkillType for the "Tune Break" UI skill of a role.
	uiSkillTypeBreak = 12
	// uiSkillTypeForte is Skill.SkillType for a role's Forte Circuit, whose DamageList also holds hits of other skills.
	uiSkillTypeForte = 6
	// uiSkillTypeOutro is Skill.SkillType for a role's Outro skill.
	uiSkillTypeOutro = 11
	// damageTypeOutro is damage.type for Outro Skill DMG.
	damageTypeOutro = 7
	// echoSummonLabelPrefix starts the developer label of the common skills that summon one Echo.
	echoSummonLabelPrefix = "召唤幻象-"
	// visionActorPathSegment marks a Vision's own actor blueprint.
	visionActorPathSegment = "/Character/Vision/"
	// visionBulletNameSeparator ends the Vision part of a common bullet's developer name.
	visionBulletNameSeparator = "-"
)

// unnamedReason values say why a bucket of hits without a SkillId has no name. They are diagnostics, never shown as a name.
const (
	unnamedReasonOverflow             = "overflow"               // some hits had a SkillId past the row bound
	unnamedReasonDamageIDMissing      = "damage_id_missing"      // a hit carried no DamageId, or more distinct ones than a row keeps
	unnamedReasonEchoNotHeld          = "echo_not_held"          // the hits are one Vision's bullets but the attacker did not hold that Vision at every hit
	unnamedReasonMixedSources         = "mixed_sources"          // the DamageIds point at different records
	unnamedReasonOutroListedElsewhere = "outro_listed_elsewhere" // Outro damage listed by a UI skill of another type, and no Outro skill to name instead
	unnamedReasonNoRelation           = "no_relation"            // nothing relates the DamageIds to this role
)

// skillGenreUITypes maps SkillInfo.SkillGenre to the Skill.SkillType of the UI skill that describes it. The genres are the
// game's own: the developer labels of the genres read basic attack (0), heavy attack (1), resonance skill (2), liberation
// (3), intro (4), dodge counter (5) and outro (13); skilltype_locale names types 1, 2, 3, 5 and 11 the same way. Other
// genres have no UI skill type of their own and stay out of this rule.
var skillGenreUITypes = map[int64]int64{0: 1, 1: 1, 2: 2, 3: 3, 4: 5, 5: 1, 13: 11}

// skillNameMaster holds the static relations behind derived skill-row names.
type skillNameMaster struct {
	// damageListSkills maps a DamageId to the only UI skill (Skill.Id) whose DamageList names it. DamageIds that more
	// than one UI skill lists are left out, so no row through them can be named.
	damageListSkills map[uint64]uint64
	// uiSkillRoles maps a UI skill to the roles of its skill group (skill.skill_group_id -> role_skill_group_ref).
	uiSkillRoles map[uint64]map[uint64]struct{}
	// uiSkillTypes maps a UI skill to its Skill.SkillType.
	uiSkillTypes map[uint64]int64
	// skillGenres maps a SkillId to its SkillInfo.SkillGenre, for SkillIds whose every SkillInfo row has the same one.
	skillGenres map[uint64]int64
	// uiSkillNames holds the official UI skill names of the master locale (skill_locale, en-US fallback).
	uiSkillNames map[uint64]string
	// tuneBreakSkills holds SkillIds whose every SkillInfo row has the weakness-break genre.
	tuneBreakSkills map[uint64]struct{}
	// tuneBreakDamage holds DamageIds of damage.type 11.
	tuneBreakDamage map[uint64]struct{}
	// roleTuneBreak maps a role to its Tune Break UI skill; a role with more than one is left out.
	roleTuneBreak map[uint64]uint64
	// outroDamage holds DamageIds of damage.type 7, the game's Outro Skill DMG.
	outroDamage map[uint64]struct{}
	// roleOutro maps a role to its Outro UI skill (Skill.SkillType 11); a role with more than one is left out.
	roleOutro map[uint64]uint64
	// echoItems maps a Vision skill key to one PhantomItem.Id whose official name every candidate Echo shares.
	echoItems map[uint64]uint64
	// echoVisions maps a skill key to the Vision.Id of the PhantomItem in echoItems. The game draws an Echo's button from the
	// PhantomSkill of its item's skill, so that one Vision holds the row's icon even when several Visions cast the skill.
	echoVisions map[uint64]uint64
	// damageVisions maps a DamageId to the one Vision its common bullets name.
	damageVisions map[uint64]uint64
	// visionItems maps a Vision.Id to the PhantomItem.Id that names it.
	visionItems map[uint64]uint64
	// itemVisions maps a PhantomItem.Id to its Vision.Id.
	itemVisions map[uint64]uint64
	// ambiguousEchoVisions maps a skill key that several Visions cast, under different Echo names, to those Visions. Only the
	// runtime Echo of the casting role can tell them apart.
	ambiguousEchoVisions map[uint64]map[uint64]struct{}
	// echoNames holds the official PhantomItem names of the master locale (phantomitem_locale, en-US fallback).
	echoNames map[uint64]string
}

func newSkillNameMaster() *skillNameMaster {
	return &skillNameMaster{damageListSkills: map[uint64]uint64{}, uiSkillRoles: map[uint64]map[uint64]struct{}{},
		uiSkillNames: map[uint64]string{}, tuneBreakSkills: map[uint64]struct{}{}, tuneBreakDamage: map[uint64]struct{}{},
		roleTuneBreak: map[uint64]uint64{}, outroDamage: map[uint64]struct{}{}, roleOutro: map[uint64]uint64{}, echoItems: map[uint64]uint64{}, echoVisions: map[uint64]uint64{},
		damageVisions: map[uint64]uint64{}, visionItems: map[uint64]uint64{}, itemVisions: map[uint64]uint64{}, echoNames: map[uint64]string{},
		uiSkillTypes: map[uint64]int64{}, skillGenres: map[uint64]int64{}, ambiguousEchoVisions: map[uint64]map[uint64]struct{}{}}
}

// SkillRowName names one skill row. roleID is zero for a vehicle, which has no skill group; damage_list and tune_break
// never apply to it. status is VERIFIED for an official name, DERIVED for a related record's name, UNKNOWN otherwise.
func (m *GameMaster) SkillRowName(roleID uint64, skill SkillDamageSnapshot) (name, status, source string, sourceID uint64) {
	if m == nil {
		return "", string(ResolutionUnknown), "", 0
	}
	if record, ok := m.DamageSkill(skill.SkillID); ok && record.Name != "" && record.NameStatus == string(ResolutionVerified) {
		return record.Name, record.NameStatus, skillNameSourceOfficial, 0
	}
	names := m.skillNames
	if names == nil {
		return "", string(ResolutionUnknown), "", 0
	}
	if uiSkill, ok := names.damageListSkill(roleID, skill); ok {
		if text := names.uiSkillNames[uiSkill]; text != "" {
			return text, SkillNameStatusDerived, skillNameSourceDamageList, uiSkill
		}
	}
	if uiSkill, ok := names.skillTypeSkill(roleID, skill); ok {
		if text := names.uiSkillNames[uiSkill]; text != "" {
			return text, SkillNameStatusDerived, skillNameSourceSkillType, uiSkill
		}
	}
	if uiSkill, ok := names.tuneBreakSkill(roleID, skill); ok {
		if text := names.uiSkillNames[uiSkill]; text != "" {
			return text, SkillNameStatusDerived, skillNameSourceTuneBreak, uiSkill
		}
	}
	if item, ok := names.echoItems[skill.SkillID]; ok {
		if text := names.echoNames[item]; text != "" {
			return text, SkillNameStatusDerived, skillNameSourceEcho, item
		}
	}
	if item, ok := names.runtimeEchoItem(skill); ok {
		if text := names.echoNames[item]; text != "" {
			return text, SkillNameStatusDerived, skillNameSourceEcho, item
		}
	}
	return "", string(ResolutionUnknown), "", 0
}

// skillRowIcon returns the asset a row should show. An official row and an unnamed row ask by SkillId; a row named through
// a related record shows that record's image. A row may have a name and no icon, and the other way round: the host answers
// 404 for an ID it has no image for, and the row then draws none.
func (m *GameMaster) skillRowIcon(skill SkillDamageSnapshot, source string, sourceID uint64) (kind string, id uint64) {
	switch source {
	case skillNameSourceDamageList, skillNameSourceSkillType, skillNameSourceTuneBreak:
		return skillIconKindSkillConfig, sourceID
	case skillNameSourceEcho:
		if m != nil && m.skillNames != nil {
			if vision, ok := m.skillNames.echoVisions[skill.SkillID]; ok {
				return skillIconKindEchoSkill, vision
			}
			if vision, ok := m.skillNames.itemVisions[sourceID]; ok {
				return skillIconKindEchoSkill, vision
			}
		}
		return "", 0
	}
	return skillIconKindSkill, skill.SkillID
}

// unassignedSkillSource is the record that names a bucket of hits without a SkillId.
type unassignedSkillSource struct {
	name     string
	source   string
	sourceID uint64
	iconKind string
	iconID   uint64
}

// unassignedSkillName decides whether a contributor's hits without a SkillId have a reliable source (see the file comment).
// roleID is zero for a vehicle. When there is no name it returns the reason, which is empty only for an empty bucket.
func (m *GameMaster) unassignedSkillName(roleID uint64, unassigned UnassignedSkillTotals) (unassignedSkillSource, string) {
	switch {
	case unassigned.OutgoingEvents == 0:
		return unassignedSkillSource{}, ""
	case unassigned.OverflowEvents != 0:
		return unassignedSkillSource{}, unnamedReasonOverflow
	case unassigned.DamageIDsIncomplete || len(unassigned.DamageIDs) == 0:
		return unassignedSkillSource{}, unnamedReasonDamageIDMissing
	case m == nil || m.skillNames == nil:
		return unassignedSkillSource{}, unnamedReasonNoRelation
	}
	names := m.skillNames
	if vision, ok := names.damageVision(unassigned.DamageIDs); ok {
		if !containsID(unassigned.EchoVisions, vision) {
			return unassignedSkillSource{}, unnamedReasonEchoNotHeld
		}
		item, known := names.visionItems[vision]
		if !known || names.echoNames[item] == "" {
			return unassignedSkillSource{}, unnamedReasonNoRelation
		}
		return unassignedSkillSource{names.echoNames[item], skillNameSourceEcho, item, skillIconKindEchoSkill, vision}, ""
	}
	if uiSkill, ok := names.outroSkill(roleID, unassigned.DamageIDs); ok {
		if names.uiSkillNames[uiSkill] == "" {
			return unassignedSkillSource{}, unnamedReasonNoRelation
		}
		return unassignedSkillSource{names.uiSkillNames[uiSkill], skillNameSourceDamageType, uiSkill, skillIconKindSkillConfig, uiSkill}, ""
	}
	if uiSkill, ok := names.damageListOwner(roleID, unassigned.DamageIDs, false); ok {
		if names.listsOutroDamageOfAnotherType(uiSkill, unassigned.DamageIDs) {
			return unassignedSkillSource{}, unnamedReasonOutroListedElsewhere
		}
		if names.uiSkillNames[uiSkill] == "" {
			return unassignedSkillSource{}, unnamedReasonNoRelation
		}
		return unassignedSkillSource{names.uiSkillNames[uiSkill], skillNameSourceDamageList, uiSkill, skillIconKindSkillConfig, uiSkill}, ""
	}
	return unassignedSkillSource{}, names.unnamedReasonForDamageIDs(roleID, unassigned.DamageIDs)
}

// containsID reports whether a sorted ID list holds id.
func containsID(ids []uint64, id uint64) bool {
	index := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	return index < len(ids) && ids[index] == id
}

// damageVision returns the one Vision whose common bullets deal every DamageId.
func (m *skillNameMaster) damageVision(damageIDs []uint64) (uint64, bool) {
	var vision uint64
	for index, damageID := range damageIDs {
		found, known := m.damageVisions[damageID]
		if !known || (index > 0 && found != vision) {
			return 0, false
		}
		vision = found
	}
	return vision, len(damageIDs) > 0
}

// outroSkill returns roleID's Outro UI skill when every DamageId is Outro Skill DMG listed by a UI skill of the role.
func (m *skillNameMaster) outroSkill(roleID uint64, damageIDs []uint64) (uint64, bool) {
	if roleID == 0 || len(damageIDs) == 0 {
		return 0, false
	}
	uiSkill, ok := m.roleOutro[roleID]
	if !ok {
		return 0, false
	}
	for _, damageID := range damageIDs {
		if !m.isOutroDamageOfRole(roleID, damageID) {
			return 0, false
		}
	}
	return uiSkill, true
}

// listsOutroDamageOfAnotherType reports whether a UI skill that is not an Outro skill lists Outro Skill DMG among damageIDs.
func (m *skillNameMaster) listsOutroDamageOfAnotherType(uiSkill uint64, damageIDs []uint64) bool {
	if m.uiSkillTypes[uiSkill] == uiSkillTypeOutro {
		return false
	}
	for _, damageID := range damageIDs {
		if _, ok := m.outroDamage[damageID]; ok {
			return true
		}
	}
	return false
}

// unnamedReasonForDamageIDs explains a bucket no relation named: mixed when its DamageIds resolve to different records.
func (m *skillNameMaster) unnamedReasonForDamageIDs(roleID uint64, damageIDs []uint64) string {
	kinds := map[string]struct{}{}
	for _, damageID := range damageIDs {
		if vision, ok := m.damageVisions[damageID]; ok {
			kinds["echo:"+strconv.FormatUint(vision, 10)] = struct{}{}
		} else if m.isOutroDamageOfRole(roleID, damageID) {
			kinds["outro"] = struct{}{}
		} else if listed, ok := m.damageListSkills[damageID]; ok {
			kinds["skill:"+strconv.FormatUint(listed, 10)] = struct{}{}
		} else {
			kinds["none"] = struct{}{}
		}
	}
	if len(kinds) > 1 {
		return unnamedReasonMixedSources
	}
	return unnamedReasonNoRelation
}

// isOutroDamageOfRole reports whether damageID is Outro Skill DMG that a UI skill of the role lists.
func (m *skillNameMaster) isOutroDamageOfRole(roleID, damageID uint64) bool {
	if _, ok := m.outroDamage[damageID]; !ok {
		return false
	}
	listed, ok := m.damageListSkills[damageID]
	if !ok {
		return false
	}
	_, belongs := m.uiSkillRoles[listed][roleID]
	return belongs
}

// damageListOwner returns the single UI skill of roleID that lists every DamageId.
func (m *skillNameMaster) damageListOwner(roleID uint64, damageIDs []uint64, incomplete bool) (uint64, bool) {
	if roleID == 0 || incomplete || len(damageIDs) == 0 {
		return 0, false
	}
	var uiSkill uint64
	for _, damageID := range damageIDs {
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

// damageListSkill returns the single UI skill that lists every DamageId of the row, when it belongs to roleID.
func (m *skillNameMaster) damageListSkill(roleID uint64, skill SkillDamageSnapshot) (uint64, bool) {
	return m.damageListOwner(roleID, skill.DamageIDs, skill.DamageIDsIncomplete)
}

// skillTypeSkill resolves a row whose hits several UI skills of the role list (see the file comment). Every hit must be
// listed, every lister must belong to the role, exactly one lister must have the type the row's genre names, and any other
// lister must be the role's Forte Circuit.
func (m *skillNameMaster) skillTypeSkill(roleID uint64, skill SkillDamageSnapshot) (uint64, bool) {
	if roleID == 0 || skill.DamageIDsIncomplete || len(skill.DamageIDs) < 2 {
		return 0, false
	}
	genre, ok := m.skillGenres[skill.SkillID]
	if !ok {
		return 0, false
	}
	wanted, ok := skillGenreUITypes[genre]
	if !ok {
		return 0, false
	}
	listers := map[uint64]struct{}{}
	for _, damageID := range skill.DamageIDs {
		listed, known := m.damageListSkills[damageID]
		if !known {
			return 0, false
		}
		listers[listed] = struct{}{}
	}
	if len(listers) < 2 {
		return 0, false
	}
	var match uint64
	for uiSkill := range listers {
		if _, belongs := m.uiSkillRoles[uiSkill][roleID]; !belongs {
			return 0, false
		}
		switch m.uiSkillTypes[uiSkill] {
		case wanted:
			if match != 0 {
				return 0, false
			}
			match = uiSkill
		case uiSkillTypeForte:
		default:
			return 0, false
		}
	}
	return match, match != 0
}

// runtimeEchoItem resolves a skill several Visions cast under different names: the casting role's own Echo, the
// ConcomitantVision entity the server summons for it, must be exactly one of those Visions.
func (m *skillNameMaster) runtimeEchoItem(skill SkillDamageSnapshot) (uint64, bool) {
	candidates := m.ambiguousEchoVisions[skill.SkillID]
	if len(candidates) == 0 {
		return 0, false
	}
	var match uint64
	for _, vision := range skill.EchoVisions {
		if _, ok := candidates[vision]; !ok {
			continue
		}
		if match != 0 {
			return 0, false
		}
		match = vision
	}
	if match == 0 {
		return 0, false
	}
	item, ok := m.visionItems[match]
	return item, ok
}

// tuneBreakSkill returns the role's Tune Break UI skill when the row is a weakness-break skill (see the file comment).
func (m *skillNameMaster) tuneBreakSkill(roleID uint64, skill SkillDamageSnapshot) (uint64, bool) {
	if roleID == 0 || skill.DamageIDsIncomplete || len(skill.DamageIDs) == 0 {
		return 0, false
	}
	if _, ok := m.tuneBreakSkills[skill.SkillID]; !ok {
		return 0, false
	}
	for _, damageID := range skill.DamageIDs {
		if _, ok := m.tuneBreakDamage[damageID]; !ok {
			return 0, false
		}
	}
	uiSkill, ok := m.roleTuneBreak[roleID]
	return uiSkill, ok
}

// loadSQLiteSkillNameSources reads the relations of the file comment.
func loadSQLiteSkillNameSources(db *sql.DB, locale string, master *GameMaster) error {
	names := newSkillNameMaster()
	master.skillNames = names
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
	rows, err := db.Query(`SELECT CAST(id AS TEXT), skill_type FROM skill WHERE skill_type IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query UI skill types: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var skillType int64
		if err := rows.Scan(&raw, &skillType); err != nil {
			return fmt.Errorf("read UI skill type: %w", err)
		}
		id, err := parseMasterID(raw, "skill.id")
		if err != nil {
			return err
		}
		names.uiSkillTypes[id] = skillType
	}
	if err := rows.Err(); err != nil {
		return err
	}
	roleSkillOfType := func(skillType int64, into map[uint64]uint64) error {
		found := map[uint64]map[uint64]struct{}{}
		if err := pairs(`SELECT CAST(r.role_id AS TEXT),CAST(s.id AS TEXT) FROM skill s
		JOIN role_skill_group_ref r ON CAST(r.skill_group_id AS TEXT)=CAST(s.skill_group_id AS TEXT) WHERE s.skill_type=?`, func(roleID, uiSkill uint64) error {
			if found[roleID] == nil {
				found[roleID] = map[uint64]struct{}{}
			}
			found[roleID][uiSkill] = struct{}{}
			return nil
		}, skillType); err != nil {
			return err
		}
		for roleID, skills := range found {
			if len(skills) == 1 {
				for uiSkill := range skills {
					into[roleID] = uiSkill
				}
			}
		}
		return nil
	}
	if err := roleSkillOfType(uiSkillTypeBreak, names.roleTuneBreak); err != nil {
		return err
	}
	if err := roleSkillOfType(uiSkillTypeOutro, names.roleOutro); err != nil {
		return err
	}
	if err := loadSQLiteSkillGenres(db, names); err != nil {
		return err
	}
	if err := loadSQLiteTuneBreakSkills(db, names); err != nil {
		return err
	}
	if err := loadSQLiteEchoSkillItems(db, names); err != nil {
		return err
	}
	if err := loadLocalizedNames(db, "phantomitem_locale", locale, names.echoNames); err != nil {
		return err
	}
	return nil
}

// loadSQLiteSkillGenres reads the genre of every SkillId whose SkillInfo rows agree on one.
func loadSQLiteSkillGenres(db *sql.DB, names *skillNameMaster) error {
	rows, err := db.Query(`SELECT CAST(skill_id AS TEXT), MIN(skill_genre_raw) FROM skillinfo WHERE skill_genre_raw IS NOT NULL GROUP BY skill_id HAVING MIN(skill_genre_raw)=MAX(skill_genre_raw)`)
	if err != nil {
		return fmt.Errorf("query skill genres: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var genre int64
		if err := rows.Scan(&raw, &genre); err != nil {
			return fmt.Errorf("read skill genre: %w", err)
		}
		id, err := parseMasterID(raw, "skillinfo.skill_id")
		if err != nil {
			return err
		}
		names.skillGenres[id] = genre
	}
	return rows.Err()
}

// loadSQLiteTuneBreakSkills reads the SkillIds of the weakness-break genre, the DamageIds of its damage type, and the DamageIds
// of the Outro damage type.
func loadSQLiteTuneBreakSkills(db *sql.DB, names *skillNameMaster) error {
	rows, err := db.Query(`SELECT CAST(skill_id AS TEXT) FROM skillinfo GROUP BY skill_id HAVING MIN(skill_genre_raw)=? AND MAX(skill_genre_raw)=?`, skillGenreBreak, skillGenreBreak)
	if err != nil {
		return fmt.Errorf("query weakness-break skills: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read weakness-break skills: %w", err)
		}
		id, err := parseMasterID(raw, "skillinfo.skill_id")
		if err != nil {
			return err
		}
		names.tuneBreakSkills[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := loadDamageIDsOfType(db, damageTypeBreak, names.tuneBreakDamage); err != nil {
		return fmt.Errorf("read weakness-break damage: %w", err)
	}
	if err := loadDamageIDsOfType(db, damageTypeOutro, names.outroDamage); err != nil {
		return fmt.Errorf("read Outro damage: %w", err)
	}
	return nil
}

// loadDamageIDsOfType adds the DamageIds of one damage.type.
func loadDamageIDsOfType(db *sql.DB, damageType int64, into map[uint64]struct{}) error {
	rows, err := db.Query(`SELECT CAST(id AS TEXT) FROM damage WHERE type=?`, damageType)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		id, err := parseMasterID(raw, "damage.id")
		if err != nil {
			return err
		}
		into[id] = struct{}{}
	}
	return rows.Err()
}

// echoItem is one PhantomItem that a Vision can be equipped as.
type echoItem struct {
	id, monster, parent uint64
	names               map[string]string
}

// resolveEchoItems picks the PhantomItem that names a set of candidates, or reports none. One official name per locale
// across every candidate wins outright. Otherwise the candidates must be one family: the base items of one monster share
// their names and every other candidate is a variant of that monster, and the family is named by its base.
func resolveEchoItems(candidates []echoItem) (uint64, bool) {
	if len(candidates) == 0 {
		return 0, false
	}
	pick := func(items []echoItem, locales map[string]map[string]struct{}) (uint64, bool) {
		ids := make([]uint64, 0, len(items))
		for _, item := range items {
			if len(item.names) == len(locales) {
				ids = append(ids, item.id)
			}
		}
		if len(ids) == 0 {
			return 0, false
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		return ids[0], true
	}
	namesOf := func(items []echoItem) (map[string]map[string]struct{}, bool) {
		locales := map[string]map[string]struct{}{}
		unique := true
		for _, item := range items {
			for locale, name := range item.names {
				if locales[locale] == nil {
					locales[locale] = map[string]struct{}{}
				}
				locales[locale][name] = struct{}{}
				if len(locales[locale]) > 1 {
					unique = false
				}
			}
		}
		return locales, unique
	}
	if locales, unique := namesOf(candidates); unique {
		return pick(candidates, locales)
	}
	var bases []echoItem
	root := uint64(0)
	for _, item := range candidates {
		itemRoot := item.monster
		if item.parent != 0 {
			itemRoot = item.parent
		} else {
			bases = append(bases, item)
		}
		if itemRoot == 0 || (root != 0 && root != itemRoot) {
			return 0, false
		}
		root = itemRoot
	}
	if len(bases) == 0 {
		return 0, false
	}
	locales, unique := namesOf(bases)
	if !unique {
		return 0, false
	}
	return pick(bases, locales)
}

// loadSQLiteEchoSkillItems links SkillIds and DamageIds to Visions and each Vision to the PhantomItem that names it.
func loadSQLiteEchoSkillItems(db *sql.DB, names *skillNameMaster) error {
	keys := `SELECT CAST(id AS TEXT) AS vision_id, CAST(skill_id AS TEXT) AS skill_key FROM vision WHERE skill_id IS NOT NULL AND CAST(skill_id AS TEXT)<>''` +
		` UNION SELECT CAST(id AS TEXT), CAST(birth_cast_skill_id AS TEXT) FROM vision WHERE birth_cast_skill_id IS NOT NULL AND CAST(birth_cast_skill_id AS TEXT)<>''`
	visionsOfSkill := map[uint64]map[uint64]struct{}{}
	addVision := func(skill, vision uint64) {
		if visionsOfSkill[skill] == nil {
			visionsOfSkill[skill] = map[uint64]struct{}{}
		}
		visionsOfSkill[skill][vision] = struct{}{}
	}
	if err := scanMasterIDPairs(db, keys, func(vision, skill uint64) { addVision(skill, vision) }); err != nil {
		return err
	}
	items, err := loadSQLiteVisionItems(db)
	if err != nil {
		return err
	}
	itemVisions := map[uint64]uint64{}
	for vision, candidates := range items {
		for _, candidate := range candidates {
			itemVisions[candidate.id] = vision
			names.itemVisions[candidate.id] = vision
		}
		if item, ok := resolveEchoItems(candidates); ok {
			names.visionItems[vision] = item
		}
	}
	notes, err := loadSQLiteVisionNotes(db)
	if err != nil {
		return err
	}
	if err := linkEchoSummonLabels(db, notes, addVision); err != nil {
		return err
	}
	if err := linkVisionActors(db, visionsOfSkill, addVision); err != nil {
		return err
	}
	for skill, visions := range visionsOfSkill {
		var candidates []echoItem
		for vision := range visions {
			candidates = append(candidates, items[vision]...)
		}
		if item, ok := resolveEchoItems(candidates); ok {
			names.echoItems[skill] = item
			names.echoVisions[skill] = itemVisions[item]
		} else if len(visions) > 1 {
			names.ambiguousEchoVisions[skill] = visions
		}
	}
	return linkCommonBulletDamage(db, notes, names)
}

func scanMasterIDPairs(db *sql.DB, query string, read func(a, b uint64)) error {
	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("query echo skill relations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawA, rawB string
		if err := rows.Scan(&rawA, &rawB); err != nil {
			return fmt.Errorf("read echo skill relations: %w", err)
		}
		a, err := parseMasterID(rawA, "echo skill relation")
		if err != nil {
			return err
		}
		b, err := parseMasterID(rawB, "echo skill relation")
		if err != nil {
			return err
		}
		read(a, b)
	}
	return rows.Err()
}

// loadSQLiteVisionItems reads every PhantomItem of each Vision with its localized names and monster family.
func loadSQLiteVisionItems(db *sql.DB) (map[uint64][]echoItem, error) {
	rows, err := db.Query(`SELECT CAST(i.skill_id AS TEXT), CAST(i.id AS TEXT), COALESCE(CAST(i.monster_id AS TEXT),'0'), COALESCE(CAST(i.parent_monster_id AS TEXT),'0'), l.locale, l.name
		FROM phantomitem i JOIN phantomitem_locale l ON CAST(l.id AS TEXT)=CAST(i.id AS TEXT) AND l.name<>''
		WHERE i.skill_id IS NOT NULL AND CAST(i.skill_id AS TEXT)<>''`)
	if err != nil {
		return nil, fmt.Errorf("query echo skill names: %w", err)
	}
	defer rows.Close()
	byVision := map[uint64]map[uint64]*echoItem{}
	for rows.Next() {
		var rawVision, rawItem, rawMonster, rawParent, locale, name string
		if err := rows.Scan(&rawVision, &rawItem, &rawMonster, &rawParent, &locale, &name); err != nil {
			return nil, fmt.Errorf("read echo skill names: %w", err)
		}
		vision, err := parseMasterID(rawVision, "phantomitem.skill_id")
		if err != nil {
			return nil, err
		}
		id, err := parseMasterID(rawItem, "phantomitem.id")
		if err != nil {
			return nil, err
		}
		monsterID, err := parseOptionalMasterID(rawMonster, "phantomitem.monster_id")
		if err != nil {
			return nil, err
		}
		parentID, err := parseOptionalMasterID(rawParent, "phantomitem.parent_monster_id")
		if err != nil {
			return nil, err
		}
		if byVision[vision] == nil {
			byVision[vision] = map[uint64]*echoItem{}
		}
		item := byVision[vision][id]
		if item == nil {
			item = &echoItem{id: id, monster: monsterID, parent: parentID, names: map[string]string{}}
			byVision[vision][id] = item
		}
		item.names[locale] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make(map[uint64][]echoItem, len(byVision))
	for vision, items := range byVision {
		for _, item := range items {
			result[vision] = append(result[vision], *item)
		}
	}
	return result, nil
}

// parseOptionalMasterID reads an ID column where zero means "none".
func parseOptionalMasterID(raw, field string) (uint64, error) {
	if raw == "" || raw == "0" {
		return 0, nil
	}
	return parseMasterID(raw, field)
}

// loadSQLiteVisionNotes maps a developer note to its Vision, leaving out notes that several Visions share.
func loadSQLiteVisionNotes(db *sql.DB) (map[string]uint64, error) {
	rows, err := db.Query(`SELECT CAST(id AS TEXT), dev_note_zh_cn FROM vision WHERE dev_note_zh_cn IS NOT NULL AND dev_note_zh_cn<>''`)
	if err != nil {
		return nil, fmt.Errorf("query vision notes: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	notes := map[string]uint64{}
	for rows.Next() {
		var raw, note string
		if err := rows.Scan(&raw, &note); err != nil {
			return nil, fmt.Errorf("read vision notes: %w", err)
		}
		id, err := parseMasterID(raw, "vision.id")
		if err != nil {
			return nil, err
		}
		counts[note]++
		notes[note] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for note, count := range counts {
		if count != 1 {
			delete(notes, note)
		}
	}
	return notes, nil
}

// linkEchoSummonLabels links the common skills that summon one Echo through their developer label.
func linkEchoSummonLabels(db *sql.DB, notes map[string]uint64, addVision func(skill, vision uint64)) error {
	rows, err := db.Query(`SELECT DISTINCT CAST(skill_id AS TEXT), dev_name_zh_cn FROM skillinfo WHERE dev_name_zh_cn LIKE ?`, echoSummonLabelPrefix+"%")
	if err != nil {
		return fmt.Errorf("query echo summon labels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, label string
		if err := rows.Scan(&raw, &label); err != nil {
			return fmt.Errorf("read echo summon labels: %w", err)
		}
		skill, err := parseMasterID(raw, "skillinfo.skill_id")
		if err != nil {
			return err
		}
		if vision, ok := notes[strings.TrimPrefix(label, echoSummonLabelPrefix)]; ok {
			addVision(skill, vision)
		}
	}
	return rows.Err()
}

// linkVisionActors adds a skill preloaded only on Vision actor blueprints to the Visions of those blueprints. The blueprint
// of a Vision is the one its directly linked skills are preloaded on, and it must lie under the Vision character tree; a
// skill common to many actors never qualifies. Several Visions of one blueprint are all candidates: resolveEchoItems then
// decides whether they name one Echo.
func linkVisionActors(db *sql.DB, direct map[uint64]map[uint64]struct{}, addVision func(skill, vision uint64)) error {
	// Both ids are TEXT columns and asset_reference.id is its primary key, so the join compares the stored text directly
	// and SQLite finds each actor through that key. A CAST on either side hides the index and compares every preload row
	// with every asset reference (about 9 s of a Master load before 2026-11-14).
	rows, err := db.Query(`SELECT CAST(p.skill_id AS TEXT), CAST(p.actor_asset_id AS TEXT), COALESCE(p.is_common,0), COALESCE(a.path,'')
		FROM entityskillpreload p LEFT JOIN asset_reference a ON a.id = p.actor_asset_id`)
	if err != nil {
		return fmt.Errorf("query skill preload actors: %w", err)
	}
	defer rows.Close()
	type row struct {
		skill  uint64
		actor  string
		common bool
	}
	var all []row
	actorPaths := map[string]string{}
	for rows.Next() {
		var rawSkill, actor, path string
		var common int64
		if err := rows.Scan(&rawSkill, &actor, &common, &path); err != nil {
			return fmt.Errorf("read skill preload actors: %w", err)
		}
		skill, err := parseMasterID(rawSkill, "entityskillpreload.skill_id")
		if err != nil {
			return err
		}
		all = append(all, row{skill: skill, actor: actor, common: common != 0})
		actorPaths[actor] = path
	}
	if err := rows.Err(); err != nil {
		return err
	}
	actorVisions := map[string]map[uint64]struct{}{}
	for _, entry := range all {
		visions, linked := direct[entry.skill]
		if !linked || entry.common || !strings.Contains(actorPaths[entry.actor], visionActorPathSegment) {
			continue
		}
		if actorVisions[entry.actor] == nil {
			actorVisions[entry.actor] = map[uint64]struct{}{}
		}
		for vision := range visions {
			actorVisions[entry.actor][vision] = struct{}{}
		}
	}
	type candidate struct {
		visions map[uint64]struct{}
		invalid bool
	}
	skills := map[uint64]*candidate{}
	for _, entry := range all {
		current := skills[entry.skill]
		if current == nil {
			current = &candidate{visions: map[uint64]struct{}{}}
			skills[entry.skill] = current
		}
		visions, known := actorVisions[entry.actor]
		if entry.common || !known {
			current.invalid = true
			continue
		}
		for vision := range visions {
			current.visions[vision] = struct{}{}
		}
	}
	for skill, current := range skills {
		if _, linked := direct[skill]; linked || current.invalid || len(current.visions) == 0 {
			continue
		}
		for vision := range current.visions {
			addVision(skill, vision)
		}
	}
	return nil
}

// linkCommonBulletDamage maps a DamageId to the one Vision named by every common bullet that deals it. A common bullet's
// developer name starts with the Vision's developer note followed by a separator.
func linkCommonBulletDamage(db *sql.DB, notes map[string]uint64, names *skillNameMaster) error {
	rows, err := db.Query(`SELECT CAST(damage_id AS TEXT), dev_name_zh_cn FROM commonbullet WHERE damage_id IS NOT NULL AND CAST(damage_id AS TEXT) NOT IN ('','0') AND dev_name_zh_cn IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query common bullet damage: %w", err)
	}
	defer rows.Close()
	conflicted := map[uint64]struct{}{}
	for rows.Next() {
		var rawDamage, name string
		if err := rows.Scan(&rawDamage, &name); err != nil {
			return fmt.Errorf("read common bullet damage: %w", err)
		}
		damageID, err := parseMasterID(rawDamage, "commonbullet.damage_id")
		if err != nil {
			return err
		}
		note, _, separated := strings.Cut(name, visionBulletNameSeparator)
		vision, known := notes[note]
		if !separated || !known {
			conflicted[damageID] = struct{}{}
			continue
		}
		if existing, seen := names.damageVisions[damageID]; seen && existing != vision {
			conflicted[damageID] = struct{}{}
			continue
		}
		names.damageVisions[damageID] = vision
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for damageID := range conflicted {
		delete(names.damageVisions, damageID)
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
