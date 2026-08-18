package update

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// The strict `key = value` format ytdl uses for deps.conf and for the installer
// marker. It is the config file's discipline (U4) reused deliberately: the file
// is READ, never sourced, and every key is checked against a whitelist — which is
// also what lets install.sh parse the same file with awk on a stock Mac, where
// jq does not exist and hand-rolled JSON parsing in bash is how installers get
// subtle (design §2).

// maxLineLen bounds one line and maxKeys bounds the file, so a wrong URL that
// answers with something enormous cannot be read into memory as a pin.
const (
	maxLineLen = 4096
	maxKeys    = 64
)

// parseKeyValue reads the format above. A line whose first non-space rune is '#'
// is a comment, blank lines are skipped, and each remaining line splits on its
// FIRST '=' with both sides trimmed.
//
// strict decides what an unknown key means. No reader in this package passes true
// — the only caller that does is the test which parses the repository's OWN
// deps.conf the way install.sh will. install.sh is the strict reader, and rightly:
// it is fetched from the same commit as deps.conf, so a key it does not know is
// genuinely a typo in the pin and refusing costs nothing.
//
// A deployed ytdl binary is in the opposite position — it is arbitrarily older
// than the deps.conf it reads, so a key added by a later cycle would make the pin
// unreadable for every existing installation, dropping the whole fleet to "non
// verificato" and stopping it seeing the very update that teaches it the new key.
// The probe therefore fails only on what it actually needs and cannot get
// (ADR-0016 §14.2, argued at length at probe.go's deps keys).
//
// The marker is tolerant for a different reason: it is ytdl's own note to itself,
// and a newer installer's extra key must not cost this binary the values it does
// know. The parameter stays because "unknown key" is a real decision either way,
// and a reader should have to see which one is in force.
func parseKeyValue(r io.Reader, allowed map[string]bool, strict bool) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(io.LimitReader(r, maxLineLen*maxKeys))
	sc.Buffer(make([]byte, 0, 1024), maxLineLen)
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
			return nil, fmt.Errorf("line %d: malformed (no key = value): %q", line, trimmed)
		}
		if !allowed[key] {
			if strict {
				return nil, fmt.Errorf("line %d: unknown key %q", line, key)
			}
			continue
		}
		if len(out) >= maxKeys {
			return nil, fmt.Errorf("line %d: too many keys", line)
		}
		out[key] = strings.TrimSpace(value)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: %w", line+1, err)
	}
	return out, nil
}

// maxVersionLen caps a version string. Tags are short; anything longer is a
// server answering with something that is not a tag, and it would land in JSON,
// in a terminal line and in the GUI.
const maxVersionLen = 64

// validVersion reports whether s is a plausible version string: non-empty, short,
// and drawn from the charset real tags use. Both repositories this package reads
// are covered — ytdl's "v2.1.0" and yt-dlp's "2026.07.04" — as is the ffmpeg
// build id "1785863997_9.0".
//
// This is not cosmetic. The string is compared, cached as JSON, printed to a
// terminal and rendered into the page; accepting whatever a redirect happened to
// carry is how a control character or a hundred-kilobyte "version" reaches all
// four.
func validVersion(s string) bool {
	if s == "" || len(s) > maxVersionLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_', r == '+':
		default:
			return false
		}
	}
	return true
}

// sanitizeVersion returns s when it is a usable version string and "" otherwise.
// An unusable answer from a local `--version` is treated as no answer at all,
// which leaves the fact empty and therefore uncompared — never displayed raw.
func sanitizeVersion(s string) string {
	if !validVersion(s) {
		return ""
	}
	return s
}
