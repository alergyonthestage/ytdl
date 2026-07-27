package term

import "strings"

// DisplayWidth returns the number of terminal columns s occupies. It approximates
// the common cases that matter for keeping a --watch line from wrapping: East-Asian
// Wide / Fullwidth runes and emoji count as 2 columns, combining/zero-width runes
// as 0, everything else as 1. The approximation deliberately errs toward
// OVER-counting ambiguous runes (never under-counting), so a clipped line is at
// worst a little short — never wide enough to wrap. It is pure and standard-library
// only. Note: ytdl's own status glyphs (▸ • ✓ ✗ ·) are narrow (width 1) and are
// intentionally NOT in the wide ranges below.
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// Clip truncates s to at most maxCols terminal columns, appending an ellipsis '…'
// when it shortens (never splitting a rune). It is how the --watch redraw keeps a
// line on one physical row — a wrapped line would desync the region redraw's
// logical-newline cursor math. maxCols <= 0 yields "".
func Clip(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if DisplayWidth(s) <= maxCols {
		return s
	}
	budget := maxCols - 1 // reserve one column for the ellipsis
	w := 0
	var b strings.Builder
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > budget {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteRune('…')
	return b.String()
}

// Pad right-pads s with spaces to exactly cols terminal COLUMNS, or clips it to
// cols when it is already wider. It is what keeps a table's columns aligned when
// a cell holds CJK or emoji: fmt's "%-20s" pads by BYTES, which misaligns a
// column as soon as a title is not plain ASCII. cols <= 0 yields s unchanged.
func Pad(s string, cols int) string {
	if cols <= 0 {
		return s
	}
	w := DisplayWidth(s)
	if w > cols {
		// Clip stops BEFORE a rune that would exceed its budget, so a 2-column
		// CJK or emoji rune at the boundary leaves the cell one column short —
		// and every column to its right on that row shifts left by one, which is
		// exactly the misalignment Pad exists to prevent. Re-measure and top up.
		c := Clip(s, cols)
		return c + strings.Repeat(" ", cols-DisplayWidth(c))
	}
	return s + strings.Repeat(" ", cols-w)
}

func runeWidth(r rune) int {
	switch {
	case isZeroWidth(r):
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

// isZeroWidth reports runes that advance the cursor by 0 columns: C0/C1 controls,
// the combining-mark ranges, and the common zero-width format characters.
func isZeroWidth(r rune) bool {
	switch {
	case r < 0x20 || (r >= 0x7F && r <= 0x9F): // C0 / DEL / C1 controls
		return true
	case r >= 0x0300 && r <= 0x036F, // Combining Diacritical Marks
		r >= 0x1AB0 && r <= 0x1AFF, // Combining Diacritical Marks Extended
		r >= 0x1DC0 && r <= 0x1DFF, // Combining Diacritical Marks Supplement
		r >= 0x20D0 && r <= 0x20FF, // Combining Diacritical Marks for Symbols
		r >= 0xFE20 && r <= 0xFE2F: // Combining Half Marks
		return true
	case r == 0x200B, r == 0x200C, r == 0x200D, r == 0xFEFF: // ZWSP, ZWNJ, ZWJ, BOM
		return true
	}
	return false
}

// isWide reports the East-Asian-wide / fullwidth / emoji runes that render as two
// terminal columns. The ranges cover CJK, Kana, Hangul, fullwidth forms and the
// main emoji blocks — the realistic sources of wide characters in a video title.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK Radicals, Kangxi, CJK Symbols (leading)
		r >= 0x3041 && r <= 0x33FF, // Hiragana, Katakana, CJK symbols/punct, enclosed
		r >= 0x3400 && r <= 0x4DBF, // CJK Unified Ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60, // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth signs
		return true
	case r >= 0x1F000 && r <= 0x1FAFF, // Mahjong/Domino/emoji & pictographs
		r >= 0x20000 && r <= 0x3FFFD: // CJK Ext B+ (supplementary ideographic planes)
		return true
	}
	return false
}
