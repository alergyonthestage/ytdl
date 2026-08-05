package cli

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/term"
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

// TestRenderHistoryHintSurvivesTheTerminalWidth: every line of this listing is
// clipped to the terminal width, and the longest hints are well over 80 columns
// — the ffmpeg one used to end in "ytdl --updat…", a command that does not
// exist. An instruction must WRAP where a title may be clipped.
func TestRenderHistoryHintSurvivesTheTerminalWidth(t *testing.T) {
	at := time.Date(2026, 7, 23, 20, 47, 0, 0, time.Local)
	const width = 80
	entries := []logstore.Entry{
		{Time: at, URL: "https://youtu.be/A", Success: false,
			Error: "ERROR: Postprocessing: ffprobe and ffmpeg not found"},
		{Time: at.Add(-time.Minute), URL: "https://youtu.be/B", Success: false,
			Error: "ERROR: Unable to download webpage: HTTP Error 429: Too Many Requests"},
	}
	got := RenderHistory(entries, HistoryView{IndexUsable: true, Width: width})

	for _, ln := range strings.Split(got, "\n") {
		if w := term.DisplayWidth(ln); w > width {
			t.Errorf("line of %d columns exceeds the terminal width:\n%s", w, ln)
		}
	}
	// The command must appear whole, not as the tail of a clipped line.
	if !strings.Contains(got, "ytdl --update.") {
		t.Errorf("the remedy was truncated into a command that does not exist:\n%s", got)
	}
	// And the actionable half of the long hint must still be there.
	if !strings.Contains(got, "riduci i download in parallelo nelle impostazioni") {
		t.Errorf("the long hint lost its actionable half:\n%s", got)
	}
}

// A pipe has no width to wrap to: the hint stays on one line, whole.
func TestRenderHistoryHintIsWholeWhenThereIsNoWidth(t *testing.T) {
	at := time.Date(2026, 7, 23, 20, 47, 0, 0, time.Local)
	entries := []logstore.Entry{
		{Time: at, URL: "https://youtu.be/A", Success: false,
			Error: "ERROR: Unable to download webpage: HTTP Error 429: Too Many Requests"},
	}
	got := RenderHistory(entries, HistoryView{IndexUsable: true}) // Width 0
	if !strings.Contains(got, "riduci i download in parallelo nelle impostazioni.") {
		t.Errorf("the hint was broken up with no width to wrap to:\n%s", got)
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
