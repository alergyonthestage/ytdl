// Package logstore is ytdl's persistence for job outcomes (Cycle 2A, phase 4).
// It owns two levels of failure/record keeping, both outside the golden argv
// path:
//
//   - a central persistent store under log_dir (${XDG_STATE_HOME}/ytdl/logs):
//     one record per completed download, retained by age — the source of truth;
//   - an optional in-destination failure breadcrumb (breadcrumb.go), keyed by a
//     stable hash so a later success can remove it (U7 auto-cleanup).
//
// All I/O is best-effort: a logging failure must never fail the download, so
// callers may ignore the returned errors (they exist for tests and stderr
// surfacing). The package is pure of any yt-dlp/exec concern.
package logstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hashLen is the number of hex characters kept from the SHA-256 digest. 16 hex
// (64 bits) is short enough for a readable filename yet collision-free in
// practice for the handful of URLs a user retries.
const hashLen = 16

// Hash returns a short, stable, filesystem-safe key for s (16 hex chars of its
// SHA-256). It is used to key breadcrumbs by URL or playlist item id.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:hashLen]
}

// NormalizeURL canonicalises a URL so that retries of the "same" download map to
// the same Hash: it trims surrounding whitespace, drops the #fragment, and
// lowercases the scheme and host. Query parameters are preserved (for YouTube
// the ?v=/?list= identity lives there); a parse failure falls back to the
// trimmed raw string so hashing still works on odd input.
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return u.String()
}

// Job is one completed download, as recorded in the central store.
type Job struct {
	URL     string
	Mode    string // "default" | "verbose" | "silent" | "background"
	Format  string
	RC      int
	Success bool
	Time    time.Time // completion time; drives both the filename and retention
	Stderr  []byte    // captured yt-dlp stderr, written only on failure
}

// Record writes j as one file in dir (created if needed). A dir of "" disables
// the central store (e.g. $HOME unresolvable) and is a silent no-op. The file
// name is <timestamp>-<hash8>-<ok|fail>.log; the nanosecond timestamp keeps
// concurrent jobs from colliding and lets Prune sort/expire by name or mtime.
func Record(dir string, j Job) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	outcome := "fail"
	if j.Success {
		outcome = "ok"
	}
	name := fmt.Sprintf("%s-%s-%s.log", j.Time.Format("20060102T150405.000000000"), Hash(NormalizeURL(j.URL)), outcome)
	path := filepath.Join(dir, name)

	var b strings.Builder
	if j.Success {
		fmt.Fprintln(&b, "ytdl — job SUCCESS")
	} else {
		fmt.Fprintln(&b, "ytdl — job FAILED")
	}
	fmt.Fprintf(&b, "time:   %s\n", j.Time.Format("Mon Jan _2 15:04:05 2006"))
	fmt.Fprintf(&b, "url:    %s\n", j.URL)
	fmt.Fprintf(&b, "mode:   %s\n", j.Mode)
	fmt.Fprintf(&b, "format: %s\n", j.Format)
	fmt.Fprintf(&b, "rc:     %d\n", j.RC)
	if !j.Success && len(j.Stderr) > 0 {
		fmt.Fprintln(&b, "----------------------------------------")
		b.Write(j.Stderr)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// Prune removes central-store *.log files older than retentionDays (by mtime).
// retentionDays <= 0 keeps everything (no-op). A missing dir is not an error.
// Only *.log files directly in dir are considered; the pruning never recurses
// or touches anything outside the store.
func Prune(dir string, retentionDays int) error {
	if dir == "" || retentionDays <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var firstErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // vanished between ReadDir and Info; ignore
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
