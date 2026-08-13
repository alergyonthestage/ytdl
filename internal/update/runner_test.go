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
