// Package settings stores the user's language choice.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings are the choices kept between runs.
type Settings struct {
	Lang string `json:"lang,omitempty"`
}

func path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ucsignal", "settings.json")
}

// Load returns the saved settings, or the zero value when there are none.
func Load() Settings {
	var s Settings
	if data, err := os.ReadFile(path()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// Save writes the settings to ~/.config/ucsignal/settings.json.
func Save(s Settings) error {
	p := path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o600)
}
