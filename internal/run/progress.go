package run

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"time"
)

// ProgressEvent is one live-progress update for a running download, forwarded to
// the web GUI over SSE (Cycle 3). It is produced by parsing yt-dlp's
// --progress-template output; the human-formatted Speed/ETA are passed through
// verbatim for display, while Percent is parsed to a number for a progress bar.
type ProgressEvent struct {
	Status  string  // "downloading" | "finished" | "processing"
	Percent float64 // 0..100; -1 when unknown
	Speed   string  // e.g. "1.20MiB/s" ("" when unknown)
	ETA     string  // e.g. "00:12" ("" when unknown)
	Title   string  // best-effort track title
}

// ProgressSink receives progress events for one running job. It is called from
// yt-dlp's stderr-reading goroutine (os/exec serialises those writes, so the
// sink is never called concurrently for the SAME job) and may fire often; a sink
// shared across jobs must be safe for concurrent use and is expected to coalesce
// for slow consumers. A nil sink disables progress capture entirely.
type ProgressSink func(ProgressEvent)

// progressSentinel marks a line yt-dlp emits from our --progress-template, so the
// stderr splitter can route it to the sink instead of the failure log. It is
// deliberately unlikely to occur in real yt-dlp error text.
const progressSentinel = "@@YTDLP-PROGRESS@@"

// progressFieldSep separates fields inside a sentinel line. Tab is safe: none of
// the status/percent/speed/eta fields contain it, and the title (always last)
// may keep stray tabs without corrupting the earlier fields.
const progressFieldSep = "\t"

// progressMinInterval throttles "downloading" updates to at most one per
// interval, so a fast download does not call the sink hundreds of times a
// second. Status changes (finished/processing) always pass, so the bar still
// reaches 100% and the post-processing hint is never dropped.
const progressMinInterval = 200 * time.Millisecond

// progressArgs are appended AFTER core.BuildArgs (off the golden argv path) to
// make yt-dlp emit machine-parseable progress on stderr. --progress forces the
// output even though stderr is a pipe (not a TTY); --newline prints each update
// on its own line (no \r bar); the two templates cover the download and the
// post-processing (audio extract / thumbnail embed) phases.
func progressArgs() []string {
	dl := progressSentinel + progressFieldSep + "%(progress.status)s" +
		progressFieldSep + "%(progress._percent_str)s" +
		progressFieldSep + "%(progress._speed_str)s" +
		progressFieldSep + "%(progress._eta_str)s" +
		progressFieldSep + "%(info.title)s"
	// Post-processing has no meaningful percent/speed/eta; emit a bare
	// "processing" marker (empty numeric fields) plus the title.
	pp := progressSentinel + progressFieldSep + "processing" +
		progressFieldSep + progressFieldSep + progressFieldSep +
		progressFieldSep + "%(info.title)s"
	return []string{
		"--progress",
		"--newline",
		"--progress-template", "download:" + dl,
		"--progress-template", "postprocess:" + pp,
	}
}

// parseProgressLine parses one sentinel line into an event. ok is false when the
// line is not one of our progress lines (real yt-dlp stderr).
func parseProgressLine(line string) (ProgressEvent, bool) {
	if !strings.HasPrefix(line, progressSentinel+progressFieldSep) {
		return ProgressEvent{}, false
	}
	// 6 fields: sentinel, status, percent, speed, eta, title. SplitN keeps any
	// tabs in the title (the 6th piece) intact.
	f := strings.SplitN(line, progressFieldSep, 6)
	for len(f) < 6 {
		f = append(f, "") // tolerate a short line (missing trailing fields)
	}
	return ProgressEvent{
		Status:  strings.TrimSpace(f[1]),
		Percent: parsePercent(f[2]),
		Speed:   strings.TrimSpace(f[3]),
		ETA:     strings.TrimSpace(f[4]),
		Title:   strings.TrimSpace(f[5]),
	}, true
}

// parsePercent turns yt-dlp's _percent_str (e.g. " 42.3%", "100.0%", "NA") into
// a number, returning -1 when it is blank or non-numeric.
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" || s == "NA" {
		return -1
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1
	}
	return v
}

// progressStderr is yt-dlp's stderr sink when live progress is captured. It
// buffers partial writes into lines and, per complete line, routes a sentinel
// progress line to the sink (throttled) and every other line to the failure log
// w — so the .log stays free of progress spam while the GUI streams progress.
// os/exec writes to it from a single goroutine, so it needs no locking.
type progressStderr struct {
	w        io.Writer // the failure-log file (the real stderr the .log keeps)
	sink     ProgressSink
	buf      []byte
	lastEmit time.Time
}

func newProgressStderr(w io.Writer, sink ProgressSink) *progressStderr {
	return &progressStderr{w: w, sink: sink}
}

func (p *progressStderr) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := string(p.buf[:i])
		p.buf = p.buf[i+1:]
		p.handleLine(line)
	}
	return len(b), nil
}

func (p *progressStderr) handleLine(line string) {
	line = strings.TrimRight(line, "\r")
	if ev, ok := parseProgressLine(line); ok {
		p.emit(ev)
		return
	}
	io.WriteString(p.w, line+"\n") // real stderr → failure log, newline restored
}

func (p *progressStderr) emit(ev ProgressEvent) {
	if p.sink == nil {
		return
	}
	if ev.Status == "downloading" {
		now := time.Now()
		if !p.lastEmit.IsZero() && now.Sub(p.lastEmit) < progressMinInterval {
			return
		}
		p.lastEmit = now
	}
	p.sink(ev)
}

// Flush routes any buffered final line that lacked a trailing newline to the
// failure log, so a truncated last error line is not lost. A partial progress
// line is dropped (it is not log-worthy). Call it after the child has exited.
func (p *progressStderr) Flush() {
	if len(p.buf) == 0 {
		return
	}
	line := strings.TrimRight(string(p.buf), "\r")
	p.buf = nil
	if _, ok := parseProgressLine(line); ok {
		return
	}
	io.WriteString(p.w, line)
}
