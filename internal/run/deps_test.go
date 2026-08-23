package run

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/update"
)

// recordingShim writes a yt-dlp into dir that dumps argv[0] and its arguments,
// one per line, into the file whose path it returns.
func recordingShim(t *testing.T, dir string) string {
	t.Helper()
	rec := filepath.Join(t.TempDir(), "argv")
	// An absolute /bin/sh shebang, not `/usr/bin/env bash`: these tests empty
	// $PATH on purpose, and env would then fail to find the interpreter.
	script := "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" > '" + rec + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return rec
}

func readArgv(t *testing.T, rec string) (argv0 string, args []string) {
	t.Helper()
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("the shim never ran: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	return lines[0], lines[1:]
}

func dryRunOptions(t *testing.T) core.Options {
	t.Helper()
	s := config.Defaults()
	s.OutputDir = t.TempDir()
	s.LogDir = t.TempDir()
	// Dry run is the one mode whose argv is core.BuildArgs and nothing else: no
	// temp redirection, no progress flags. That makes it the honest place to
	// assert the golden argv is untouched by how the program was resolved.
	return core.Options{Mode: core.ModeDryRun, URL: "https://youtu.be/ABC", Settings: s}
}

// The parity gate's whole basis: the golden files carry no argv[0], so changing
// WHICH yt-dlp is executed cannot change the argv. This asserts it directly, in
// both resolution modes, against core.BuildArgs itself.
func TestDependencyResolutionLeavesTheArgvIdentical(t *testing.T) {
	t.Run("our own copy, by absolute path", func(t *testing.T) {
		ourDir := t.TempDir()
		rec := recordingShim(t, ourDir)
		useShimDir(t, ourDir)
		t.Setenv("PATH", t.TempDir()) // nothing on $PATH at all

		o := dryRunOptions(t)
		var out bytes.Buffer
		if rc := Dispatch(o, &out, &out); rc != 0 {
			t.Fatalf("rc = %d", rc)
		}
		argv0, args := readArgv(t, rec)

		if want := filepath.Join(ourDir, "yt-dlp"); argv0 != want {
			t.Errorf("argv[0] = %q, want our copy at %q", argv0, want)
		}
		if got := core.BuildArgs(o); !reflect.DeepEqual(args, got) {
			t.Errorf("argv differs from core.BuildArgs:\n got=%q\nwant=%q", args, got)
		}
	})

	t.Run("somebody else's, from $PATH", func(t *testing.T) {
		pathDir := t.TempDir()
		rec := recordingShim(t, pathDir)
		useShimDir(t, t.TempDir()) // our bin dir is empty
		t.Setenv("PATH", pathDir)

		o := dryRunOptions(t)
		var out bytes.Buffer
		if rc := Dispatch(o, &out, &out); rc != 0 {
			t.Fatalf("rc = %d", rc)
		}
		argv0, args := readArgv(t, rec)

		if want := filepath.Join(pathDir, "yt-dlp"); argv0 != want {
			t.Errorf("argv[0] = %q, want the $PATH copy at %q", argv0, want)
		}
		if got := core.BuildArgs(o); !reflect.DeepEqual(args, got) {
			t.Errorf("argv differs from core.BuildArgs:\n got=%q\nwant=%q", args, got)
		}
	})
}

// Our copy wins over a $PATH one, which is the entire behaviour change: a
// Homebrew yt-dlp must never be the binary ytdl drives.
func TestOurCopyBeatsThePath(t *testing.T) {
	ourDir, pathDir := t.TempDir(), t.TempDir()
	rec := recordingShim(t, ourDir)
	recOther := recordingShim(t, pathDir)
	useShimDir(t, ourDir)
	t.Setenv("PATH", pathDir)

	var out bytes.Buffer
	if rc := Dispatch(dryRunOptions(t), &out, &out); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if _, err := os.Stat(recOther); err == nil {
		t.Error("the $PATH copy ran; ytdl must drive its own")
	}
	if argv0, _ := readArgv(t, rec); argv0 != filepath.Join(ourDir, "yt-dlp") {
		t.Errorf("argv[0] = %q", argv0)
	}
}

// flagValue returns the value following flag in args, and whether it is there.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// silentOptions is a mode that actually downloads, so it carries the runtime args
// appended after core.BuildArgs.
func silentOptions(t *testing.T) core.Options {
	t.Helper()
	o := dryRunOptions(t)
	o.Mode = core.ModeSilent
	return o
}

// ytdl never invokes ffmpeg — yt-dlp does, off the $PATH it inherited. Naming our
// own copy's directory is what makes ADR-0016 §4 true for ffmpeg too, instead of
// only for yt-dlp.
func TestFFmpegLocationNamesOurCopy(t *testing.T) {
	ourDir := t.TempDir()
	rec := recordingShim(t, ourDir)
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(ourDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	useShimDir(t, ourDir)
	t.Setenv("PATH", t.TempDir())

	o := silentOptions(t)
	if rc := Dispatch(o, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	_, args := readArgv(t, rec)

	got, ok := flagValue(args, "--ffmpeg-location")
	if !ok {
		t.Fatalf("--ffmpeg-location absent; yt-dlp would fall back to $PATH.\nargv=%q", args)
	}
	// The DIRECTORY, because yt-dlp needs ffprobe from beside it.
	if got != ourDir {
		t.Errorf("--ffmpeg-location = %q, want our bin dir %q", got, ourDir)
	}
	// It is APPENDED, never woven in: core.BuildArgs ends with the URL, so
	// anything ytdl adds afterwards must sit past it. (The exact-prefix property
	// is asserted by the dry-run test, whose argv has no runtime args at all —
	// silent mode's BuildArgs embeds temp-file paths created during the run, so
	// it cannot be recomputed after the fact.)
	urlAt, flagAt := indexOf(args, o.URL), indexOf(args, "--ffmpeg-location")
	if urlAt < 0 || flagAt < urlAt {
		t.Errorf("--ffmpeg-location at %d is not past the URL at %d:\nargv=%q", flagAt, urlAt, args)
	}
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// A dependency that came from $PATH is one yt-dlp would have found by itself.
// Pinning its location would claim an ownership ytdl does not have.
func TestFFmpegLocationIsAbsentForAForeignCopy(t *testing.T) {
	ourDir, pathDir := t.TempDir(), t.TempDir()
	rec := recordingShim(t, ourDir) // yt-dlp is ours...
	useShimDir(t, ourDir)
	// ...but ffmpeg is somebody else's.
	if err := os.WriteFile(filepath.Join(pathDir, "ffmpeg"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	if rc := Dispatch(silentOptions(t), io.Discard, io.Discard); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if _, args := readArgv(t, rec); func() bool { _, ok := flagValue(args, "--ffmpeg-location"); return ok }() {
		t.Errorf("--ffmpeg-location was passed for a $PATH ffmpeg:\nargv=%q", args)
	}
}

// Dry run postprocesses nothing, so its argv stays core.BuildArgs and nothing
// else — asserted above, and restated here as the reason no ffmpeg flag appears.
func TestDryRunGetsNoRuntimeArgs(t *testing.T) {
	ourDir := t.TempDir()
	rec := recordingShim(t, ourDir)
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(ourDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	useShimDir(t, ourDir)
	t.Setenv("PATH", t.TempDir())

	o := dryRunOptions(t)
	if rc := Dispatch(o, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if _, args := readArgv(t, rec); !reflect.DeepEqual(args, core.BuildArgs(o)) {
		t.Errorf("dry run gained runtime args:\n got=%q\nwant=%q", args, core.BuildArgs(o))
	}
}

// A dependency present nowhere yields the bare name, so exec fails exactly as it
// always did and the familiar missing-dependency message still fires.
func TestToolPathFallsBackToTheBareName(t *testing.T) {
	useShimDir(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())

	if got := ytDlpCmd(); got != ytDlp {
		t.Errorf("ytDlpCmd() = %q, want the bare name %q", got, ytDlp)
	}
	if _, _, found := update.Resolve(ytDlp); found {
		t.Error("Resolve claims to have found a tool that is not there")
	}
}
