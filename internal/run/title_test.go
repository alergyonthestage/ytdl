package run

import (
	"context"
	"sync"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/core"
)

// RunQueued reports the resolved before_dl title through onTitle, so the daemon
// can name the running/failed job in the queue/retry views (Cycle 4). A fast job
// finishes before the 300ms watcher ticks, so this also exercises the final flush
// that guarantees the title is reported at least once.
func TestRunQueuedReportsTitle(t *testing.T) {
	fakeYtDlp(t)
	t.Setenv("YTDLP_FAKE_TITLE", "Rick Astley - Never Gonna Give You Up")
	t.Setenv("YTDLP_FAKE_NLINES", "1") // a saved file → success
	o := runOptions(t, core.ModeSilent, t.TempDir())

	var mu sync.Mutex
	var got []string
	onTitle := func(s string) { mu.Lock(); got = append(got, s); mu.Unlock() }

	if rc := RunQueued(context.Background(), o, nil, onTitle); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || got[len(got)-1] != "Rick Astley - Never Gonna Give You Up" {
		t.Fatalf("onTitle calls = %v, want the resolved title reported", got)
	}
}

// A nil onTitle is the interactive / no-write-back path: RunQueued must run
// cleanly, proving the watcher is entirely optional.
func TestRunQueuedNilOnTitleIsClean(t *testing.T) {
	fakeYtDlp(t)
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	o := runOptions(t, core.ModeSilent, t.TempDir())
	if rc := RunQueued(context.Background(), o, nil, nil); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
}
