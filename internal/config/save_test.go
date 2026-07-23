package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleSettings returns a non-default Settings whose paths live under dir, so a
// Save→LoadFile→Resolve round-trip is deterministic (no reliance on $HOME).
func sampleSettings(dir string) Settings {
	s := Defaults()
	s.OutputDir = filepath.Join(dir, "out")
	s.LogDir = filepath.Join(dir, "logs")
	s.Format = "flac"
	s.AudioQuality = "5"
	s.PlaylistDefault = true
	s.EmbedThumbnail = false
	s.LogRetentionDays = 7
	s.NotifyOn = "failure"
	s.NotifyForeground = true
	s.Concurrency = 4
	return s
}

// resolveFile is the resolution main.go performs with only the config-file layer
// (empty flag/session/env), i.e. what a saved file reloads to. It returns the
// PARSE warnings (LoadFile) separately: a cleanly-reloadable Save'd file has
// none. Resolve's own advisory warnings (e.g. concurrency 'unlimited') are about
// the resolved value, not the file, so they are deliberately not folded in.
func resolveFile(t *testing.T, path string) (Settings, []Warning) {
	t.Helper()
	p, parseWarns, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	s, _ := Resolve(Partial{}, Partial{}, p, Env{})
	return s, parseWarns
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	want := sampleSettings(dir)

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, warns := resolveFile(t, path)
	if len(warns) != 0 {
		t.Errorf("reloading a Save'd file warned: %v", warns)
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestSaveConcurrencyUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	want := sampleSettings(dir)
	want.Concurrency = ConcurrencyUnlimited // must serialize as the keyword, not 0

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "concurrency = unlimited") {
		t.Errorf("ConcurrencyUnlimited should write the keyword; got:\n%s", data)
	}
	got, warns := resolveFile(t, path)
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if got.Concurrency != ConcurrencyUnlimited {
		t.Errorf("Concurrency round-trip = %d, want %d", got.Concurrency, ConcurrencyUnlimited)
	}
}

func TestSaveAtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("# stale hand-written config\nformat = wav\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := sampleSettings(dir)
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _ := resolveFile(t, path)
	if got.Format != "flac" {
		t.Errorf("overwrite did not take: format = %q, want flac", got.Format)
	}
	// No sibling temp file must be left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ytdl-config-") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestSaveCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "config")
	if err := Save(path, sampleSettings(dir)); err != nil {
		t.Fatalf("Save should create parent dirs: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config not written: %v", err)
	}
}

func TestSaveRejectsNewlineValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	s := sampleSettings(dir)
	s.NameTemplate = "%(title)s\nnotify = false" // a smuggled extra line
	if err := Save(path, s); err == nil {
		t.Fatal("Save must reject a value containing a newline")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Save must not create the file when it rejects a value")
	}
}

func TestSaveEmptyPath(t *testing.T) {
	if err := Save("", Defaults()); err == nil {
		t.Error("Save with an empty path must error")
	}
}

// TestSavePreservesRegexTemplates guards the crown-jewel values: the default
// name_template and strip_* regexes contain =, (, [, \, # — none of which may be
// mangled by the line format.
func TestSavePreservesRegexTemplates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	want := sampleSettings(dir) // keeps the default NameTemplate/StripBrackets/StripTags
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, warns := resolveFile(t, path)
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if got.NameTemplate != want.NameTemplate {
		t.Errorf("name_template mangled:\n got=%q\nwant=%q", got.NameTemplate, want.NameTemplate)
	}
	if got.StripBrackets != want.StripBrackets || got.StripTags != want.StripTags {
		t.Errorf("strip_* mangled:\n got=(%q,%q)\nwant=(%q,%q)",
			got.StripBrackets, got.StripTags, want.StripBrackets, want.StripTags)
	}
}
