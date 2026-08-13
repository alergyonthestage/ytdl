// Package update owns ytdl's update path: detecting that an update exists,
// caching that verdict off every critical path, and (from Cycle 6-plus onward)
// launching the installer that applies it. It imports buildinfo, config and the
// standard library only — internal/core stays byte-frozen and internal/daemon is
// untouched, so everything the daemon needs from here arrives by injection from
// cmd/ytdl (ADR-0016).
//
// Two sources answer two different questions, and both comparisons are string
// equality rather than version ordering (ADR-0016 §1):
//
//   - "what is the newest ytdl?" — the redirect github.com answers to
//     releases/latest, whose target tag IS the answer, with no API and no
//     rate-limit budget;
//   - "which yt-dlp and which ffmpeg should this machine be running?" — deps.conf
//     at the repository root, which is ytdl's decision and never the user's
//     (ADR-0016 §2).
//
// String equality is possible because both components already carry, locally, the
// exact string their tag carries: buildinfo.Version is the release's
// GITHUB_REF_NAME and `yt-dlp --version` is byte-identical to its tag. So there
// is no semver parser, no date parser and none of the bug class ordering brings.
//
// Nothing here is version-aware in the other sense either: a pin may legitimately
// name an OLDER yt-dlp than the one installed — that is the rollback lever, and
// it is an update to apply like any other. Every derivation below therefore
// reports what DIFFERS, never what is newer.
package update

import "time"

// DevVersion is buildinfo.Version for a local build. It is never checked and
// never reported stale (ADR-0016 §1): a working tree has no tag to compare
// against, and telling its author they are behind their own commit is noise.
const DevVersion = "dev"

// The component names. They are what the user reads, so they are each tool's own
// spelling rather than a Go identifier, and they are the keys the GUI's changes
// table is drawn from.
const (
	ComponentYtdl   = "ytdl"
	ComponentYtDlp  = "yt-dlp"
	ComponentFFmpeg = "ffmpeg"
)

// Pin is what deps.conf declares, already RESOLVED: "latest" never survives as
// far as this struct, so every comparison downstream is between two concrete
// version strings and never against a placeholder.
type Pin struct {
	YtDlp  string `json:"yt_dlp,omitempty"` // exact tag; "" = not answered
	FFmpeg string `json:"ffmpeg,omitempty"` // exact build id; "" = not answered
}

// Installed is what this machine actually has, read locally. These are facts
// about the install and are always shown, whether or not any probe ever
// succeeded (ADR-0016 §8).
type Installed struct {
	Ytdl   string `json:"ytdl,omitempty"`
	YtDlp  string `json:"yt_dlp,omitempty"`
	FFmpeg string `json:"ffmpeg,omitempty"`

	// Foreign names the dependencies that resolved OUTSIDE the directory ytdl
	// installs into — a Homebrew yt-dlp winning the $PATH, say. It is a different
	// problem from being out of date, with a different remedy, so it is a separate
	// fact rather than a kind of staleness (ADR-0016 §4).
	Foreign []string `json:"foreign,omitempty"`
}

// Verdict is one check round, as cached. Everything a surface asks of it is
// DERIVED below rather than stored — the same discipline the history record's id
// follows, and the reason a stale cache can never contradict the binary reading
// it.
type Verdict struct {
	CheckedAt  time.Time `json:"checked_at"`
	LatestYtdl string    `json:"latest_ytdl,omitempty"` // "" = the probe did not answer
	Pin        Pin       `json:"pin"`
	Installed  Installed `json:"installed"`
}

// Change is one component that would move, for the surface that shows the
// detail. From and To are stated in that order and never labelled "old"/"new":
// after a rollback To is the older string of the two.
type Change struct {
	Component string `json:"component"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// Known reports whether BOTH sources answered. It is the difference between "sei
// aggiornato" and "non verificato" (ADR-0016 §8), and the two never collapse:
// a machine behind a captive portal is not a machine that is up to date.
func (v Verdict) Known() bool {
	return v.LatestYtdl != "" && v.Pin.YtDlp != "" && v.Pin.FFmpeg != ""
}

// IsDev reports whether the round describes a local build, which is never
// compared and never reported stale.
func (v Verdict) IsDev() bool { return v.Installed.Ytdl == DevVersion }

// Available reports whether anything DIFFERS — never whether anything is newer.
// One axis, whatever moved: the user's question is "is there an update for me?",
// and the answer is the same whether what changed is the ytdl binary, the pinned
// yt-dlp, or both (ADR-0016 §8).
func (v Verdict) Available() bool { return len(v.Changes()) > 0 }

// Changes lists what an update would move. The order is fixed — ytdl first,
// because that is the thing the user is being asked to update; its dependencies
// after, because they are attributes of that install rather than a second
// decision.
func (v Verdict) Changes() []Change {
	var out []Change
	if !v.IsDev() {
		out = appendChange(out, ComponentYtdl, v.Installed.Ytdl, v.LatestYtdl)
	}
	out = appendChange(out, ComponentYtDlp, v.Installed.YtDlp, v.Pin.YtDlp)
	out = appendChange(out, ComponentFFmpeg, v.Installed.FFmpeg, v.Pin.FFmpeg)
	return out
}

// appendChange records a component only when both sides are known AND they
// differ. An empty side is a question nobody answered — never a licence to guess,
// and never a difference: reporting an update because a probe failed is the
// surface stating a certainty it does not have (ADR-0016 §8).
func appendChange(out []Change, component, from, to string) []Change {
	if from == "" || to == "" || from == to {
		return out
	}
	return append(out, Change{Component: component, From: from, To: to})
}

// ChangedYtdl reports whether the ytdl BINARY is one of the things that would
// move. It is what decides whether applying the update costs a handover and a
// reload, or nothing at all: the common case after the installer became
// idempotent is a re-pinned yt-dlp, which needs neither (design §5, §7.2).
func (v Verdict) ChangedYtdl() bool {
	for _, c := range v.Changes() {
		if c.Component == ComponentYtdl {
			return true
		}
	}
	return false
}

// WithInstalled returns v with its local facts replaced by the given ones.
//
// The cache exists to remember the REMOTE answer, which costs a network round
// trip; the local facts cost a file read and a `--version`. Pairing a remembered
// remote answer with a freshly read local one is what stops a surface announcing
// an update the user already applied by hand — the cached Installed would still
// name yesterday's yt-dlp.
func (v Verdict) WithInstalled(in Installed) Verdict {
	v.Installed = in
	return v
}
