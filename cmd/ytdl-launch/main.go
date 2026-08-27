// Command ytdl-launch is the Mach-O inside YTDL.app: the thing a double-click
// runs. It resolves the installed ytdl by absolute path, runs `ytdl gui`, and
// makes a failure visible to somebody who has no Terminal open.
//
// It is deliberately NOT ytdl itself (ADR-0019 §1). Two reasons decided it: the
// bundle must be cheap to leave alone, and ytdl's bytes move on every release —
// a bundle wrapping the engine would be rewritten on every update, churning the
// Dock entry, the Spotlight identity and any alias the user made. And ytdl gui
// spawns its daemon from os.Executable(), so a bundle copy serving the interface
// in process would spawn its daemon from the binary the last update did not
// replace.
//
// It owns NO policy. Ports, tokens, daemons, browsers and every message about
// them belong to `ytdl gui`, which both channels share; this program transports
// that command's own words to a surface Finder can show, and authors none of
// them (ADR-0019 Rationale 2).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
)

const (
	// sidecarName is the file install.sh writes into Contents/Resources holding
	// the absolute path of the ytdl this bundle must launch. It is a PATH and
	// never a command line: the argv below is fixed, so nothing in a file can
	// become an argument.
	sidecarName = "ytdl-path"

	// logName is the half of the visible failure path that does not depend on an
	// alert reaching the front of the screen. The gate-A probe learned everything
	// it learned this way, so it is known to work on the target platform.
	logName = "launcher.log"

	// guiCmdTimeout bounds a wedged child. `ytdl gui` bounds ITSELF at waitForGUI's
	// 10 s plus a browser spawn, so this is six times its own worst case and
	// exists only so a hung engine cannot leave a Dock tile that never goes away.
	guiCmdTimeout = 60 * time.Second

	// guiWaitDelay bounds how long we keep reading the child's output after the
	// child itself is gone. A grandchild that inherited the pipe must not keep
	// this process — and its Dock tile — alive.
	guiWaitDelay = 5 * time.Second

	alertHeading  = "YTDL non è riuscito ad aprire l'interfaccia"
	alertFallback = "Il motore non ha risposto. Riprova; se il problema resta, apri il Terminale e scrivi: ytdl gui"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		// Without our own path there is no sidecar to read; the default below
		// still applies, so this is degraded rather than fatal.
		exe = ""
	}
	l := &launcher{
		exe:       exe,
		stateDir:  config.StatePath(),
		timeout:   guiCmdTimeout,
		waitDelay: guiWaitDelay,
		now:       time.Now,
		showAlert: showAlert,
	}
	os.Exit(l.run())
}

// launcher holds what this program needs from its environment, so every branch
// is reachable from a test with no app bundle, no osascript and no real ytdl.
type launcher struct {
	exe       string // this binary's own path, from os.Executable
	stateDir  string // where launcher.log is appended
	timeout   time.Duration
	waitDelay time.Duration
	now       func() time.Time
	showAlert func(script string)
}

// run is the whole program. Its return value is the child's exit status: Finder
// discards it, a test does not.
func (l *launcher) run() int {
	ytdl := resolveYtdl(l.exe)

	if !executableFile(ytdl) {
		l.fail(ytdl, 1, fmt.Sprintf("Non trovo ytdl in %s.\nReinstalla ytdl e riprova.", ytdl))
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	// exec.Command, never syscall.Exec: replacing the process image discards the
	// exit status and the output, which are the two things the alert is made of.
	cmd := exec.CommandContext(ctx, ytdl, "gui")
	// A grandchild holding the pipe must not keep this process — and its Dock
	// tile — alive after the child itself is killed.
	cmd.WaitDelay = l.waitDelay
	out, err := cmd.CombinedOutput()

	code := 0
	if err != nil {
		// The status comes from the PROCESS, not from the error's type. Wait
		// reports ErrWaitDelay — which is not an *ExitError — when the child
		// exited but something it spawned still held the pipe; reading the error
		// alone would call that failure and pop a critical alert on a launch that
		// worked. ProcessState is nil only when the child never started at all.
		code = 1
		if st := cmd.ProcessState; st != nil {
			code = st.ExitCode()
		}
		if ctx.Err() != nil {
			// A timeout is our verdict, not the child's: say so rather than
			// reporting whatever signal ended it.
			out = append(out, []byte("\nL'avvio non si è concluso entro "+l.timeout.String()+".")...)
		}
	}

	if code == 0 {
		// Success shows nothing. An "everything worked" pop-up on every launch
		// would be a worse surface than the silence it replaces.
		l.log(ytdl, 0, "")
		return 0
	}
	l.fail(ytdl, code, string(out))
	return code
}

// fail records the attempt and shows the child's own message. The log is written
// FIRST and unconditionally: it is the remedy that is proven on hardware, and it
// must not depend on the one that is not.
func (l *launcher) fail(ytdl string, code int, childSaid string) {
	l.log(ytdl, code, childSaid)
	if l.showAlert != nil {
		l.showAlert(alertScript(childSaid, filepath.Join(l.stateDir, logName)))
	}
}

// log appends one line per launch: when, which ytdl, how it ended, and on a
// failure what it said. Folded to a single line so the file stays greppable by
// somebody reading it over the user's shoulder.
func (l *launcher) log(ytdl string, code int, childSaid string) {
	if l.stateDir == "" {
		return
	}
	_ = os.MkdirAll(l.stateDir, 0o700)
	f, err := os.OpenFile(filepath.Join(l.stateDir, logName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf("%s ytdl=%s exit=%d", l.now().UTC().Format(time.RFC3339), ytdl, code)
	if s := fold(childSaid); s != "" {
		line += " output=" + s
	}
	fmt.Fprintln(f, line)
}

// resolveYtdl finds the engine this bundle must launch: the sidecar when it
// names an ABSOLUTE path, else the installer's default location.
//
// The sidecar is located from the executable's own directory and never from the
// working directory, which for a Finder launch is "/" — that is what lets the
// user drag the bundle to the Dock, the Desktop or a folder of their own and
// have it keep working (ADR-0018 §1).
//
// A relative path is REFUSED rather than made absolute: it could only be
// resolved against a working directory this program deliberately ignores.
func resolveYtdl(exe string) string {
	if exe != "" {
		// Contents/MacOS/<launcher> -> Contents/Resources/ytdl-path
		sidecar := filepath.Join(filepath.Dir(exe), "..", "Resources", sidecarName)
		if raw, err := os.ReadFile(sidecar); err == nil {
			if p := strings.TrimSpace(string(raw)); filepath.IsAbs(p) {
				return filepath.Clean(p)
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "ytdl"
	}
	return filepath.Join(home, ".local", "bin", "ytdl")
}

// alertScript builds the one AppleScript statement the failure path shows. It is
// pure so the escaping can be tested without an osascript on the machine.
//
// The escaping rule differs from internal/notify's escapeAS ON PURPOSE: a
// notification body is one line, so notify folds newlines to spaces; an alert
// must keep the child's lines apart, so a newline becomes the two characters \n
// INSIDE the AppleScript literal. A raw one would end the statement.
func alertScript(childSaid, logPath string) string {
	body := strings.TrimSpace(childSaid)
	if body == "" {
		body = alertFallback
	}
	body += "\n\nDettagli in " + logPath
	return `display alert "` + escapeAS(alertHeading) + `" message "` + escapeAS(body) + `" as critical`
}

// escapeAS makes arbitrary text safe inside an AppleScript double-quoted
// literal. The backslash goes first, or it would escape the escapes added after
// it and the child's own text could still close the literal.
func escapeAS(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\r\n", `\n`,
		"\n", `\n`,
		"\r", `\n`,
		" ", `\n`, // U+2028 line separator
		" ", `\n`, // U+2029 paragraph separator
	).Replace(s)
}

// showAlert is the only impure sink besides the child itself. A failure to show
// it is ignored: the log has already recorded everything, and a launcher that
// died reporting a failure would be worse than one that stayed quiet.
func showAlert(script string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

// fold flattens multi-line output onto one log line.
func fold(s string) string {
	f := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	return f
}

// executableFile reports whether path is a regular file with an execute bit —
// the same test internal/update applies, restated here because the launcher
// imports no engine code.
func executableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}
