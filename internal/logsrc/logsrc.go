// Package logsrc reads the nearbyd and Universal Control entries from the unified log through /usr/bin/log.
package logsrc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of a log event.
type Kind int

// The kinds of events that ucsignal reads from the log.
const (
	Sample  Kind = iota // one Bluetooth reading of a device
	Nearby              // the Universal Control list after a change
	Timeout             // nearbyd gave up on a device after the timeout
	EarlyIn             // nearbyd took a device back after one strong reading
)

// Event is one parsed log entry; Kind decides which of the other fields are set.
type Event struct {
	Kind   Kind
	Time   time.Time
	ID     string
	Model  string
	RSSI   int
	List   []string
	Secs   float64
	Border float64
	Offset float64
}

// Predicates for /usr/bin/log. Decisions are logged at the default level and readings at the info level,
// which macOS keeps much shorter, so the two are queried separately.
const (
	EventsPredicate  = `(subsystem == "com.apple.universalcontrol" AND eventMessage CONTAINS "Nearby:") OR (process == "nearbyd" AND (eventMessage CONTAINS "measurement timeout" OR eventMessage CONTAINS "in early"))`
	SamplesPredicate = `process == "nearbyd" AND eventMessage CONTAINS "Device found"`
)

const (
	logBinary  = "/usr/bin/log"
	timeLayout = "2006-01-02 15:04:05.000000-0700"
)

var (
	deviceFound = regexp.MustCompile(`Md ([A-Za-z]+\d+,\d+), IDS ([0-9A-F]{8})[0-9A-F-]*,.*? RSSI (-?\d+),`)
	nearbyList  = regexp.MustCompile(`Nearby: \[([0-9A-F,]*)\]`)
	timeoutSecs = regexp.MustCompile(`measurement timeout \(([0-9.]+)s\)`)
	earlyIn     = regexp.MustCompile(`\[in early\] rssi: (-?[0-9.]+), rssiBorder: (-?[0-9.]+), rssiEarlyOffset: ([0-9.]+)`)
)

type record struct {
	Timestamp        string `json:"timestamp"`
	ProcessImagePath string `json:"processImagePath"`
	Subsystem        string `json:"subsystem"`
	EventMessage     string `json:"eventMessage"`
}

func parse(line []byte) (Event, bool) {
	var r record
	if json.Unmarshal(line, &r) != nil || r.EventMessage == "" {
		return Event{}, false
	}
	t, err := time.Parse(timeLayout, r.Timestamp)
	if err != nil {
		return Event{}, false
	}
	msg := r.EventMessage
	fromNearbyd := strings.HasSuffix(r.ProcessImagePath, "/nearbyd")
	switch {
	case r.Subsystem == "com.apple.universalcontrol":
		if m := nearbyList.FindStringSubmatch(msg); m != nil {
			var list []string
			if m[1] != "" {
				list = strings.Split(m[1], ",")
			}
			return Event{Kind: Nearby, Time: t, List: list}, true
		}
	case fromNearbyd && strings.Contains(msg, "Device found"):
		if m := deviceFound.FindStringSubmatch(msg); m != nil {
			rssi, _ := strconv.Atoi(m[3])
			return Event{Kind: Sample, Time: t, Model: m[1], ID: m[2], RSSI: rssi}, true
		}
	case fromNearbyd && strings.Contains(msg, "measurement timeout"):
		if m := timeoutSecs.FindStringSubmatch(msg); m != nil {
			secs, _ := strconv.ParseFloat(m[1], 64)
			return Event{Kind: Timeout, Time: t, Secs: secs}, true
		}
	case fromNearbyd && strings.Contains(msg, "in early"):
		if m := earlyIn.FindStringSubmatch(msg); m != nil {
			border, _ := strconv.ParseFloat(m[2], 64)
			offset, _ := strconv.ParseFloat(m[3], 64)
			return Event{Kind: EarlyIn, Time: t, Border: border, Offset: offset}, true
		}
	}
	return Event{}, false
}

// ReadAll parses a recording made with `log stream --style ndjson`.
func ReadAll(r io.Reader) ([]Event, error) {
	var out []Event
	err := scan(r, func(e Event) { out = append(out, e) })
	return out, err
}

func scan(r io.Reader, emit func(Event)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if e, ok := parse(sc.Bytes()); ok {
			emit(e)
		}
	}
	return sc.Err()
}

// Show runs `log show`; missing permissions end in an error carrying the tool's message.
func Show(ctx context.Context, since string, info bool, predicate string) ([]Event, error) {
	args := []string{"show", "--style", "ndjson", "--predicate", predicate}
	if after, ok := strings.CutPrefix(since, "-"); ok {
		args = append(args, "--last", after)
	} else {
		args = append(args, "--start", since)
	}
	if info {
		args = append(args, "--info")
	}
	cmd := exec.CommandContext(ctx, logBinary, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var events []Event
	if err := scan(out, func(e Event) { events = append(events, e) }); err != nil {
		// An interrupted read would leave `log` blocked on a full pipe, so the process must be killed before Wait.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return events, err
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return events, fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return events, nil
}

// Stream runs `log stream` and calls emit for every event until ctx is cancelled or log exits.
func Stream(ctx context.Context, emit func(Event)) error {
	cmd := exec.CommandContext(ctx, logBinary, "stream", "--info", "--style", "ndjson",
		"--predicate", "("+EventsPredicate+") OR ("+SamplesPredicate+")")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := scan(out, emit); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if ctx.Err() == nil {
		return errors.New("log stream ended")
	}
	return nil
}
