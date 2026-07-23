package config

import (
	"bufio"
	"fmt"
	"os"
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
