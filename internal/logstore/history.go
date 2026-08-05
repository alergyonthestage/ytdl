package logstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	// Cycle 5. All omitempty: a record written by an older ytdl simply has none
	// of them, and the actions that need them are offered as unavailable rather
	// than migrated (design §5.2). Nothing here is ever trusted as a path to
	// act on without revalidation — see the open/reveal guards (design §7).
	Path     string `json:"path,omitempty"`     // absolute path of the first saved file
	Dir      string `json:"dir,omitempty"`      // the job's resolved output directory
	Count    int    `json:"count,omitempty"`    // how many files the job saved
	Playlist bool   `json:"playlist,omitempty"` // needed to reproduce the job
	Error    string `json:"error,omitempty"`    // one-line, capped failure reason
}

// ID is the stable handle both channels use to reference a record: 16 hex chars
// of the hash of the record's own timestamp and URL. It is DERIVED rather than
// stored, so records written before Cycle 5 are addressable too and no migration
// is needed (design §5.3).
//
// Stability across a marshal/unmarshal cycle rests on RFC3339Nano being exactly
// what encoding/json writes for a time.Time: the string that goes into the file
// is the string that comes back out, monotonic reading and Location naming
// dropped by both. Two records for the same URL differ as soon as their
// nanosecond timestamps do.
func (e Entry) ID() string {
	return Hash(e.Time.Format(time.RFC3339Nano) + "|" + e.URL)
}

// LogName is the name of the per-job .log Record wrote for the same job, so a
// history record can point at its own failure detail with no stored path. The
// file may legitimately be gone (pruned by retention, or never written because
// log_dir was unset); a caller must treat a missing file as "detail
// unavailable", not as an error.
func (e Entry) LogName() string { return logName(e.Time, e.URL, e.Success) }

// reasonMaxRunes caps Entry.Error. It exists to keep one history line small and
// one UI row readable: the full detail always stays in the per-job .log.
const reasonMaxRunes = 200

// capReason turns a raw stderr line into the one-line reason stored on a record:
// ANSI escape sequences removed, remaining control characters folded to spaces,
// whitespace runs collapsed, and the result truncated to reasonMaxRunes. yt-dlp
// colourises its errors when it thinks it has a terminal, and a stored escape
// sequence would be replayed verbatim into a terminal or an HTML page later.
func capReason(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b { // ESC: skip the whole escape sequence, not just the ESC byte
			i = skipEscape(runes, i)
			continue
		}
		// C0, DEL, and the C1 block. C1 matters as much as C0 here: U+009B is
		// CSI and U+009D is OSC in their 8-bit forms, and an xterm-family
		// terminal in UTF-8 mode honours them — so leaving them in would replay
		// exactly the control sequence this function exists to remove.
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			b.WriteByte(' ') // fold to a space so words are not glued together
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > reasonMaxRunes {
		out = strings.TrimRight(string(r[:reasonMaxRunes-1]), " ") + "…"
	}
	return out
}

// skipEscape returns the index of the last rune of the escape sequence starting
// at runes[i] (an ESC), so the caller's loop resumes after it. CSI sequences
// (ESC '[' … final byte in @-~) and OSC strings (ESC ']' … BEL or ESC '\') are
// recognised; anything else consumes just the ESC and its single next rune. An
// unterminated sequence consumes the rest of the string, which is the safe
// direction: the alternative is emitting its tail as text.
func skipEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}
	switch runes[i+1] {
	case '[':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] >= '@' && runes[j] <= '~' {
				return j
			}
		}
		return len(runes)
	case ']':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] == 0x07 {
				return j
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 1
			}
		}
		return len(runes)
	default:
		return i + 1
	}
}

// QueryOpts filters and bounds a Load. The zero value returns every record,
// newest first. Filters are applied first, then the newest-first ordering, then
// Offset and Limit — so paging a filtered set is stable.
type QueryOpts struct {
	Since      time.Time // only records at or after this time (zero = no lower bound)
	Limit      int       // at most this many, newest first (<= 0 = no limit)
	Offset     int       // skip this many of the newest matches first (<= 0 = none)
	OnlyFailed bool      // only failures
	// OnlyOK is the complement of OnlyFailed: only successes. Both set is a
	// contradiction and yields nothing, which is what the caller asked for.
	OnlyOK bool
	Search string // case-insensitive substring of title, URL or saved file name ("" = no filter)
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
		// Title comes from yt-dlp's print of the video's own metadata, so it is
		// remote-controlled text exactly like the failure reason — and since
		// Cycle 5 it is rendered in a terminal TABLE and in an HTML page. An
		// escape sequence in a video title would clear the user's screen and
		// wreck the column alignment (DisplayWidth counts escape bytes as
		// printable); an unbounded one would make a single history line
		// megabytes, which every later Load then parses.
		Time: j.Time, URL: j.URL, Title: capReason(j.Title),
		Mode: j.Mode, Format: j.Format, RC: j.RC, Success: j.Success,
		Path: j.Path, Dir: j.Dir, Count: j.Count, Playlist: j.Playlist,
		Error: capReason(j.Error),
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

	needle := strings.ToLower(strings.TrimSpace(opts.Search))
	var out []Entry
	for _, line := range readLines(f) {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip a malformed line
		}
		// A bare `null` unmarshals into the zero Entry without error, which the
		// UI would render as a failed download with no title and no link. A
		// record with neither a time nor a URL is not a record.
		if e.Time.IsZero() && e.URL == "" {
			continue
		}
		if opts.OnlyFailed && e.Success {
			continue
		}
		if opts.OnlyOK && !e.Success {
			continue
		}
		if !opts.Since.IsZero() && e.Time.Before(opts.Since) {
			continue
		}
		if needle != "" && !e.matches(needle) {
			continue
		}
		out = append(out, e)
	}
	// Newest first; stable so equal timestamps keep their append order.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if opts.Offset > 0 {
		if opts.Offset >= len(out) {
			return nil, nil
		}
		out = out[opts.Offset:]
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// matches reports whether the record satisfies an already-lowercased search
// needle. It searches the three handles a user actually remembers a download by:
// the resolved title, the link, and the saved file's name. The file's *directory*
// is deliberately excluded — every record shares it, so including it would make
// a search for the download folder match everything.
func (e Entry) matches(lowerNeedle string) bool {
	if strings.Contains(strings.ToLower(e.Title), lowerNeedle) {
		return true
	}
	if strings.Contains(strings.ToLower(e.URL), lowerNeedle) {
		return true
	}
	return e.Path != "" && strings.Contains(strings.ToLower(filepath.Base(e.Path)), lowerNeedle)
}

// ErrNotFound is returned by Find when no record has the requested id.
var ErrNotFound = errors.New("logstore: no history record with that id")

// ErrAmbiguous is returned by Find when a partial id matches several records.
var ErrAmbiguous = errors.New("logstore: id prefix matches several records")

// Find resolves a record by its ID, accepting either the full 16-hex id or an
// unambiguous prefix of it. Both channels resolve targets through this one
// function so that `ytdl open 3f2a` and the GUI's row button cannot drift apart
// (design §9.2). An exact match always wins over prefix matching, so a caller
// holding a full id — every API caller does — is never told "ambiguous".
func Find(dir, id string) (Entry, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if dir == "" || id == "" {
		return Entry{}, ErrNotFound
	}
	entries, err := Load(dir, QueryOpts{})
	if err != nil {
		return Entry{}, err
	}
	var partial []Entry
	for _, e := range entries {
		switch eid := e.ID(); {
		case eid == id:
			return e, nil
		case strings.HasPrefix(eid, id):
			partial = append(partial, e)
		}
	}
	switch len(partial) {
	case 0:
		return Entry{}, ErrNotFound
	case 1:
		return partial[0], nil
	default:
		return Entry{}, ErrAmbiguous
	}
}

// TitleForURL returns the most recently recorded resolved title for url in dir, or
// "" if none. It is a single-lookup convenience over Load + TitleIn; a caller
// enriching many jobs should Load once per dir and use TitleIn to avoid re-scanning
// the whole history per lookup. Best-effort: a missing/unreadable store yields "".
func TitleForURL(dir, url string) string {
	if dir == "" || url == "" {
		return ""
	}
	entries, err := Load(dir, QueryOpts{})
	if err != nil {
		return ""
	}
	return TitleIn(entries, url)
}

// TitleIn returns the most recently recorded title for url within already-loaded
// history entries, or "" if none. It matches on the same normalized-URL identity
// that keys breadcrumbs and spool ids, so a title captured on an earlier attempt
// (foreground or background) can name a failed queued job in `ytdl retry` even
// before the run-time write-back ever ran for it. Load returns entries newest-first,
// so the first matching non-empty title is the latest.
func TitleIn(entries []Entry, url string) string {
	if url == "" {
		return ""
	}
	target := NormalizeURL(url)
	for _, e := range entries {
		if e.Title != "" && NormalizeURL(e.URL) == target {
			return e.Title
		}
	}
	return ""
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
		// A record with no time is not "infinitely old": it is a line this
		// version does not understand, and the doc promise is that such a line
		// is preserved, never dropped by a parse outcome.
		if err := json.Unmarshal(line, &e); err == nil && !e.Time.IsZero() && e.Time.Before(cutoff) {
			dropped++
			continue
		}
		kept = append(kept, line) // in-window, timeless or malformed: keep verbatim
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
