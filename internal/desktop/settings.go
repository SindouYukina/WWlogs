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
	Version              int            `json:"version"`
	DefaultAdapterIPv4   string         `json:"defaultAdapterIPv4"`
	ShowOverlayOnStartup bool           `json:"showOverlayOnStartup"`
	Locale               string         `json:"locale"`
	Overlay              *OverlayBounds `json:"overlay,omitempty"`
	// Theme is one of the official theme IDs; empty means the default theme.
	Theme        string `json:"theme,omitempty"`
	ReduceMotion bool   `json:"reduceMotion,omitempty"`
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

type OverlayBounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
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
	store := &settingsStore{path: path, catalog: catalog, state: SettingsState{Settings: Settings{Version: settingsVersion, Locale: catalog.FallbackLocale}, LocaleRevision: 1}}
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
	if value.Overlay != nil && !validOverlayBounds(*value.Overlay) {
		value.Overlay = nil
		store.state.Warning = "overlay_geometry_reset"
	}
	store.state.Settings = value
	return store
}

func (s *settingsStore) current() SettingsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	if state.Settings.Overlay != nil {
		bounds := *state.Settings.Overlay
		state.Settings.Overlay = &bounds
	}
	return state
}

// save replaces the file only after a complete, validated write. Failed writes preserve the previous preferences.
func (s *settingsStore) save(value Settings) (SettingsState, error) {
	if value.Locale != "" && !s.catalog.valid(value.Locale) {
		return SettingsState{}, errors.New("unsupported_locale")
	}
	if err := validateSettings(value); err != nil {
		return SettingsState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Locale, geometry and appearance have dedicated updates; a stale Settings form must not overwrite them.
	value.Locale = s.state.Settings.Locale
	value.Overlay = s.state.Settings.Overlay
	value.Theme = s.state.Settings.Theme
	value.ReduceMotion = s.state.Settings.ReduceMotion
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

func (s *settingsStore) setOverlay(bounds OverlayBounds) error {
	if !validOverlayBounds(bounds) {
		return errors.New("invalid_overlay_bounds")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Settings.Overlay != nil && *s.state.Settings.Overlay == bounds {
		return nil
	}
	value := s.state.Settings
	value.Overlay = &bounds
	if s.state.Warning == "settings_invalid" || s.state.Warning == "settings_read_failed" {
		s.state.Settings.Overlay = &bounds
		return nil
	}
	_, err := s.writeLocked(value)
	return err
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
	return s.state, nil
}
