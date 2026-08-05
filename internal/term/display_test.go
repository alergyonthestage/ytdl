package term

import (
	"strings"
	"testing"
)

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"ytdl · queue", 12}, // the ytdl glyphs/·  are width 1
		{"▸ • ✓ ✗", 7},       // status glyphs are narrow
		{"テスト", 6},           // 3 CJK runes × 2
		{"😀😀", 4},            // emoji × 2
		{"é", 1},            // e + combining acute = 1 column
	}
	for _, c := range cases {
		if got := DisplayWidth(c.in); got != c.want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Clip must never return more display columns than allowed, must not split a rune,
// and must ellipsize when it shortens — across ASCII, CJK and emoji.
// TestWrap: the property that matters is that no line exceeds the budget and
// that no word is lost — Wrap exists for text that must survive intact (an
// instruction), unlike Clip, which exists for text that may be cut (a title).
func TestWrap(t *testing.T) {
	for _, s := range []string{
		"YouTube sta limitando le richieste: aspetta qualche minuto e riprova.",
		"Manca ffmpeg o è incompleto: reinstalla le dipendenze con  ytdl --update.",
		strings.Repeat("テスト ", 12),
		"corto",
		"",
	} {
		for _, max := range []int{0, 24, 40, 72, 200} {
			lines := Wrap(s, max)
			if len(lines) == 0 {
				t.Errorf("Wrap(%q, %d) returned no lines", s, max)
				continue
			}
			if max > 0 {
				for _, ln := range lines {
					if w := DisplayWidth(ln); w > max {
						t.Errorf("Wrap(%q, %d) line %q has width %d > %d", s, max, ln, w, max)
					}
				}
			}
			// Every word survives, in order, unless a single word was too wide to
			// fit a line of its own (then it is clipped, like Clip does).
			joined := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
			want := strings.Join(strings.Fields(s), " ")
			if joined != want && !strings.Contains(joined, "…") {
				t.Errorf("Wrap(%q, %d) lost text:\n got %q\nwant %q", s, max, joined, want)
			}
		}
	}
	// A word that cannot fit is clipped, never broken into a column of fragments.
	lines := Wrap("breve "+strings.Repeat("a", 50), 20)
	for _, ln := range lines {
		if DisplayWidth(ln) > 20 {
			t.Errorf("an over-long word was not clipped: %q", ln)
		}
	}
	// maxCols <= 0 means "no width to wrap to": one line, unchanged.
	if got := Wrap("a b c", 0); len(got) != 1 || got[0] != "a b c" {
		t.Errorf("Wrap with no width = %q, want the string unchanged on one line", got)
	}
}

func TestClip(t *testing.T) {
	for _, s := range []string{
		"hello world this is long",
		strings.Repeat("テスト", 10),
		strings.Repeat("😀", 10),
		"short",
	} {
		for _, max := range []int{0, 1, 2, 5, 8, 40} {
			got := Clip(s, max)
			if w := DisplayWidth(got); w > max {
				t.Errorf("Clip(%q, %d) = %q has width %d > %d", s, max, got, w, max)
			}
			if max > 0 && DisplayWidth(s) > max && !strings.HasSuffix(got, "…") {
				t.Errorf("Clip(%q, %d) = %q should end with an ellipsis", s, max, got)
			}
			if max > 0 && DisplayWidth(s) <= max && got != s {
				t.Errorf("Clip(%q, %d) = %q should be unchanged (fits)", s, max, got)
			}
		}
	}
}
