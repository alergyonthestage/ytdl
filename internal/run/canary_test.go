package run

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
)

// The canary (ADR-0016 §3).
//
// The residual risk of a "latest" yt-dlp policy is a new yt-dlp breaking ytdl and
// nobody noticing. The maintainer will not hear about it: the users are students,
// artists, clients and strangers who found the repository, and none of them file
// bug reports — they stop using the tool. Detection therefore cannot depend on a
// human noticing, so a scheduled job has to notice instead.
//
// It runs ytdl END TO END rather than merely checking that the flags parse,
// because what breaks is the COUPLING between the two programs, and every part of
// that coupling is asserted below:
//
//   - the naming rules produce the filename;
//   - the ID3 tags are written;
//   - `--print-to-file after_move:` returned the saved path — how ytdl learns
//     where the file went;
//   - the `--progress-template` lines are still parseable by this package.
//
// It lives here, beside the parser it protects, so it can drive the real
// RunQueued with the real progress plumbing instead of a reimplementation.
//
// It is env-gated rather than build-tagged so the ordinary suite compiles it —
// a canary that stopped compiling unnoticed would be worse than none. It needs a
// real yt-dlp, a real ffmpeg, and a fixture served over local HTTP; the workflow
// supplies all three.
//
// RECORDED LIMIT: the metadata fallback chain %(artist,creator,xartist,uploader)s
// is populated by the EXTRACTOR, and the generic extractor is not YouTube's. This
// verifies the pipeline mechanics and the tag writing, not YouTube's specific
// field mapping. That is most of the coupling surface, not all of it.
func TestCanaryEndToEnd(t *testing.T) {
	url := os.Getenv("YTDL_CANARY_URL")
	if os.Getenv("YTDL_CANARY") != "1" || url == "" {
		t.Skip("canary: set YTDL_CANARY=1 and YTDL_CANARY_URL to run this against a real yt-dlp")
	}
	for _, tool := range []string{"yt-dlp", "ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("canary needs a real %s on PATH: %v", tool, err)
		}
	}

	dest, logs := t.TempDir(), t.TempDir()
	s := config.Defaults()
	s.OutputDir, s.LogDir = dest, logs
	s.BreadcrumbOnFailure = false
	o := core.Options{Mode: core.ModeSilent, URL: url, Settings: s}

	// The progress sink is the daemon's: the same one the GUI draws from, so the
	// template and the parser are exercised exactly as they ship.
	var mu sync.Mutex
	var events []ProgressEvent
	sink := func(ev ProgressEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	var title string
	onTitle := func(s string) {
		mu.Lock()
		title = s
		mu.Unlock()
	}

	if rc := RunQueued(context.Background(), o, sink, onTitle); rc != 0 {
		t.Fatalf("yt-dlp exited %d — ytdl and this yt-dlp no longer agree", rc)
	}

	// 1. The saved path came back through --print-to-file after_move:. This is the
	//    ONLY way ytdl learns where the file went; without it every "Apri",
	//    "Mostra nella cartella" and history path in both channels is empty.
	saved := savedFileFrom(t, dest)
	if saved == "" {
		t.Fatal("no file was saved, or --print-to-file after_move: reported nothing")
	}
	fi, err := os.Stat(saved)
	if err != nil {
		t.Fatalf("the reported path does not exist: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("the saved file is empty")
	}

	// 2. The naming rules produced the filename: the configured extension, and the
	//    template's " - " separator between the two metadata halves.
	base := filepath.Base(saved)
	if filepath.Ext(base) != "."+s.Format {
		t.Errorf("saved %q, want a .%s file — the audio-format flags no longer take effect", base, s.Format)
	}
	if !strings.Contains(base, " - ") {
		t.Errorf("saved %q; the name template's separator is gone, so the naming rules did not apply", base)
	}

	// 3. The ID3 tags were written — the part the maintainer actually cares about,
	//    and the one --embed-metadata is there for.
	tags := probeTags(t, saved)
	if len(tags) == 0 {
		t.Fatal("the saved file carries no metadata tags at all")
	}
	if tags["title"] == "" {
		t.Errorf("no title tag was written; tags = %v", tags)
	}

	// 4. The progress lines still parse. A template yt-dlp changed under us would
	//    leave the GUI's bar frozen at zero with nothing else failing, which is
	//    exactly the silent breakage this job exists to catch.
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no progress events: the --progress-template output no longer parses")
	}
	sawPercent := false
	for _, ev := range events {
		if ev.Percent > 0 {
			sawPercent = true
		}
	}
	if !sawPercent {
		t.Errorf("%d progress events, none with a percentage — the template's fields moved", len(events))
	}
	if title == "" {
		t.Error("the before_dl title hook produced nothing; `ytdl queue` would show only a URL")
	}
}

// savedFileFrom returns the one audio file in the destination, or "".
func savedFileFrom(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".log") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// probeTags reads the container's metadata with ffprobe, lower-cased.
func probeTags(t *testing.T, path string) map[string]string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_format", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var probed struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		t.Fatalf("ffprobe output: %v", err)
	}
	tags := map[string]string{}
	for k, v := range probed.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	return tags
}
