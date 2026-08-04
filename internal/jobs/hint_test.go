package jobs

import (
	"strings"
	"testing"
)

// TestFailureHintAnswersWithTheStepThatExists: every hint must name something
// the user can actually do today (G8, ux-principles.md §5). The raw lines are
// the shapes yt-dlp really emits.
func TestFailureHintAnswersWithTheStepThatExists(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   string // a substring of the expected hint
	}{
		{
			"stale yt-dlp",
			"ERROR: [youtube] dQw4w9WgXcQ: Unable to extract player response; please report this issue on https://github.com/yt-dl…",
			"ytdl --update",
		},
		{
			"rate limited",
			"ERROR: Unable to download webpage: HTTP Error 429: Too Many Requests",
			"aspetta qualche minuto",
		},
		{
			"video gone",
			"ERROR: [youtube] abc: Video unavailable. This video has been removed by the uploader",
			"non è più disponibile",
		},
		{
			"no ffmpeg",
			"ERROR: Postprocessing: ffprobe and ffmpeg not found. Please install or provide the path using --ffmpeg-location",
			"ytdl --update",
		},
		{
			"disk full",
			"ERROR: unable to write data: [Errno 28] No space left on device",
			"Spazio esaurito",
		},
		{
			"unwritable folder",
			"ERROR: unable to open for writing: [Errno 13] Permission denied: '/srv/musica/x.mp3'",
			"scegline un'altra",
		},
		{
			"no network",
			"ERROR: Unable to download webpage: <urlopen error [Errno -3] Temporary failure in name resolution>",
			"Connessione assente",
		},
		{
			"not a supported link",
			"ERROR: Unsupported URL: https://example.com/pagina",
			"controlla di aver incollato",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FailureHint(c.reason)
			if got == "" {
				t.Fatalf("no hint for %q", c.reason)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("hint = %q, want it to contain %q", got, c.want)
			}
		})
	}
}

// TestFailureHintStaysSilentWhereNoRemedyExists: an age- or bot-restricted
// video needs cookies_from_browser, which does not exist yet (G25, Cycle 9).
// Inventing a step the tool cannot perform is the same defect one layer up, so
// the row shows the raw reason alone.
func TestFailureHintStaysSilentWhereNoRemedyExists(t *testing.T) {
	for _, reason := range []string{
		"ERROR: [youtube] abc: Sign in to confirm you're not a bot. Use --cookies-from-browser or --cookies for the authentication",
		"ERROR: [youtube] abc: Sign in to confirm your age. This video may be inappropriate for some users",
		"",
		"ERROR: something nobody has classified yet",
	} {
		if got := FailureHint(reason); got != "" {
			t.Errorf("FailureHint(%q) = %q, want no hint", reason, got)
		}
	}
}

// TestFailureHintPrefersTheSpecificCause: a 429 also says "Unable to download
// webpage", and the answer to it is not "check your network". The table is
// ordered for exactly this reason.
func TestFailureHintPrefersTheSpecificCause(t *testing.T) {
	got := FailureHint("ERROR: Unable to download webpage: HTTP Error 429: Too Many Requests")
	if strings.Contains(got, "Connessione") {
		t.Errorf("a rate limit was reported as a network problem: %q", got)
	}
}
