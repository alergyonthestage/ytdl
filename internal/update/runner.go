package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

// The files one installer run leaves behind, in the state dir beside the cached
// verdict. Together they are what lets a page — even one that reloaded onto a
// different binary — find out how the update went, with no acknowledgement
// endpoint and nothing held in memory (design §7.1).
const (
	RunName = "update-run.json"
	LogName = "update.log"

	// logTailBytes bounds what a surface is shown of the installer's output. The
	// tail is the useful end: the failure is at the bottom.
	logTailBytes = 8 * 1024
)

// The states one run passes through. "done" and "failed" are distinguished by the
// installer's exit code and nothing else — a partial failure is never summarised,
// because the installer is the only thing that knows how far it got (design §7.3).
const (
	StateIdle    = "idle"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"

	// StateAbandoned is a record that says "running" while nothing is running it:
	// the process that would have recorded the outcome died first. The installer
	// is setsid'd and very probably finished — but "very probably" is not
	// something a surface may state, so this is its own state and not a guess at
	// one of the two above (design §7.3).
	//
	// It is DERIVED at read time and never written, the same discipline the
	// history record's id follows. A machine therefore recovers by being asked,
	// with no repair step and nothing to migrate.
	StateAbandoned = "abandoned"
)

// StaleAfter bounds how long a "running" record may stand once nothing can be
// found running it.
//
// It is a BACKSTOP, not the main test: a run is normally recognised as abandoned
// the moment its pid is gone. It decides only what a pid cannot — a record whose
// pid has since been handed to some other process, which a reboot makes likely,
// and a record written before the pid was recorded at all.
//
// Generous on purpose. Declaring a LIVE installer abandoned would unblock a
// second one over the top of it, and install.sh replaces binaries with mv.
const StaleAfter = 2 * time.Hour

// ErrAlreadyRunning is returned when an installer is already in flight.
var ErrAlreadyRunning = errors.New("update: an update is already running")

// Run is the record of one installer run.
type Run struct {
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	ExitCode  int       `json:"exit_code"`

	// Changed reports that the ytdl binary on disk is a DIFFERENT build from the
	// one running. It is what decides whether applying the update costs a handover
	// and a reload or nothing at all: after the installer became idempotent the
	// common update is a re-pinned yt-dlp, which needs neither (design §5).
	Changed bool `json:"changed"`

	// Version is what the installer recorded as installed, so a page that reloaded
	// onto the new binary can confirm the outcome rather than infer it.
	Version string `json:"version,omitempty"`

	// PID is the installer's own process, so a later reader can ask whether the
	// run this record describes is still happening. omitempty like every other
	// field: a record written before this existed simply has none, and falls back
	// to the clock (see Abandoned).
	PID int `json:"pid,omitempty"`
}

// Abandoned reports a run that claims to be running while nothing is running it.
//
// now and alive are parameters so the rule is a pure function of the record: the
// caller supplies the clock and the liveness test, and the rule can be tested as
// a rule.
//
// The order matters. The clock wins first, because a pid can lie in the direction
// that never resolves — a number handed to some other process would keep the
// record blocking for ever, which is the defect this exists to end. Within the
// backstop the pid decides, because it is exact.
func (r Run) Abandoned(now time.Time, alive func(int) bool) bool {
	if r.State != StateRunning {
		return false
	}
	// A record with no start time can never age out on the clock, so it is the one
	// shape that gets no benefit of the doubt.
	if r.StartedAt.IsZero() || now.Sub(r.StartedAt) >= StaleAfter {
		return true
	}
	return r.PID > 0 && !alive(r.PID)
}

// processAlive reports whether a process with this pid can be signalled. Signal 0
// runs the existence and permission checks and delivers nothing.
//
// EPERM answers "alive": the pid IS taken, by a process this user may not signal.
// That is the conservative direction — it keeps the record blocking, and
// StaleAfter is what stops it blocking for ever.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Progress is one run as a surface sees it: the record plus the tail of the log,
// which is the only honest answer to "what went wrong".
type Progress struct {
	Run
	LogTail string `json:"logTail,omitempty"`
}

// Runner launches install.sh and remembers how it went. install.sh stays the
// single thing that downloads and verifies anything (ADR-0005); this only starts
// it and reports.
type Runner struct {
	StateDir string
	Slug     string // "" = Slug()
	Branch   string // "" = Branch()

	// OnFinish, when set, is called once with the recorded outcome after each run
	// completes. It is how the daemon learns it has to hand over to the binary
	// that has just replaced it (ADR-0016 §9). It runs on the waiter goroutine, so
	// a handler that exits the process is a legitimate thing for it to do.
	OnFinish func(Run)

	mu      sync.Mutex
	running bool
}

// NewRunner builds a Runner for the given state directory.
func NewRunner(stateDir string) *Runner { return &Runner{StateDir: stateDir} }

func (r *Runner) slug() string {
	if r.Slug != "" {
		return r.Slug
	}
	return Slug()
}

func (r *Runner) branch() string {
	if r.Branch != "" {
		return r.Branch
	}
	return Branch()
}

// RunPath and LogPath are where this run's record and output live.
func (r *Runner) RunPath() string { return filepath.Join(r.StateDir, RunName) }
func (r *Runner) LogPath() string { return filepath.Join(r.StateDir, LogName) }

// Start launches install.sh detached and returns immediately.
//
// The child is setsid'd with its stdio on update.log, so it outlives the process
// that launched it — including the binary it is about to replace. The old daemon
// keeps its own open inode and therefore survives its own replacement, which is
// what lets the page be TOLD whether the update succeeded instead of watching the
// server vanish mid-operation (ADR-0016 §9).
//
// force passes --force, which reinstalls every component regardless of what looks
// current: what a retry after a failed update needs.
func (r *Runner) Start(force bool) error {
	if r.StateDir == "" {
		return errors.New("update: no state directory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return ErrAlreadyRunning
	}

	for _, tool := range []string{"curl", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("update: %s is not available", tool)
		}
	}
	if err := os.MkdirAll(r.StateDir, 0o700); err != nil {
		return err
	}
	// Truncated per run: the log answers "how did THIS update go", and a log that
	// accumulated runs would make the tail a surface shows meaningless.
	logFile, err := os.OpenFile(r.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	// Two processes joined by a pipe, exactly as run.Update() builds it — never a
	// shell string with the URL interpolated. The slug is $YTDL_REPO-controlled,
	// which is why the existing code is written this way and why this copy must be
	// too.
	url := fmt.Sprintf("%s/%s/%s/install.sh", rawBase, r.slug(), r.branch())
	curl := exec.Command("curl", "-fsSL", "--retry", "3", "--connect-timeout", "20", url)
	shArgs := []string{"-s", "--"}
	if force {
		shArgs = append(shArgs, "--force")
	}
	sh := exec.Command("bash", shArgs...)

	pipe, err := curl.StdoutPipe()
	if err != nil {
		logFile.Close()
		return err
	}
	sh.Stdin = pipe
	sh.Stdout, sh.Stderr = logFile, logFile
	curl.Stderr = logFile
	// Its own session, so the installer survives both the daemon exiting and any
	// process-group teardown aimed at it.
	detach := &syscall.SysProcAttr{Setsid: true}
	curl.SysProcAttr, sh.SysProcAttr = detach, detach

	if err := sh.Start(); err != nil {
		pipe.Close() // curl.Wait() will never run to close it; do not leak the fds
		logFile.Close()
		return err
	}
	if err := curl.Start(); err != nil {
		pipe.Close()
		_ = sh.Process.Kill()
		_, _ = sh.Process.Wait()
		logFile.Close()
		return err
	}

	started := time.Now()
	r.running = true
	// The installer's pid goes on the record, so a process that did not launch it
	// can still tell a run in flight from one nobody is left to finish. bash is
	// the pid to keep: it is the thing that runs the installer, and curl exits
	// long before it does.
	_ = SaveRun(r.StateDir, Run{State: StateRunning, StartedAt: started, PID: sh.Process.Pid})

	go func() {
		defer logFile.Close()
		curlErr := curl.Wait()
		shErr := sh.Wait()
		r.finish(started, curlErr, shErr)
	}()
	return nil
}

// finish records how the run went.
//
// If this process dies before it gets here the installer still completes — it is
// setsid'd — but the record stays "running". A later reader recognises it as
// StateAbandoned, and the surface then says it cannot tell how it went and points
// at the log. It never guesses (design §7.3).
func (r *Runner) finish(started time.Time, curlErr, shErr error) {
	run := Run{State: StateDone, StartedAt: started, EndedAt: time.Now()}
	if curlErr != nil || shErr != nil {
		run.State = StateFailed
		run.ExitCode = exitCode(shErr, curlErr)
	}

	// What the installer recorded as installed. Comparing it against the version
	// THIS process is running is how we learn whether the binary on disk changed —
	// buildinfo.Version cannot tell us, since it describes the running image and
	// the installer replaced the file underneath it.
	//
	// Read only for a run that SUCCEEDED. After a failure the marker still
	// describes the previous install, and carrying it on this record would let a
	// surface report a version for a run that installed nothing.
	if run.State == StateDone {
		if m, ok := LoadMarker(r.StateDir); ok {
			run.Version = m[markerYtdlVersion]
		}
		if run.Version != "" && run.Version != buildinfo.Version {
			run.Changed = true
		}
		// Whatever just changed, the cached verdict now describes a machine that no
		// longer exists.
		_ = Invalidate(r.StateDir)
	}
	// The record is written BEFORE the callback, so a handler that hands over and
	// exits leaves the outcome on disk for the next process — and for the page,
	// which polls the same file through whichever daemon is answering.
	//
	// It is also written before `running` is cleared, and that ordering is
	// load-bearing. Clearing the flag first opened a window — every run, ~200 µs
	// wide, because LoadMarker and Invalidate sit inside it — in which the record
	// still said "running", Running() already said false, and the installer's pid
	// had been reaped: every branch of Abandoned was then satisfied for a run that
	// had just SUCCEEDED. The invariant is "the record says running" ⇒ "the flag
	// was true", which is what Start already establishes by holding the mutex
	// across its own SaveRun (V10).
	_ = SaveRun(r.StateDir, run)
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()

	if r.OnFinish != nil {
		r.OnFinish(run)
	}
}

// exitCode digs the installer's own exit status out of the error, preferring
// bash's over curl's: bash is the thing that ran the installer, so its code is
// the one the log explains. 1 stands in for a process that never started.
func exitCode(errs ...error) int {
	for _, err := range errs {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if c := ee.ExitCode(); c > 0 {
				return c
			}
		}
	}
	for _, err := range errs {
		if err != nil {
			return 1
		}
	}
	return 0
}

// Running reports whether an installer launched by THIS process is still in
// flight. It is the in-memory half of the answer; the run file is the durable
// half, and they disagree only after a crash — which the run file's "running"
// state is exactly how a surface is told about.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Progress is what a polling page is told: the run record and the tail of the
// installer's own output.
//
// A partial failure is never summarised. What is reported is the exit code and
// the log, because the installer is the only thing that knows how far it got
// (design §7.3).
func (r *Runner) Progress() Progress {
	// Running() is read BEFORE the record, and finish writes the record BEFORE it
	// clears the flag. Together those two orderings mean a "running" record read
	// after a false flag really is somebody else's: if it were ours, the flag we
	// already read would still have been true (V10).
	ours := r.Running()
	run, ok := LoadRun(r.StateDir)
	if !ok {
		return Progress{Run: Run{State: StateIdle}}
	}
	// The in-memory half outranks the record when it has an answer: an installer
	// THIS process launched is running, whatever its pid looks like from here.
	// Otherwise the record is somebody else's, and it has to earn "running".
	if !ours && run.Abandoned(time.Now(), processAlive) {
		run.State = StateAbandoned
	}
	return Progress{Run: run, LogTail: r.logTail()}
}

// logTail returns the last logTailBytes of the installer's output, starting at a
// line boundary so the first line shown is a whole one.
func (r *Runner) logTail() string {
	f, err := os.Open(r.LogPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	size := fi.Size()
	offset := int64(0)
	if size > logTailBytes {
		offset = size - logTailBytes
	}
	buf := make([]byte, min64(size-offset, logTailBytes))
	n, _ := f.ReadAt(buf, offset)
	tail := string(buf[:n])
	if offset > 0 {
		if i := strings.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
	}
	return tail
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// LoadRun reads the run record. A missing or corrupt file is "no run", which
// renders as idle — never an error a caller has to handle.
func LoadRun(stateDir string) (Run, bool) {
	if stateDir == "" {
		return Run{}, false
	}
	data, err := os.ReadFile(filepath.Join(stateDir, RunName))
	if err != nil {
		return Run{}, false
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil || run.State == "" {
		return Run{}, false
	}
	return run, true
}

// SaveRun writes the run record atomically, so a page polling while it is written
// never reads half of one.
func SaveRun(stateDir string, run Run) error {
	if stateDir == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(stateDir, RunName+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(stateDir, RunName)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
