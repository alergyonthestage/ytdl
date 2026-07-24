package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/queue"
	"github.com/alergyonthestage/ytdl/internal/term"
)

// A one-shot queue view shows the resolved title AND the full, untruncated URL, so
// the user can tell which video a job is (Cycle 4).
func TestRenderQueueTitleAndFullURL(t *testing.T) {
	at := time.Date(2026, 7, 24, 11, 54, 0, 0, time.Local)
	fullURL := "https://www.youtube.com/watch?v=6TSBMYQZq9Q&list=RDEMxyz&index=2"
	snap := queue.Snapshot{Running: []queue.Entry{
		{ID: "r", State: queue.Running, Job: queue.Job{URL: fullURL, Title: "Rick Astley - Never Gonna Give You Up", EnqueuedAt: at}},
	}}
	got := RenderQueue(snap, true, 0)
	if !strings.Contains(got, "Rick Astley - Never Gonna Give You Up") {
		t.Errorf("missing title:\n%s", got)
	}
	if !strings.Contains(got, fullURL) {
		t.Errorf("missing full (untruncated) URL:\n%s", got)
	}
}

// A playlist job never shows a (per-item) title and is flagged as a playlist,
// since it pulls more than the single URL implies.
func TestRenderQueuePlaylistFlagNoTitle(t *testing.T) {
	snap := queue.Snapshot{Pending: []queue.Entry{
		{ID: "p", State: queue.Pending, Job: queue.Job{URL: "https://x/list", Title: "Should Not Show", Playlist: true}},
	}}
	got := RenderQueue(snap, true, 0)
	if strings.Contains(got, "Should Not Show") {
		t.Errorf("playlist per-item title must not be shown:\n%s", got)
	}
	if !strings.Contains(got, "(playlist)") {
		t.Errorf("playlist should be flagged:\n%s", got)
	}
}

// In --watch (full=false), every rendered line must fit the width in DISPLAY
// COLUMNS so the in-place region redraw never wraps a line onto a second physical
// row (which desyncs its logical-newline cursor math). This must hold for wide
// scripts too — CJK and emoji titles are ordinary on YouTube (review CRITICAL).
func TestRenderQueueWatchClipsToWidth(t *testing.T) {
	longURL := "https://www.youtube.com/watch?v=" + strings.Repeat("A", 200)
	titles := []string{
		strings.Repeat("T", 200),       // ASCII
		strings.Repeat("テスト動画タイトル", 5), // CJK (2 columns per rune)
		strings.Repeat("😀", 60),        // emoji (2 columns)
	}
	const width = 40
	for _, title := range titles {
		snap := queue.Snapshot{Running: []queue.Entry{
			{ID: "r", State: queue.Running, Job: queue.Job{URL: longURL, Title: title}},
		}}
		got := RenderQueue(snap, false, width)
		for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
			if w := term.DisplayWidth(line); w > width {
				t.Errorf("line exceeds %d display columns (would wrap): %q (%d cols)", width, line, w)
			}
		}
	}
}

// The retry list, too, names a failed job by title with its full URL below.
func TestRenderRetryListShowsTitle(t *testing.T) {
	failed := []queue.Entry{{ID: "f", State: queue.Failed, Job: queue.Job{URL: "https://youtu.be/ZZZ", Title: "Some Artist - Some Track"}}}
	out := RenderRetryList(failed)
	if !strings.Contains(out, "Some Artist - Some Track") || !strings.Contains(out, "https://youtu.be/ZZZ") {
		t.Errorf("retry list should show title + full URL:\n%s", out)
	}
}
