package run

import (
	"bytes"
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
