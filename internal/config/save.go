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
// assign), so writing one would produce a file LoadFile complains about.
func Save(path string, s Settings) error {
	if path == "" {
		return errors.New("config: empty config path")
	}

	// Emitted in Settings/whitelist order so the file reads like the documented
	// config top-to-bottom. Every entry round-trips through LoadFile.
	kvs := []struct{ key, val string }{
		{"output_dir", s.OutputDir},
		{"format", s.Format},
		{"audio_quality", s.AudioQuality},
		{"playlist_default", strconv.FormatBool(s.PlaylistDefault)},
		{"name_template", s.NameTemplate},
		{"strip_brackets", s.StripBrackets},
		{"strip_tags", s.StripTags},
		{"embed_thumbnail", strconv.FormatBool(s.EmbedThumbnail)},
		{"embed_metadata", strconv.FormatBool(s.EmbedMetadata)},
		{"log_dir", s.LogDir},
		{"log_retention_days", strconv.Itoa(s.LogRetentionDays)},
		{"breadcrumb_on_failure", strconv.FormatBool(s.BreadcrumbOnFailure)},
		{"notify", strconv.FormatBool(s.Notify)},
		{"notify_on", s.NotifyOn},
		{"notify_foreground", strconv.FormatBool(s.NotifyForeground)},
		{"notify_sound", strconv.FormatBool(s.NotifySound)},
		{"concurrency", concurrencyValue(s.Concurrency)},
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
		fmt.Fprintf(&b, "%s = %s\n", kv.key, kv.val)
	}
	return atomicWrite(path, []byte(b.String()))
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
// success the rename replaces path in one step (0644, like a hand-written config).
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
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
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // renamed into place: keep it
	return nil
}
