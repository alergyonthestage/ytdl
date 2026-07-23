package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/queue"
)

func entry(url string, at time.Time, st queue.State) queue.Entry {
	return queue.Entry{ID: "id-" + url, State: st, Job: queue.Job{URL: url, EnqueuedAt: at}}
}

func TestRenderQueueEmpty(t *testing.T) {
	got := RenderQueue(queue.Snapshot{})
	if !strings.Contains(got, "nessun download") {
		t.Errorf("empty queue render missing the empty notice:\n%s", got)
	}
	if !strings.Contains(got, "0 in attesa") {
		t.Errorf("empty queue footer missing zero counts:\n%s", got)
	}
}

func TestRenderQueueListsJobs(t *testing.T) {
	at := time.Date(2026, 7, 23, 14, 5, 0, 0, time.Local)
	snap := queue.Snapshot{
		Running: []queue.Entry{entry("https://youtu.be/RUN", at, queue.Running)},
		Pending: []queue.Entry{
			entry("https://youtu.be/P1", at, queue.Pending),
			entry("https://youtu.be/P2", at, queue.Pending),
		},
		Done:   []queue.Entry{entry("https://youtu.be/D", at, queue.Done)},
		Failed: []queue.Entry{entry("https://youtu.be/F", at, queue.Failed)},
	}
	got := RenderQueue(snap)
	for _, want := range []string{
		"in corso (1):", "https://youtu.be/RUN",
		"in attesa (2):", "https://youtu.be/P1", "https://youtu.be/P2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q:\n%s", want, got)
		}
	}
	// The footer reports all four counts.
	if !strings.Contains(got, "2 in attesa · 1 in corso · 1 completati · 1 falliti") {
		t.Errorf("footer counts wrong:\n%s", got)
	}
}

func TestRenderQueueUnreadableJob(t *testing.T) {
	// An entry whose job did not parse (zero Job) still renders, keyed by id.
	snap := queue.Snapshot{Pending: []queue.Entry{{ID: "broken", State: queue.Pending}}}
	got := RenderQueue(snap)
	if !strings.Contains(got, "broken") || !strings.Contains(got, "illeggibile") {
		t.Errorf("unreadable job not surfaced:\n%s", got)
	}
}

func TestRenderStatus(t *testing.T) {
	snap := queue.Snapshot{
		Pending: []queue.Entry{{}, {}, {}},
		Running: []queue.Entry{{}},
		Done:    []queue.Entry{{}, {}},
	}
	active := RenderStatus(snap, true)
	if !strings.Contains(active, "daemon:      attivo") {
		t.Errorf("status(active) missing active daemon line:\n%s", active)
	}
	for _, want := range []string{"in attesa:   3", "in corso:    1", "completati:  2", "falliti:     0"} {
		if !strings.Contains(active, want) {
			t.Errorf("status missing %q:\n%s", want, active)
		}
	}
	inactive := RenderStatus(queue.Snapshot{}, false)
	if !strings.Contains(inactive, "daemon:      non attivo") {
		t.Errorf("status(inactive) missing inactive daemon line:\n%s", inactive)
	}
}
