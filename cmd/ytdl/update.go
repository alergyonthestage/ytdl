package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/daemon"
	"github.com/alergyonthestage/ytdl/internal/update"
	"github.com/alergyonthestage/ytdl/internal/webui"
)

// guiTokenEnv carries the GUI session token across a handover.
//
// Without it the new daemon would generate a fresh token and answer every API
// call with "401 … riapri l'interfaccia con `ytdl gui`" — a Terminal, which is
// exactly the acceptance test failing. The variable is readable only by the same
// user, who can already read the 0600 token file, so it adds no exposure class
// that did not already exist (ADR-0016 §9).
const guiTokenEnv = "YTDL_GUI_TOKEN"

// handoverGrace is how long the outgoing daemon keeps serving after the update
// finished, so the polling page can read the "done" frame from the process that
// produced it. Missing it is survivable — the run record is on disk and the new
// daemon serves the same answer — but seeing it is what makes the sequence read
// as one continuous operation rather than as the server disappearing.
const handoverGrace = 2 * time.Second

// guiUpdater is the update capability as the GUI daemon offers it. It owns no
// policy: internal/update decides what an update is, and webui decides how to
// show it.
type guiUpdater struct {
	stateDir string
	runner   *update.Runner
	settings func() config.Settings

	// deps is this machine's local facts, refreshed at the events that can change
	// them — construction, the reconcile that follows it, an explicit check, and
	// the end of an installer run — rather than on every /api/state. Asking yt-dlp
	// its version costs 7.33 s on a user's Mac, and the state endpoint is on the
	// page's load path.
	//
	// CONSTRUCTION fills it from the installer's record and execs nothing: it runs
	// before the port is bound, so every millisecond it takes is time the browser
	// spends waiting on a daemon that does not answer yet (ADR-0019 §2). The probe
	// still happens — runDaemon schedules refreshLocal once the port is open —
	// which is what keeps a stale marker from being believed for ever.
	//
	// The DEPENDENCY list is what is held, not the flattened Installed: the page
	// needs "present but no version recorded" and "not there at all" to stay
	// different facts, and flattening loses that (V12). Installed is derived from
	// it by the same InstalledFrom both channels use.
	mu   sync.Mutex
	deps []update.Dependency

	// web is the HTTP server whose listener the handover closes. It is published
	// after construction, because the Updater has to exist before the server it
	// is injected into.
	web atomic.Pointer[http.Server]
}

func newGUIUpdater(stateDir string, settings func() config.Settings) *guiUpdater {
	u := &guiUpdater{
		stateDir: stateDir,
		runner:   update.NewRunner(stateDir),
		settings: settings,
		deps:     update.RecordedDependencies(stateDir),
	}
	return u
}

// Deps is the local facts as the surfaces that SHOW them need,
// and local() is the same walk flattened for the ones that COMPARE.
func (u *guiUpdater) Deps() []update.Dependency {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.deps
}

func (u *guiUpdater) local() update.Installed {
	return update.InstalledFrom(u.Deps())
}

func (u *guiUpdater) refreshLocal() {
	deps := update.Dependencies(u.stateDir, true)
	u.mu.Lock()
	u.deps = deps
	u.mu.Unlock()
}

// Verdict pairs the remembered REMOTE answer with the local facts.
//
// The local half is filled in even when no round has ever completed, because the
// installed versions are always shown whether or not the probe ever succeeded
// (ADR-0016 §8) — and because the page's reload trigger compares the ytdl version
// the server reports, which therefore has to be present at all times.
func (u *guiUpdater) Verdict() (update.Verdict, bool) {
	v, ok := update.Load(u.stateDir)
	v.Installed = u.local()
	return v, ok
}

// Check runs one round now and caches it. It works with update_check off: that
// key is consent for the machine to phone home by itself, not a restriction on
// the user's right to ask (ADR-0016 §6).
func (u *guiUpdater) Check(ctx context.Context) (update.Verdict, error) {
	v, err := update.Check(ctx, nil, u.stateDir, time.Now())
	// The round already read the local facts; re-walk them so the SHOW shape and
	// the COMPARE shape stay the same walk rather than two.
	u.refreshLocal()
	if v.Known() {
		// Only a complete round is cached, so a source that stayed silent cannot
		// destroy the last known result and its date.
		_ = update.Save(u.stateDir, v)
	}
	return v, err
}

func (u *guiUpdater) Start(force bool) error    { return u.runner.Start(force) }
func (u *guiUpdater) Progress() update.Progress { return u.runner.Progress() }
func (u *guiUpdater) Enabled() bool             { return u.settings().UpdateCheck }

// Running reports whether an installer THIS daemon launched is still in flight.
// It is what daemonAlive consults, and it is deliberately the in-memory answer:
// the run record on disk describes any process, this one describes ours.
func (u *guiUpdater) Running() bool { return u.runner.Running() }

// daemonAlive is ADR-0008's lifetime rule with the installer's own duration added
// to it: alive while a GUI client is connected OR while an update this process
// launched is still running.
//
// The second clause exists because a connected client is not the only thing this
// daemon owes something to. It also owes the RECORD of how the update went, and
// only the process that launched the installer can write it — update.Runner
// waits on the child and calls finish. Without this clause, closing the tab
// mid-update drops the SSE client, the drained queue idle-exits the daemon after
// 20 s, the setsid'd installer finishes anyway with nobody left to notice, and
// update-run.json stays at "running": the update path is then refused with a
// reason the user can neither see nor act on. Silent, deferred, and permanent
// until the record is recognised as abandoned (V1, design §7.3).
//
// Both halves are injected rather than reached for, which is what keeps this
// composition-root work: internal/daemon takes cfg.LiveClients and is untouched.
func daemonAlive(hasClients, updating func() bool) func() bool {
	return func() bool { return hasClients() || updating() }
}

// handOver replaces this daemon with the binary the installer has just put down.
//
// It runs only when the ytdl binary actually changed: after the installer became
// idempotent the common update is a re-pinned yt-dlp, which costs no restart at
// all and leaves the page exactly where it was (design §5).
//
// This is the third exit cause ADR-0016 §9 adds to ADR-0008: the daemon exits
// because it was ASKED to. It is not an idle-exit, and it does not weaken the
// clause it sits beside — the tab watching the update IS a connected client, so
// the ordinary exit test would keep this process alive for ever.
func handOver(stateDir, token string, hs *http.Server) {
	time.Sleep(handoverGrace)

	// 1. Close, NOT Shutdown. Shutdown waits for active connections, and an SSE
	//    connection never ends — it would block for ever. Close frees the port
	//    deterministically BEFORE the child is spawned, so there is no bind race
	//    to lose.
	if hs != nil {
		_ = hs.Close()
	}

	// 2. The token travels in the environment (see guiTokenEnv).
	if err := spawnGUIWithToken(token); err != nil {
		// Leave this daemon serving rather than leave the user with nothing: the
		// update itself succeeded, and the page can still be told so.
		logDaemon(stateDir, "handover: could not start the new interface: "+err.Error())
		return
	}

	// 3. os.Exit skips defers ON PURPOSE. runDaemon defers os.Remove(gui.token),
	//    and that file now belongs to the child, which has just written the same
	//    value into it. Stated here because it is otherwise an accident waiting to
	//    be "cleaned up".
	// 4. Process exit also releases the queue flock, which is held on a descriptor
	//    inside daemon.Serve that cmd/ytdl cannot reach — and does not need to.
	//    The child's serveGUI already retries the lock while serving the UI,
	//    because Cycle 3 built it for the "a headless daemon holds the queue"
	//    case; the handover reuses that, unchanged.
	os.Exit(0)
}

// spawnGUIWithToken self-exec's into the GUI daemon role with the session token
// in the environment.
//
// It replicates daemon.spawn rather than extending it, deliberately:
// internal/daemon is untouched this cycle, and the handover is composition-root
// work by ADR-0016's own account. os.Executable resolves the path the installer
// has just replaced, so this starts the NEW binary.
func spawnGUIWithToken(token string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, daemon.DaemonSubcommand, daemon.GUIFlag)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), guiTokenEnv+"="+token)
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		defer devnull.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// sessionToken is the token this GUI daemon will use: the one handed over by an
// outgoing daemon when there is one, else a fresh one. An ordinary `ytdl gui`
// always gets a fresh token, because nothing set the variable.
func sessionToken() (string, error) {
	if t := os.Getenv(guiTokenEnv); t != "" {
		return t, nil
	}
	return newGUIToken()
}

// logDaemon appends one line to the daemon's diagnostics log. The daemon runs
// with /dev/null stdio, so this is the only way anything it notices can reach a
// person — and `ytdl status` points at the file.
func logDaemon(stateDir, msg string) {
	if stateDir == "" {
		return
	}
	f, err := os.OpenFile(daemonLogPath(stateDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(time.Now().Format(time.RFC3339) + " " + msg + "\n")
}

// wireHandover arms the handover that follows a run which replaced the binary.
//
// The HTTP server does not exist yet when this is called — the Updater has to be
// injected into it before it is built — so it is published afterwards through an
// atomic pointer. Atomic rather than plain, because the handover runs on the
// installer's waiter goroutine.
func (u *guiUpdater) wireHandover(token string) {
	u.runner.OnFinish = func(run update.Run) {
		u.refreshLocal()
		if shouldHandOver(run) {
			handOver(u.stateDir, token, u.web.Load())
		}
	}
}

// shouldHandOver reports whether a finished run leaves this process running the
// wrong binary.
//
// Only a SUCCESSFUL run that replaced ytdl itself does. After the installer
// became idempotent the common update is a re-pinned yt-dlp, which costs no
// restart at all and leaves the page exactly where it was; and a failed run
// changed nothing, so restarting into "the same binary, but the user lost their
// page" would be pure cost (design §5, §7.3).
func shouldHandOver(run update.Run) bool {
	return run.State == update.StateDone && run.Changed
}

// setWeb publishes the HTTP server whose listener the handover must close.
func (u *guiUpdater) setWeb(hs *http.Server) { u.web.Store(hs) }

// Compile-time proof that the capability the GUI is handed is the one it asks
// for; a missing method would otherwise only show up when the daemon runs.
var _ webui.Updater = (*guiUpdater)(nil)
