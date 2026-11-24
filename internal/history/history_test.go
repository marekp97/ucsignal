package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marekp97/ucsignal/internal/logsrc"
)

func setup(t *testing.T) (string, time.Time) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, "Library", "Caches", "ucsignal", "history.json"), time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)
}

func sample() []logsrc.Event {
	return []logsrc.Event{
		{Kind: logsrc.Nearby, Time: time.Date(2026, 1, 15, 19, 0, 0, 0, time.UTC), List: []string{"A0000002"}},
		{Kind: logsrc.Timeout, Time: time.Date(2026, 1, 15, 19, 30, 0, 0, time.UTC), Secs: 120},
	}
}

func TestCompleteHistoryLoadsOnlyTheGap(t *testing.T) {
	_, now := setup(t)
	if err := Save(sample(), sample()[:1], true, now); err != nil {
		t.Fatal(err)
	}
	events, samples, since := Load(now.Add(time.Minute))
	if len(events) != 2 || len(samples) != 1 || !events[1].Time.Equal(sample()[1].Time) || events[0].List[0] != "A0000002" {
		t.Fatalf("loaded events %+v, readings %+v", events, samples)
	}
	if want := now.Add(-5 * time.Second).Local().Format("2006-01-02 15:04:05"); since != want {
		t.Fatalf("since %q, want %q", since, want)
	}
}

func TestIncompleteHistoryReadsFullSpan(t *testing.T) {
	_, now := setup(t)
	if err := Save(sample(), nil, false, now); err != nil {
		t.Fatal(err)
	}
	if events, _, since := Load(now.Add(time.Minute)); len(events) != 2 || since != "-12h" {
		t.Fatalf("%d events, since %q; want 2 and -12h", len(events), since)
	}
}

func TestStaleOrBrokenCacheIsIgnored(t *testing.T) {
	path, now := setup(t)
	if err := Save(sample(), nil, true, now); err != nil {
		t.Fatal(err)
	}
	for name, at := range map[string]time.Time{"older than 12 h": now.Add(12 * time.Hour), "from the future": now.Add(-time.Minute)} {
		if events, _, since := Load(at); events != nil || since != "-12h" {
			t.Errorf("%s: %d events, since %q", name, len(events), since)
		}
	}
	for name, content := range map[string]string{"corrupted": `{"saved": "2026-01-15T20:00`, "empty": ""} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if events, _, since := Load(now); events != nil || since != "-12h" {
			t.Errorf("%s: %d events, since %q", name, len(events), since)
		}
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	path, now := setup(t)
	if err := Save(sample(), nil, true, now); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != "history.json" {
		t.Fatalf("in the directory: %v", entries)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %v, want 0600", fi.Mode().Perm())
	}
}
