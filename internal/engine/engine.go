// Package engine turns log events into the per-device state shown by ucsignal: list membership, time left before a drop and drop history.
package engine

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/marekp97/ucsignal/internal/logsrc"
)

const (
	defaultBorder  = -61
	defaultOffset  = 3
	defaultTimeout = 120 * time.Second
	// The nearbyd event and the UC list change are logged in the same millisecond; 50 ms is a margin.
	matchWindow = 50 * time.Millisecond
	historySpan = 12 * time.Hour
	sampleKeep  = 10 * time.Minute
	silentAfter = 90 * time.Second
	// A device not heard for longer is usually someone else's hardware passing by; it should not clutter the panel.
	forgetAfter = 30 * time.Minute
)

// NoReading is the value of Device.Best and Device.Median when the window holds no readings.
const NoReading = -999

// Rule is the proximity rule of Universal Control: readings of Border dBm or stronger count, and a device
// without one for Timeout is dropped. Calibrated counts the nearbyd decisions that confirmed the values.
type Rule struct {
	Border     int
	Offset     int
	Timeout    time.Duration
	Calibrated int
}

// ReturnAt is the weakest reading that brings a dropped device back at once.
func (r Rule) ReturnAt() int { return r.Border + r.Offset + 1 }

// ListState says whether a device is on the Universal Control list.
type ListState int

// ListUnknown means the log holds no Universal Control list yet.
const (
	ListUnknown ListState = iota
	Listed
	Unlisted
)

// Status is the verdict the panel shows for a device.
type Status int

// Values of Status.
const (
	StatusOK     Status = iota // on the list with signal to spare
	StatusEdge                 // on the list, median reading below the threshold
	StatusSoon                 // on the list, less than half of the timeout left
	StatusOut                  // dropped from the list
	StatusSilent               // not heard for more than silentAfter
	StatusFar                  // heard, but never on the list in the history window
)

// Device is the state of one Mac or iPad at the moment of a snapshot.
type Device struct {
	ID, Model    string
	List         ListState
	OutSince     time.Time
	StrongSeen   bool
	StrongAgo    time.Duration
	Left         time.Duration
	Total, Good  int
	Best, Median int
	Last         int
	LastAgo      time.Duration
	Points       []Point
	Drops        int
	LastDrop     time.Time
	Level        int
	Status       Status
}

// Point is one reading on the chart.
type Point struct {
	Ago  time.Duration
	RSSI int
}

// Snapshot is everything the panel shows at one moment.
type Snapshot struct {
	Now             time.Time
	Rule            Rule
	Devices         []Device
	HistoryComplete bool
}

type sample struct {
	t    time.Time
	rssi int
}

type change struct {
	t   time.Time
	out bool
}

// Engine collects log events and derives the state of every device from them. It is not safe for concurrent use.
type Engine struct {
	observedFrom    time.Time
	historyComplete bool
	models          map[string]string
	order           []string
	samples         map[string][]sample
	events          []logsrc.Event
	seen            map[string]bool
	dirty           bool

	listKnown  bool
	list       map[string]bool
	everListed map[string]bool
	drops      map[string][]time.Time
	lastChg    map[string]change
	borders    map[int]int
	offsets    map[int]int
	timeouts   map[time.Duration]int
}

// New returns an empty engine; observedFrom is how far back the readings it is going to receive reach.
func New(observedFrom time.Time) *Engine {
	return &Engine{
		observedFrom: observedFrom,
		models:       map[string]string{},
		samples:      map[string][]sample{},
		seen:         map[string]bool{},
	}
}

// SetHistoryComplete records that the whole history window has been loaded.
func (e *Engine) SetHistoryComplete() { e.historyComplete = true }

// HistoryComplete reports whether the whole history window has been loaded.
func (e *Engine) HistoryComplete() bool { return e.historyComplete }

func eventKey(ev logsrc.Event) string {
	switch ev.Kind {
	case logsrc.Nearby:
		return fmt.Sprintf("N%d%s", ev.Time.UnixMicro(), strings.Join(ev.List, ","))
	case logsrc.Timeout:
		return fmt.Sprintf("T%d%g", ev.Time.UnixMicro(), ev.Secs)
	default:
		return fmt.Sprintf("E%d%g%g", ev.Time.UnixMicro(), ev.Border, ev.Offset)
	}
}

func isDevice(model string) bool {
	return strings.HasPrefix(model, "Mac") || strings.HasPrefix(model, "iMac") || strings.HasPrefix(model, "iPad")
}

// Add takes events in any order and ignores the ones it has already seen.
func (e *Engine) Add(evs ...logsrc.Event) {
	for _, ev := range evs {
		if ev.Kind == logsrc.Sample {
			e.addSample(ev)
			continue
		}
		key := eventKey(ev)
		if e.seen[key] {
			continue
		}
		e.seen[key] = true
		e.events = append(e.events, ev)
		e.dirty = true
	}
}

func (e *Engine) addSample(ev logsrc.Event) {
	if !isDevice(ev.Model) {
		return
	}
	if _, ok := e.models[ev.ID]; !ok {
		e.models[ev.ID] = ev.Model
		e.order = append(e.order, ev.ID)
	}
	list := e.samples[ev.ID]
	i := sort.Search(len(list), func(i int) bool { return list[i].t.After(ev.Time) })
	for j := i - 1; j >= 0 && list[j].t.Equal(ev.Time); j-- {
		if list[j].rssi == ev.RSSI {
			return
		}
	}
	list = slices.Insert(list, i, sample{ev.Time, ev.RSSI})
	if cut := sort.Search(len(list), func(i int) bool { return !list[i].t.Before(ev.Time.Add(-sampleKeep)) }); cut > 0 {
		list = list[cut:]
	}
	e.samples[ev.ID] = list
}

// Prune removes events older than 12 h and keeps the last list before that boundary as a reference point.
func (e *Engine) Prune(now time.Time) {
	e.recompute()
	cutoff := now.Add(-historySpan)
	keepFrom := 0
	for i, ev := range e.events {
		if !ev.Time.Before(cutoff) {
			break
		}
		if ev.Kind == logsrc.Nearby {
			keepFrom = i
		}
	}
	for _, ev := range e.events[:keepFrom] {
		delete(e.seen, eventKey(ev))
	}
	if keepFrom > 0 {
		e.events = slices.Clone(e.events[keepFrom:])
		e.dirty = true
	}
}

// Samples returns the readings from the last few minutes so that the next start can show something immediately.
func (e *Engine) Samples(now time.Time) []logsrc.Event {
	var out []logsrc.Event
	for _, id := range e.order {
		for _, s := range e.samples[id] {
			if now.Sub(s.t) <= 3*time.Minute {
				out = append(out, logsrc.Event{Kind: logsrc.Sample, Time: s.t, ID: id, Model: e.models[id], RSSI: s.rssi})
			}
		}
	}
	return out
}

// Events returns the events from the last 12 h for the cache, together with the last list before that boundary:
// UC logs the list only when it changes, so without it the list state after 12 h without changes would be unknown.
func (e *Engine) Events(now time.Time) []logsrc.Event {
	e.recompute()
	cutoff := now.Add(-historySpan)
	var out []logsrc.Event
	baseline := -1
	for i, ev := range e.events {
		switch {
		case !ev.Time.Before(cutoff):
			out = append(out, ev)
		case ev.Kind == logsrc.Nearby:
			baseline = i
		}
	}
	if baseline >= 0 {
		out = append([]logsrc.Event{e.events[baseline]}, out...)
	}
	return out
}

func (e *Engine) recompute() {
	if !e.dirty {
		return
	}
	e.dirty = false
	sort.SliceStable(e.events, func(i, j int) bool { return e.events[i].Time.Before(e.events[j].Time) })
	e.listKnown, e.list, e.everListed = false, nil, map[string]bool{}
	e.drops, e.lastChg = map[string][]time.Time{}, map[string]change{}
	e.borders, e.offsets, e.timeouts = map[int]int{}, map[int]int{}, map[time.Duration]int{}
	for i, ev := range e.events {
		if ev.Kind != logsrc.Nearby {
			continue
		}
		next := map[string]bool{}
		for _, id := range ev.List {
			next[id] = true
			e.everListed[id] = true
		}
		if e.listKnown {
			for id := range e.list {
				if !next[id] {
					e.drops[id] = append(e.drops[id], ev.Time)
					e.lastChg[id] = change{ev.Time, true}
					if m := e.match(i, logsrc.Timeout); m >= 0 {
						e.timeouts[time.Duration(e.events[m].Secs*float64(time.Second))]++
					}
				}
			}
			for id := range next {
				if !e.list[id] {
					e.lastChg[id] = change{ev.Time, false}
					if m := e.match(i, logsrc.EarlyIn); m >= 0 {
						e.borders[int(e.events[m].Border)]++
						e.offsets[int(e.events[m].Offset)]++
					}
				}
			}
		}
		e.list, e.listKnown = next, true
	}
}

func (e *Engine) match(i int, kind logsrc.Kind) int {
	t := e.events[i].Time
	for j := i - 1; j >= 0 && t.Sub(e.events[j].Time) <= matchWindow; j-- {
		if e.events[j].Kind == kind {
			return j
		}
	}
	return -1
}

func mode[K cmp.Ordered](tally map[K]int, fallback K) (K, int) {
	best, bestN, total := fallback, 0, 0
	for k, n := range tally {
		total += n
		if n > bestN || n == bestN && best != fallback && (k == fallback || k < best) {
			best, bestN = k, n
		}
	}
	return best, total
}

// Rule returns the thresholds confirmed by the nearbyd decisions seen so far, or the defaults.
func (e *Engine) Rule() Rule {
	e.recompute()
	border, nb := mode(e.borders, defaultBorder)
	offset, _ := mode(e.offsets, defaultOffset)
	timeout, nt := mode(e.timeouts, defaultTimeout)
	return Rule{Border: border, Offset: offset, Timeout: timeout, Calibrated: nb + nt}
}

// Snapshot computes the state of every device heard within forgetAfter.
func (e *Engine) Snapshot(now time.Time) Snapshot {
	rule := e.Rule()
	snap := Snapshot{Now: now, Rule: rule, HistoryComplete: e.historyComplete}
	for _, id := range e.order {
		if d := e.device(id, now, rule); d.LastAgo <= forgetAfter {
			snap.Devices = append(snap.Devices, d)
		}
	}
	return snap
}

func (e *Engine) device(id string, now time.Time, rule Rule) Device {
	d := Device{ID: id, Model: e.models[id], Best: NoReading, Median: NoReading}
	list := e.samples[id]
	var strongT time.Time
	var window []int
	for _, s := range slices.Backward(list) {
		if !d.StrongSeen && s.rssi >= rule.Border {
			d.StrongSeen, strongT = true, s.t
		}
		if now.Sub(s.t) <= rule.Timeout {
			window = append(window, s.rssi)
			if s.rssi >= rule.Border {
				d.Good++
			}
			d.Best = max(d.Best, s.rssi)
		}
		d.Points = append(d.Points, Point{Ago: now.Sub(s.t), RSSI: s.rssi})
	}
	slices.Reverse(d.Points)
	d.Total = len(window)
	if len(window) > 0 {
		slices.Sort(window)
		d.Median = window[(len(window)-1)/2]
	}
	if n := len(list); n > 0 {
		d.Last, d.LastAgo = list[n-1].rssi, now.Sub(list[n-1].t)
	}
	if !d.StrongSeen {
		// Readings older than sampleKeep are gone, so the time without a strong one is counted only as far back as they reach.
		strongT = e.observedFrom
		if n := len(list); n > 0 {
			if list[0].t.Before(strongT) {
				strongT = list[0].t
			}
			if kept := list[n-1].t.Add(-sampleKeep); strongT.Before(kept) {
				strongT = kept
			}
		}
	}
	d.StrongAgo = now.Sub(strongT)
	d.Left = rule.Timeout - d.StrongAgo

	switch {
	case !e.listKnown:
		d.List = ListUnknown
	case e.list[id]:
		d.List = Listed
	default:
		d.List = Unlisted
		if c, ok := e.lastChg[id]; ok && c.out {
			d.OutSince = c.t
		}
	}
	for _, t := range e.drops[id] {
		if !t.Before(now.Add(-historySpan)) {
			d.Drops++
			if t.After(d.LastDrop) {
				d.LastDrop = t
			}
		}
	}

	b := rule.Border
	switch m := d.Median; {
	case m >= b+11:
		d.Level = 5
	case m >= b+5:
		d.Level = 4
	case m >= b:
		d.Level = 3
	case m >= b-4:
		d.Level = 2
	case m >= b-9:
		d.Level = 1
	}
	switch {
	case len(list) == 0 || d.LastAgo > silentAfter:
		d.Status, d.Level = StatusSilent, 0
	case d.List == Unlisted && !e.everListed[id]:
		d.Status = StatusFar
	case d.List == Unlisted:
		d.Status = StatusOut
	case d.List == ListUnknown && d.Left <= 0:
		d.Status = StatusOut
	case d.Left <= rule.Timeout/2:
		d.Status = StatusSoon
	case d.Median < b:
		d.Status = StatusEdge
	default:
		d.Status = StatusOK
	}
	return d
}
