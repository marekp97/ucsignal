// Package history caches recent events between runs so the next start does not have to read 12 hours of the system log again.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/marekp97/ucsignal/internal/logsrc"
)

const span = 12 * time.Hour

type file struct {
	Saved    time.Time      `json:"saved"`
	Complete bool           `json:"complete"`
	Events   []logsrc.Event `json:"events"`
	Samples  []logsrc.Event `json:"samples"`
}

func path() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ucsignal", "history.json"), nil
}

// Load returns the saved events, the latest readings and the argument for `log show`: the full 12 h or only what was added since the save.
func Load(now time.Time) (events, samples []logsrc.Event, since string) {
	p, err := path()
	if err != nil {
		return nil, nil, "-12h"
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, "-12h"
	}
	var f file
	if json.Unmarshal(data, &f) != nil || now.Sub(f.Saved) >= span || f.Saved.After(now) {
		return nil, nil, "-12h"
	}
	if !f.Complete {
		return f.Events, f.Samples, "-12h"
	}
	return f.Events, f.Samples, f.Saved.Add(-5 * time.Second).Local().Format("2006-01-02 15:04:05")
}

// Save writes the state even when the history is incomplete so the next start has something to show; Load then fetches the full 12 h.
func Save(events, samples []logsrc.Event, complete bool, now time.Time) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(file{Saved: now, Complete: complete, Events: events, Samples: samples})
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
