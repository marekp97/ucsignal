package i18n

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

var verb = regexp.MustCompile(`%(\[(\d+)\])?([a-z])`)

// arguments returns the string's arguments in numeric order so that languages with a different word order can be compared.
func arguments(s string) []string {
	next := 1
	var out []string
	for _, m := range verb.FindAllStringSubmatch(s, -1) {
		n := next
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2]) // the pattern only matches digits
		}
		out = append(out, fmt.Sprintf("%d%s", n, m[3]))
		next = n + 1
	}
	sort.Strings(out)
	return out
}

func TestEveryLanguageIsComplete(t *testing.T) {
	en := langEN
	codes := map[string]bool{}
	for _, l := range Languages() {
		if codes[l.Code] {
			t.Errorf("language %s is listed twice", l.Code)
		}
		codes[l.Code] = true
	}
	for _, l := range Languages() {
		for key, ref := range en.msgs {
			got, ok := l.msgs[key]
			if !ok || got == "" {
				t.Errorf("%s: missing string %s", l.Code, key)
				continue
			}
			if a, b := fmt.Sprint(arguments(ref)), fmt.Sprint(arguments(got)); a != b {
				t.Errorf("%s: %s has arguments %s, English has %s", l.Code, key, b, a)
			}
		}
		for key := range l.msgs {
			if _, ok := en.msgs[key]; !ok {
				t.Errorf("%s: extra string %s", l.Code, key)
			}
		}
		for key := range en.nouns {
			if len(l.nouns[key]) == 0 {
				t.Errorf("%s: missing noun %s", l.Code, key)
			}
		}
	}
}

func TestPluralRules(t *testing.T) {
	defer Set(nil)
	cases := map[string]map[int]string{
		"pl": {1: "zdarzenie", 2: "zdarzenia", 5: "zdarzeń", 12: "zdarzeń", 22: "zdarzenia", 103: "zdarzenia", 112: "zdarzeń"},
		"ru": {1: "событие", 2: "события", 5: "событий", 11: "событий", 21: "событие", 22: "события", 105: "событий"},
		"uk": {1: "подія", 3: "події", 5: "подій", 21: "подія"},
		"fr": {0: "événement", 1: "événement", 2: "événements"},
		"en": {0: "events", 1: "event", 2: "events"},
		"ja": {1: "件", 105: "件"},
	}
	for code, want := range cases {
		l, ok := Lookup(code)
		if !ok {
			t.Fatalf("language %s not found", code)
		}
		Set(l)
		for n, form := range want {
			if got := N(n, "event"); got != form {
				t.Errorf("%s %d: %s, want %s", code, n, got, form)
			}
		}
	}
}

func TestLookup(t *testing.T) {
	for in, want := range map[string]string{
		"pl_PL.UTF-8": "pl", "pl-PL": "pl", "en": "en", "zh-Hans-PL": "zh-Hans", "zh_CN": "zh-Hans",
		"zh-Hant-TW": "zh-Hant", "zh_TW": "zh-Hant", "pt-BR": "pt", "de-DE": "de", "ja": "ja",
	} {
		l, ok := Lookup(in)
		if !ok || l.Code != want {
			t.Errorf("%s: %v, want %s", in, l, want)
		}
	}
	if _, ok := Lookup("xx"); ok {
		t.Error("an unknown code should not match")
	}
}

func TestReorderedArguments(t *testing.T) {
	defer Set(nil)
	Set(langEN)
	if got := T("rule", 120, -61, -57); got != "Universal Control drops a device from its list when no reading of -61 dBm or stronger arrives from it for 120 s. A reading of -57 dBm or stronger reliably brings it back." {
		t.Fatal(got)
	}
}
