// Package run is ytdl's runtime layer: it executes yt-dlp, manages temp files,
// writes the silent-mode failure log, enqueues background downloads to the
// on-demand daemon, and checks dependencies — the behaviours the pure argv
// goldens do not cover. All user-facing runtime strings live here, verbatim from
// the Bash ytdl.
package run

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// runDryRun previews filenames without downloading (ytdl lines 224-235). With
// the shims absent in production, yt-dlp's own exit code propagates.
func runDryRun(o core.Options, w io.Writer) int {
	fmt.Fprintf(w, "▸ Anteprima (nessun download) — formato .%s\n", o.Settings.Format)
	fmt.Fprintf(w, "▸ Destinazione: %s\n\n", o.Settings.OutputDir)
	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
	cmd.Stdout, cmd.Stderr = w, os.Stderr
	return runCode(cmd)
}

// runVerbose downloads with full yt-dlp output (ytdl lines 256-267). The success
// line prints only when yt-dlp succeeded (the Bash `set -e` aborts otherwise).
func runVerbose(o core.Options, w io.Writer) int {
	fmt.Fprintf(w, "▸ Scarico (.%s) → %s  [verbose]\n\n", o.Settings.Format, o.Settings.OutputDir)
	var errbuf capBuffer
	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &errbuf)
	rc := runCode(cmd)
	success := rc == 0
	if success {
		fmt.Fprintf(w, "\n✓ Fatto → %s\n", o.Settings.OutputDir)
	}
	now := time.Now()
	recordJob(o, "verbose", rc, success, errbuf.Bytes(), now)
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

	var errbuf capBuffer
	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
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
	recordJob(o, "default", rc, success, errbuf.Bytes(), now)
	maybeNotify(o, true, success, count) // foreground
	return rc
}

// runSilent produces no terminal output. It records the job centrally and, on
// failure, drops an in-destination breadcrumb (single, or one per failed
// playlist item), keyed by a stable hash so a later success cleans it up (U7).
// A completion notification fires per config (U6). It is also the mode the
// background launcher re-executes into, so background inherits all of this.
func runSilent(o core.Options) int {
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

	args := core.BuildArgs(o)

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

	ef, err := os.OpenFile(errlog, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Errore file temporaneo: %v\n", err)
		return 1
	}
	cmd := exec.Command(ytDlp, args...)
	cmd.Stdout = nil // → /dev/null
	cmd.Stderr = ef
	rc := runCode(cmd)
	ef.Close()

	// "Success" means yt-dlp exited 0 AND actually saved a file. Count non-blank
	// after_move lines once and reuse it for the record, the breadcrumb decision
	// and the notification — consistent with default mode (an empty or
	// blank-only saved list is a failure, matching the Bash tool's zero-files
	// case, ytdl lines 290-293).
	_, count := readSavedPaths(saved)
	success := rc == 0 && count > 0
	now := time.Now()

	recordJob(o, "silent", rc, success, readCapped(errlog, stderrCap), now)

	if o.Settings.BreadcrumbOnFailure {
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

	pending := 0
	if snap, err := sp.List(); err == nil {
		pending, _, _, _ = snap.Counts()
	}
	fmt.Fprintf(stdout, "▸ Download accodato (%d in attesa).\n", pending)
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
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		// ExitCode() is -1 for a signal death; recover the shell's 128+signal.
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return 1
}

func tempFileError(err error) int {
	fmt.Fprintf(os.Stderr, "✗ Errore file temporaneo: %v\n", err)
	return 1
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

// recordJob writes the central log-store record for a completed download and
// then prunes expired records (U7). Both are best-effort — a logging failure
// must never change the download's exit code — so errors are dropped. dry-run
// and the background launcher do not call this (the background child, running
// silent, records the real job).
func recordJob(o core.Options, mode string, rc int, success bool, stderr []byte, now time.Time) {
	logstore.Record(o.Settings.LogDir, logstore.Job{
		URL:     o.URL,
		Mode:    mode,
		Format:  o.Settings.Format,
		RC:      rc,
		Success: success,
		Time:    now,
		Stderr:  stderr,
	})
	logstore.Prune(o.Settings.LogDir, o.Settings.LogRetentionDays)
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
