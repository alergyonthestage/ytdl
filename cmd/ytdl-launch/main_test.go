package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bundle builds a plausible Contents/MacOS/YTDL layout in a temp dir and returns
// the launcher's own path. Tests that care about resolution need the real shape,
// because the sidecar is located RELATIVE TO THE EXECUTABLE and nothing else.
func bundle(t *testing.T) (exe, resources string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "YTDL.app", "Contents")
	macos := filepath.Join(root, "MacOS")
	resources = filepath.Join(root, "Resources")
	for _, d := range []string{macos, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe = filepath.Join(macos, "YTDL")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, resources
}

func writeSidecar(t *testing.T, resources, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(resources, sidecarName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubYtdl writes an executable that records the argv it was given and answers
// with the given output and exit status.
func stubYtdl(t *testing.T, dir, argvLog, prints string, exit int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ytdl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvLog + "'\n"
	if prints != "" {
		script += "printf '%s' '" + prints + "'\n"
	}
	script += "exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// testLauncher wires a launcher at a temp state dir with the alert captured
// rather than shown, so every branch is reachable with no osascript and no Finder.
func testLauncher(t *testing.T, exe string) (*launcher, *string) {
	t.Helper()
	var shown string
	l := &launcher{
		exe:       exe,
		stateDir:  t.TempDir(),
		timeout:   10 * time.Second,
		waitDelay: 500 * time.Millisecond,
		now:       func() time.Time { return time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC) },
		showAlert: func(script string) { shown = script },
	}
	return l, &shown
}

// An absolute sidecar wins. It is what lets the development sandbox have its own
// bundle without it launching the real, installed ytdl.
func TestLauncherPrefersTheSidecarPath(t *testing.T) {
	exe, resources := bundle(t)
	want := filepath.Join(t.TempDir(), "sandbox", "ytdl")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSidecar(t, resources, want+"\n")

	if got := resolveYtdl(exe); got != want {
		t.Errorf("resolveYtdl = %q, want the sidecar's %q", got, want)
	}
}

// Missing, empty, blank or RELATIVE all fall back. A relative path would be
// resolved against the working directory, which for a Finder launch is "/" —
// exactly the resolution this launcher must never do.
func TestLauncherFallsBackToTheDefaultBinPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".local", "bin", "ytdl")

	t.Run("no sidecar at all", func(t *testing.T) {
		exe, _ := bundle(t)
		if got := resolveYtdl(exe); got != want {
			t.Errorf("resolveYtdl = %q, want %q", got, want)
		}
	})
	for name, content := range map[string]string{
		"empty":        "",
		"blank":        "   \n",
		"relative":     "bin/ytdl\n",
		"dot relative": "./ytdl\n",
		"parent walk":  "../ytdl\n",
	} {
		t.Run(name, func(t *testing.T) {
			exe, resources := bundle(t)
			writeSidecar(t, resources, content)
			if got := resolveYtdl(exe); got != want {
				t.Errorf("resolveYtdl(%q) = %q, want the default %q", content, got, want)
			}
		})
	}
}

// The bundle survives being dragged to the Dock, the Desktop or a folder of the
// user's own: resolution reads the executable's own directory and never the
// working directory. A ytdl sitting in the CWD must not be picked up.
func TestLauncherNeverResolvesRelativeToItself(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exe, _ := bundle(t)

	// A decoy beside the launcher, and another in the working directory.
	decoy := filepath.Join(filepath.Dir(exe), "ytdl")
	if err := os.WriteFile(decoy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "ytdl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// t.Chdir needs go1.24 and this module is go1.22, so restore it by hand.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	got := resolveYtdl(exe)
	if got == decoy {
		t.Error("resolved the binary sitting beside the launcher")
	}
	if got == filepath.Join(cwd, "ytdl") {
		t.Error("resolved relative to the working directory")
	}
	if want := filepath.Join(home, ".local", "bin", "ytdl"); got != want {
		t.Errorf("resolveYtdl = %q, want %q", got, want)
	}
}

// Fixed argv, no shell. Nothing read from a file can become an argument: the
// sidecar is a PATH, and the command line is this launcher's, not its content's.
func TestLauncherRunsExactlyYtdlGui(t *testing.T) {
	exe, resources := bundle(t)
	argv := filepath.Join(t.TempDir(), "argv")
	ytdl := stubYtdl(t, t.TempDir(), argv, "", 0)
	writeSidecar(t, resources, ytdl+"\n")

	l, _ := testLauncher(t, exe)
	if code := l.run(); code != 0 {
		t.Fatalf("run = %d, want 0", code)
	}

	got, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "gui" {
		t.Errorf("argv = %q, want exactly \"gui\"", string(got))
	}
}

// Success shows nothing. An "everything worked" pop-up on every launch would be
// a worse surface than the silence it replaces.
func TestLauncherIsSilentOnSuccess(t *testing.T) {
	exe, resources := bundle(t)
	ytdl := stubYtdl(t, t.TempDir(), filepath.Join(t.TempDir(), "argv"), "tutto bene", 0)
	writeSidecar(t, resources, ytdl+"\n")

	l, shown := testLauncher(t, exe)
	if code := l.run(); code != 0 {
		t.Fatalf("run = %d, want 0", code)
	}
	if *shown != "" {
		t.Errorf("an alert was built on success: %q", *shown)
	}
}

// A child that SUCCEEDED is a success, whatever Wait had to say about the pipe.
//
// CombinedOutput reads through an os.Pipe, so anything the child spawned that
// inherited that pipe keeps Wait reading after the child is gone; WaitDelay ends
// the read and returns exec.ErrWaitDelay, which is NOT an *exec.ExitError.
// Deciding the status from the error's type therefore reported exit 1 for a
// launch that worked — a critical alert, in Italian, on top of a browser that
// had just opened. The status comes from the process (design §3.2: "the
// launcher's exit status mirrors the child's").
//
// `ytdl gui` does not do this today — daemon.spawn gives its child /dev/null
// stdio and openBrowser leaves Stdout nil, which Go also sends to /dev/null —
// so this pins the launcher's half of the contract, which must not depend on
// that staying true.
func TestLauncherIsSilentWhenAGrandchildHoldsThePipe(t *testing.T) {
	exe, resources := bundle(t)
	dir := t.TempDir()
	ytdl := filepath.Join(dir, "ytdl")
	// Exits 0 immediately, leaving a background process holding stdout.
	script := "#!/bin/sh\n( /bin/sleep 30 ) &\necho 'interfaccia aperta'\nexit 0\n"
	if err := os.WriteFile(ytdl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSidecar(t, resources, ytdl+"\n")

	l, shown := testLauncher(t, exe)

	code := l.run()

	if code != 0 {
		t.Errorf("run = %d: the child exited 0, so the launcher must too", code)
	}
	if *shown != "" {
		t.Errorf("an alert was shown for a launch that succeeded: %q", *shown)
	}
	raw, err := os.ReadFile(filepath.Join(l.stateDir, logName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "exit=0") {
		t.Errorf("the log recorded a failure for a successful launch: %q", string(raw))
	}
}

// One message catalogue. The port-taken case must read the Italian `ytdl gui`
// already writes — the launcher transports that message, it does not author a
// second voice for the same failure (ux-principles §7).
func TestLauncherAlertCarriesTheChildsOwnMessage(t *testing.T) {
	exe, resources := bundle(t)
	const childSaid = "La porta 8765 è occupata da un altro programma."
	ytdl := stubYtdl(t, t.TempDir(), filepath.Join(t.TempDir(), "argv"), childSaid, 3)
	writeSidecar(t, resources, ytdl+"\n")

	l, shown := testLauncher(t, exe)
	if code := l.run(); code != 3 {
		t.Errorf("run = %d, want the child's 3", code)
	}
	if !strings.Contains(*shown, childSaid) {
		t.Errorf("the alert dropped the child's own message.\ngot: %s", *shown)
	}
	if !strings.Contains(*shown, logName) {
		t.Errorf("the alert does not name the log file.\ngot: %s", *shown)
	}
}

// Silence still produces something readable. A child that fails without saying
// why must not become an empty alert.
func TestLauncherAlertFallsBackWhenTheChildSaidNothing(t *testing.T) {
	exe, resources := bundle(t)
	ytdl := stubYtdl(t, t.TempDir(), filepath.Join(t.TempDir(), "argv"), "", 1)
	writeSidecar(t, resources, ytdl+"\n")

	l, shown := testLauncher(t, exe)
	if code := l.run(); code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(*shown, "ytdl gui") {
		t.Errorf("the fallback line is missing; the user is told nothing.\ngot: %s", *shown)
	}
}

// Quotes, backslashes and newlines cannot break out of the AppleScript literal
// or inject a second statement. The child's output is DATA.
func TestLauncherAlertEscapesAppleScript(t *testing.T) {
	hostile := "say \"pwned\"\nend tell\\ntrailing \" quote"
	script := alertScript(hostile, "/state/launcher.log")

	// A raw newline would terminate the AppleScript statement, so the child's line
	// breaks must survive as the two characters \n INSIDE the literal.
	if strings.Contains(script, "\n") {
		t.Errorf("a raw newline survived into the script: %q", script)
	}
	// Not one quote from the child may appear unescaped, or it closes the literal
	// and whatever follows becomes AppleScript.
	if strings.Contains(script, `"pwned"`) {
		t.Errorf("an unescaped quote pair survived: %s", script)
	}
	if !strings.Contains(script, `\"pwned\"`) {
		t.Errorf("the child's quotes were dropped rather than escaped: %s", script)
	}
	// A backslash the child wrote must not become an escape of ours: the literal
	// \n it typed stays two visible characters, it does not turn into a newline.
	if !strings.Contains(script, `end tell\\n`) {
		t.Errorf("a literal backslash from the child was not doubled: %s", script)
	}
	// And the whole thing is still one statement.
	if strings.Count(script, "display alert") != 1 {
		t.Errorf("the script is not exactly one alert: %s", script)
	}
}

// No ytdl at all: the alert fires, the exit is non-zero, and nothing is run.
func TestLauncherRefusesAMissingYtdl(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exe, _ := bundle(t)

	l, shown := testLauncher(t, exe)
	code := l.run()
	if code == 0 {
		t.Error("run = 0 with no ytdl to run")
	}
	if *shown == "" {
		t.Error("no alert for a launcher that could not find its engine")
	}
}

// A wedged child is killed rather than left holding a Dock tile that never goes
// away. `ytdl gui` bounds itself well below this, so reaching the timeout at all
// means something is broken.
func TestLauncherTimesOutAWedgedChild(t *testing.T) {
	exe, resources := bundle(t)
	dir := t.TempDir()
	wedged := filepath.Join(dir, "ytdl")
	if err := os.WriteFile(wedged, []byte("#!/bin/sh\nwhile : ; do /bin/sleep 1 ; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSidecar(t, resources, wedged+"\n")

	l, shown := testLauncher(t, exe)
	l.timeout = 300 * time.Millisecond

	start := time.Now()
	code := l.run()
	elapsed := time.Since(start)

	if code == 0 {
		t.Error("a killed child reported success")
	}
	if elapsed > 10*time.Second {
		t.Errorf("run took %s: the child was not killed", elapsed)
	}
	if *shown == "" {
		t.Error("no alert after a timeout")
	}
}

// The proven half of the visible failure path always records — success and
// failure alike. It is the mechanism the gate-A probe used to learn anything at
// all on the target platform, and it does not depend on the alert reaching the
// front of the screen.
func TestLauncherLogsBothOutcomes(t *testing.T) {
	for name, tc := range map[string]struct {
		prints string
		exit   int
	}{
		"success": {"", 0},
		"failure": {"non parte", 4},
	} {
		t.Run(name, func(t *testing.T) {
			exe, resources := bundle(t)
			ytdl := stubYtdl(t, t.TempDir(), filepath.Join(t.TempDir(), "argv"), tc.prints, tc.exit)
			writeSidecar(t, resources, ytdl+"\n")

			l, _ := testLauncher(t, exe)
			l.run()

			raw, err := os.ReadFile(filepath.Join(l.stateDir, logName))
			if err != nil {
				t.Fatalf("no %s was written: %v", logName, err)
			}
			line := string(raw)
			if !strings.Contains(line, ytdl) {
				t.Errorf("the log does not say which ytdl was run: %q", line)
			}
			if !strings.Contains(line, "2026-08-27") {
				t.Errorf("the log has no timestamp: %q", line)
			}
			if tc.exit != 0 && !strings.Contains(line, tc.prints) {
				t.Errorf("a failure did not record the child's output: %q", line)
			}
		})
	}
}

// The log APPENDS: a second launch must not erase what the first recorded, or
// the one mechanism proven on hardware only ever holds the last attempt.
func TestLauncherLogAppends(t *testing.T) {
	exe, resources := bundle(t)
	ytdl := stubYtdl(t, t.TempDir(), filepath.Join(t.TempDir(), "argv"), "", 0)
	writeSidecar(t, resources, ytdl+"\n")

	l, _ := testLauncher(t, exe)
	l.run()
	l.run()

	raw, err := os.ReadFile(filepath.Join(l.stateDir, logName))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(raw)), "\n") + 1; got != 2 {
		t.Errorf("the log holds %d lines after two launches, want 2", got)
	}
}
