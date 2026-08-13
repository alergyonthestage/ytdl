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
	useShimDir(t, bin)

	dest := t.TempDir()
	o := runOptions(t, core.ModeSilent, dest)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- RunQueued(ctx, o, nil, nil) }()

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

// A job that finishes cleanly (writes its file, exits 0) WITHIN the SIGTERM grace
// after a cancel must be recorded as SUCCESS, not a false failure. runCode reads
// the real exit status from ProcessState, not the ctx-decorated Run() error, so a
// graceful finish inside the grace window is rc 0 with no breadcrumb.
func TestRunQueuedGracefulFinishWithinCancelIsSuccess(t *testing.T) {
	bin := t.TempDir()
	// Fake yt-dlp: on SIGTERM, write the after_move sink (so a file "saved") and
	// exit 0 — modelling ffmpeg finishing postprocessing during the grace window.
	script := `#!/usr/bin/env bash
saved=""
args=("$@"); i=0
while [ $i -lt ${#args[@]} ]; do
  if [ "${args[$i]}" = "--print-to-file" ] && [[ "${args[$((i+1))]}" == after_move:*filepath* ]]; then
    saved="${args[$((i+2))]}"; i=$((i+3)); continue
  fi
  i=$((i+1))
done
finish() { [ -n "$saved" ] && printf '%s\n' "/out/track.mp3" > "$saved"; exit 0; }
trap finish TERM
sleep 300 &
wait
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	useShimDir(t, bin)

	dest := t.TempDir()
	o := runOptions(t, core.ModeSilent, dest)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()

	if rc := RunQueued(ctx, o, nil, nil); rc != 0 {
		t.Errorf("a graceful finish within the grace window: rc = %d, want 0 (success)", rc)
	}
	// Success ⇒ no failure breadcrumb in the destination.
	if m, _ := filepath.Glob(filepath.Join(dest, "*.log")); len(m) != 0 {
		t.Errorf("a successful (raced) job left a breadcrumb: %v", m)
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
