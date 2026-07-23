package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileMissingIsSilent(t *testing.T) {
	p, warns, err := LoadFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing file returned error: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("missing file produced warnings: %v", warns)
	}
	if (p != Partial{}) {
		t.Fatalf("missing file produced a non-zero Partial: %+v", p)
	}
}

func TestLoadFileValidKeys(t *testing.T) {
	path := writeConfig(t, `
# a comment
   # indented comment

output_dir = /music/out
format=flac
audio_quality = 5
playlist_default = TRUE
embed_thumbnail = false
embed_metadata=true
`)
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if p.OutputDir == nil || *p.OutputDir != "/music/out" {
		t.Errorf("OutputDir = %v, want /music/out", p.OutputDir)
	}
	if p.Format == nil || *p.Format != "flac" {
		t.Errorf("Format = %v, want flac", p.Format)
	}
	if p.AudioQuality == nil || *p.AudioQuality != "5" {
		t.Errorf("AudioQuality = %v, want 5", p.AudioQuality)
	}
	if p.PlaylistDefault == nil || !*p.PlaylistDefault {
		t.Errorf("PlaylistDefault = %v, want true (case-insensitive)", p.PlaylistDefault)
	}
	if p.EmbedThumbnail == nil || *p.EmbedThumbnail {
		t.Errorf("EmbedThumbnail = %v, want false", p.EmbedThumbnail)
	}
	if p.EmbedMetadata == nil || !*p.EmbedMetadata {
		t.Errorf("EmbedMetadata = %v, want true", p.EmbedMetadata)
	}
}

// The regex and template values contain spaces, (), \, (?i:...) and even a '#'.
// They must survive verbatim: split on the FIRST '=', no unquoting, no inline
// comment stripping.
func TestLoadFileRawValuesPreserved(t *testing.T) {
	nameTpl := `%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s`
	brackets := `\s*\[[^]]*\]`
	tags := `\s*\((?i:original mix|original|extended mix|extended|radio edit)\)`
	weird := `a = b # not a comment = still value`

	path := writeConfig(t, "name_template = "+nameTpl+"\n"+
		"strip_brackets = "+brackets+"\n"+
		"strip_tags = "+tags+"\n"+
		"output_dir = "+weird+"\n")

	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if p.NameTemplate == nil || *p.NameTemplate != nameTpl {
		t.Errorf("NameTemplate = %q, want %q", derefS(p.NameTemplate), nameTpl)
	}
	if p.StripBrackets == nil || *p.StripBrackets != brackets {
		t.Errorf("StripBrackets = %q, want %q", derefS(p.StripBrackets), brackets)
	}
	if p.StripTags == nil || *p.StripTags != tags {
		t.Errorf("StripTags = %q, want %q", derefS(p.StripTags), tags)
	}
	// first '=' split: key "output_dir", value is the whole remainder after the
	// first '=' — including later '=' and the '#', which is NOT a comment here.
	if p.OutputDir == nil || *p.OutputDir != weird {
		t.Errorf("OutputDir = %q, want %q (raw remainder after the first '=')", derefS(p.OutputDir), weird)
	}
}

func TestLoadFileHomeExpansion(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	path := writeConfig(t, "output_dir = ~/Music/x\n")
	p, _, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.OutputDir == nil || *p.OutputDir != "/home/tester/Music/x" {
		t.Errorf("OutputDir = %q, want /home/tester/Music/x", derefS(p.OutputDir))
	}

	path = writeConfig(t, "output_dir = ~\n")
	p, _, _ = LoadFile(path)
	if p.OutputDir == nil || *p.OutputDir != "/home/tester" {
		t.Errorf("bare ~ = %q, want /home/tester", derefS(p.OutputDir))
	}

	// No $VAR expansion.
	path = writeConfig(t, "output_dir = $HOME/x\n")
	p, _, _ = LoadFile(path)
	if p.OutputDir == nil || *p.OutputDir != "$HOME/x" {
		t.Errorf("OutputDir = %q, want the literal $HOME/x (no var expansion)", derefS(p.OutputDir))
	}
}

func TestLoadFileUnknownKeyWarns(t *testing.T) {
	// Future/GUI keys must warn-and-ignore, never brick an older binary. These
	// two stay out of the 2A whitelist (concurrency → 2B; overwrites would break
	// golden parity, so it is excluded on purpose).
	path := writeConfig(t, "concurrency = 4\noverwrites = true\nformat = mp3\n")
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 2 {
		t.Fatalf("want 2 unknown-key warnings, got %d: %v", len(warns), warns)
	}
	if p.Format == nil || *p.Format != "mp3" {
		t.Errorf("the known key after unknown ones was dropped: %v", p.Format)
	}
}

func TestLoadFileBackendKeys(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	path := writeConfig(t, `
log_dir = ~/state/ytdl/logs
log_retention_days = 7
breadcrumb_on_failure = false
notify = false
notify_on = failure
notify_foreground = true
notify_sound = false
`)
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if p.LogDir == nil || *p.LogDir != "/home/tester/state/ytdl/logs" {
		t.Errorf("LogDir = %v, want expanded ~/state/ytdl/logs", derefS(p.LogDir))
	}
	if p.LogRetentionDays == nil || *p.LogRetentionDays != 7 {
		t.Errorf("LogRetentionDays = %v, want 7", p.LogRetentionDays)
	}
	if p.BreadcrumbOnFailure == nil || *p.BreadcrumbOnFailure {
		t.Errorf("BreadcrumbOnFailure = %v, want false", p.BreadcrumbOnFailure)
	}
	if p.Notify == nil || *p.Notify {
		t.Errorf("Notify = %v, want false", p.Notify)
	}
	if p.NotifyOn == nil || *p.NotifyOn != "failure" {
		t.Errorf("NotifyOn = %v, want failure", derefS(p.NotifyOn))
	}
	if p.NotifyForeground == nil || !*p.NotifyForeground {
		t.Errorf("NotifyForeground = %v, want true", p.NotifyForeground)
	}
	if p.NotifySound == nil || *p.NotifySound {
		t.Errorf("NotifySound = %v, want false", p.NotifySound)
	}
}

func TestLoadFileBackendInvalidValues(t *testing.T) {
	// Invalid values on known keys warn and fall through (nil), never fatal.
	path := writeConfig(t, "log_retention_days = -1\nlog_retention_days = x\nnotify_on = sometimes\nlog_dir = \n")
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 4 {
		t.Fatalf("want 4 invalid-value warnings, got %d: %v", len(warns), warns)
	}
	if p.LogRetentionDays != nil {
		t.Errorf("invalid log_retention_days was kept: %v", *p.LogRetentionDays)
	}
	if p.NotifyOn != nil {
		t.Errorf("invalid notify_on was kept: %v", *p.NotifyOn)
	}
	if p.LogDir != nil {
		t.Errorf("empty log_dir was kept: %v", *p.LogDir)
	}
}

func TestLoadFileMalformedLineWarns(t *testing.T) {
	path := writeConfig(t, "this line has no equals\n= empty key\nformat = mp3\n")
	_, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 2 {
		t.Fatalf("want 2 malformed-line warnings, got %d: %v", len(warns), warns)
	}
}

func TestLoadFileInvalidValueOnKnownKeyWarnsAndFallsThrough(t *testing.T) {
	pinHome(t)
	path := writeConfig(t, "format = bogus\nplaylist_default = maybe\naudio_quality = x\n")
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 3 {
		t.Fatalf("want 3 invalid-value warnings, got %d: %v", len(warns), warns)
	}
	// Invalid values must be left unset (nil) so they fall through to defaults.
	if p.Format != nil {
		t.Errorf("invalid format was kept: %v", *p.Format)
	}
	if p.PlaylistDefault != nil {
		t.Errorf("invalid playlist_default was kept: %v", *p.PlaylistDefault)
	}
	if p.AudioQuality != nil {
		t.Errorf("invalid audio_quality was kept: %v", *p.AudioQuality)
	}
	// End-to-end: the invalid config values fall through to the built-in defaults.
	got, _ := Resolve(Partial{}, Partial{}, p, Env{})
	if got.Format != DefaultFormat {
		t.Errorf("resolved Format = %q, want default %q", got.Format, DefaultFormat)
	}
}

// An empty output_dir value must warn and fall through to the default, not
// override it with "" (which apply() would otherwise honour).
func TestLoadFileEmptyOutputDirWarnsAndFallsThrough(t *testing.T) {
	pinHome(t)
	path := writeConfig(t, "output_dir = \nformat = flac\n")
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 {
		t.Fatalf("want 1 empty-output_dir warning, got %d: %v", len(warns), warns)
	}
	if p.OutputDir != nil {
		t.Errorf("empty output_dir was kept: %q", *p.OutputDir)
	}
	// End-to-end: the empty value falls through to the built-in default dir.
	got, _ := Resolve(Partial{}, Partial{}, p, Env{})
	if got.OutputDir != Defaults().OutputDir {
		t.Errorf("resolved OutputDir = %q, want default %q", got.OutputDir, Defaults().OutputDir)
	}
	// The later key must still be parsed.
	if p.Format == nil || *p.Format != "flac" {
		t.Errorf("key after empty output_dir was dropped: %v", p.Format)
	}
}

// A raw value larger than bufio.Scanner's default 64 KiB token must still parse
// (the buffer is widened), and must not discard later keys.
func TestLoadFileLargeValue(t *testing.T) {
	big := strings.Repeat("x", 200*1024)
	path := writeConfig(t, "name_template = "+big+"\nformat = flac\n")
	p, warns, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if p.NameTemplate == nil || *p.NameTemplate != big {
		t.Errorf("large name_template not preserved (got len %d, want %d)", len(derefS(p.NameTemplate)), len(big))
	}
	if p.Format == nil || *p.Format != "flac" {
		t.Errorf("key after the large line was dropped: %v", p.Format)
	}
}

func derefS(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
