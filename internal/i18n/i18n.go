// Package i18n holds the interface strings and plural rules for every supported language.
package i18n

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Language is one translation of the interface.
type Language struct {
	Code   string
	Name   string
	plural func(n int) int
	msgs   map[string]string
	nouns  map[string][]string
}

// languages is in the order of the language picker.
var languages = []*Language{langEN, langPL, langDE, langFR, langES, langPT, langIT, langNL, langRU, langUK, langTR, langJA, langKO, langZHHans, langZHHant}

var current *Language

func byCode(code string) (*Language, bool) {
	for _, l := range languages {
		if l.Code == code {
			return l, true
		}
	}
	return nil, false
}

// Languages returns the supported languages in the order of the language picker.
func Languages() []*Language { return languages }

// Codes returns the language codes as one comma-separated string for the help text.
func Codes() string {
	codes := make([]string, 0, len(languages))
	for _, l := range languages {
		codes = append(codes, l.Code)
	}
	return strings.Join(codes, ", ")
}

// Current returns the language in use, English until Set is called.
func Current() *Language {
	if current == nil {
		return langEN
	}
	return current
}

// Set switches the language in use; nil switches back to English.
func Set(l *Language) { current = l }

// Lookup accepts a code from --lang, from the settings or from the system (pl_PL, zh-Hans-PL, pt-BR).
func Lookup(code string) (*Language, bool) {
	c := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "_", "-"))
	if c == "" {
		return nil, false
	}
	if strings.HasPrefix(c, "zh") {
		if strings.Contains(c, "hant") || strings.HasPrefix(c, "zh-tw") || strings.HasPrefix(c, "zh-hk") || strings.HasPrefix(c, "zh-mo") {
			return langZHHant, true
		}
		return langZHHans, true
	}
	if len(c) == 2 || len(c) > 2 && (c[2] == '-' || c[2] == '.') {
		return byCode(c[:2])
	}
	return nil, false
}

// Detect picks the language: the explicit choice, then the locale variables, then the macOS interface language.
// A variable set to C or POSIX says nothing about the language (Warp sets LANG=C.UTF-8), so it is skipped.
func Detect(choice string) *Language {
	if l, ok := Lookup(choice); ok {
		return l
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(key)
		if v == "" || strings.HasPrefix(v, "C") || strings.HasPrefix(v, "POSIX") {
			continue
		}
		if l, ok := Lookup(v); ok {
			return l
		}
		return langEN
	}
	if out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
		fields := strings.FieldsFunc(string(out), func(r rune) bool { return strings.ContainsRune("()\", \n", r) })
		if len(fields) > 0 {
			if l, ok := Lookup(fields[0]); ok {
				return l
			}
		}
	}
	return langEN
}

// T takes the string from the current language and falls back to English.
func T(key string, args ...any) string {
	s, ok := Current().msgs[key]
	if !ok {
		if s, ok = langEN.msgs[key]; !ok {
			return key
		}
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// N returns the noun in the plural form that the current language uses for n.
func N(n int, key string) string {
	l := Current()
	forms, ok := l.nouns[key]
	if !ok {
		l = langEN
		forms = l.nouns[key]
	}
	if len(forms) == 0 {
		return key
	}
	return forms[min(l.plural(n), len(forms)-1)]
}

func oneOther(n int) int {
	if n == 1 {
		return 0
	}
	return 1
}

// In French and Brazilian Portuguese, zero is singular too.
func zeroOneOther(n int) int {
	if n == 0 || n == 1 {
		return 0
	}
	return 1
}

func polish(n int) int {
	switch {
	case n == 1:
		return 0
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return 1
	default:
		return 2
	}
}

func eastSlavic(n int) int {
	switch {
	case n%10 == 1 && n%100 != 11:
		return 0
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return 1
	default:
		return 2
	}
}

func noPlural(int) int { return 0 }
