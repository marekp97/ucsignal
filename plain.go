package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/i18n"
	"github.com/marekp97/ucsignal/internal/logsrc"
	"github.com/marekp97/ucsignal/internal/ui"
)

// runPlain prints every reading and every list change on its own line until ctx is cancelled.
func runPlain(ctx context.Context, stdout, stderr io.Writer, eng *engine.Engine, opts ui.Options, since string) {
	ch := make(chan message, 256)
	wg := startSources(ctx, since, func(m message) {
		select {
		case ch <- m:
		case <-ctx.Done():
		}
	})
	fmt.Fprintln(stdout, i18n.T("plain.header", opts.Host))
	listed := map[string]engine.ListState{}
	for {
		select {
		case <-ctx.Done():
			saveHistory(eng)
			waitSources(wg)
			return
		case msg := <-ch:
			switch msg := msg.(type) {
			case historyMsg:
				eng.Add(msg.events...)
				if msg.complete && !msg.failed {
					eng.SetHistoryComplete()
					saveHistory(eng)
				}
				for _, d := range eng.Snapshot(time.Now()).Devices {
					listed[d.ID] = d.List
				}
			case errMsg:
				fmt.Fprintln(stderr, i18n.T("err.log", msg.err))
			case eventsMsg:
				for _, ev := range msg {
					eng.Add(ev)
					printEvent(stdout, eng.Snapshot(time.Now()), ev, opts, listed)
				}
			}
		}
	}
}

// printEvent prints the reading itself and any list change it caused; listed remembers the last known state per device.
func printEvent(w io.Writer, snap engine.Snapshot, ev logsrc.Event, opts ui.Options, listed map[string]engine.ListState) {
	for _, d := range snap.Devices {
		if !opts.Visible(d) {
			continue
		}
		if ev.Kind == logsrc.Sample && d.ID == ev.ID {
			fmt.Fprintln(w, ui.PlainSample(d, ev.RSSI, ev.Time, snap.Rule, opts))
		}
		if prev, ok := listed[d.ID]; ok && prev != d.List && d.List != engine.ListUnknown {
			fmt.Fprintln(w, ui.PlainChange(d, ev.Time, d.List == engine.Listed, opts))
		}
		listed[d.ID] = d.List
	}
}
