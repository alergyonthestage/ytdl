package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAttemptedAndSucceeded(t *testing.T) {
	dir := t.TempDir()
	att := filepath.Join(dir, "attempted")
	// "id\ttitle", a blank line, and a title that itself contains a tab (split
	// must be on the FIRST tab only).
	os.WriteFile(att, []byte("id1\tArtist - One\n\nid2\tHas\ttab\nid3\tThree\n"), 0o644)
	items := ParseAttempted(att)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d: %+v", len(items), items)
	}
	if items[1].ID != "id2" || items[1].Title != "Has\ttab" {
		t.Errorf("second item = %+v, want id2 / 'Has\\ttab'", items[1])
	}

	suc := filepath.Join(dir, "succeeded")
	os.WriteFile(suc, []byte("id1\n\nid3\n"), 0o644)
	ids := ParseSucceededIDs(suc)
	if len(ids) != 2 || ids[0] != "id1" || ids[1] != "id3" {
		t.Errorf("succeeded ids = %v, want [id1 id3]", ids)
	}

	if got := ParseAttempted(filepath.Join(dir, "nope")); got != nil {
		t.Errorf("missing file should parse to nil, got %v", got)
	}
}

func TestReconcilePlaylist(t *testing.T) {
	out := t.TempDir()
	now := time.Now()
	attempted := []Item{
		{ID: "id1", Title: "One"},
		{ID: "id2", Title: "Two"},
		{ID: "id3", Title: "Three"},
	}
	succeeded := []string{"id1", "id3"} // id2 failed

	if err := ReconcilePlaylist(out, "https://youtu.be/list", 1, now, attempted, succeeded); err != nil {
		t.Fatal(err)
	}
	logs, _ := filepath.Glob(filepath.Join(out, "*.log"))
	if len(logs) != 1 {
		t.Fatalf("want exactly 1 breadcrumb (for the failed item), got %d: %v", len(logs), logs)
	}
	want := filepath.Join(out, "Two — FALLITO."+Hash("id2")+".log")
	if logs[0] != want {
		t.Errorf("breadcrumb = %s, want %s", logs[0], want)
	}

	// A later, fully-successful run of the same playlist removes id2's breadcrumb.
	if err := ReconcilePlaylist(out, "https://youtu.be/list", 0, now, attempted, []string{"id1", "id2", "id3"}); err != nil {
		t.Fatal(err)
	}
	if logs, _ = filepath.Glob(filepath.Join(out, "*.log")); len(logs) != 0 {
		t.Errorf("per-item success must remove its own breadcrumb, still have: %v", logs)
	}
}

func TestReconcilePlaylistPartialThenPartial(t *testing.T) {
	out := t.TempDir()
	now := time.Now()
	attempted := []Item{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}

	// Run 1: both fail → two breadcrumbs.
	ReconcilePlaylist(out, "u", 1, now, attempted, nil)
	if logs, _ := filepath.Glob(filepath.Join(out, "*.log")); len(logs) != 2 {
		t.Fatalf("run 1 want 2 breadcrumbs, got %v", logs)
	}
	// Run 2: a succeeds, b still fails → a's breadcrumb cleared, b's remains.
	ReconcilePlaylist(out, "u", 1, now, attempted, []string{"a"})
	logs, _ := filepath.Glob(filepath.Join(out, "*.log"))
	if len(logs) != 1 || !strings.Contains(logs[0], Hash("b")) {
		t.Errorf("run 2 want only b's breadcrumb, got %v", logs)
	}
}
