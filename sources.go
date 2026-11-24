package main

import (
	"context"
	"sync"
	"time"

	"github.com/marekp97/ucsignal/internal/logsrc"
)

// message is what the log sources deliver to the panel or to the plain output.
type message interface{ message() }

type (
	// eventsMsg carries live events from `log stream`, batched once per second.
	eventsMsg []logsrc.Event
	// historyMsg carries the result of one `log show` query.
	historyMsg struct {
		events   []logsrc.Event
		complete bool
		failed   bool
		fresh    bool
		list     bool
	}
	errMsg struct{ err error }
)

func (eventsMsg) message()  {}
func (historyMsg) message() {}
func (errMsg) message()     {}

// startSources runs the queries in parallel: every `log show` has a fixed cost of about 0.8 s, so running them in sequence would make startup several times slower.
// The caller waits on the returned group before exiting; otherwise `log stream` would be left running.
func startSources(ctx context.Context, since string, send func(message)) *sync.WaitGroup {
	var wg sync.WaitGroup
	show := func(since string, info bool, predicate string, msg historyMsg) {
		events, err := logsrc.Show(ctx, since, info, predicate)
		if err != nil {
			send(errMsg{err})
		}
		msg.failed = err != nil || ctx.Err() != nil
		msg.events = events
		send(msg)
	}
	// The system keeps readings for about 11 min; querying 10 min costs the same as 2.5 min (about 0.9 s) and fills the detail chart.
	wg.Go(func() { show("-10m", true, logsrc.SamplesPredicate, historyMsg{fresh: true}) })
	if since == "-12h" {
		wg.Go(func() { show("-30m", false, logsrc.EventsPredicate, historyMsg{list: true}) })
	}
	wg.Go(func() { show(since, false, logsrc.EventsPredicate, historyMsg{complete: true}) })
	// Log events reach the UI in batches once per second, in step with the panel clock: every message redraws the whole screen.
	batch := make(chan logsrc.Event, 256)
	// `log stream` can exit, for example after the logging service restarts; it is then started again.
	wg.Go(func() {
		for {
			err := logsrc.Stream(ctx, func(ev logsrc.Event) {
				select {
				case batch <- ev:
				case <-ctx.Done():
				}
			})
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				send(errMsg{err})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	})
	wg.Go(func() {
		flush := time.NewTicker(time.Second)
		defer flush.Stop()
		var pending []logsrc.Event
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-batch:
				pending = append(pending, ev)
			case <-flush.C:
				if len(pending) > 0 {
					send(eventsMsg(pending))
					pending = nil
				}
			}
		}
	})
	return &wg
}

// waitSources gives the sources a second to stop; `log` normally exits at once when its context is cancelled.
func waitSources(wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
