package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Save writes s to the config file at path as a canonical `key = value`
// document — the inverse of LoadFile. Every whitelist key is emitted explicitly,
// so the written file becomes the settings editor's single source of truth (U4,
// E2): Save then LoadFile yields a Partial that Resolve turns back into the same
// Settings.
//
// Only whitelisted keys are ever written, so the settings editor (Cycle 3 GUI)
// cannot inject a key that would change the yt-dlp argv (golden parity) — and, as
// throughout ytdl, the file is parsed, never `source`d. The write is atomic (a
// sibling temp file rename'd into place) so a crash mid-save never leaves a
// half-written config. A value containing a newline is rejected: it cannot
// survive the line-based format, and a settings editor must not be able to smuggle
// extra lines into the file.
//
// output_dir and log_dir are skipped when empty: an empty value reloads as a
// warning (a non-nil pointer to "" must not win over the built-in default, see
// assign), so writing one would produce a file LoadFile complains about. They are
// also written back in ~-relative form when they sit under $HOME, so a config
// saved from the GUI stays portable between machines instead of being frozen to
// one absolute path.
//
// Keys outside the whitelist are DROPPED, by design (that is what makes the
// settings editor injection-proof) — so a config written by a newer ytdl loses
// its unknown keys if an older one saves over it.
func Save(path string, s Settings) error {
	if path == "" {
		return errors.New("config: empty config path")
	}
	// Validate before writing. LoadFile drops invalid values with a warning, and
	// the daemon's warnings go to /dev/null — so without this a settings editor
	// could "successfully" save a file that silently reloads as something else.
	if err := validateForSave(s); err != nil {
		return err
	}

	// Emitted in Settings/whitelist order so the file reads like the documented
	// config top-to-bottom. Every entry round-trips through LoadFile.
	kvs := []struct{ key, val string }{
		{"output_dir", contractHome(s.OutputDir)},
		{"format", s.Format},
		{"audio_quality", s.AudioQuality},
		{"playlist_default", strconv.FormatBool(s.PlaylistDefault)},
		{"name_template", s.NameTemplate},
		{"strip_brackets", s.StripBrackets},
		{"strip_tags", s.StripTags},
		{"embed_thumbnail", strconv.FormatBool(s.EmbedThumbnail)},
		{"embed_metadata", strconv.FormatBool(s.EmbedMetadata)},
		{"log_dir", contractHome(s.LogDir)},
		{"log_retention_days", strconv.Itoa(s.LogRetentionDays)},
		{"breadcrumb_on_failure", strconv.FormatBool(s.BreadcrumbOnFailure)},
		{"notify", strconv.FormatBool(s.Notify)},
		{"notify_on", s.NotifyOn},
		{"notify_foreground", strconv.FormatBool(s.NotifyForeground)},
		{"notify_sound", strconv.FormatBool(s.NotifySound)},
		{"concurrency", concurrencyValue(s.Concurrency)},
		{"job_timeout", strconv.Itoa(s.JobTimeout)},
	}

	var b strings.Builder
	b.WriteString("# ytdl configuration\n")
	b.WriteString("# Written by the ytdl settings editor. One `key = value` per line;\n")
	b.WriteString("# hand edits are honoured on load, but comments are rewritten when saved here.\n\n")
	for _, kv := range kvs {
		if (kv.key == "output_dir" || kv.key == "log_dir") && kv.val == "" {
			continue
		}
		if strings.ContainsAny(kv.val, "\n\r") {
			return fmt.Errorf("config: value for %q contains a newline", kv.key)
		}
		// LoadFile trims surrounding whitespace, so an edge space cannot survive a
		// round-trip. Refuse rather than silently changing a value the user typed —
		// it matters for the strip_* regexes, where a trailing space is meaningful.
		if strings.TrimSpace(kv.val) != kv.val {
			return fmt.Errorf("config: value for %q has leading or trailing whitespace", kv.key)
		}
		fmt.Fprintf(&b, "%s = %s\n", kv.key, kv.val)
	}
	return atomicWrite(path, []byte(b.String()))
}

// validateForSave rejects what assign() would refuse on reload, so a written
// config always reloads to the Settings that were saved. It lives here, not in a
// caller, because Save is the only place that can guarantee that invariant.
func validateForSave(s Settings) error {
	if strings.TrimSpace(s.OutputDir) == "" {
		return errors.New("config: output_dir must not be empty")
	}
	if strings.TrimSpace(s.NameTemplate) == "" {
		// An empty name_template is a valid override that yields "-o <dir>/.%(ext)s":
		// every download becomes the same hidden file, each overwriting the last.
		return errors.New("config: name_template must not be empty")
	}
	if !ValidFormat(s.Format) {
		return fmt.Errorf("config: invalid format %q (want %s)", s.Format, FormatList)
	}
	if !validAudioQuality(s.AudioQuality) {
		return fmt.Errorf("config: invalid audio_quality %q (want 0-9)", s.AudioQuality)
	}
	if !ValidNotifyOn(s.NotifyOn) {
		return fmt.Errorf("config: invalid notify_on %q (want %s)", s.NotifyOn, NotifyOnList)
	}
	if s.LogRetentionDays < 0 {
		return fmt.Errorf("config: log_retention_days %d is negative", s.LogRetentionDays)
	}
	if s.Concurrency < 0 {
		return fmt.Errorf("config: concurrency %d is negative (0 = unlimited)", s.Concurrency)
	}
	if s.JobTimeout < 0 {
		return fmt.Errorf("config: job_timeout %d is negative (0 = no limit)", s.JobTimeout)
	}
	return nil
}

// contractHome is expandHome's inverse: a path under $HOME is written back as
// "~/..." so the saved config stays machine-agnostic.
func contractHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// concurrencyValue renders Concurrency the way parseConcurrency reads it back:
// ConcurrencyUnlimited (0) is the keyword `unlimited`, every other value its
// decimal form. A bare `0` would be rejected on reload (parseConcurrency wants
// ≥1 or the keyword), so it must never be written literally.
func concurrencyValue(c int) string {
	if c == ConcurrencyUnlimited {
		return "unlimited"
	}
	return strconv.Itoa(c)
}

// atomicWrite writes data to path via a sibling temp file + rename, creating the
// parent directory if needed. The temp file is removed on every failure path; on
// success the rename replaces the file in one step.
//
// Two details that matter for a real user's config: a SYMLINKED config (a
// dotfiles repo is the common case) is followed, so we update the file it points
// at instead of replacing the link with a regular file; and the existing
// permissions are preserved, so a deliberately 0600 config does not silently
// widen to 0644.
func atomicWrite(path string, data []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ytdl-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName) // no-op after a successful rename (tmpName cleared)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	// Flush to disk before the rename: without it the atomicity claim holds for a
	// process crash but not for power loss, which can leave the renamed file empty.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	tmpName = "" // renamed into place: keep it
	return nil
}
