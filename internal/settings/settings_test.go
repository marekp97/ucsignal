package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := Load(); got.Lang != "" {
		t.Fatalf("without a file: %+v", got)
	}
	if err := Save(Settings{Lang: "zh-Hant"}); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got.Lang != "zh-Hant" {
		t.Fatalf("after saving: %+v", got)
	}
}

func TestBrokenFileGivesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".config", "ucsignal", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{lang"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got.Lang != "" {
		t.Fatalf("corrupted file: %+v", got)
	}
}
