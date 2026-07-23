package logstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// appendN is a helper: append a job at a given time with a title and success.
func appendJob(t *testing.T, dir string, when time.Time, title string, success bool) {
	t.Helper()
	rc := 0
	if !success {
		rc = 1
	}
	if err := Append(dir, Job{URL: "https://youtu.be/" + title, Title: title, Mode: "silent", Format: "mp3", RC: rc, Success: success, Time: when}); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestAppendAndLoadNewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	appendJob(t, dir, base, "first", true)
	appendJob(t, dir, base.Add(time.Minute), "second", true)
	appendJob(t, dir, base.Add(2*time.Minute), "third", false)

	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("Load returned %d records, want 3", len(got))
	}
	wantOrder := []string{"third", "second", "first"} // newest first
	for i, w := range wantOrder {
		if got[i].Title != w {
			t.Errorf("record %d title = %q, want %q", i, got[i].Title, w)
		}
	}
	if got[0].Success {
		t.Error("newest record should be a failure (success=false)")
	}
}

func TestLoadOnlyFailed(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	appendJob(t, dir, base, "ok1", true)
	appendJob(t, dir, base.Add(time.Minute), "bad", false)
	appendJob(t, dir, base.Add(2*time.Minute), "ok2", true)

	got, err := Load(dir, QueryOpts{OnlyFailed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "bad" {
		t.Fatalf("OnlyFailed = %+v, want the single 'bad' record", got)
	}
}

func TestLoadSinceAndLimit(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		appendJob(t, dir, base.Add(time.Duration(i)*time.Minute), "j"+string(rune('0'+i)), true)
	}
	// Since drops the first two (j0, j1); Limit caps to 2 of the remaining, newest first.
	got, err := Load(dir, QueryOpts{Since: base.Add(2 * time.Minute), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Title != "j4" || got[1].Title != "j3" {
		t.Errorf("got %q,%q want j4,j3", got[0].Title, got[1].Title)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := Load(t.TempDir(), QueryOpts{})
	if err != nil {
		t.Fatalf("missing history should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing history should be empty, got %d", len(got))
	}
}

func TestLoadSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, historyFile)
	content := `{"time":"2026-07-23T20:00:00Z","url":"u","title":"good","mode":"silent","format":"mp3","rc":0,"success":true}
this is not json
{"time":"2026-07-23T20:01:00Z","url":"u2","title":"good2","mode":"silent","format":"mp3","rc":0,"success":true}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 valid records (malformed skipped), got %d", len(got))
	}
}

func TestPruneHistoryDropsOldKeepsRecentAndMalformed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	appendJob(t, dir, now.Add(-40*24*time.Hour), "old", true) // older than 30d
	appendJob(t, dir, now.Add(-1*time.Hour), "recent", true)  // within 30d
	// A malformed line must survive pruning (never dropped by a parse failure).
	f, _ := os.OpenFile(filepath.Join(dir, historyFile), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("garbage line\n")
	f.Close()

	if err := Prune(dir, 30); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "recent" {
		t.Fatalf("after prune Load = %+v, want only 'recent'", got)
	}
	// The malformed line is preserved on disk even though Load ignores it.
	raw, _ := os.ReadFile(filepath.Join(dir, historyFile))
	if !contains(string(raw), "garbage line") {
		t.Error("malformed line was dropped by prune; it must be preserved")
	}
}

func TestPruneHistoryZeroRetentionKeepsAll(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	appendJob(t, dir, now.Add(-400*24*time.Hour), "ancient", true)
	if err := Prune(dir, 0); err != nil { // 0 = keep forever
		t.Fatal(err)
	}
	got, _ := Load(dir, QueryOpts{})
	if len(got) != 1 {
		t.Fatalf("retention 0 must keep everything, got %d", len(got))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
