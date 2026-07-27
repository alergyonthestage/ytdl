// Package run is ytdl's runtime layer: it executes yt-dlp, manages temp files,
// writes the silent-mode failure log, enqueues background downloads to the
// on-demand daemon, and checks dependencies — the behaviours the pure argv
// goldens do not cover. All user-facing runtime strings live here, verbatim from
// the Bash ytdl.
package run

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/daemon"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/notify"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// ytDlp and ffmpeg are the external tools ytdl drives.
const ytDlp = "yt-dlp"
const ffmpeg = "ffmpeg"

// stderrCap bounds how much yt-dlp stderr is kept for the central log store, so
// a pathological run cannot balloon a record (or memory) unbounded.
const stderrCap = 256 * 1024

// notifier is the completion-notification backend (U6). It is a package var so
// tests can inject a capturing fake; production uses the platform default
// (osascript on macOS, no-op elsewhere).
var notifier notify.Notifier = notify.Default()

// spawnDaemon starts the on-demand queue daemon (Cycle 2B). It is a package var
// so tests can stub the self-exec, which would otherwise launch a real detached
// process from the test binary.
var spawnDaemon = daemon.Spawn

// PrependLocalBin puts $HOME/.local/bin at the front of PATH if absent, so the
// installer-provisioned yt-dlp/ffmpeg are found even when ytdl is launched from
// Finder or a never-reopened Terminal (ytdl lines 20-23).
func PrependLocalBin() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	dir := filepath.Join(home, ".local", "bin")
	path := os.Getenv("PATH")
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return
		}
	}
	if path == "" {
		os.Setenv("PATH", dir)
		return
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+path)
}

// DepError reports a missing dependency, carrying the exact label the Bash tool
// used in its message.
type DepError struct{ Tool string }

func (e *DepError) Error() string { return e.Tool }

// CheckDeps requires both yt-dlp and ffmpeg on PATH (parity: both are hard
// requirements). The ffmpeg label matches the Bash message (ytdl line 188).
func CheckDeps() error {
	if _, err := exec.LookPath(ytDlp); err != nil {
		return &DepError{Tool: "yt-dlp"}
	}
	if _, err := exec.LookPath(ffmpeg); err != nil {
		return &DepError{Tool: "ffmpeg (serve per estrarre l'audio e incorporare la copertina)"}
	}
	return nil
}

// MissingDepMessage returns the multi-line stderr message for a missing tool
// (ytdl lines 176-185).
func MissingDepMessage(tool string) string {
	return fmt.Sprintf("✗ %s non trovato.\n\n"+
		"  Le dipendenze di ytdl sembrano mancanti o incomplete.\n"+
		"  Per reinstallarle:\n\n"+
		"      ytdl --update\n", tool)
}

// Dispatch runs the resolved options in the appropriate mode and returns the
// process exit code. It performs the mkdir the download modes need (all but
// dry-run), matching ytdl line 241.
func Dispatch(o core.Options, stdout, stderr io.Writer) int {
	if o.Mode != core.ModeDryRun {
		if err := os.MkdirAll(o.Settings.OutputDir, 0o755); err != nil {
			fmt.Fprintf(stderr, "✗ Impossibile creare la cartella %s: %v\n", o.Settings.OutputDir, err)
			return 1
		}
	}
	switch o.Mode {
	case core.ModeDryRun:
		return runDryRun(o, stdout)
	case core.ModeBackground:
		return runBackground(o, stdout, stderr)
	case core.ModeVerbose:
		return runVerbose(o, stdout)
	case core.ModeSilent:
		return runSilent(o)
	default:
		return runDefault(o, stdout)
	}
}

// botCheckSignature marks YouTube's anti-bot challenge in yt-dlp's stderr
// ("Sign in to confirm you're not a bot"). Matched on the prefix before the
// apostrophe, which yt-dlp emits as a curly ’ (U+2019), not a straight quote.
const botCheckSignature = "Sign in to confirm you"

// runDryRun previews filenames without downloading (ytdl lines 224-235). Preview
// is the one mode that runs yt-dlp with an effectively-simulate extraction (no
// --no-simulate, no -x), so YouTube can challenge it independently of a download
// that would succeed. Rather than tee yt-dlp's raw stderr, we capture it and, on
// failure, print a mediated message (the bot-check gets a specific hint). The
// printed filenames still stream to stdout; yt-dlp's own exit code propagates.
func runDryRun(o core.Options, w io.Writer) int {
	fmt.Fprintf(w, "▸ Anteprima (nessun download) — formato .%s\n", o.Settings.Format)
	fmt.Fprintf(w, "▸ Destinazione: %s\n\n", o.Settings.OutputDir)
	var errbuf capBuffer
	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
	cmd.Stdout = w
	cmd.Stderr = &errbuf
	rc := runCode(cmd)
	if rc == 0 {
		// Preserve any notices yt-dlp emitted on a SUCCESSFUL preview (parity with
		// the old raw tee); only a failure is mediated, to hide the bot-check dump.
		os.Stderr.Write(errbuf.Bytes())
	} else {
		fmt.Fprint(os.Stderr, dryRunErrorMessage(errbuf.Bytes()))
	}
	return rc
}

// dryRunErrorMessage turns captured yt-dlp stderr into a legible preview-failure
// message. The YouTube anti-bot challenge is recognised and reassures the user
// that the real download usually still works; any other failure shows its last
// stderr line rather than the full dump.
func dryRunErrorMessage(stderr []byte) string {
	s := string(stderr)
	if strings.Contains(s, botCheckSignature) || strings.Contains(s, "not a bot") {
		return "✗ Anteprima non disponibile: YouTube ha chiesto una verifica anti-bot per leggere i soli metadati.\n" +
			"  Il download vero di solito funziona lo stesso — riprova senza -n.\n"
	}
	if last := lastNonEmptyLine(s); last != "" {
		return fmt.Sprintf("✗ Anteprima non riuscita: %s\n", last)
	}
	return "✗ Anteprima non riuscita.\n"
}

// lastNonEmptyLine returns the last non-blank line of s (yt-dlp's final "ERROR:"
// line is usually the useful one), trimmed; "" when s has no non-blank line.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// runVerbose downloads with full yt-dlp output (ytdl lines 256-267). The success
// line prints only when yt-dlp succeeded (the Bash `set -e` aborts otherwise).
func runVerbose(o core.Options, w io.Writer) int {
	fmt.Fprintf(w, "▸ Scarico (.%s) → %s  [verbose]\n\n", o.Settings.Format, o.Settings.OutputDir)
	work, cleanWork := workDir()
	defer cleanWork()
	var errbuf capBuffer
	cmd := exec.Command(ytDlp, withTempRedirect(core.BuildArgs(o), o, work)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &errbuf)
	rc := runCode(cmd)
	success := rc == 0
	if success {
		fmt.Fprintf(w, "\n✓ Fatto → %s\n", o.Settings.OutputDir)
	}
	now := time.Now()
	// Verbose has no saved-file capture, so it can report neither a title nor a
	// path/count — its history record says what ran and how it ended, no more.
	recordJob(o, outcome{Mode: "verbose", RC: rc, Success: success, Stderr: errbuf.Bytes(), Now: now})
	maybeNotify(o, true, success, 0) // foreground; verbose has no saved-file count
	return rc
}

// runDefault downloads with a progress bar, printing the title immediately and
// reporting the saved path(s) at the end (ytdl lines 310-345).
func runDefault(o core.Options, w io.Writer) int {
	fmt.Fprintf(w, "▸ Scarico (.%s) → %s\n", o.Settings.Format, o.Settings.OutputDir)

	saved, cleanup, err := tempFile("ytdl-saved-")
	if err != nil {
		return tempFileError(err)
	}
	defer cleanup()
	o.SavedFile = saved

	work, cleanWork := workDir()
	defer cleanWork()

	var errbuf capBuffer
	cmd := exec.Command(ytDlp, withTempRedirect(core.BuildArgs(o), o, work)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &errbuf)
	rc := runCode(cmd)

	first, count := readSavedPaths(saved)
	fmt.Fprintln(w)
	switch {
	case count == 1:
		fmt.Fprintf(w, "✓ Salvata in: %s\n", first)
	case count > 1:
		fmt.Fprintf(w, "✓ %d tracce salvate in: %s\n", count, o.Settings.OutputDir)
	default:
		fmt.Fprintf(w, "✗ Nessun file scaricato (rc=%d).\n", rc)
		fmt.Fprintf(w, "  (se l'URL conteneva &, controlla che fosse tra virgolette)\n")
	}
	success := rc == 0 && count > 0
	now := time.Now()
	recordJob(o, outcome{
		Mode: "default", Title: titleFromPath(first), RC: rc, Success: success,
		SavedPath: first, Count: count, Stderr: errbuf.Bytes(), Now: now,
	})
	maybeNotify(o, true, success, count) // foreground
	return rc
}

// runSilent produces no terminal output. It records the job centrally and, on
// failure, drops an in-destination breadcrumb (single, or one per failed
// playlist item), keyed by a stable hash so a later success cleans it up (U7).
// A completion notification fires per config (U6). It is also the mode the
// background launcher re-executes into, so background inherits all of this.
// The interactive `ytdl -s` is not cancellable — it runs in the foreground under
// the user's own shell, so a background context and no process-group isolation
// preserve its pre-2B-plus behaviour (Ctrl-C reaches it via the terminal group).
func runSilent(o core.Options) int { return runSilentSink(context.Background(), o, nil, false, nil) }

// RunQueued executes a queued job in silent mode with optional live-progress
// capture — the daemon's execution entry point (Cycle 3 GUI; Cycle 2B-plus
// cancel/timeout). With a non-nil sink, yt-dlp is asked to emit machine-parseable
// progress (appended AFTER core.BuildArgs, off the golden argv path) and every
// update is forwarded to sink for SSE streaming, while the failure .log stays
// free of progress lines. ctx is the daemon's per-job context: cancelling it (an
// external `ytdl cancel` or the job timeout) tears down the whole yt-dlp/ffmpeg
// process group. A nil sink still behaves like runSilent for output, but the run
// is cancellable. It performs the pre-download mkdir that Dispatch does for the
// interactive silent mode.
//
// onTitle, when non-nil, is invoked with the resolved "Artist - Track" as soon as
// yt-dlp's before_dl hook writes it (and once more at the end), so the daemon can
// persist it onto the running job — naming it in `ytdl queue`/`retry` (Cycle 4).
// It works regardless of sink, since the title comes from the before_dl temp file
// that is part of the ordinary metadata pipeline, not from the progress stream.
func RunQueued(ctx context.Context, o core.Options, sink ProgressSink, onTitle func(string)) int {
	if err := os.MkdirAll(o.Settings.OutputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Impossibile creare la cartella %s: %v\n", o.Settings.OutputDir, err)
		return 1
	}
	return runSilentSink(ctx, o, sink, true, onTitle)
}

// runSilentSink is runSilent's body, parameterised by an optional progress sink
// and whether the run is cancellable. A nil sink means no progress args (yt-dlp
// stderr goes straight to the failure log, unchanged); cancellable=true wires
// ctx to a process-group teardown so an external cancel or the job timeout stops
// the whole yt-dlp/ffmpeg tree. A ctx-driven stop (cancel or timeout) is NOT a
// genuine download failure: it drops no .log breadcrumb and is recorded under a
// distinct mode ("cancelled"/"timeout") so history shows what happened.
func runSilentSink(ctx context.Context, o core.Options, sink ProgressSink, cancellable bool, onTitle func(string)) int {
	// Register each cleanup immediately after its own successful creation, so a
	// later failure still removes the temp files already made.
	saved, cleanSaved, err := tempFile("ytdl-saved-")
	if err != nil {
		return tempFileError(err)
	}
	defer cleanSaved()
	title, cleanTitle, err := tempFile("ytdl-title-")
	if err != nil {
		return tempFileError(err)
	}
	defer cleanTitle()
	errlog, cleanErr, err := tempFile("ytdl-err-")
	if err != nil {
		return tempFileError(err)
	}
	defer cleanErr()

	o.TitleFile = title
	o.SavedFile = saved

	work, cleanWork := workDir()
	defer cleanWork()

	// Route intermediates (.part, fragments, the embed thumbnail, pre-conversion
	// audio) into the scratch dir so a failed/cancelled queued download leaves no
	// residue in the destination — only the final file on success, or the .log
	// breadcrumb below (ADR-0011). Appended after BuildArgs, off the golden path.
	args := withTempRedirect(core.BuildArgs(o), o, work)

	// Per-item playlist tracking (U7): append extra yt-dlp print sinks AFTER
	// BuildArgs — pure runtime instrumentation, off the golden argv path — only
	// when we will actually reconcile per-item breadcrumbs for a playlist.
	trackItems := o.Settings.BreadcrumbOnFailure && o.Playlist
	var attemptedFile, succeededFile string
	if trackItems {
		var c1, c2 func()
		attemptedFile, c1, err = tempFile("ytdl-att-")
		if err != nil {
			return tempFileError(err)
		}
		defer c1()
		succeededFile, c2, err = tempFile("ytdl-suc-")
		if err != nil {
			return tempFileError(err)
		}
		defer c2()
		args = append(args,
			"--print-to-file", logstore.AttemptedItemTemplate, attemptedFile,
			"--print-to-file", logstore.SucceededItemTemplate, succeededFile)
	}

	// Live-progress capture (GUI): ask yt-dlp for machine-parseable progress on
	// stderr, appended after BuildArgs so the golden argv is untouched. The
	// progressStderr splitter routes those lines to the sink and everything else
	// to the failure log, keeping the .log identical to a no-progress run.
	if sink != nil {
		args = append(args, progressArgs()...)
	}

	ef, err := os.OpenFile(errlog, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Errore file temporaneo: %v\n", err)
		return 1
	}
	cmd := exec.CommandContext(ctx, ytDlp, args...)
	var markReaped func()
	if cancellable {
		markReaped = cancellableProcessGroup(cmd)
	}
	cmd.Stdout = nil // → /dev/null
	cmd.Stderr = ef
	var em *progressEmitter
	var pout, perr *progressSplitter
	if sink != nil {
		// yt-dlp splits its progress across BOTH streams: the `download:` template
		// goes to stdout and `postprocess:` to stderr (verified against yt-dlp
		// 2026.07.04). Route each through a splitter sharing one emitter — stdout's
		// non-progress output is discarded exactly as /dev/null did, and stderr's
		// still reaches the failure log byte-for-byte.
		em = newProgressEmitter(sink)
		pout = newProgressSplitter(io.Discard, em)
		perr = newProgressSplitter(ef, em)
		cmd.Stdout, cmd.Stderr = pout, perr
	}
	// Title write-back (Cycle 4): watch the before_dl temp file and report the
	// resolved "Artist - Track" the moment it lands, so the daemon can name a
	// still-running job in `ytdl queue`. Runs regardless of sink (headless too),
	// since the title comes from the metadata pipeline, not the progress stream.
	var titleWG sync.WaitGroup
	titleDone := make(chan struct{})
	if onTitle != nil {
		titleWG.Add(1)
		go func() {
			defer titleWG.Done()
			tk := time.NewTicker(300 * time.Millisecond)
			defer tk.Stop()
			for {
				select {
				case <-titleDone:
					return
				case <-tk.C:
					if s := lastLine(title); s != "" {
						onTitle(s)
						return
					}
				}
			}
		}()
		// Stop the watcher and report the final resolved title exactly once, on ANY
		// return path INCLUDING a panic — so a leaked goroutine's SetTitle can never
		// race the daemon's terminal rename (which happens only after RunQueued
		// returns). Deferred right after the goroutine starts, so it is LIFO-ordered
		// before the temp-file cleanups above: the join runs while the before_dl file
		// still exists, and it precedes the final flush, keeping the watcher's onTitle
		// and the flush's onTitle mutually exclusive. A fast job that finished before
		// the first tick is covered by this final flush.
		defer func() {
			close(titleDone)
			titleWG.Wait()
			if s := lastLine(title); s != "" {
				onTitle(s)
			}
		}()
	}

	rc := runCode(cmd)
	if markReaped != nil {
		markReaped() // child reaped by os/exec — stop the escalation SIGKILL firing
	}
	if em != nil {
		// cmd.Run has already joined os/exec's copy goroutines, so no write can
		// land after this point.
		pout.Flush()
		perr.Flush() // any buffered trailing (non-progress) line → the .log
		em.flush()   // replay the last throttled-away frame
	}
	ef.Close()

	// "Success" means yt-dlp exited 0 AND actually saved a file. Count non-blank
	// after_move lines once and reuse it for the record, the breadcrumb decision
	// and the notification — consistent with default mode (an empty or
	// blank-only saved list is a failure, matching the Bash tool's zero-files
	// case, ytdl lines 290-293).
	first, count := readSavedPaths(saved)
	success := rc == 0 && count > 0

	// A ctx cancel or timeout that stopped an unfinished job is not a genuine
	// download failure: record it under a distinct mode and skip the .log
	// breadcrumb, so a user-initiated cancel or a policy timeout never litters the
	// destination with a failure log (ADR-0011). Only intermediates in the scratch
	// dir are cleaned; a job that actually saved a file stays "silent".
	mode, stopped := "silent", false
	if cancellable && !success {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			mode, stopped = "timeout", true
		case ctx.Err() != nil:
			mode, stopped = "cancelled", true
		}
	}
	now := time.Now()

	// Title from the dedicated before_dl temp file (the "Artist - Track" contract,
	// core.silentBeforeDL), NOT the saved filename — the latter depends on the
	// user's name_template, the former does not. Empty on an early failure → the
	// history falls back to the URL.
	recordJob(o, outcome{
		Mode: mode, Title: lastLine(title), RC: rc, Success: success,
		SavedPath: first, Count: count, Stderr: readCapped(errlog, stderrCap), Now: now,
	})

	if o.Settings.BreadcrumbOnFailure && !stopped {
		if o.Playlist {
			logstore.ReconcilePlaylist(o.Settings.OutputDir, o.URL, rc, now,
				logstore.ParseAttempted(attemptedFile), logstore.ParseSucceededIDs(succeededFile))
		} else {
			key := logstore.Hash(logstore.NormalizeURL(o.URL))
			if success {
				logstore.RemoveBreadcrumb(o.Settings.OutputDir, key)
			} else {
				logstore.WriteBreadcrumb(o.Settings.OutputDir, lastLine(title), key, o.URL, rc, now)
			}
		}
	}

	maybeNotify(o, false, success, count) // silent/background: not gated on notify_foreground
	return rc
}

// runBackground enqueues the download onto the filesystem spool and ensures the
// on-demand daemon is running to drain it under the concurrency cap, then returns
// immediately (Cycle 2B, ADR-0007). It supersedes the pre-2B unbounded Setsid
// re-exec: five `ytdl -b` now share one queue instead of spawning five
// simultaneous downloads (U5). The queued job runs in silent mode, so it inherits
// the central log store, failure breadcrumb and completion notification.
func runBackground(o core.Options, stdout, stderr io.Writer) int {
	stateDir := config.StatePath()
	if stateDir == "" {
		fmt.Fprintln(stderr, "✗ Impossibile individuare la coda: $HOME non è impostata.")
		return 1
	}
	sp := queue.Open(stateDir)
	if _, err := sp.Enqueue(queue.Job{URL: o.URL, Playlist: o.Playlist, Settings: o.Settings}); err != nil {
		fmt.Fprintf(stderr, "✗ Impossibile accodare il download: %v\n", err)
		return 1
	}
	if err := spawnDaemon(); err != nil {
		// The job is safely queued; a later `ytdl -b` / `ytdl queue` will start a
		// daemon to drain it. Warn but do not fail — the download is not lost.
		fmt.Fprintf(stderr, "! Accodato, ma non ho potuto avviare il daemon: %v\n", err)
		fmt.Fprintln(stderr, "  Verrà ripreso al prossimo `ytdl -b`; controlla con `ytdl queue`.")
	}

	// Report jobs not yet terminal (pending + running), so the count stays truthful
	// even if an already-warm daemon claimed this job before we read the spool. On
	// a List error, omit the count rather than print a misleading "0".
	if snap, err := sp.List(); err == nil {
		p, r, _, _ := snap.Counts()
		fmt.Fprintf(stdout, "▸ Download accodato (%d in coda).\n", p+r)
	} else {
		fmt.Fprintln(stdout, "▸ Download accodato.")
	}
	fmt.Fprintf(stdout, "  Audio → %s\n", o.Settings.OutputDir)
	fmt.Fprintf(stdout, "  Stato: ytdl queue   ·   se fallisce, troverai un file .log accanto all'audio.\n")
	return 0
}

// ShowVersion prints ytdl's version and yt-dlp's, tolerating a missing or
// unresponsive yt-dlp (ytdl lines 101-108).
func ShowVersion(w io.Writer) int {
	fmt.Fprintf(w, "ytdl %s\n", buildinfo.Version)
	if _, err := exec.LookPath(ytDlp); err != nil {
		fmt.Fprintln(w, "yt-dlp non installato")
		return 0
	}
	out, err := exec.Command(ytDlp, "--version").Output()
	if err != nil {
		fmt.Fprintln(w, "yt-dlp (non risponde)")
		return 0
	}
	fmt.Fprintf(w, "yt-dlp %s\n", strings.TrimSpace(string(out)))
	return 0
}

// Update re-runs the installer, the single place the provisioning logic lives
// (ytdl lines 112-131). It streams `curl … | bash`.
func Update() int {
	repo := envOr("YTDL_REPO", "alergyonthestage/ytdl")
	branch := envOr("YTDL_BRANCH", "main")
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/install.sh", repo, branch)

	if _, err := exec.LookPath("curl"); err != nil {
		fmt.Fprintln(os.Stderr, "✗ curl non disponibile: impossibile aggiornare.")
		return 1
	}
	if _, err := exec.LookPath("bash"); err != nil {
		fmt.Fprintln(os.Stderr, "✗ bash non disponibile: impossibile aggiornare.")
		return 1
	}

	fmt.Println("▸ Aggiorno ytdl e yt-dlp…")
	fmt.Println()

	curl := exec.Command("curl", "-fsSL", "--retry", "3", "--connect-timeout", "20", url)
	sh := exec.Command("bash")
	pipe, err := curl.StdoutPipe()
	if err != nil {
		return updateFailed()
	}
	sh.Stdin = pipe
	sh.Stdout, sh.Stderr = os.Stdout, os.Stderr
	curl.Stderr = os.Stderr

	if err := sh.Start(); err != nil {
		pipe.Close() // curl.Wait() never runs to close it; avoid leaking the fds
		return updateFailed()
	}
	curlErr := curl.Run()
	shErr := sh.Wait()
	if curlErr != nil || shErr != nil {
		return updateFailed()
	}
	return 0
}

func updateFailed() int {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "✗ Aggiornamento fallito.")
	fmt.Fprintln(os.Stderr, "  Controlla la connessione e riprova. Se il problema persiste,")
	fmt.Fprintln(os.Stderr, "  reinstalla ytdl seguendo le istruzioni del README.")
	return 1
}

// runCode runs cmd and returns its exit code: 0 on success, the process exit
// code on a normal failure, 128+signal when the child was killed by a signal, and
// 1 if it could not be started. The 128+signal path matches the shell's
// convention (Bash `set -e`/`rc=$?` propagates e.g. 137 for SIGKILL, 130 for
// SIGINT): yt-dlp/ffmpeg are the memory-heavy, OOM-killable part of the pipeline,
// and a caller keying off 130/137 to tell "interrupted/killed" from "yt-dlp
// reported an error" would otherwise see every signal death collapse to a
// generic 1 (also under-reporting the cause in silent mode's .log).
func runCode(cmd *exec.Cmd) int {
	err := cmd.Run()
	// Read the exit status from ProcessState, NOT from err: for an
	// exec.CommandContext whose ctx was cancelled (a cancel or the job timeout),
	// os/exec returns the context error from Run() even when the child actually
	// exited 0 — because our Cancel func returns nil rather than ErrProcessDone. A
	// job that finishes cleanly within the SIGTERM grace window would otherwise be
	// misreported as a failure. ProcessState carries the true wait status
	// regardless of how Cancel decorates the returned error.
	if ps := cmd.ProcessState; ps != nil {
		if code := ps.ExitCode(); code >= 0 {
			return code
		}
		// ExitCode() is -1 for a signal death; recover the shell's 128+signal.
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	if err != nil {
		return 1 // never started (ProcessState is nil), or an unusual non-exit error
	}
	return 0
}

// killGrace is how long a cancelled or timed-out job's process group is given to
// exit on SIGTERM before it is SIGKILLed.
const killGrace = 5 * time.Second

// cancellableProcessGroup wires cmd — which MUST be an exec.CommandContext — so a
// context cancel (an external `ytdl cancel` or the per-job timeout) tears down the
// WHOLE process group: yt-dlp AND the ffmpeg child it spawns, not just yt-dlp.
// Setpgid makes the child its own group leader (pgid == its pid); on cancel we
// SIGTERM the group, then SIGKILL it after killGrace so a process ignoring
// SIGTERM cannot linger holding a file open in the destination. It returns a
// markReaped func the caller MUST invoke right after the child is reaped
// (cmd.Run returns): it stops the escalation timer and disarms it, so the
// group-SIGKILL only ever fires while the process is genuinely still alive.
// WaitDelay is set past killGrace so os/exec keeps the leader un-reaped until the
// SIGKILL has had its chance.
//
// Residual (accepted): a group-SIGKILL keyed by pgid could in principle hit a
// reused pgid if the child is reaped in the sub-microsecond gap between cmd.Run
// reaping it and markReaped disarming the timer, AND a concurrent job reuses that
// exact pid as a group leader in the same instant. Stopping the timer on reap
// plus the reaped guard narrows this to a few instructions; fully closing it needs
// pidfd (Linux ≥5.3), which the macOS target lacks, so it is left as documented risk.
func cancellableProcessGroup(cmd *exec.Cmd) (markReaped func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var reaped atomic.Bool
	var timer *time.Timer
	var mu sync.Mutex
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid // == pgid, since Setpgid made the child a group leader
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		t := time.AfterFunc(killGrace, func() {
			if !reaped.Load() {
				_ = syscall.Kill(-pgid, syscall.SIGKILL) // group SIGKILL; child still alive at killGrace
			}
		})
		mu.Lock()
		timer = t
		mu.Unlock()
		return nil
	}
	cmd.WaitDelay = killGrace + 2*time.Second
	return func() {
		reaped.Store(true)
		mu.Lock()
		if timer != nil {
			timer.Stop() // child reaped before the grace elapsed → cancel the escalation
		}
		mu.Unlock()
	}
}

func tempFileError(err error) int {
	fmt.Fprintf(os.Stderr, "✗ Errore file temporaneo: %v\n", err)
	return 1
}

// workDir makes a per-download scratch directory for yt-dlp's intermediate files
// and returns it with a cleanup func. Every mode that downloads routes
// intermediates here (via core.TempRedirectArgs), so the destination only ever
// receives the final file — a failed or cancelled download leaves no .part/.webp
// residue (ADR-0011). The cleanup is deferred in the same goroutine that runs the
// child, so it fires on every exit path, including a context-cancel/timeout kill.
// On a mkdir failure it yields "" and a no-op cleanup, and the caller falls back
// to yt-dlp's default placement (residue possible, but the download still works —
// degraded, not broken).
func workDir() (string, func()) {
	d, err := os.MkdirTemp("", "ytdl-work-*")
	if err != nil {
		return "", func() {}
	}
	return d, func() { _ = os.RemoveAll(d) }
}

// withTempRedirect appends the intermediate-file redirection args (see
// core.TempRedirectArgs) when work is non-empty; an empty work dir leaves args
// unchanged, so a workDir mkdir failure degrades to the pre-redirection argv.
func withTempRedirect(args []string, o core.Options, work string) []string {
	if work == "" {
		return args
	}
	return append(args, core.TempRedirectArgs(o, work)...)
}

// tempFile creates an empty temp file and returns its path and a cleanup func,
// mirroring the Bash mktemp + EXIT-trap pattern.
func tempFile(prefix string) (string, func(), error) {
	f, err := os.CreateTemp("", prefix+"*")
	if err != nil {
		return "", func() {}, err
	}
	name := f.Name()
	f.Close()
	return name, func() { os.Remove(name) }, nil
}

// readSavedPaths reads the after_move file list, skipping empty lines, and
// returns the first path and the count (ytdl lines 326-333).
func readSavedPaths(path string) (first string, count int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		count++
		if first == "" {
			first = line
		}
	}
	if err := sc.Err(); err != nil {
		// A truncated read would make the 1/N/0 report misleading; surface it.
		fmt.Fprintf(os.Stderr, "! ytdl: lettura elenco file incompleta: %v\n", err)
	}
	return first, count
}

// outcome is what a finished run has to say about itself, as handed to
// recordJob. It is a struct rather than a parameter list because the fields are
// same-typed and easy to transpose: Mode/Title/SavedPath are all strings, RC and
// Count both ints.
type outcome struct {
	Mode      string    // "default" | "verbose" | "silent" | "cancelled" | "timeout"
	Title     string    // resolved "Artist - Track"; "" if never resolved
	RC        int       // yt-dlp's exit code
	Success   bool      // exited 0 AND actually saved a file
	SavedPath string    // first after_move path; "" when nothing was saved
	Count     int       // how many files the job saved
	Stderr    []byte    // captured yt-dlp stderr
	Now       time.Time // completion time
}

// recordJob writes the central log-store record for a completed download —
// both the per-job .log (failure detail) and a structured history.jsonl line
// (the durable history that `ytdl history`/`status` read) — then prunes expired
// records (U7, ADR-0009). All best-effort: a logging failure must never change
// the download's exit code, so errors are dropped. dry-run and the background
// launcher do not call this (the background child, running silent, records the
// real job).
//
// Cycle 5: the record also carries WHERE the files landed and, on a failure, a
// one-line reason — the facts "apri", "riscarica" and "vedi errore" are built on
// in both channels (design §5.2). Every one of them is already at hand here;
// none costs an extra syscall.
func recordJob(o core.Options, out outcome) {
	j := logstore.Job{
		URL:      o.URL,
		Title:    out.Title,
		Mode:     out.Mode,
		Format:   o.Settings.Format,
		RC:       out.RC,
		Success:  out.Success,
		Time:     out.Now,
		Stderr:   out.Stderr,
		Path:     absSavedPath(out.SavedPath, o.Settings.OutputDir),
		Dir:      o.Settings.OutputDir,
		Count:    out.Count,
		Playlist: o.Playlist,
	}
	// Only a failure carries a reason: on success yt-dlp's last stderr line is a
	// warning at best, and labelling it "why it failed" would be a lie. logstore
	// sanitises and caps the line on the way to disk.
	if !out.Success {
		j.Error = lastNonEmptyLine(string(out.Stderr))
	}
	logstore.Record(o.Settings.LogDir, j)
	logstore.Append(o.Settings.LogDir, j)
	logstore.Prune(o.Settings.LogDir, o.Settings.LogRetentionDays)
}

// absSavedPath makes the recorded path absolute, which is what Entry.Path
// promises and what the open/reveal validation requires (design §7). yt-dlp
// prints an absolute %(filepath)s because TempRedirectArgs pairs a relative -o
// with an absolute `-P home:`; this only has to hold when that pairing does not,
// e.g. a relative output_dir. An empty path stays empty — "nothing was saved" is
// not a path.
func absSavedPath(saved, outputDir string) string {
	if saved == "" || filepath.IsAbs(saved) {
		return saved
	}
	if outputDir == "" {
		return saved
	}
	return filepath.Join(outputDir, saved)
}

// titleFromPath derives a display title from a saved file path: its base name
// without the extension ("…/Artist - Track.mp3" → "Artist - Track"). An empty
// path (nothing saved) yields "", so a failure falls back to the URL in history.
func titleFromPath(p string) string {
	if p == "" {
		return ""
	}
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// maybeNotify fires a completion banner per config (U6). It is gated by:
// notify (master switch), notify_on (success/failure/both), and — for the
// foreground default/verbose modes — notify_foreground; silent/background are
// never foreground-gated, since their completion is otherwise invisible.
func maybeNotify(o core.Options, foreground, success bool, count int) {
	s := o.Settings
	if !s.Notify {
		return
	}
	if success && s.NotifyOn == "failure" {
		return
	}
	if !success && s.NotifyOn == "success" {
		return
	}
	if foreground && !s.NotifyForeground {
		return
	}
	n := notify.Notification{Success: success, Title: "ytdl", Sound: s.NotifySound}
	switch {
	case !success:
		n.Body = "✗ Download fallito"
	case count == 1:
		n.Body = "✓ 1 brano scaricato"
	case count > 1:
		n.Body = fmt.Sprintf("✓ %d brani scaricati", count)
	default:
		n.Body = "✓ Download completato"
	}
	notifier.Notify(n)
}

// capBuffer is an io.Writer that keeps at most stderrCap bytes but always
// reports a full write, so teeing it alongside os.Stderr via io.MultiWriter
// never truncates the live output.
type capBuffer struct{ buf bytes.Buffer }

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := stderrCap - c.buf.Len(); room > 0 {
		if len(p) <= room {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:room])
		}
	}
	return len(p), nil
}

func (c *capBuffer) Bytes() []byte { return c.buf.Bytes() }

// readCapped reads at most max bytes of the file at path (silent mode's captured
// yt-dlp stderr), returning nil on any read error.
func readCapped(path string, max int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(max)))
	if err != nil {
		return nil
	}
	return data
}

// lastLine returns the file's last line, matching Bash `tail -n1`: a single
// trailing newline is stripped, but a trailing BLANK line (…\n\n) yields "" —
// so a before_dl file ending in a blank line makes silent mode fall back to the
// timestamped .log name, exactly like the Bash tool (parity).
func lastLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSuffix(string(data), "\n")
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
