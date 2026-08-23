package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleVerdict(at time.Time) Verdict {
	return Verdict{
		CheckedAt:  at,
		LatestYtdl: "v2.2.0",
		Pin:        Pin{YtDlp: "2026.08.02", FFmpeg: "1785863997_9.0"},
		Installed: Installed{
			Ytdl: "v2.1.0", YtDlp: "2026.07.04", FFmpeg: "1785863997_9.0",
			Foreign: []string{ComponentYtDlp},
		},
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 13, 9, 30, 0, 0, time.UTC)
	want := sampleVerdict(at)

	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := Load(dir)
	if !ok {
		t.Fatal("Load reported no verdict right after Save")
	}
	if !got.CheckedAt.Equal(at) {
		t.Errorf("CheckedAt = %v, want %v", got.CheckedAt, at)
	}
	if got.LatestYtdl != want.LatestYtdl || got.Pin != want.Pin {
		t.Errorf("remote half = %+v / %+v", got.LatestYtdl, got.Pin)
	}
	if got.Installed.YtDlp != want.Installed.YtDlp || len(got.Installed.Foreign) != 1 {
		t.Errorf("local half = %+v", got.Installed)
	}
	// The derivations must survive the round trip, since they are what surfaces
	// actually ask.
	if !got.Known() || !got.Available() {
		t.Errorf("Known=%v Available=%v after a round trip", got.Known(), got.Available())
	}
}

func TestCacheIsPrivateToTheUser(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sampleVerdict(time.Now())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(CachePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600 like everything else in the state dir", perm)
	}
}

// A corrupt cache is never an error a caller has to handle: it is "no verdict",
// which renders as "non verificato". The user did not ask for the check, so a
// damaged file must never reach them as a diagnostic.
func TestLoadTreatsDamageAsNoVerdict(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty file", ""},
		{"truncated json", `{"checked_at":"2026-08-13T09:3`},
		{"not json at all", "not json"},
		{"json of the wrong shape", `["a","b"]`},
		{"a round with no time behind it", `{"latest_ytdl":"v2.2.0"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(CachePath(dir), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if v, ok := Load(dir); ok {
				t.Fatalf("Load accepted damage and returned %+v", v)
			}
		})
	}
}

func TestLoadWithNoCacheAndNoStateDir(t *testing.T) {
	if _, ok := Load(t.TempDir()); ok {
		t.Error("Load found a verdict in an empty directory")
	}
	if _, ok := Load(""); ok {
		t.Error("Load found a verdict with no state dir")
	}
	if err := Save("", sampleVerdict(time.Now())); err == nil {
		t.Error("Save with no state dir: want an error")
	}
}

// The write is atomic — temp file plus rename — so a process dying mid-write
// cannot leave a half-file that reads as a verdict, and a completed write leaves
// nothing behind either.
func TestSaveLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Save(dir, sampleVerdict(time.Now())); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != CacheName {
			t.Errorf("leftover %q in the state dir; the temp file must be renamed or removed", e.Name())
		}
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %q survived a successful Save", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("%d entries after three saves, want 1", len(entries))
	}
}

func TestSaveCreatesTheStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ytdl")
	if err := Save(dir, sampleVerdict(time.Now())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := Load(dir); !ok {
		t.Error("Load after Save into a fresh state dir")
	}
}

func TestFresh(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		checkedAt time.Time
		want      bool
	}{
		{"just now", now, true},
		{"one second inside the window", now.Add(-DefaultTTL + time.Second), true},
		{"exactly at the boundary", now.Add(-DefaultTTL), false},
		{"one second past it", now.Add(-DefaultTTL - time.Second), false},
		{"never checked", time.Time{}, false},
		// A clock that moved back, a restored backup, a file copied from another
		// machine: re-probing heals the cache, never probing again does not.
		{"checked in the future", now.Add(time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Verdict{CheckedAt: tc.checkedAt}
			if got := v.Fresh(now, DefaultTTL); got != tc.want {
				t.Errorf("Fresh = %v, want %v", got, tc.want)
			}
		})
	}
}
