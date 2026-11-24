package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/i18n"
	"github.com/marekp97/ucsignal/internal/logsrc"
	"github.com/marekp97/ucsignal/internal/ui"
)

// replay prints one frame of a recording as the panel looked at the given time of day, or at its end.
func replay(w io.Writer, path, at string, opts ui.Options, view *ui.Renderer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	events, err := logsrc.ReadAll(f)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return errors.New(i18n.T("err.emptyFile", path))
	}
	first := events[0].Time
	now := events[len(events)-1].Time
	if at != "" {
		now, err = time.ParseInLocation("2006-01-02 15:04:05", first.Format("2006-01-02 ")+at, first.Location())
		if err != nil {
			return errors.New(i18n.T("err.at", err))
		}
	}
	eng := engine.New(first)
	for _, ev := range events {
		if !ev.Time.After(now) {
			eng.Add(ev)
		}
	}
	eng.SetHistoryComplete()
	opts.Replay = true
	opts.Host = i18n.T("recording", filepath.Base(path))
	width := 120
	if tty, ok := terminal(w); ok {
		if cols, _, err := term.GetSize(tty.Fd()); err == nil && cols > 0 {
			width = cols
		}
	}
	_, err = lipgloss.Fprint(w, view.Render(eng.Snapshot(now), opts, width))
	return err
}
