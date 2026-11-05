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
	Version              int    `json:"version"`
	DefaultAdapterIPv4   string `json:"defaultAdapterIPv4"`
	ShowOverlayOnStartup bool   `json:"showOverlayOnStartup"`
}

type SettingsState struct {
	Settings Settings `json:"settings"`
	Warning  string   `json:"warning,omitempty"`
}

type settingsStore struct {
	mu    sync.Mutex
	path  string
	state SettingsState
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

func loadSettings(path string) *settingsStore {
	store := &settingsStore{path: path, state: SettingsState{Settings: Settings{Version: settingsVersion}}}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && path != "" {
		return store
	}
	if err != nil {
		store.state.Warning = "Settings could not be read. Defaults are in use."
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
		store.state.Warning = "Settings are invalid or unsupported. Defaults are in use; save to replace them."
		return store
	}
	store.state.Settings = value
	return store
}

func (s *settingsStore) current() SettingsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// save replaces the file only after a complete, validated write. Failed writes preserve the previous preferences.
func (s *settingsStore) save(value Settings) (SettingsState, error) {
	if err := validateSettings(value); err != nil {
		return SettingsState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.state = SettingsState{Settings: value}
	return s.state, nil
}
