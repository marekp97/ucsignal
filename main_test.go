package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/i18n"
	"github.com/marekp97/ucsignal/internal/ui"
)

func TestRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defer i18n.Set(i18n.Current())
	const recording = "testdata/drop-and-return.ndjson"
	replay := []string{"--lang", "en", "--replay", recording, "--at", "20:11:14", "--names", "testdata/names"}
	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
	}{
		{"version", []string{"--version"}, 0, "ucsignal dev"},
		{"help flag", []string{"--lang", "en", "--help"}, 0, "Usage:"},
		{"help word", []string{"--lang", "en", "help"}, 0, "Usage:"},
		{"replay", replay, 0, "drops in about 43 s"},
		{"replay in detail", append(slices.Clone(replay), "-v"), 0, "43 s of 120 left before drop"},
		{"replay in another language", []string{"--lang", "pl", "--replay", recording, "--at", "20:11:14"}, 0, "za ok. 43 s"},
		{"unknown flag", []string{"--bogus"}, 1, ""},
		{"two names", []string{"a", "b"}, 1, ""},
		{"unknown theme", []string{"--theme", "blue"}, 1, ""},
		{"unknown language", []string{"--lang", "xx"}, 1, ""},
		{"missing recording", []string{"--replay", "missing.ndjson"}, 1, ""},
		{"missing names file", []string{"--names", "missing", "--replay", recording}, 1, ""},
		{"malformed time", []string{"--replay", recording, "--at", "noon"}, 1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr)
			out := ansi.Strip(stdout.String())
			if code != c.code {
				t.Fatalf("exit code %d, want %d; stderr: %s", code, c.code, stderr.String())
			}
			if code == 0 && (!strings.Contains(out, c.stdout) || stderr.Len() > 0) {
				t.Errorf("stdout lacks %q or stderr is not empty\nstdout: %s\nstderr: %s", c.stdout, out, stderr.String())
			}
			if code != 0 && (stdout.Len() > 0 || stderr.Len() == 0) {
				t.Errorf("a failure should explain itself on stderr only\nstdout: %s\nstderr: %s", out, stderr.String())
			}
		})
	}
}

func TestLoadNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), "names")
	content := "# comment\n\na0000008 MacBook  \nA0000002\tiPad\nNONAME\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := loadNames(p)
	if err != nil || names["A0000008"] != "MacBook" || names["A0000002"] != "iPad" || len(names) != 2 {
		t.Fatalf("%q, %v", names, err)
	}
	if _, err := loadNames(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing --names file should be an error")
	}
	t.Setenv("HOME", t.TempDir())
	if names, err := loadNames(""); err != nil || len(names) != 0 {
		t.Fatalf("missing default file: %q, %v", names, err)
	}
}

func TestLangArg(t *testing.T) {
	for want, args := range map[string][]string{
		"de":      {"-v", "--lang", "de"},
		"zh-Hans": {"--lang=zh-Hans"},
		"ja":      {"-lang=ja", "ipad"},
		"":        {"--lang"},
	} {
		if got := langArg(args); got != want {
			t.Errorf("%v: %q, want %q", args, got, want)
		}
	}
}

func TestFailedHistoryIsNotComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := &model{eng: engine.New(time.Now()), view: ui.NewRenderer(true), meter: map[string]float64{}}
	m.Update(historyMsg{complete: true, failed: true})
	if m.eng.HistoryComplete() || !m.opts.HistoryFailed || m.loading() {
		t.Fatalf("complete %v, failed %v, loading %v; want false, true, false", m.eng.HistoryComplete(), m.opts.HistoryFailed, m.loading())
	}
	m.Update(historyMsg{complete: true})
	if !m.eng.HistoryComplete() {
		t.Fatal("a successful query should complete the history")
	}
}
