package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

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
