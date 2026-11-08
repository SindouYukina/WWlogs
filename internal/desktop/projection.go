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
}

func (p *displayProjection) encode(source *service.Snapshot, settings SettingsState, catalog localeCatalog, listening service.ListeningStatus, overlays OverlayStates) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	shell := projectedShell{Listening: listening, LanguagePreference: settings.Settings.LanguagePreference, Theme: effectiveTheme(settings.Settings), ReduceMotion: settings.Settings.ReduceMotion,
		UpdateIntervalMs: int(effectiveUpdateInterval(settings.Settings) / time.Millisecond), Overlays: overlays, SettingsWarning: settings.Warning}
	if p.source == source && p.revision == settings.LocaleRevision && p.shell == shell {
		return append(json.RawMessage(nil), p.encoded...), nil
	}
	locale := settings.Settings.Locale
	masterLocale := catalog.Languages[locale].MasterLocale
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var projectionErr error
	skills := func(original []capture.ViewSkill) []capture.ViewSkill {
		result := append([]capture.ViewSkill{}, original...)
		for i := range result {
			result[i].Name, result[i].NameStatus = projectSkillName(ctx, p.resolver, result[i], masterLocale, &projectionErr)
		}
		return result
	}
	roles := func(original []capture.ViewRole) []capture.ViewRole {
		result := append([]capture.ViewRole{}, original...)
		for i := range result {
			name, err := p.resolver.Resolve(ctx, "role", result[i].RoleID, masterLocale)
			if err != nil {
				projectionErr = err
			}
			result[i].Name, result[i].NameStatus = name.Name, name.Status
			result[i].ElementID = p.resolver.RoleElement(ctx, result[i].RoleID)
			result[i].Skills = skills(result[i].Skills)
			result[i].UnassignedSkill = projectUnassignedSkill(ctx, p.resolver, result[i].UnassignedSkill, masterLocale, &projectionErr)
		}
		return result
	}
	vehicle := func(original *capture.ViewVehicle) *capture.ViewVehicle {
		if original == nil {
			return nil
		}
		copied := *original
		copied.Skills = skills(copied.Skills)
		copied.UnassignedSkill = projectUnassignedSkill(ctx, p.resolver, copied.UnassignedSkill, masterLocale, &projectionErr)
		return &copied
	}
	effects := func(original []capture.ViewEffect) []capture.ViewEffect {
		result := append([]capture.ViewEffect{}, original...)
		for i := range result {
			effect := &result[i]
			effect.TermName = ""
			if effect.HolderRoleID != nil {
				name, err := p.resolver.Resolve(ctx, "role", *effect.HolderRoleID, masterLocale)
				if err != nil {
					projectionErr = err
				}
				effect.HolderName, effect.HolderNameStatus = name.Name, name.Status
			}
			if effect.TermID != nil && (effect.TermStatus == "VERIFIED" || effect.TermStatus == "CORRELATED") {
				name, err := p.resolver.Resolve(ctx, "term", *effect.TermID, masterLocale)
				if err != nil {
					projectionErr = err
				}
				effect.TermName = name.Name
			}
		}
		return result
	}
	var view *capture.ViewSnapshot
	if source.View != nil {
		copied := *source.View
		copied.Roles = roles(copied.Roles)
		copied.Vehicle = vehicle(copied.Vehicle)
		copied.Encounters.Recent = append([]capture.ViewEncounter{}, copied.Encounters.Recent...)
		for i := range copied.Encounters.Recent {
			copied.Encounters.Recent[i].Roles = roles(copied.Encounters.Recent[i].Roles)
			copied.Encounters.Recent[i].Vehicle = vehicle(copied.Encounters.Recent[i].Vehicle)
		}
		if copied.Encounters.Current != nil {
			current := *copied.Encounters.Current
			current.Roles = roles(current.Roles)
			current.Vehicle = vehicle(current.Vehicle)
			copied.Encounters.Current = &current
		}
		copied.Effects.Active = effects(copied.Effects.Active)
		copied.Effects.RecentEnded = effects(copied.Effects.RecentEnded)
		view = &copied
	}
	if projectionErr != nil {
		return nil, projectionErr
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

// projectUnassignedSkill re-resolves the Echo name of an unassigned row for the UI locale. When the Echo has no name in any
// locale the row goes back to being plainly unassigned.
func projectUnassignedSkill(ctx context.Context, resolver *capture.DisplayResolver, skill capture.ViewUnassignedSkill, locale string, projectionErr *error) capture.ViewUnassignedSkill {
	if skill.NameSource != "echo" {
		return skill
	}
	name, err := resolver.Resolve(ctx, "echo", skill.SourceID, locale)
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
	switch skill.NameSource {
	case "damage_list", "tune_break":
		kind, id, status = "skill", skill.SourceID, "DERIVED"
	case "echo":
		kind, id, status = "echo", skill.SourceID, "DERIVED"
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
