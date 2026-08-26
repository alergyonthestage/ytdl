package main

import (
	"io"
	"os"
	"path/filepath"
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

	// The spool holds pending work and no daemon is live, so `queue` resumes one.
	// Stubbed: unstubbed this launched a REAL detached daemon from the test binary
	// — which under `go test` is the suite itself. Asserting the request is also
	// stronger than the silence it replaces, which checked nothing either way.
	resumed := 0
	old := spawnQueueDaemon
	spawnQueueDaemon = func() error { resumed++; return nil }
	t.Cleanup(func() { spawnQueueDaemon = old })

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
	if resumed != 1 {
		t.Errorf("daemon resumes = %d, want 1: pending work with no daemon live must resume one", resumed)
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
	if e, res := resolveTarget("2", es); res != targetOK || e.ID != "200-bbb" {
		t.Errorf("index 2 = %+v, %v", e, res)
	}
	// Out-of-range index is not found.
	if _, res := resolveTarget("9", es); res != targetNotFound {
		t.Errorf("index 9 should be not-found, got %v", res)
	}
	// A long numeric-plus id is an id-prefix, not an index — and unique here.
	if e, res := resolveTarget("200-bbb", es); res != targetOK || e.ID != "200-bbb" {
		t.Errorf("id-prefix = %+v, %v", e, res)
	}
	// An ambiguous id-prefix (matches two) is distinguished from not-found.
	if _, res := resolveTarget("100-", es); res != targetAmbiguous {
		t.Errorf("prefix 100- should be ambiguous, got %v", res)
	}
	// A truly-absent prefix is not-found.
	if _, res := resolveTarget("zzz", es); res != targetNotFound {
		t.Errorf("prefix zzz should be not-found, got %v", res)
	}
	// A unique id-prefix resolves.
	if e, res := resolveTarget("100-a", es); res != targetOK || e.ID != "100-aaa" {
		t.Errorf("unique prefix = %+v, %v", e, res)
	}
}

func TestRunCancelCmdAll(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sp := queue.Open(config.StatePath())
	for i, u := range []string{"https://a", "https://b"} {
		if _, err := sp.Enqueue(queue.Job{URL: u, EnqueuedAt: time.Unix(int64(i+1), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	rc, out := captureStdout(t, func() int {
		return runCancelCmd(&cli.Parsed{Action: cli.ActionCancel, All: true})
	})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "2 annullati") {
		t.Errorf("--all summary missing:\n%s", out)
	}
	if snap, _ := sp.List(); len(snap.Pending) != 0 {
		t.Errorf("all pending should be cancelled, got %d", len(snap.Pending))
	}
}

// Cancelling a RUNNING job leaves a marker for the daemon's watcher (it can't be
// deleted from under a live daemon) and reports the async "richiesto" wording.
func TestRunCancelCmdRunningLeavesMarker(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sp := queue.Open(config.StatePath())
	id, err := sp.Enqueue(queue.Job{URL: "https://a", EnqueuedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.ClaimNext(); err != nil { // now running
		t.Fatal(err)
	}
	rc, out := captureStdout(t, func() int {
		return runCancelCmd(&cli.Parsed{Action: cli.ActionCancel, Target: "1"})
	})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "Annullamento richiesto") {
		t.Errorf("running cancel should read as async-requested:\n%s", out)
	}
	if ids, _ := sp.CancelRequests(); len(ids) != 1 || ids[0] != id {
		t.Errorf("running cancel did not leave a marker for the daemon: %v", ids)
	}
}

// A retry that fails to re-queue (TOCTOU / filesystem error) must exit non-zero,
// so a script's `ytdl retry ... || alert` actually fires.
func TestRunRetryCmdReportsFailureExitCode(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions; cannot force a rename failure")
	}
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
	// Make pending/ read-only so Retry's rename INTO it fails — same failure the
	// caller must surface as the TOCTOU ErrNotExist, easier to force deterministically.
	pendDir := filepath.Join(config.StatePath(), "queue", "pending")
	if err := os.Chmod(pendDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pendDir, 0o755) })

	rc, _ := captureStdout(t, func() int {
		return runRetryCmd(&cli.Parsed{Action: cli.ActionRetry, Target: "1"})
	})
	if rc != 1 {
		t.Errorf("a failed retry should exit 1, got %d", rc)
	}
	if snap, _ := sp.List(); len(snap.Failed) != 1 {
		t.Errorf("the failed job must remain (not lost): failed=%d", len(snap.Failed))
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

	// Hold the daemon lock so resumeIfStalled takes its "a daemon is already
	// live" branch and asks for no spawn at all. This once also stood in for the
	// missing seam — resumeIfStalled called daemon.Spawn directly, and holding the
	// lock was the only way to stop a test from self-exec'ing the suite. It now
	// exercises the branch and nothing more; TestMain's guard covers the rest.
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

// enrichTitlesFromHistory must look up each failed job in ITS OWN snapshotted
// Settings.LogDir (ADR-0007), not a single global dir — otherwise a job whose title
// was recorded in a different log dir is missed. It also preserves an existing
// title and skips playlists.
func TestEnrichTitlesFromHistoryPerJobLogDir(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	base := time.Now()
	if err := logstore.Append(dirA, logstore.Job{URL: "https://youtu.be/A", Title: "Title A", Time: base}); err != nil {
		t.Fatal(err)
	}
	if err := logstore.Append(dirB, logstore.Job{URL: "https://youtu.be/B", Title: "Title B", Time: base}); err != nil {
		t.Fatal(err)
	}
	entries := []queue.Entry{
		{ID: "a", State: queue.Failed, Job: queue.Job{URL: "https://youtu.be/A", Settings: config.Settings{LogDir: dirA}}},
		{ID: "b", State: queue.Failed, Job: queue.Job{URL: "https://youtu.be/B", Settings: config.Settings{LogDir: dirB}}},
		{ID: "c", State: queue.Failed, Job: queue.Job{URL: "https://youtu.be/A", Title: "Already", Settings: config.Settings{LogDir: dirA}}},
		{ID: "d", State: queue.Failed, Job: queue.Job{URL: "https://youtu.be/L", Playlist: true, Settings: config.Settings{LogDir: dirA}}},
	}
	enrichTitlesFromHistory(entries)
	for i, want := range []string{"Title A", "Title B", "Already", ""} {
		if got := entries[i].Job.Title; got != want {
			t.Errorf("entry[%d].Title = %q, want %q", i, got, want)
		}
	}
}
