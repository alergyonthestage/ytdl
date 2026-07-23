package term

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsTTYRegularFileIsFalse(t *testing.T) {
	// A regular file (the piped/redirected case) is never a TTY.
	p := filepath.Join(t.TempDir(), "out")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTTY(f) {
		t.Error("a regular file should not be detected as a TTY")
	}
}

func TestColorEnabled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out")
	f, _ := os.Create(p)
	defer f.Close()

	t.Setenv("NO_COLOR", "1")
	if ColorEnabled(f) {
		t.Error("NO_COLOR must disable colour")
	}
	os.Unsetenv("NO_COLOR")
	if ColorEnabled(f) {
		t.Error("a non-TTY must disable colour even without NO_COLOR")
	}
}

func TestColorize(t *testing.T) {
	// Disabled: verbatim.
	if got := Colorize("✓ ok", false); got != "✓ ok" {
		t.Errorf("disabled Colorize changed the string: %q", got)
	}
	// Enabled: the glyph is wrapped in codes, the text survives.
	got := Colorize("✓ Salvata", true)
	if !strings.Contains(got, green+"✓"+Reset) {
		t.Errorf("✓ not coloured green: %q", got)
	}
	if !strings.Contains(got, "Salvata") {
		t.Errorf("text lost after colourising: %q", got)
	}
	if !strings.Contains(Colorize("✗ fail", true), red+"✗"+Reset) {
		t.Error("✗ not coloured red")
	}
}
