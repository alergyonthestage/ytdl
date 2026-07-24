package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/webui"
)

func TestGUIPort(t *testing.T) {
	cases := map[string]int{
		"":       webui.DefaultPort,
		"8790":   8790,
		"abc":    webui.DefaultPort, // garbage falls back, with a warning
		"0":      webui.DefaultPort,
		"99999":  webui.DefaultPort,
		"-1":     webui.DefaultPort,
		"  8791": webui.DefaultPort, // Atoi rejects the padding; no silent surprise
	}
	for v, want := range cases {
		t.Run("YTDL_GUI_PORT="+v, func(t *testing.T) {
			t.Setenv("YTDL_GUI_PORT", v)
			if got := guiPort(); got != want {
				t.Errorf("guiPort() = %d, want %d", got, want)
			}
		})
	}
}

func TestGUIAddrIsLoopbackOnly(t *testing.T) {
	t.Setenv("YTDL_GUI_PORT", "8790")
	// The GUI must never be reachable from the network: the address it binds is
	// what enforces that, before any Host or token check.
	if got := guiAddr(); got != "127.0.0.1:8790" {
		t.Errorf("guiAddr() = %q, want 127.0.0.1:8790", got)
	}
}

// The session token is a secret: it must be unguessable and unreadable by other
// local users, which is the only defence against a local (non-browser) attacker.
func TestGUIToken(t *testing.T) {
	a, err := newGUIToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newGUIToken()
	if a == b {
		t.Error("tokens must not repeat")
	}
	if len(a) < 32 {
		t.Errorf("token too short (%d chars)", len(a))
	}

	dir := t.TempDir()
	path := guiTokenPath(dir)
	if err := writeGUIToken(path, a); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 0600 (other local users must not read it)", perm)
	}
	if got := readGUIToken(path); got != a {
		t.Errorf("readGUIToken = %q, want %q", got, a)
	}
	if got := readGUIToken(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("a missing token file should read as empty, got %q", got)
	}
}

// A trailing newline (an editor, a shell redirect) must not break the token.
func TestReadGUITokenTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := guiTokenPath(dir)
	if err := os.WriteFile(path, []byte("  deadbeef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readGUIToken(path); got != "deadbeef" {
		t.Errorf("readGUIToken = %q, want %q", got, "deadbeef")
	}
}

// `ytdl -b` must keep spawning a HEADLESS daemon: the GUI (and its listening
// socket) is opt-in per spawn, so the CLI-only path opens no socket at all.
func TestDaemonGUIFlagIsOptIn(t *testing.T) {
	if strings.Contains(usageOfDaemonSpawn(t), "--gui") {
		t.Error("the plain queue daemon must not be spawned with --gui")
	}
}

// usageOfDaemonSpawn documents intent: run.runBackground uses daemon.Spawn (no
// flag) while only runGUICmd uses daemon.SpawnGUI. Asserted by reading the
// source rather than launching processes, which a unit test must not do.
func usageOfDaemonSpawn(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../internal/run/runner.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
