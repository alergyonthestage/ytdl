package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SetTitle records a title on a running job; the title is visible in List and
// then travels with the file into a terminal state (so `ytdl retry` can name a
// failed job, not just show its URL).
func TestSetTitleOnRunningJobTravelsToTerminal(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))
	if _, err := s.ClaimNext(); err != nil { // → running/
		t.Fatal(err)
	}

	if err := s.SetTitle(id, "Artist - Track"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if got := mustList(t, s).Running[0].Job.Title; got != "Artist - Track" {
		t.Fatalf("running Title = %q, want %q", got, "Artist - Track")
	}

	// The title must survive the terminal move (a pure rename), so the retry view
	// reads it off failed/.
	if err := s.MarkFailed(id); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if got := mustList(t, s).Failed[0].Job.Title; got != "Artist - Track" {
		t.Fatalf("failed Title = %q, want it preserved across the move", got)
	}
}

// An empty title is a no-op (the title just is not known yet), and never clears
// an already-recorded one.
func TestSetTitleEmptyIsNoop(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))
	if _, err := s.ClaimNext(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle(id, "Real Title"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle(id, ""); err != nil {
		t.Fatalf("SetTitle empty: %v", err)
	}
	if got := mustList(t, s).Running[0].Job.Title; got != "Real Title" {
		t.Fatalf("Title = %q, want the empty call to be a no-op", got)
	}
}

// A title for a job that is no longer running (already terminal, or never
// claimed) is swallowed: the write just missed its window, which is not an error.
func TestSetTitleAbsentJobTolerated(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle("nonexistent-id", "whatever"); err != nil {
		t.Fatalf("SetTitle on absent running job = %v, want nil (best-effort)", err)
	}
}

// A job file written before the Title field existed (no "title" key) still parses,
// with an empty Title — the omitempty back-compat guarantee.
func TestLegacyJobWithoutTitleParses(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	legacy := `{"url":"https://youtu.be/OLD","playlist":false,"settings":{},"enqueued_at":"2020-01-02T03:04:05Z"}`
	path := filepath.Join(s.dir(Pending), "00000000000000000001-deadbeefdeadbeef-old0.json")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	got := mustList(t, s).Pending[0].Job
	if got.URL != "https://youtu.be/OLD" || got.Title != "" {
		t.Fatalf("legacy job = %+v, want URL preserved and empty Title", got)
	}
}
