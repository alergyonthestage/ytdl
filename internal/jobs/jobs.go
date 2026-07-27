// Package jobs holds the queue actions both channels perform, so the GUI and
// the CLI cannot drift apart on what "annulla" and "riscarica" mean. Each was
// previously a private helper inside cmd/ytdl, reachable only by the CLI; the
// GUI gaining the same actions in Cycle 5 is what makes a shared home
// necessary (design §10).
//
// Everything here is a thin, ordering-sensitive composition over
// internal/queue's primitives. The package owns the ORDER and the guards, not
// the storage.
package jobs

import (
	"errors"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// ErrAlreadyQueued is returned by Again when the same link is already waiting or
// running. The GUI turns it into a 409, the CLI into a message.
var ErrAlreadyQueued = errors.New("jobs: this link is already pending or running")

// ErrNoURL is returned by Again for a record with no usable link — nothing can
// be re-downloaded from it.
var ErrNoURL = errors.New("jobs: the record has no URL")

// Cancel stops one live job by spool id. Order is the whole point: the cancel
// MARKER is dropped first, so a job the daemon claims in the meantime is still
// stopped by the watcher that polls for it; only then is a still-pending job
// deleted.
//
// wasPending reports which of the two happened — true if the job was still
// waiting and is now gone (the marker is cleaned up, since it was never needed),
// false if it was already running and the daemon's watcher will act on the
// marker. A non-nil error means the cancel did NOT take effect: either the
// marker could not be written, or the pending delete hit a real filesystem
// error. Callers must not report success on an error — a job that keeps
// downloading after the user pressed "Annulla" is the worst outcome here.
func Cancel(sp *queue.Spool, id string) (wasPending bool, err error) {
	if err := sp.RequestCancel(id); err != nil {
		return false, err
	}
	was, err := sp.CancelPending(id)
	if err != nil {
		return false, err
	}
	if was {
		_ = sp.ClearCancel(id) // deleted before it ever ran → the marker is moot
	}
	return was, nil
}

// Again enqueues a NEW job reproducing a history record ("Riscarica"). It is
// deliberately not `retry`: retry requeues a spool job that still exists, keeping
// its identity and its settings snapshot, while a history record outlives the
// spool entry and can only be reproduced (ux-principles.md §3).
//
// What is reproduced and what is not:
//
//   - Format and Playlist come from the RECORD. They decide what gets
//     downloaded, so a ✓ .flac row whose primary action quietly produced an .mp3
//     would not be "the same thing again". A record with no or an unknown format
//     (written by an older ytdl, or hand-edited) falls back to the current
//     setting rather than failing.
//   - Everything else — the output folder above all — comes from the CURRENT
//     settings. The folder the file went to a month ago may not exist any more,
//     and a user who has since changed it means the new one.
//   - The known title is carried over so the queue can name the job immediately
//     instead of showing a bare URL until the daemon resolves it. Never for a
//     playlist, whose single title would misrepresent the whole job (Cycle 4).
//
// It refuses when the same normalised URL is already pending or running:
// without that, a double-click (or an impatient second press in the CLI) queues
// the same download twice. The check is a read followed by a write on a
// deliberately lock-free spool, so a genuinely simultaneous pair of requests can
// still slip through; it closes the case that actually happens, not a race
// between two humans.
func Again(sp *queue.Spool, e logstore.Entry, s config.Settings) (string, error) {
	url := strings.TrimSpace(e.URL)
	if url == "" {
		return "", ErrNoURL
	}
	snap, err := sp.List()
	if err != nil {
		return "", err
	}
	if IsQueued(snap, url) {
		return "", ErrAlreadyQueued
	}
	if config.ValidFormat(e.Format) {
		s.Format = e.Format
	}
	j := queue.Job{URL: url, Playlist: e.Playlist, Settings: s}
	if !e.Playlist {
		j.Title = e.Title
	}
	return sp.Enqueue(j)
}

// IsQueued reports whether url is already waiting or running in snap, comparing
// on the same normalised-URL identity that keys breadcrumbs, spool ids and
// history titles — so a link that differs only by a #fragment or by scheme case
// is recognised as the same download. Terminal states (done/failed) do not
// count: re-downloading something that already finished is exactly what
// "Riscarica" is for.
func IsQueued(snap queue.Snapshot, url string) bool {
	target := logstore.NormalizeURL(url)
	if target == "" {
		return false
	}
	for _, group := range [][]queue.Entry{snap.Pending, snap.Running} {
		for _, e := range group {
			if logstore.NormalizeURL(e.Job.URL) == target {
				return true
			}
		}
	}
	return false
}
