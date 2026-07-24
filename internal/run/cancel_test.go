package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/core"
)

// A cancelled queued download must tear down the WHOLE process group (yt-dlp AND
// its ffmpeg child), return promptly, and leave no .log breadcrumb (ADR-0011).
// The fake yt-dlp forks a child that traps SIGTERM and writes a marker: the
// marker appears only if the group signal reached the child, i.e. only if the
// process-group kill worked (a leader-only kill would leave the child sleeping).
func TestRunQueuedCancelKillsProcessGroup(t *testing.T) {
	bin := t.TempDir()
	sig := t.TempDir()
	started := filepath.Join(sig, "started")
	mark := filepath.Join(sig, "child-signalled")
	script := "#!/usr/bin/env bash\n" +
		"( trap 'printf killed > \"" + mark + "\"; exit 0' TERM; sleep 300 ) &\n" +
		"printf started > \"" + started + "\"\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dest := t.TempDir()
	o := runOptions(t, core.ModeSilent, dest)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- RunQueued(ctx, o, nil) }()

	waitFor(t, started, 3*time.Second, "fake yt-dlp never started")
	cancel()

	select {
	case rc := <-done:
		if rc == 0 {
			t.Errorf("cancelled run returned rc 0, want a signal death")
		}
	case <-time.After(killGrace + 3*time.Second):
		t.Fatal("RunQueued did not return after cancel")
	}

	// The marker proves SIGTERM reached the CHILD via the process group.
	waitFor(t, mark, killGrace+2*time.Second, "child process was not killed with the group")

	// A cancel drops NO breadcrumb .log in the destination.
	if m, _ := filepath.Glob(filepath.Join(dest, "*.log")); len(m) != 0 {
		t.Errorf("cancelled run left a breadcrumb: %v", m)
	}
}

// waitFor polls until path exists or the timeout elapses.
func waitFor(t *testing.T, path string, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
