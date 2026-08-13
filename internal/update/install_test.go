package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

// fakeTool writes an executable stub called name into dir.
func fakeTool(t *testing.T, dir, name, printsVersion string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\necho '" + printsVersion + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolate points dependency resolution at two empty directories — ours and a
// stand-in $PATH — so a test never sees the container's real yt-dlp.
func isolate(t *testing.T) (ourDir, pathDir string) {
	t.Helper()
	ourDir, pathDir = t.TempDir(), t.TempDir()
	t.Setenv(BinDirEnv, ourDir)
	t.Setenv("PATH", pathDir)
	return ourDir, pathDir
}

// Our copy wins even when $PATH offers another, which is the whole point: a
// Homebrew yt-dlp must never be the one ytdl drives (ADR-0016 §4).
func TestResolvePrefersOurCopy(t *testing.T) {
	ourDir, pathDir := isolate(t)
	want := fakeTool(t, ourDir, ComponentYtDlp, "2026.07.04")
	fakeTool(t, pathDir, ComponentYtDlp, "2020.01.01")

	path, ours, found := Resolve(ComponentYtDlp)
	if !found || !ours {
		t.Fatalf("Resolve = (%q, ours=%v, found=%v)", path, ours, found)
	}
	if path != want {
		t.Errorf("path = %q, want our copy at %q", path, want)
	}
}

func TestResolveFallsBackToPathAndSaysSo(t *testing.T) {
	_, pathDir := isolate(t)
	want := fakeTool(t, pathDir, ComponentYtDlp, "2020.01.01")

	path, ours, found := Resolve(ComponentYtDlp)
	if !found {
		t.Fatal("Resolve did not find the tool on $PATH")
	}
	if ours {
		t.Error("a $PATH copy must not be reported as ours; the surface says so instead of pretending the pin holds")
	}
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// Absent everywhere is a MISSING dependency, not a foreign one: run.CheckDeps
// already has a message for it, and conflating the two would send a user with no
// yt-dlp a warning about somebody else's.
func TestResolveDistinguishesMissingFromForeign(t *testing.T) {
	isolate(t)

	path, ours, found := Resolve(ComponentYtDlp)
	if found || ours {
		t.Fatalf("Resolve = (%q, ours=%v, found=%v), want not found", path, ours, found)
	}
	// The bare name keeps exec failing exactly as it does today.
	if path != ComponentYtDlp {
		t.Errorf("path = %q, want the bare name %q", path, ComponentYtDlp)
	}
	if p, o := ToolPath(ComponentYtDlp); p != ComponentYtDlp || o {
		t.Errorf("ToolPath = (%q, %v)", p, o)
	}
}

// A file in our bin dir that is not executable is not our copy: it is a
// leftover, and treating it as the tool would make ytdl exec something that
// cannot run.
func TestResolveIgnoresNonExecutablesInOurBinDir(t *testing.T) {
	ourDir, pathDir := isolate(t)
	if err := os.WriteFile(filepath.Join(ourDir, ComponentYtDlp), []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := fakeTool(t, pathDir, ComponentYtDlp, "2020.01.01")

	path, ours, _ := Resolve(ComponentYtDlp)
	if ours || path != want {
		t.Errorf("Resolve = (%q, ours=%v), want the $PATH copy at %q", path, ours, want)
	}
}

func TestReadInstalledReportsLocalFacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stubs are shell scripts")
	}
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentYtDlp, "2026.07.04")

	state := t.TempDir()
	writeMarker(t, state, "ytdl_version = v2.1.0\nyt_dlp_version = 2026.07.04\nffmpeg_build = 1785863997_9.0\n")

	in := ReadInstalled(state)
	if in.Ytdl != buildinfo.Version {
		t.Errorf("Ytdl = %q, want the running build %q", in.Ytdl, buildinfo.Version)
	}
	if in.YtDlp != "2026.07.04" {
		t.Errorf("YtDlp = %q, want the version the tool prints", in.YtDlp)
	}
	// ffmpeg cannot describe its own build, so the marker is the only exact
	// answer (design §5).
	if in.FFmpeg != "1785863997_9.0" {
		t.Errorf("FFmpeg = %q, want the marker's build id", in.FFmpeg)
	}
	if len(in.Foreign) != 0 {
		t.Errorf("Foreign = %v, want none: everything found was ours", in.Foreign)
	}
}

func TestReadInstalledNamesForeignDependencies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stubs are shell scripts")
	}
	ourDir, pathDir := isolate(t)
	fakeTool(t, ourDir, ComponentFFmpeg, "9.0")        // ours
	fakeTool(t, pathDir, ComponentYtDlp, "2020.01.01") // somebody else's

	in := ReadInstalled(t.TempDir())
	if len(in.Foreign) != 1 || in.Foreign[0] != ComponentYtDlp {
		t.Errorf("Foreign = %v, want exactly [%s]", in.Foreign, ComponentYtDlp)
	}
}

// A missing dependency is not a foreign one.
func TestReadInstalledDoesNotCallAMissingToolForeign(t *testing.T) {
	isolate(t)
	if in := ReadInstalled(t.TempDir()); len(in.Foreign) != 0 {
		t.Errorf("Foreign = %v, want none", in.Foreign)
	}
}

// A tool that prints something that is not a version answers nothing, rather
// than putting whatever it said into a terminal and into the page.
func TestReadInstalledRefusesAnUnusableVersionString(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stubs are shell scripts")
	}
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentYtDlp, "ERROR: something went very wrong")

	if in := ReadInstalled(t.TempDir()); in.YtDlp != "" {
		t.Errorf("YtDlp = %q, want empty", in.YtDlp)
	}
}

func writeMarker(t *testing.T, stateDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(stateDir, MarkerName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMarker(t *testing.T) {
	state := t.TempDir()
	if _, ok := LoadMarker(state); ok {
		t.Error("LoadMarker found a marker that was never written")
	}
	if _, ok := LoadMarker(""); ok {
		t.Error("LoadMarker with no state dir")
	}

	writeMarker(t, state, "# written by install.sh\nffmpeg_build = 1785863997_9.0\ninstalled_at = 2026-08-13T10:00:00Z\n")
	m, ok := LoadMarker(state)
	if !ok {
		t.Fatal("LoadMarker: not ok")
	}
	if m[markerFFmpegBuild] != "1785863997_9.0" {
		t.Errorf("ffmpeg_build = %q", m[markerFFmpegBuild])
	}
	if m[markerInstalledAt] == "" {
		t.Error("installed_at was dropped")
	}
}

// The marker is ytdl's own note to itself. Unlike deps.conf it does NOT fail
// closed: a newer installer writing a key this binary does not know about must
// not cost the user the values it does know.
func TestLoadMarkerToleratesUnknownKeys(t *testing.T) {
	state := t.TempDir()
	writeMarker(t, state, "ffmpeg_build = 1785863997_9.0\nsomething_new = 42\n")

	m, ok := LoadMarker(state)
	if !ok {
		t.Fatal("an unknown key made the whole marker unreadable")
	}
	if m[markerFFmpegBuild] != "1785863997_9.0" {
		t.Errorf("ffmpeg_build = %q", m[markerFFmpegBuild])
	}
	if _, present := m["something_new"]; present {
		t.Error("an unknown key was kept; only whitelisted keys are read")
	}
}
