package main

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/i18n"
	"github.com/marekp97/ucsignal/internal/settings"
	"github.com/marekp97/ucsignal/internal/ui"
)

type tickMsg time.Time

type model struct {
	eng       *engine.Engine
	view      *ui.Renderer
	opts      ui.Options
	width     int
	err       string
	lastPrune time.Time
	started   time.Time
	firstRun  bool
	steps     ui.LoadSteps
	meter     map[string]float64
	theme     string
	dark      *bool
	bgAsked   time.Time
	animating bool
	pulsing   bool
}

// runPanel shows the live panel until the user quits or ctx is cancelled.
func runPanel(ctx context.Context, eng *engine.Engine, view *ui.Renderer, opts ui.Options, since, theme string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	now := time.Now()
	opts.Loading = true
	m := &model{eng: eng, view: view, opts: opts, lastPrune: now, started: now, firstRun: since == "-12h", meter: map[string]float64{}, theme: theme}
	// Bubble Tea only renders on frame ticks: 5 fps delayed the arrow keys in the language list by up to 200 ms.
	// 30 fps keeps up with key repeat; measured side by side: 5 fps 0.53%, 20 fps 0.66%, 30 fps 0.75% of one core.
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithFPS(30))
	wg := startSources(ctx, since, func(msg message) { p.Send(msg) })
	_, err := p.Run()
	interrupted := ctx.Err() != nil
	cancel()
	saveHistory(eng)
	waitSources(wg)
	if interrupted {
		return nil
	}
	return err
}

// The clock speeds up only while animating, loading or pulsing; at rest it wakes the process once per second.
func (m *model) tick() tea.Cmd {
	every := time.Second
	switch {
	case m.animating, m.loading():
		every = 200 * time.Millisecond
	case m.pulsing:
		every = 500 * time.Millisecond
	}
	return tea.Tick(every, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// animate moves the displayed bar level towards the real one, damped like a spring that does not oscillate.
func (m *model) animate(now time.Time) {
	wasAnimating := m.animating
	m.animating, m.pulsing = false, false
	for _, d := range m.eng.Snapshot(now).Devices {
		if d.Status == engine.StatusSoon || d.Status == engine.StatusOut {
			m.pulsing = true
		}
		if d.Median == engine.NoReading {
			continue
		}
		target := ui.MeterFraction(d.Median)
		shown, seen := m.meter[d.ID]
		diff := target - shown
		// Small median changes jump directly; animation starts at about 1.5 dBm because each one wakes the process 5 times per second.
		switch {
		case diff > 0.004 || diff < -0.004:
			if !seen || wasAnimating || diff > 0.03 || diff < -0.03 {
				m.meter[d.ID] = shown + diff*0.5
				m.animating = true
			} else {
				m.meter[d.ID] = target
			}
		default:
			m.meter[d.ID] = target
		}
	}
}

func (m *model) loading() bool {
	return m.opts.Loading || (!m.eng.HistoryComplete() && !m.opts.HistoryFailed)
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.tick(), tea.RequestBackgroundColor)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.opts.Picking {
			return m, m.pickLanguage(msg.String())
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "v":
			m.opts.Detail = !m.opts.Detail
		case "l":
			m.opts.Picking = true
			for i, l := range i18n.Languages() {
				if l == i18n.Current() {
					m.opts.Picker = i
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.BackgroundColorMsg:
		if dark := msg.IsDark(); m.theme == "" && (m.dark == nil || *m.dark != dark) {
			m.view.SetTheme(dark)
			m.dark = &dark
		}
	case eventsMsg:
		m.eng.Add(msg...)
		if !m.opts.HistoryFailed {
			m.err = ""
		}
	case historyMsg:
		m.addHistory(msg)
	case errMsg:
		m.err = msg.err.Error()
		m.opts.Loading = false
	case tickMsg:
		now := time.Time(msg)
		if now.Sub(m.lastPrune) > time.Minute {
			m.eng.Prune(now)
			saveHistory(m.eng)
			m.lastPrune = now
		}
		m.animate(now)
		// Bubble Tea is not notified when the terminal theme changes, so the background is queried every 5 s.
		if m.theme == "" && now.Sub(m.bgAsked) > 5*time.Second {
			m.bgAsked = now
			return m, tea.Batch(m.tick(), tea.RequestBackgroundColor)
		}
		return m, m.tick()
	}
	return m, nil
}

func (m *model) pickLanguage(key string) tea.Cmd {
	langs := i18n.Languages()
	switch key {
	case "ctrl+c", "q":
		return tea.Quit
	case "up", "k":
		m.opts.Picker = (m.opts.Picker + len(langs) - 1) % len(langs)
	case "down", "j", "tab":
		m.opts.Picker = (m.opts.Picker + 1) % len(langs)
	case "enter", "space":
		i18n.Set(langs[m.opts.Picker])
		// The choice already applies to this run; if it cannot be saved, the next run starts in the old language.
		_ = settings.Save(settings.Settings{Lang: langs[m.opts.Picker].Code})
		m.opts.Picking = false
	case "esc", "l":
		m.opts.Picking = false
	}
	return nil
}

func (m *model) addHistory(msg historyMsg) {
	m.eng.Add(msg.events...)
	if msg.fresh {
		m.opts.Loading = false
		m.steps.Samples = true
	}
	if msg.list || msg.complete {
		m.steps.List = true
	}
	if !msg.complete {
		return
	}
	m.steps.History = true
	if msg.failed {
		m.opts.HistoryFailed = true
		return
	}
	m.eng.SetHistoryComplete()
	saveHistory(m.eng)
}

func (m *model) View() tea.View {
	now := time.Now()
	m.opts.Spinner, m.opts.FirstRun = "", nil
	m.opts.Meter, m.opts.Pulse = m.meter, now.UnixMilli()/500%2 == 1
	if m.loading() {
		m.opts.Spinner = ui.SpinnerFrame(now)
	}
	if m.firstRun && !m.eng.HistoryComplete() && !m.opts.HistoryFailed {
		steps := m.steps
		steps.Elapsed = now.Sub(m.started)
		m.opts.FirstRun = &steps
	}
	out := m.view.Render(m.eng.Snapshot(now), m.opts, m.width)
	if m.err != "" {
		out += "\n " + i18n.T("err.log", m.err) + "\n"
	}
	v := tea.NewView(out)
	v.AltScreen = true
	return v
}
