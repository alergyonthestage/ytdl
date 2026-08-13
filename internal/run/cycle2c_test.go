package run

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/logstore"
)

// writeFakeYtDlp puts a minimal yt-dlp/ffmpeg (the given script) on PATH — used
// by the dry-run tests, which need control over stderr and the exit code rather
// than the richer print-to-file fake.
func writeFakeYtDlp(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"yt-dlp", "ffmpeg"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	useShimDir(t, bin)
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what was
// written. Not parallel-safe (mutates the global os.Stderr).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestDryRunSuccessPreservesStderr(t *testing.T) {
	writeFakeYtDlp(t, "#!/usr/bin/env bash\necho 'una nota' 1>&2\nexit 0\n")
	o := runOptions(t, core.ModeDryRun, t.TempDir())
	var buf bytes.Buffer
	got := captureStderr(t, func() { Dispatch(o, &buf, &buf) })
	if !strings.Contains(got, "una nota") {
		t.Errorf("a successful preview must not swallow yt-dlp stderr; got %q", got)
	}
}

func TestDryRunFailureMediated(t *testing.T) {
	writeFakeYtDlp(t, "#!/usr/bin/env bash\nprintf 'ERROR: Sign in to confirm you are not a bot\\n' 1>&2\nexit 1\n")
	o := runOptions(t, core.ModeDryRun, t.TempDir())
	var buf bytes.Buffer
	got := captureStderr(t, func() { Dispatch(o, &buf, &buf) })
	if !strings.Contains(got, "verifica anti-bot") {
		t.Errorf("a bot-check preview failure must be mediated; got %q", got)
	}
	if strings.Contains(got, "ERROR: Sign in") {
		t.Errorf("the raw yt-dlp error must be hidden on failure; got %q", got)
	}
}

func TestDryRunErrorMessage(t *testing.T) {
	// The YouTube anti-bot challenge gets a specific, reassuring message.
	botRaw := []byte("ERROR: [youtube] X: Sign in to confirm you’re not a bot. Use --cookies ...")
	msg := dryRunErrorMessage(botRaw)
	if !strings.Contains(msg, "verifica anti-bot") || !strings.Contains(msg, "riprova senza -n") {
		t.Errorf("bot-check message not mediated:\n%s", msg)
	}
	if strings.Contains(msg, "Use --cookies") {
		t.Error("bot-check message leaked the raw yt-dlp line")
	}

	// A generic failure shows its last stderr line, not the whole dump.
	generic := []byte("WARNING: something\nERROR: video unavailable\n")
	msg = dryRunErrorMessage(generic)
	if !strings.Contains(msg, "video unavailable") {
		t.Errorf("generic message should carry the last line:\n%s", msg)
	}
	if strings.Contains(msg, "WARNING: something") {
		t.Error("generic message should not include earlier lines")
	}

	// Empty stderr still yields a message.
	if got := dryRunErrorMessage(nil); !strings.Contains(got, "Anteprima non riuscita") {
		t.Errorf("empty stderr message = %q", got)
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	cases := map[string]string{
		"a\nb\nc\n":    "c",
		"a\nb\n\n\n":   "b",
		"  padded  \n": "padded",
		"":             "",
		"\n\n":         "",
		"only":         "only",
	}
	for in, want := range cases {
		if got := lastNonEmptyLine(in); got != want {
			t.Errorf("lastNonEmptyLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleFromPath(t *testing.T) {
	cases := map[string]string{
		"/music/Artist - Track.mp3": "Artist - Track",
		"track1.mp3":                "track1",
		"/a/b/No Extension":         "No Extension",
		"":                          "",
	}
	for in, want := range cases {
		if got := titleFromPath(in); got != want {
			t.Errorf("titleFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDefaultDownloadAppendsHistory checks that a foreground download — which
// never touches the spool — still lands in the durable history stream, with the
// saved file's name as the title. This is the fix for "I downloaded more but the
// count said 2": foreground jobs are now visible.
func TestDefaultDownloadAppendsHistory(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_RC", "0")

	o := runOptions(t, core.ModeDefault, out)
	var buf bytes.Buffer
	if rc := Dispatch(o, &buf, &buf); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	entries, err := logstore.Load(o.Settings.LogDir, logstore.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("history has %d entries, want 1", len(entries))
	}
	e := entries[0]
	if !e.Success || e.Mode != "default" || e.Title != "track1" || e.URL != o.URL {
		t.Errorf("history entry = %+v, want success default title=track1 url=%s", e, o.URL)
	}
}

// TestSilentHistoryTitleFromBeforeDL guards the review fix: silent/background
// mode must take the history title from the dedicated before_dl temp file (the
// "Artist - Track" contract), not the saved filename (which depends on the user's
// name_template). The fake yt-dlp writes YTDLP_FAKE_TITLE to the before_dl sink
// and "track1.mp3" as the saved file — the title must be the former.
func TestSilentHistoryTitleFromBeforeDL(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_RC", "0")
	t.Setenv("YTDLP_FAKE_TITLE", "Custom Artist - Custom Track")

	o := runOptions(t, core.ModeSilent, out)
	if rc := Dispatch(o, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	entries, err := logstore.Load(o.Settings.LogDir, logstore.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("history has %d entries, want 1", len(entries))
	}
	if entries[0].Title != "Custom Artist - Custom Track" {
		t.Errorf("silent title = %q, want the before_dl title (not the filename)", entries[0].Title)
	}
}
