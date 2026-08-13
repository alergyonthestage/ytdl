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

// ReadInstalled reads what this machine actually has, live and without touching
// the network. The installed versions are local facts and are always shown, in
// both channels, whether or not the probe ever succeeds (ADR-0016 §8).
func ReadInstalled(stateDir string) Installed {
	in := Installed{Ytdl: buildinfo.Version}
	in.YtDlp = toolVersion(ComponentYtDlp)

	// ffmpeg does not describe its own BUILD: `ffmpeg -version` reports 9.0 while
	// the pin names 1785863997_9.0, so the only exact answer is the one the
	// installer wrote down when it put this copy here (design §5). An install
	// predating the marker leaves this empty, and an empty side is never compared.
	if m, ok := LoadMarker(stateDir); ok {
		in.FFmpeg = m[markerFFmpegBuild]
	}

	for _, name := range []string{ComponentYtDlp, ComponentFFmpeg} {
		if _, ours, found := Resolve(name); found && !ours {
			in.Foreign = append(in.Foreign, name)
		}
	}
	return in
}

// toolVersion asks a dependency for its own version, returning "" when it is
// absent or does not answer. yt-dlp prints exactly its tag, which is what makes
// the comparison a string equality (ADR-0016 §1).
func toolVersion(name string) string {
	path, _, found := Resolve(name)
	if !found {
		return ""
	}
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
