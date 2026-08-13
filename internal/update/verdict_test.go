package update

import (
	"reflect"
	"testing"
	"time"
)

// verdict builds a round for the matrix below: what the two sources said, and
// what the machine has.
func verdict(latestYtdl, pinYtDlp, pinFFmpeg, ytdl, ytDlp, ffmpeg string) Verdict {
	return Verdict{
		CheckedAt:  time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC),
		LatestYtdl: latestYtdl,
		Pin:        Pin{YtDlp: pinYtDlp, FFmpeg: pinFFmpeg},
		Installed:  Installed{Ytdl: ytdl, YtDlp: ytDlp, FFmpeg: ffmpeg},
	}
}

func TestVerdictMatrix(t *testing.T) {
	const (
		build = "1785863997_9.0"
		newer = "1900000000_9.1"
	)
	cases := []struct {
		name      string
		v         Verdict
		known     bool
		available bool
		changes   []Change
	}{
		{
			name:  "everything matches",
			v:     verdict("v2.1.0", "2026.07.04", build, "v2.1.0", "2026.07.04", build),
			known: true,
		},
		{
			name:      "ytdl moved",
			v:         verdict("v2.2.0", "2026.07.04", build, "v2.1.0", "2026.07.04", build),
			known:     true,
			available: true,
			changes:   []Change{{ComponentYtdl, "v2.1.0", "v2.2.0"}},
		},
		{
			name:      "the pin moved",
			v:         verdict("v2.1.0", "2026.08.02", build, "v2.1.0", "2026.07.04", build),
			known:     true,
			available: true,
			changes:   []Change{{ComponentYtDlp, "2026.07.04", "2026.08.02"}},
		},
		{
			name:      "both moved, ytdl first",
			v:         verdict("v2.2.0", "2026.08.02", newer, "v2.1.0", "2026.07.04", build),
			known:     true,
			available: true,
			changes: []Change{
				{ComponentYtdl, "v2.1.0", "v2.2.0"},
				{ComponentYtDlp, "2026.07.04", "2026.08.02"},
				{ComponentFFmpeg, build, newer},
			},
		},
		{
			// The rollback lever (ADR-0016 §2): the pin names an OLDER yt-dlp than
			// the one installed, and that is an update to apply like any other. A
			// comparison that asked "is it newer?" would miss exactly this case,
			// which is the one the maintainer reaches for when something breaks.
			name:      "the pin rolls BACK to an older yt-dlp",
			v:         verdict("v2.1.0", "2026.06.01", build, "v2.1.0", "2026.07.04", build),
			known:     true,
			available: true,
			changes:   []Change{{ComponentYtDlp, "2026.07.04", "2026.06.01"}},
		},
		{
			name: "the ytdl probe stayed silent",
			v:    verdict("", "2026.07.04", build, "v2.1.0", "2026.07.04", build),
		},
		{
			name: "the pin stayed silent",
			v:    verdict("v2.1.0", "", "", "v2.1.0", "2026.07.04", build),
		},
		{
			// A source that did not answer is never a difference. Announcing an
			// update because a probe failed is the surface claiming a certainty it
			// does not have (ADR-0016 §8).
			name: "both sources silent, nothing is claimed",
			v:    verdict("", "", "", "v2.1.0", "2026.07.04", build),
		},
		{
			name:      "the pin answered but ytdl did not: what IS known still counts",
			v:         verdict("", "2026.08.02", build, "v2.1.0", "2026.07.04", build),
			available: true,
			changes:   []Change{{ComponentYtDlp, "2026.07.04", "2026.08.02"}},
		},
		{
			// A dev build has no tag to compare against and is never reported stale
			// — but its DEPENDENCIES still are, because they are pinned all the same.
			name:      "a dev build is never stale itself",
			v:         verdict("v2.2.0", "2026.07.04", build, DevVersion, "2026.07.04", build),
			known:     true,
			available: false,
		},
		{
			name:      "a dev build still reports a moved pin",
			v:         verdict("v2.2.0", "2026.08.02", build, DevVersion, "2026.07.04", build),
			known:     true,
			available: true,
			changes:   []Change{{ComponentYtDlp, "2026.07.04", "2026.08.02"}},
		},
		{
			// An install predating the marker has no ffmpeg build recorded. An empty
			// local side is uncompared rather than assumed stale.
			name:  "no ffmpeg marker: uncompared, not assumed stale",
			v:     verdict("v2.1.0", "2026.07.04", build, "v2.1.0", "2026.07.04", ""),
			known: true,
		},
		{
			name: "yt-dlp missing from the machine: uncompared, not assumed stale",
			v:    verdict("v2.1.0", "2026.07.04", build, "v2.1.0", "", build),
			// yt-dlp is uncompared; everything else matches.
			known: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.Known(); got != tc.known {
				t.Errorf("Known() = %v, want %v", got, tc.known)
			}
			if got := tc.v.Available(); got != tc.available {
				t.Errorf("Available() = %v, want %v", got, tc.available)
			}
			got := tc.v.Changes()
			if len(got) == 0 && len(tc.changes) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.changes) {
				t.Errorf("Changes() = %+v, want %+v", got, tc.changes)
			}
		})
	}
}

// Whether the ytdl BINARY moved is what decides between a handover with a reload
// and nothing at all — the common re-pinned-yt-dlp update costs neither
// (design §5, §7.2).
func TestChangedYtdlSeparatesTheRestartFromTheRest(t *testing.T) {
	const build = "1785863997_9.0"
	if v := verdict("v2.2.0", "2026.07.04", build, "v2.1.0", "2026.07.04", build); !v.ChangedYtdl() {
		t.Error("a moved ytdl must report ChangedYtdl")
	}
	if v := verdict("v2.1.0", "2026.08.02", build, "v2.1.0", "2026.07.04", build); v.ChangedYtdl() {
		t.Error("a dependency-only update must NOT claim the binary changed")
	}
	if v := verdict("v2.1.0", "2026.07.04", build, DevVersion, "2026.07.04", build); v.ChangedYtdl() {
		t.Error("a dev build must never report its own binary as changed")
	}
}

// The cache remembers the REMOTE answer, which cost a round trip. Pairing it with
// a freshly read LOCAL fact is what stops a surface announcing an update the user
// already applied by hand.
func TestWithInstalledRebasesACachedRoundOnLiveFacts(t *testing.T) {
	const build = "1785863997_9.0"
	cached := verdict("v2.1.0", "2026.08.02", build, "v2.1.0", "2026.07.04", build)
	if !cached.Available() {
		t.Fatal("precondition: the cached round should report an available update")
	}

	live := cached.WithInstalled(Installed{Ytdl: "v2.1.0", YtDlp: "2026.08.02", FFmpeg: build})
	if live.Available() {
		t.Errorf("after the user updated yt-dlp by hand, Changes() = %+v, want none", live.Changes())
	}
	if cached.Installed.YtDlp != "2026.07.04" {
		t.Error("WithInstalled mutated its receiver")
	}
	if live.CheckedAt != cached.CheckedAt || live.LatestYtdl != cached.LatestYtdl {
		t.Error("WithInstalled disturbed the remote half of the round")
	}
}
