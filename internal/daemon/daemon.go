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
	"context"
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
	// DefaultFirstClientGrace covers the gap between `ytdl gui` spawning the
	// daemon and the browser actually connecting — a cold browser start can far
	// exceed DefaultIdleTimeout, and the URL just printed must not go dead.
	DefaultFirstClientGrace = 2 * time.Minute
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
	// Run executes one claimed job under ctx and returns its exit code (0 =
	// success). It receives the whole Claim, so the runner can key side-channels
	// (the GUI's live-progress stream) by the job's spool id. ctx is cancelled to
	// stop the job — by the per-job JobTimeout, or by an external `ytdl cancel`
	// the daemon's watcher routes to this job's cancel func. It is called from a
	// worker goroutine; it must be safe for concurrent use.
	Run func(ctx context.Context, cl queue.Claim) int
	// JobTimeout caps how long a single job may run before its ctx is cancelled
	// (the process group is then torn down by the runner). 0 means no limit.
	JobTimeout time.Duration
	// IdleTimeout / PollInterval default to the package constants when zero.
	IdleTimeout  time.Duration
	PollInterval time.Duration
	// RetentionDays ages out terminal (done/failed) spool files at startup,
	// reusing log_retention_days; <= 0 keeps everything (ADR-0009).
	RetentionDays int
	// LiveClients reports whether a GUI client is currently connected. It is the
	// second half of the ADR-0008 lifetime rule — the daemon lives while
	// (queue has work) OR (a GUI client is connected) — so an idle queue no
	// longer ends the process while a browser tab is watching. nil (the CLI-only
	// case) means "no GUI", i.e. exactly the Cycle 2B-core idle-exit behaviour.
	LiveClients func() bool
	// FirstClientGrace is how long a GUI-serving daemon waits for its FIRST
	// client before the ordinary idle timeout applies; it defaults to
	// DefaultFirstClientGrace and is ignored when LiveClients is nil.
	FirstClientGrace time.Duration
	// LogPath is the daemon's diagnostics log (the seam ADR-0007 flagged as
	// missing — the daemon runs detached with /dev/null stdio, so lifecycle
	// events, persistent spool errors and cancel/timeout kills are otherwise
	// invisible). Empty disables it. Best-effort: a log failure never affects
	// draining.
	LogPath string
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
	if cfg.FirstClientGrace <= 0 {
		cfg.FirstClientGrace = DefaultFirstClientGrace
	}

	if err := cfg.Spool.EnsureDirs(); err != nil {
		return err
	}

	release, err := acquireLock(cfg.Spool.LockPath())
	if err != nil {
		return err // ErrAlreadyRunning or a real lock failure
	}
	defer release()

	d := newDiag(cfg.LogPath)
	d.logf("daemon start (pid=%d, concurrency=%d, job_timeout=%s)", os.Getpid(), cfg.Concurrency, cfg.JobTimeout)
	defer d.logf("daemon exit")

	// Crash recovery: we hold the exclusive lock, so no live worker owns any
	// running/ file — anything there is an orphan of a killed daemon. Best-effort:
	// a file that fails to requeue (permission, cross-mount) must not stop us from
	// draining the rest of the queue, matching the "never block on a best-effort
	// step" philosophy the log store already follows.
	if n, _ := cfg.Spool.RequeueRunning(); n > 0 {
		d.logf("recovered %d orphaned running job(s) → pending", n)
	}

	// Best-effort: age out old terminal spool files so done/failed do not grow
	// unbounded across drain sessions. A prune failure must not stop draining.
	_ = cfg.Spool.PruneTerminal(cfg.RetentionDays)

	drain(cfg, d)
	return nil
}

// drain is the claim/dispatch/idle loop. Extracted from Serve so the locking and
// recovery stay readable; assumes the lock is held.
func drain(cfg Config, d *diag) {
	var sem chan struct{}
	if cfg.Concurrency > 0 {
		sem = make(chan struct{}, cfg.Concurrency)
	}
	acquire := func() {
		if sem != nil {
			sem <- struct{}{}
		}
	}
	release := func() {
		if sem != nil {
			<-sem
		}
	}
	var wg sync.WaitGroup
	var inflight int64

	// Registry of in-flight jobs' cancel funcs, and the watcher that turns a
	// filesystem cancel marker (`ytdl cancel`, another process) into a ctx cancel
	// on the matching running job. The watcher lives for the whole drain.
	reg := newCancelRegistry()
	watchDone := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() { defer watchWG.Done(); watchCancels(cfg, reg, d, watchDone) }()

	idleSince := time.Now()
	started := time.Now()
	sawClient := false
	for {
		// Take a worker slot BEFORE claiming, so a job is only moved into running/
		// once a slot is truly free — the pool bounds claims, not just executions,
		// and `running/` never transiently exceeds the cap.
		acquire()
		c, err := cfg.Spool.ClaimNext()
		if err != nil {
			d.logf("claim error: %v", err) // otherwise invisible — the daemon is silent
		}
		if err == nil && c != nil {
			idleSince = time.Now()
			atomic.AddInt64(&inflight, 1)
			wg.Add(1)
			go func(cl queue.Claim) {
				defer wg.Done()
				defer atomic.AddInt64(&inflight, -1)
				defer release()
				// Per-job context: bounded by JobTimeout, and cancellable so an
				// external `ytdl cancel` (routed by the watcher) can stop this job.
				ctx, cancel := jobContext(cfg.JobTimeout)
				defer cancel()
				reg.add(cl.ID, cancel)
				defer reg.remove(cl.ID)

				rc, panicked := runJobGuarded(cfg.Run, ctx, cl)
				switch {
				case panicked:
					d.logf("job %s panicked → marked failed", cl.ID)
				case errors.Is(ctx.Err(), context.DeadlineExceeded):
					d.logf("job %s timed out after %s → killed", cl.ID, cfg.JobTimeout)
				}
				if rc == 0 {
					_ = cfg.Spool.MarkDone(cl.ID)
				} else {
					_ = cfg.Spool.MarkFailed(cl.ID)
				}
			}(*c)
			continue
		}

		// No job claimed — the queue is empty, or ClaimNext hit a filesystem error.
		// Release the slot we took and fall into idle handling. A ClaimNext error
		// deliberately reaches this idle check (rather than a hot `continue`): a
		// persistently-failing spool must NOT keep the daemon — and its exclusive
		// lock — alive forever, which would make every later `ytdl -b` see
		// ErrAlreadyRunning against a wedged incumbent. After IdleTimeout it exits,
		// and a later invocation spawns a fresh daemon to retry.
		release()
		if atomic.LoadInt64(&inflight) > 0 {
			idleSince = time.Now() // work is still in flight → not idle
			time.Sleep(cfg.PollInterval)
			continue
		}
		// ADR-0008: the daemon is long-lived but session-scoped — alive while the
		// queue has work OR a GUI client is connected. A drained queue is therefore
		// NOT enough to exit while a browser tab is watching; keep the idle clock
		// reset so the full grace period only starts once the GUI disconnects.
		if liveClients(cfg.LiveClients) {
			sawClient = true
			idleSince = time.Now()
			time.Sleep(cfg.PollInterval)
			continue
		}
		// A GUI daemon has just been spawned by `ytdl gui`, which prints a URL and
		// launches a browser: a cold browser start can take far longer than
		// IdleTimeout, so hold the door open until the FIRST client arrives.
		if !sawClient && cfg.LiveClients != nil && time.Since(started) < cfg.FirstClientGrace {
			idleSince = time.Now()
			time.Sleep(cfg.PollInterval)
			continue
		}
		if time.Since(idleSince) >= cfg.IdleTimeout {
			// Re-check on the way out: a client that connected during the last poll
			// window would otherwise be cut off the instant it loaded the page.
			if liveClients(cfg.LiveClients) {
				sawClient = true
				idleSince = time.Now()
				continue
			}
			break
		}
		time.Sleep(cfg.PollInterval)
	}
	wg.Wait()
	close(watchDone)
	watchWG.Wait()
}

// runJobGuarded runs one job and converts a panic in the injected runner into a
// failure exit code, so a single misbehaving job cannot crash the whole daemon
// (which would abandon every sibling download's yt-dlp child and strand their
// spool entries in running/). The panicking job is marked failed, not retried;
// panicked lets the caller log it (the daemon is otherwise silent).
func runJobGuarded(run func(context.Context, queue.Claim) int, ctx context.Context, cl queue.Claim) (rc int, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			rc, panicked = 1, true
		}
	}()
	return run(ctx, cl), false
}

// jobContext builds a per-job context: bounded by timeout when set (>0), else a
// plain cancellable context (0 = no limit). The returned cancel MUST be called
// when the job ends — it releases the timer and lets the watcher's external
// cancel unblock.
func jobContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.WithCancel(context.Background())
}

// cancelRegistry maps an in-flight job's spool id to the CancelFunc that stops
// it. The watcher looks a marker's id up here to cancel the matching running job.
// It is safe for concurrent use (workers register/deregister; the watcher reads).
type cancelRegistry struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

func newCancelRegistry() *cancelRegistry { return &cancelRegistry{m: map[string]context.CancelFunc{}} }

func (r *cancelRegistry) add(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	r.m[id] = cancel
	r.mu.Unlock()
}

func (r *cancelRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.m, id)
	r.mu.Unlock()
}

func (r *cancelRegistry) get(id string) context.CancelFunc {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[id]
}

// watchCancels polls the spool's cancel markers every PollInterval and applies
// them, until done is closed. It is the cross-process half of `ytdl cancel`: the
// CLI (or any other process) drops a marker, this turns it into a ctx cancel on
// the matching running job.
func watchCancels(cfg Config, reg *cancelRegistry, d *diag, done <-chan struct{}) {
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			processCancels(cfg, reg, d)
		}
	}
}

// processCancels applies one round of cancel markers. For each marker id:
//   - if the job is running here (in the registry) → cancel its ctx (the runner
//     tears down the process group) and clear the marker;
//   - if it is still pending → delete it (a cancel that raced the claim; the
//     matching CLI path also does this) and clear the marker. Losing that race
//     means it just became running, so the next poll cancels it via the registry;
//   - if it is neither pending nor running (terminal, or already gone) → the
//     marker is stale, so clear it. A just-claimed job whose worker has not yet
//     registered is left for the next poll (it shows as running, not stale).
func processCancels(cfg Config, reg *cancelRegistry, d *diag) {
	ids, err := cfg.Spool.CancelRequests()
	if err != nil || len(ids) == 0 {
		return
	}
	snap, _ := cfg.Spool.List()
	state := make(map[string]queue.State, len(snap.Pending)+len(snap.Running))
	for _, e := range snap.Pending {
		state[e.ID] = queue.Pending
	}
	for _, e := range snap.Running {
		state[e.ID] = queue.Running
	}
	for _, id := range ids {
		if cancel := reg.get(id); cancel != nil {
			cancel()
			d.logf("cancelled running job %s", id)
			_ = cfg.Spool.ClearCancel(id)
			continue
		}
		switch state[id] {
		case queue.Pending:
			if was, _ := cfg.Spool.CancelPending(id); was {
				d.logf("cancelled pending job %s", id)
				_ = cfg.Spool.ClearCancel(id)
			}
			// lost the claim race → now running → next poll cancels via the registry
		case queue.Running:
			// just claimed; the worker will register momentarily → next poll
		default:
			_ = cfg.Spool.ClearCancel(id) // terminal or gone: stale marker
		}
	}
}

// maxDaemonLog caps the diagnostics log; past it the file is truncated so a
// long-running daemon cannot grow it without bound.
const maxDaemonLog = 256 * 1024

// diag is the daemon's best-effort diagnostics log (Config.LogPath). The daemon
// runs detached with /dev/null stdio, so without this a persistent spool error,
// a cancel/timeout kill or a job panic would be invisible (the gap ADR-0007
// flagged). A nil *diag or an empty path is a silent no-op; a write failure is
// swallowed — logging must never affect draining.
type diag struct {
	mu   sync.Mutex
	path string
}

func newDiag(path string) *diag { return &diag{path: path} }

func (d *diag) logf(format string, args ...any) {
	if d == nil || d.path == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if fi, err := os.Stat(d.path); err == nil && fi.Size() > maxDaemonLog {
		_ = os.Truncate(d.path, 0)
	}
	f, err := os.OpenFile(d.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// liveClients evaluates the injected GUI-connected hook defensively: nil means
// "no GUI" (the CLI-only case), and a panicking hook must not kill the daemon
// and strand its running jobs — Run is already protected this way.
func liveClients(f func() bool) (live bool) {
	if f == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			live = false
		}
	}()
	return f()
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

// GUIFlag asks a spawned daemon to also serve the web interface. Only `ytdl gui`
// passes it: a queue daemon started by `ytdl -b` stays headless, so the CLI-only
// path opens no listening socket at all (the pre-Cycle-3 surface).
const GUIFlag = "--gui"

// Spawn starts a headless queue daemon: it drains the spool and serves no GUI.
func Spawn() error { return spawn() }

// SpawnGUI starts a daemon that also serves the web interface. Kept separate
// from Spawn so the GUI is opt-in per invocation rather than a side effect of
// every background download.
func SpawnGUI() error { return spawn(GUIFlag) }

// spawn self-exec's the current binary into the hidden __daemon subcommand,
// detached from the terminal (Setsid + /dev/null stdio) and released so the
// parent does not wait. It is idempotent: a duplicate daemon exits immediately on
// the contended lock. Mirrors run.runBackground's detach, which it supersedes.
func spawn(extra ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, append([]string{DaemonSubcommand}, extra...)...)
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
