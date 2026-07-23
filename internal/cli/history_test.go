package cli

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alergyonthestage/ytdl/internal/logstore"
)

func TestRenderHistoryEmpty(t *testing.T) {
	got := RenderHistory(nil, 30)
	if !strings.Contains(got, "ultimi 30 giorni") {
		t.Errorf("history header missing window label:\n%s", got)
	}
	if !strings.Contains(got, "nessun download registrato") {
		t.Errorf("empty history missing notice:\n%s", got)
	}
}

func TestRenderHistoryRows(t *testing.T) {
	at := time.Date(2026, 7, 23, 20, 47, 0, 0, time.Local)
	entries := []logstore.Entry{
		{Time: at, Title: "Massive Attack - Black Milk", Success: true},
		{Time: at.Add(-time.Hour), URL: "https://www.youtube.com/watch?v=Xk3long", Success: false},
	}
	got := RenderHistory(entries, 0) // 0 = keep forever
	if !strings.Contains(got, "da sempre") {
		t.Errorf("retention 0 should label 'da sempre':\n%s", got)
	}
	if !strings.Contains(got, "✓ 23/07 20:47  Massive Attack - Black Milk") {
		t.Errorf("success row (title-first) wrong:\n%s", got)
	}
	// A failure with no title falls back to a shortened URL, flagged failed.
	if !strings.Contains(got, "✗ ") || !strings.Contains(got, "(fallito)") {
		t.Errorf("failure row wrong:\n%s", got)
	}
	if strings.Contains(got, "https://www.") {
		t.Errorf("URL should be shortened (no scheme/www):\n%s", got)
	}
}

func TestShortenURL(t *testing.T) {
	if got := shortenURL("https://www.youtube.com/watch?v=abc"); got != "youtube.com/watch?v=abc" {
		t.Errorf("shortenURL trim = %q", got)
	}
	long := "https://example.com/" + strings.Repeat("a", 80)
	if got := shortenURL(long); len([]rune(got)) > 40 || !strings.HasSuffix(got, "…") {
		t.Errorf("shortenURL cap = %q (len %d)", got, len([]rune(got)))
	}
	// A multi-byte tail must be cut on a rune boundary, never split into invalid UTF-8.
	got := shortenURL("example.com/" + strings.Repeat("日", 60))
	if !utf8.ValidString(got) {
		t.Errorf("shortenURL produced invalid UTF-8: %q", got)
	}
	if len([]rune(got)) > 40 {
		t.Errorf("shortenURL rune cap exceeded: %d runes", len([]rune(got)))
	}
}
