package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(out, "https://youtu.be/MAIN") {
		t.Errorf("queue output missing the enqueued job:\n%s", out)
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
