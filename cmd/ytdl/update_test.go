package main

import (
	"os"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/update"
)

// timeFixture is a fixed instant, so a cached round is never "in the future".
func timeFixture() time.Time { return time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC) }

// The token has to survive the handover. Without it the new daemon answers every
// API call with "401 … riapri l'interfaccia con ytdl gui" — a Terminal, which is
// precisely the acceptance test failing (ADR-0016 §9).
func TestSessionTokenIsInheritedAcrossAHandover(t *testing.T) {
	t.Setenv(guiTokenEnv, "handed-over-token")
	got, err := sessionToken()
	if err != nil {
		t.Fatalf("sessionToken: %v", err)
	}
	if got != "handed-over-token" {
		t.Errorf("token = %q, want the one handed over", got)
	}
}

// An ordinary `ytdl gui` sets nothing, so it must get a fresh token — never a
// predictable or shared one.
func TestSessionTokenIsFreshWithoutAHandover(t *testing.T) {
	for _, value := range []string{"", ""} {
		t.Setenv(guiTokenEnv, value)
		first, err := sessionToken()
		if err != nil {
			t.Fatal(err)
		}
		second, err := sessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if first == "" || second == "" {
			t.Fatal("sessionToken returned an empty token")
		}
		if first == second {
			t.Error("two fresh tokens are identical; the session secret is predictable")
		}
		if len(first) < 32 {
			t.Errorf("token is only %d characters", len(first))
		}
	}
	// Unset entirely, not merely empty.
	os.Unsetenv(guiTokenEnv)
	if tok, err := sessionToken(); err != nil || tok == "" {
		t.Errorf("sessionToken with the variable unset = (%q, %v)", tok, err)
	}
}

// Only a successful run that replaced ytdl ITSELF costs a restart. After the
// installer became idempotent the common update is a re-pinned yt-dlp, which
// leaves the page exactly where it was (design §5).
func TestOnlyAReplacedBinaryCostsAHandover(t *testing.T) {
	cases := []struct {
		name string
		run  update.Run
		want bool
	}{
		{"ytdl itself was replaced", update.Run{State: update.StateDone, Changed: true}, true},
		{"only a dependency moved", update.Run{State: update.StateDone, Changed: false}, false},
		{"the update failed", update.Run{State: update.StateFailed, Changed: true}, false},
		{"still running", update.Run{State: update.StateRunning, Changed: true}, false},
		{"nothing has run", update.Run{State: update.StateIdle}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldHandOver(tc.run); got != tc.want {
				t.Errorf("shouldHandOver(%+v) = %v, want %v", tc.run, got, tc.want)
			}
		})
	}
}

// The local facts are present whether or not any round ever completed
// (ADR-0016 §8) — and the page's reload trigger compares the ytdl version the
// server reports, so it has to be there at all times or the handover never
// finishes.
func TestUpdaterAlwaysReportsTheRunningBuild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(update.BinDirEnv, t.TempDir()) // no real dependencies in view
	t.Setenv("PATH", t.TempDir())

	u := newGUIUpdater(dir, func() config.Settings { return config.Defaults() })

	v, ok := u.Verdict()
	if ok {
		t.Error("a verdict was reported with nothing cached")
	}
	if v.Installed.Ytdl != buildinfo.Version {
		t.Errorf("Installed.Ytdl = %q, want the running build %q", v.Installed.Ytdl, buildinfo.Version)
	}

	// With a round cached, the remote half comes from the cache and the local half
	// still describes this process.
	if err := update.Save(dir, update.Verdict{
		CheckedAt:  timeFixture(),
		LatestYtdl: "v9.9.9",
		Pin:        update.Pin{YtDlp: "2026.08.02", FFmpeg: "1785863997_9.0"},
		Installed:  update.Installed{Ytdl: "a-stale-version"},
	}); err != nil {
		t.Fatal(err)
	}
	v, ok = u.Verdict()
	if !ok {
		t.Fatal("the cached round was not loaded")
	}
	if v.LatestYtdl != "v9.9.9" {
		t.Errorf("LatestYtdl = %q; the remote half must come from the cache", v.LatestYtdl)
	}
	if v.Installed.Ytdl != buildinfo.Version {
		t.Errorf("Installed.Ytdl = %q; a cached local fact must not outrank this process", v.Installed.Ytdl)
	}
}

// Enabled reads the config file on every call, so turning the automatic check
// off in the GUI takes effect without restarting the daemon.
func TestUpdaterEnabledFollowsTheResolvedSetting(t *testing.T) {
	for _, want := range []bool{true, false} {
		s := config.Defaults()
		s.UpdateCheck = want
		u := newGUIUpdater(t.TempDir(), func() config.Settings { return s })
		if got := u.Enabled(); got != want {
			t.Errorf("Enabled() = %v, want %v", got, want)
		}
	}
}

// The daemon's lifetime covers the installer's whole run, not just the tab
// watching it.
//
// Only the process that launched the installer can record how it went, so a
// daemon that idle-exits mid-update leaves update-run.json at "running" with
// nobody left to finish it — and that record then refuses every later update
// (V1, ADR-0008's rule extended by ADR-0016 §9).
func TestTheDaemonOutlivesTheUpdateItLaunched(t *testing.T) {
	cases := []struct {
		name              string
		clients, updating bool
		want              bool
	}{
		{"a tab is watching", true, false, true},
		{"no tab, but an update is running", false, true, true},
		{"a tab and an update", true, true, true},
		{"neither: the ordinary idle-exit applies", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alive := daemonAlive(
				func() bool { return tc.clients },
				func() bool { return tc.updating },
			)
			if got := alive(); got != tc.want {
				t.Errorf("daemonAlive(clients=%v, updating=%v)() = %v, want %v",
					tc.clients, tc.updating, got, tc.want)
			}
		})
	}
}

// The updater reports ITS OWN installer, not any installer: the run record on
// disk describes whatever process wrote it, and a daemon that adopted a stale
// one must not hold itself alive for a run it cannot finish.
func TestUpdaterRunningIsAboutThisProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(update.BinDirEnv, t.TempDir())
	t.Setenv("PATH", t.TempDir())

	u := newGUIUpdater(dir, func() config.Settings { return config.Defaults() })
	if u.Running() {
		t.Error("a fresh updater reports an installer in flight")
	}
	// Somebody else's record, left at "running".
	if err := update.SaveRun(dir, update.Run{
		State: update.StateRunning, StartedAt: timeFixture(),
	}); err != nil {
		t.Fatal(err)
	}
	if u.Running() {
		t.Error("a run record written by another process was adopted as ours")
	}
}
