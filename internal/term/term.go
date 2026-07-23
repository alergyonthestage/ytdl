// Package term is ytdl's tiny terminal helper: TTY detection and restrained
// colour for the CLI inspection commands. It is standard-library only (isatty is
// os.ModeCharDevice), preserving the single-static-binary distribution
// (ADR-0003); colour is applied at the edge so the pure cli renderers stay
// colour-free and golden-testable (ADR-0009).
package term

import (
	"os"
	"strings"
	"unicode/utf8"
)

// IsTTY reports whether f is a terminal (a character device). A piped or
// redirected stream is not, which is how callers avoid emitting ANSI into a file.
func IsTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ColorEnabled reports whether coloured output should be used for f: only when f
// is a TTY and NO_COLOR is unset (the https://no-color.org/ convention).
func ColorEnabled(f *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return IsTTY(f)
}

// ANSI codes (a deliberately small, restrained palette).
const (
	Reset = "\033[0m"
	green = "\033[32m"
	red   = "\033[31m"
	cyan  = "\033[36m"
	dim   = "\033[2m"
)

// Colorize tints ytdl's status glyphs — ✓ green, ✗ red, ▸/⟳ cyan, • dim — when
// enabled, colouring ONLY the leading glyph of each line (the status marker) and
// leaving everything else default. Scoping to the line-leading position (not a
// whole-string replace) means a glyph that legitimately appears inside a track
// title or URL — e.g. "•" as a separator in a real video title — is never spliced
// with colour. When enabled is false, s is returned verbatim.
func Colorize(s string, enabled bool) string {
	if !enabled {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		r, size := utf8.DecodeRuneInString(trimmed)
		if code, ok := glyphColor[r]; ok {
			indent := line[:len(line)-len(trimmed)]
			lines[i] = indent + code + string(r) + Reset + trimmed[size:]
		}
	}
	return strings.Join(lines, "\n")
}

var glyphColor = map[rune]string{
	'✓': green, '✗': red, '▸': cyan, '⟳': cyan, '•': dim,
}
