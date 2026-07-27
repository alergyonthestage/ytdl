package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

func settings() config.Settings {
	s := config.Defaults()
	s.OutputDir = "/music/current"
	s.Format = "mp3"
	return s
}

func enqueue(t *testing.T, sp *queue.Spool, j queue.Job) string {
	t.Helper()
	id, err := sp.Enqueue(j)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

// pendingJob returns the single pending job, failing if there is not exactly one.
func pendingJob(t *testing.T, sp *queue.Spool) queue.Job {
	t.Helper()
	snap, err := sp.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snap.Pending) != 1 {
		t.Fatalf("want exactly 1 pending job, got %d", len(snap.Pending))
	}
	return snap.Pending[0].Job
}

// ---- Cancel --------------------------------------------------------------

// TestCancelPendingRemovesTheJob: a job that never started is simply gone, and
// the marker it briefly needed is cleaned up rather than left to confuse a
// later daemon.
func TestCancelPendingRemovesTheJob(t *testing.T) {
	dir := t.TempDir()
	sp := queue.Open(dir)
	id := enqueue(t, sp, queue.Job{URL: "https://youtu.be/A", Settings: settings()})

	was, err := Cancel(sp, id)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !was {
		t.Error("wasPending = false for a job that had not started")
	}
	snap, err := sp.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Pending) != 0 {
		t.Errorf("the cancelled job is still pending: %+v", snap.Pending)
	}
	if _, err := os.Stat(filepath.Join(dir, "queue", "cancel", id)); !os.IsNotExist(err) {
		t.Error("a marker was left behind for a job that was deleted before it ran")
	}
}

// TestCancelRunningLeavesTheMarker is the mechanism the daemon's watcher polls:
// a running job cannot be deleted from under a live download, so the marker IS
// the cancel. Losing it would leave the job downloading after the user asked it
// to stop.
func TestCancelRunningLeavesTheMarker(t *testing.T) {
	dir := t.TempDir()
	sp := queue.Open(dir)
	enqueue(t, sp, queue.Job{URL: "https://youtu.be/A", Settings: settings()})
	claim, err := sp.ClaimNext()
	if err != nil || claim == nil {
		t.Fatalf("ClaimNext: %v (claim %v)", err, claim)
	}

	was, err := Cancel(sp, claim.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if was {
		t.Error("wasPending = true for a job the daemon had already claimed")
	}
	if _, err := os.Stat(filepath.Join(dir, "queue", "cancel", claim.ID)); err != nil {
		t.Errorf("no cancel marker for the running job: %v", err)
	}
}

// TestCancelUnknownIDStillMarks: the spool is lock-free, so a job can move or
// vanish between the listing a user read and the cancel they asked for. Marking
// an id that is not there must not be an error — the marker is harmless and the
// alternative is telling the user a cancel failed when the job is already gone.
func TestCancelUnknownIDStillMarks(t *testing.T) {
	sp := queue.Open(t.TempDir())
	was, err := Cancel(sp, "nosuchjob")
	if err != nil {
		t.Fatalf("Cancel of an unknown id errored: %v", err)
	}
	if was {
		t.Error("wasPending = true for an id that was never in the spool")
	}
}

// ---- Again ---------------------------------------------------------------

func record(url string) logstore.Entry {
	return logstore.Entry{
		Time: time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC),
		URL:  url, Title: "Artista - Traccia", Format: "flac", Success: true,
		Path: "/music/old/Artista - Traccia.flac", Dir: "/music/old",
	}
}

// TestAgainEnqueuesFromARecord is the feature: a history row becomes a fresh
// queued job.
func TestAgainEnqueuesFromARecord(t *testing.T) {
	sp := queue.Open(t.TempDir())
	id, err := Again(sp, record("https://youtu.be/A"), settings())
	if err != nil {
		t.Fatalf("Again: %v", err)
	}
	if id == "" {
		t.Error("Again returned an empty job id")
	}
	j := pendingJob(t, sp)
	if j.URL != "https://youtu.be/A" {
		t.Errorf("URL = %q, want the record's link", j.URL)
	}
}

// TestAgainReproducesFormatAndPlaylist: these two decide WHAT gets downloaded.
// A ✓ .flac row whose primary action quietly produces an .mp3 is not "the same
// thing again".
func TestAgainReproducesFormatAndPlaylist(t *testing.T) {
	sp := queue.Open(t.TempDir())
	e := record("https://youtu.be/A")
	e.Playlist = true
	if _, err := Again(sp, e, settings()); err != nil { // current default is mp3
		t.Fatalf("Again: %v", err)
	}
	j := pendingJob(t, sp)
	if j.Settings.Format != "flac" {
		t.Errorf("Format = %q, want the record's flac, not the current default", j.Settings.Format)
	}
	if !j.Playlist {
		t.Error("Playlist = false; the record was a playlist job")
	}
}

// TestAgainUsesCurrentFolder: the folder the file went to a month ago may not
// exist any more, and a user who has since changed it means the new one.
func TestAgainUsesCurrentFolder(t *testing.T) {
	sp := queue.Open(t.TempDir())
	if _, err := Again(sp, record("https://youtu.be/A"), settings()); err != nil {
		t.Fatalf("Again: %v", err)
	}
	if j := pendingJob(t, sp); j.Settings.OutputDir != "/music/current" {
		t.Errorf("OutputDir = %q, want the CURRENT folder /music/current", j.Settings.OutputDir)
	}
}

// TestAgainFallsBackToCurrentFormat: a record written by an older ytdl, or one
// hand-edited into nonsense, must not queue an invalid format (or fail).
func TestAgainFallsBackToCurrentFormat(t *testing.T) {
	for _, format := range []string{"", "mp4", "wav; rm -rf ~"} {
		t.Run("format "+format, func(t *testing.T) {
			sp := queue.Open(t.TempDir())
			e := record("https://youtu.be/A")
			e.Format = format
			if _, err := Again(sp, e, settings()); err != nil {
				t.Fatalf("Again: %v", err)
			}
			if j := pendingJob(t, sp); j.Settings.Format != "mp3" {
				t.Errorf("Format = %q, want the current default mp3", j.Settings.Format)
			}
		})
	}
}

// TestAgainCarriesTheTitle lets the queue name the job at once instead of showing
// a bare URL until the daemon resolves the metadata.
func TestAgainCarriesTheTitle(t *testing.T) {
	sp := queue.Open(t.TempDir())
	if _, err := Again(sp, record("https://youtu.be/A"), settings()); err != nil {
		t.Fatalf("Again: %v", err)
	}
	if j := pendingJob(t, sp); j.Title != "Artista - Traccia" {
		t.Errorf("Title = %q, want the record's title", j.Title)
	}
}

// TestAgainDropsThePlaylistTitle: one track's title misrepresents a whole
// playlist, which is why the queue leaves it empty for playlist jobs (Cycle 4).
func TestAgainDropsThePlaylistTitle(t *testing.T) {
	sp := queue.Open(t.TempDir())
	e := record("https://youtu.be/A")
	e.Playlist = true
	if _, err := Again(sp, e, settings()); err != nil {
		t.Fatalf("Again: %v", err)
	}
	if j := pendingJob(t, sp); j.Title != "" {
		t.Errorf("Title = %q on a playlist job, want empty", j.Title)
	}
}

// TestAgainRefusesADuplicate is the double-click guard (design §6): without it,
// pressing "Riscarica" twice queues the same download twice.
func TestAgainRefusesADuplicate(t *testing.T) {
	sp := queue.Open(t.TempDir())
	if _, err := Again(sp, record("https://youtu.be/A"), settings()); err != nil {
		t.Fatalf("first Again: %v", err)
	}
	if _, err := Again(sp, record("https://youtu.be/A"), settings()); !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("second Again error = %v, want ErrAlreadyQueued", err)
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 1 {
		t.Errorf("the queue holds %d copies of the same link, want 1", len(snap.Pending))
	}
}

// TestAgainMatchesTheDuplicateOnNormalisedURL: the same download reached through
// a #fragment or an upper-case host is the same download.
func TestAgainMatchesTheDuplicateOnNormalisedURL(t *testing.T) {
	sp := queue.Open(t.TempDir())
	enqueue(t, sp, queue.Job{URL: "https://youtu.be/A", Settings: settings()})

	for _, variant := range []string{
		"https://youtu.be/A#t=30",
		"HTTPS://YOUTU.BE/A",
		"  https://youtu.be/A  ",
	} {
		t.Run(variant, func(t *testing.T) {
			if _, err := Again(sp, record(variant), settings()); !errors.Is(err, ErrAlreadyQueued) {
				t.Errorf("Again(%q) error = %v, want ErrAlreadyQueued", variant, err)
			}
		})
	}
}

// TestAgainAllowsARecordThatAlreadyFinished: re-downloading something that
// finished (or failed) is exactly what Riscarica is for, so terminal spool
// states must not block it.
func TestAgainAllowsARecordThatAlreadyFinished(t *testing.T) {
	sp := queue.Open(t.TempDir())
	id := enqueue(t, sp, queue.Job{URL: "https://youtu.be/A", Settings: settings()})
	claim, err := sp.ClaimNext()
	if err != nil || claim == nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if err := sp.MarkDone(id); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	if _, err := Again(sp, record("https://youtu.be/A"), settings()); err != nil {
		t.Fatalf("Again after a completed job: %v, want it to enqueue", err)
	}
}

// TestAgainRejectsARecordWithNoURL: nothing can be re-downloaded from it, and
// enqueueing a blank job would give the daemon a guaranteed failure.
func TestAgainRejectsARecordWithNoURL(t *testing.T) {
	sp := queue.Open(t.TempDir())
	for _, url := range []string{"", "   "} {
		if _, err := Again(sp, record(url), settings()); !errors.Is(err, ErrNoURL) {
			t.Errorf("Again(%q) error = %v, want ErrNoURL", url, err)
		}
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 0 {
		t.Errorf("a URL-less record enqueued %d jobs", len(snap.Pending))
	}
}

func TestIsQueued(t *testing.T) {
	snap := queue.Snapshot{
		Pending: []queue.Entry{{Job: queue.Job{URL: "https://youtu.be/P"}}},
		Running: []queue.Entry{{Job: queue.Job{URL: "https://youtu.be/R"}}},
		Done:    []queue.Entry{{Job: queue.Job{URL: "https://youtu.be/D"}}},
		Failed:  []queue.Entry{{Job: queue.Job{URL: "https://youtu.be/F"}}},
	}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://youtu.be/P", true},
		{"https://youtu.be/R", true},
		{"https://youtu.be/D", false}, // terminal: re-downloading is the point
		{"https://youtu.be/F", false},
		{"https://youtu.be/X", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsQueued(snap, tc.url); got != tc.want {
			t.Errorf("IsQueued(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}
