package queue

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
)

func job(url string, at time.Time) Job {
	return Job{
		URL:        url,
		Playlist:   true,
		Settings:   config.Settings{Format: "flac", OutputDir: "/music/x", Concurrency: 5},
		EnqueuedAt: at,
	}
}

func mustEnqueue(t *testing.T, s *Spool, j Job) string {
	t.Helper()
	id, err := s.Enqueue(j)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func TestEnqueueRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	at := time.Unix(1000, 0)
	mustEnqueue(t, s, job("https://youtu.be/A", at))

	snap, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if p, r, d, f := snap.Counts(); p != 1 || r != 0 || d != 0 || f != 0 {
		t.Fatalf("counts = %d/%d/%d/%d, want 1/0/0/0", p, r, d, f)
	}
	got := snap.Pending[0].Job
	if got.URL != "https://youtu.be/A" || !got.Playlist || got.Settings.Format != "flac" || got.Settings.Concurrency != 5 {
		t.Errorf("job did not round-trip: %+v", got)
	}
	if !got.EnqueuedAt.Equal(at) {
		t.Errorf("EnqueuedAt = %v, want %v", got.EnqueuedAt, at)
	}
}

func TestClaimAndComplete(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))

	c, err := s.ClaimNext()
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.ID != id || c.Job.URL != "https://youtu.be/A" {
		t.Fatalf("ClaimNext = %+v, want id %s", c, id)
	}
	// It is now in running/, no longer claimable.
	if c2, _ := s.ClaimNext(); c2 != nil {
		t.Fatalf("second ClaimNext returned %+v, want nil", c2)
	}
	snap, _ := s.List()
	if _, r, _, _ := snap.Counts(); r != 1 {
		t.Fatalf("running count = %d, want 1", r)
	}

	if err := s.MarkDone(id); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	snap, _ = s.List()
	if p, r, d, f := snap.Counts(); p != 0 || r != 0 || d != 1 || f != 0 {
		t.Fatalf("after MarkDone counts = %d/%d/%d/%d, want 0/0/1/0", p, r, d, f)
	}
}

func TestClaimIsFIFO(t *testing.T) {
	s := Open(t.TempDir())
	// Enqueue newest-first by wall time, oldest EnqueuedAt should claim first.
	mustEnqueue(t, s, job("C", time.Unix(300, 0)))
	mustEnqueue(t, s, job("A", time.Unix(100, 0)))
	mustEnqueue(t, s, job("B", time.Unix(200, 0)))

	var order []string
	for {
		c, err := s.ClaimNext()
		if err != nil {
			t.Fatal(err)
		}
		if c == nil {
			break
		}
		order = append(order, c.Job.URL)
		if err := s.MarkDone(c.ID); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"A", "B", "C"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("claim order = %v, want %v (FIFO by EnqueuedAt)", order, want)
	}
}

// Same nanosecond + same URL must not overwrite: the per-file unique suffix keeps
// both jobs. This is the durability guarantee behind the atomic publish.
func TestEnqueueSameInstantNoOverwrite(t *testing.T) {
	s := Open(t.TempDir())
	at := time.Unix(42, 0)
	id1 := mustEnqueue(t, s, job("https://youtu.be/A", at))
	id2 := mustEnqueue(t, s, job("https://youtu.be/A", at))
	if id1 == id2 {
		t.Fatalf("two enqueues produced the same id %q", id1)
	}
	snap, _ := s.List()
	if p, _, _, _ := snap.Counts(); p != 2 {
		t.Fatalf("pending count = %d, want 2 (no overwrite)", p)
	}
}

// Concurrent workers claiming from a shared spool: every job is claimed exactly
// once, none lost, none duplicated. Run under -race.
func TestConcurrentClaimNoDuplicates(t *testing.T) {
	s := Open(t.TempDir())
	const n = 60
	for i := 0; i < n; i++ {
		mustEnqueue(t, s, job("https://youtu.be/"+string(rune('a'+i%26)), time.Unix(int64(i), 0)))
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, err := s.ClaimNext()
				if err != nil {
					t.Errorf("ClaimNext: %v", err)
					return
				}
				if c == nil {
					return
				}
				mu.Lock()
				seen[c.ID]++
				mu.Unlock()
				if err := s.MarkDone(c.ID); err != nil {
					t.Errorf("MarkDone: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("claimed %d distinct jobs, want %d (some lost)", len(seen), n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("job %s claimed %d times, want exactly 1", id, count)
		}
	}
	snap, _ := s.List()
	if p, r, d, f := snap.Counts(); p != 0 || r != 0 || d != n || f != 0 {
		t.Fatalf("final counts = %d/%d/%d/%d, want 0/0/%d/0", p, r, d, f, n)
	}
}

func TestRequeueRunning(t *testing.T) {
	s := Open(t.TempDir())
	mustEnqueue(t, s, job("A", time.Unix(1, 0)))
	mustEnqueue(t, s, job("B", time.Unix(2, 0)))
	// Claim both → both in running/.
	if _, err := s.ClaimNext(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNext(); err != nil {
		t.Fatal(err)
	}
	if _, r, _, _ := mustList(t, s).Counts(); r != 2 {
		t.Fatalf("running != 2 before requeue")
	}

	n, err := s.RequeueRunning()
	if err != nil {
		t.Fatalf("RequeueRunning: %v", err)
	}
	if n != 2 {
		t.Errorf("requeued %d, want 2", n)
	}
	if p, r, _, _ := mustList(t, s).Counts(); p != 2 || r != 0 {
		t.Fatalf("after requeue pending/running = %d/%d, want 2/0", p, r)
	}
}

// A corrupt (non-JSON) file in pending/ is quarantined to failed/ and does not
// block claiming the valid job behind it.
func TestClaimQuarantinesCorruptJob(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// A corrupt job with an early-sorting id so it is scanned first.
	corrupt := filepath.Join(s.dir(Pending), "00000000000000000000-deadbeef-xxxx.json")
	if err := os.WriteFile(corrupt, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	valid := mustEnqueue(t, s, job("https://youtu.be/OK", time.Unix(9999, 0)))

	c, err := s.ClaimNext()
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.ID != valid {
		t.Fatalf("ClaimNext = %+v, want the valid job %s", c, valid)
	}
	if _, _, _, f := mustList(t, s).Counts(); f != 1 {
		t.Errorf("corrupt job was not quarantined to failed/ (failed count = %d)", f)
	}
}

func TestListMissingSpoolIsEmpty(t *testing.T) {
	s := Open(t.TempDir()) // nothing ever enqueued; no dirs exist
	snap, err := s.List()
	if err != nil {
		t.Fatalf("List on missing spool errored: %v", err)
	}
	if p, r, d, f := snap.Counts(); p != 0 || r != 0 || d != 0 || f != 0 {
		t.Fatalf("empty spool counts = %d/%d/%d/%d, want all 0", p, r, d, f)
	}
	// And claiming from an empty spool is a clean nil.
	if c, err := s.ClaimNext(); err != nil || c != nil {
		t.Fatalf("ClaimNext on empty = (%+v, %v), want (nil, nil)", c, err)
	}
}

func mustList(t *testing.T, s *Spool) Snapshot {
	t.Helper()
	snap, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
