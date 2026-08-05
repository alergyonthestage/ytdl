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
	p, err := plan(e, t)
	if err != nil {
		return err
	}
	if p.reveal {
		return revealPath(p.path)
	}
	return openPath(p.path)
}

// Available reports whether Open would succeed, without launching anything. The
// GUI needs it to decide a row's PRIMARY action — "Apri" when the file is still
// there, "Riscarica" when it is not (design §8.3) — and the browser cannot stat
// the disk. It runs exactly the checks Open runs, so a rendered button and the
// action behind it cannot disagree.
func Available(e logstore.Entry, t OpenTarget) bool {
	_, err := plan(e, t)
	return err == nil
}

// openPlan is what Open would do: which path, opened how.
type openPlan struct {
	path   string
	reveal bool
}

func plan(e logstore.Entry, t OpenTarget) (openPlan, error) {
	path, err := resolveOpenPath(e)
	if err != nil {
		if t == OpenFolder {
			// Fall back to the folder itself: "show me where it was" is still
			// answerable when the file is not there. ErrOutsideDir is NOT
			// forgiven — that record is untrustworthy, full stop.
			if errors.Is(err, ErrOutsideDir) {
				return openPlan{}, err
			}
			if dir := strings.TrimSpace(e.Dir); dir != "" && filepath.IsAbs(dir) && isDir(dir) {
				// Opening the folder itself, not revealing it: a reveal on Linux
				// would open the folder's PARENT.
				return openPlan{path: filepath.Clean(dir)}, nil
			}
		}
		return openPlan{}, err
	}
	if t == OpenFolder {
		return openPlan{path: path, reveal: true}, nil
	}
	if !config.ValidFormat(strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")) {
		return openPlan{}, ErrNotAudio
	}
	return openPlan{path: path}, nil
}

// resolveOpenPath applies the record-independent checks and returns the cleaned
// absolute path of the recorded file.
func resolveOpenPath(e logstore.Entry) (string, error) {
	path := strings.TrimSpace(e.Path)
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrNoPath
	}
	dir := strings.TrimSpace(e.Dir)
	// A record with no usable folder is REFUSED, not treated as unconstrained.
	// The containment rule is the whole of design §7 constraint 3, and skipping
	// it for an empty Dir made it optional for exactly the tampered record it
	// exists to stop. Nothing legitimate regresses: Path and Dir are written
	// together, and a record old enough to lack Dir has no Path either.
	if dir == "" || !filepath.IsAbs(dir) {
		return "", ErrOutsideDir
	}
	// Resolve symlinks on BOTH sides before comparing. Comparing cleaned strings
	// alone lets a symlink planted inside the download folder point anywhere,
	// and os.Stat follows it, so every later check passes on the target while
	// the containment check saw only the link.
	path = resolveSymlinks(filepath.Clean(path))
	if !withinDir(path, resolveSymlinks(filepath.Clean(dir))) {
		return "", ErrOutsideDir
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

// resolveSymlinks returns the fully-resolved path, or the input unchanged when
// it cannot be resolved (the file may simply not exist yet — that case is caught
// by the os.Stat below, with a better error).
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// withinDir reports whether path is dir itself or lies beneath it. Both are
// expected already cleaned and symlink-resolved.
//
// The separator boundary is one point: a plain string prefix would accept
// "/music/ytdl-evil/x.mp3" as being inside "/music/ytdl". The filesystem ROOT is
// the other: appending a separator to "/" yields the prefix "/", which every
// absolute path satisfies, so a record claiming Dir="/" would have turned the
// containment check into a no-op.
func withinDir(path, dir string) bool {
	sep := string(filepath.Separator)
	if dir == "" || dir == "." || dir == sep {
		return false
	}
	if path == dir {
		return true
	}
	if !strings.HasSuffix(dir, sep) {
		dir += sep
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
