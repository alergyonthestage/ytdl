package daemon

import (
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/queue"
)

// fastCfg builds a Config with tiny timings so tests do not wait 20s to idle-exit.
func fastCfg(sp *queue.Spool, concurrency int, run func(queue.Job) int) Config {
	return Config{
		Spool:        sp,
		Concurrency:  concurrency,
		Run:          run,
		IdleTimeout:  40 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	}
}

func enqueueN(t *testing.T, sp *queue.Spool, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/x", EnqueuedAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
}

func TestServeDrainsAllToDone(t *testing.T) {
	sp := queue.Open(t.TempDir())
	enqueueN(t, sp, 10)

	var ran int64
	err := Serve(fastCfg(sp, 3, func(queue.Job) int { atomic.AddInt64(&ran, 1); return 0 }))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if ran != 10 {
		t.Errorf("ran %d jobs, want 10", ran)
	}
	if p, r, d, f := mustCounts(t, sp); p != 0 || r != 0 || d != 10 || f != 0 {
		t.Errorf("counts = %d/%d/%d/%d, want 0/0/10/0", p, r, d, f)
	}
}

func TestServeFailedJobsGoToFailed(t *testing.T) {
	sp := queue.Open(t.TempDir())
	enqueueN(t, sp, 4)
	// Odd-arrival jobs "fail" (rc=1). Since URLs are identical we just alternate by
	// a call counter.
	var n int64
	err := Serve(fastCfg(sp, 2, func(queue.Job) int {
		if atomic.AddInt64(&n, 1)%2 == 0 {
			return 1
		}
		return 0
	}))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if p, r, d, f := mustCounts(t, sp); p != 0 || r != 0 || d != 2 || f != 2 {
		t.Errorf("counts = %d/%d/%d/%d, want 0/0/2/2", p, r, d, f)
	}
}

func TestServeRespectsConcurrencyCap(t *testing.T) {
	sp := queue.Open(t.TempDir())
	enqueueN(t, sp, 12)

	var cur, max int64
	run := func(queue.Job) int {
		c := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&max)
			if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		return 0
	}
	if err := Serve(fastCfg(sp, 2, run)); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if max > 2 {
		t.Errorf("observed max concurrency %d, want <= 2", max)
	}
	if _, _, d, _ := mustCounts(t, sp); d != 12 {
		t.Errorf("done = %d, want 12", d)
	}
}

func TestServeUnlimitedDrainsAll(t *testing.T) {
	sp := queue.Open(t.TempDir())
	enqueueN(t, sp, 8)
	// Concurrency 0 = unlimited; it must still drain everything.
	if err := Serve(fastCfg(sp, 0, func(queue.Job) int { return 0 })); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if _, _, d, _ := mustCounts(t, sp); d != 8 {
		t.Errorf("done = %d, want 8", d)
	}
}

// A job left in running/ (a killed daemon) is requeued and drained on the next
// Serve, because Serve holds the exclusive lock during recovery.
func TestServeRecoversOrphanedRunning(t *testing.T) {
	sp := queue.Open(t.TempDir())
	if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/orphan", EnqueuedAt: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-download: claim it (→ running/) and never complete it.
	if _, err := sp.ClaimNext(); err != nil {
		t.Fatal(err)
	}
	if _, r, _, _ := mustCounts(t, sp); r != 1 {
		t.Fatalf("setup: running != 1")
	}

	var ran int64
	if err := Serve(fastCfg(sp, 2, func(queue.Job) int { atomic.AddInt64(&ran, 1); return 0 })); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if ran != 1 {
		t.Errorf("recovered-and-ran = %d, want 1", ran)
	}
	if p, r, d, f := mustCounts(t, sp); p != 0 || r != 0 || d != 1 || f != 0 {
		t.Errorf("counts = %d/%d/%d/%d, want 0/0/1/0", p, r, d, f)
	}
}

func TestServeAlreadyRunning(t *testing.T) {
	sp := queue.Open(t.TempDir())
	if err := sp.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// Hold the lock, imitating a live daemon.
	lf, err := os.OpenFile(sp.LockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("test could not take the lock: %v", err)
	}
	defer func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
	}()

	err = Serve(fastCfg(sp, 2, func(queue.Job) int { return 0 }))
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Serve with a held lock = %v, want ErrAlreadyRunning", err)
	}
}

func TestServeRejectsNilConfig(t *testing.T) {
	if err := Serve(Config{Run: func(queue.Job) int { return 0 }}); err == nil {
		t.Error("Serve without a Spool returned nil error")
	}
	if err := Serve(Config{Spool: queue.Open(t.TempDir())}); err == nil {
		t.Error("Serve without a Run returned nil error")
	}
}

func TestIsRunning(t *testing.T) {
	sp := queue.Open(t.TempDir())
	if err := sp.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if IsRunning(sp.LockPath()) {
		t.Error("IsRunning = true with no daemon, want false")
	}

	lf, err := os.OpenFile(sp.LockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("could not take the lock: %v", err)
	}
	defer func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
	}()
	if !IsRunning(sp.LockPath()) {
		t.Error("IsRunning = false while the lock is held, want true")
	}
}

func mustCounts(t *testing.T, sp *queue.Spool) (pending, running, done, failed int) {
	t.Helper()
	snap, err := sp.List()
	if err != nil {
		t.Fatal(err)
	}
	return snap.Counts()
}
