package main

import (
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/cli"
	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what was
// written. Not parallel-safe (mutates the global os.Stdout).
func captureStdout(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	rc := fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return rc, string(out)
}

// The `queue` subcommand reads the same spool the enqueue path writes: an
// enqueued job shows up in `ytdl queue`, exercising StatePath -> queue.Open ->
// RenderQueue end to end.
func TestRealMainQueueListsEnqueued(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)

	sp := queue.Open(config.StatePath())
	if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/MAIN", EnqueuedAt: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}

	rc, out := captureStdout(t, func() int { return realMain([]string{"queue"}) })
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "youtu.be/MAIN") { // scheme trimmed by the one-row shortener
		t.Errorf("queue output missing the enqueued job:\n%s", out)
	}
}

// `queue --watch` must NOT enter its redraw loop when stdout is not a terminal
// (here a pipe, under captureStdout): it falls back to a single snapshot and
// returns. If the fallback regressed this test would hang (caught by the timeout).
func TestRealMainQueueWatchNonTTYFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)

	rc, out := captureStdout(t, func() int { return realMain([]string{"queue", "--watch"}) })
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "CODA ytdl") {
		t.Errorf("non-TTY --watch should print one snapshot:\n%s", out)
	}
	// No ANSI redraw/cursor sequences must leak into a piped stream.
	if strings.Contains(out, "\033[") {
		t.Errorf("non-TTY --watch leaked ANSI escapes:\n%q", out)
	}
}

// `status` reports the daemon as not running when none holds the lock.
func TestRealMainStatusNoDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)

	rc, out := captureStdout(t, func() int { return realMain([]string{"status"}) })
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "inattivo") {
		t.Errorf("status output should report the daemon as not running:\n%s", out)
	}
}

// `history` reads the durable log store — including a foreground download, which
// never touches the spool — and lists it title-first with the window label.
func TestRealMainHistoryLists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)

	logDir := config.Defaults().LogDir
	if err := logstore.Append(logDir, logstore.Job{
		URL: "https://youtu.be/H", Title: "Artist - Song", Mode: "default",
		Format: "mp3", Success: true, Time: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	rc, out := captureStdout(t, func() int { return realMain([]string{"history"}) })
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "STORICO ytdl") || !strings.Contains(out, "Artist - Song") {
		t.Errorf("history output missing header/title:\n%s", out)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what was
// written. Not parallel-safe (mutates the global os.Stderr).
func captureStderr(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	rc := fn()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return rc, string(out)
}

// D1: with $HOME unresolvable and no explicit absolute output dir, a run action
// must fail fast rather than download to a CWD-relative path.
func TestRealMainHomeUnsetFailsDownload(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YTDL_OUT_DIR", "")

	rc, stderr := captureStderr(t, func() int {
		return realMain([]string{"https://youtu.be/x"})
	})
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(stderr, "$HOME") {
		t.Errorf("stderr should name $HOME, got:\n%s", stderr)
	}
}

// Help and version stay permissive even without $HOME.
func TestRealMainHelpPermissiveWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	// Silence the usage text.
	oldOut := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	rc := realMain([]string{"-h"})
	w.Close()
	os.Stdout = oldOut
	if rc != 0 {
		t.Errorf("help without $HOME: rc = %d, want 0 (permissive)", rc)
	}
}

// An explicit absolute -o is honoured even with $HOME unset (the guard is
// skipped); it then proceeds past D1 (and fails later on the missing yt-dlp,
// which is a different, expected exit — we only assert D1 did not trip).
func TestRealMainAbsoluteOutputBypassesHomeGuard(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("PATH", t.TempDir()) // no yt-dlp/ffmpeg -> dep check will fail
	rc, stderr := captureStderr(t, func() int {
		return realMain([]string{"-o", "/tmp/ytdl-abs", "https://youtu.be/x"})
	})
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if strings.Contains(stderr, "$HOME") {
		t.Errorf("D1 guard tripped despite an absolute -o:\n%s", stderr)
	}
	if !strings.Contains(stderr, "non trovato") {
		t.Errorf("expected the missing-dependency path, got:\n%s", stderr)
	}
}

func TestIsIndex(t *testing.T) {
	for _, s := range []string{"1", "42", "999"} {
		if !isIndex(s) {
			t.Errorf("isIndex(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "1000", "1a", "100-aaa", "-1"} {
		if isIndex(s) {
			t.Errorf("isIndex(%q) = true, want false", s)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	es := []queue.Entry{{ID: "100-aaa"}, {ID: "200-bbb"}, {ID: "100-ccc"}}

	// A short integer is a 1-based index.
	if e, ok := resolveTarget("2", es); !ok || e.ID != "200-bbb" {
		t.Errorf("index 2 = %+v, %v", e, ok)
	}
	// Out-of-range index fails.
	if _, ok := resolveTarget("9", es); ok {
		t.Error("index 9 should not resolve")
	}
	// A long numeric-plus id is an id-prefix, not an index — and unique here.
	if e, ok := resolveTarget("200-bbb", es); !ok || e.ID != "200-bbb" {
		t.Errorf("id-prefix = %+v, %v", e, ok)
	}
	// An ambiguous id-prefix (matches two) does not resolve.
	if _, ok := resolveTarget("100-", es); ok {
		t.Error("ambiguous prefix 100- should not resolve")
	}
	// A unique id-prefix resolves.
	if e, ok := resolveTarget("100-a", es); !ok || e.ID != "100-aaa" {
		t.Errorf("unique prefix = %+v, %v", e, ok)
	}
}

func TestRunCancelCmdDeletesPending(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sp := queue.Open(config.StatePath())
	if _, err := sp.Enqueue(queue.Job{URL: "https://a", EnqueuedAt: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	id2, err := sp.Enqueue(queue.Job{URL: "https://b", EnqueuedAt: time.Unix(2, 0)})
	if err != nil {
		t.Fatal(err)
	}

	rc, out := captureStdout(t, func() int {
		return runCancelCmd(&cli.Parsed{Action: cli.ActionCancel, Target: "1"})
	})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "Annullato") {
		t.Errorf("cancel output missing confirmation:\n%s", out)
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 1 || snap.Pending[0].ID != id2 {
		t.Fatalf("only the second job should remain pending: %+v", snap.Pending)
	}
}

func TestRunRetryCmdRequeues(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sp := queue.Open(config.StatePath())
	id, err := sp.Enqueue(queue.Job{URL: "https://a", EnqueuedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.ClaimNext(); err != nil {
		t.Fatal(err)
	}
	if err := sp.MarkFailed(id); err != nil {
		t.Fatal(err)
	}

	// Hold the daemon lock so resumeIfStalled sees a live daemon and does NOT
	// self-exec a real one from the test binary.
	lf, err := os.OpenFile(sp.LockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	rc, out := captureStdout(t, func() int {
		return runRetryCmd(&cli.Parsed{Action: cli.ActionRetry, Target: "1"})
	})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "Rimesso in coda") {
		t.Errorf("retry output missing confirmation:\n%s", out)
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 1 || len(snap.Failed) != 0 {
		t.Fatalf("job should be re-queued: pending=%d failed=%d", len(snap.Pending), len(snap.Failed))
	}
}
