package run

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/logstore"
)

// loadOneRecord runs the assertions every Cycle 5 recording test needs first:
// exactly one history record was written, and returns it.
func loadOneRecord(t *testing.T, o core.Options) logstore.Entry {
	t.Helper()
	entries, err := logstore.Load(o.Settings.LogDir, logstore.QueryOpts{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 history record, got %d: %+v", len(entries), entries)
	}
	return entries[0]
}

// TestRecordedJobCarriesSavedLocation is the fact the whole cycle rests on: a
// finished download must record WHERE its files landed, or "apri" and "mostra
// nel Finder" have nothing to act on (design §5.1).
func TestRecordedJobCarriesSavedLocation(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	t.Setenv("YTDLP_FAKE_NLINES", "2")
	t.Setenv("YTDLP_FAKE_DIR", dest)

	o := runOptions(t, core.ModeDefault, dest)
	var buf bytes.Buffer
	if rc := Dispatch(o, &buf, &buf); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	e := loadOneRecord(t, o)
	wantPath := filepath.Join(dest, "track1.mp3")
	if e.Path != wantPath {
		t.Errorf("Path = %q, want the FIRST saved file %q", e.Path, wantPath)
	}
	if e.Count != 2 {
		t.Errorf("Count = %d, want 2", e.Count)
	}
	if e.Dir != dest {
		t.Errorf("Dir = %q, want the job's output dir %q", e.Dir, dest)
	}
	if e.Playlist {
		t.Error("Playlist = true for a single-link job")
	}
	if e.Error != "" {
		t.Errorf("a successful job recorded a failure reason: %q", e.Error)
	}
}

// TestRecordedJobCarriesPlaylistFlag: `again` has to reproduce the job it came
// from, and "whole playlist" is the one option that changes what gets downloaded.
func TestRecordedJobCarriesPlaylistFlag(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	t.Setenv("YTDLP_FAKE_NLINES", "3")
	t.Setenv("YTDLP_FAKE_DIR", dest)

	o := runOptions(t, core.ModeDefault, dest)
	o.Playlist = true
	var buf bytes.Buffer
	if rc := Dispatch(o, &buf, &buf); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	if e := loadOneRecord(t, o); !e.Playlist {
		t.Error("a playlist job recorded Playlist = false")
	}
}

// TestRecordedFailureCarriesReason: "why did it fail?" must be answerable from
// the history line alone, without opening the .log (design §5.2, T6).
func TestRecordedFailureCarriesReason(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	t.Setenv("YTDLP_FAKE_NLINES", "0")
	t.Setenv("YTDLP_FAKE_RC", "1")
	t.Setenv("YTDLP_FAKE_STDERR", "[youtube] extracting\\n\\x1b[0;31mERROR:\\x1b[0m HTTP Error 403: Forbidden")

	o := runOptions(t, core.ModeDefault, dest)
	var buf bytes.Buffer
	if rc := Dispatch(o, &buf, &buf); rc == 0 {
		t.Fatal("rc = 0, want a failure")
	}

	e := loadOneRecord(t, o)
	if e.Success {
		t.Fatal("the record says success, want a failure")
	}
	const want = "ERROR: HTTP Error 403: Forbidden"
	if e.Error != want {
		t.Errorf("Error = %q, want the sanitised LAST stderr line %q", e.Error, want)
	}
	if e.Path != "" || e.Count != 0 {
		t.Errorf("a job that saved nothing recorded Path=%q Count=%d, want empty", e.Path, e.Count)
	}
	if e.Dir != dest {
		t.Errorf("Dir = %q, want %q even on a failure (the folder is still known)", e.Dir, dest)
	}
}

// TestRecordedSuccessCarriesNoReason: yt-dlp warns on stderr during perfectly
// good downloads. Storing that as the failure reason would show "why it failed"
// under a ✓ row.
func TestRecordedSuccessCarriesNoReason(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_DIR", dest)
	t.Setenv("YTDLP_FAKE_STDERR", "WARNING: unable to obtain file audio codec with ffprobe")

	o := runOptions(t, core.ModeDefault, dest)
	var buf bytes.Buffer
	if rc := Dispatch(o, &buf, &buf); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	if e := loadOneRecord(t, o); e.Error != "" {
		t.Errorf("a successful job recorded Error = %q, want empty", e.Error)
	}
}

// TestRecordedZeroFileRunIsAFailureWithReason: yt-dlp exiting 0 without saving
// anything is already treated as a failure; it must carry a reason like any
// other, not a blank row.
func TestRecordedZeroFileRunIsAFailureWithReason(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	t.Setenv("YTDLP_FAKE_NLINES", "0")
	t.Setenv("YTDLP_FAKE_RC", "0")
	t.Setenv("YTDLP_FAKE_STDERR", "ERROR: nothing to download")

	o := runOptions(t, core.ModeDefault, dest)
	var buf bytes.Buffer
	Dispatch(o, &buf, &buf)

	e := loadOneRecord(t, o)
	if e.Success {
		t.Fatal("a run that saved no file recorded success")
	}
	if e.Error != "ERROR: nothing to download" {
		t.Errorf("Error = %q, want the stderr reason", e.Error)
	}
}

// TestRunQueuedRecordsSavedLocation: the queued/daemon path must record the same
// facts as the foreground one, or every background download would be missing its
// "apri" action — most downloads go through here.
func TestRunQueuedRecordsSavedLocation(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	t.Setenv("YTDLP_FAKE_NLINES", "1")
	t.Setenv("YTDLP_FAKE_DIR", dest)

	o := runOptions(t, core.ModeSilent, dest)
	if rc := RunQueued(context.Background(), o, nil, nil); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	e := loadOneRecord(t, o)
	if e.Path != filepath.Join(dest, "track1.mp3") {
		t.Errorf("Path = %q, want the saved file under %q", e.Path, dest)
	}
	if e.Count != 1 {
		t.Errorf("Count = %d, want 1", e.Count)
	}
	if e.Dir != dest {
		t.Errorf("Dir = %q, want %q", e.Dir, dest)
	}
}

// TestVerboseRecordsNoPath: verbose mode has no --print-to-file saved sink, so it
// genuinely does not know where the file went. It must record nothing rather
// than guess — a wrong path is worse than an unavailable action.
func TestVerboseRecordsNoPath(t *testing.T) {
	fakeYtDlp(t)
	dest := t.TempDir()
	o := runOptions(t, core.ModeVerbose, dest)
	var buf bytes.Buffer
	if rc := Dispatch(o, &buf, &buf); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	e := loadOneRecord(t, o)
	if e.Path != "" || e.Count != 0 {
		t.Errorf("verbose recorded Path=%q Count=%d, want empty (it cannot know)", e.Path, e.Count)
	}
	if e.Dir != dest {
		t.Errorf("Dir = %q, want %q (the configured folder IS known)", e.Dir, dest)
	}
}

func TestAbsSavedPath(t *testing.T) {
	tests := []struct {
		name      string
		saved     string
		outputDir string
		want      string
	}{
		{"an absolute path is untouched", "/m/ytdl/a.mp3", "/m/ytdl", "/m/ytdl/a.mp3"},
		{"a relative path is rooted at the output dir", "a.mp3", "/m/ytdl", "/m/ytdl/a.mp3"},
		{"nothing saved stays nothing", "", "/m/ytdl", ""},
		{"no output dir leaves it as-is", "a.mp3", "", "a.mp3"},
		{"a relative subfolder is preserved", "Playlist/a.mp3", "/m/ytdl", "/m/ytdl/Playlist/a.mp3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := absSavedPath(tc.saved, tc.outputDir); got != tc.want {
				t.Errorf("absSavedPath(%q, %q) = %q, want %q", tc.saved, tc.outputDir, got, tc.want)
			}
		})
	}
}
