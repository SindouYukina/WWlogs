package desktop

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

const settingsVersion = 1

// Settings contains only supported user preferences, never capture or diagnostic internals.
type Settings struct {
	Version            int    `json:"version"`
	DefaultAdapterIPv4 string `json:"defaultAdapterIPv4"`
	Locale             string `json:"locale"`
	// ShowOverlayOnStartup is read from v0.5 files only: it migrates to Overlays.Combat.Visible on load.
	ShowOverlayOnStartup bool `json:"showOverlayOnStartup,omitempty"`
	// Overlays keeps each Overlay's preferences separately. Nil in a file means it predates the two-window Overlay.
	Overlays *OverlaySet `json:"overlays,omitempty"`
	// Theme is one of the official theme IDs; empty means the default theme.
	Theme        string `json:"theme,omitempty"`
	ReduceMotion bool   `json:"reduceMotion,omitempty"`
}

// OverlaySet holds independent preferences per Overlay; a change to one never touches the other.
type OverlaySet struct {
	Combat  OverlayPreferences `json:"combat"`
	Effects OverlayPreferences `json:"effects"`
}

// OverlayPreferences are the persisted properties of one Overlay window.
type OverlayPreferences struct {
	Visible      bool              `json:"visible"`
	Opacity      int               `json:"opacity"`
	AlwaysOnTop  bool              `json:"alwaysOnTop"`
	ClickThrough bool              `json:"clickThrough"`
	Placement    *OverlayPlacement `json:"placement,omitempty"`
}

// OverlayPlacement is in physical pixels, with the scale of the display it was saved on. Wails DIP coordinates are per
// screen, so they are not stable across monitors with different scaling; Scale lets a restore keep the logical size.
type OverlayPlacement struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

func (p OverlayPreferences) state(editing bool) OverlayState {
	return OverlayState{Visible: p.Visible, Opacity: p.Opacity, AlwaysOnTop: p.AlwaysOnTop, ClickThrough: p.ClickThrough, Editing: editing}
}

const minOverlayOpacity, maxOverlayOpacity = 20, 100

func clampOpacity(value int) int {
	return max(minOverlayOpacity, min(value, maxOverlayOpacity))
}

// defaultOverlayPreferences starts in click-through so a first-run Overlay cannot take clicks from the game; Edit Mode
// in Settings is the way to move it.
func defaultOverlayPreferences(kind string) OverlayPreferences {
	return OverlayPreferences{Opacity: overlaySpecs[kind].defaultOpacity, AlwaysOnTop: true, ClickThrough: true}
}

func defaultOverlays() *OverlaySet {
	return &OverlaySet{Combat: defaultOverlayPreferences(overlayCombat), Effects: defaultOverlayPreferences(overlayEffects)}
}

func validPlacement(p *OverlayPlacement) bool {
	return p != nil && p.Scale > 0 && p.Scale <= 16 && p.Width > 0 && p.Width <= 20000 && p.Height > 0 && p.Height <= 20000 && p.X >= -200000 && p.X <= 200000 && p.Y >= -200000 && p.Y <= 200000
}

func normalizeOverlay(kind string, prefs *OverlayPreferences) (reset bool) {
	if prefs.Opacity == 0 {
		prefs.Opacity = defaultOverlayPreferences(kind).Opacity
	}
	prefs.Opacity = clampOpacity(prefs.Opacity)
	if prefs.Placement != nil && !validPlacement(prefs.Placement) {
		prefs.Placement = nil
		return true
	}
	return false
}

// defaultTheme is the brand theme used until the user chooses another.
const defaultTheme = "electro"

// validTheme lists stable, language-independent theme IDs. Frontend tokens use the same IDs.
func validTheme(theme string) bool {
	switch theme {
	case "electro", "aero", "havoc", "glacio":
		return true
	}
	return false
}

// effectiveTheme maps stored preferences to the theme to display. An unknown value from a newer version is not an error.
func effectiveTheme(settings Settings) string {
	if validTheme(settings.Theme) {
		return settings.Theme
	}
	return defaultTheme
}

type SettingsState struct {
	Settings       Settings `json:"settings"`
	Warning        string   `json:"warning,omitempty"`
	LocaleRevision uint64   `json:"localeRevision"`
}

type settingsStore struct {
	mu      sync.Mutex
	path    string
	state   SettingsState
	catalog localeCatalog
	// dirty marks a change held in memory until the next write, such as an opacity slider drag.
	dirty bool
}

func settingsPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserCacheDir()
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "WWlogs", "settings.json")
}

func validateSettings(value Settings) error {
	if value.Version != settingsVersion {
		return errors.New("unsupported settings version")
	}
	if value.DefaultAdapterIPv4 != "" {
		ip := net.ParseIP(value.DefaultAdapterIPv4)
		if ip == nil || ip.To4() == nil || ip.String() != value.DefaultAdapterIPv4 {
			return errors.New("the default adapter must be an IPv4 address")
		}
	}
	return nil
}

func loadSettings(path string, catalog localeCatalog) *settingsStore {
	store := &settingsStore{path: path, catalog: catalog, state: SettingsState{Settings: Settings{Version: settingsVersion, Locale: catalog.FallbackLocale, Overlays: defaultOverlays()}, LocaleRevision: 1}}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && path != "" {
		return store
	}
	if err != nil {
		store.state.Warning = "settings_read_failed"
		return store
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16385))
	var value Settings
	err = decoder.Decode(&value)
	if err == nil {
		err = validateSettings(value)
	}
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = errors.New("unexpected settings content")
		}
	}
	if info, statErr := file.Stat(); statErr != nil || info.Size() > 16384 {
		err = errors.New("invalid settings size")
	}
	if err != nil {
		store.state.Warning = "settings_invalid"
		return store
	}
	if value.Theme != "" && !validTheme(value.Theme) {
		value.Theme = ""
	}
	if !catalog.valid(value.Locale) {
		value.Locale = catalog.FallbackLocale
	}
	if value.Overlays == nil {
		// v0.5 had one Overlay with a different layout. Its visibility carries over; its geometry does not fit either window.
		value.Overlays = defaultOverlays()
		value.Overlays.Combat.Visible = value.ShowOverlayOnStartup
	}
	value.ShowOverlayOnStartup = false
	combatReset := normalizeOverlay(overlayCombat, &value.Overlays.Combat)
	effectsReset := normalizeOverlay(overlayEffects, &value.Overlays.Effects)
	if combatReset || effectsReset {
		store.state.Warning = "overlay_geometry_reset"
	}
	store.state.Settings = value
	return store
}

func (s *settingsStore) current() SettingsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	state.Settings.Overlays = copyOverlays(state.Settings.Overlays)
	return state
}

func copyOverlays(set *OverlaySet) *OverlaySet {
	if set == nil {
		return nil
	}
	copied := *set
	copied.Combat.Placement = copyPlacement(set.Combat.Placement)
	copied.Effects.Placement = copyPlacement(set.Effects.Placement)
	return &copied
}

func copyPlacement(placement *OverlayPlacement) *OverlayPlacement {
	if placement == nil {
		return nil
	}
	copied := *placement
	return &copied
}

func (set *OverlaySet) of(kind string) *OverlayPreferences {
	if kind == overlayEffects {
		return &set.Effects
	}
	return &set.Combat
}

// setDefaultAdapter persists the interface preselected at launch. Every preference has its own update, so settings apply
// at once and no stale form can overwrite another preference.
func (s *settingsStore) setDefaultAdapter(ipv4 string) (SettingsState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.state.Settings
	value.DefaultAdapterIPv4 = ipv4
	if err := validateSettings(value); err != nil {
		return SettingsState{}, err
	}
	return s.writeLocked(value)
}

// setAppearance persists the theme and motion preference. Every window picks them up from its next snapshot.
func (s *settingsStore) setAppearance(theme string, reduceMotion bool) (SettingsState, error) {
	if !validTheme(theme) {
		return SettingsState{}, errors.New("unsupported_theme")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.state.Settings
	value.Theme, value.ReduceMotion = theme, reduceMotion
	return s.writeLocked(value)
}

func (s *settingsStore) setLocale(locale string) (SettingsState, error) {
	if !s.catalog.valid(locale) {
		return SettingsState{}, errors.New("unsupported_locale")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.state.Settings
	value.Locale = locale
	return s.writeLocked(value)
}

// overlay returns a copy of one Overlay's preferences.
func (s *settingsStore) overlay(kind string) OverlayPreferences {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefs := *s.state.Settings.Overlays.of(kind)
	prefs.Placement = copyPlacement(prefs.Placement)
	return prefs
}

// changeOverlay edits one Overlay's preferences. Without persist the change stays in memory until the next write.
func (s *settingsStore) changeOverlay(kind string, persist bool, change func(*OverlayPreferences)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !persist {
		change(s.state.Settings.Overlays.of(kind))
		s.dirty = true
		return nil
	}
	value := s.state.Settings
	value.Overlays = copyOverlays(value.Overlays)
	change(value.Overlays.of(kind))
	_, err := s.writeLocked(value)
	return err
}

// saveOverlayPlacement records where a window is. It runs on its own without a user request, so it must not replace a
// settings file that could not be read; the placement then lasts for this session only.
func (s *settingsStore) saveOverlayPlacement(kind string, placement OverlayPlacement) error {
	if !validPlacement(&placement) {
		return errors.New("invalid_overlay_placement")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.state.Settings.Overlays.of(kind).Placement
	if current != nil && *current == placement && !s.dirty {
		return nil
	}
	if s.state.Warning == "settings_invalid" || s.state.Warning == "settings_read_failed" {
		s.state.Settings.Overlays.of(kind).Placement = &placement
		return nil
	}
	value := s.state.Settings
	value.Overlays = copyOverlays(value.Overlays)
	value.Overlays.of(kind).Placement = &placement
	_, err := s.writeLocked(value)
	return err
}

// flush writes changes that were held in memory. It follows the same rule as saveOverlayPlacement for unreadable files.
func (s *settingsStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty || s.state.Warning == "settings_invalid" || s.state.Warning == "settings_read_failed" {
		return nil
	}
	_, err := s.writeLocked(s.state.Settings)
	return err
}

func (s *settingsStore) setWarning(warning string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Warning = warning
}

func (s *settingsStore) writeLocked(value Settings) (SettingsState, error) {
	if s.path == "" {
		return s.state, errors.New("the user settings directory is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return s.state, errors.New("could not create the user settings directory")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return s.state, errors.New("could not encode settings")
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".settings-*.tmp")
	if err != nil {
		return s.state, errors.New("could not create the settings file")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return s.state, errors.New("could not write settings; previous preferences were preserved")
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return s.state, errors.New("could not replace settings; previous preferences were preserved")
	}
	revision := s.state.LocaleRevision
	if value.Locale != s.state.Settings.Locale {
		revision++
	}
	s.state = SettingsState{Settings: value, LocaleRevision: revision}
	s.dirty = false
	return s.state, nil
}
