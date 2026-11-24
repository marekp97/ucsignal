package ui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marekp97/ucsignal/internal/engine"
)

type rgb struct{ r, g, b float64 }

type palette struct{ red, yellow, green, gray, track, background rgb }

// Light reds, yellows and greens are unreadable on a light background, so the second palette uses darker, more saturated colors.
var (
	darkPalette  = palette{rgb{242, 95, 92}, rgb{242, 193, 78}, rgb{95, 208, 104}, rgb{110, 116, 128}, rgb{58, 62, 70}, rgb{20, 22, 26}}
	lightPalette = palette{rgb{198, 40, 40}, rgb{176, 110, 0}, rgb{24, 135, 58}, rgb{140, 145, 153}, rgb{214, 217, 222}, rgb{255, 255, 255}}
)

// Renderer draws the panel. It owns the palette and the chart cache, so the package keeps no state of its own.
type Renderer struct {
	pal                palette
	green, yellow, red lipgloss.Style
	track              string
	charts             map[string]cachedLines
}

// NewRenderer returns a renderer with the dark or the light palette.
func NewRenderer(dark bool) *Renderer {
	r := &Renderer{track: "█"}
	r.SetTheme(dark)
	return r
}

// SetTheme switches the palette. Rendered charts embed their colors, so the chart cache is dropped.
func (r *Renderer) SetTheme(dark bool) {
	r.pal = lightPalette
	if dark {
		r.pal = darkPalette
	}
	r.green, r.yellow, r.red = r.pal.green.style(), r.pal.yellow.style(), r.pal.red.style()
	r.charts = map[string]cachedLines{}
}

// SetPlain adapts the bar track to output without colors, where a full block would look like a full bar.
func (r *Renderer) SetPlain(plain bool) {
	r.track = "█"
	if plain {
		r.track = "░"
	}
}

func (c rgb) hex() string { return fmt.Sprintf("#%02X%02X%02X", int(c.r), int(c.g), int(c.b)) }

// Colored glyphs are written as raw ANSI runs instead of calling lipgloss.Render per glyph:
// with several hundred glyphs per frame that was most of the CPU usage. Bubble Tea adapts the colors to the terminal.
type painter struct {
	b    strings.Builder
	open string
}

func (p *painter) put(c rgb, s string) {
	code := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(c.r), int(c.g), int(c.b))
	if code != p.open {
		p.b.WriteString(code)
		p.open = code
	}
	p.b.WriteString(s)
}

func (p *painter) putBg(fg, bg rgb, s string) {
	p.open = ""
	fmt.Fprintf(&p.b, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm%s\x1b[0m", int(fg.r), int(fg.g), int(fg.b), int(bg.r), int(bg.g), int(bg.b), s)
}

func (p *painter) plain(s string) {
	if p.open != "" {
		p.b.WriteString("\x1b[0m")
		p.open = ""
	}
	p.b.WriteString(s)
}

func (p *painter) String() string {
	p.plain("")
	return p.b.String()
}

func (c rgb) style() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c.hex())) }

func lerp(a, b rgb, t float64) rgb {
	t = math.Max(0, math.Min(1, t))
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// signalColor goes from red to yellow up to the threshold and from yellow to green up to the return threshold, so the color means the same as the rule.
func (r *Renderer) signalColor(v float64, rule engine.Rule) rgb {
	border, back := float64(rule.Border), float64(rule.ReturnAt())
	switch {
	case v < border:
		return lerp(r.pal.red, r.pal.yellow, (v-(border-8))/8)
	case v < back:
		return lerp(r.pal.yellow, r.pal.green, (v-border)/(back-border)*0.5)
	default:
		return lerp(lerp(r.pal.yellow, r.pal.green, 0.5), r.pal.green, (v-back)/6)
	}
}

const (
	meterMin = -85.0
	meterMax = -35.0
)

var eighths = []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// MeterFraction maps a reading in dBm onto the 0 to 1 scale of the signal bar.
func MeterFraction(dbm int) float64 {
	return math.Max(0, math.Min(1, (float64(dbm)-meterMin)/(meterMax-meterMin)))
}

// meter draws the bar with 1/8 character precision in a single color that matches the value:
// a gradient starting at red looked like a warning even with a very good signal.
func (r *Renderer) meter(frac float64, width int, rule engine.Rule) string {
	return r.bar(frac, width, r.signalColor(meterMin+frac*(meterMax-meterMin), rule))
}

func (r *Renderer) bar(frac float64, width int, c rgb) string {
	var p painter
	total := int(math.Round(frac * float64(width) * 8))
	for i := range width {
		switch fill := total - i*8; {
		case fill >= 8:
			p.put(c, "█")
		case fill > 0:
			p.putBg(c, r.pal.track, eighths[fill])
		default:
			p.put(r.pal.track, r.track)
		}
	}
	return p.String()
}

// countdownBar shows the time left before the drop in one color: green above half of the limit, yellow below, red in the last 15 s.
func (r *Renderer) countdownBar(left, limit time.Duration, width int) string {
	c := r.pal.green
	switch {
	case left <= 15*time.Second:
		c = r.pal.red
	case left <= limit/2:
		c = r.pal.yellow
	}
	return r.bar(math.Max(0, math.Min(1, left.Seconds()/limit.Seconds())), width, c)
}

const (
	chartTop    = -35.0
	chartBottom = -75.0
	// Readings arrive every few seconds; the chart bridges a short gap with a line and leaves a longer one as a visible hole.
	chartHold = 15 * time.Second
)

type chartKey struct {
	id             string
	points         int
	newest, column int64
	width, height  int
	span           time.Duration
	rule           engine.Rule
}

type cachedLines struct {
	key   chartKey
	lines []string
}

// cachedChart recomputes the chart only when a reading arrives or time moves by one column;
// bar animation frames in between reuse the cached result.
func (r *Renderer) cachedChart(id string, points []engine.Point, now time.Time, span time.Duration, width, height int, rule engine.Rule) []string {
	key := chartKey{id: id, points: len(points), width: width, height: height, span: span, rule: rule,
		column: now.UnixNano() / int64(span/time.Duration(width*2))}
	if len(points) > 0 {
		key.newest = now.Add(-points[len(points)-1].Ago).UnixNano()
	}
	if c, ok := r.charts[id]; ok && c.key == key {
		return c.lines
	}
	lines := r.chart(points, now, span, width, height, rule)
	r.charts[id] = cachedLines{key, lines}
	return lines
}

// chart draws the readings with Braille dots (2×4 dots per character). Each column shows the
// strongest reading, because a single strong reading keeps a device on the list, not the average.
func (r *Renderer) chart(points []engine.Point, now time.Time, span time.Duration, width, height int, rule engine.Rule) []string {
	cols, rows := width*2, height*4
	perCol := int64(span / time.Duration(cols))
	nowCol := now.UnixNano() / perCol
	value := make([]float64, cols)
	has := make([]bool, cols)
	for _, p := range points {
		// Columns are aligned to a shared time grid so the chart stays fixed until the next column and can be cached.
		x := cols - 1 - int(nowCol-now.Add(-p.Ago).UnixNano()/perCol)
		if p.Ago < 0 || x < 0 || x >= cols {
			continue
		}
		if !has[x] || float64(p.RSSI) > value[x] {
			value[x], has[x] = float64(p.RSSI), true
		}
	}
	last, lastX := 0.0, -1
	for x := range cols {
		if has[x] {
			last, lastX = value[x], x
		} else if lastX >= 0 && time.Duration(int64(x-lastX)*perCol) <= chartHold {
			value[x], has[x] = last, true
		}
	}
	toRow := func(v float64) int {
		y := int(math.Round((chartTop - v) / (chartTop - chartBottom) * float64(rows-1)))
		return max(0, min(rows-1, y))
	}
	dots := make([][]uint8, height)
	cellMax := make([][]float64, height)
	cellData := make([][]bool, height)
	fillMax := make([][]float64, height)
	cellFill := make([][]bool, height)
	for i := range dots {
		dots[i] = make([]uint8, width)
		cellMax[i] = make([]float64, width)
		cellData[i] = make([]bool, width)
		fillMax[i] = make([]float64, width)
		cellFill[i] = make([]bool, width)
	}
	bit := [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}
	set := func(x, y int, v float64, data bool) {
		cx, cy := x/2, y/4
		dots[cy][cx] |= bit[x%2][y%4]
		if data && (!cellData[cy][cx] || v > cellMax[cy][cx]) {
			cellMax[cy][cx], cellData[cy][cx] = v, true
		}
	}
	borderRow := toRow(float64(rule.Border))
	for x := 0; x < cols; x += 4 {
		set(x, borderRow, 0, false)
	}
	for x := range cols {
		if !has[x] {
			continue
		}
		y := toRow(value[x])
		// Sparse, dark dots below the line: the fill is background and the line must stand out clearly against it.
		for yy := y + 3; yy < rows; yy++ {
			if x%2 == 0 && yy%2 == 0 {
				cx, cy := x/2, yy/4
				dots[cy][cx] |= bit[x%2][yy%4]
				if !cellFill[cy][cx] || value[x] > fillMax[cy][cx] {
					fillMax[cy][cx], cellFill[cy][cx] = value[x], true
				}
			}
		}
		set(x, y, value[x], true)
		if y+1 < rows {
			set(x, y+1, value[x], true)
		}
		if x > 0 && has[x-1] {
			py := toRow(value[x-1])
			for yy := min(py, y); yy <= max(py, y); yy++ {
				set(x, yy, max(value[x], value[x-1]), true)
			}
		}
	}
	lines := make([]string, height)
	for cy := range height {
		var b painter
		for cx := range width {
			ch := string(rune(0x2800) + rune(dots[cy][cx]))
			switch {
			case cellData[cy][cx]:
				b.put(r.signalColor(cellMax[cy][cx], rule), ch)
			case cellFill[cy][cx]:
				b.put(lerp(r.signalColor(fillMax[cy][cx], rule), r.pal.background, 0.75), ch)
			case dots[cy][cx] != 0:
				b.put(r.pal.gray, ch)
			default:
				b.plain(" ")
			}
		}
		label := ""
		switch cy {
		case borderRow / 4:
			label = r.yellow.Render(strconv.Itoa(rule.Border))
		case 0:
			label = faint.Render(fmt.Sprintf("%.0f", chartTop))
		case height - 1:
			label = faint.Render(fmt.Sprintf("%.0f", chartBottom))
		}
		lines[cy] = b.String() + " " + label
	}
	return lines
}

func card(title, subtitle string, border rgb, inner int, lines []string) string {
	bs := border.style()
	head := bs.Render("╭─ ") + bold.Render(title)
	if subtitle != "" {
		head += " " + faint.Render(subtitle)
	}
	if lipgloss.Width(head) > inner+2 {
		head = ansi.Truncate(head, inner+2, "…")
	}
	if gap := inner + 3 - lipgloss.Width(head); gap > 0 {
		head += " " + bs.Render(strings.Repeat("─", gap-1)+"╮")
	}
	var b strings.Builder
	b.WriteString(head + "\n")
	side := bs.Render("│")
	for _, l := range lines {
		parts := []string{l}
		if lipgloss.Width(l) > inner {
			parts = wrapText(l, inner)
		}
		for _, part := range parts {
			w := lipgloss.Width(part)
			if w > inner {
				part, w = ansi.Truncate(part, inner, "…"), inner
			}
			b.WriteString(side + " " + part + strings.Repeat(" ", inner-w) + " " + side + "\n")
		}
	}
	b.WriteString(bs.Render("╰" + strings.Repeat("─", inner+2) + "╯"))
	return b.String()
}

var negativeNumber = regexp.MustCompile(`-(\d)`)

// wrapText wraps text to a width; lipgloss also breaks lines at "-", so a number's minus sign is made non-breaking while wrapping.
func wrapText(text string, width int) []string {
	text = negativeNumber.ReplaceAllString(text, "\u2011$1")
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n")
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, "\u2011", "-")
	}
	return lines
}
