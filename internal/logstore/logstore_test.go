package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHashStableAndShort(t *testing.T) {
	h := Hash("https://youtu.be/x")
	if len(h) != hashLen {
		t.Errorf("Hash len = %d, want %d", len(h), hashLen)
	}
	if h != Hash("https://youtu.be/x") {
		t.Error("Hash is not stable for the same input")
	}
	if h == Hash("https://youtu.be/y") {
		t.Error("Hash collided for different inputs")
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"  https://YouTu.be/AbC  ":              "https://youtu.be/AbC", // trim + lowercase host, keep path case
		"https://www.youtube.com/watch?v=A#t=1": "https://www.youtube.com/watch?v=A",
		"HTTPS://Example.com/P?q=1":             "https://example.com/P?q=1", // scheme lowercased, query kept
		"not a url":                             "not a url",                 // parse fallback: trimmed raw
	}
	for in, want := range cases {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
	// A retry with only a fragment/whitespace difference must hash identically.
	if Hash(NormalizeURL("https://youtu.be/x ")) != Hash(NormalizeURL("https://youtu.be/x#frag")) {
		t.Error("normalisation must make trivial URL variants hash the same")
	}
}

func TestRecordSuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 7, 23, 15, 4, 5, 123, time.UTC)

	if err := Record(dir, Job{URL: "u1", Mode: "silent", Format: "mp3", RC: 0, Success: true, Time: when}); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, Job{URL: "u2", Mode: "default", Format: "flac", RC: 1, Success: false, Time: when, Stderr: []byte("boom\n")}); err != nil {
		t.Fatal(err)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(logs) != 2 {
		t.Fatalf("want 2 records, got %d: %v", len(logs), logs)
	}

	var okData, failData string
	for _, p := range logs {
		b, _ := os.ReadFile(p)
		switch {
		case strings.HasSuffix(p, "-ok.log"):
			okData = string(b)
		case strings.HasSuffix(p, "-fail.log"):
			failData = string(b)
		}
	}
	if !strings.Contains(okData, "ytdl — job SUCCESS") || strings.Contains(okData, "-------") {
		t.Errorf("success record wrong (should have no stderr divider):\n%s", okData)
	}
	if !strings.Contains(failData, "ytdl — job FAILED") || !strings.Contains(failData, "boom") || !strings.Contains(failData, "rc:     1") {
		t.Errorf("failure record missing header/stderr/rc:\n%s", failData)
	}
}

func TestRecordEmptyDirIsNoOp(t *testing.T) {
	if err := Record("", Job{URL: "u", Time: time.Now()}); err != nil {
		t.Errorf("Record with empty dir should be a silent no-op, got %v", err)
	}
}

func TestPruneByAge(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.log")
	fresh := filepath.Join(dir, "fresh.log")
	keepNonLog := filepath.Join(dir, "keep.txt")
	for _, p := range []string{old, fresh, keepNonLog} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Age "old" well past a 30-day retention; leave "fresh" at now.
	aged := time.Now().AddDate(0, 0, -40)
	if err := os.Chtimes(old, aged, aged); err != nil {
		t.Fatal(err)
	}

	if err := Prune(dir, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old .log should have been pruned")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh .log should have been kept")
	}
	if _, err := os.Stat(keepNonLog); err != nil {
		t.Error("non-.log file must never be pruned")
	}
}

func TestPruneRetentionZeroKeepsAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	os.WriteFile(p, []byte("x"), 0o644)
	aged := time.Now().AddDate(0, 0, -1000)
	os.Chtimes(p, aged, aged)
	if err := Prune(dir, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("retention 0 must keep everything")
	}
	// A missing directory is not an error.
	if err := Prune(filepath.Join(dir, "nope"), 30); err != nil {
		t.Errorf("Prune on a missing dir should be nil, got %v", err)
	}
}

func TestBreadcrumbWriteAndRemove(t *testing.T) {
	out := t.TempDir()
	key := Hash(NormalizeURL("https://youtu.be/x"))
	when := time.Date(2026, 7, 23, 15, 4, 5, 0, time.UTC)

	if err := WriteBreadcrumb(out, "Artist - Track", key, "https://youtu.be/x", 1, when); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(out, "Artist - Track — FALLITO."+key+".log")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected breadcrumb %s: %v", want, err)
	}
	if !strings.HasPrefix(string(data), breadcrumbMarker) {
		t.Errorf("breadcrumb missing marker:\n%s", data)
	}

	// A success for the same URL removes it.
	if err := RemoveBreadcrumb(out, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Error("breadcrumb should have been removed on cleanup")
	}
}

// M2: two different URLs that share a title must not collide.
func TestBreadcrumbNoTitleCollision(t *testing.T) {
	out := t.TempDir()
	k1 := Hash(NormalizeURL("https://youtu.be/one"))
	k2 := Hash(NormalizeURL("https://youtu.be/two"))
	now := time.Now()
	WriteBreadcrumb(out, "Same Title", k1, "https://youtu.be/one", 1, now)
	WriteBreadcrumb(out, "Same Title", k2, "https://youtu.be/two", 1, now)
	logs, _ := filepath.Glob(filepath.Join(out, "*.log"))
	if len(logs) != 2 {
		t.Fatalf("same-title different-URL should yield 2 breadcrumbs, got %d: %v", len(logs), logs)
	}
	// Removing one leaves the other.
	RemoveBreadcrumb(out, k1)
	logs, _ = filepath.Glob(filepath.Join(out, "*.log"))
	if len(logs) != 1 {
		t.Errorf("cleanup of one key must not remove the other: %v", logs)
	}
}

// RemoveBreadcrumb must never delete a user's file that merely matches the glob.
func TestRemoveBreadcrumbSpareUserFile(t *testing.T) {
	out := t.TempDir()
	key := "deadbeefdeadbeef"
	userFile := filepath.Join(out, "my song."+key+".log") // same shape, not ours
	if err := os.WriteFile(userFile, []byte("user content, not a breadcrumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBreadcrumb(out, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Error("a non-breadcrumb file matching the glob must be preserved")
	}
}

// An output folder whose NAME contains glob metacharacters must not defeat
// cleanup (regression: filepath.Glob treated the whole path as a pattern).
func TestRemoveBreadcrumbGlobMetaDir(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "80s Hits [Remastered]")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	key := Hash(NormalizeURL("https://youtu.be/x"))
	if err := WriteBreadcrumb(out, "Track", key, "https://youtu.be/x", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBreadcrumb(out, key); err != nil {
		t.Fatal(err)
	}
	if logs, _ := filepath.Glob(filepath.Join(base, "*", "*.log")); len(logs) != 0 {
		t.Errorf("breadcrumb not cleaned up in a bracketed folder: %v", logs)
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"Artist - Track":   "Artist - Track",
		"A/B:C":            "A_B_C",
		"a\\b*c?d\"e<f>g|": "a_b_c_d_e_f_g_",
		"  spaced  ":       "spaced",
		"...dots":          "dots",
	}
	for in, want := range cases {
		if got := sanitizeTitle(in); got != want {
			t.Errorf("sanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
