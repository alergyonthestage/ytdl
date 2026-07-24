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
	}{
		// dry-run
		{"dryrun-mp3-single", ModeDryRun, "mp3", outDirPH, false, singleURL},
		{"dryrun-mp3-playlist", ModeDryRun, "mp3", outDirPH, true, playlistURL},
		{"dryrun-flac-single", ModeDryRun, "flac", outDirPH, false, singleURL},
		{"dryrun-flac-playlist", ModeDryRun, "flac", outDirPH, true, playlistURL},
		// verbose
		{"verbose-mp3-single", ModeVerbose, "mp3", outDirPH, false, singleURL},
		{"verbose-m4a-single", ModeVerbose, "m4a", outDirPH, false, singleURL},
		{"verbose-flac-playlist", ModeVerbose, "flac", outDirPH, true, playlistURL},
		// silent
		{"silent-mp3-single", ModeSilent, "mp3", outDirPH, false, singleURL},
		{"silent-mp3-playlist", ModeSilent, "mp3", outDirPH, true, playlistURL},
		{"silent-opus-single", ModeSilent, "opus", outDirPH, false, singleURL},
		{"silent-wav-playlist", ModeSilent, "wav", outDirPH, true, playlistURL},
		// default
		{"normal-mp3-single", ModeDefault, "mp3", outDirPH, false, singleURL},
		{"normal-mp3-playlist", ModeDefault, "mp3", outDirPH, true, playlistURL},
		{"normal-flac-single", ModeDefault, "flac", outDirPH, false, singleURL},
		{"normal-m4a-playlist", ModeDefault, "m4a", outDirPH, true, playlistURL},
		// default output dir ($HOME/Music/ytdl)
		{"normal-mp3-defaultdir", ModeDefault, "mp3", homePH + "/Music/ytdl", false, singleURL},
		{"silent-mp3-defaultdir", ModeSilent, "mp3", homePH + "/Music/ytdl", false, singleURL},
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
			got := BuildArgs(o)
			want := readGolden(t, c.golden)
			if !slices.Equal(got, want) {
				t.Errorf("argv mismatch for %s\n got: %#v\nwant: %#v", c.golden, got, want)
			}
		})
	}
}

func TestTempRedirectArgs(t *testing.T) {
	s := settings(t, "mp3", "/music/out")
	o := Options{Mode: ModeSilent, URL: singleURL, Settings: s}
	got := TempRedirectArgs(o, "/tmp/work-123")
	want := []string{
		"-o", s.NameTemplate + ".%(ext)s", // RELATIVE — overrides BuildArgs's absolute -o
		"-P", "home:/music/out", // final files still land in OutputDir
		"-P", "temp:/tmp/work-123", // intermediates go to the scratch dir
	}
	if !slices.Equal(got, want) {
		t.Errorf("TempRedirectArgs\n got: %#v\nwant: %#v", got, want)
	}

	// The relative -o must be exactly BuildArgs's absolute -o with the OutputDir
	// prefix removed — same final path, so after_move and parity are unchanged.
	rel := TempRedirectArgs(o, "/w")[1]
	if want := s.OutputDir + "/" + rel; want != outputTemplate(s) {
		t.Errorf("relative -o %q + OutputDir != absolute -o %q", rel, outputTemplate(s))
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
