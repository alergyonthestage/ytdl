package open

import (
	"errors"
	"runtime"
	"testing"
)

// captureSpawn swaps the launcher for one that records the argv instead of
// running it, and returns a pointer to the recorded slice.
func captureSpawn(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := spawn
	spawn = func(argv []string) error {
		got = argv
		return nil
	}
	t.Cleanup(func() { spawn = old })
	return &got
}

// TestArgvPerPlatform pins the launcher command line for every platform,
// including the ones the test host is not. The reveal fallback on Linux is the
// design's explicit choice (§7): there is no portable "select this file".
func TestArgvPerPlatform(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		target target
		path   string
		want   []string
	}{
		{"macOS opens the file", "darwin", targetFile, "/m/ytdl/a.mp3", []string{"open", "/m/ytdl/a.mp3"}},
		{"macOS reveals the file itself", "darwin", targetReveal, "/m/ytdl/a.mp3", []string{"open", "-R", "/m/ytdl/a.mp3"}},
		{"Linux opens the file", "linux", targetFile, "/m/ytdl/a.mp3", []string{"xdg-open", "/m/ytdl/a.mp3"}},
		{"Linux reveals the containing folder", "linux", targetReveal, "/m/ytdl/a.mp3", []string{"xdg-open", "/m/ytdl"}},
		{"an unknown platform has no launcher", "plan9", targetFile, "/m/ytdl/a.mp3", nil},
		{"windows is not supported this cycle", "windows", targetReveal, "/m/ytdl/a.mp3", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := argv(tc.goos, tc.target, tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("argv = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("argv = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// TestSupportedFollowsThePlatform: Supported must agree with argv, since the GUI
// renders (or hides) buttons on the strength of it.
func TestSupportedFollowsThePlatform(t *testing.T) {
	wantSupported := runtime.GOOS == "darwin" || runtime.GOOS == "linux"
	if Supported() != wantSupported {
		t.Errorf("Supported() = %v on %s, want %v", Supported(), runtime.GOOS, wantSupported)
	}
}

// TestPathIsItsOwnArgument is the "no shell" guarantee in test form: a filename
// full of shell metacharacters must arrive at the launcher as ONE untouched
// argument, not as anything a shell would look at.
func TestPathIsItsOwnArgument(t *testing.T) {
	if !Supported() {
		t.Skip("no launcher on this platform")
	}
	got := captureSpawn(t)
	const nasty = `/m/ytdl/a; rm -rf ~ && echo $(whoami) 'x' "y" |z.mp3`
	if err := File(nasty); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(*got) != 2 {
		t.Fatalf("argv = %q, want exactly two elements (launcher + path)", *got)
	}
	if (*got)[1] != nasty {
		t.Errorf("path was altered on the way to the launcher:\n got %q\nwant %q", (*got)[1], nasty)
	}
}

// TestRejectsNonAbsolutePaths: a relative path resolves against whatever the
// process's working directory happens to be, and a leading "-" would be read as
// a flag by open(1)/xdg-open(1). Neither ever reaches the launcher.
func TestRejectsNonAbsolutePaths(t *testing.T) {
	for _, path := range []string{"", "a.mp3", "./a.mp3", "../../etc/passwd", "-R", "-"} {
		t.Run(path, func(t *testing.T) {
			var spawned bool
			old := spawn
			spawn = func([]string) error { spawned = true; return nil }
			t.Cleanup(func() { spawn = old })

			if err := File(path); !errors.Is(err, ErrBadPath) {
				t.Errorf("File(%q) error = %v, want ErrBadPath", path, err)
			}
			if err := Reveal(path); !errors.Is(err, ErrBadPath) {
				t.Errorf("Reveal(%q) error = %v, want ErrBadPath", path, err)
			}
			if spawned {
				t.Errorf("File/Reveal(%q) spawned a launcher for a rejected path", path)
			}
		})
	}
}

// TestUnsupportedPlatformDoesNotSpawn: on a platform with no launcher the
// callers must get ErrUnsupported and nothing must be executed.
func TestUnsupportedPlatformDoesNotSpawn(t *testing.T) {
	if Supported() {
		t.Skip("this platform has a launcher; the unsupported path is covered by TestArgvPerPlatform")
	}
	var spawned bool
	old := spawn
	spawn = func([]string) error { spawned = true; return nil }
	t.Cleanup(func() { spawn = old })

	if err := File("/m/ytdl/a.mp3"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("File error = %v, want ErrUnsupported", err)
	}
	if spawned {
		t.Error("a launcher was spawned on an unsupported platform")
	}
}

// TestSpawnErrorReachesTheCaller: a missing xdg-open must be reported, not
// swallowed — the GUI turns it into "non è stato possibile aprire".
func TestSpawnErrorReachesTheCaller(t *testing.T) {
	if !Supported() {
		t.Skip("no launcher on this platform")
	}
	sentinel := errors.New("launcher exploded")
	old := spawn
	spawn = func([]string) error { return sentinel }
	t.Cleanup(func() { spawn = old })

	if err := Reveal("/m/ytdl/a.mp3"); !errors.Is(err, sentinel) {
		t.Errorf("Reveal error = %v, want the launcher's own error", err)
	}
}
