package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

// BinDirEnv overrides where ytdl looks for the dependencies it provisioned. It
// exists so the golden and integration tests can point resolution at their own
// shim directory; a real install never sets it.
const BinDirEnv = "YTDL_BIN_DIR"

// MarkerName is the installer's record of what it actually put on this machine,
// beside the queue and the logs in the state dir. It is what makes the ffmpeg
// comparison exact and what lets ytdl show which ffmpeg it has (design §5).
const MarkerName = "installed.conf"

// versionTimeout bounds asking a dependency for its own version. It is a local
// exec, so it is fast or it is broken; waiting longer would only delay a surface.
const versionTimeout = 3 * time.Second

// BinDir is where install.sh puts the tools ytdl drives. It returns "" when the
// home directory cannot be resolved, in which case resolution falls back to $PATH
// and says so rather than pretending the pin holds.
func BinDir() string {
	if d := os.Getenv(BinDirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// Resolve reports where a dependency ytdl drives actually comes from: OUR copy
// under BinDir when it is there and executable, else whatever $PATH offers.
//
// This exists because PrependLocalBin puts ~/.local/bin at the front of $PATH
// only when it is ABSENT from it: a Mac whose .zprofile runs Homebrew's shellenv
// after the installer's line has /opt/homebrew/bin first, so a Homebrew yt-dlp
// wins the LookPath and ytdl silently drives a binary it never installed and
// never tested — precisely the situation the pin exists to prevent (ADR-0016 §4).
//
// found is false when neither our copy nor $PATH has it at all. That is a MISSING
// dependency, not a foreign one: run.CheckDeps already has a message for it, and
// conflating the two would send a user with no yt-dlp a warning about somebody
// else's.
func Resolve(name string) (path string, ours, found bool) {
	if dir := BinDir(); dir != "" {
		p := filepath.Join(dir, name)
		if executableFile(p) {
			return p, true, true
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, false, true
	}
	return name, false, false
}

// ToolPath is Resolve for the call sites that only need something to exec. A
// dependency present nowhere yields the bare name, so exec fails exactly as it
// does today and the familiar missing-dependency message still fires.
func ToolPath(name string) (path string, ours bool) {
	p, ours, _ := Resolve(name)
	return p, ours
}

// executableFile reports whether path is a regular file with an execute bit. Stat
// follows symlinks, so a symlinked binary in our bin dir still counts as ours —
// which is right: the installer's business is what the name resolves to.
func executableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// Dependency is one tool ytdl drives, as this machine actually has it: which
// version, where it came from, and whether we put it there.
//
// It is the local half of a verdict spelled out for the surfaces that SHOW it,
// while Installed is the same facts flattened for the ones that COMPARE and cache
// them. Both are built by the same walk below, so they cannot drift apart.
type Dependency struct {
	Name    string // "yt-dlp" | "ffmpeg"
	Version string // "" = present, but nothing recorded its version
	Path    string // "" = not found at all
	Ours    bool   // provisioned by our own installer
}

// Missing reports a dependency that is not on this machine at all — a different
// state from one whose version simply was never written down.
func (d Dependency) Missing() bool { return d.Path == "" }

// Dependencies lists what ytdl drives, in the order the surfaces show them, live
// and without touching the network.
//
// withVersions decides whether each tool is ASKED for its version. It is a real
// choice, not a convenience: `yt-dlp --version` costs the better part of a second
// (it is a Python zipapp), measured on the reference container. A probe is never
// on a path a user is waiting on, and neither is that exec — so the surfaces that
// interrupt a download pass false and read the versions from the cached verdict
// instead, while the screens the user deliberately opened pass true.
func Dependencies(stateDir string, withVersions bool) []Dependency {
	// ffmpeg does not describe its own BUILD: `ffmpeg -version` reports 9.0 while
	// the pin names 1785863997_9.0, so the only exact answer is the one the
	// installer wrote down when it put this copy here (design §5). An install
	// predating the marker leaves it empty, and an empty side is never compared.
	marker, _ := LoadMarker(stateDir)

	deps := make([]Dependency, 0, 2)
	for _, name := range []string{ComponentYtDlp, ComponentFFmpeg} {
		path, ours, found := Resolve(name)
		d := Dependency{Name: name, Ours: ours}
		if found {
			d.Path = path
		}
		switch {
		case !found:
		case name == ComponentFFmpeg:
			// Free: the marker is a file read, so it is answered on every path.
			d.Version = marker[markerFFmpegBuild]
		case withVersions:
			d.Version = toolVersion(path)
		}
		deps = append(deps, d)
	}
	return deps
}

// ReadInstalled reads what this machine actually has, flattened for comparison
// and for the cache. The installed versions are local facts and are always shown,
// in both channels, whether or not the probe ever succeeds (ADR-0016 §8).
func ReadInstalled(stateDir string) Installed {
	return InstalledFrom(Dependencies(stateDir, true))
}

// InstalledFrom flattens a dependency list into the shape that is compared and
// cached. It is separate from Dependencies so a caller that already paid for the
// walk does not pay twice.
func InstalledFrom(deps []Dependency) Installed {
	in := Installed{Ytdl: buildinfo.Version}
	for _, d := range deps {
		switch d.Name {
		case ComponentYtDlp:
			in.YtDlp = d.Version
		case ComponentFFmpeg:
			in.FFmpeg = d.Version
		}
		// Missing is not foreign: run.CheckDeps already has a message for a tool
		// that is not there, and conflating the two would send a user with no
		// yt-dlp a warning about somebody else's.
		if !d.Missing() && !d.Ours {
			in.Foreign = append(in.Foreign, d.Name)
		}
	}
	return in
}

// toolVersion asks a dependency for its own version, returning "" when it does
// not answer with something usable. yt-dlp prints exactly its tag, which is what
// makes the comparison a string equality (ADR-0016 §1).
func toolVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	return sanitizeVersion(firstNonEmptyLine(string(out)))
}

// markerFFmpegBuild is the marker key naming the exact ffmpeg build installed.
// The other keys the installer writes are read by the installer itself.
const (
	markerYtdlVersion  = "ytdl_version"
	markerYtDlpVersion = "yt_dlp_version"
	markerFFmpegBuild  = "ffmpeg_build"
	markerInstalledAt  = "installed_at"
)

// markerKeys is the marker's whole surface. Unlike deps.conf — which fails closed
// because a typo there would silently change what every installation fetches —
// an unknown key here is merely ignored: the marker is ytdl's own note to itself,
// and a newer installer writing a key this binary does not know about must not
// cost the user their ffmpeg version.
var markerKeys = map[string]bool{
	markerYtdlVersion:  true,
	markerYtDlpVersion: true,
	markerFFmpegBuild:  true,
	markerInstalledAt:  true,
}

// LoadMarker reads the installer's marker file. A missing or unreadable marker is
// not an error a caller must handle — it is "the installer never recorded this",
// which leaves the affected facts empty and therefore uncompared.
func LoadMarker(stateDir string) (map[string]string, bool) {
	if stateDir == "" {
		return nil, false
	}
	f, err := os.Open(filepath.Join(stateDir, MarkerName))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	kv, err := parseKeyValue(f, markerKeys, false)
	if err != nil {
		return nil, false
	}
	return kv, true
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
