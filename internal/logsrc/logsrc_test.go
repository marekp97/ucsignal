package logsrc

import (
	"os"
	"strings"
	"testing"
)

func TestParseRecording(t *testing.T) {
	f, err := os.Open("../../testdata/drop-and-return.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events, err := ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	count := map[Kind]int{}
	for _, ev := range events {
		count[ev.Kind]++
	}
	if count[Sample] != 276 || count[Nearby] != 3 || count[Timeout] != 2 || count[EarlyIn] != 1 {
		t.Fatalf("readings %d, lists %d, timeouts %d, returns %d", count[Sample], count[Nearby], count[Timeout], count[EarlyIn])
	}
}

func TestParseEdgeCases(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-01-15 20:11:57.260000+0000","processImagePath":"/System/Library/CoreServices/UniversalControl.app/Contents/MacOS/UniversalControl","subsystem":"com.apple.universalcontrol","eventMessage":"Nearby: []"}`,
		`{"timestamp":"2026-01-15 20:11:57.260000+0000","processImagePath":"/usr/libexec/sharingd","subsystem":"com.apple.bluetooth","eventMessage":"Device found: Md Mac15,3, IDS A0000008-0000, RSSI -50, Ch 37"}`,
		`{"count":98,"finished":1}`,
	}
	evs, err := ReadAll(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil || len(evs) != 1 || evs[0].Kind != Nearby || len(evs[0].List) != 0 {
		t.Fatalf("want only an empty Nearby list, got %+v", evs)
	}
}
