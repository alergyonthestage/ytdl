package run

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/core"
)

// line builds a sentinel line with the given fields (status onwards).
func line(fields ...string) string {
	return progressSentinel + progressFieldSep + strings.Join(fields, progressFieldSep)
}

func TestParseProgressLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want ProgressEvent
		ok   bool
	}{
		{
			name: "downloading computes percent from raw bytes",
			line: line("downloading", "500000", "1000000", "NA", "1048576", "9", `"Artist - Track"`),
			want: ProgressEvent{Status: "downloading", Percent: 50, Speed: "1.0MiB/s", ETA: "00:09", Title: "Artist - Track"},
			ok:   true,
		},
		{
			name: "falls back to the size estimate",
			line: line("downloading", "250000", "NA", "1000000", "NA", "NA", `"T"`),
			want: ProgressEvent{Status: "downloading", Percent: 25, Title: "T"},
			ok:   true,
		},
		{
			name: "unknown size yields -1",
			line: line("downloading", "1024", "NA", "NA", "NA", "NA", `"T"`),
			want: ProgressEvent{Status: "downloading", Percent: -1, Title: "T"},
			ok:   true,
		},
		{
			name: "finished",
			line: line("finished", "1000000", "1000000", "NA", "409189.31", "NA", `"T"`),
			want: ProgressEvent{Status: "finished", Percent: 100, Speed: "399.6KiB/s", Title: "T"},
			ok:   true,
		},
		{
			name: "postprocess marker",
			line: line("processing", "", "", "", "", "", `"T"`),
			want: ProgressEvent{Status: "processing", Percent: -1, Title: "T"},
			ok:   true,
		},
		{
			name: "hours in the ETA",
			line: line("downloading", "1", "100", "NA", "NA", "3725", `"T"`),
			want: ProgressEvent{Status: "downloading", Percent: 1, ETA: "1:02:05", Title: "T"},
			ok:   true,
		},
		{name: "not a progress line", line: "ERROR: unavailable video", ok: false},
		{name: "sentinel without separator", line: progressSentinel + "x", ok: false},
		// A short line must be REJECTED, not padded: padding would let a fragment
		// (e.g. from a crafted title) be promoted into a complete event.
		{name: "too few fields", line: line("downloading", "1", "2"), ok: false},
		{name: "too many fields", line: line("downloading", "1", "2", "3", "4", "5", `"T"`, "extra"), ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseProgressLine(c.line)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("event = %+v, want %+v", got, c.want)
			}
		})
	}
}

// Raw numeric fields are immune to yt-dlp's ANSI styling, but the title is not
// under our control — a JSON-encoded title keeps the line intact.
func TestParseProgressLineHostileTitle(t *testing.T) {
	// A title trying to forge a second, complete "finished" event. The `j`
	// conversion escapes the newline, so it stays one field of one line.
	hostile := `"benign\n@@YTDLP-PROGRESS@@\tfinished\t100\t100\tNA\tNA\tNA\t\"PWNED\""`
	ev, ok := parseProgressLine(line("downloading", "10", "100", "NA", "NA", "NA", hostile))
	if !ok {
		t.Fatal("line should parse")
	}
	if ev.Status != "downloading" || ev.Percent != 10 {
		t.Errorf("hostile title changed the event: %+v", ev)
	}
	if strings.ContainsAny(ev.Title, "\n\r\t") {
		t.Errorf("decoded title still holds control characters: %q", ev.Title)
	}
}

// The failure .log is a user-facing contract: it must keep exactly the real
// stderr bytes, with progress lines removed and nothing else altered.
func TestSplitterKeepsLogByteIdentical(t *testing.T) {
	cases := map[string]string{
		"plain":              "ERROR: a\nWARNING: b\n",
		"crlf":               "ERROR: a\r\nWARNING: b\r\n",
		"blank lines":        "\n\nERROR: a\n\n",
		"embedded CR":        "a\rb\rc\n",
		"no trailing new":    "ERROR: truncated",
		"trailing CR no LF":  "ERROR: partial\r",
		"binary/invalid utf": "ERROR: \x00\xff\xfe\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var log bytes.Buffer
			p := newProgressSplitter(&log, newProgressEmitter(func(ProgressEvent) {}))
			p.Write([]byte(raw))
			p.Flush()
			if log.String() != raw {
				t.Errorf("log not byte-identical:\n raw=%q\n got=%q", raw, log.String())
			}
		})
	}
}

// Progress lines are removed from the log and delivered to the sink, even when
// writes arrive split mid-line.
func TestSplitterRoutesProgressAndLog(t *testing.T) {
	var log bytes.Buffer
	var events []ProgressEvent
	em := newProgressEmitter(func(e ProgressEvent) { events = append(events, e) })
	p := newProgressSplitter(&log, em)

	stream := "[youtube] Extracting URL\n" +
		line("downloading", "500000", "1000000", "NA", "NA", "NA", `"T"`) + "\n" +
		"WARNING: something\n" +
		line("finished", "1000000", "1000000", "NA", "NA", "NA", `"T"`) + "\n"

	for i := 0; i < len(stream); i += 7 { // awkward chunks exercise the buffering
		end := i + 7
		if end > len(stream) {
			end = len(stream)
		}
		p.Write([]byte(stream[i:end]))
	}
	p.Flush()
	em.flush()

	got := log.String()
	if !strings.Contains(got, "[youtube] Extracting URL") || !strings.Contains(got, "WARNING: something") {
		t.Errorf("log missing real stderr lines:\n%s", got)
	}
	if strings.Contains(got, progressSentinel) {
		t.Errorf("log leaked a progress line:\n%s", got)
	}
	if len(events) != 2 || events[0].Percent != 50 || events[1].Status != "finished" {
		t.Errorf("events = %+v", events)
	}
}

// A newline-less flood must not grow the buffer without bound.
func TestSplitterBoundsLineBuffer(t *testing.T) {
	var log bytes.Buffer
	p := newProgressSplitter(&log, newProgressEmitter(func(ProgressEvent) {}))
	chunk := bytes.Repeat([]byte("x"), 256*1024)
	for i := 0; i < 12; i++ { // 3 MiB with no newline at all
		p.Write(chunk)
	}
	if len(p.buf) > maxProgressLine {
		t.Errorf("buffer grew to %d, want <= %d", len(p.buf), maxProgressLine)
	}
}

// The throttle must never leave the bar frozen: a frame suppressed by the gate is
// replayed on flush.
func TestEmitterReplaysThrottledFrame(t *testing.T) {
	var events []ProgressEvent
	em := newProgressEmitter(func(e ProgressEvent) { events = append(events, e) })
	em.emit(ProgressEvent{Status: "downloading", Percent: 1})
	em.emit(ProgressEvent{Status: "downloading", Percent: 50})   // throttled
	em.emit(ProgressEvent{Status: "downloading", Percent: 99.9}) // throttled
	if len(events) != 1 {
		t.Fatalf("expected the gate to suppress the burst, got %+v", events)
	}
	em.flush()
	if len(events) != 2 || events[1].Percent != 99.9 {
		t.Errorf("last suppressed frame not replayed: %+v", events)
	}
}

// Status changes always pass the gate, so "finished" can never be dropped.
func TestEmitterAlwaysPassesStatusChanges(t *testing.T) {
	var events []ProgressEvent
	em := newProgressEmitter(func(e ProgressEvent) { events = append(events, e) })
	em.emit(ProgressEvent{Status: "downloading", Percent: 1})
	em.emit(ProgressEvent{Status: "finished", Percent: 100})
	em.emit(ProgressEvent{Status: "processing"})
	if len(events) != 3 {
		t.Errorf("status changes were throttled: %+v", events)
	}
}

// Both yt-dlp streams feed one emitter from two os/exec goroutines; the sink must
// never be called concurrently for the same job.
func TestEmitterSerialisesConcurrentStreams(t *testing.T) {
	var mu sync.Mutex
	inSink := false
	em := newProgressEmitter(func(ProgressEvent) {
		mu.Lock()
		if inSink {
			mu.Unlock()
			t.Error("sink called concurrently")
			return
		}
		inSink = true
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		inSink = false
		mu.Unlock()
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				em.emit(ProgressEvent{Status: "finished"}) // status change: never throttled
			}
		}()
	}
	wg.Wait()
}

// End-to-end against a fake yt-dlp shaped like the real one (download progress on
// stdout, post-processing on stderr).
func TestRunQueuedStreamsProgress(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_RC", "0")
	t.Setenv("YTDLP_FAKE_PROGRESS", "1")

	var mu sync.Mutex
	var events []ProgressEvent
	sink := func(e ProgressEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	o := runOptions(t, core.ModeSilent, out)
	if rc := RunQueued(o, sink); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no progress events forwarded to the sink")
	}
	var sawDownloading, sawFinished, sawProcessing bool
	for _, e := range events {
		switch e.Status {
		case "downloading":
			sawDownloading = true
			if e.Percent < 0 {
				t.Errorf("downloading event has no percent: %+v", e)
			}
		case "finished":
			sawFinished = true
			if e.Percent != 100 {
				t.Errorf("finished event percent = %v, want 100", e.Percent)
			}
		case "processing":
			sawProcessing = true
		}
	}
	if !sawDownloading {
		t.Error("no downloading event — is stdout being routed?")
	}
	if !sawFinished {
		t.Error("no finished event")
	}
	if !sawProcessing {
		t.Error("no postprocess event — is stderr being routed?")
	}
}

// The failure log must not gain progress lines, and must keep the real stderr.
func TestRunQueuedKeepsLogClean(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)
	t.Setenv("YTDLP_FAKE_NLINES", "0") // no file saved → failure → a .log is written
	t.Setenv("YTDLP_FAKE_RC", "1")
	t.Setenv("YTDLP_FAKE_PROGRESS", "1")

	o := runOptions(t, core.ModeSilent, out)
	if rc := RunQueued(o, func(ProgressEvent) {}); rc == 0 {
		t.Fatal("expected a failure exit code")
	}
	logs := storeLogs(t, o)
	if len(logs) == 0 {
		t.Fatal("no failure log written")
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), progressSentinel) {
		t.Errorf("failure log leaked progress lines:\n%s", data)
	}
	if !strings.Contains(string(data), "a genuine stderr warning") {
		t.Errorf("failure log lost the real stderr:\n%s", data)
	}
}

// A nil sink is the interactive silent path: no progress args, stderr straight to
// the file — byte-identical to the pre-Cycle-3 behaviour.
func TestRunQueuedNilSinkAddsNoProgressArgs(t *testing.T) {
	o := runOptions(t, core.ModeSilent, t.TempDir())
	base := core.BuildArgs(o)
	for _, a := range base {
		if strings.Contains(a, progressSentinel) || a == "--progress" || a == "--newline" {
			t.Fatalf("core.BuildArgs must never carry progress args, found %q", a)
		}
	}
	// progressArgs is a pure append: BuildArgs stays a byte-identical prefix.
	full := append(append([]string{}, base...), progressArgs()...)
	for i := range base {
		if full[i] != base[i] {
			t.Fatalf("progress args are not a pure append at %d: %q vs %q", i, full[i], base[i])
		}
	}
	// --progress must come AFTER core's --no-progress (last flag wins in yt-dlp).
	noProg, prog := -1, -1
	for i, a := range full {
		switch a {
		case "--no-progress":
			noProg = i
		case "--progress":
			prog = i
		}
	}
	if noProg >= 0 && prog >= 0 && prog < noProg {
		t.Errorf("--progress (%d) must come after --no-progress (%d)", prog, noProg)
	}
}
