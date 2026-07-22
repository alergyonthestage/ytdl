package core

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
)

var update = flag.Bool("update", false, "regenerate golden files by running the Bash capture harness")

const (
	singleURL   = "https://youtu.be/dQw4w9WgXcQ"
	playlistURL = "https://www.youtube.com/playlist?list=PLtest0123456789"

	// Placeholders the capture harness normalizes real paths to.
	outDirPH = "{{OUTPUT_DIR}}"
	homePH   = "{{HOME}}"
	selfPH   = "{{SELF}}"
	titlePH  = "{{TITLEFILE}}"
	savedPH  = "{{SAVEDFILE}}"
)

// settings returns Defaults() with the fields a golden case varies overridden.
// HOME is pinned so Defaults() is deterministic even though OutputDir is set
// explicitly afterwards.
func settings(t *testing.T, format, outDir string) config.Settings {
	t.Helper()
	t.Setenv("HOME", "/pinned-test-home")
	s := config.Defaults()
	s.Format = format
	s.OutputDir = outDir
	return s
}

func TestBuildArgsGoldens(t *testing.T) {
	if *update {
		regenerateGoldens(t)
	}

	cases := []struct {
		golden   string
		mode     Mode
		format   string
		outDir   string
		playlist bool
		url      string
		reexec   bool // background: compare ReExecArgs instead of BuildArgs
	}{
		// dry-run
		{"dryrun-mp3-single", ModeDryRun, "mp3", outDirPH, false, singleURL, false},
		{"dryrun-mp3-playlist", ModeDryRun, "mp3", outDirPH, true, playlistURL, false},
		{"dryrun-flac-single", ModeDryRun, "flac", outDirPH, false, singleURL, false},
		{"dryrun-flac-playlist", ModeDryRun, "flac", outDirPH, true, playlistURL, false},
		// verbose
		{"verbose-mp3-single", ModeVerbose, "mp3", outDirPH, false, singleURL, false},
		{"verbose-m4a-single", ModeVerbose, "m4a", outDirPH, false, singleURL, false},
		{"verbose-flac-playlist", ModeVerbose, "flac", outDirPH, true, playlistURL, false},
		// silent
		{"silent-mp3-single", ModeSilent, "mp3", outDirPH, false, singleURL, false},
		{"silent-mp3-playlist", ModeSilent, "mp3", outDirPH, true, playlistURL, false},
		{"silent-opus-single", ModeSilent, "opus", outDirPH, false, singleURL, false},
		{"silent-wav-playlist", ModeSilent, "wav", outDirPH, true, playlistURL, false},
		// default
		{"normal-mp3-single", ModeDefault, "mp3", outDirPH, false, singleURL, false},
		{"normal-mp3-playlist", ModeDefault, "mp3", outDirPH, true, playlistURL, false},
		{"normal-flac-single", ModeDefault, "flac", outDirPH, false, singleURL, false},
		{"normal-m4a-playlist", ModeDefault, "m4a", outDirPH, true, playlistURL, false},
		// default output dir ($HOME/Music/ytdl)
		{"normal-mp3-defaultdir", ModeDefault, "mp3", homePH + "/Music/ytdl", false, singleURL, false},
		{"silent-mp3-defaultdir", ModeSilent, "mp3", homePH + "/Music/ytdl", false, singleURL, false},
		// background (re-exec argv)
		{"background-mp3-single", ModeBackground, "mp3", outDirPH, false, singleURL, true},
		{"background-flac-single", ModeBackground, "flac", outDirPH, false, singleURL, true},
		{"background-playlist", ModeBackground, "mp3", outDirPH, true, playlistURL, true},
	}

	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			o := Options{
				Mode:      c.mode,
				URL:       c.url,
				Settings:  settings(t, c.format, c.outDir),
				Playlist:  c.playlist,
				TitleFile: titlePH,
				SavedFile: savedPH,
			}
			var got []string
			if c.reexec {
				got = ReExecArgs(o, selfPH)
			} else {
				got = BuildArgs(o)
			}
			want := readGolden(t, c.golden)
			if !slices.Equal(got, want) {
				t.Errorf("argv mismatch for %s\n got: %#v\nwant: %#v", c.golden, got, want)
			}
		})
	}
}

// readGolden reads the NUL-delimited golden byte stream and splits it into the
// argument slice, dropping only the trailing empty left by the final NUL (the
// empty-string argument in the metadata pipeline is a legitimate interior token).
func readGolden(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".args"))
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	parts := bytes.Split(data, []byte{0})
	if n := len(parts); n > 0 && len(parts[n-1]) == 0 {
		parts = parts[:n-1]
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

func regenerateGoldens(t *testing.T) {
	t.Helper()
	harness := filepath.Join("..", "..", "tests", "harness", "capture-goldens.sh")
	out, err := exec.Command("bash", harness).CombinedOutput()
	if err != nil {
		t.Fatalf("regenerating goldens: %v\n%s", err, out)
	}
	t.Logf("regenerated goldens:\n%s", out)
}
