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
	encoded  json.RawMessage
}

func (p *displayProjection) encode(source *service.Snapshot, settings SettingsState, catalog localeCatalog) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.source == source && p.revision == settings.LocaleRevision {
		return append(json.RawMessage(nil), p.encoded...), nil
	}
	locale := settings.Settings.Locale
	masterLocale := catalog.Languages[locale].MasterLocale
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var projectionErr error
	roles := func(original []capture.ViewRole) []capture.ViewRole {
		result := append([]capture.ViewRole{}, original...)
		for i := range result {
			name, err := p.resolver.Resolve(ctx, "role", result[i].RoleID, masterLocale)
			if err != nil {
				projectionErr = err
			}
			result[i].Name, result[i].NameStatus = name.Name, name.Status
		}
		return result
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
		copied.Encounters.Recent = append([]capture.ViewEncounter{}, copied.Encounters.Recent...)
		for i := range copied.Encounters.Recent {
			copied.Encounters.Recent[i].Roles = roles(copied.Encounters.Recent[i].Roles)
		}
		if copied.Encounters.Current != nil {
			current := *copied.Encounters.Current
			current.Roles = roles(current.Roles)
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
	}{source.Sequence, source.PublishedAt, source.Service, view, locale, settings.LocaleRevision})
	if err != nil {
		return nil, err
	}
	p.source, p.revision, p.encoded = source, settings.LocaleRevision, encoded
	return append(json.RawMessage(nil), encoded...), nil
}
