package cli

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alergyonthestage/ytdl/internal/logstore"
)

func TestRenderHistoryEmpty(t *testing.T) {
	got := RenderHistory(nil, HistoryView{RetentionDays: 30, IndexUsable: true})
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
	got := RenderHistory(entries, HistoryView{IndexUsable: true}) // retention 0 = keep forever
	if !strings.Contains(got, "da sempre") {
		t.Errorf("retention 0 should label 'da sempre':\n%s", got)
	}
	if !strings.Contains(got, "[1] ✓ 23/07 20:47  Massive Attack - Black Milk") {
		t.Errorf("success row (numbered, title-first) wrong:\n%s", got)
	}
	if !strings.Contains(got, "[2] ✗ ") {
		t.Errorf("failure row wrong:\n%s", got)
	}
	if strings.Contains(got, "https://www.") {
		t.Errorf("URL should be shortened (no scheme/www):\n%s", got)
	}
}

// TestRenderHistoryFailureCarriesTheNextStep: a row that only states what went
// wrong is incomplete (ux-principles.md §5). The hint is derived from the stored
// reason, so it also appears on records written before the catalogue existed
// (G8), and it goes on its own line — appended to the row it would be the first
// thing the width clip removed.
func TestRenderHistoryFailureCarriesTheNextStep(t *testing.T) {
	at := time.Date(2026, 7, 23, 20, 47, 0, 0, time.Local)
	entries := []logstore.Entry{
		{Time: at, URL: "https://youtu.be/A", Success: false,
			Error: "ERROR: [youtube] A: Unable to extract player response; please report this issue"},
		{Time: at.Add(-time.Hour), Title: "Un successo", Success: true},
	}
	got := RenderHistory(entries, HistoryView{IndexUsable: true})

	if !strings.Contains(got, "ytdl --update") {
		t.Errorf("the failed row says what went wrong but not what to do:\n%s", got)
	}
	// One line for the row, one for the hint — and nothing under the success.
	lines := strings.Split(got, "\n")
	var hintLines int
	for _, ln := range lines {
		if strings.Contains(ln, "ytdl --update") {
			hintLines++
			if strings.Contains(ln, "✗") {
				t.Errorf("the hint was appended to the row instead of its own line:\n%s", ln)
			}
		}
	}
	if hintLines != 1 {
		t.Errorf("expected exactly one hint line, got %d:\n%s", hintLines, got)
	}
}

// A failure ytdl has no honest remedy for prints no hint line at all.
func TestRenderHistoryPrintsNoHintWithoutARemedy(t *testing.T) {
	at := time.Date(2026, 7, 23, 20, 47, 0, 0, time.Local)
	entries := []logstore.Entry{
		{Time: at, URL: "https://youtu.be/A", Success: false,
			Error: "ERROR: [youtube] A: Sign in to confirm you're not a bot"},
	}
	got := RenderHistory(entries, HistoryView{IndexUsable: true})
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, hintIndent) && strings.TrimSpace(ln) != "" {
			t.Errorf("a hint was invented where no remedy exists:\n%s", ln)
		}
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
