// Package daemon is ytdl's on-demand queue drainer, the `ytdld` role (Cycle 2B,
// ADR-0007). It is deliberately thin and I/O-agnostic about downloads: it owns
// the single-instance lock, crash recovery, a bounded worker pool, and idle
// self-termination, while the actual per-job execution is INJECTED as Config.Run
// (production wires it to run.Dispatch in ModeSilent; tests inject a fake). So
// this package imports only internal/queue and the standard library.
//
// Lifecycle (Serve): acquire an exclusive non-blocking flock on <queue>/lock — if
// another daemon holds it, return ErrAlreadyRunning; requeue any jobs stranded in
// running/ by a previously-killed daemon; drain pending/ under the concurrency
// cap; when pending is empty and nothing is in flight for IdleTimeout, release the
// lock and return. The residual enqueue-vs-exit race (a job arriving in the gap
// between the final empty scan and the lock release) is recovered by the next
// invocation, which spawns a fresh daemon (ADR-0007 §4).
package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alergyonthestage/ytdl/internal/queue"
)

// DaemonSubcommand is the hidden argv[0]-level keyword the CLI self-exec's into to
// become the daemon. It is absent from --help; only Spawn produces it.
const DaemonSubcommand = "__daemon"

// Default idle/poll timings; Config may override them (tests use tiny values).
const (
	DefaultIdleTimeout  = 20 * time.Second
	DefaultPollInterval = 1 * time.Second
)

// ErrAlreadyRunning is returned by Serve when another daemon already holds the
// lock. It is not a failure — the caller (the CLI) treats it as a clean exit,
// since the incumbent daemon will drain the just-enqueued job.
var ErrAlreadyRunning = errors.New("daemon already running")

// Config parameterises Serve.
type Config struct {
	Spool *queue.Spool
	// Concurrency caps parallel jobs; <= 0 means unlimited (no cap — the
	// discouraged pre-2B behaviour).
	Concurrency int
	// Run executes one claimed job and returns its exit code (0 = success). It is
	// called from a worker goroutine; it must be safe for concurrent use.
	Run func(queue.Job) int
	// IdleTimeout / PollInterval default to the package constants when zero.
	IdleTimeout  time.Duration
	PollInterval time.Duration
}

// Serve runs the drain loop until the queue is idle for IdleTimeout, then returns
// nil. It returns ErrAlreadyRunning if another daemon owns the lock, or a non-nil
// error only on an unexpected filesystem/lock failure.
func Serve(cfg Config) error {
	if cfg.Spool == nil || cfg.Run == nil {
		return errors.New("daemon: Config.Spool and Config.Run are required")
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}

	if err := cfg.Spool.EnsureDirs(); err != nil {
		return err
	}

	release, err := acquireLock(cfg.Spool.LockPath())
	if err != nil {
		return err // ErrAlreadyRunning or a real lock failure
	}
	defer release()

	// Crash recovery: we hold the exclusive lock, so no live worker owns any
	// running/ file — anything there is an orphan of a killed daemon.
	if _, err := cfg.Spool.RequeueRunning(); err != nil {
		return err
	}

	drain(cfg)
	return nil
}

// drain is the claim/dispatch/idle loop. Extracted from Serve so the locking and
// recovery stay readable; assumes the lock is held.
func drain(cfg Config) {
	var sem chan struct{}
	if cfg.Concurrency > 0 {
		sem = make(chan struct{}, cfg.Concurrency)
	}
	var wg sync.WaitGroup
	var inflight int64

	idleSince := time.Now()
	for {
		c, err := cfg.Spool.ClaimNext()
		if err != nil {
			// A transient filesystem hiccup must not kill the daemon; back off and
			// retry. Persistent failure just keeps the daemon alive until idle-exit
			// (nothing gets drained, but nothing is lost either).
			time.Sleep(cfg.PollInterval)
			continue
		}
		if c != nil {
			idleSince = time.Now()
			atomic.AddInt64(&inflight, 1)
			if sem != nil {
				sem <- struct{}{} // acquire a slot (blocks the loop when the pool is full)
			}
			wg.Add(1)
			go func(cl queue.Claim) {
				defer wg.Done()
				defer atomic.AddInt64(&inflight, -1)
				if sem != nil {
					defer func() { <-sem }()
				}
				rc := cfg.Run(cl.Job)
				if rc == 0 {
					_ = cfg.Spool.MarkDone(cl.ID)
				} else {
					_ = cfg.Spool.MarkFailed(cl.ID)
				}
			}(*c)
			continue
		}

		// Nothing claimable right now.
		if atomic.LoadInt64(&inflight) > 0 {
			idleSince = time.Now() // work is still in flight → not idle
			time.Sleep(cfg.PollInterval)
			continue
		}
		// Pending empty AND nothing in flight: the ClaimNext above was the final
		// re-scan. Exit once we have been idle long enough.
		if time.Since(idleSince) >= cfg.IdleTimeout {
			break
		}
		time.Sleep(cfg.PollInterval)
	}
	wg.Wait()
}

// acquireLock opens (creating) the lock file and takes an exclusive, non-blocking
// flock. It returns a release func that unlocks and closes. A contended lock
// yields ErrAlreadyRunning.
func acquireLock(path string) (func(), error) {
	lf, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("daemon: flock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
	}, nil
}

// IsRunning reports whether a daemon currently holds the lock, by probing it: it
// tries the same exclusive non-blocking flock and, if it succeeds, immediately
// releases (no daemon was running). A contended lock means a daemon is live. The
// probe holds the lock only for the microseconds between acquire and release; the
// tiny chance of a concurrent daemon spawn seeing that as "busy" is the same
// documented residual race the next invocation recovers (ADR-0007 §4).
func IsRunning(lockPath string) bool {
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false // can't tell; report not-running rather than block status
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	return false
}

// Spawn self-exec's the current binary into the hidden __daemon subcommand,
// detached from the terminal (Setsid + /dev/null stdio) and released so the
// parent does not wait. It is idempotent: a duplicate daemon exits immediately on
// the contended lock. Mirrors run.runBackground's detach, which it supersedes.
func Spawn() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, DaemonSubcommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		defer devnull.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
