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

// versionTimeout bounds asking a dependency for its own version.
//
// It was 3 s, on the reasoning that a local exec "is fast or it is broken", and
// on a measurement of ~650 ms taken in the development container. That
// measurement was real; what it described was a WARM invocation (`V20`).
//
// yt-dlp ships as a PyInstaller one-file bundle that unpacks itself before it
// can answer. Warm, that costs well under a second — ~800 ms measured in the
// container. COLD, on a stock macOS machine, `yt-dlp --version` measured
// **7.4 s**, of which only 0.86 s was CPU: the rest is a cold page cache and
// Gatekeeper's first-run verification.
//
// A cold invocation is not the rare case it sounds like. The first `--version` a
// new user ever runs is cold, and so is the first startup probe after the machine
// is switched on.
//
// The failure was silent and expensive: the exec returned no answer, which
// looked exactly like "nobody ever recorded a version", and left the installed
// yt-dlp UNCOMPARED — so the surface reported "sei aggiornato" on a machine that
// could not see its own dependency at all.
//
// 30 s is four times the measured worst case, leaving room for a slower disk or
// an antivirus scanner, while still bounding a genuinely hung process. The
// released Bash-era path had no timeout whatsoever, so this is not a regression
// against it. Nothing here is on a hot path: only the screens a user opened
// deliberately pay this cost (see Dependencies' withVersions).
const versionTimeout = 30 * time.Second

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

	// Attested reports that this copy is the exact build deps.conf vouches for.
	// It is false only for an ffmpeg installed after its attested build was
	// withdrawn upstream (ADR-0016 §15): keeping ytdl installable outranks
	// keeping it verifiable, but the surface must say which one it got.
	Attested bool

	// Probed reports that this tool was ASKED for its version. It is what keeps
	// two different facts apart, which an empty Version alone cannot (`V20`):
	//
	//   Probed && Version == ""   we asked and got no usable answer
	//   !Probed && Version == ""  nobody ever recorded one
	//
	// The second is legitimate and common — an ffmpeg installed before the marker
	// existed has no build id anybody wrote down. The first is a failure, with a
	// different remedy, and rendering it as the second told the user "versione non
	// registrata" about a tool that was installed, working, and answering on
	// demand.
	Probed bool
}

// VersionUnreadable reports a tool that was asked for its version and did not
// give a usable one — it timed out, exited non-zero, or answered something that
// is not a version string.
//
// It is deliberately NOT a kind of staleness: nothing is out of date, we simply
// failed to look. Like Foreign and Unattested it is its own fact with its own
// sentence, never folded into the verdict (ADR-0016 §16.5).
func (d Dependency) VersionUnreadable() bool { return d.Probed && d.Version == "" }

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

	// An install predating the marker has no ffmpeg_pinned key. Treat that as
	// attested: every install before ADR-0016 §15 existed took the pinned path or
	// no path at all, so "absent" here means "nothing ever fell back".
	ffmpegAttested := marker[markerFFmpegPinned] != "false"

	deps := make([]Dependency, 0, 2)
	for _, name := range []string{ComponentYtDlp, ComponentFFmpeg} {
		path, ours, found := Resolve(name)
		// yt-dlp is always attested when present: it is fetched by tag and verified
		// against the SHA2-256SUMS its own release publishes, which cannot be
		// withdrawn separately from the tag.
		d := Dependency{Name: name, Ours: ours, Attested: true}
		if found {
			d.Path = path
		}
		switch {
		case !found:
		case name == ComponentFFmpeg:
			// Free: the marker is a file read, so it is answered on every path — but
			// it records what OUR installer put down, so it describes THIS copy only
			// when this copy is ours. Attributing it to a Homebrew ffmpeg would print
			// our recorded version beside somebody else's path, and would drag our
			// "non verificata" onto a binary the pin never covered. A foreign copy
			// therefore has no version anybody wrote down, which is what "" already
			// means, and Foreign is the fact that describes it.
			if ours {
				d.Version = marker[markerFFmpegBuild]
				d.Attested = ffmpegAttested
			}
		case withVersions:
			// Probed regardless of the outcome: the point of the flag is that we
			// tried, so an empty Version below reads as a failure rather than as an
			// absence of record.
			d.Probed = true
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
		switch {
		case d.Name == ComponentYtDlp:
			in.YtDlp = d.Version
		case d.Name == ComponentFFmpeg && d.Attested:
			in.FFmpeg = d.Version
		case d.Name == ComponentFFmpeg:
			// Deliberately left EMPTY, which makes it uncompared.
			//
			// An unattested ffmpeg is one the pin's build no longer exists for, so
			// comparing it against that build would report an update on every
			// check, for ever, that applying could never resolve — the installer
			// would fall back again and the difference would persist. An empty side
			// is already the rule for "nobody answered this", and nobody can.
			in.Unattested = append(in.Unattested, d.Name)
		}
		// Missing is not foreign: run.CheckDeps already has a message for a tool
		// that is not there, and conflating the two would send a user with no
		// yt-dlp a warning about somebody else's.
		if !d.Missing() && !d.Ours {
			in.Foreign = append(in.Foreign, d.Name)
		}
		// Asked and unanswered. Recorded whatever else is true of the tool — a
		// FOREIGN copy whose version we could not read is both facts at once, and
		// each carries a different remedy.
		if d.VersionUnreadable() {
			in.Unreadable = append(in.Unreadable, d.Name)
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
	markerFFmpegPinned = "ffmpeg_pinned"
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
	markerFFmpegPinned: true,
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
