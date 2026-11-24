package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/marekp97/ucsignal/internal/i18n"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// plainText drops colors and trailing spaces so the golden files stay readable and survive editors that trim lines.
func plainText(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func TestRenderGolden(t *testing.T) {
	defer i18n.Set(i18n.Current())
	i18n.Set(i18n.Detect("en"))
	snaps := snapshots(t)
	simple := Options{Names: map[string]string{"A0000008": "MacBook", "A0000002": "iPad"}, Host: "recording", Replay: true}
	detail := simple
	detail.Detail = true
	cases := []struct {
		name, clock string
		opts        Options
		width       int
	}{
		{"simple", "20:11:14", simple, 120},
		{"detail", "20:11:14", detail, 120},
		{"narrow", "20:11:14", simple, 46},
		{"dropped", "20:12:25", simple, 120},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := snaps[c.clock]
			snap.HistoryComplete = true
			view := NewRenderer(true)
			view.SetPlain(true)
			got := plainText(view.Render(snap, c.opts, c.width))
			path := filepath.Join("testdata", c.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("screen differs from %s; after an intended change run: go test ./internal/ui -update\n%s", path, got)
			}
		})
	}
}
