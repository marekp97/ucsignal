// Package ui renders the terminal panel and the plain line-by-line output.
package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/i18n"
)

// LoadSteps tracks which parts of the first, slow start have finished.
type LoadSteps struct {
	Samples, List, History bool
	Elapsed                time.Duration
}

// Options is what the caller decides about one frame: the view, the filter, the names and the transient states.
type Options struct {
	Detail        bool
	Loading       bool
	HistoryFailed bool
	Spinner       string
	FirstRun      *LoadSteps
	Filter        string
	Names         map[string]string
	Host          string
	Replay        bool
	Meter         map[string]float64
	Pulse         bool
	Picker        int
	Picking       bool
}

var (
	bold  = lipgloss.NewStyle().Bold(true)
	faint = lipgloss.NewStyle().Faint(true)
)

const (
	simpleSpan = 3 * time.Minute
	detailSpan = 10 * time.Minute
	cardMin    = 44
	minWidth   = 36
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerFrame returns the spinner glyph for the given moment.
func SpinnerFrame(t time.Time) string {
	return spinnerFrames[(t.UnixMilli()/200)%int64(len(spinnerFrames))]
}

func (r *Renderer) loadingText(o Options, text string) string {
	if o.Spinner == "" {
		return faint.Render(text)
	}
	return r.yellow.Render(o.Spinner) + " " + faint.Render(text)
}

// Name returns the user's name for a device, or its model identifier.
func (o Options) Name(d engine.Device) string {
	if n, ok := o.Names[d.ID]; ok {
		return n
	}
	return d.Model
}

// Visible reports whether a device matches the name filter.
func (o Options) Visible(d engine.Device) bool {
	if o.Filter == "" {
		return true
	}
	hay := strings.ToLower(o.Name(d) + " " + d.Model + " " + d.ID)
	return strings.Contains(hay, strings.ToLower(o.Filter))
}

func clock(t time.Time) string { return t.Format("15:04:05") }

func pad(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func (r *Renderer) statusColor(s engine.Status) rgb {
	switch s {
	case engine.StatusOK:
		return r.pal.green
	case engine.StatusEdge, engine.StatusSoon:
		return r.pal.yellow
	case engine.StatusOut:
		return r.pal.red
	default:
		return r.pal.gray
	}
}

func levelText(d engine.Device) string {
	if d.Status == engine.StatusSilent {
		return i18n.T("level.none")
	}
	return i18n.T(fmt.Sprintf("level.%d", d.Level))
}

func statusText(d engine.Device) string {
	switch d.Status {
	case engine.StatusOK:
		return i18n.T("status.ok")
	case engine.StatusEdge:
		return i18n.T("status.edge")
	case engine.StatusSoon:
		return i18n.T("status.soon", max(0, int(d.Left.Seconds())))
	case engine.StatusOut:
		return i18n.T("status.out")
	case engine.StatusFar:
		return i18n.T("status.far")
	default:
		return i18n.T("status.silent", silentFor(d))
	}
}

func silentFor(d engine.Device) string {
	if d.LastAgo < time.Minute {
		return i18n.T("dur.s", int(d.LastAgo.Seconds()))
	}
	return i18n.T("dur.min", int(d.LastAgo.Minutes()))
}

func farFor(d engine.Device) string {
	if d.Median > engine.NoReading {
		return fmt.Sprintf("%d dBm", d.Median)
	}
	return fmt.Sprintf("%d dBm %s", d.Last, i18n.T("ago", silentFor(d)))
}

func period(o Options) string {
	if o.Replay {
		return i18n.T("period.replay")
	}
	return i18n.T("period.12h")
}

func (r *Renderer) historyNote(s engine.Snapshot, d engine.Device, o Options) string {
	switch {
	case o.HistoryFailed && !s.HistoryComplete:
		return faint.Render(i18n.T("history.failed"))
	case !s.HistoryComplete:
		return r.loadingText(o, i18n.T("history.loading"))
	case d.Status == engine.StatusOut && !d.OutSince.IsZero():
		return faint.Render(i18n.T("history.outSince", clock(d.OutSince), d.Drops, period(o)))
	case d.Drops > 0:
		return faint.Render(i18n.T("history.drops", d.Drops, period(o), clock(d.LastDrop)))
	default:
		return faint.Render(i18n.T("history.none", period(o)))
	}
}

func (r *Renderer) statusLine(d engine.Device, o Options, inner int) string {
	c := r.statusColor(d.Status)
	dot := c.style().Render("●")
	if o.Pulse && (d.Status == engine.StatusSoon || d.Status == engine.StatusOut) {
		dot = c.style().Faint(true).Render("○")
	}
	left := dot + " " + c.style().Bold(true).Render(statusText(d))
	right := faint.Render(levelText(d))
	if gap := inner - lipgloss.Width(left) - lipgloss.Width(right); gap > 1 {
		return left + strings.Repeat(" ", gap) + right
	}
	return left
}

func (r *Renderer) signalLine(d engine.Device, o Options, rule engine.Rule, inner int) string {
	if d.Median == engine.NoReading {
		return faint.Render(i18n.T("signal.none", int(rule.Timeout.Seconds())))
	}
	frac, ok := o.Meter[d.ID]
	if !ok {
		frac = MeterFraction(d.Median)
	}
	margin := d.Median - rule.Border
	note := r.green.Render(i18n.T("signal.margin", margin))
	if margin < 0 {
		note = r.red.Render(i18n.T("signal.short", -margin))
	}
	return r.meter(frac, max(8, min(20, inner-24)), rule) + "  " + bold.Render(fmt.Sprintf("%d dBm", d.Median)) + "  " + note
}

// frame is the shared content column: the header, cards and texts have the same width and equal margins.
type frame struct{ content, margin int }

const maxContent = 150

func newFrame(width int) frame {
	content := min(width-2, maxContent)
	return frame{content: content, margin: (width - content) / 2}
}

func (f frame) indent(block string) string {
	m := strings.Repeat(" ", f.margin)
	return m + strings.ReplaceAll(block, "\n", "\n"+m)
}

func (f frame) para(text string, style lipgloss.Style) string {
	return f.wrap(style.Render(text)) + "\n"
}

func (f frame) wrap(line string) string {
	return f.indent(strings.Join(wrapText(line, f.content), "\n"))
}

// Render draws one frame of the panel for a terminal of the given width.
func (r *Renderer) Render(s engine.Snapshot, o Options, width int) string {
	if width <= 0 {
		width = 100
	}
	f := newFrame(max(width, minWidth))
	var b strings.Builder
	title := i18n.T("title")
	if o.Detail {
		title += i18n.T("title.detail")
	}
	head := bold.Render(title) + faint.Render(i18n.T("heardBy")) + o.Host
	now := faint.Render(clock(s.Now))
	if room := f.content - lipgloss.Width(now) - 1; lipgloss.Width(head) > room {
		head = ansi.Truncate(head, room, "…")
	}
	b.WriteString("\n" + f.indent(pad(head, f.content-lipgloss.Width(now))+now) + "\n\n")
	if o.Picking {
		r.renderPicker(&b, o, f)
		return b.String()
	}
	if o.FirstRun != nil {
		r.renderFirstRun(&b, o, f)
	}
	if o.Detail {
		renderRule(&b, s, f)
	}
	columns := 1
	left, right := f.content-4, 0
	if !o.Detail && f.content >= 2*(cardMin+4)+2 {
		columns = 2
		left = (f.content-2)/2 - 4
		right = f.content - 2 - (left + 4) - 4
	}
	var cards []func(inner int) string
	var silent, far []string
	shown := 0
	for _, d := range s.Devices {
		if !o.Visible(d) {
			continue
		}
		shown++
		switch {
		case o.Loading && d.LastAgo > s.Rule.Timeout:
			cards = append(cards, func(inner int) string {
				return card(o.Name(d), d.Model, r.pal.gray, inner, []string{r.loadingText(o, i18n.T("loading.fresh"))})
			})
		case o.Detail:
			cards = append(cards, func(inner int) string { return r.detailCard(s, d, o, inner) })
		case d.Status == engine.StatusSilent:
			silent = append(silent, fmt.Sprintf("%s (%s)", o.Name(d), silentFor(d)))
		case d.Status == engine.StatusFar:
			far = append(far, fmt.Sprintf("%s (%s)", o.Name(d), farFor(d)))
		default:
			cards = append(cards, func(inner int) string { return r.simpleCard(s, d, o, inner) })
		}
	}
	for i := 0; i < len(cards); i += columns {
		if columns == 2 && i+1 < len(cards) {
			b.WriteString(f.indent(lipgloss.JoinHorizontal(lipgloss.Top, cards[i](left), "  ", cards[i+1](right))) + "\n")
		} else {
			b.WriteString(f.indent(cards[i](left)) + "\n")
		}
	}
	if len(cards) > 0 {
		b.WriteString("\n")
	}
	if len(far) > 0 {
		b.WriteString(f.para(i18n.T("list.far", strings.Join(far, ", ")), faint))
	}
	if len(silent) > 0 {
		b.WriteString(f.para(i18n.T("list.silent", strings.Join(silent, ", ")), faint))
	}
	if len(far)+len(silent) > 0 {
		b.WriteString("\n")
	}
	if shown == 0 && o.FirstRun == nil {
		if o.Loading {
			b.WriteString(f.wrap(r.loadingText(o, i18n.T("loading.log"))) + "\n\n")
		} else {
			b.WriteString(f.para(i18n.T("waiting"), faint) + "\n")
		}
	}
	b.WriteString(f.para(i18n.T("footer.legend", int(s.Rule.Timeout.Seconds()), s.Rule.Border), faint))
	legend := r.green.Render("■") + faint.Render(i18n.T("legend.strong")) + r.yellow.Render("■") + faint.Render(i18n.T("legend.edge")) + r.red.Render("■") + faint.Render(i18n.T("legend.weak"))
	b.WriteString(f.wrap(legend) + "\n")
	b.WriteString(f.para(i18n.T("footer.otherMac"), faint))
	if o.Detail {
		b.WriteString(f.para(i18n.T("footer.necessary"), faint))
	}
	if !o.Replay {
		view := i18n.T("key.details")
		if o.Detail {
			view = i18n.T("key.simple")
		}
		b.WriteString("\n" + f.wrap(r.keyHints(view, i18n.T("key.lang"), i18n.T("key.quit"))) + "\n")
	}
	return b.String()
}

// renderPicker lists the languages by their own names; the title also includes English so that someone who switched to an unfamiliar language by accident can find it.
func (r *Renderer) renderPicker(b *strings.Builder, o Options, f frame) {
	title := i18n.T("picker.title")
	if i18n.Current().Code != "en" {
		title += " · Language"
	}
	var lines []string
	for i, l := range i18n.Languages() {
		mark := "  "
		name := l.Name
		if l == i18n.Current() {
			name += faint.Render("  ✓")
		}
		if i == o.Picker {
			mark = r.green.Bold(true).Render("▌ ")
			name = bold.Render(l.Name)
			if l == i18n.Current() {
				name += faint.Render("  ✓")
			}
		}
		lines = append(lines, mark+name)
	}
	b.WriteString(f.indent(card(title, "", r.pal.green, min(f.content-4, 40), lines)) + "\n\n")
	b.WriteString(f.wrap(r.keyHints(strings.Split(i18n.T("picker.keys"), "   ")...)) + "\n")
}

// keyHints draws "key description" hints: the key in bold accent color, the description muted.
func (r *Renderer) keyHints(hints ...string) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		k, text, _ := strings.Cut(strings.TrimSpace(h), " ")
		parts = append(parts, r.green.Bold(true).Render(k)+" "+faint.Render(text))
	}
	return strings.Join(parts, "   ")
}

func timeAxis(span time.Duration, width int) string {
	left := i18n.T("axis.ago", int(span.Minutes()))
	now := i18n.T("axis.now")
	return faint.Render(pad(left, width-lipgloss.Width(now)) + now)
}

func (r *Renderer) simpleCard(s engine.Snapshot, d engine.Device, o Options, inner int) string {
	chartWidth := inner - 5
	lines := []string{r.statusLine(d, o, inner), r.signalLine(d, o, s.Rule, inner), ""}
	lines = append(lines, r.cachedChart(d.ID, d.Points, s.Now, simpleSpan, chartWidth, 4, s.Rule)...)
	lines = append(lines, timeAxis(simpleSpan, chartWidth), r.historyNote(s, d, o))
	return card(o.Name(d), "", r.statusColor(d.Status), inner, lines)
}

func (r *Renderer) renderFirstRun(b *strings.Builder, o Options, f frame) {
	l := o.FirstRun
	step := func(done bool, text string) string {
		if done {
			return r.green.Render("✓") + " " + text
		}
		return r.yellow.Render(o.Spinner) + " " + text
	}
	b.WriteString(f.wrap(r.yellow.Render(o.Spinner)+" "+bold.Render(i18n.T("first.title"))+" "+faint.Render(fmt.Sprintf("%d s", int(l.Elapsed.Seconds())))) + "\n")
	b.WriteString(f.para(i18n.T("first.note"), faint))
	b.WriteString(f.wrap("  "+step(l.Samples, i18n.T("first.samples"))) + "\n")
	b.WriteString(f.wrap("  "+step(l.List, i18n.T("first.list"))) + "\n")
	b.WriteString(f.wrap("  "+step(l.History, i18n.T("first.history"))) + "\n\n")
}

func renderRule(b *strings.Builder, s engine.Snapshot, f frame) {
	rule := s.Rule
	b.WriteString(f.para(i18n.T("rule", int(rule.Timeout.Seconds()), rule.Border, rule.ReturnAt()), lipgloss.NewStyle()))
	if rule.Calibrated > 0 {
		b.WriteString(f.para(i18n.T("rule.calibrated", rule.Calibrated, i18n.N(rule.Calibrated, "event")), faint) + "\n")
	} else {
		b.WriteString(f.para(i18n.T("rule.default"), faint) + "\n")
	}
}

func (r *Renderer) detailCard(s engine.Snapshot, d engine.Device, o Options, inner int) string {
	rule := s.Rule
	var state string
	switch {
	case d.List == engine.ListUnknown:
		state = faint.Render(i18n.T("detail.unknown"))
	case d.Status == engine.StatusFar:
		state = faint.Render(i18n.T("detail.far")) + i18n.T("detail.farNote")
	case d.List == engine.Unlisted:
		since := ""
		if !d.OutSince.IsZero() {
			since = i18n.T("detail.since", clock(d.OutSince))
		}
		state = r.red.Bold(true).Render(i18n.T("detail.out")+since) + i18n.T("detail.outNote", rule.ReturnAt())
	case d.Left > rule.Timeout/2:
		state = r.green.Bold(true).Render(i18n.T("detail.listed"))
	case d.Left > 0:
		state = r.yellow.Bold(true).Render(i18n.T("detail.soon")) + i18n.T("detail.soonNote", int(d.Left.Seconds()), rule.Border)
	default:
		state = r.yellow.Bold(true).Render(i18n.T("detail.now")) + i18n.T("detail.nowNote", int(d.StrongAgo.Seconds()), rule.Border)
	}
	lines := []string{state}
	if d.List == engine.Unlisted {
		best := i18n.T("detail.none")
		if d.Best > engine.NoReading {
			best = fmt.Sprintf("%d dBm", d.Best)
			if d.Best < rule.ReturnAt() {
				best += i18n.T("detail.bestShort", rule.ReturnAt()-d.Best)
			}
		}
		lines = append(lines, i18n.T("detail.best", int(rule.Timeout.Seconds()), best))
	} else {
		left := i18n.T("detail.left", max(0, int(d.Left.Seconds())), int(rule.Timeout.Seconds()))
		ago := i18n.T("detail.lastGood", rule.Border, int(d.StrongAgo.Seconds()))
		if !d.StrongSeen {
			ago = i18n.T("detail.noGood", rule.Border, int(d.StrongAgo.Seconds()))
		}
		lines = append(lines, r.countdownBar(d.Left, rule.Timeout, 20)+"  "+bold.Render(left)+faint.Render("  ·  "+ago))
	}
	median := i18n.T("detail.none")
	if d.Median > engine.NoReading {
		median = fmt.Sprintf("%d dBm", d.Median)
	}
	lines = append(lines, i18n.T("detail.window", int(rule.Timeout.Seconds()), d.Good, d.Total, i18n.N(d.Total, "reading"), rule.Border, median))
	if d.Total > 0 || d.LastAgo > 0 {
		lines = append(lines, i18n.T("detail.last", d.Last, int(d.LastAgo.Seconds())))
	}
	lines = append(lines, "")
	chartWidth := inner - 5
	lines = append(lines, r.cachedChart(d.ID+"/v", d.Points, s.Now, detailSpan, chartWidth, 5, rule)...)
	lines = append(lines, timeAxis(detailSpan, chartWidth), r.historyNote(s, d, o))
	return card(o.Name(d), d.Model+" · "+d.ID, r.statusColor(d.Status), inner, lines)
}

// PlainSample formats one reading and what it means under the rule, for the line-by-line output.
func PlainSample(d engine.Device, rssi int, t time.Time, r engine.Rule, o Options) string {
	verdict := i18n.T("plain.weak", r.Border)
	switch {
	case rssi > r.Border+r.Offset:
		verdict = i18n.T("plain.back")
	case rssi >= r.Border:
		verdict = i18n.T("plain.keep")
	}
	return fmt.Sprintf("%s  %s %4d dBm  %s", clock(t), pad(o.Name(d), 24), rssi, verdict)
}

// PlainChange formats a device leaving or rejoining the Universal Control list.
func PlainChange(d engine.Device, t time.Time, listed bool, o Options) string {
	what := i18n.T("plain.out")
	if listed {
		what = i18n.T("plain.in")
	}
	return fmt.Sprintf("%s  %s %s", clock(t), pad(o.Name(d), 24), what)
}
