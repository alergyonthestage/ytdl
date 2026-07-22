package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
)

// fakeYtDlp writes a fake yt-dlp (and ffmpeg) onto PATH. The fake finds the
// --print-to-file after_move/before_dl targets, writes YTDLP_FAKE_NLINES fake
// paths and a title, and exits with YTDLP_FAKE_RC — enough to exercise the
// runner's temp-file, reporting and .log behaviour without a real yt-dlp.
func fakeYtDlp(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/usr/bin/env bash
saved=""; title=""
args=("$@"); i=0
while [ $i -lt ${#args[@]} ]; do
  if [ "${args[$i]}" = "--print-to-file" ]; then
    k="${args[$((i+1))]}"; p="${args[$((i+2))]}"
    case "$k" in
      after_move:*) saved="$p" ;;
      before_dl:*)  title="$p" ;;
    esac
    i=$((i+3)); continue
  fi
  i=$((i+1))
done
[ -n "$title" ] && printf '%s\n' "${YTDLP_FAKE_TITLE:-Artist - Track}" > "$title"
n="${YTDLP_FAKE_NLINES:-0}"
if [ -n "$saved" ] && [ "$n" -gt 0 ]; then
  for j in $(seq 1 "$n"); do printf '%s/track%d.mp3\n' "${YTDLP_FAKE_DIR:-/out}" "$j" >> "$saved"; done
fi
exit "${YTDLP_FAKE_RC:-0}"
`
	for _, name := range []string{"yt-dlp", "ffmpeg"} {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runOptions(t *testing.T, mode core.Mode, outDir string) core.Options {
	t.Helper()
	t.Setenv("HOME", "/pinned")
	s := config.Defaults()
	s.OutputDir = outDir
	return core.Options{Mode: mode, URL: "https://youtu.be/x", Settings: s}
}

func TestDispatchDefaultReporting(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)

	t.Run("one file", func(t *testing.T) {
		t.Setenv("YTDLP_FAKE_NLINES", "1")
		t.Setenv("YTDLP_FAKE_RC", "0")
		var buf bytes.Buffer
		rc := Dispatch(runOptions(t, core.ModeDefault, out), &buf, &buf)
		if rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
		if !strings.Contains(buf.String(), "✓ Salvata in: "+out+"/track1.mp3") {
			t.Errorf("output missing single-file confirmation:\n%s", buf.String())
		}
	})

	t.Run("many files", func(t *testing.T) {
		t.Setenv("YTDLP_FAKE_NLINES", "3")
		t.Setenv("YTDLP_FAKE_RC", "0")
		var buf bytes.Buffer
		rc := Dispatch(runOptions(t, core.ModeDefault, out), &buf, &buf)
		if rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
		if !strings.Contains(buf.String(), "✓ 3 tracce salvate in: "+out) {
			t.Errorf("output missing multi-file confirmation:\n%s", buf.String())
		}
	})

	t.Run("no files propagates rc", func(t *testing.T) {
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "3")
		var buf bytes.Buffer
		rc := Dispatch(runOptions(t, core.ModeDefault, out), &buf, &buf)
		if rc != 3 {
			t.Errorf("rc = %d, want 3 (propagated)", rc)
		}
		if !strings.Contains(buf.String(), "✗ Nessun file scaricato (rc=3).") {
			t.Errorf("output missing zero-file report:\n%s", buf.String())
		}
	})
}

func TestDispatchSilentLog(t *testing.T) {
	fakeYtDlp(t)

	t.Run("success writes no log", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "1")
		t.Setenv("YTDLP_FAKE_RC", "0")
		rc := Dispatch(runOptions(t, core.ModeSilent, out), &bytes.Buffer{}, &bytes.Buffer{})
		if rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
		if logs := findLogs(t, out); len(logs) != 0 {
			t.Errorf("unexpected .log files on success: %v", logs)
		}
	})

	t.Run("failure writes a titled log", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "1")
		t.Setenv("YTDLP_FAKE_TITLE", "Artist - Track")
		rc := Dispatch(runOptions(t, core.ModeSilent, out), &bytes.Buffer{}, &bytes.Buffer{})
		if rc != 1 {
			t.Errorf("rc = %d, want 1", rc)
		}
		want := filepath.Join(out, "Artist - Track.log")
		data, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("expected log %s: %v", want, err)
		}
		if !strings.Contains(string(data), "ytdl — download FALLITO") {
			t.Errorf("log missing header:\n%s", data)
		}
		if !strings.Contains(string(data), "rc:   1") {
			t.Errorf("log missing rc line:\n%s", data)
		}
	})

	t.Run("empty saved on rc 0 still logs", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "0")
		rc := Dispatch(runOptions(t, core.ModeSilent, out), &bytes.Buffer{}, &bytes.Buffer{})
		if rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
		if logs := findLogs(t, out); len(logs) != 1 {
			t.Errorf("want exactly one .log for an empty result, got %v", logs)
		}
	})
}

func findLogs(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheckDeps(t *testing.T) {
	fakeYtDlp(t)
	if err := CheckDeps(); err != nil {
		t.Errorf("CheckDeps with both tools present: %v", err)
	}
	// Point PATH at an empty dir: both tools vanish.
	t.Setenv("PATH", t.TempDir())
	err := CheckDeps()
	if err == nil {
		t.Fatal("expected a DepError with no tools on PATH")
	}
	var de *DepError
	if !asDepError(err, &de) || de.Tool != "yt-dlp" {
		t.Errorf("want DepError for yt-dlp, got %v", err)
	}
}

func asDepError(err error, target **DepError) bool {
	de, ok := err.(*DepError)
	if ok {
		*target = de
	}
	return ok
}

func TestReadSavedPaths(t *testing.T) {
	f := filepath.Join(t.TempDir(), "saved")
	os.WriteFile(f, []byte("/a/one.mp3\n\n/a/two.mp3\n\n"), 0o644)
	first, count := readSavedPaths(f)
	if first != "/a/one.mp3" || count != 2 {
		t.Errorf("first=%q count=%d, want /a/one.mp3, 2 (empty lines skipped)", first, count)
	}
	if _, c := readSavedPaths(filepath.Join(t.TempDir(), "nope")); c != 0 {
		t.Errorf("missing file should give count 0")
	}
}

func TestSanitizeLogName(t *testing.T) {
	cases := map[string]string{
		"Artist - Track":   "Artist - Track",
		"A/B:C":            "A_B_C",          // parity tr '/:' '__'
		"a\\b*c?d\"e<f>g|": "a_b_c_d_e_f_g_", // C5 hostile chars
		"  spaced  ":       "spaced",         // trimmed
		"...dots":          "dots",           // no leading dots
	}
	for in, want := range cases {
		if got := sanitizeLogName(in); got != want {
			t.Errorf("sanitizeLogName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLastLine(t *testing.T) {
	// Matches `tail -n1`: one trailing newline stripped; a trailing BLANK line
	// yields "" (so silent mode falls back to the timestamped .log name).
	cases := map[string]string{
		"first\nsecond\nlast\n": "last", // normal: trailing newline stripped
		"Artist - Track\n\n":    "",     // trailing blank line -> empty (Bash tail -n1)
		"a\nb":                  "b",    // no trailing newline
		"only\n":                "only", // single line
		"":                      "",     // empty file
	}
	for content, want := range cases {
		f := filepath.Join(t.TempDir(), "titles")
		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := lastLine(f); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", content, got, want)
		}
	}
	if got := lastLine(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("missing file lastLine = %q, want empty", got)
	}
}

func TestPrependLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin")
	PrependLocalBin()
	want := filepath.Join(home, ".local", "bin")
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, want+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want %s prepended", got, want)
	}
	// Idempotent: a second call must not double-prepend.
	before := os.Getenv("PATH")
	PrependLocalBin()
	if os.Getenv("PATH") != before {
		t.Errorf("PrependLocalBin not idempotent:\n before %q\n after  %q", before, os.Getenv("PATH"))
	}
}

func TestMissingDepMessage(t *testing.T) {
	msg := MissingDepMessage("yt-dlp")
	if !strings.Contains(msg, "✗ yt-dlp non trovato.") || !strings.Contains(msg, "ytdl --update") {
		t.Errorf("unexpected message:\n%s", msg)
	}
}
