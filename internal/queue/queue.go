// Package queue is ytdl's filesystem-spooled download queue (Cycle 2B). It is a
// lock-free maildir-style spool: a job is one JSON file that moves between state
// directories by atomic os.Rename, so concurrent daemon workers can claim jobs
// without locks (exactly one wins each rename; the losers get ErrNotExist).
//
//	<stateDir>/queue/
//	├── tmp/       staging — written here, then renamed into pending/ (atomic publish)
//	├── pending/   <nanos>-<hash16>-<uniq>.json  (FIFO by name)
//	├── running/   claimed jobs
//	├── done/      terminal: succeeded
//	├── failed/    terminal: failed
//	└── lock       the daemon's exclusive flock (owned by internal/daemon)
//
// The package is pure of any yt-dlp/exec concern: it moves job specs around, it
// does not run them. See ADR-0007.
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
)

// State is a spool sub-directory a job can live in.
type State string

const (
	Pending State = "pending"
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"

	tmpSub    = "tmp"
	cancelSub = "cancel"
	lockFile  = "lock"
)

// stateDirs are the four job states, plus tmp and cancel, created up front.
var stateDirs = []string{string(Pending), string(Running), string(Done), string(Failed), tmpSub, cancelSub}

// Job is one enqueued download. The resolved settings are snapshotted at enqueue
// time so a later config edit cannot retroactively change a queued job (ADR-0007).
type Job struct {
	URL      string          `json:"url"`
	Playlist bool            `json:"playlist"`
	Settings config.Settings `json:"settings"`
	// Title is the resolved "Artist - Track", filled in by the daemon (via
	// SetTitle) once yt-dlp resolves the metadata mid-run, so the queue/retry views
	// can name the video rather than only show its URL. Empty until known — and
	// deliberately left empty for a playlist job, whose per-item title would
	// misrepresent the whole job (Cycle 4). omitempty keeps legacy job files
	// (written before this field existed) parsing unchanged.
	Title      string    `json:"title,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	// Reserved for a dedicated Phase-5 cycle (retry/backoff): Attempts, LastError.
}

// Claim is a job atomically moved from pending/ to running/ by ClaimNext, ready
// for the worker to execute. ID is the spool filename stem (no ".json").
type Claim struct {
	ID  string
	Job Job
}

// Spool is a handle to one queue root (<stateDir>/queue). It is stateless beyond
// the root path, so it is safe to share across the daemon's worker goroutines.
type Spool struct{ root string }

// Open returns the spool rooted at <stateDir>/queue. stateDir is normally
// config.StatePath(); an empty stateDir yields a spool whose operations fail
// loudly (callers already guard on an unresolvable state path).
func Open(stateDir string) *Spool {
	return &Spool{root: filepath.Join(stateDir, "queue")}
}

// Root is the queue directory. LockPath is the daemon's flock file (owned by
// internal/daemon, exposed here so the layout stays in one place).
func (s *Spool) Root() string     { return s.root }
func (s *Spool) LockPath() string { return filepath.Join(s.root, lockFile) }

func (s *Spool) dir(st State) string { return filepath.Join(s.root, string(st)) }

// EnsureDirs creates the spool sub-directories. Enqueue and the daemon call it
// before use; read-only callers (List) tolerate their absence instead.
func (s *Spool) EnsureDirs() error {
	for _, d := range stateDirs {
		if err := os.MkdirAll(filepath.Join(s.root, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Enqueue writes j into pending/ and returns its id. It publishes atomically:
// the JSON is written to a unique tmp/ file, then os.Rename'd into pending/, so a
// reader (List, ClaimNext) never observes a half-written job. EnqueuedAt is
// stamped now if the caller left it zero; its nanoseconds give the FIFO filename
// prefix, and a per-file unique suffix from the staging name makes a
// same-nanosecond, same-URL collision effectively impossible.
//
// FIFO ordering assumes a non-negative UnixNano (post-1970), which every
// production caller satisfies (Enqueue stamps time.Now, or a real enqueue time);
// a pre-epoch EnqueuedAt would sort wrongly under the zero-padded prefix.
func (s *Spool) Enqueue(j Job) (string, error) {
	if err := s.EnsureDirs(); err != nil {
		return "", err
	}
	if j.EnqueuedAt.IsZero() {
		j.EnqueuedAt = time.Now()
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Join(s.root, tmpSub), "job-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: if anything below fails before the rename, don't leak
	// the staging file. After a successful rename the path no longer exists, so
	// the deferred Remove is a harmless no-op.
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	// uniq is the random middle of the CreateTemp name ("job-<uniq>.tmp"); carrying
	// it into the id avoids a racy exists-check. os.Rename would silently replace
	// a colliding destination, but a collision needs the same nanosecond AND hash
	// AND the same 32-bit random suffix — negligible in practice for one user.
	uniq := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(tmpName), "job-"), ".tmp")
	id := fmt.Sprintf("%020d-%s-%s", j.EnqueuedAt.UnixNano(), logstore.Hash(logstore.NormalizeURL(j.URL)), uniq)
	if err := os.Rename(tmpName, filepath.Join(s.dir(Pending), id+".json")); err != nil {
		return "", err
	}
	return id, nil
}

// ClaimNext atomically moves the oldest claimable pending job to running/ and
// returns it. It returns (nil, nil) when nothing is claimable. Concurrent workers
// racing for the same file are safe: os.Rename has exactly one winner and the
// losers see ErrNotExist and try the next candidate. A file that claims but fails
// to parse is quarantined to failed/ (a corrupt job must never wedge the queue)
// and the scan continues.
func (s *Spool) ClaimNext() (*Claim, error) {
	if err := s.EnsureDirs(); err != nil {
		return nil, err
	}
	ids, err := s.sortedIDs(Pending)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		src := filepath.Join(s.dir(Pending), id+".json")
		dst := filepath.Join(s.dir(Running), id+".json")
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // lost the race for this one; try the next
			}
			return nil, err
		}
		j, err := readJob(dst)
		if err != nil {
			// We own it now (in running/); park it in failed/ so it can't be
			// re-claimed forever, and keep scanning.
			_ = os.Rename(dst, filepath.Join(s.dir(Failed), id+".json"))
			continue
		}
		return &Claim{ID: id, Job: j}, nil
	}
	return nil, nil
}

// MarkDone and MarkFailed move a claimed (running) job to its terminal state.
func (s *Spool) MarkDone(id string) error   { return s.moveRunning(id, Done) }
func (s *Spool) MarkFailed(id string) error { return s.moveRunning(id, Failed) }

func (s *Spool) moveRunning(id string, to State) error {
	return os.Rename(filepath.Join(s.dir(Running), id+".json"), filepath.Join(s.dir(to), id+".json"))
}

// SetTitle records the resolved display title on a RUNNING job, so the queue and
// (once it moves to failed/) the retry view can name the video instead of only
// showing its URL. It read-modify-writes running/<id>.json via a tmp file + atomic
// os.Rename, so a concurrent reader (List from `ytdl status`/`queue`) never
// observes a half-written body. It is best-effort and idempotent:
//   - an empty title, or one already stored, is a no-op;
//   - a job no longer in running/ (already moved to a terminal state) yields
//     os.ErrNotExist, which is swallowed as nil — the title just missed the window.
//
// The daemon calls this from the worker that OWNS the running job, before the
// terminal MarkDone/MarkFailed rename, so the rewrite cannot race that move; the
// title then travels into done/ or failed/ with the file (a pure rename).
func (s *Spool) SetTitle(id, title string) error {
	if title == "" {
		return nil
	}
	path := filepath.Join(s.dir(Running), id+".json")
	j, err := readJob(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // already terminal (or never running): missed the window, not an error
		}
		return err
	}
	if j.Title == title {
		return nil
	}
	j.Title = title
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.root, tmpSub), "title-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename; cleanup on any early return
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// RequeueRunning moves every job stranded in running/ back to pending/ and
// returns how many. The daemon calls it at startup, under its exclusive lock, to
// recover jobs a previous daemon was killed mid-download (ADR-0007). Holding the
// lock guarantees no live worker owns any running/ file, so this cannot race a
// claim.
func (s *Spool) RequeueRunning() (int, error) {
	ids, err := s.sortedIDs(Running)
	if err != nil {
		return 0, err
	}
	n := 0
	var firstErr error
	for _, id := range ids {
		if err := os.Rename(filepath.Join(s.dir(Running), id+".json"), filepath.Join(s.dir(Pending), id+".json")); err != nil {
			// A vanished file (already moved on — e.g. a worker finished it) is
			// benign, exactly as in ClaimNext: skip it, don't report it. This keeps
			// requeue correct-by-construction even if a future caller ever runs it
			// alongside live workers, not just safe by the current call order.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Crash recovery is a fresh run of the id: drop a stale cancel marker left
		// by the crashed daemon, so the watcher does not delete the recovered job.
		_ = s.ClearCancel(id)
		n++
	}
	return n, firstErr
}

// cancelDir holds the cancel markers: an empty file cancel/<id> asks the daemon
// to stop the currently-running job with that id. Markers live apart from the
// job files so writing one never races the pending→running or running→terminal
// renames — the two touch different directories.
func (s *Spool) cancelDir() string { return filepath.Join(s.root, cancelSub) }

// RequestCancel drops a cancel marker for id (idempotent — re-requesting is a
// no-op). The daemon's watcher picks it up and, if that id is currently running,
// kills the job; a marker for a job that is not (or no longer) running is swept
// as stale. Safe to call in any job state: it only creates a file in cancel/.
func (s *Spool) RequestCancel(id string) error {
	if err := s.EnsureDirs(); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.cancelDir(), id), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// CancelPending removes a still-pending job outright — the cancel path for work
// that has not started. A pending cancel is a delete (ADR-0011): the job never
// ran, so there is nothing to record and no residue to clean. It returns
// (true, nil) if the job was pending and is now gone, (false, nil) if it was not
// pending (already claimed/running, or terminal), or (false, err) on a real
// error. It races the daemon's claim safely: os.Remove and the claim's
// os.Rename both target pending/<id>.json, so exactly one wins — if Remove wins
// the job never runs; if the claim wins, Remove sees ErrNotExist and the caller
// falls back to the cancel marker for the now-running job.
func (s *Spool) CancelPending(id string) (bool, error) {
	err := os.Remove(filepath.Join(s.dir(Pending), id+".json"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// CancelRequests lists the ids that currently have a cancel marker. The daemon
// polls it: an id in its in-flight set is killed, any other (stale) marker is
// cleared with ClearCancel. Reads are lock-free; a missing cancel/ dir yields no
// ids and no error.
func (s *Spool) CancelRequests() ([]string, error) {
	des, err := os.ReadDir(s.cancelDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(des))
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		ids = append(ids, de.Name())
	}
	return ids, nil
}

// ClearCancel removes a cancel marker. Best-effort: a marker that has already
// vanished is not an error.
func (s *Spool) ClearCancel(id string) error {
	if err := os.Remove(filepath.Join(s.cancelDir(), id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Retry re-enqueues a failed job: rename(failed/<id> → pending/<id>), so the
// daemon (which the caller spawns/resumes) drains it again. The job keeps its
// original id, so it sorts by its first-enqueue time and is claimed ahead of
// newer pending work — matching "retry it now". Returns os.ErrNotExist if id is
// not in failed/ (e.g. it was pruned or already retried).
//
// It clears any leftover cancel marker for the id: retry starts a FRESH run, so a
// marker from the previous (cancelled) run must not carry over — otherwise the
// daemon's watcher, seeing a marker on the now-pending id, would delete the
// just-retried job.
func (s *Spool) Retry(id string) error {
	if err := s.EnsureDirs(); err != nil {
		return err
	}
	if err := os.Rename(
		filepath.Join(s.dir(Failed), id+".json"),
		filepath.Join(s.dir(Pending), id+".json"),
	); err != nil {
		return err
	}
	_ = s.ClearCancel(id) // fresh run: drop a stale marker from the previous attempt
	return nil
}

// LiveIDs returns the ids currently in pending/ and running/, WITHOUT parsing the
// job bodies — a cheap membership check for the daemon's cancel watcher, which
// only needs each marker id's state, not its Job. A missing dir yields no ids.
func (s *Spool) LiveIDs() (pending, running []string, err error) {
	if pending, err = s.sortedIDs(Pending); err != nil {
		return nil, nil, err
	}
	if running, err = s.sortedIDs(Running); err != nil {
		return nil, nil, err
	}
	return pending, running, nil
}

// PruneTerminal removes done/ and failed/ job files older than maxAgeDays (by
// mtime), bounding the spool's terminal growth. The durable job history lives in
// the log store, so terminal spool files are only recent operational state
// (ADR-0009); this is the fix for their previously-unbounded accumulation.
// maxAgeDays <= 0 keeps everything (no-op), matching the log store's
// log_retention_days semantics. Best-effort: a vanished file or a per-file remove
// error does not abort the sweep; the first error (if any) is returned.
func (s *Spool) PruneTerminal(maxAgeDays int) error {
	if maxAgeDays <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	var firstErr error
	for _, st := range []State{Done, Failed} {
		des, err := os.ReadDir(s.dir(st))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, de := range des {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue // vanished between ReadDir and Info; ignore
			}
			if info.ModTime().Before(cutoff) {
				if err := os.Remove(filepath.Join(s.dir(st), de.Name())); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

// Entry is one job as seen by List: its id, state, and parsed spec. Job is the
// zero value if the file could not be read (surfaced, never fatal).
type Entry struct {
	ID    string
	State State
	Job   Job
}

// Snapshot is a point-in-time view of the spool for `ytdl queue`/`status`. Reads
// are lock-free; a job moving between dirs mid-scan may appear in zero or two
// slices for that instant, which is acceptable for a status display.
type Snapshot struct {
	Pending []Entry
	Running []Entry
	Done    []Entry
	Failed  []Entry
}

// Counts returns the size of each state slice.
func (s Snapshot) Counts() (pending, running, done, failed int) {
	return len(s.Pending), len(s.Running), len(s.Done), len(s.Failed)
}

// List reads the whole spool into a Snapshot, each slice sorted FIFO by id. A
// missing spool (nothing ever enqueued) is not an error: it yields an empty
// Snapshot.
func (s *Spool) List() (Snapshot, error) {
	var snap Snapshot
	for _, st := range []State{Pending, Running, Done, Failed} {
		entries, err := s.entries(st)
		if err != nil {
			return snap, err
		}
		switch st {
		case Pending:
			snap.Pending = entries
		case Running:
			snap.Running = entries
		case Done:
			snap.Done = entries
		case Failed:
			snap.Failed = entries
		}
	}
	return snap, nil
}

func (s *Spool) entries(st State) ([]Entry, error) {
	ids, err := s.sortedIDs(st)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		e := Entry{ID: id, State: st}
		if j, err := readJob(filepath.Join(s.dir(st), id+".json")); err == nil {
			e.Job = j
		}
		out = append(out, e)
	}
	return out, nil
}

// sortedIDs returns the ".json" file stems in a state dir, ascending (FIFO by the
// zero-padded nanosecond prefix). A missing dir yields no ids and no error.
func (s *Spool) sortedIDs(st State) ([]string, error) {
	des, err := os.ReadDir(s.dir(st))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(des))
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(de.Name(), ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

func readJob(path string) (Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return Job{}, err
	}
	return j, nil
}
