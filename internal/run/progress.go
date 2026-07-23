package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProgressEvent is one running download's live-progress update, forwarded to the
// web GUI over SSE (Cycle 3). Percent/Speed/ETA are computed here from yt-dlp's
// RAW numeric progress fields rather than its pre-formatted `_*_str` ones: those
// carry ANSI colour whenever the user's yt-dlp config says `--color always`,
// which would silently defeat every parse.
type ProgressEvent struct {
	Status  string  // "downloading" | "finished" | "processing"
	Percent float64 // 0..100; -1 when unknown
	Speed   string  // human, e.g. "1.2MiB/s" ("" when unknown)
	ETA     string  // human, e.g. "00:12" ("" when unknown)
	Title   string  // best-effort track title
}

// ProgressSink receives progress events for one running job. yt-dlp writes
// download progress to stdout and post-processing progress to stderr, which
// os/exec copies in TWO goroutines, so a sink IS called concurrently for the same
// job — progressEmitter serialises the calls, but a sink shared across jobs must
// still be safe for concurrent use and is expected to coalesce for slow
// consumers. A nil sink disables progress capture entirely.
type ProgressSink func(ProgressEvent)

// progressSentinel marks a line yt-dlp emits from our --progress-template, so the
// stream splitter can route it to the sink instead of the failure log. It is
// deliberately unlikely to occur in real yt-dlp output.
const progressSentinel = "@@YTDLP-PROGRESS@@"

// progressFieldSep separates fields inside a sentinel line. Tab is safe: the
// numeric/status fields never contain one, and the title is emitted with the `j`
// (JSON) conversion, which escapes tabs and newlines — so a hostile video title
// can neither break the field layout nor forge a second line (see progressFields).
const progressFieldSep = "\t"

// progressFields is the exact field count of a sentinel line: sentinel, status,
// downloaded_bytes, total_bytes, total_bytes_estimate, speed, eta, title. A line
// with any other count is rejected rather than padded, so a fragment can never be
// promoted into a complete (forgeable) event.
const progressFields = 8

// progressMinInterval throttles "downloading" updates to at most one per
// interval, so a fast download does not call the sink hundreds of times a second.
// Status changes always pass, and the last suppressed frame is replayed on flush,
// so the bar can never freeze at a stale percentage.
const progressMinInterval = 200 * time.Millisecond

// maxProgressLine bounds the splitter's line buffer. yt-dlp's --newline makes
// output line-oriented, but if that ever regresses (a user config, a chatty
// external downloader) a newline-less stream must not grow the daemon's memory
// without limit; past the cap the buffer is drained to the log and reset.
const maxProgressLine = 1 << 20

// progressArgs are appended AFTER core.BuildArgs (off the golden argv path) to
// make yt-dlp emit machine-parseable progress.
//
// Two subtleties, both verified against yt-dlp 2026.07.04:
//   - --progress must come after core's --no-progress: they share one optparse
//     dest, so the LAST flag wins. Being appended after BuildArgs is what makes
//     this correct — reversing the order yields zero progress lines.
//   - the `download:` template goes to yt-dlp's STDOUT and `postprocess:` to its
//     STDERR, so both streams must be routed through a splitter.
func progressArgs() []string {
	f := func(parts ...string) string {
		return progressSentinel + progressFieldSep + strings.Join(parts, progressFieldSep)
	}
	// Raw numeric fields (colour-proof); the title uses the `j` JSON conversion so
	// newlines/tabs inside it are escaped instead of corrupting the stream.
	dl := f("%(progress.status)s",
		"%(progress.downloaded_bytes)s",
		"%(progress.total_bytes)s",
		"%(progress.total_bytes_estimate)s",
		"%(progress.speed)s",
		"%(progress.eta)s",
		"%(info.title)j")
	// Post-processing (audio extract / thumbnail embed) has no meaningful byte or
	// speed figures; emit a bare marker with the same field count.
	pp := f("processing", "", "", "", "", "", "%(info.title)j")
	return []string{
		"--progress",
		"--newline",
		"--progress-template", "download:" + dl,
		"--progress-template", "postprocess:" + pp,
	}
}

// parseProgressLine parses one sentinel line into an event. ok is false when the
// line is not one of ours or does not have exactly progressFields fields.
func parseProgressLine(line string) (ProgressEvent, bool) {
	if !strings.HasPrefix(line, progressSentinel+progressFieldSep) {
		return ProgressEvent{}, false
	}
	f := strings.Split(line, progressFieldSep)
	if len(f) != progressFields {
		return ProgressEvent{}, false
	}
	downloaded, total := parseNum(f[2]), parseNum(f[3])
	if total < 0 {
		total = parseNum(f[4]) // fall back to the estimate when the size is unknown
	}
	return ProgressEvent{
		Status:  strings.TrimSpace(f[1]),
		Percent: percentOf(downloaded, total),
		Speed:   formatSpeed(parseNum(f[5])),
		ETA:     formatETA(parseNum(f[6])),
		Title:   decodeJSONString(f[7]),
	}, true
}

// parseNum reads one raw progress number. yt-dlp prints "NA" for a field it does
// not have; anything unparseable or non-finite is treated the same way (-1).
func parseNum(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "N/A" {
		return -1
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return -1
	}
	return v
}

// percentOf computes the completion percentage, clamped to [0,100] because it
// drives a CSS width in the browser. -1 means unknown.
func percentOf(downloaded, total float64) float64 {
	if downloaded < 0 || total <= 0 {
		return -1
	}
	return math.Min(100, math.Max(0, downloaded/total*100))
}

// formatSpeed renders bytes/second the way yt-dlp's own bar does; "" when unknown.
func formatSpeed(bps float64) string {
	if bps <= 0 {
		return ""
	}
	units := []string{"B/s", "KiB/s", "MiB/s", "GiB/s"}
	i := 0
	for bps >= 1024 && i < len(units)-1 {
		bps /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%s", bps, units[i])
}

// formatETA renders seconds as mm:ss (or h:mm:ss); "" when unknown.
func formatETA(sec float64) string {
	if sec < 0 {
		return ""
	}
	s := int(sec)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, (s%3600)/60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// decodeJSONString undoes the template's `j` conversion. A value that is not
// valid JSON (a yt-dlp version without the conversion) is returned as-is, with
// any stray control characters stripped so it cannot corrupt a later consumer.
func decodeJSONString(s string) string {
	s = strings.TrimSpace(s)
	var out string
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return sanitizeLine(out)
	}
	return sanitizeLine(strings.Trim(s, `"`))
}

func sanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// progressEmitter serialises sink calls and owns the throttle. yt-dlp's download
// progress arrives on stdout and its post-processing progress on stderr, each
// copied by its own os/exec goroutine, so both splitters share ONE emitter.
type progressEmitter struct {
	mu       sync.Mutex
	sink     ProgressSink
	lastEmit time.Time
	pending  *ProgressEvent // last suppressed frame, replayed by flush
}

func newProgressEmitter(sink ProgressSink) *progressEmitter {
	return &progressEmitter{sink: sink}
}

// emit delivers ev to the sink, subject to the throttle. The sink is called
// while holding the mutex, so it is never entered concurrently for one job even
// though two os/exec goroutines feed this emitter — which is why a sink must not
// block (the GUI's does a non-blocking fan-out); blocking here would backpressure
// yt-dlp's own writes.
func (e *progressEmitter) emit(ev ProgressEvent) {
	if e == nil || e.sink == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ev.Status == "downloading" {
		now := time.Now()
		if !e.lastEmit.IsZero() && now.Sub(e.lastEmit) < progressMinInterval {
			// Suppressed: remember it so the bar cannot end up frozen at an older
			// percentage if the download stops before the next allowed frame.
			shelved := ev
			e.pending = &shelved
			return
		}
		e.lastEmit = now
	}
	e.pending = nil
	e.sink(ev)
}

// flush emits the last throttled-away frame, if any. Called once the child has
// exited, so a download that ended between frames still shows its final progress.
func (e *progressEmitter) flush() {
	if e == nil || e.sink == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ev := e.pending; ev != nil {
		e.pending = nil
		e.sink(*ev)
	}
}

// progressSplitter is one of yt-dlp's output streams when live progress is
// captured. It buffers partial writes into lines and, per complete line, routes a
// sentinel progress line to the emitter and every other line VERBATIM to w. For
// stderr, w is the failure-log file, so the .log keeps exactly the real stderr
// bytes; for stdout, w is io.Discard, matching the silent mode's /dev/null.
// os/exec writes to each splitter from a single goroutine, so it needs no lock of
// its own (the shared emitter has one).
type progressSplitter struct {
	w   io.Writer
	em  *progressEmitter
	buf []byte
}

func newProgressSplitter(w io.Writer, em *progressEmitter) *progressSplitter {
	return &progressSplitter{w: w, em: em}
}

func (p *progressSplitter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		p.handleLine(p.buf[:i])
		p.buf = p.buf[i+1:]
	}
	// A pathologically long newline-less run must not grow without bound: it
	// cannot be one of our progress lines, so hand it to the log and reset.
	if len(p.buf) > maxProgressLine {
		p.w.Write(p.buf)
		p.buf = nil
	}
	return len(b), nil
}

// handleLine routes one line (without its trailing newline). The \r trim applies
// ONLY to the sentinel test: the log gets the original bytes, so CRLF input stays
// byte-identical to a run with no progress capture.
func (p *progressSplitter) handleLine(line []byte) {
	if ev, ok := parseProgressLine(strings.TrimRight(string(line), "\r")); ok {
		p.em.emit(ev)
		return
	}
	p.w.Write(line)
	p.w.Write([]byte{'\n'})
}

// Flush routes a buffered final line that lacked a trailing newline to w, so a
// truncated last error line is not lost. A partial progress line is dropped. Call
// it after the child has exited.
func (p *progressSplitter) Flush() {
	if len(p.buf) == 0 {
		return
	}
	line := p.buf
	p.buf = nil
	if _, ok := parseProgressLine(strings.TrimRight(string(line), "\r")); ok {
		return
	}
	p.w.Write(line)
}
