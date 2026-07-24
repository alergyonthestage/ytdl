package logstore

import (
	"testing"
	"time"
)

// TitleForURL returns the newest recorded title for a URL, matched on the same
// normalized identity used everywhere else — so `ytdl retry` can name a failed
// job from an earlier attempt's history record.
func TestTitleForURL(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	must := func(j Job) {
		t.Helper()
		if err := Append(dir, j); err != nil {
			t.Fatal(err)
		}
	}
	must(Job{URL: "https://youtu.be/A", Title: "Old Title", Time: base.Add(-time.Hour), Success: true})
	must(Job{URL: "https://youtu.be/A#frag", Title: "New Title", Time: base, Success: false})
	must(Job{URL: "https://youtu.be/B", Title: "Other", Time: base, Success: true})

	if got := TitleForURL(dir, "https://youtu.be/A"); got != "New Title" {
		t.Errorf("TitleForURL = %q, want the newest matching title", got)
	}
	// The #fragment is dropped by NormalizeURL, so differing fragments still match.
	if got := TitleForURL(dir, "https://youtu.be/A#other"); got != "New Title" {
		t.Errorf("normalized match failed: got %q", got)
	}
	if got := TitleForURL(dir, "https://youtu.be/UNKNOWN"); got != "" {
		t.Errorf("unknown URL = %q, want empty", got)
	}
	if got := TitleForURL("", "x"); got != "" {
		t.Errorf("empty dir = %q, want empty", got)
	}
}
