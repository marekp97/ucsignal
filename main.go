// Command ucsignal shows how close Universal Control is to dropping your other Macs and iPads.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/marekp97/ucsignal/internal/engine"
	"github.com/marekp97/ucsignal/internal/history"
	"github.com/marekp97/ucsignal/internal/i18n"
	"github.com/marekp97/ucsignal/internal/settings"
	"github.com/marekp97/ucsignal/internal/ui"
)

// version is set by release builds: -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the program behind main. The arguments and both outputs are passed in so that tests can drive it.
func run(args []string, stdout, stderr io.Writer) int {
	lang := langArg(args)
	_, langKnown := i18n.Lookup(lang)
	if lang == "" {
		lang = settings.Load().Lang
		langKnown = true
	}
	i18n.Set(i18n.Detect(lang))

	// The flag package would print help and errors to one stream; here help goes to stdout and errors to stderr.
	fs := flag.NewFlagSet("ucsignal", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	detail := fs.Bool("v", false, i18n.T("flag.v"))
	plain := fs.Bool("plain", false, i18n.T("flag.plain"))
	replayFile := fs.String("replay", "", i18n.T("flag.replay"))
	at := fs.String("at", "", i18n.T("flag.at"))
	namesFile := fs.String("names", "", i18n.T("flag.names"))
	fs.String("lang", "", i18n.T("flag.lang", i18n.Codes()))
	theme := fs.String("theme", "", i18n.T("flag.theme"))
	showVersion := fs.Bool("version", false, i18n.T("flag.version"))
	usage := func(w io.Writer) {
		fmt.Fprint(w, i18n.T("usage"))
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "ucsignal:", err)
		return 1
	}

	err := fs.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp), err == nil && fs.NArg() == 1 && fs.Arg(0) == "help":
		usage(stdout)
		return 0
	case err != nil:
		fail(err)
		usage(stderr)
		return 1
	case fs.NArg() > 1, strings.HasPrefix(fs.Arg(0), "-"), !langKnown, *theme != "" && *theme != "dark" && *theme != "light":
		usage(stderr)
		return 1
	}
	if *showVersion {
		fmt.Fprintln(stdout, "ucsignal", buildVersion())
		return 0
	}

	names, err := loadNames(*namesFile)
	if err != nil {
		return fail(err)
	}
	opts := ui.Options{Detail: *detail, Filter: fs.Arg(0), Names: names}
	tty, interactive := terminal(stdout)
	view := ui.NewRenderer(true)
	switch {
	case *theme != "":
		view.SetTheme(*theme == "dark")
	case *replayFile != "" && interactive:
		view.SetTheme(lipgloss.HasDarkBackground(os.Stdin, tty))
	}
	if *replayFile != "" {
		view.SetPlain(!interactive)
		if err := replay(stdout, *replayFile, *at, opts, view); err != nil {
			return fail(err)
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	now := time.Now()
	eng := engine.New(now.Add(-10 * time.Minute))
	cachedEvents, cachedSamples, since := history.Load(now)
	eng.Add(cachedEvents...)
	eng.Add(cachedSamples...)
	opts.Host = hostName()

	if *plain || !interactive {
		runPlain(ctx, stdout, stderr, eng, opts, since)
		return 0
	}
	if err := runPanel(ctx, eng, view, opts, since, *theme); err != nil {
		return fail(err)
	}
	return 0
}

// langArg reads --lang before the other flags because the flag descriptions in the help already depend on the language.
func langArg(args []string) string {
	for i, a := range args {
		for _, prefix := range []string{"--lang=", "-lang="} {
			if v, ok := strings.CutPrefix(a, prefix); ok {
				return v
			}
		}
		if (a == "--lang" || a == "-lang") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// buildVersion prefers the version stamped by the release build and falls back to the module version of `go install`.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// loadNames reads the device names; only the default file may be missing.
func loadNames(path string) (map[string]string, error) {
	explicit := path != ""
	if !explicit {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "ucsignal", "names")
	}
	names := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return names, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if fields := strings.Fields(line); len(fields) > 1 {
			names[strings.ToUpper(fields[0])] = strings.Join(fields[1:], " ")
		}
	}
	return names, sc.Err()
}

func hostName() string {
	if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	h, _ := os.Hostname()
	return strings.TrimSuffix(h, ".local")
}

func terminal(w io.Writer) (*os.File, bool) {
	f, ok := w.(*os.File)
	return f, ok && term.IsTerminal(f.Fd())
}

// saveHistory writes the cache; a failed write only costs a slower next start, so the error is dropped.
func saveHistory(eng *engine.Engine) {
	now := time.Now()
	_ = history.Save(eng.Events(now), eng.Samples(now), eng.HistoryComplete(), now)
}
