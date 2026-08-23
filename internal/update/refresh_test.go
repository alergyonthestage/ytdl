package update

import (
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

// released pretends this process is a release build, since a "dev" one is never
// checked at all and would short-circuit every case below.
func released(t *testing.T, version string) {
	t.Helper()
	old := buildinfo.Version
	buildinfo.Version = version
	t.Cleanup(func() { buildinfo.Version = old })
}

func TestShouldRefresh(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	t.Run("consent withheld means no probe at all", func(t *testing.T) {
		released(t, "v2.1.0")
		if shouldRefresh(t.TempDir(), false, now) {
			t.Error("update_check = false must stop the automatic probe")
		}
	})

	t.Run("no state dir, nowhere to cache", func(t *testing.T) {
		released(t, "v2.1.0")
		if shouldRefresh("", true, now) {
			t.Error("no state dir must stop the probe")
		}
	})

	t.Run("a dev build is never checked", func(t *testing.T) {
		released(t, DevVersion)
		if shouldRefresh(t.TempDir(), true, now) {
			t.Error("a local build has no tag to compare against")
		}
	})

	t.Run("nothing cached yet", func(t *testing.T) {
		released(t, "v2.1.0")
		if !shouldRefresh(t.TempDir(), true, now) {
			t.Error("with no verdict cached, a round is worth running")
		}
	})

	t.Run("a fresh cache is reused", func(t *testing.T) {
		released(t, "v2.1.0")
		dir := t.TempDir()
		if err := Save(dir, sampleVerdict(now.Add(-time.Hour))); err != nil {
			t.Fatal(err)
		}
		if shouldRefresh(dir, true, now) {
			t.Error("a verdict from an hour ago must not be re-probed")
		}
	})

	t.Run("an expired cache is refreshed", func(t *testing.T) {
		released(t, "v2.1.0")
		dir := t.TempDir()
		if err := Save(dir, sampleVerdict(now.Add(-DefaultTTL-time.Minute))); err != nil {
			t.Fatal(err)
		}
		if !shouldRefresh(dir, true, now) {
			t.Error("an expired verdict must be refreshed")
		}
	})
}

// The paths that must not probe must make NO request, which is asserted against
// the hub's counter rather than inferred.
func TestRefreshAsyncMakesNoRequestWhenItShouldNot(t *testing.T) {
	now := time.Now()
	h := newHub(t)
	h.tags[DefaultSlug] = "v2.2.0"
	h.deps = validDeps

	released(t, "v2.1.0")
	dir := t.TempDir()
	if err := Save(dir, sampleVerdict(now)); err != nil {
		t.Fatal(err)
	}

	RefreshAsync(dir, false, now) // consent withheld
	RefreshAsync("", true, now)   // nowhere to cache
	RefreshAsync(dir, true, now)  // cache still fresh

	released(t, DevVersion)
	RefreshAsync(t.TempDir(), true, now) // a local build

	if n := h.hits.Load(); n != 0 {
		t.Errorf("made %d requests, want none", n)
	}
}

func TestRefreshOnceWritesACompleteRound(t *testing.T) {
	isolate(t) // no real yt-dlp; the local half is empty, which is fine here
	released(t, "v2.1.0")
	h := newHub(t)
	h.tags[DefaultSlug] = "v2.2.0"
	h.deps = validDeps

	dir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	refreshOnce(dir, now)

	v, ok := Load(dir)
	if !ok {
		t.Fatal("refreshOnce wrote no verdict")
	}
	if !v.Known() {
		t.Errorf("verdict is not Known: %+v", v)
	}
	if v.LatestYtdl != "v2.2.0" || v.Pin.YtDlp != "2026.07.04" {
		t.Errorf("remote half = %q / %+v", v.LatestYtdl, v.Pin)
	}
	if !v.CheckedAt.Equal(now) {
		t.Errorf("CheckedAt = %v, want %v", v.CheckedAt, now)
	}
	if v.Installed.Ytdl != "v2.1.0" {
		t.Errorf("Installed.Ytdl = %q, want the running build", v.Installed.Ytdl)
	}
}

// A round where a source stayed silent must not overwrite a complete one: that
// would destroy "the last known result and its date", which is exactly what the
// not-verified state has to carry (ADR-0016 §8).
func TestRefreshOnceKeepsTheLastKnownResultWhenAProbeFails(t *testing.T) {
	isolate(t)
	released(t, "v2.1.0")
	h := newHub(t)
	h.deps = validDeps // the pin answers, the ytdl tag does not (no h.tags entry)

	dir := t.TempDir()
	good := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	if err := Save(dir, sampleVerdict(good)); err != nil {
		t.Fatal(err)
	}

	refreshOnce(dir, good.Add(48*time.Hour))

	v, ok := Load(dir)
	if !ok {
		t.Fatal("the cached verdict disappeared")
	}
	if !v.CheckedAt.Equal(good) {
		t.Errorf("CheckedAt = %v, want the last GOOD check %v", v.CheckedAt, good)
	}
	if !v.Known() {
		t.Error("the complete cached round was replaced by an incomplete one")
	}
}

// With nothing cached there is nothing to lose, so a partial round is kept: it
// still carries the local facts, which are always shown.
func TestRefreshOnceKeepsAPartialRoundWhenNothingWasCached(t *testing.T) {
	isolate(t)
	released(t, "v2.1.0")
	h := newHub(t)
	h.deps = validDeps // again, no ytdl tag

	dir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	refreshOnce(dir, now)

	v, ok := Load(dir)
	if !ok {
		t.Fatal("refreshOnce wrote nothing, so the surface has no local facts to show")
	}
	if v.Known() {
		t.Error("a round with a silent source must not report itself as Known")
	}
	if v.Installed.Ytdl != "v2.1.0" {
		t.Errorf("Installed.Ytdl = %q; the local facts survive a failed probe", v.Installed.Ytdl)
	}
}

// The verdict Check returns is usable whatever the error says: the error exists
// for a caller that wants to log why, not as a reason to discard the round.
func TestCheckReturnsAUsableVerdictAlongsideItsError(t *testing.T) {
	isolate(t)
	released(t, "v2.1.0")
	h := newHub(t)
	h.tags[DefaultSlug] = "v2.2.0" // the tag answers, deps.conf 404s

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	v, err := Check(ctxT(t), nil, t.TempDir(), now)
	if err == nil {
		t.Fatal("Check: want an error when a source did not answer")
	}
	if v.LatestYtdl != "v2.2.0" {
		t.Errorf("LatestYtdl = %q; what DID answer must survive", v.LatestYtdl)
	}
	if v.Known() {
		t.Error("Known() must be false when a source stayed silent")
	}
	if v.Installed.Ytdl != "v2.1.0" {
		t.Errorf("Installed.Ytdl = %q; the local facts need no network", v.Installed.Ytdl)
	}
}
