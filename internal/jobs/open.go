package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/open"
)

// OpenTarget selects what the user asked to see.
type OpenTarget int

const (
	// OpenFile opens the audio file in its default application.
	OpenFile OpenTarget = iota
	// OpenFolder shows the file in the file manager, falling back to the job's
	// download folder when the file itself is no longer there.
	OpenFolder
)

// Errors from Open. Each names a distinct refusal so the caller can say
// something useful instead of a generic failure.
var (
	// ErrNoPath: the record never recorded where its file went — it predates
	// Cycle 5, or the job saved nothing.
	ErrNoPath = errors.New("jobs: the record has no saved path")
	// ErrGone: the recorded path is no longer on disk (moved, renamed, deleted).
	ErrGone = errors.New("jobs: the file is no longer there")
	// ErrOutsideDir: the recorded path is not under the record's own download
	// folder. Only a tampered or hand-edited history can produce this.
	ErrOutsideDir = errors.New("jobs: the path is outside the job's own folder")
	// ErrNotAudio: the recorded path is not one of the audio files ytdl produces.
	ErrNotAudio = errors.New("jobs: not an audio file ytdl produced")
)

// Launchers, indirected so tests can assert what would be opened without
// opening it. Production is internal/open, the tool's only launcher.
var (
	revealPath = open.Reveal
	openPath   = open.File
)

// Open shows a history record's result to the user: the audio file itself, or
// its location in the file manager. It is the single implementation behind both
// `ytdl open` and the GUI's row actions, so the two cannot drift apart — which
// matters because this is the tool's one privileged operation, the only place
// where a stored value causes a program to be launched.
//
// The record is never trusted. history.jsonl is a plain file in the user's state
// directory: anything that can write it (a hand edit, a bug, a future importer)
// would otherwise be able to nominate an arbitrary path for launching. So the
// path — which the CALLER never supplies; it comes from ytdl's own store, keyed
// by record id — must still be:
//
//  1. non-empty and absolute (also re-checked inside internal/open);
//  2. actually present on disk, and a regular file for the file target;
//  3. under the record's OWN download folder, compared on a path-separator
//     boundary so "/music/ytdl-evil" is not accepted as being inside
//     "/music/ytdl";
//  4. for the file target, one of the five audio extensions ytdl produces —
//     the same whitelist the -f flag and the config key use.
//
// Together with internal/open's "never a shell, path is its own argv element"
// and the API guards in front of the HTTP caller, this is design §7.
//
// The folder target is more forgiving by design: a user whose file has been
// moved or tidied away still wants to be taken to where it was, so when the
// file is gone (or was never recorded) it falls back to the job's folder.
func Open(e logstore.Entry, t OpenTarget) error {
	path, err := resolveOpenPath(e)
	if err != nil {
		if t == OpenFolder {
			// Fall back to the folder itself: "show me where it was" is still
			// answerable when the file is not there. ErrOutsideDir is NOT
			// forgiven — that record is untrustworthy, full stop.
			if errors.Is(err, ErrOutsideDir) {
				return err
			}
			if dir := strings.TrimSpace(e.Dir); dir != "" && filepath.IsAbs(dir) && isDir(dir) {
				return openPath(filepath.Clean(dir))
			}
		}
		return err
	}
	if t == OpenFolder {
		return revealPath(path)
	}
	if !config.ValidFormat(strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")) {
		return ErrNotAudio
	}
	return openPath(path)
}

// resolveOpenPath applies the record-independent checks and returns the cleaned
// absolute path of the recorded file.
func resolveOpenPath(e logstore.Entry) (string, error) {
	path := strings.TrimSpace(e.Path)
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrNoPath
	}
	path = filepath.Clean(path)
	if dir := strings.TrimSpace(e.Dir); dir != "" {
		if !withinDir(path, filepath.Clean(dir)) {
			return "", ErrOutsideDir
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", ErrGone
	}
	if !info.Mode().IsRegular() {
		return "", ErrGone
	}
	return path, nil
}

// withinDir reports whether path is dir itself or lies beneath it. Both are
// expected already cleaned. The separator boundary is the point: a plain string
// prefix would accept "/music/ytdl-evil/x.mp3" as being inside "/music/ytdl".
func withinDir(path, dir string) bool {
	if dir == "" || dir == "." {
		return false
	}
	if path == dir {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(path, dir)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// CanOpen reports whether this platform can open anything at all, for the GUI's
// capability flag and the CLI's explanation. A control that cannot work is not
// rendered rather than rendered and failed (ux-principles.md §4).
func CanOpen() bool { return open.Supported() }
