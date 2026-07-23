// Command ytdl is a thin CLI front-end over the shared core: it parses the
// command line, resolves settings, checks dependencies, and dispatches to the
// runtime layer. It owns the process exit codes.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
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
		return runDaemon()
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
// concurrency cap, then idle-exit. It runs detached with /dev/null stdio, so it
// prints nothing; per-job logs/notifications flow through the same silent-mode
// path a CLI download uses. ErrAlreadyRunning is a clean exit (another daemon is
// already draining).
func runDaemon() int {
	stateDir := config.StatePath()
	if stateDir == "" {
		return 1
	}
	settings, _ := resolveWithFlags(config.Partial{})
	err := daemon.Serve(daemon.Config{
		Spool:         queue.Open(stateDir),
		Concurrency:   settings.Concurrency,
		Run:           jobRunner,
		RetentionDays: settings.LogRetentionDays,
	})
	if err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
		return 1
	}
	return 0
}

// jobRunner executes one queued job through the shared runtime layer in silent
// mode — the same core.BuildArgs, log store, breadcrumb and notification wiring
// as an interactive `ytdl -s`, so queued downloads behave identically.
func jobRunner(j queue.Job) int {
	o := core.Options{Mode: core.ModeSilent, URL: j.URL, Settings: j.Settings, Playlist: j.Playlist}
	return run.Dispatch(o, io.Discard, io.Discard)
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
	return 0
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
