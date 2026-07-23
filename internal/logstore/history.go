package logstore

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// historyFile is the append-only structured history stream, one JSON record per
// line, beside the per-job .log files in the central store. Every job (foreground
// and background) appends one, so the log store — not the background-only spool —
// is the durable source for `ytdl history` and the `ytdl status` recent summary
// (ADR-0009).
const historyFile = "history.jsonl"

// Entry is one history record as stored in history.jsonl and returned by Load:
// the structured, machine-readable counterpart of the human .log file.
type Entry struct {
	Time    time.Time `json:"time"`
	URL     string    `json:"url"`
	Title   string    `json:"title,omitempty"`
	Mode    string    `json:"mode"`
	Format  string    `json:"format"`
	RC      int       `json:"rc"`
	Success bool      `json:"success"`
}

// QueryOpts filters and bounds a Load. The zero value returns every record,
// newest first.
type QueryOpts struct {
	Since      time.Time // only records at or after this time (zero = no lower bound)
	Limit      int       // at most this many, newest first (<= 0 = no limit)
	OnlyFailed bool      // only failures
}

// Append writes j as one JSON line to <dir>/history.jsonl, creating the dir and
// file if needed. Best-effort like Record: a dir of "" is a no-op and the caller
// may ignore the error. Concurrent appends from several processes (a foreground
// ytdl and daemon workers) are safe: each record is a single small O_APPEND
// write, which the kernel does not interleave on a local filesystem (verified by
// a concurrent stress test).
func Append(dir string, j Job) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(Entry{
		Time: j.Time, URL: j.URL, Title: j.Title,
		Mode: j.Mode, Format: j.Format, RC: j.RC, Success: j.Success,
	})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(filepath.Join(dir, historyFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load reads history.jsonl and returns the matching records, newest first. A
// missing file (nothing recorded yet) yields no records and no error. Malformed
// lines are skipped, not fatal — a partially-written or hand-edited file must not
// break `ytdl history`.
func Load(dir string, opts QueryOpts) ([]Entry, error) {
	if dir == "" {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(dir, historyFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Entry
	for _, line := range readLines(f) {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip a malformed line
		}
		if opts.OnlyFailed && e.Success {
			continue
		}
		if !opts.Since.IsZero() && e.Time.Before(opts.Since) {
			continue
		}
		out = append(out, e)
	}
	// Newest first; stable so equal timestamps keep their append order.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// pruneHistory rewrites history.jsonl keeping only records newer than the
// retention cutoff (called by Prune). Best-effort and lock-free like the rest of
// the package: read, filter, write a temp file, rename into place. It rewrites
// only when something actually expires, so the common case touches nothing. A
// concurrent Append during the rewrite could be lost (it lands in the file the
// rename replaces); acceptable because the per-job .log remains and history is
// best-effort. Malformed lines are preserved (never silently dropped by a parse
// failure).
func pruneHistory(dir string, retentionDays int) error {
	if dir == "" || retentionDays <= 0 {
		return nil
	}
	path := filepath.Join(dir, historyFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var kept [][]byte
	dropped := 0
	for _, line := range readLines(f) {
		var e Entry
		if err := json.Unmarshal(line, &e); err == nil && e.Time.Before(cutoff) {
			dropped++
			continue
		}
		kept = append(kept, line) // in-window or malformed: keep verbatim
	}
	f.Close()
	if dropped == 0 {
		return nil // nothing expired; don't rewrite (and don't needlessly race an append)
	}

	tmp, err := os.CreateTemp(dir, historyFile+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	w := bufio.NewWriter(tmp)
	for _, line := range kept {
		w.Write(line)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// readLines reads f line by line with a bufio.Reader (deliberately NOT a
// bufio.Scanner): a Scanner aborts the whole read on a single token longer than
// its buffer, which would make one pathological line permanently break `ytdl
// history` AND block pruneHistory's self-repair. ReadBytes has no such cap — an
// oversize line comes back like any other, to be JSON-parsed and, if malformed,
// skipped. Each returned line has its trailing newline stripped; empty lines are
// dropped. A read error just stops the scan with whatever was read so far.
func readLines(f *os.File) [][]byte {
	var lines [][]byte
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if n := len(line); n > 0 {
			if line[n-1] == '\n' {
				line = line[:n-1]
			}
			if len(line) > 0 {
				lines = append(lines, line) // ReadBytes returns a fresh slice each call
			}
		}
		if err != nil {
			break // io.EOF (incl. a final newline-less line, already handled above) or a read error
		}
	}
	return lines
}
