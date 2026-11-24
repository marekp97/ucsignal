package engine

import (
	"os"
	"testing"
	"time"

	"github.com/marekp97/ucsignal/internal/logsrc"
)

const (
	otherMac = "A0000008"
	ipad     = "A0000002"
)

func recording(t *testing.T) []logsrc.Event {
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
	return events
}

func loadUntil(t *testing.T, clock string) (*Engine, time.Time) {
	t.Helper()
	events := recording(t)
	at, err := time.Parse("2006-01-02 15:04:05-0700", "2026-01-15 "+clock+"+0000")
	if err != nil {
		t.Fatal(err)
	}
	e := New(events[0].Time)
	for _, ev := range events {
		if !ev.Time.After(at) {
			e.Add(ev)
		}
	}
	return e, at
}

func find(t *testing.T, s Snapshot, id string) Device {
	t.Helper()
	for _, d := range s.Devices {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("device %s not found", id)
	return Device{}
}

func TestCountdownBeforeDrop(t *testing.T) {
	e, at := loadUntil(t, "20:11:14")
	d := find(t, e.Snapshot(at), otherMac)
	if d.List != Listed || d.Status != StatusSoon {
		t.Fatalf("list %v, status %v; want listed and StatusSoon", d.List, d.Status)
	}
	if left := d.Left.Seconds(); left < 42 || left > 44 {
		t.Fatalf("%.1f s left before the drop, want about 43 s (last -61 at 20:09:57.252)", left)
	}
}

func TestDropMatchesNearbydTimeout(t *testing.T) {
	e, at := loadUntil(t, "20:12:25")
	snap := e.Snapshot(at)
	d := find(t, snap, otherMac)
	if d.List != Unlisted || d.Status != StatusOut || d.Drops != 1 {
		t.Fatalf("list %v, status %v, drops %d; want off the list, StatusOut, 1 drop", d.List, d.Status, d.Drops)
	}
	if got := d.OutSince.Format("15:04:05"); got != "20:11:57" {
		t.Fatalf("off the list since %s, want 20:11:57", got)
	}
	if d.Best != -60 {
		t.Fatalf("strongest reading %d, want -60", d.Best)
	}
	if snap.Rule.Timeout != 120*time.Second || snap.Rule.Calibrated != 1 {
		t.Fatalf("rule %+v, want a 120 s limit from 1 event", snap.Rule)
	}
}

func TestReturnCalibratesBorder(t *testing.T) {
	e, at := loadUntil(t, "20:13:10")
	snap := e.Snapshot(at)
	if d := find(t, snap, otherMac); d.List != Listed {
		t.Fatalf("the other Mac did not return to the list: %v", d.List)
	}
	r := snap.Rule
	if r.Border != -61 || r.Offset != 3 || r.ReturnAt() != -57 || r.Calibrated != 2 {
		t.Fatalf("rule %+v, want -61, +3, return from -57, 2 events", r)
	}
	if d := find(t, snap, ipad); d.Status != StatusOK {
		t.Fatalf("iPad has status %v, want StatusOK", d.Status)
	}
}

func TestDuplicateHistoryIsIgnored(t *testing.T) {
	e, at := loadUntil(t, "20:12:25")
	for _, ev := range recording(t) {
		if !ev.Time.After(at) {
			e.Add(ev)
		}
	}
	if d := find(t, e.Snapshot(at), otherMac); d.Drops != 1 {
		t.Fatalf("%d drops after reloading the history, want 1", d.Drops)
	}
}

func TestNeverListedDeviceIsFar(t *testing.T) {
	now := time.Date(2026, 1, 15, 22, 0, 0, 0, time.Local)
	e := New(now.Add(-150 * time.Second))
	e.Add(
		logsrc.Event{Kind: logsrc.Nearby, Time: now.Add(-50 * time.Second), List: []string{"A0000001"}},
		logsrc.Event{Kind: logsrc.Nearby, Time: now.Add(-40 * time.Second), List: []string{"A0000001", "A0000002"}},
		logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-10 * time.Second), ID: "A0000003", Model: "Mac15,12", RSSI: -72},
		logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-5 * time.Second), ID: "A0000001", Model: "Mac15,3", RSSI: -55},
	)
	snap := e.Snapshot(now)
	if d := find(t, snap, "A0000003"); d.Status != StatusFar {
		t.Fatalf("device never on the list has status %v, want StatusFar", d.Status)
	}
	if d := find(t, snap, "A0000001"); d.Status != StatusOK {
		t.Fatalf("listed device has status %v, want StatusOK", d.Status)
	}
}

func TestCacheKeepsListOlderThanHistoryWindow(t *testing.T) {
	now := time.Date(2026, 1, 16, 12, 0, 0, 0, time.Local)
	e := New(now)
	e.Add(logsrc.Event{Kind: logsrc.Nearby, Time: now.Add(-20 * time.Hour), List: []string{"A0000001"}})
	events := e.Events(now)
	if len(events) != 1 || events[0].Kind != logsrc.Nearby {
		t.Fatalf("cache lost the last list from before 12 h: %+v", events)
	}
	restored := New(now)
	restored.Add(events...)
	restored.Add(logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-5 * time.Second), ID: "A0000001", Model: "Mac15,3", RSSI: -50})
	if d := find(t, restored.Snapshot(now), "A0000001"); d.List != Listed {
		t.Fatalf("list state %v after restoring from the cache, want Listed", d.List)
	}
}

func TestLongSilentDeviceIsHidden(t *testing.T) {
	now := time.Date(2026, 1, 16, 12, 0, 0, 0, time.Local)
	e := New(now)
	e.Add(logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-31 * time.Minute), ID: "A0000009", Model: "iPad16,3", RSSI: -70})
	if n := len(e.Snapshot(now).Devices); n != 0 {
		t.Fatalf("device not heard for 31 min is still visible (%d)", n)
	}
}

func TestUnknownListWithoutStrongReadingIsOut(t *testing.T) {
	now := time.Date(2026, 1, 16, 12, 0, 0, 0, time.Local)
	e := New(now.Add(-10 * time.Minute))
	e.Add(logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-5 * time.Second), ID: "A0000001", Model: "Mac15,3", RSSI: -75})
	if d := find(t, e.Snapshot(now), "A0000001"); d.List != ListUnknown || d.Status != StatusOut {
		t.Fatalf("list %v, status %v; want ListUnknown and StatusOut", d.List, d.Status)
	}
}

func TestDuplicateSamplesAreIgnored(t *testing.T) {
	now := time.Date(2026, 1, 16, 12, 0, 0, 0, time.Local)
	e := New(now.Add(-10 * time.Minute))
	at := now.Add(-5 * time.Second)
	for range 2 {
		e.Add(
			logsrc.Event{Kind: logsrc.Sample, Time: at, ID: "A0000001", Model: "Mac15,3", RSSI: -67},
			logsrc.Event{Kind: logsrc.Sample, Time: at, ID: "A0000001", Model: "Mac15,3", RSSI: -62},
		)
	}
	if d := find(t, e.Snapshot(now), "A0000001"); d.Total != 2 {
		t.Fatalf("%d readings, want 2", d.Total)
	}
}

func TestModeBreaksTiesDeterministically(t *testing.T) {
	for range 100 {
		if got, _ := mode(map[int]int{-61: 1, -52: 1}, -61); got != -61 {
			t.Fatalf("tie with the default gave %d, want -61", got)
		}
		if got, _ := mode(map[int]int{-29: 1, -52: 1}, -61); got != -52 {
			t.Fatalf("tie without the default gave %d, want -52", got)
		}
	}
}

func TestTimeWithoutStrongReadingIsNotOverstated(t *testing.T) {
	now := time.Date(2026, 1, 16, 12, 0, 0, 0, time.Local)
	e := New(now.Add(-time.Hour))
	e.Add(logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-15 * time.Minute), ID: "A0000001", Model: "Mac15,3", RSSI: -50})
	for ago := 14 * time.Minute; ago > 0; ago -= time.Minute {
		e.Add(logsrc.Event{Kind: logsrc.Sample, Time: now.Add(-ago), ID: "A0000001", Model: "Mac15,3", RSSI: -70})
	}
	if d := find(t, e.Snapshot(now), "A0000001"); d.StrongSeen || d.StrongAgo > 15*time.Minute {
		t.Fatalf("strong seen %v, %v without a strong reading; the last one was 15 min ago", d.StrongSeen, d.StrongAgo)
	}
}
