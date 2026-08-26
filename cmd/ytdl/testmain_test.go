package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/daemon"
)

// selfExecLogEnv names a file the guard below appends to when the test binary is
// asked to be a daemon. It is empty in an ordinary run: the guard costs nothing
// and says nothing unless a measurement asks it to.
const selfExecLogEnv = "YTDL_TEST_SELFEXEC_LOG"

// TestMain stops the test binary from becoming a daemon.
//
// `daemon.spawn` self-exec's os.Executable() with the hidden `__daemon` keyword.
// Under `go test` os.Executable() is THIS binary, and Go's generated main does
// not reject an unknown positional argument: flag.Parse stops at the first
// non-flag argument and leaves it in flag.Args(), so the binary runs the WHOLE
// suite and exits 0. `realMain` intercepts `__daemon` as its first act, but a
// test binary never enters realMain — that interception is on the production
// path only.
//
// The result was a suite that re-executed itself without bound: each run started
// another, detached with Setsid and released, so it outlived `go test` returning,
// ignored -test.timeout, and survived the session. Four sessions were destroyed
// before the cause was found, and the disk it filled is only the visible half —
// the CPU those copies burn is the other.
//
// The guard is deliberately the outermost one. The seam in resumeIfStalled means
// no test should reach a real self-exec at all; this is what holds when a future
// test reaches one anyway, by a path nobody anticipated. A backstop that is never
// hit is doing its job.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == daemon.DaemonSubcommand {
		recordSelfExec()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// noRecurseEnv keeps the regression test below from becoming the very bomb it
// guards against. If the guard in TestMain is ever removed, the child it starts
// runs the whole suite — including that test — and would start a child of its
// own, for ever. The child is marked, and a marked run skips it.
const noRecurseEnv = "YTDL_TEST_NO_SPAWN_PROBE"

// TestTheTestBinaryRefusesToBecomeADaemon is the regression test for the defect
// this file exists to close: `ytdl.test __daemon` used to run the entire suite.
//
// It reproduces the exact call daemon.spawn makes — os.Executable() plus the
// hidden keyword — and asserts the two things that distinguish a refusal from a
// second suite: the guard was taken, and nothing was printed. A test binary that
// ran its suite here would emit output and leave the log empty, failing on both.
func TestTheTestBinaryRefusesToBecomeADaemon(t *testing.T) {
	if os.Getenv(noRecurseEnv) != "" {
		t.Skip("this run IS the child probe; not re-entering")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "selfexec.log")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, daemon.DaemonSubcommand)
	cmd.Env = append(os.Environ(), selfExecLogEnv+"="+log, noRecurseEnv+"=1")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the guard must exit cleanly, got %v; output:\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("the test binary produced output when asked to be a daemon — it ran its suite:\n%s", out)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the guard was never reached: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Errorf("guard entries = %d, want exactly 1:\n%s", got, data)
	}
}

// recordSelfExec appends one line per refusal, so a run can be MEASURED rather
// than assumed — the question "how many self-execs does one suite run attempt?"
// is answered without ever letting one succeed. Failures are ignored on purpose:
// a measurement aid must never be able to change the outcome of a test run.
func recordSelfExec() {
	path := os.Getenv(selfExecLogEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pid=%d ppid=%d args=%q\n", os.Getpid(), os.Getppid(), os.Args[1:])
}
