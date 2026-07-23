package logstore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// breadcrumbMarker is the first line of every breadcrumb. RemoveBreadcrumb only
// deletes files that begin with it, so a name collision with a user's own file
// can never cause data loss.
const breadcrumbMarker = "⚠ ytdl — DOWNLOAD FALLITO"

// breadcrumbSuffix is the ".<key>.log" tail used both to name a breadcrumb and
// to glob for it during cleanup.
func breadcrumbSuffix(key string) string { return "." + key + ".log" }

// WriteBreadcrumb drops a small, human-readable failure marker into outDir,
// named "<title> — FALLITO.<key>.log". The readable title preserves the
// at-a-glance discovery the Bash tool's <title>.log gave a CLI user; the <key>
// suffix (a Hash of the URL or playlist item id) gives the stable identity that
// fixes the title-collision hazard (M2) and lets a later success find and
// remove it (U7 auto-cleanup). The body is deliberately minimal — the full
// stderr lives in the central store.
func WriteBreadcrumb(outDir, title, key, srcURL string, rc int, when time.Time) error {
	label := sanitizeTitle(title)
	if label == "" {
		label = "ytdl-failed"
	}
	name := label + " — FALLITO" + breadcrumbSuffix(key)
	path := filepath.Join(outDir, name)

	var b strings.Builder
	fmt.Fprintln(&b, breadcrumbMarker)
	fmt.Fprintf(&b, "quando: %s\n", when.Format("Mon Jan _2 15:04:05 2006"))
	fmt.Fprintf(&b, "url:    %s\n", srcURL)
	fmt.Fprintf(&b, "rc:     %d\n", rc)
	fmt.Fprintln(&b, "Dettagli completi nel log store centrale di ytdl (~/.local/state/ytdl/logs).")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// RemoveBreadcrumb deletes any breadcrumb in outDir keyed by key. It scans the
// directory for names ending in ".<key>.log" and, defensively, removes only
// files whose first line is the breadcrumb marker — so a same-shaped user file
// is never touched. A missing directory is not an error.
//
// It deliberately does NOT use filepath.Glob: Glob treats the whole joined path
// as a pattern, so an output folder whose name contains glob metacharacters
// (e.g. "80s Hits [Remastered]") would make the pattern match nothing and
// silently break U7 auto-cleanup. os.ReadDir + a literal suffix match is immune.
func RemoveBreadcrumb(outDir, key string) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	suffix := breadcrumbSuffix(key)
	var firstErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		full := filepath.Join(outDir, e.Name())
		if !isBreadcrumb(full) {
			continue
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// isBreadcrumb reports whether path is one of our breadcrumbs (first line is the
// marker), guarding RemoveBreadcrumb against deleting a user's file that merely
// shares the "*.<key>.log" shape.
func isBreadcrumb(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(breadcrumbMarker))
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n]) == breadcrumbMarker
}

// sanitizeTitle maps the parity `/` and `:` to `_`, replaces any remaining
// filesystem-hostile characters, and trims leading dots and surrounding space —
// the same rule the Bash tool's <title>.log naming used (C5), now applied to
// breadcrumb names.
func sanitizeTitle(s string) string {
	s = strings.NewReplacer("/", "_", ":", "_").Replace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20: // control characters, incl. NUL and newline
			b.WriteRune('_')
		case strings.ContainsRune(`\*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(strings.TrimSpace(b.String()), ".")
}
