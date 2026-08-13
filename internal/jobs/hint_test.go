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

// TestFailureHintPrefersTheSpecificCause: several distinct causes share the
// same generic yt-dlp wording, and the ordering of the table is the only thing
// that keeps each one pointed at its own remedy. Every case here was a WRONG
// hint before the review pass — a wrong next step is worse than none, because
// the user follows it.
func TestFailureHintPrefersTheSpecificCause(t *testing.T) {
	cases := []struct {
		name       string
		reason     string
		want       string // substring the hint must contain
		mustNotSay string // the wrong remedy this case used to get
	}{
		{
			"a full disk during postprocessing is not a missing ffmpeg",
			"ERROR: Postprocessing: Error opening output file /Users/a/Music/x.mp3: No space left on device",
			"Spazio esaurito", "ytdl --update",
		},
		{
			"an unwritable folder during postprocessing is not a missing ffmpeg",
			"ERROR: Postprocessing: Error opening output files: Permission denied",
			"non scrivibile", "ytdl --update",
		},
		{
			"a rate limit is not a network problem",
			"ERROR: Unable to download webpage: HTTP Error 429: Too Many Requests",
			"aspetta qualche minuto", "Connessione",
		},
		{
			"a dead link is not a network problem",
			"ERROR: Unable to download webpage: HTTP Error 404: Not Found",
			"copiato per intero", "Connessione",
		},
		{
			"a 403 is a stale yt-dlp, not a network problem",
			"ERROR: Unable to download webpage: HTTP Error 403: Forbidden",
			"ytdl --update", "Connessione",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FailureHint(c.reason)
			if !strings.Contains(got, c.want) {
				t.Errorf("hint = %q, want it to contain %q", got, c.want)
			}
			if strings.Contains(got, c.mustNotSay) {
				t.Errorf("hint = %q — it still sends the user down the %q dead end", got, c.mustNotSay)
			}
		})
	}
}

// A missing ffmpeg still gets its own remedy: narrowing the needles must not
// cost the case they exist for.
func TestFailureHintStillCatchesAMissingFFmpeg(t *testing.T) {
	got := FailureHint("ERROR: Postprocessing: ffprobe and ffmpeg not found. Please install or provide the path using --ffmpeg-location")
	if !strings.Contains(got, "ytdl --update") {
		t.Errorf("a genuinely missing ffmpeg lost its hint: %q", got)
	}
}

// The break-glass remedy of ADR-0016 §13. A user whose downloads have started
// failing because their extractor is behind what YouTube now needs must not have
// to understand any of the pin machinery: the hint fires at the moment they are
// already stuck and names updating as the next step.
//
// It is deliberately NOT a flag, a config key or a GUI control — those would
// hand back the version decision that ADR-0016 §2 took away.
func TestOutdatedExtractorPointsAtTheUpdate(t *testing.T) {
	stranded := []string{
		"ERROR: unable to extract player response; please report this issue",
		"ERROR: [youtube] nsig extraction failed: Some formats may be missing",
		"ERROR: Signature extraction failed",
		"ERROR: unable to download video data: HTTP Error 403: Forbidden",
		"ERROR: The following content is not available on this app",
		"Please report this issue on https://github.com/yt-dlp/yt-dlp/issues",
	}
	for _, reason := range stranded {
		hint := FailureHint(reason)
		if hint == "" {
			t.Errorf("no hint for a stranded extractor: %q", reason)
			continue
		}
		if !strings.Contains(hint, "aggiorna ytdl") {
			t.Errorf("the hint for %q does not name updating: %q", reason, hint)
		}
	}
}

// The hint is rendered in BOTH channels from one string, so it has to be
// actionable in both: a GUI user has no Terminal, which is the whole reason this
// cycle exists (ux-principles.md §9.2).
func TestUpdateHintIsActionableInBothChannels(t *testing.T) {
	hint := FailureHint("ERROR: unable to extract player response")
	if !strings.Contains(hint, "impostazioni") {
		t.Errorf("the hint offers a GUI user no way to act: %q", hint)
	}
	if !strings.Contains(hint, "ytdl --update") {
		t.Errorf("the hint no longer gives the terminal user the exact command: %q", hint)
	}
}

// The one case with NO remedy stays without one. Naming a step ytdl cannot
// perform would be the same defect one layer up, and an update does not solve a
// bot check.
func TestABotCheckStillGetsNoUpdateHint(t *testing.T) {
	for _, reason := range []string{
		"ERROR: Sign in to confirm you're not a bot",
		"ERROR: This video is age-restricted",
	} {
		if hint := FailureHint(reason); hint != "" {
			t.Errorf("a case with no remedy was given one: %q → %q", reason, hint)
		}
	}
}
