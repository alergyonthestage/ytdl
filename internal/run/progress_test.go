package run

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/core"
)

func TestParseProgressLine(t *testing.T) {
	sep := progressFieldSep
	cases := []struct {
		name string
		line string
		want ProgressEvent
		ok   bool
	}{
		{
			name: "downloading",
			line: progressSentinel + sep + "downloading" + sep + " 42.3%" + sep + "1.20MiB/s" + sep + "00:12" + sep + "Artist - Track",
			want: ProgressEvent{Status: "downloading", Percent: 42.3, Speed: "1.20MiB/s", ETA: "00:12", Title: "Artist - Track"},
			ok:   true,
		},
		{
			name: "finished has no speed/eta",
			line: progressSentinel + sep + "finished" + sep + "100.0%" + sep + sep + sep + "Track",
			want: ProgressEvent{Status: "finished", Percent: 100, Title: "Track"},
			ok:   true,
		},
		{
			name: "unknown percent",
			line: progressSentinel + sep + "downloading" + sep + "NA" + sep + "Unknown B/s" + sep + "Unknown" + sep + "T",
			want: ProgressEvent{Status: "downloading", Percent: -1, Speed: "Unknown B/s", ETA: "Unknown", Title: "T"},
			ok:   true,
		},
		{
			name: "title keeps embedded tabs",
			line: progressSentinel + sep + "downloading" + sep + "1.0%" + sep + sep + sep + "A\tB",
			want: ProgressEvent{Status: "downloading", Percent: 1, Title: "A\tB"},
			ok:   true,
		},
		{name: "not a progress line", line: "ERROR: unavailable video", ok: false},
		{name: "sentinel substring but no separator", line: progressSentinel + "x", ok: false},
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

// The stderr splitter must send progress lines to the sink and everything else,
// verbatim, to the failure log — even when writes arrive split mid-line.
func TestProgressStderrSplitsLines(t *testing.T) {
	var log bytes.Buffer
	var events []ProgressEvent
	pw := newProgressStderr(&log, func(e ProgressEvent) { events = append(events, e) })

	sep := progressFieldSep
	stream := "" +
		"[youtube] Extracting URL\n" +
		progressSentinel + sep + "downloading" + sep + "50.0%" + sep + "1MiB/s" + sep + "00:03" + sep + "T\n" +
		"WARNING: something\n" +
		progressSentinel + sep + "finished" + sep + "100.0%" + sep + sep + sep + "T\n"

	// Feed the stream in awkward chunks to exercise partial-line buffering.
	for i := 0; i < len(stream); i += 7 {
		end := i + 7
		if end > len(stream) {
			end = len(stream)
		}
		pw.Write([]byte(stream[i:end]))
	}
	pw.Flush()

	logStr := log.String()
	if !strings.Contains(logStr, "[youtube] Extracting URL") || !strings.Contains(logStr, "WARNING: something") {
		t.Errorf("failure log missing real stderr lines:\n%s", logStr)
	}
	if strings.Contains(logStr, progressSentinel) {
		t.Errorf("failure log leaked a progress line:\n%s", logStr)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (throttle does not apply — distinct statuses/times): %+v", len(events), events)
	}
	if events[0].Status != "downloading" || events[0].Percent != 50 {
		t.Errorf("first event = %+v", events[0])
	}
	if events[1].Status != "finished" || events[1].Percent != 100 {
		t.Errorf("last event = %+v", events[1])
	}
}

// A partial (newline-less) progress line at EOF is dropped, not logged; a
// partial real line is flushed so a truncated final error survives.
func TestProgressStderrFlush(t *testing.T) {
	t.Run("drops partial progress line", func(t *testing.T) {
		var log bytes.Buffer
		pw := newProgressStderr(&log, func(ProgressEvent) {})
		pw.Write([]byte(progressSentinel + progressFieldSep + "downloading" + progressFieldSep + "9.0%"))
		pw.Flush()
		if log.Len() != 0 {
			t.Errorf("partial progress line should be dropped, log = %q", log.String())
		}
	})
	t.Run("flushes partial real line", func(t *testing.T) {
		var log bytes.Buffer
		pw := newProgressStderr(&log, func(ProgressEvent) {})
		pw.Write([]byte("ERROR: truncated"))
		pw.Flush()
		if log.String() != "ERROR: truncated" {
			t.Errorf("partial real line lost, log = %q", log.String())
		}
	})
}

// End-to-end: RunQueued with a sink drives the fake yt-dlp (which emits progress
// on stderr), forwards events to the sink, records a successful job, and keeps
// the failure-path decisions intact. The sink is called from yt-dlp's stderr
// goroutine, so guard the slice.
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
	last := events[len(events)-1]
	if last.Status != "finished" || last.Percent != 100 {
		t.Errorf("final event = %+v, want finished at 100%%", last)
	}
	var sawDownloading bool
	for _, e := range events {
		if e.Status == "downloading" {
			sawDownloading = true
		}
	}
	if !sawDownloading {
		t.Error("expected at least one downloading event")
	}
}

// A nil sink is the interactive silent path: no progress args are added, so the
// run is byte-identical to before. Guard it against accidental progress capture.
func TestRunQueuedNilSinkIsSilent(t *testing.T) {
	fakeYtDlp(t)
	out := t.TempDir()
	t.Setenv("YTDLP_FAKE_DIR", out)
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_RC", "0")
	t.Setenv("YTDLP_FAKE_PROGRESS", "1") // fake still emits, but nil sink ignores it

	o := runOptions(t, core.ModeSilent, out)
	if rc := RunQueued(o, nil); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
}
