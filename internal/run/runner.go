// Package run is ytdl's runtime layer: it executes yt-dlp, manages temp files,
// writes the silent-mode failure log, detaches background downloads, and checks
// dependencies — the behaviours the pure argv goldens do not cover. All
// user-facing runtime strings live here, verbatim from the Bash ytdl.
package run

import (
	"bufio"
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
	"github.com/alergyonthestage/ytdl/internal/core"
)

// ytDlp and ffmpeg are the external tools ytdl drives.
const ytDlp = "yt-dlp"
const ffmpeg = "ffmpeg"

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
	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	rc := runCode(cmd)
	if rc == 0 {
		fmt.Fprintf(w, "\n✓ Fatto → %s\n", o.Settings.OutputDir)
	}
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

	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
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
	return rc
}

// runSilent produces no terminal output; on failure (or zero files) it writes
// <title>.log to the output dir with the yt-dlp stderr (ytdl lines 282-308).
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

	ef, err := os.OpenFile(errlog, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Errore file temporaneo: %v\n", err)
		return 1
	}
	cmd := exec.Command(ytDlp, core.BuildArgs(o)...)
	cmd.Stdout = nil // → /dev/null
	cmd.Stderr = ef
	rc := runCode(cmd)
	ef.Close()

	info, statErr := os.Stat(saved)
	empty := statErr != nil || info.Size() == 0
	if rc != 0 || empty {
		writeFailLog(o, title, errlog, rc)
	}
	return rc
}

// runBackground re-executes ytdl in silent mode, detached from the terminal, and
// returns immediately (ytdl lines 245-253). Uses Setsid + /dev/null stdio — the
// `nohup … &` equivalent.
func runBackground(o core.Options, stdout, stderr io.Writer) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "✗ Impossibile determinare il percorso di ytdl: %v\n", err)
		return 1
	}
	argv := core.ReExecArgs(o, self) // [self, -s, -f, FMT, -o, DIR, (-p), URL]
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		defer devnull.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "✗ Impossibile avviare in background: %v\n", err)
		return 1
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release() // detach: don't wait for the child

	fmt.Fprintf(stdout, "▸ Avviato in background (PID %d).\n", pid)
	fmt.Fprintf(stdout, "  Audio → %s\n", o.Settings.OutputDir)
	fmt.Fprintf(stdout, "  Se un download fallisce, troverai <titolo>.log nella stessa cartella.\n")
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

// writeFailLog writes the silent-mode failure log to <output>/<label>.log. The
// label is the last before_dl line, sanitized (parity `tr '/:' '__'` plus the C5
// hardening), with a timestamped fallback (ytdl lines 295-306).
func writeFailLog(o core.Options, titleFile, errlog string, rc int) {
	label := sanitizeLogName(lastLine(titleFile))
	if label == "" {
		label = "ytdl-failed-" + time.Now().Format("20060102-150405")
	}
	dest := filepath.Join(o.Settings.OutputDir, label+".log")

	var b strings.Builder
	fmt.Fprintln(&b, "ytdl — download FALLITO")
	// _2 pads the day the way C's %e does; "Jan  2" would emit a double space
	// before a two-digit day. (Bash `date` also prints a %Z zone Go omits here.)
	fmt.Fprintf(&b, "data: %s\n", time.Now().Format("Mon Jan _2 15:04:05 2006"))
	fmt.Fprintf(&b, "url:  %s\n", o.URL)
	fmt.Fprintf(&b, "rc:   %d\n", rc)
	fmt.Fprintln(&b, "----------------------------------------")
	if data, err := os.ReadFile(errlog); err == nil {
		b.Write(data)
	}
	// The .log is silent mode's only failure signal; if even that write fails,
	// break silence on stderr so the user is not left with no indication at all.
	if err := os.WriteFile(dest, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "✗ ytdl: impossibile scrivere il log di errore %s: %v\n", dest, err)
	}
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

// sanitizeLogName maps the parity `/` and `:` to `_`, then (C5) replaces any
// remaining filesystem-hostile characters, keeping the name readable.
func sanitizeLogName(s string) string {
	s = strings.NewReplacer("/", "_", ":", "_").Replace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20: // control characters, incl. NUL and newline
			b.WriteRune('_')
		case strings.ContainsRune(`\*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(strings.TrimSpace(b.String()), ".")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
