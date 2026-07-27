// Package open is ytdl's platform launcher: the one place in the tool that asks
// the desktop to open a file or show it in the file manager. Both channels (the
// CLI's `ytdl open` and the GUI's row actions) and the open_folder_on_done
// config key go through it, so "open" means the same thing everywhere and there
// is exactly one piece of code that spawns a launcher process.
//
// It never uses a shell: the path is always its own argv element, so nothing in
// a filename can become a command. It also refuses a path that is not absolute
// — a relative path could resolve anywhere, and a path beginning with "-" would
// be read as a flag by open(1)/xdg-open(1). Callers are expected to validate the
// path against their own rules as well (the GUI's open endpoint has four more
// checks, design §7); this package's job is to make the spawn itself
// unexploitable regardless of who calls it.
package open

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrUnsupported is returned when the running platform has no known launcher.
// Callers should present the action as unavailable rather than as failed.
var ErrUnsupported = errors.New("open: no launcher on this platform")

// ErrBadPath is returned for an empty or non-absolute path.
var ErrBadPath = errors.New("open: path must be absolute")

// target says what the user asked for.
type target int

const (
	targetFile   target = iota // open the file itself in its default application
	targetReveal               // show the file in the file manager
)

// spawn runs a launcher. It is a variable so tests can observe the exact argv
// without opening windows on the machine running them.
var spawn = func(argv []string) error {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("open: %s non disponibile: %w", argv[0], err)
	}
	// exec.Command, never a shell: argv[1] is one argument whatever it contains.
	return exec.Command(argv[0], argv[1:]...).Start()
}

// argv builds the launcher command line for goos, or nil if that platform has
// no launcher. macOS reveals with `open -R`; Linux has no portable reveal, so it
// falls back to opening the containing directory (design §7).
func argv(goos string, t target, path string) []string {
	switch goos {
	case "darwin":
		if t == targetReveal {
			return []string{"open", "-R", path}
		}
		return []string{"open", path}
	case "linux":
		if t == targetReveal {
			return []string{"xdg-open", filepath.Dir(path)}
		}
		return []string{"xdg-open", path}
	default:
		return nil
	}
}

// Supported reports whether this platform has a launcher at all. The GUI sends
// it to the browser as a capability flag so the buttons are not rendered where
// they cannot work, and the CLI uses it to explain rather than fail.
func Supported() bool { return argv(runtime.GOOS, targetFile, "/x") != nil }

// File opens path in the user's default application for that file type.
func File(path string) error { return launch(targetFile, path) }

// Reveal shows path in the file manager: selected in a Finder window on macOS,
// or — since there is no portable equivalent — its containing folder on Linux.
func Reveal(path string) error { return launch(targetReveal, path) }

func launch(t target, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return ErrBadPath
	}
	a := argv(runtime.GOOS, t, path)
	if a == nil {
		return ErrUnsupported
	}
	return spawn(a)
}
