package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CacheName is the cached verdict, in the state dir beside queue/, logs/,
// daemon.log and gui.token.
const CacheName = "update.json"

// DefaultTTL is how long a verdict is reused before another round is worth
// running.
//
// (design choice) 24 h. The check is startup-only — recurrence comes from the
// user starting ytdl again, never from a timer (ADR-0016 §5) — so the TTL is not
// what paces the checking. Its only job is to stop a user with ytdl in a shell
// loop from probing once per command.
const DefaultTTL = 24 * time.Hour

// CachePath is where Load and Save keep the verdict, or "" when there is no
// state dir to keep it in.
func CachePath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, CacheName)
}

// Load reads the cached verdict. A missing, unreadable or corrupt file is not an
// error a caller must handle — it is "no verdict", which every surface renders as
// "non verificato" (ADR-0016 §8). Nothing about a damaged cache should ever reach
// a user as a diagnostic: they did not ask for the check.
func Load(stateDir string) (Verdict, bool) {
	path := CachePath(stateDir)
	if path == "" {
		return Verdict{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Verdict{}, false
	}
	var v Verdict
	if err := json.Unmarshal(data, &v); err != nil {
		return Verdict{}, false
	}
	if v.CheckedAt.IsZero() {
		// A round with no time behind it cannot be aged, and every state the
		// surface shows names the date it was checked. Treat it as no verdict.
		return Verdict{}, false
	}
	return v, true
}

// Save writes the verdict atomically — a temp file in the same directory, then a
// rename — so a process dying mid-write cannot leave a half-file that reads as a
// verdict. The file is 0600 like the other things in the state dir.
func Save(stateDir string, v Verdict) error {
	path := CachePath(stateDir)
	if path == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// CreateTemp makes the file 0600, which is the mode this needs; it is in the
	// destination directory so the rename below is on one filesystem and therefore
	// atomic.
	f, err := os.CreateTemp(stateDir, CacheName+".tmp-*")
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
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Fresh reports whether the verdict is recent enough that another round would
// tell the caller nothing new.
//
// A CheckedAt in the FUTURE — a clock that moved back, a restored backup, a file
// copied from another machine — counts as stale rather than as eternally fresh.
// Re-probing costs one redirect read and heals the cache; never probing again
// does not heal, and the surface would keep quoting a date that has not happened.
func (v Verdict) Fresh(now time.Time, ttl time.Duration) bool {
	if v.CheckedAt.IsZero() {
		return false
	}
	age := now.Sub(v.CheckedAt)
	return age >= 0 && age < ttl
}
