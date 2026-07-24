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
