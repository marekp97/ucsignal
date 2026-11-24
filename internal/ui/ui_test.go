package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/i18n"
	"github.com/marekp97/ucsignal/internal/logsrc"
)

func snapshots(t *testing.T) map[string]engine.Snapshot {
	t.Helper()
	f, err := os.Open("../../testdata/drop-and-return.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events, err := logsrc.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]engine.Snapshot{}
	for _, clock := range []string{"20:09:30", "20:11:14", "20:12:25", "20:13:10"} {
		at, _ := time.Parse("2006-01-02 15:04:05-0700", "2026-01-15 "+clock+"+0000")
		e := engine.New(events[0].Time)
		for _, ev := range events {
			if !ev.Time.After(at) {
				e.Add(ev)
			}
		}
		out[clock] = e.Snapshot(at)
	}
	out["empty"] = engine.New(events[0].Time).Snapshot(events[0].Time)
	return out
}

func variants() map[string]Options {
	names := map[string]string{"A0000008": "MacBook", "A0000002": "iPad with a very long name given to it by its owner at the office"}
	base := Options{Names: names, Host: "Desk Mac", Spinner: "⠋"}
	v := map[string]Options{"simple": base}
	d := base
	d.Detail = true
	v["detail"] = d
	l := base
	l.Loading = true
	v["loading"] = l
	fr := base
	fr.Loading, fr.FirstRun = true, &LoadSteps{Samples: true, Elapsed: 3 * time.Second}
	v["firstRun"] = fr
	p := base
	p.Picking, p.Picker = true, 3
	v["picker"] = p
	return v
}

// The screen must never be wider than the terminal, because the terminal would wrap lines and break the cards; this applies to every language and state.
func TestRenderFitsTerminal(t *testing.T) {
	defer i18n.Set(i18n.Current())
	snaps := snapshots(t)
	for _, lang := range i18n.Languages() {
		i18n.Set(lang)
		for _, dark := range []bool{true, false} {
			view := NewRenderer(dark)
			for vname, o := range variants() {
				for sname, s := range snaps {
					for _, width := range []int{36, 40, 44, 50, 64, 80, 100, 118, 140, 200} {
						out := view.Render(s, o, width)
						for i, line := range strings.Split(out, "\n") {
							if w := ansi.StringWidth(line); w > width {
								t.Errorf("%s %s %s width %d: line %d has %d columns: %q", lang.Code, vname, sname, width, i, w, ansi.Strip(line))
							}
						}
					}
				}
			}
		}
	}
}

func TestRenderShowsState(t *testing.T) {
	defer i18n.Set(i18n.Current())
	en, _ := i18n.Lookup("en")
	i18n.Set(en)
	snaps := snapshots(t)
	o := variants()["simple"]
	for clock, want := range map[string]string{
		"20:11:14": "drops in about 43 s",
		"20:12:25": "MacBook",
	} {
		if out := ansi.Strip(NewRenderer(true).Render(snaps[clock], o, 120)); !strings.Contains(out, want) {
			t.Errorf("%s: %q missing in:\n%s", clock, want, out)
		}
	}
}

func TestPlainSampleFollowsRule(t *testing.T) {
	defer i18n.Set(i18n.Current())
	i18n.Set(i18n.Detect("en"))
	r := engine.Rule{Border: -61, Offset: 3, Timeout: 120 * time.Second}
	d := engine.Device{ID: "A0000001", Model: "Mac15,3"}
	for rssi, want := range map[int]string{-57: i18n.T("plain.back"), -58: i18n.T("plain.keep"), -61: i18n.T("plain.keep"), -62: i18n.T("plain.weak", -61)} {
		if got := PlainSample(d, rssi, time.Time{}, r, Options{}); !strings.HasSuffix(got, want) {
			t.Errorf("%d dBm: %q, want it to end with %q", rssi, got, want)
		}
	}
}

func TestHistoryFailureIsShown(t *testing.T) {
	defer i18n.Set(i18n.Current())
	i18n.Set(i18n.Detect("en"))
	d := engine.Device{ID: "A0000001"}
	if got := ansi.Strip(NewRenderer(true).historyNote(engine.Snapshot{}, d, Options{HistoryFailed: true})); got != i18n.T("history.failed") {
		t.Fatalf("history note %q, want %q", got, i18n.T("history.failed"))
	}
}
