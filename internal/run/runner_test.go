package run

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/notify"
)

// fakeYtDlp writes a fake yt-dlp (and ffmpeg) onto PATH. It distinguishes the
// runner's --print-to-file sinks by their template (filepath vs id, plain title
// vs id-tagged), emitting: a single title (YTDLP_FAKE_TITLE), YTDLP_FAKE_NLINES
// saved filepaths, and — for a playlist — attempted "id\ttitle" lines
// (YTDLP_FAKE_ATTEMPTED="id:Title,...") and succeeded ids
// (YTDLP_FAKE_SUCCEEDED="id,..."). It exits YTDLP_FAKE_RC.
func fakeYtDlp(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/usr/bin/env bash
titlefile=""; savedfile=""; attfile=""; sucfile=""
args=("$@"); i=0
while [ $i -lt ${#args[@]} ]; do
  if [ "${args[$i]}" = "--print-to-file" ]; then
    k="${args[$((i+1))]}"; p="${args[$((i+2))]}"
    case "$k" in
      after_move:*filepath*) savedfile="$p" ;;
      after_move:*)          sucfile="$p" ;;
      before_dl:*id*)        attfile="$p" ;;
      before_dl:*)           titlefile="$p" ;;
    esac
    i=$((i+3)); continue
  fi
  i=$((i+1))
done
[ -n "$titlefile" ] && printf '%s\n' "${YTDLP_FAKE_TITLE:-Artist - Track}" > "$titlefile"
n="${YTDLP_FAKE_NLINES:-0}"
if [ -n "$savedfile" ] && [ "$n" -gt 0 ]; then
  for j in $(seq 1 "$n"); do printf '%s/track%d.mp3\n' "${YTDLP_FAKE_DIR:-/out}" "$j" >> "$savedfile"; done
fi
if [ -n "$attfile" ] && [ -n "$YTDLP_FAKE_ATTEMPTED" ]; then
  IFS=',' read -ra items <<< "$YTDLP_FAKE_ATTEMPTED"
  for it in "${items[@]}"; do printf '%s\t%s\n' "${it%%:*}" "${it#*:}" >> "$attfile"; done
fi
if [ -n "$sucfile" ] && [ -n "$YTDLP_FAKE_SUCCEEDED" ]; then
  IFS=',' read -ra sids <<< "$YTDLP_FAKE_SUCCEEDED"
  for id in "${sids[@]}"; do printf '%s\n' "$id" >> "$sucfile"; done
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
	// Point the central log store at a writable temp dir (the default lives under
	// the pinned, non-writable $HOME) so Record/Prune are exercised for real.
	s.LogDir = filepath.Join(t.TempDir(), "logs")
	return core.Options{Mode: mode, URL: "https://youtu.be/x", Settings: s}
}

// storeLogs returns the central-store *.log files written under o's LogDir.
func storeLogs(t *testing.T, o core.Options) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(o.Settings.LogDir, "*.log"))
	return m
}

// fakeNotifier captures notifications instead of showing a banner.
type fakeNotifier struct{ calls []notify.Notification }

func (f *fakeNotifier) Notify(n notify.Notification) { f.calls = append(f.calls, n) }

// withNotifier swaps the package notifier for the duration of a test.
func withNotifier(t *testing.T, f notify.Notifier) {
	t.Helper()
	old := notifier
	notifier = f
	t.Cleanup(func() { notifier = old })
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

func TestDispatchSilentBreadcrumb(t *testing.T) {
	fakeYtDlp(t)

	t.Run("success writes no breadcrumb, records ok centrally", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "1")
		t.Setenv("YTDLP_FAKE_RC", "0")
		o := runOptions(t, core.ModeSilent, out)
		rc := Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
		if logs := findLogs(t, out); len(logs) != 0 {
			t.Errorf("unexpected breadcrumb on success: %v", logs)
		}
		store := storeLogs(t, o)
		if len(store) != 1 || !strings.HasSuffix(store[0], "-ok.log") {
			t.Errorf("want one central -ok record, got %v", store)
		}
	})

	t.Run("failure writes a title+hash breadcrumb and a central fail record", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "1")
		t.Setenv("YTDLP_FAKE_TITLE", "Artist - Track")
		o := runOptions(t, core.ModeSilent, out)
		rc := Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if rc != 1 {
			t.Errorf("rc = %d, want 1", rc)
		}
		key := logstore.Hash(logstore.NormalizeURL(o.URL))
		want := filepath.Join(out, "Artist - Track — FALLITO."+key+".log")
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected breadcrumb %s: %v", want, err)
		}
		store := storeLogs(t, o)
		if len(store) != 1 || !strings.HasSuffix(store[0], "-fail.log") {
			t.Fatalf("want one central -fail record, got %v", store)
		}
		if data, _ := os.ReadFile(store[0]); !strings.Contains(string(data), "rc:     1") {
			t.Errorf("central record missing rc:\n%s", data)
		}
	})

	t.Run("empty saved on rc 0 is a failure (breadcrumb written)", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "0")
		o := runOptions(t, core.ModeSilent, out)
		rc := Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
		if logs := findLogs(t, out); len(logs) != 1 {
			t.Errorf("want exactly one breadcrumb for an empty result, got %v", logs)
		}
	})

	t.Run("breadcrumb_on_failure=false suppresses the breadcrumb", func(t *testing.T) {
		out := t.TempDir()
		t.Setenv("YTDLP_FAKE_DIR", out)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "1")
		o := runOptions(t, core.ModeSilent, out)
		o.Settings.BreadcrumbOnFailure = false
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if logs := findLogs(t, out); len(logs) != 0 {
			t.Errorf("breadcrumb written despite breadcrumb_on_failure=false: %v", logs)
		}
		if store := storeLogs(t, o); len(store) != 1 {
			t.Errorf("central store still records regardless: got %v", store)
		}
	})
}

// A failure then a later success for the same URL removes the breadcrumb (U7).
func TestBreadcrumbAutoCleanup(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)

	// First run fails → breadcrumb appears.
	t.Setenv("YTDLP_FAKE_NLINES", "0")
	t.Setenv("YTDLP_FAKE_RC", "1")
	o := runOptions(t, core.ModeSilent, out)
	Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
	if logs := findLogs(t, out); len(logs) != 1 {
		t.Fatalf("want a breadcrumb after failure, got %v", logs)
	}
	// Second run for the same URL succeeds → breadcrumb removed.
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_RC", "0")
	Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
	if logs := findLogs(t, out); len(logs) != 0 {
		t.Errorf("a later success must remove the stale breadcrumb, still: %v", logs)
	}
}

// A partly-failed playlist leaves exactly one breadcrumb per failed item.
func TestPlaylistPerItemBreadcrumb(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)
	t.Setenv("YTDLP_FAKE_NLINES", "2") // two of three items produced a file
	t.Setenv("YTDLP_FAKE_RC", "0")     // -i playlist: overall rc 0 despite an item failure
	t.Setenv("YTDLP_FAKE_ATTEMPTED", "id1:One,id2:Two,id3:Three")
	t.Setenv("YTDLP_FAKE_SUCCEEDED", "id1,id3") // id2 failed

	o := runOptions(t, core.ModeSilent, out)
	o.Playlist = true
	Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})

	logs := findLogs(t, out)
	if len(logs) != 1 {
		t.Fatalf("want one per-item breadcrumb (id2), got %v", logs)
	}
	want := "Two — FALLITO." + logstore.Hash("id2") + ".log"
	if filepath.Base(logs[0]) != want {
		t.Errorf("breadcrumb = %s, want %s", filepath.Base(logs[0]), want)
	}
}

func findLogs(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNotifyGating(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)

	// Silent success with notify_on=both fires one success banner.
	t.Run("silent success both", func(t *testing.T) {
		fn := &fakeNotifier{}
		withNotifier(t, fn)
		t.Setenv("YTDLP_FAKE_NLINES", "1")
		t.Setenv("YTDLP_FAKE_RC", "0")
		Dispatch(runOptions(t, core.ModeSilent, out), &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 1 || !fn.calls[0].Success {
			t.Fatalf("want one success notification, got %+v", fn.calls)
		}
	})

	// notify_on=failure suppresses a success notification.
	t.Run("notify_on=failure skips success", func(t *testing.T) {
		fn := &fakeNotifier{}
		withNotifier(t, fn)
		t.Setenv("YTDLP_FAKE_NLINES", "1")
		t.Setenv("YTDLP_FAKE_RC", "0")
		o := runOptions(t, core.ModeSilent, out)
		o.Settings.NotifyOn = "failure"
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 0 {
			t.Errorf("success should not notify with notify_on=failure: %+v", fn.calls)
		}
	})

	// notify_on=failure DOES fire on a real failure.
	t.Run("notify_on=failure fires on failure", func(t *testing.T) {
		fn := &fakeNotifier{}
		withNotifier(t, fn)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "1")
		o := runOptions(t, core.ModeSilent, out)
		o.Settings.NotifyOn = "failure"
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 1 || fn.calls[0].Success {
			t.Fatalf("want one failure notification, got %+v", fn.calls)
		}
	})

	// notify_on=success suppresses a failure notification (the mirror gate).
	t.Run("notify_on=success skips failure", func(t *testing.T) {
		fn := &fakeNotifier{}
		withNotifier(t, fn)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "1")
		o := runOptions(t, core.ModeSilent, out)
		o.Settings.NotifyOn = "success"
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 0 {
			t.Errorf("failure should not notify with notify_on=success: %+v", fn.calls)
		}
	})

	// Verbose (foreground) notifies when notify_foreground is on.
	t.Run("verbose foreground notifies when enabled", func(t *testing.T) {
		fn := &fakeNotifier{}
		withNotifier(t, fn)
		t.Setenv("YTDLP_FAKE_RC", "0")
		o := runOptions(t, core.ModeVerbose, out)
		o.Settings.NotifyForeground = true
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 1 || !fn.calls[0].Success {
			t.Errorf("verbose with notify_foreground should fire one success banner: %+v", fn.calls)
		}
	})

	// Foreground default mode does not notify unless notify_foreground is on.
	t.Run("foreground gated by notify_foreground", func(t *testing.T) {
		t.Setenv("YTDLP_FAKE_NLINES", "1")
		t.Setenv("YTDLP_FAKE_RC", "0")

		fn := &fakeNotifier{}
		withNotifier(t, fn)
		Dispatch(runOptions(t, core.ModeDefault, out), &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 0 {
			t.Errorf("default (foreground) must not notify by default: %+v", fn.calls)
		}

		fn2 := &fakeNotifier{}
		withNotifier(t, fn2)
		o := runOptions(t, core.ModeDefault, out)
		o.Settings.NotifyForeground = true
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn2.calls) != 1 {
			t.Errorf("default with notify_foreground=true should notify once: %+v", fn2.calls)
		}
	})

	// notify=false is a hard master switch.
	t.Run("notify=false silences everything", func(t *testing.T) {
		fn := &fakeNotifier{}
		withNotifier(t, fn)
		t.Setenv("YTDLP_FAKE_NLINES", "0")
		t.Setenv("YTDLP_FAKE_RC", "1")
		o := runOptions(t, core.ModeSilent, out)
		o.Settings.Notify = false
		Dispatch(o, &bytes.Buffer{}, &bytes.Buffer{})
		if len(fn.calls) != 0 {
			t.Errorf("notify=false must silence notifications: %+v", fn.calls)
		}
	})
}

func TestRunCode(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	cases := []struct {
		name string
		cmd  *exec.Cmd
		want int
	}{
		{"success", exec.Command("bash", "-c", "exit 0"), 0},
		{"normal failure propagates code", exec.Command("bash", "-c", "exit 3"), 3},
		// A signal-killed child must surface 128+signal (SIGKILL=9 → 137), the
		// shell's convention, not collapse to a generic 1.
		{"SIGKILL becomes 137", exec.Command("bash", "-c", "kill -9 $$"), 137},
		{"SIGTERM becomes 143", exec.Command("bash", "-c", "kill -TERM $$"), 143},
		{"cannot start is 1", exec.Command(filepath.Join(t.TempDir(), "nope")), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runCode(c.cmd); got != c.want {
				t.Errorf("runCode = %d, want %d", got, c.want)
			}
		})
	}
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
