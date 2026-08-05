package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/logstore"
)

// openCall records what would have been handed to the platform launcher.
type openCall struct {
	action string // "reveal" | "open"
	path   string
}

func captureOpen(t *testing.T) *[]openCall {
	t.Helper()
	var calls []openCall
	oldReveal, oldOpen := revealPath, openPath
	revealPath = func(p string) error { calls = append(calls, openCall{"reveal", p}); return nil }
	openPath = func(p string) error { calls = append(calls, openCall{"open", p}); return nil }
	t.Cleanup(func() { revealPath, openPath = oldReveal, oldOpen })
	return &calls
}

// realRecord writes an actual audio file and returns a record pointing at it.
func realRecord(t *testing.T) (dir, path string, e logstore.Entry) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "Artista - Traccia.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path, logstore.Entry{
		URL: "https://youtu.be/A", Title: "Artista - Traccia", Format: "mp3",
		Success: true, Path: path, Dir: dir,
	}
}

func TestOpenFileLaunchesTheRecordedFile(t *testing.T) {
	_, path, e := realRecord(t)
	calls := captureOpen(t)

	if err := Open(e, OpenFile); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].action != "open" || (*calls)[0].path != path {
		t.Errorf("got %+v, want a single open of %q", *calls, path)
	}
}

func TestOpenFolderRevealsTheRecordedFile(t *testing.T) {
	_, path, e := realRecord(t)
	calls := captureOpen(t)

	if err := Open(e, OpenFolder); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].action != "reveal" || (*calls)[0].path != path {
		t.Errorf("got %+v, want a single reveal of %q", *calls, path)
	}
}

// TestOpenRejectsARecordWithNoPath: a record written before Cycle 5, or a job
// that saved nothing, has nothing to open. The action must be refused, not
// guessed at.
func TestOpenRejectsARecordWithNoPath(t *testing.T) {
	calls := captureOpen(t)
	for _, path := range []string{"", "   ", "relative/a.mp3", "~/Music/a.mp3"} {
		e := logstore.Entry{URL: "https://youtu.be/A", Path: path, Dir: "/music"}
		if err := Open(e, OpenFile); !errors.Is(err, ErrNoPath) {
			t.Errorf("Open(path=%q) error = %v, want ErrNoPath", path, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a pathless record launched %+v", *calls)
	}
}

// TestOpenRejectsAVanishedFile: the recorded path is a fact about the past. The
// file may have been moved, renamed or deleted since.
func TestOpenRejectsAVanishedFile(t *testing.T) {
	dir, path, e := realRecord(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	calls := captureOpen(t)

	if err := Open(e, OpenFile); !errors.Is(err, ErrGone) {
		t.Errorf("Open of a deleted file = %v, want ErrGone", err)
	}
	if len(*calls) != 0 {
		t.Errorf("a deleted file launched %+v", *calls)
	}
	_ = dir
}

// TestOpenFolderFallsBackToTheJobFolder: "show me where it was" is still
// answerable when the file itself is gone — the design's success-but-file-gone
// row state (§8.3).
func TestOpenFolderFallsBackToTheJobFolder(t *testing.T) {
	dir, path, e := realRecord(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	calls := captureOpen(t)

	if err := Open(e, OpenFolder); err != nil {
		t.Fatalf("Open(folder) after the file vanished: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].action != "open" || (*calls)[0].path != dir {
		t.Errorf("got %+v, want the job folder %q opened", *calls, dir)
	}
}

// TestOpenRejectsAPathOutsideItsOwnFolder is the containment check of design §7.
// Only a tampered or hand-edited history produces this, which is exactly why it
// must be refused: without it the endpoint becomes "open any file on the disk".
func TestOpenRejectsAPathOutsideItsOwnFolder(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "ytdl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "secret.mp3")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling whose name merely STARTS with the folder name: a plain string
	// prefix check would wrongly accept it.
	sibling := filepath.Join(home, "ytdl-evil")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	siblingFile := filepath.Join(sibling, "a.mp3")
	if err := os.WriteFile(siblingFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"a sibling of the folder", outside},
		{"traversal out of the folder", filepath.Join(dir, "..", "secret.mp3")},
		{"a folder whose name shares the prefix", siblingFile},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := captureOpen(t)
			e := logstore.Entry{URL: "u", Path: tc.path, Dir: dir}
			for _, target := range []OpenTarget{OpenFile, OpenFolder} {
				if err := Open(e, target); !errors.Is(err, ErrOutsideDir) {
					t.Errorf("Open(%q, target %d) error = %v, want ErrOutsideDir", tc.path, target, err)
				}
			}
			if len(*calls) != 0 {
				t.Errorf("an out-of-folder path launched %+v", *calls)
			}
		})
	}
}

// TestOpenFolderDoesNotForgiveAnUntrustworthyRecord: the folder fallback is for
// a file that legitimately moved, never for a record that points somewhere it
// has no business pointing.
func TestOpenFolderDoesNotForgiveAnUntrustworthyRecord(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "ytdl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := captureOpen(t)
	e := logstore.Entry{URL: "u", Path: filepath.Join(home, "elsewhere.mp3"), Dir: dir}
	if err := Open(e, OpenFolder); !errors.Is(err, ErrOutsideDir) {
		t.Errorf("Open(folder) = %v, want ErrOutsideDir even with a valid Dir to fall back to", err)
	}
	if len(*calls) != 0 {
		t.Errorf("an out-of-folder record launched %+v", *calls)
	}
}

// TestOpenFileRejectsANonAudioExtension: the extension whitelist is the same one
// -f and the config key enforce — the five formats ytdl can produce. A record
// naming anything else did not come from a download.
func TestOpenFileRejectsANonAudioExtension(t *testing.T) {
	for _, name := range []string{"a.sh", "a.txt", "a", "a.mp3.sh", "a.exe"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
				t.Fatal(err)
			}
			calls := captureOpen(t)
			e := logstore.Entry{URL: "u", Path: path, Dir: dir}
			if err := Open(e, OpenFile); !errors.Is(err, ErrNotAudio) {
				t.Errorf("Open(%q) error = %v, want ErrNotAudio", name, err)
			}
			if len(*calls) != 0 {
				t.Errorf("%q launched %+v", name, *calls)
			}
		})
	}
}

// TestOpenFileAcceptsEveryFormatYtdlProduces guards against the whitelist and
// the tool's actual output drifting apart.
func TestOpenFileAcceptsEveryFormatYtdlProduces(t *testing.T) {
	for _, ext := range []string{"mp3", "flac", "m4a", "opus", "wav", "MP3", "FlAc"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a."+ext)
			if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			calls := captureOpen(t)
			e := logstore.Entry{URL: "u", Path: path, Dir: dir}
			if err := Open(e, OpenFile); err != nil {
				t.Fatalf("Open(.%s) = %v, want it to open", ext, err)
			}
			if len(*calls) != 1 {
				t.Errorf(".%s produced %+v, want one launch", ext, *calls)
			}
		})
	}
}

// TestOpenFolderRevealDoesNotRequireAnAudioExtension: revealing a location does
// not run the file, so the extension whitelist would only get in the way — the
// containment and existence checks still apply.
func TestOpenFolderRevealDoesNotRequireAnAudioExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := captureOpen(t)
	e := logstore.Entry{URL: "u", Path: path, Dir: dir}
	if err := Open(e, OpenFolder); err != nil {
		t.Fatalf("Open(folder) = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].action != "reveal" {
		t.Errorf("got %+v, want a reveal", *calls)
	}
}

// TestOpenRejectsADirectoryAsTheFile: a record whose "file" is a directory would
// otherwise pass the extension check on a name like "album.mp3/".
func TestOpenRejectsADirectoryAsTheFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "album.mp3")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := captureOpen(t)
	e := logstore.Entry{URL: "u", Path: sub, Dir: dir}
	if err := Open(e, OpenFile); !errors.Is(err, ErrGone) {
		t.Errorf("Open of a directory = %v, want ErrGone (not a regular file)", err)
	}
	if len(*calls) != 0 {
		t.Errorf("a directory launched %+v", *calls)
	}
}

// TestOpenRefusesARecordWithNoFolder is a review finding: skipping the
// containment check when Dir is empty made design §7 constraint 3 optional for
// exactly the tampered record it exists to stop ("a tampered history.jsonl
// cannot turn this into 'open any file on the disk'" — it could).
//
// Nothing legitimate regresses: Path and Dir are written together by recordJob,
// and a record old enough to predate them has no Path either, so it was already
// refused with ErrNoPath.
func TestOpenRefusesARecordWithNoFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, badDir := range []string{"", "   ", "relative/dir"} {
		t.Run("dir="+badDir, func(t *testing.T) {
			calls := captureOpen(t)
			e := logstore.Entry{URL: "u", Path: path, Dir: badDir}
			if err := Open(e, OpenFile); !errors.Is(err, ErrOutsideDir) {
				t.Errorf("Open(dir=%q) error = %v, want ErrOutsideDir", badDir, err)
			}
			if len(*calls) != 0 {
				t.Errorf("a record with no usable folder launched %+v", *calls)
			}
		})
	}
}

// TestOpenRefusesTheFilesystemRoot: appending a separator to "/" yields the
// prefix "/", which every absolute path satisfies — so a record claiming Dir="/"
// turned the containment check into a no-op. The folder target applies no
// extension whitelist either, so it would have revealed ANY regular file.
func TestOpenRefusesTheFilesystemRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []OpenTarget{OpenFile, OpenFolder} {
		calls := captureOpen(t)
		e := logstore.Entry{URL: "u", Path: path, Dir: "/"}
		if err := Open(e, target); !errors.Is(err, ErrOutsideDir) {
			t.Errorf("Open(dir=/, target %d) error = %v, want ErrOutsideDir", target, err)
		}
		if len(*calls) != 0 {
			t.Errorf("Dir=/ launched %+v", *calls)
		}
	}
}

// TestOpenResolvesSymlinksBeforeContainment: comparing cleaned strings alone let
// a symlink planted INSIDE the download folder point anywhere — os.Stat follows
// it, so every later check passed on the target while the containment check had
// only seen the link.
func TestOpenResolvesSymlinksBeforeContainment(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "ytdl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "secret.mp3")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "song.mp3")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	calls := captureOpen(t)
	e := logstore.Entry{URL: "u", Path: link, Dir: dir}
	if err := Open(e, OpenFile); !errors.Is(err, ErrOutsideDir) {
		t.Errorf("Open through a symlink out of the folder = %v, want ErrOutsideDir", err)
	}
	if len(*calls) != 0 {
		t.Errorf("a symlink out of the folder launched %+v", *calls)
	}
}

// TestOpenStillAllowsASymlinkedDownloadFolder: resolving both sides means a user
// whose ~/Music is itself a symlink (a common setup on an external disk) is not
// locked out of their own downloads.
func TestOpenStillAllowsASymlinkedDownloadFolder(t *testing.T) {
	real := t.TempDir()
	realDir := filepath.Join(real, "music")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(realDir, "a.mp3")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "Music")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	calls := captureOpen(t)
	// The record was written with the symlinked folder, as the config had it.
	e := logstore.Entry{URL: "u", Path: filepath.Join(link, "a.mp3"), Dir: link}
	if err := Open(e, OpenFile); err != nil {
		t.Fatalf("Open through a symlinked download folder = %v, want it to open", err)
	}
	if len(*calls) != 1 {
		t.Errorf("got %+v, want one launch", *calls)
	}
}
