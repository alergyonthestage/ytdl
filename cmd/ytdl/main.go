// Command ytdl is a thin CLI front-end over the shared core: it parses the
// command line, resolves settings, checks dependencies, and dispatches to the
// runtime layer. It owns the process exit codes.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/cli"
	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/daemon"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
	"github.com/alergyonthestage/ytdl/internal/run"
	"github.com/alergyonthestage/ytdl/internal/term"
	"github.com/alergyonthestage/ytdl/internal/webui"
)

// colorStdout reports whether the inspection commands should colour their output
// (a TTY, NO_COLOR unset). Computed at the edge; the cli renderers stay pure.
func colorStdout() bool { return term.ColorEnabled(os.Stdout) }

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
	// Ensure installer-provisioned tools are found even without a configured
	// PATH (Finder, an app, a never-reopened Terminal) — done first, like the
	// Bash tool, so it applies to every action.
	run.PrependLocalBin()

	// The hidden daemon role is intercepted before the user-facing parser: it is
	// the queue drainer the enqueue path self-exec's into, not a command a user
	// types (absent from --help).
	if len(args) > 0 && args[0] == daemon.DaemonSubcommand {
		return runDaemon(args[1:])
	}

	parsed, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		var pe *cli.ParseError
		if errors.As(err, &pe) && pe.Usage {
			fmt.Fprintln(os.Stderr)
			fmt.Fprint(os.Stderr, cli.Usage)
		}
		return 1
	}

	switch parsed.Action {
	case cli.ActionHelp:
		fmt.Print(cli.Usage)
		return 0
	case cli.ActionVersion:
		return run.ShowVersion(os.Stdout)
	case cli.ActionUpdate:
		return run.Update()
	case cli.ActionQueue:
		return runQueueCmd(parsed.QueueWatch)
	case cli.ActionStatus:
		return runStatusCmd()
	case cli.ActionHistory:
		return runHistoryCmd(parsed)
	case cli.ActionGUI:
		return runGUICmd()
	case cli.ActionCancel:
		return runCancelCmd(parsed)
	case cli.ActionRetry:
		return runRetryCmd(parsed)
	}

	// ActionRun. C1: a bad -f flag fails fast (unlike the config file, which
	// warns and falls through — the deliberate asymmetry of §2.4).
	if parsed.Format != nil && !config.ValidFormat(*parsed.Format) {
		fmt.Fprintf(os.Stderr, cli.MsgInvalidFormat+"\n", *parsed.Format, config.FormatList)
		return 1
	}

	settings, warns := resolveSettings(parsed)
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "! %s\n", w.String())
	}

	// A download needs a usable destination. If $HOME is unresolvable the default
	// output dir degrades to a CWD-relative path; fail fast for run actions rather
	// than writing somewhere unexpected. Help/version already returned above and
	// stay permissive; an explicit absolute -o / $YTDL_OUT_DIR / config dir is honoured.
	if _, err := os.UserHomeDir(); err != nil && !filepath.IsAbs(settings.OutputDir) {
		fmt.Fprintln(os.Stderr, "✗ Impossibile determinare la cartella di destinazione: $HOME non è impostata.")
		fmt.Fprintln(os.Stderr, "  Imposta $HOME oppure indica una cartella assoluta con -o /percorso.")
		return 1
	}

	if err := run.CheckDeps(); err != nil {
		tool := err.Error()
		var de *run.DepError
		if errors.As(err, &de) {
			tool = de.Tool
		}
		fmt.Fprint(os.Stderr, run.MissingDepMessage(tool))
		return 1
	}

	o := core.Options{
		Mode:     parsed.RunMode,
		URL:      parsed.URL,
		Settings: settings,
		// Playlist mode is on via the -p flag or the persistent config default.
		Playlist: parsed.Playlist || settings.PlaylistDefault,
	}
	return run.Dispatch(o, os.Stdout, os.Stderr)
}

// resolveSettings builds the layer Partials and resolves them. The session layer
// is empty in Cycle 1; the env layer honours only $YTDL_OUT_DIR.
func resolveSettings(parsed *cli.Parsed) (config.Settings, []config.Warning) {
	return resolveWithFlags(config.Partial{OutputDir: parsed.OutputDir, Format: parsed.Format})
}

// resolveWithFlags overlays the config file and env onto the given flag layer. The
// daemon calls it with an empty flag layer (it only needs the config-file values,
// notably concurrency).
func resolveWithFlags(flags config.Partial) (config.Settings, []config.Warning) {
	var file config.Partial
	var warns []config.Warning
	if path := config.ConfigPath(); path != "" {
		fp, fw, err := config.LoadFile(path)
		if err != nil {
			warns = append(warns, config.Warning{Msg: fmt.Sprintf("impossibile leggere il config %s: %v", path, err)})
		} else {
			file = fp
			warns = append(warns, fw...)
		}
	}

	env := config.Env{OutDir: os.Getenv("YTDL_OUT_DIR")}
	settings, rwarns := config.Resolve(flags, config.Partial{}, file, env)
	warns = append(warns, rwarns...)
	return settings, warns
}

// runDaemon is the hidden __daemon role: drain the queue under the configured
// concurrency cap and, when spawned with --gui, also serve the web interface.
// It exits once the queue is drained AND no GUI client is connected (ADR-0008).
// It runs detached with /dev/null stdio, so it prints nothing; per-job
// logs/notifications flow through the same silent-mode path a CLI download uses.
// ErrAlreadyRunning is a clean exit (another daemon owns the queue).
//
// The GUI is opt-in per spawn: a daemon started by `ytdl -b` stays headless and
// opens no listening socket, so the CLI-only path keeps its pre-Cycle-3 surface.
func runDaemon(daemonArgs []string) int {
	stateDir := config.StatePath()
	if stateDir == "" {
		return 1
	}
	gui := false
	for _, a := range daemonArgs {
		if a == daemon.GUIFlag {
			gui = true
		}
	}

	settings, _ := resolveWithFlags(config.Partial{})
	sp := queue.Open(stateDir)
	cfg := daemon.Config{
		Spool:         sp,
		Concurrency:   settings.Concurrency,
		RetentionDays: settings.LogRetentionDays,
		JobTimeout:    time.Duration(settings.JobTimeout) * time.Second,
		LogPath:       daemonLogPath(stateDir),
	}

	var srv *webui.Server
	if gui {
		token, err := newGUIToken()
		if err != nil {
			return 1
		}
		srv = webui.New(webui.Deps{
			Spool:         sp,
			ConfigPath:    config.ConfigPath(),
			Resolve:       resolveWithFlags,
			LogDir:        settings.LogDir,
			RetentionDays: settings.LogRetentionDays,
			// Honest liveness: this process may be serving the UI while a headless
			// daemon spawned by `ytdl -b` still owns the queue.
			DaemonRunning: func() bool { return daemon.IsRunning(sp.LockPath()) },
			Token:         token,
		})
		cfg.LiveClients = srv.HasClients
		// Set explicitly (Serve would only default its own copy) because serveGUI
		// reads it too, to decide how long to keep retrying the queue lock.
		cfg.FirstClientGrace = daemon.DefaultFirstClientGrace
		cfg.Run = jobRunner(sp, srv)

		// The user asked for the interface, so THIS process owns the port and
		// serves immediately — publishing the token first, so `ytdl gui` can read
		// it the moment the port answers.
		_ = writeGUIToken(guiTokenPath(stateDir), token)
		defer os.Remove(guiTokenPath(stateDir))
		stopWeb := startWebUI(srv)
		defer stopWeb()

		return serveGUI(cfg, srv)
	}

	cfg.Run = jobRunner(sp, nil) // headless: no GUI, so no progress sink (title write-back still runs)
	if err := daemon.Serve(cfg); err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
		return 1
	}
	return 0
}

// serveGUI runs the interface and takes over the queue when it can.
//
// Port ownership and queue ownership are separate: a headless daemon spawned by
// an earlier `ytdl -b` may already hold the queue lock. Rather than give up —
// which would make `ytdl gui` fail for as long as that daemon drains — we keep
// serving the UI (enqueue, queue, history and settings all work through the
// shared spool and config; only live progress needs queue ownership) and retry
// the lock until the incumbent idle-exits, then start draining ourselves.
//
// We stop retrying once no client is connected and the first-client grace has
// passed, so an unused GUI daemon does not linger (ADR-0008: no idle process).
func serveGUI(cfg daemon.Config, srv *webui.Server) int {
	started := time.Now()
	for {
		err := daemon.Serve(cfg)
		if err == nil {
			return 0 // we owned the queue and idle-exited per ADR-0008
		}
		if !errors.Is(err, daemon.ErrAlreadyRunning) {
			return 1
		}
		if !srv.HasClients() && time.Since(started) > cfg.FirstClientGrace {
			return 0
		}
		time.Sleep(time.Second)
	}
}

// guiTokenPath is the 0600 file a GUI daemon publishes its session token in, for
// `ytdl gui` to hand to the browser. It never leaves the user's state dir.
func guiTokenPath(stateDir string) string { return filepath.Join(stateDir, "gui.token") }

// daemonLogPath is the daemon's diagnostics log, beside the queue/logs in the
// state dir. `ytdl status` points the user at it.
func daemonLogPath(stateDir string) string { return filepath.Join(stateDir, "daemon.log") }

func newGUIToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeGUIToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token), 0o600)
}

func readGUIToken(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// jobRunner builds the daemon's per-job executor: one queued job through the
// shared runtime layer in silent mode — the same core.BuildArgs, log store,
// breadcrumb and notification wiring as an interactive `ytdl -s`, so queued
// downloads behave identically. When the GUI is being served, each job also gets
// a progress sink keyed by its spool id, so the browser can draw a live bar. A
// headless daemon (every `ytdl -b`) passes a nil srv, so the sink is nil, no
// progress flags are added and execution is byte-for-byte the pre-GUI path.
func jobRunner(sp *queue.Spool, srv *webui.Server) func(context.Context, queue.Claim) int {
	return func(ctx context.Context, cl queue.Claim) int {
		j := cl.Job
		o := core.Options{Mode: core.ModeSilent, URL: j.URL, Settings: j.Settings, Playlist: j.Playlist}
		var sink run.ProgressSink
		if srv != nil {
			sink = func(ev run.ProgressEvent) {
				srv.PublishProgress(cl.ID, webui.Progress{
					Status:  ev.Status,
					Percent: ev.Percent,
					Speed:   ev.Speed,
					ETA:     ev.ETA,
					Title:   ev.Title,
				})
			}
		}
		// Persist the resolved title onto the running spool job, so `ytdl queue`
		// names the in-flight job and `ytdl retry` names a failed one, rather than
		// only showing a URL (Cycle 4). Skipped for a playlist, whose per-item
		// before_dl title would misrepresent the whole job.
		var onTitle func(string)
		if !j.Playlist {
			onTitle = func(title string) { _ = sp.SetTitle(cl.ID, title) }
		}
		return run.RunQueued(ctx, o, sink, onTitle)
	}
}

// startWebUI binds the loopback GUI port and serves the interface, returning a
// shutdown func. It runs from daemon.AfterLock, so the caller already owns the
// queue. A bind failure is non-fatal: the daemon simply drains headlessly.
func startWebUI(srv *webui.Server) func() {
	ln, err := net.Listen("tcp", guiAddr())
	if err != nil {
		return func() {}
	}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
		// No WriteTimeout on purpose: SSE responses are long-lived by design.
	}
	go hs.Serve(ln)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}
}

// guiPort is the loopback port the GUI listens on: webui.DefaultPort unless
// $YTDL_GUI_PORT overrides it. It is deliberately not a config-file key —
// editing the GUI's own port from inside the GUI would be circular.
func guiPort() int {
	if v := os.Getenv("YTDL_GUI_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
		// The daemon is silent by design, but a user who set the variable and got
		// the default port anyway deserves to know why.
		fmt.Fprintf(os.Stderr, "! YTDL_GUI_PORT=%q non è una porta valida; uso %d.\n", v, webui.DefaultPort)
	}
	return webui.DefaultPort
}

func guiAddr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(guiPort())) }

// runGUICmd opens the web interface: it starts a GUI-serving daemon if nothing
// of ours is listening yet, waits for it, then hands the browser a URL carrying
// the session token. The URL is always printed, so the user can open it manually.
func runGUICmd() int {
	stateDir := config.StatePath()
	if stateDir == "" {
		fmt.Fprintln(os.Stderr, "✗ Impossibile individuare la coda: $HOME non è impostata.")
		return 1
	}
	addr := guiAddr()

	listening, ours := guiProbe(addr)
	if listening && !ours {
		// Someone else holds the port. Without this check we would send the
		// browser to a stranger's server and report success while never starting
		// the engine at all.
		fmt.Fprintf(os.Stderr, "✗ La porta %d è occupata da un altro programma.\n", guiPort())
		fmt.Fprintln(os.Stderr, "  Scegli un'altra porta, per esempio:")
		fmt.Fprintln(os.Stderr, "      YTDL_GUI_PORT=8790 ytdl gui")
		return 1
	}
	if !listening {
		if err := daemon.SpawnGUI(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Impossibile avviare il motore: %v\n", err)
			return 1
		}
		if !waitForGUI(addr, 10*time.Second) {
			fmt.Fprintln(os.Stderr, "✗ Il motore non ha aperto l'interfaccia in tempo.")
			fmt.Fprintln(os.Stderr, "  Riprova con `ytdl gui`.")
			return 1
		}
	}

	url := "http://" + addr + "/"
	if token := readGUIToken(guiTokenPath(stateDir)); token != "" {
		url += "?t=" + token
	}

	fmt.Printf("▸ Interfaccia ytdl su http://%s/\n", addr)
	if err := openBrowser(url); err != nil {
		fmt.Println("  Non sono riuscito ad aprire il browser da solo; apri questo indirizzo:")
		fmt.Printf("      %s\n", url)
	}
	fmt.Println("  Il motore resta attivo finché la pagina è aperta; si chiude da solo")
	fmt.Println("  quando la chiudi e la coda è vuota.")
	return 0
}

// guiProbe reports whether anything accepts on addr and whether it is a ytdl
// GUI, identified by the marker header every response carries. Distinguishing
// the two is what stops `ytdl gui` from pointing the browser at an unrelated
// program that merely happens to hold the port.
func guiProbe(addr string) (listening, isYtdl bool) {
	c, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
	if err != nil {
		return false, false
	}
	c.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		return true, false
	}
	defer resp.Body.Close()
	return true, resp.Header.Get(webui.MarkerHeader) != ""
}

// waitForGUI polls until OUR interface answers or the deadline passes — the
// daemon is spawned detached, so there is no process to wait on.
func waitForGUI(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ours := guiProbe(addr); ours {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// openBrowser hands the URL to the platform launcher (macOS `open`, Linux
// `xdg-open`). Best-effort: the caller has already printed the URL.
func openBrowser(url string) error {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	default:
		return fmt.Errorf("piattaforma non supportata")
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return err
	}
	return exec.Command(cmd, url).Start()
}

// runQueueCmd prints the live queue once, or (with --watch on a TTY) redraws it
// in place until the queue drains or the user interrupts. On a non-TTY stdout,
// --watch degrades to a single snapshot — no ANSI, no loop — so piping stays
// sane. It resumes a stalled daemon (ADR-0007 §4) and opportunistically prunes
// the terminal spool + log store on read.
func runQueueCmd(watch bool) int {
	stateDir := config.StatePath()
	if stateDir == "" {
		fmt.Fprintln(os.Stderr, "✗ Impossibile individuare la coda: $HOME non è impostata.")
		return 1
	}
	sp := queue.Open(stateDir)
	settings, _ := resolveWithFlags(config.Partial{})
	pruneStores(sp, settings)

	if !watch || !term.IsTTY(os.Stdout) {
		return printQueueOnce(sp)
	}
	return watchQueue(sp)
}

// printQueueOnce reads and prints one queue snapshot — the non-watch path and the
// non-TTY --watch fallback — resuming a stalled daemon.
func printQueueOnce(sp *queue.Spool) int {
	snap, err := sp.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Impossibile leggere la coda: %v\n", err)
		return 1
	}
	resumeIfStalled(sp, snap)
	fmt.Print(term.Colorize(cli.RenderQueue(snap), colorStdout()))
	return 0
}

// watchQueue redraws the live queue in place (ADR-0009 §5): it rewrites only its
// own region — cursor-up + clear-to-end — never clearing the whole screen or
// polluting scrollback, advancing a spinner for liveness (and rewriting just the
// spinner line while the queue is unchanged). It auto-exits when the queue drains
// with a session summary, hides the cursor for the duration, and restores it on
// every exit path including SIGINT.
func watchQueue(sp *queue.Spool) int {
	color := colorStdout()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	fmt.Print("\033[?25l")       // hide cursor
	defer fmt.Print("\033[?25h") // restore it on any return

	spin := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastContent string
	var lastLines int // total lines drawn last frame (queue + spinner)
	var baseDone, baseFailed int
	haveBase, sawWork := false, false

	for frame := 0; ; frame++ {
		snap, err := sp.List()
		if err != nil {
			fmt.Print("\033[?25h")
			fmt.Fprintf(os.Stderr, "\n✗ Impossibile leggere la coda: %v\n", err)
			return 1
		}
		resumeIfStalled(sp, snap)
		p, r, d, f := snap.Counts()
		if !haveBase {
			baseDone, baseFailed, haveBase = d, f, true
		}
		if p > 0 || r > 0 {
			sawWork = true
		}
		content := cli.RenderQueue(snap)

		// Auto-exit once the queue has drained: clear the region, print the final
		// (empty) queue, and — if we actually watched work finish — a session summary.
		if p == 0 && r == 0 {
			if lastLines > 0 {
				fmt.Printf("\033[%dA\033[0J", lastLines)
			}
			fmt.Print(term.Colorize(content, color))
			if sawWork {
				done, failed := max(0, d-baseDone), max(0, f-baseFailed) // floor: a concurrent prune could shrink the totals
				fmt.Printf("✓ Coda evasa — in questa sessione: %d %s · %d %s\n",
					done, cli.Plural(done, "completato", "completati"),
					failed, cli.Plural(failed, "fallito", "falliti"))
			}
			return 0
		}

		spinnerLine := fmt.Sprintf("  %c aggiornamento · Ctrl-C per uscire\n", spin[frame%len(spin)])
		switch {
		case lastLines == 0:
			fmt.Print(term.Colorize(content, color) + spinnerLine)
		case content == lastContent:
			fmt.Print("\033[1A\033[2K" + spinnerLine) // only the spinner changed
		default:
			fmt.Printf("\033[%dA\033[0J", lastLines)
			fmt.Print(term.Colorize(content, color) + spinnerLine)
		}
		lastContent = content
		lastLines = strings.Count(content, "\n") + 1

		select {
		case <-sig:
			fmt.Print("\033[?25h\n^C interrotto.\n")
			return 0
		case <-ticker.C:
		}
	}
}

// resumeIfStalled starts a daemon when the spool holds un-terminal work
// (pending or orphaned-running) but none is draining. Spawn is idempotent, so a
// harmless double-start is impossible; the IsRunning probe just avoids the churn
// of a spawn-then-immediately-exit when a daemon is already live.
func resumeIfStalled(sp *queue.Spool, snap queue.Snapshot) {
	p, r, _, _ := snap.Counts()
	if p+r > 0 && !daemon.IsRunning(sp.LockPath()) {
		_ = daemon.Spawn()
	}
}

// runCancelCmd cancels live work. A pending job is deleted outright; a running one
// gets a cancel marker the daemon turns into a process-group kill (ADR-0011). With
// neither a target nor --all it prints the numbered live queue to read an index
// off. A running job is already owned by a live daemon, so its watcher will act on
// the marker — no daemon needs starting here.
func runCancelCmd(p *cli.Parsed) int {
	stateDir := config.StatePath()
	if stateDir == "" {
		fmt.Fprintln(os.Stderr, "✗ Impossibile individuare la coda: $HOME non è impostata.")
		return 1
	}
	sp := queue.Open(stateDir)
	snap, err := sp.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Impossibile leggere la coda: %v\n", err)
		return 1
	}
	ordered := cli.LiveOrdered(snap)

	if !p.All && p.Target == "" {
		fmt.Print(term.Colorize(cli.RenderCancelList(snap), colorStdout()))
		return 0
	}

	var targets []queue.Entry
	if p.All {
		if targets = ordered; len(targets) == 0 {
			fmt.Println("▸ Niente da annullare.")
			return 0
		}
	} else {
		e, res := resolveTarget(p.Target, ordered)
		if res != targetOK {
			return reportUnresolved(p.Target, res)
		}
		targets = []queue.Entry{e}
	}

	failures := 0
	for _, e := range targets {
		was, err := cancelOne(sp, e)
		if err != nil {
			// The marker is how the daemon stops a running job, so a failure to write
			// it means the cancel is a no-op — say so rather than claim success.
			fmt.Fprintf(os.Stderr, "! Impossibile annullare %s: %v\n", jobLabel(e), err)
			failures++
			continue
		}
		if was {
			fmt.Printf("▸ Annullato (era in attesa): %s\n", jobLabel(e))
		} else {
			fmt.Printf("▸ Annullamento richiesto: %s\n", jobLabel(e))
		}
	}
	if p.All {
		fmt.Printf("▸ %d annullati o in annullamento.\n", len(targets)-failures)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// cancelOne cancels a single live entry: it drops the marker FIRST (so a job the
// daemon claims in the meantime is still stopped by the watcher), then deletes it
// if it is still pending. Returns wasPending=true if it was pending (deleted now),
// false if it was already running/gone (the marker is left for the daemon's
// watcher). A non-nil error means the cancel did NOT take effect (the marker
// could not be written, or the pending-delete hit a real filesystem error).
func cancelOne(sp *queue.Spool, e queue.Entry) (wasPending bool, err error) {
	if err := sp.RequestCancel(e.ID); err != nil {
		return false, err
	}
	was, err := sp.CancelPending(e.ID)
	if err != nil {
		return false, err
	}
	if was {
		_ = sp.ClearCancel(e.ID) // deleted before it ran → the marker is unneeded
	}
	return was, nil
}

// runRetryCmd re-queues failed jobs (failed/ → pending/) and resumes the daemon to
// drain them. With neither a target nor --all it prints the numbered failed list.
func runRetryCmd(p *cli.Parsed) int {
	stateDir := config.StatePath()
	if stateDir == "" {
		fmt.Fprintln(os.Stderr, "✗ Impossibile individuare la coda: $HOME non è impostata.")
		return 1
	}
	sp := queue.Open(stateDir)
	snap, err := sp.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Impossibile leggere la coda: %v\n", err)
		return 1
	}
	failed := snap.Failed
	settings, _ := resolveWithFlags(config.Partial{})
	enrichTitlesFromHistory(failed, settings.LogDir)

	if !p.All && p.Target == "" {
		fmt.Print(term.Colorize(cli.RenderRetryList(failed), colorStdout()))
		return 0
	}

	var targets []queue.Entry
	if p.All {
		if targets = failed; len(targets) == 0 {
			fmt.Println("▸ Nessun download fallito da riprovare.")
			return 0
		}
	} else {
		e, res := resolveTarget(p.Target, failed)
		if res != targetOK {
			return reportUnresolved(p.Target, res)
		}
		targets = []queue.Entry{e}
	}

	failures := 0
	for _, e := range targets {
		if err := sp.Retry(e.ID); err != nil {
			// TOCTOU: the job may have been pruned or already retried since the
			// listing (queue.Spool.Retry returns ErrNotExist). Report it and let the
			// exit code reflect it, so a script's `|| alert` actually fires.
			fmt.Fprintf(os.Stderr, "! Non riprovato %s: %v\n", jobLabel(e), err)
			failures++
			continue
		}
		fmt.Printf("▸ Rimesso in coda: %s\n", jobLabel(e))
	}
	succeeded := len(targets) - failures
	if succeeded > 0 {
		if fresh, err := sp.List(); err == nil {
			resumeIfStalled(sp, fresh) // start a daemon to drain the re-queued jobs
		}
		if p.All {
			fmt.Printf("▸ %d rimessi in coda.\n", succeeded)
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// enrichTitlesFromHistory fills in a display title for failed jobs that never got
// a run-time write-back — those that failed before yt-dlp resolved the metadata,
// or were queued before the write-back existed — by looking the URL up in the
// durable history (a prior attempt may have recorded it). Playlists are skipped:
// their per-item title would misrepresent the whole job. Best-effort — a miss just
// leaves the URL as the label. Only `retry` (a one-shot command) calls this, so the
// single history scan never burdens the live `queue --watch` redraw.
func enrichTitlesFromHistory(entries []queue.Entry, logDir string) {
	for i := range entries {
		e := &entries[i]
		if e.Job.Title == "" && !e.Job.Playlist && e.Job.URL != "" {
			if t := logstore.TitleForURL(logDir, e.Job.URL); t != "" {
				e.Job.Title = t
			}
		}
	}
}

// targetResult is the outcome of resolving a cancel/retry target.
type targetResult int

const (
	targetOK        targetResult = iota // resolved to exactly one entry
	targetNotFound                      // no index/prefix match
	targetAmbiguous                     // an id-prefix matched more than one entry
)

// resolveTarget maps a cancel/retry target to an entry: a short integer (1-3
// digits) is a 1-based index into the numbered list the command prints; anything
// else is an id-prefix, accepted only when it UNIQUELY identifies one entry. It
// distinguishes not-found from ambiguous so the caller can advise the user
// precisely (type more characters vs. it isn't there).
func resolveTarget(target string, entries []queue.Entry) (queue.Entry, targetResult) {
	if isIndex(target) {
		n, _ := strconv.Atoi(target)
		if n >= 1 && n <= len(entries) {
			return entries[n-1], targetOK
		}
		return queue.Entry{}, targetNotFound
	}
	var match queue.Entry
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.ID, target) {
			match, count = e, count+1
		}
	}
	switch count {
	case 1:
		return match, targetOK
	case 0:
		return queue.Entry{}, targetNotFound
	default:
		return queue.Entry{}, targetAmbiguous
	}
}

// reportUnresolved prints the right message for a non-OK resolution and returns 1.
func reportUnresolved(target string, res targetResult) int {
	if res == targetAmbiguous {
		fmt.Fprintf(os.Stderr, cli.MsgTargetAmbiguous+"\n", target)
	} else {
		fmt.Fprintf(os.Stderr, cli.MsgTargetNotFound+"\n", target)
	}
	return 1
}

// isIndex reports whether s is a short list index (1-3 digits) rather than an
// id-prefix. Spool ids begin with a long nanosecond number, so capping the index
// at three digits keeps a pasted numeric id from being misread as an index.
func isIndex(s string) bool {
	if len(s) == 0 || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// jobLabel is a short human label for a cancel/retry confirmation line.
func jobLabel(e queue.Entry) string {
	if e.Job.URL != "" {
		return e.Job.URL
	}
	return e.ID
}

// runStatusCmd prints a one-shot summary: daemon liveness (informational), the
// live queue, and a recent windowed outcome tally from the log store (which
// includes foreground downloads, unlike the background-only spool).
func runStatusCmd() int {
	stateDir := config.StatePath()
	if stateDir == "" {
		fmt.Fprintln(os.Stderr, "✗ Impossibile individuare la coda: $HOME non è impostata.")
		return 1
	}
	sp := queue.Open(stateDir)
	snap, err := sp.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Impossibile leggere la coda: %v\n", err)
		return 1
	}
	settings, _ := resolveWithFlags(config.Partial{})
	pruneStores(sp, settings)
	recent := recentSummary(settings.LogDir, settings.LogRetentionDays)
	fmt.Print(term.Colorize(cli.RenderStatus(snap, daemon.IsRunning(sp.LockPath()), recent, settings.LogRetentionDays), colorStdout()))
	// Point at the daemon's diagnostics log, but only once it exists — nothing to
	// cite before the daemon has ever run.
	if lp := daemonLogPath(stateDir); fileExists(lp) {
		fmt.Printf("  diagnostica: %s\n", lp)
	}
	return 0
}

// fileExists reports whether path is a regular, statable file.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runHistoryCmd prints the durable download history from the log store —
// foreground and background alike — newest first, within the retention window.
func runHistoryCmd(p *cli.Parsed) int {
	settings, _ := resolveWithFlags(config.Partial{})
	if settings.LogDir == "" {
		fmt.Fprintln(os.Stderr, "✗ Impossibile individuare lo storico: $HOME non è impostata.")
		return 1
	}
	_ = logstore.Prune(settings.LogDir, settings.LogRetentionDays) // keep the window honest
	var since time.Time
	if settings.LogRetentionDays > 0 {
		since = time.Now().AddDate(0, 0, -settings.LogRetentionDays)
	}
	entries, err := logstore.Load(settings.LogDir, logstore.QueryOpts{
		Since:      since,
		Limit:      p.HistoryLimit,
		OnlyFailed: p.HistoryFailed,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Impossibile leggere lo storico: %v\n", err)
		return 1
	}
	fmt.Print(term.Colorize(cli.RenderHistory(entries, settings.LogRetentionDays), colorStdout()))
	return 0
}

// recentSummary tallies success/failure from the log store within the retention
// window (all-time when retention is keep-forever), for the `status` summary.
func recentSummary(logDir string, retentionDays int) cli.RecentSummary {
	var since time.Time
	if retentionDays > 0 {
		since = time.Now().AddDate(0, 0, -retentionDays)
	}
	entries, _ := logstore.Load(logDir, logstore.QueryOpts{Since: since})
	var s cli.RecentSummary
	for _, e := range entries {
		if e.Success {
			s.OK++
		} else {
			s.Failed++
		}
	}
	return s
}

// pruneStores opportunistically ages out the terminal spool and the log store on
// a read path, best-effort — a prune failure must never fail the command.
func pruneStores(sp *queue.Spool, settings config.Settings) {
	_ = sp.PruneTerminal(settings.LogRetentionDays)
	_ = logstore.Prune(settings.LogDir, settings.LogRetentionDays)
}
