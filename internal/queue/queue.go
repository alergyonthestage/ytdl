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

	tmpSub   = "tmp"
	lockFile = "lock"
)

// stateDirs are the four job states, plus tmp, created up front.
var stateDirs = []string{string(Pending), string(Running), string(Done), string(Failed), tmpSub}

// Job is one enqueued download. The resolved settings are snapshotted at enqueue
// time so a later config edit cannot retroactively change a queued job (ADR-0007).
type Job struct {
	URL        string          `json:"url"`
	Playlist   bool            `json:"playlist"`
	Settings   config.Settings `json:"settings"`
	EnqueuedAt time.Time       `json:"enqueued_at"`
	// Reserved for Cycle 2B-plus (retry/backoff): Attempts, LastError.
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
		n++
	}
	return n, firstErr
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
