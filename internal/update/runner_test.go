package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

// These tests run the REAL launch path — a real curl piped into a real bash —
// against a local httptest server serving a stand-in install.sh. Stubbing the
// exec would leave the one thing worth testing untested: that the two processes,
// the pipe, the detachment and the log redirection actually fit together.

// settle waits for the launched installer to be reaped, so the assertions look at
// a finished run rather than a race.
func settle(t *testing.T, r *Runner) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !r.Running() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the installer never finished")
}

func TestRunnerRunsTheInstallerAndRecordsSuccess(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\necho 'installing things'\nexit 0\n")

	dir := t.TempDir()
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	settle(t, r)

	p := r.Progress()
	if p.State != StateDone {
		t.Errorf("state = %q, want %q (log: %s)", p.State, StateDone, p.LogTail)
	}
	if p.ExitCode != 0 {
		t.Errorf("exit code = %d", p.ExitCode)
	}
	if !strings.Contains(p.LogTail, "installing things") {
		t.Errorf("the installer's output did not reach the log: %q", p.LogTail)
	}
	if p.StartedAt.IsZero() || p.EndedAt.IsZero() {
		t.Errorf("run is not stamped: %+v", p.Run)
	}
}

// A partial failure is never summarised: what is reported is the exit code and
// the log, because the installer is the only thing that knows how far it got.
func TestRunnerRecordsFailureWithItsExitCodeAndLog(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\necho 'got as far as ffmpeg'\necho 'boom' >&2\nexit 3\n")

	dir := t.TempDir()
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	settle(t, r)

	p := r.Progress()
	if p.State != StateFailed {
		t.Errorf("state = %q, want %q", p.State, StateFailed)
	}
	if p.ExitCode != 3 {
		t.Errorf("exit code = %d, want the installer's own 3", p.ExitCode)
	}
	for _, want := range []string{"got as far as ffmpeg", "boom"} {
		if !strings.Contains(p.LogTail, want) {
			t.Errorf("the log is missing %q:\n%s", want, p.LogTail)
		}
	}
	if p.Changed {
		t.Error("a failed run must never claim the binary changed")
	}
}

// --force is what a retry after a failed update uses, and it has to reach the
// installer as an argument rather than as part of an interpolated command line.
func TestRunnerPassesForceToTheInstaller(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\necho \"args:$*\"\nexit 0\n")

	for _, force := range []bool{false, true} {
		dir := t.TempDir()
		r := NewRunner(dir)
		if err := r.Start(force); err != nil {
			t.Fatalf("Start(%v): %v", force, err)
		}
		settle(t, r)

		got := r.Progress().LogTail
		want := "args:"
		if force {
			want = "args:--force"
		}
		if !strings.Contains(got, want) {
			t.Errorf("force=%v: log %q does not contain %q", force, got, want)
		}
	}
}

// The ytdl binary changing is what decides whether applying the update costs a
// handover and a reload, or nothing at all. It cannot be read from
// buildinfo.Version — that describes the running image, which the installer
// replaced underneath — so it comes from the marker the installer just wrote.
func TestRunnerDetectsThatTheYtdlBinaryChanged(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "v2.1.0"
	t.Cleanup(func() { buildinfo.Version = old })

	cases := []struct {
		name    string
		marker  string
		changed bool
	}{
		{"a new ytdl was installed", "v2.2.0", true},
		{"only a dependency moved", "v2.1.0", false},
		{"the installer wrote no version", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHub(t)
			dir := t.TempDir()
			// The stand-in installer writes the marker, exactly as the real one does
			// at the end of a successful run.
			script := "#!/bin/bash\nprintf 'ytdl_version = %s\\n' > '" +
				filepath.Join(dir, MarkerName) + "'\nexit 0\n"
			h.setInstaller(strings.Replace(script, "%s", tc.marker, 1))

			r := NewRunner(dir)
			if err := r.Start(false); err != nil {
				t.Fatalf("Start: %v", err)
			}
			settle(t, r)

			if got := r.Progress().Changed; got != tc.changed {
				t.Errorf("Changed = %v, want %v", got, tc.changed)
			}
		})
	}
}

// A successful run leaves the machine describing something the cached verdict no
// longer knows about, so the verdict goes.
func TestRunnerInvalidatesTheVerdictOnSuccess(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nexit 0\n")

	dir := t.TempDir()
	if err := Save(dir, sampleVerdict(time.Now())); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	settle(t, r)

	if _, ok := Load(dir); ok {
		t.Error("the verdict survived a successful update; the notice would keep asking for an action already taken")
	}
}

// A failed run changed nothing, so the verdict it was based on is still true.
func TestRunnerKeepsTheVerdictOnFailure(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nexit 1\n")

	dir := t.TempDir()
	if err := Save(dir, sampleVerdict(time.Now())); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	settle(t, r)

	if _, ok := Load(dir); !ok {
		t.Error("a failed update discarded a verdict that is still true")
	}
}

func TestRunnerRefusesASecondRun(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nsleep 2\nexit 0\n")

	r := NewRunner(t.TempDir())
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Start(false); err != ErrAlreadyRunning {
		t.Errorf("second Start = %v, want ErrAlreadyRunning", err)
	}
	if p := r.Progress(); p.State != StateRunning {
		t.Errorf("state = %q while running, want %q", p.State, StateRunning)
	}
	settle(t, r)
}

// The log is truncated per run, so the tail a surface shows always answers "how
// did THIS update go" rather than mixing in an older one.
func TestRunnerTruncatesTheLogPerRun(t *testing.T) {
	h := newHub(t)
	dir := t.TempDir()
	r := NewRunner(dir)

	h.setInstaller("#!/bin/bash\necho 'FIRST RUN'\nexit 0\n")
	if err := r.Start(false); err != nil {
		t.Fatal(err)
	}
	settle(t, r)

	h.setInstaller("#!/bin/bash\necho 'SECOND RUN'\nexit 0\n")
	if err := r.Start(false); err != nil {
		t.Fatal(err)
	}
	settle(t, r)

	tail := r.Progress().LogTail
	if strings.Contains(tail, "FIRST RUN") {
		t.Errorf("the previous run's output survived:\n%s", tail)
	}
	if !strings.Contains(tail, "SECOND RUN") {
		t.Errorf("this run's output is missing:\n%s", tail)
	}
}

func TestProgressWithNoRunIsIdle(t *testing.T) {
	r := NewRunner(t.TempDir())
	if p := r.Progress(); p.State != StateIdle {
		t.Errorf("state = %q, want %q", p.State, StateIdle)
	}
	if r.Running() {
		t.Error("Running() is true before anything started")
	}
}

// A corrupt run file is "no run", which renders as idle rather than as an error
// the user did not ask for.
func TestLoadRunTreatsDamageAsNoRun(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"", "not json", `{"state":""}`, `["a"]`} {
		if err := os.WriteFile(filepath.Join(dir, RunName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if run, ok := LoadRun(dir); ok {
			t.Errorf("LoadRun accepted %q and returned %+v", content, run)
		}
	}
}

func TestRunnerNeedsAStateDir(t *testing.T) {
	if err := NewRunner("").Start(false); err == nil {
		t.Error("Start with no state dir: want an error")
	}
}

// The log tail is bounded and starts at a line boundary, so a surface never shows
// half a line and never shows megabytes.
func TestLogTailIsBoundedAndStartsAtALineBoundary(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("a line of installer output that is reasonably long\n")
	}
	b.WriteString("THE LAST LINE\n")
	if err := os.WriteFile(r.LogPath(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	tail := r.logTail()
	if len(tail) > logTailBytes {
		t.Errorf("tail is %d bytes, want at most %d", len(tail), logTailBytes)
	}
	if !strings.Contains(tail, "THE LAST LINE") {
		t.Error("the tail does not reach the end of the log, which is where the failure is")
	}
	if !strings.HasPrefix(tail, "a line of installer output") {
		t.Errorf("the tail starts mid-line: %q", tail[:40])
	}
}

// The abandoned rule, as a rule. A record that says "running" while nothing is
// running it must not refuse every later update for ever: that is a failure the
// user can neither see nor act on, and a reboot mid-install produces it (V1,
// design §7.3).
func TestAbandonedIsARuleAboutTheRecord(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	live := func(int) bool { return true }
	gone := func(int) bool { return false }

	cases := []struct {
		name  string
		run   Run
		alive func(int) bool
		want  bool
	}{
		{"a live installer, started a minute ago",
			Run{State: StateRunning, StartedAt: now.Add(-time.Minute), PID: 4242}, live, false},
		{"its process is gone",
			Run{State: StateRunning, StartedAt: now.Add(-time.Minute), PID: 4242}, gone, true},
		{"no pid recorded, still inside the backstop",
			Run{State: StateRunning, StartedAt: now.Add(-time.Minute)}, gone, false},
		{"no pid recorded, past the backstop",
			Run{State: StateRunning, StartedAt: now.Add(-StaleAfter)}, gone, true},
		{"a pid that answers past the backstop is a number handed to somebody else",
			Run{State: StateRunning, StartedAt: now.Add(-StaleAfter), PID: 4242}, live, true},
		{"no start time at all gets no benefit of the doubt",
			Run{State: StateRunning, PID: 4242}, live, true},
		{"a start time in the future gets none either — the clock can never reach it",
			Run{State: StateRunning, StartedAt: now.Add(time.Hour)}, live, true},
		{"a future start time with a dead pid is abandoned too",
			Run{State: StateRunning, StartedAt: now.Add(48 * time.Hour), PID: 4242}, gone, true},
		{"a finished run is not abandoned", Run{State: StateDone, PID: 4242}, gone, false},
		{"a failed run is not abandoned", Run{State: StateFailed}, gone, false},
		{"an idle record is not abandoned", Run{State: StateIdle}, gone, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.run.Abandoned(now, tc.alive); got != tc.want {
				t.Errorf("Abandoned(%+v) = %v, want %v", tc.run, got, tc.want)
			}
		})
	}
}

// Progress DERIVES the abandoned state and never writes it: the record on disk is
// the installer's, and a reader that rewrote it would be recording an outcome it
// does not know.
func TestProgressReportsAnAbandonedRunWithoutRewritingIt(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	// Past the backstop, so the assertion does not depend on which pids this
	// machine happens to have free.
	stale := Run{State: StateRunning, StartedAt: time.Now().Add(-StaleAfter - time.Minute), PID: 4242}
	if err := SaveRun(dir, stale); err != nil {
		t.Fatal(err)
	}

	if p := r.Progress(); p.State != StateAbandoned {
		t.Errorf("state = %q, want %q; a record nobody is left to finish would block every later update",
			p.State, StateAbandoned)
	}
	run, ok := LoadRun(dir)
	if !ok || run.State != StateRunning {
		t.Errorf("the record on disk was rewritten: %+v (ok=%v)", run, ok)
	}
}

// The in-memory half outranks the record: an installer THIS process launched is
// running, whatever the record's timestamps look like from here. Getting this
// backwards would unblock a second installer over a live one, and install.sh
// replaces binaries with mv.
func TestOurOwnRunIsNeverCalledAbandoned(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nsleep 2\nexit 0\n")

	dir := t.TempDir()
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Forge a record that every clock-based test would call stale, while our own
	// installer is genuinely still going.
	if err := SaveRun(dir, Run{
		State: StateRunning, StartedAt: time.Now().Add(-StaleAfter - time.Hour), PID: 4242,
	}); err != nil {
		t.Fatal(err)
	}
	if p := r.Progress(); p.State != StateRunning {
		t.Errorf("state = %q while our own installer is running, want %q", p.State, StateRunning)
	}
	settle(t, r)
}

// The pid goes on the record so a process that did not launch the installer can
// still tell a run in flight from one nobody is left to finish.
func TestTheRunRecordCarriesTheInstallerPID(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nsleep 1\nexit 0\n")

	dir := t.TempDir()
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	run, ok := LoadRun(dir)
	if !ok {
		t.Fatal("no run record was written")
	}
	if run.PID <= 0 {
		t.Errorf("PID = %d; without it a later reader has only the clock", run.PID)
	}
	if !processAlive(run.PID) {
		t.Errorf("processAlive(%d) = false for an installer that is still running", run.PID)
	}
	settle(t, r)
}

// A failed run installed nothing, so it has no version to report. The marker
// still describes the PREVIOUS install, and carrying that onto this record would
// let a surface attribute an install to a run that performed none (V6).
func TestAFailedRunRecordsNoVersion(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "v2.1.0"
	t.Cleanup(func() { buildinfo.Version = old })

	dir := t.TempDir()
	// The marker left by the install that is already on this machine.
	if err := os.WriteFile(filepath.Join(dir, MarkerName),
		[]byte("ytdl_version = v2.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHub(t)
	h.setInstaller("#!/bin/bash\necho 'could not reach the release'\nexit 4\n")
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	settle(t, r)

	p := r.Progress()
	if p.State != StateFailed {
		t.Fatalf("state = %q, want %q", p.State, StateFailed)
	}
	if p.Version != "" {
		t.Errorf("Version = %q for a run that installed nothing", p.Version)
	}
	if p.Changed {
		t.Error("a failed run claimed the binary changed")
	}
}

// A run that SUCCEEDS is never, at any instant, reported as abandoned.
//
// finish used to clear the in-memory flag before writing the terminal record,
// which left a window — every run, wide enough to contain a file read and two
// file writes — where the record still said "running", Running() already said
// false and the installer's pid had been reaped. Abandoned was then satisfied for
// a run that had just succeeded: the page took its terminal branch, stopped
// polling and printed something false, and a POST in the same window started a
// SECOND installer over the first (V10).
//
// It polls rather than reasoning about the ordering, because the ordering is
// exactly what a future refactor would change.
func TestASuccessfulRunIsNeverSeenAsAbandoned(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nexit 0\n")

	for run := 0; run < 5; run++ {
		dir := t.TempDir()
		r := NewRunner(dir)

		stop := make(chan struct{})
		bad := make(chan string, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if st := r.Progress().State; st == StateAbandoned {
					select {
					case bad <- st:
					default:
					}
					return
				}
			}
		}()

		if err := r.Start(false); err != nil {
			close(stop)
			<-done
			t.Fatalf("Start: %v", err)
		}
		settle(t, r)
		close(stop)
		<-done

		select {
		case st := <-bad:
			t.Fatalf("run %d: a successful run was reported as %q", run, st)
		default:
		}
		if p := r.Progress(); p.State != StateDone {
			t.Fatalf("run %d: final state = %q, want %q", run, p.State, StateDone)
		}
	}
}

// settle() returns when the flag clears, and callers then read the record. That
// is only sound if the record is written FIRST — otherwise every
// settle-then-Progress test in this file races the writer.
func TestTheRecordIsTerminalAsSoonAsTheRunnerIsIdle(t *testing.T) {
	h := newHub(t)
	h.setInstaller("#!/bin/bash\nexit 0\n")

	dir := t.TempDir()
	r := NewRunner(dir)
	if err := r.Start(false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	settle(t, r)

	run, ok := LoadRun(dir)
	if !ok {
		t.Fatal("the runner went idle with no record on disk")
	}
	if run.State != StateDone {
		t.Errorf("record says %q the moment Running() went false, want %q", run.State, StateDone)
	}
}
