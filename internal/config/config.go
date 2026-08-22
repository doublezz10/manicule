// Package config persists manicule's settings as JSON in the user config dir.
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Settings is the full persisted application configuration.
type Settings struct {
	// LibraryPath is the root folder of the user's book library.
	LibraryPath string `json:"library_path"`

	// SourcesEnabled maps source ID → enabled. Tier 1 sources default on;
	// Tier 2 sources additionally require Tier2Acknowledged.
	SourcesEnabled    map[string]bool `json:"sources_enabled"`
	Tier2Acknowledged bool            `json:"tier2_acknowledged"`

	// SourceCredentials holds per-source credentials keyed by source ID.
	// Standard Ebooks uses {"email": "..."} (blank password per SE policy).
	SourceCredentials map[string]map[string]string `json:"source_credentials,omitempty"`

	CleanOnImport           bool `json:"clean_on_import"`            // auto-clean derivatives on import (default true)
	ImageMaxWidth           int  `json:"image_max_width"`            // px cap for cleaned images (default 1440)
	DeleteSourceAfterImport bool `json:"delete_source_after_import"` // watch-folder remove toggle, default OFF

	WatchEnabled bool   `json:"watch_enabled"`
	WatchPath    string `json:"watch_path,omitempty"`

	ServerEnabled bool   `json:"server_enabled"` // OPDS server on while app runs
	ServerPort    int    `json:"server_port"`    // default 8787
	AuthEnabled   bool   `json:"auth_enabled"`   // ON by default with tiny creds
	Pin           string `json:"pin"`            // 4-char numeric PIN, username fixed "reader"

	LaunchAtLogin bool `json:"launch_at_login"`

	FleetOverrides map[string]string `json:"fleet_overrides,omitempty"` // sourceID → manual endpoint base URL

	WizardDone bool `json:"wizard_done"`
}

func (s *Settings) applyDefaults() {
	if s.SourcesEnabled == nil {
		s.SourcesEnabled = map[string]bool{}
	}
	if _, ok := s.SourcesEnabled["gutendex"]; !ok {
		s.SourcesEnabled["gutendex"] = true // Tier 1 default-on
	}
	if _, ok := s.SourcesEnabled["openlibrary"]; !ok {
		s.SourcesEnabled["openlibrary"] = true // Tier 1 default-on
	}
	if s.ImageMaxWidth <= 0 {
		s.ImageMaxWidth = 1440
	}
	if s.ServerPort == 0 {
		s.ServerPort = 8787
	}
}

// Manager loads and saves settings.json.
type Manager struct {
	path string
}

func NewManager() (*Manager, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "manicule")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{path: filepath.Join(dir, "settings.json")}, nil
}

func (m *Manager) Path() string { return m.path }

// Load reads settings; a missing file yields a fresh install with product
// defaults (cleaning on, server on, auth on, random PIN) already applied.
func (m *Manager) Load() (*Settings, error) {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		s := &Settings{
			CleanOnImport:     true,
			ServerEnabled:     true,
			AuthEnabled:       true,
			Tier2Acknowledged: false,
		}
		s.applyDefaults()
		s.Pin = GeneratePIN()
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	s.applyDefaults()
	return &s, nil
}

func (m *Manager) Save(s *Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// GeneratePIN returns a random 4-digit PIN sized for the device's virtual keyboard.
func GeneratePIN() string {
	const digits = "0123456789"
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = digits[n%10]
			n /= 10
		}
	} else {
		for i, v := range b {
			b[i] = digits[v%10]
		}
	}
	return string(b)
}
