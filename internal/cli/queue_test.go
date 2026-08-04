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
	got := RenderQueue(queue.Snapshot{}, QueueView{Full: true})
	if !strings.Contains(got, "coda vuota") {
		t.Errorf("empty queue render missing the empty notice:\n%s", got)
	}
	// The empty state guides the next action instead of showing a bare zero.
	if !strings.Contains(got, "ytdl -b") {
		t.Errorf("empty queue missing the enqueue hint:\n%s", got)
	}
}

func TestRenderQueueListsLiveOnly(t *testing.T) {
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
	got := RenderQueue(snap, QueueView{Full: true})
	for _, want := range []string{
		"in corso (1):", "youtu.be/RUN", // full URL contains the shortened form as a substring
		"in attesa (2):", "youtu.be/P1", "youtu.be/P2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q:\n%s", want, got)
		}
	}
	// Live-only footer: no lifetime completed/failed counts, and no terminal jobs.
	if !strings.Contains(got, "In coda: 2 in attesa · 1 in corso") {
		t.Errorf("live footer wrong:\n%s", got)
	}
	for _, absent := range []string{"completati", "falliti", "youtu.be/D", "youtu.be/F"} {
		if strings.Contains(got, absent) {
			t.Errorf("queue leaked terminal info %q (should be live-only):\n%s", absent, got)
		}
	}
}

// TestRenderQueueStatesTheDestination: the queue was the one list that never
// said where a file was going, though the job froze its resolved output dir at
// enqueue (G1). Both sections state it, ~-contracted like the history column.
func TestRenderQueueStatesTheDestination(t *testing.T) {
	at := time.Date(2026, 7, 23, 14, 5, 0, 0, time.Local)
	run := entry("https://youtu.be/RUN", at, queue.Running)
	run.Job.Settings.OutputDir = "/home/tester/Music/ytdl"
	pend := entry("https://youtu.be/P1", at, queue.Pending)
	pend.Job.Settings.OutputDir = "/mnt/backup/audio"
	snap := queue.Snapshot{Running: []queue.Entry{run}, Pending: []queue.Entry{pend}}

	got := RenderQueue(snap, QueueView{Full: true, Home: "/home/tester"})
	if !strings.Contains(got, "cartella: ~/Music/ytdl") {
		t.Errorf("the running job does not state its destination:\n%s", got)
	}
	// Outside $HOME there is nothing to contract: the path stays absolute.
	if !strings.Contains(got, "cartella: /mnt/backup/audio") {
		t.Errorf("the pending job does not state its destination:\n%s", got)
	}
}

// A job whose spec could not be read has no settings to state; an empty label
// would be a line that says nothing.
func TestRenderQueueOmitsAnUnknownDestination(t *testing.T) {
	snap := queue.Snapshot{Pending: []queue.Entry{{ID: "broken", State: queue.Pending}}}
	got := RenderQueue(snap, QueueView{Full: true, Home: "/home/tester"})
	if strings.Contains(got, "cartella:") {
		t.Errorf("an unreadable job printed an empty destination:\n%s", got)
	}
}

func TestRenderQueueUnreadableJob(t *testing.T) {
	snap := queue.Snapshot{Pending: []queue.Entry{{ID: "broken", State: queue.Pending}}}
	got := RenderQueue(snap, QueueView{Full: true})
	if !strings.Contains(got, "broken") || !strings.Contains(got, "illeggibile") {
		t.Errorf("unreadable job not surfaced:\n%s", got)
	}
}

func TestRenderStatus(t *testing.T) {
	snap := queue.Snapshot{
		Pending: []queue.Entry{{}, {}, {}},
		Running: []queue.Entry{{}},
	}
	active := RenderStatus(snap, true, RecentSummary{OK: 12, Failed: 1}, 30)
	if !strings.Contains(active, "daemon:    attivo") {
		t.Errorf("status(active) missing active daemon line:\n%s", active)
	}
	for _, want := range []string{"in attesa: 3", "in corso:  1", "recenti (ultimi 30 giorni): 12 ok · 1 fallito"} {
		if !strings.Contains(active, want) {
			t.Errorf("status missing %q:\n%s", want, active)
		}
	}
	// Inactive daemon reads as normal, not as an error (ADR-0008).
	inactive := RenderStatus(queue.Snapshot{}, false, RecentSummary{}, 30)
	if !strings.Contains(inactive, "inattivo") || strings.Contains(inactive, "non attivo") {
		t.Errorf("status(inactive) should read informationally:\n%s", inactive)
	}
}

func TestPlural(t *testing.T) {
	if Plural(1, "fallito", "falliti") != "fallito" {
		t.Error("n=1 should be singular")
	}
	for _, n := range []int{0, 2, 5} {
		if Plural(n, "fallito", "falliti") != "falliti" {
			t.Errorf("n=%d should be plural", n)
		}
	}
}

func TestRetentionWindow(t *testing.T) {
	cases := map[int]string{
		0:  "da sempre",
		1:  "ultimo giorno",
		7:  "ultimi 7 giorni",
		30: "ultimi 30 giorni",
	}
	for days, want := range cases {
		if got := retentionWindow(days); got != want {
			t.Errorf("retentionWindow(%d) = %q, want %q", days, got, want)
		}
	}
	// A negative value (keep-forever, like the config) also reads "da sempre".
	if got := retentionWindow(-1); got != "da sempre" {
		t.Errorf("retentionWindow(-1) = %q, want da sempre", got)
	}
}
