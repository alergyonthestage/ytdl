package core

import (
	"slices"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
)

// These non-golden tests prove BuildArgs wires the Settings fields that the Bash
// tool never varies (embed toggles, name template) — the goldens can't cover
// them because the Bash reference hardcodes both (design-cycle1-core.md §4).

func nonDefault(t *testing.T) config.Settings {
	t.Helper()
	t.Setenv("HOME", "/pinned")
	s := config.Defaults()
	s.OutputDir = "/out"
	return s
}

func TestBuildArgsEmbedTogglesOff(t *testing.T) {
	s := nonDefault(t)
	s.EmbedMetadata = false
	s.EmbedThumbnail = false
	got := BuildArgs(Options{Mode: ModeVerbose, URL: "u", Settings: s})
	if slices.Contains(got, "--embed-metadata") {
		t.Errorf("--embed-metadata present despite EmbedMetadata=false: %v", got)
	}
	if slices.Contains(got, "--embed-thumbnail") {
		t.Errorf("--embed-thumbnail present despite EmbedThumbnail=false: %v", got)
	}

	// Only metadata on: thumbnail must be omitted, metadata kept, in order.
	s.EmbedMetadata = true
	got = BuildArgs(Options{Mode: ModeSilent, URL: "u", Settings: s})
	if !slices.Contains(got, "--embed-metadata") || slices.Contains(got, "--embed-thumbnail") {
		t.Errorf("want --embed-metadata only, got: %v", got)
	}
}

func TestBuildArgsCustomNameTemplate(t *testing.T) {
	s := nonDefault(t)
	s.NameTemplate = "%(uploader)s/%(title)s"

	// default mode: template flows into the -o value and the before_dl print
	got := BuildArgs(Options{Mode: ModeDefault, URL: "u", Settings: s, SavedFile: "sf"})
	if !slices.Contains(got, "/out/%(uploader)s/%(title)s.%(ext)s") {
		t.Errorf("custom template not in -o value; got: %v", got)
	}
	if !slices.Contains(got, "before_dl:  ♪ %(uploader)s/%(title)s.mp3") {
		t.Errorf("custom template not in before_dl print; got: %v", got)
	}

	// dry-run: template flows into --print
	got = BuildArgs(Options{Mode: ModeDryRun, URL: "u", Settings: s})
	if !slices.Contains(got, "%(uploader)s/%(title)s.mp3") {
		t.Errorf("custom template not in dry-run --print; got: %v", got)
	}
}
