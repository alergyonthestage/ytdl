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
	s.JobTimeout = 300
	s.OpenFolderOnDone = true
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

// Save must refuse anything LoadFile would drop on reload: otherwise a settings
// editor reports success while the value silently snaps back to a default (and
// the daemon's warning goes to /dev/null).
func TestSaveRejectsValuesThatWouldNotReload(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]func(*Settings){
		"empty format":             func(s *Settings) { s.Format = "" },
		"invalid format":           func(s *Settings) { s.Format = "mkv" },
		"empty notify_on":          func(s *Settings) { s.NotifyOn = "" },
		"invalid audio_quality":    func(s *Settings) { s.AudioQuality = "12" },
		"negative retention":       func(s *Settings) { s.LogRetentionDays = -5 },
		"negative concurrency":     func(s *Settings) { s.Concurrency = -1 },
		"negative job_timeout":     func(s *Settings) { s.JobTimeout = -1 },
		"empty output_dir":         func(s *Settings) { s.OutputDir = "" },
		"empty name_template":      func(s *Settings) { s.NameTemplate = "" },
		"padded name_template":     func(s *Settings) { s.NameTemplate = "  %(title)s  " },
		"padded strip_tags":        func(s *Settings) { s.StripTags = `\s*\(x\) ` },
		"newline in name_template": func(s *Settings) { s.NameTemplate = "a\nnotify = false" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := sampleSettings(dir)
			mutate(&s)
			path := filepath.Join(t.TempDir(), "config")
			if err := Save(path, s); err == nil {
				t.Errorf("Save accepted %s", name)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("a rejected Save must not write the file")
			}
		})
	}
}

// A path under $HOME is written back ~-relative, so a config saved from the GUI
// stays portable, and still resolves to the same absolute path on reload.
func TestSaveContractsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config")

	want := sampleSettings(home)
	want.OutputDir = filepath.Join(home, "Music", "ytdl")
	want.LogDir = filepath.Join(home, ".local", "state", "ytdl", "logs")
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "output_dir = ~/Music/ytdl") {
		t.Errorf("output_dir not contracted to ~:\n%s", data)
	}
	got, warns := resolveFile(t, path)
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if got.OutputDir != want.OutputDir || got.LogDir != want.LogDir {
		t.Errorf("~ round-trip broken:\n got=(%q,%q)\nwant=(%q,%q)",
			got.OutputDir, got.LogDir, want.OutputDir, want.LogDir)
	}
}

// A symlinked config (a dotfiles repo) must be followed, not replaced with a
// regular file — otherwise the user's real config silently stops being updated.
func TestSaveFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-config")
	if err := os.WriteFile(real, []byte("format = wav\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	s := sampleSettings(dir)
	s.Format = "flac"
	if err := Save(link, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("Save replaced the symlink with a regular file")
	}
	data, _ := os.ReadFile(real)
	if !strings.Contains(string(data), "format = flac") {
		t.Errorf("the symlink target was not updated:\n%s", data)
	}
	// A deliberately restrictive mode must survive a save.
	if got := fi2Mode(t, real); got != 0o600 {
		t.Errorf("mode = %o, want 0600 preserved", got)
	}
}

func fi2Mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}
