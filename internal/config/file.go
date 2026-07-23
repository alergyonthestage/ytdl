package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ConfigPath returns the config file location:
// ${XDG_CONFIG_HOME:-$HOME/.config}/ytdl/config.
func ConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = home + "/.config"
	}
	return base + "/ytdl/config"
}

// StatePath returns ytdl's base state directory:
// ${XDG_STATE_HOME:-$HOME/.local/state}/ytdl. It returns "" when neither
// $XDG_STATE_HOME nor $HOME is resolvable, in which case the central log store
// is skipped (best-effort — a download itself already fails fast without $HOME).
func StatePath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = home + "/.local/state"
	}
	return base + "/ytdl"
}

// LoadFile parses the config file at path into a Partial. It is read-if-present:
// a missing file yields a zero Partial and no error (all defaults, no warning).
// Parsing is minimal and tolerant (design-cycle1-remaining.md §2.2):
//
//   - a line whose first non-space rune is '#' is a comment; blank lines are skipped;
//   - each remaining line is split on its FIRST '='; the key is the left side
//     trimmed, the value is the right side trimmed of surrounding whitespace only
//     (no inline comments, no unquoting, no escaping — the value is the raw
//     remainder, so regexes and templates containing '#', '\', '(' survive intact);
//   - a line with no '=' or an empty key is malformed → warn, skip;
//   - a key not in the whitelist → warn, ignore (forward/backward compatibility);
//   - a known key with an invalid value → warn, ignore (falls through to a lower layer).
//
// It never returns an error for bad content: only a genuine read failure (a file
// that exists but cannot be opened) is returned.
func LoadFile(path string) (Partial, []Warning, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Partial{}, nil, nil
		}
		return Partial{}, nil, err
	}
	defer f.Close()

	var p Partial
	var warns []Warning
	sc := bufio.NewScanner(f)
	// Raw name_template/strip_* values have no length cap, so widen past the
	// default 64 KiB to avoid ErrTooLong on a big regex line.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			warns = append(warns, Warning{Line: line, Msg: fmt.Sprintf("malformed line (no key=value): %q", raw)})
			continue
		}
		value = strings.TrimSpace(value)
		if w := assign(&p, key, value, line); w != nil {
			warns = append(warns, *w)
		}
	}
	if err := sc.Err(); err != nil {
		// Bad content (an over-long line) or a mid-read error: keep whatever was
		// parsed and warn, rather than discarding every key parsed so far.
		warns = append(warns, Warning{Line: line + 1, Msg: fmt.Sprintf("lettura interrotta: %v", err)})
		return p, warns, nil
	}
	return p, warns, nil
}

// assign validates value for key and, if valid, records it in p. It returns a
// Warning (and leaves p untouched for that field) for an unknown key or an
// invalid value; nil on success.
func assign(p *Partial, key, value string, line int) *Warning {
	switch key {
	case "output_dir":
		// An empty value (e.g. `output_dir =`) must not win over the built-in
		// default: a non-nil *string, even pointing at "", would override it in
		// apply(). Warn and ignore, mirroring the other keys' validation.
		if value == "" {
			return &Warning{Line: line, Msg: "empty output_dir; ignoring"}
		}
		v := expandHome(value)
		p.OutputDir = &v
	case "format":
		if !ValidFormat(value) {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid format %q (want %s); ignoring", value, FormatList)}
		}
		v := value
		p.Format = &v
	case "audio_quality":
		if !validAudioQuality(value) {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid audio_quality %q (want 0-9); ignoring", value)}
		}
		v := value
		p.AudioQuality = &v
	case "playlist_default":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid playlist_default %q (want true|false); ignoring", value)}
		}
		p.PlaylistDefault = &b
	case "name_template":
		v := value
		p.NameTemplate = &v
	case "strip_brackets":
		v := value
		p.StripBrackets = &v
	case "strip_tags":
		v := value
		p.StripTags = &v
	case "embed_thumbnail":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid embed_thumbnail %q (want true|false); ignoring", value)}
		}
		p.EmbedThumbnail = &b
	case "embed_metadata":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid embed_metadata %q (want true|false); ignoring", value)}
		}
		p.EmbedMetadata = &b
	case "log_dir":
		// Like output_dir, an empty value must not win over the default via a
		// non-nil pointer to "".
		if value == "" {
			return &Warning{Line: line, Msg: "empty log_dir; ignoring"}
		}
		v := expandHome(value)
		p.LogDir = &v
	case "log_retention_days":
		n, ok := parseNonNegInt(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid log_retention_days %q (want a non-negative integer); ignoring", value)}
		}
		p.LogRetentionDays = &n
	case "breadcrumb_on_failure":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid breadcrumb_on_failure %q (want true|false); ignoring", value)}
		}
		p.BreadcrumbOnFailure = &b
	case "notify":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid notify %q (want true|false); ignoring", value)}
		}
		p.Notify = &b
	case "notify_on":
		if !ValidNotifyOn(value) {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid notify_on %q (want %s); ignoring", value, NotifyOnList)}
		}
		v := value
		p.NotifyOn = &v
	case "notify_foreground":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid notify_foreground %q (want true|false); ignoring", value)}
		}
		p.NotifyForeground = &b
	case "notify_sound":
		b, ok := parseBool(value)
		if !ok {
			return &Warning{Line: line, Msg: fmt.Sprintf("invalid notify_sound %q (want true|false); ignoring", value)}
		}
		p.NotifySound = &b
	default:
		return &Warning{Line: line, Msg: fmt.Sprintf("unknown key %q; ignoring", key)}
	}
	return nil
}

// expandHome expands a leading "~/" or a bare "~" to $HOME. It deliberately does
// not do $VAR expansion (design-cycle1-remaining.md §2.3).
func expandHome(v string) string {
	if v == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return v
	}
	if strings.HasPrefix(v, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + v[1:]
		}
	}
	return v
}

// parseNonNegInt accepts a base-10 non-negative integer and nothing else.
func parseNonNegInt(v string) (int, bool) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// parseBool accepts true|false case-insensitively and nothing else.
func parseBool(v string) (bool, bool) {
	switch strings.ToLower(v) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}
