package desktop

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// displayProjection serializes a display-only copy per core sequence and locale revision.
// SQLite reads happen here, never on the packet-processing goroutine.
type displayProjection struct {
	mu       sync.Mutex
	resolver *capture.DisplayResolver
	source   *service.Snapshot
	revision uint64
	shell    projectedShell
	encoded  json.RawMessage
}

// projectedShell is host state published with every snapshot. A change reaches every window without a new core sequence.
type projectedShell struct {
	Listening service.ListeningStatus `json:"listening"`
	// LanguagePreference tells the Settings view whether the language follows Windows; the language itself is in Locale.
	LanguagePreference string `json:"languagePreference,omitempty"`
	Theme              string `json:"theme"`
	ReduceMotion       bool   `json:"reduceMotion"`
	// UpdateIntervalMs is the publish cadence the windows receive; the countdown bars ease across one interval.
	UpdateIntervalMs int           `json:"updateIntervalMs"`
	Overlays         OverlayStates `json:"overlays"`
	// SettingsWarning is a recoverable settings problem to show in the Desktop window.
	SettingsWarning string `json:"settingsWarning,omitempty"`
	// History is the Combat History indicator (history_status.go).
	History historyIndicator `json:"history"`
}

func (p *displayProjection) encode(source *service.Snapshot, settings SettingsState, catalog localeCatalog, listening service.ListeningStatus, overlays OverlayStates, history historyIndicator) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	shell := projectedShell{Listening: listening, LanguagePreference: settings.Settings.LanguagePreference, Theme: effectiveTheme(settings.Settings), ReduceMotion: settings.Settings.ReduceMotion,
		UpdateIntervalMs: int(effectiveUpdateInterval(settings.Settings) / time.Millisecond), Overlays: overlays, SettingsWarning: settings.Warning, History: history}
	if p.source == source && p.revision == settings.LocaleRevision && p.shell == shell {
		return append(json.RawMessage(nil), p.encoded...), nil
	}
	locale := settings.Settings.Locale
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names := &nameProjection{ctx: ctx, resolver: p.resolver, locale: catalog.Languages[locale].MasterLocale}
	var view *capture.ViewSnapshot
	if source.View != nil {
		copied := *source.View
		copied.Roles = names.roles(copied.Roles)
		copied.Vehicle = names.vehicle(copied.Vehicle)
		copied.Encounters.Recent = append([]capture.ViewEncounter{}, copied.Encounters.Recent...)
		for i := range copied.Encounters.Recent {
			names.encounter(&copied.Encounters.Recent[i])
		}
		if copied.Encounters.Current != nil {
			current := *copied.Encounters.Current
			names.encounter(&current)
			copied.Encounters.Current = &current
		}
		copied.Effects.Active = names.effects(copied.Effects.Active)
		copied.Effects.Buffs.Effects = names.effects(copied.Effects.Buffs.Effects)
		copied.Effects.Debuffs.Effects = names.effects(copied.Effects.Debuffs.Effects)
		copied.Effects.RecentEnded = names.effects(copied.Effects.RecentEnded)
		view = &copied
	}
	if names.err != nil {
		return nil, names.err
	}
	encoded, err := json.Marshal(struct {
		Sequence       uint64                `json:"sequence"`
		PublishedAt    time.Time             `json:"publishedAt"`
		Service        service.Status        `json:"service"`
		View           *capture.ViewSnapshot `json:"view"`
		Locale         string                `json:"locale"`
		LocaleRevision uint64                `json:"localeRevision"`
		projectedShell
	}{source.Sequence, source.PublishedAt, source.Service, view, locale, settings.LocaleRevision, shell})
	if err != nil {
		return nil, err
	}
	p.source, p.revision, p.shell, p.encoded = source, settings.LocaleRevision, shell, encoded
	return append(json.RawMessage(nil), encoded...), nil
}

// nameProjection resolves the display names of engine views for one Master locale. Every lookup is read-only; the first
// failed lookup is kept in err, and a caller then returns that error instead of a partly named view.
type nameProjection struct {
	ctx      context.Context
	resolver *capture.DisplayResolver
	locale   string
	err      error
}

func (n *nameProjection) resolve(kind string, id uint64) capture.DisplayName {
	name, err := n.resolver.Resolve(n.ctx, kind, id, n.locale)
	if err != nil {
		n.err = err
	}
	return name
}

// staticRole is the RoleId a runtime role shows the art of (DisplayResolver.StaticRole), and element its element; a
// failed lookup is kept in err as resolve keeps it. Without a resolver a role shows as itself, with no element.
func (n *nameProjection) staticRole(role uint64) uint64 {
	if n.resolver == nil {
		return role
	}
	static, err := n.resolver.StaticRole(n.ctx, role)
	if err != nil {
		n.err = err
	}
	return static
}

func (n *nameProjection) element(role uint64) uint64 {
	if n.resolver == nil || role == 0 {
		return 0
	}
	element, err := n.resolver.RoleElement(n.ctx, role)
	if err != nil {
		n.err = err
	}
	return element
}

func (n *nameProjection) skills(original []capture.ViewSkill) []capture.ViewSkill {
	result := append([]capture.ViewSkill{}, original...)
	for i := range result {
		result[i].Name, result[i].NameStatus = projectSkillName(n.ctx, n.resolver, result[i], n.locale, &n.err)
	}
	return result
}

func (n *nameProjection) roles(original []capture.ViewRole) []capture.ViewRole {
	result := append([]capture.ViewRole{}, original...)
	for i := range result {
		name := n.resolve("role", result[i].RoleID)
		result[i].Name, result[i].NameStatus = name.Name, name.Status
		result[i].StaticRoleID = n.staticRole(result[i].RoleID)
		result[i].ElementID = n.element(result[i].RoleID)
		result[i].Skills = n.skills(result[i].Skills)
		result[i].UnassignedSkill = projectUnassignedSkill(n.ctx, n.resolver, result[i].UnassignedSkill, n.locale, &n.err)
		result[i].Statuses = n.statuses(result[i].Statuses)
	}
	return result
}

// statuses names the status rows of a role in the UI locale through the glossary term the engine found. A term without a
// name in that locale keeps the engine's name.
func (n *nameProjection) statuses(original []capture.ViewStatus) []capture.ViewStatus {
	if len(original) == 0 {
		return original
	}
	result := append([]capture.ViewStatus{}, original...)
	for i := range result {
		if result[i].TermID == 0 {
			continue
		}
		if name := n.resolve("term", result[i].TermID); name.Name != "" {
			result[i].Name = name.Name
		}
	}
	return result
}

func (n *nameProjection) vehicle(original *capture.ViewVehicle) *capture.ViewVehicle {
	if original == nil {
		return nil
	}
	copied := *original
	copied.Skills = n.skills(copied.Skills)
	copied.UnassignedSkill = projectUnassignedSkill(n.ctx, n.resolver, copied.UnassignedSkill, n.locale, &n.err)
	return &copied
}

// encounter names one encounter's contributors in place.
func (n *nameProjection) encounter(encounter *capture.ViewEncounter) {
	encounter.Roles = n.roles(encounter.Roles)
	encounter.Vehicle = n.vehicle(encounter.Vehicle)
}

func (n *nameProjection) effects(original []capture.ViewEffect) []capture.ViewEffect {
	result := append([]capture.ViewEffect{}, original...)
	for i := range result {
		effect := &result[i]
		effect.TermName = ""
		if effect.HolderRoleID != nil {
			name := n.resolve("role", *effect.HolderRoleID)
			effect.HolderName, effect.HolderNameStatus = name.Name, name.Status
		}
		if effect.TermID != nil && (effect.TermStatus == "VERIFIED" || effect.TermStatus == "CORRELATED") {
			effect.TermName = n.resolve("term", *effect.TermID).Name
		}
	}
	return result
}

// skillSourceKind is the display-name namespace of a derived skill source: a UI skill (Skill.Id) or an Echo (PhantomItem.Id).
func skillSourceKind(source string) (string, bool) {
	switch source {
	case "damage_list", "skill_type", "tune_break", "damage_type":
		return "skill", true
	case "echo":
		return "echo", true
	}
	return "", false
}

// projectUnassignedSkill re-resolves the name of an unassigned row for the UI locale through the source the engine selected.
// When the record has no name in any locale the row goes back to being plainly unassigned.
func projectUnassignedSkill(ctx context.Context, resolver *capture.DisplayResolver, skill capture.ViewUnassignedSkill, locale string, projectionErr *error) capture.ViewUnassignedSkill {
	kind, ok := skillSourceKind(skill.NameSource)
	if !ok {
		return skill
	}
	name, err := resolver.Resolve(ctx, kind, skill.SourceID, locale)
	if err != nil {
		*projectionErr = err
	}
	skill.Name = name.Name
	if name.Name == "" {
		skill.NameStatus, skill.NameSource, skill.SourceID, skill.IconKind, skill.IconID = "", "", 0, "", 0
	}
	return skill
}

// projectSkillName re-resolves a skill row's name for the UI locale through the source the engine selected. A derived
// source keeps its DERIVED status; when its record has no name in any locale the row falls back to UNKNOWN.
func projectSkillName(ctx context.Context, resolver *capture.DisplayResolver, skill capture.ViewSkill, locale string, projectionErr *error) (string, string) {
	kind, id, status := "combat_skill", skill.SkillID, ""
	if sourceKind, ok := skillSourceKind(skill.NameSource); ok {
		kind, id, status = sourceKind, skill.SourceID, "DERIVED"
	}
	name, err := resolver.Resolve(ctx, kind, id, locale)
	if err != nil {
		*projectionErr = err
	}
	if status != "" && name.Name != "" {
		return name.Name, status
	}
	return name.Name, name.Status
}
