package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
	fakeTool(t, ourDir, ComponentFFmpeg, "9.0")

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

// The marker records what the installer PUT there, not what is there now. A copy
// the user has since deleted must not be reported as installed — the marker is
// evidence, not a substitute for looking.
func TestDependenciesDoNotTrustTheMarkerOverReality(t *testing.T) {
	isolate(t) // nothing installed anywhere
	state := t.TempDir()
	writeMarker(t, state, "ffmpeg_build = 1785863997_9.0\n")

	for _, d := range Dependencies(state, true) {
		if !d.Missing() {
			t.Errorf("%s reported as present at %q", d.Name, d.Path)
		}
		if d.Version != "" {
			t.Errorf("%s reported version %q while absent from the machine", d.Name, d.Version)
		}
	}
	if in := ReadInstalled(state); in.FFmpeg != "" {
		t.Errorf("Installed.FFmpeg = %q for an ffmpeg that is not there", in.FFmpeg)
	}
}

// An install predating the marker has ffmpeg but no record of WHICH ffmpeg. That
// is a third state — present, version unknown — and it must not collapse into
// either "absent" or a guess.
func TestDependencyPresentButUnrecorded(t *testing.T) {
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentFFmpeg, "9.0")

	var ff Dependency
	for _, d := range Dependencies(t.TempDir(), true) {
		if d.Name == ComponentFFmpeg {
			ff = d
		}
	}
	if ff.Missing() {
		t.Fatal("ffmpeg is on the machine but was reported missing")
	}
	if ff.Version != "" {
		t.Errorf("Version = %q; with no marker there is nothing to report", ff.Version)
	}
	if !ff.Ours {
		t.Error("Ours = false for a copy in our own bin dir")
	}
}

func TestFFmpegVersion(t *testing.T) {
	cases := map[string]string{
		"1785863997_9.0": "9.0",
		"9.0":            "9.0", // no build prefix: shown whole
		"":               "",
		"weird_":         "weird_", // nothing after the separator: not trimmed into ""
	}
	for in, want := range cases {
		if got := FFmpegVersion(in); got != want {
			t.Errorf("FFmpegVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// A completed `ytdl --update` discards the verdict, so the surface stops telling
// the user to do a thing they have just done.
func TestInvalidate(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sampleVerdict(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := Invalidate(dir); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := Load(dir); ok {
		t.Error("the verdict survived Invalidate")
	}
	// Idempotent: invalidating what is already gone is not a failure.
	if err := Invalidate(dir); err != nil {
		t.Errorf("second Invalidate: %v", err)
	}
	if err := Invalidate(""); err != nil {
		t.Errorf("Invalidate with no state dir: %v", err)
	}
}

// An ffmpeg installed after its attested build was withdrawn is UNCOMPARED, not
// stale. Comparing it against a build that no longer exists would report an
// update on every check, for ever, that applying could never resolve — the
// installer would fall back again and the difference would persist (ADR-0016 §15).
func TestAnUnattestedFFmpegIsUncomparedRatherThanStale(t *testing.T) {
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentFFmpeg, "9.1")
	state := t.TempDir()
	writeMarker(t, state, "ffmpeg_build = 1900000000_9.1\nffmpeg_pinned = false\n")

	var ff Dependency
	for _, d := range Dependencies(state, false) {
		if d.Name == ComponentFFmpeg {
			ff = d
		}
	}
	if ff.Attested {
		t.Error("a fallback install was reported as attested")
	}
	// It is still SHOWN — silence would be the dishonest half of the trade.
	if ff.Version != "1900000000_9.1" {
		t.Errorf("Version = %q; the surface still has to say which copy this is", ff.Version)
	}

	in := InstalledFrom(Dependencies(state, false))
	if in.FFmpeg != "" {
		t.Errorf("Installed.FFmpeg = %q; an unattested copy must be uncompared", in.FFmpeg)
	}
	if len(in.Unattested) != 1 || in.Unattested[0] != ComponentFFmpeg {
		t.Errorf("Unattested = %v, want [%s]", in.Unattested, ComponentFFmpeg)
	}

	// The proof it matters: with the pin naming the withdrawn build, nothing is
	// reported as available.
	v := Verdict{
		CheckedAt:  time.Now(),
		LatestYtdl: buildinfo.Version,
		Pin:        Pin{YtDlp: "2026.07.04", FFmpeg: "1785863997_9.0"},
		Installed:  in,
	}
	for _, c := range v.Changes() {
		if c.Component == ComponentFFmpeg {
			t.Errorf("a phantom ffmpeg update survived: %+v", c)
		}
	}
}

// An install predating the fallback has no ffmpeg_pinned key at all. Absent means
// attested: every install before it existed took the pinned path or none.
func TestAMarkerWithoutThePinnedKeyCountsAsAttested(t *testing.T) {
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentFFmpeg, "9.0")
	state := t.TempDir()
	writeMarker(t, state, "ffmpeg_build = 1785863997_9.0\n")

	in := InstalledFrom(Dependencies(state, false))
	if in.FFmpeg != "1785863997_9.0" {
		t.Errorf("Installed.FFmpeg = %q, want the recorded build", in.FFmpeg)
	}
	if len(in.Unattested) != 0 {
		t.Errorf("Unattested = %v, want none", in.Unattested)
	}
}

// recordingTool writes an executable stub that leaves a sentinel behind when it
// runs. It is how a test asserts something was NEVER exec'd: an assertion on a
// returned value cannot tell "asked and got this" from "never asked".
func recordingTool(t *testing.T, dir, name, sentinel, printsVersion string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n: > '" + sentinel + "'\necho '" + printsVersion + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// byName picks one dependency out of the walk, so a test names what it asserts
// about instead of indexing into a slice whose order it would then depend on.
func byName(t *testing.T, deps []Dependency, name string) Dependency {
	t.Helper()
	for _, d := range deps {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("%s missing from the walk of %d", name, len(deps))
	return Dependency{}
}

// The startup path answers from what the installer wrote down. The version is
// present immediately and is NOT flagged as probed: `!Probed && Version != ""`
// is the documented state meaning "the installer recorded this", which is
// already ffmpeg's normal one (ADR-0019 §2).
func TestRecordedDependenciesAnswerFromTheMarker(t *testing.T) {
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentYtDlp, "2026.07.04")
	state := t.TempDir()
	writeMarker(t, state, "yt_dlp_version = 2026.08.20\n")

	d := byName(t, RecordedDependencies(state), ComponentYtDlp)
	if d.Version != "2026.08.20" {
		t.Errorf("Version = %q, want the marker's 2026.08.20", d.Version)
	}
	if d.Probed {
		t.Error("Probed = true: a recorded version was never asked for")
	}
	if !d.Ours || d.Missing() {
		t.Errorf("Ours = %v, Path = %q, want our copy found", d.Ours, d.Path)
	}
}

// The measured defect, asserted at its source: nothing on the startup path
// execs a dependency. A stub that records its own invocation leaves no record.
func TestRecordedDependenciesAskNoTool(t *testing.T) {
	ourDir, pathDir := isolate(t)
	sentinel := filepath.Join(t.TempDir(), "invoked")
	recordingTool(t, ourDir, ComponentYtDlp, sentinel, "2026.07.04")
	recordingTool(t, pathDir, ComponentFFmpeg, sentinel, "ffmpeg version 9.0")
	state := t.TempDir()
	writeMarker(t, state, "yt_dlp_version = 2026.08.20\nffmpeg_build = 1785863997_9.0\n")

	RecordedDependencies(state)

	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("a dependency was exec'd on the recorded walk")
	}
}

// Our record describes OUR copy. Attributing it to a Homebrew yt-dlp would print
// our number beside somebody else's path — the rule ffmpeg has followed since
// Cycle 6-plus, applied to the tool that now reads the marker too.
func TestRecordedDependenciesDoNotAttributeOurRecordToAForeignCopy(t *testing.T) {
	_, pathDir := isolate(t)
	fakeTool(t, pathDir, ComponentYtDlp, "2020.01.01")
	state := t.TempDir()
	writeMarker(t, state, "yt_dlp_version = 2026.08.20\n")

	d := byName(t, RecordedDependencies(state), ComponentYtDlp)
	if d.Ours {
		t.Fatal("a $PATH copy was called ours")
	}
	if d.Version != "" {
		t.Errorf("Version = %q, want empty: the marker does not describe a foreign copy", d.Version)
	}
}

// Unrecorded is not missing. An install predating the marker has yt-dlp and no
// note of its version: the tool is still listed, with an empty version that
// reads as "nobody wrote one down" rather than as a failed probe.
func TestRecordedDependenciesWithNoMarker(t *testing.T) {
	ourDir, _ := isolate(t)
	fakeTool(t, ourDir, ComponentYtDlp, "2026.07.04")

	d := byName(t, RecordedDependencies(t.TempDir()), ComponentYtDlp)
	if d.Missing() {
		t.Fatal("an installed yt-dlp was reported missing")
	}
	if d.Version != "" || d.Probed {
		t.Errorf("Version = %q, Probed = %v, want unrecorded rather than unreadable", d.Version, d.Probed)
	}
	if d.VersionUnreadable() {
		t.Error("VersionUnreadable: nothing was asked, so nothing failed to answer")
	}
}

// The deliberate screens keep paying. Dependencies(_, true) is unchanged by the
// refactor: it asks the tool, and says it asked.
func TestDependenciesStillProbeWhenAsked(t *testing.T) {
	ourDir, _ := isolate(t)
	sentinel := filepath.Join(t.TempDir(), "invoked")
	recordingTool(t, ourDir, ComponentYtDlp, sentinel, "2026.07.04")
	state := t.TempDir()
	writeMarker(t, state, "yt_dlp_version = 2026.08.20\n")

	d := byName(t, Dependencies(state, true), ComponentYtDlp)
	if !d.Probed {
		t.Error("Probed = false on the probing walk")
	}
	if d.Version != "2026.07.04" {
		t.Errorf("Version = %q, want what the tool said, not the marker", d.Version)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("the tool was never asked")
	}
}

// The refactor moved no ffmpeg rule. In every mode ffmpeg's version comes from
// the marker, only for a copy that is ours, and Attested is gated by
// ffmpeg_pinned — the three facts ADR-0016 §15 fixed.
func TestFFmpegSemanticsUnchangedAcrossSources(t *testing.T) {
	walks := map[string]func(string) []Dependency{
		"none":     func(s string) []Dependency { return Dependencies(s, false) },
		"recorded": RecordedDependencies,
		"probed":   func(s string) []Dependency { return Dependencies(s, true) },
	}

	t.Run("ours and attested", func(t *testing.T) {
		for mode, walk := range walks {
			t.Run(mode, func(t *testing.T) {
				ourDir, _ := isolate(t)
				fakeTool(t, ourDir, ComponentFFmpeg, "ffmpeg version 9.0")
				state := t.TempDir()
				writeMarker(t, state, "ffmpeg_build = 1785863997_9.0\n")

				d := byName(t, walk(state), ComponentFFmpeg)
				if d.Version != "1785863997_9.0" {
					t.Errorf("Version = %q, want the marker's build", d.Version)
				}
				if !d.Attested {
					t.Error("Attested = false with no ffmpeg_pinned key")
				}
				if d.Probed {
					t.Error("ffmpeg was probed: it does not describe its own build")
				}
			})
		}
	})

	t.Run("foreign gets no version", func(t *testing.T) {
		for mode, walk := range walks {
			t.Run(mode, func(t *testing.T) {
				_, pathDir := isolate(t)
				fakeTool(t, pathDir, ComponentFFmpeg, "ffmpeg version 9.0")
				state := t.TempDir()
				writeMarker(t, state, "ffmpeg_build = 1785863997_9.0\n")

				if d := byName(t, walk(state), ComponentFFmpeg); d.Version != "" {
					t.Errorf("Version = %q on a foreign copy", d.Version)
				}
			})
		}
	})

	t.Run("unattested when the pin fell back", func(t *testing.T) {
		for mode, walk := range walks {
			t.Run(mode, func(t *testing.T) {
				ourDir, _ := isolate(t)
				fakeTool(t, ourDir, ComponentFFmpeg, "ffmpeg version 9.0")
				state := t.TempDir()
				writeMarker(t, state, "ffmpeg_build = 9.0\nffmpeg_pinned = false\n")

				if d := byName(t, walk(state), ComponentFFmpeg); d.Attested {
					t.Error("Attested = true after a declared fallback")
				}
			})
		}
	})
}
