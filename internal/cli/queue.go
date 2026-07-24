package cli

import (
	"fmt"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/queue"
)

// enqueuedTimeFormat is the compact local-time stamp shown next to each job.
const enqueuedTimeFormat = "02/01 15:04"

// RenderQueue formats a spool Snapshot for `ytdl queue`: only the LIVE work —
// running then pending (oldest first) — and a live-only footer. Lifetime
// completed/failed counts are deliberately absent: they belong to history
// (`ytdl history`/`status`), not to a "what is happening now" view (ADR-0009).
// It is pure — the caller reads the spool and handles the --watch redraw loop.
func RenderQueue(snap queue.Snapshot) string {
	var b strings.Builder
	b.WriteString("CODA ytdl\n")

	if len(snap.Running) == 0 && len(snap.Pending) == 0 {
		b.WriteString("  (coda vuota) · accoda con  ytdl -b <url>\n")
		return b.String()
	}
	if len(snap.Running) > 0 {
		fmt.Fprintf(&b, "  in corso (%d):\n", len(snap.Running))
		for _, e := range snap.Running {
			fmt.Fprintf(&b, "    ▸ %s\n", jobLine(e))
		}
	}
	if len(snap.Pending) > 0 {
		fmt.Fprintf(&b, "  in attesa (%d):\n", len(snap.Pending))
		for _, e := range snap.Pending {
			fmt.Fprintf(&b, "    • %s\n", jobLine(e))
		}
	}
	p, r, _, _ := snap.Counts()
	fmt.Fprintf(&b, "In coda: %d in attesa · %d in corso\n", p, r)
	return b.String()
}

// LiveOrdered flattens the live queue into the SAME order the cancel list numbers
// it: running first, then pending (each FIFO). The caller resolves a `ytdl cancel
// <n>` index against this slice, so the numbering the user sees and the job that
// gets cancelled cannot drift apart.
func LiveOrdered(snap queue.Snapshot) []queue.Entry {
	return append(append([]queue.Entry{}, snap.Running...), snap.Pending...)
}

// RenderCancelList numbers the live queue (running then pending) for `ytdl cancel`
// with no target, so the user can read off an index to cancel. Empty → a note.
func RenderCancelList(snap queue.Snapshot) string {
	ordered := LiveOrdered(snap)
	var b strings.Builder
	b.WriteString("ANNULLA — in corso o in attesa\n")
	if len(ordered) == 0 {
		b.WriteString("  (niente da annullare)\n")
		return b.String()
	}
	for i, e := range ordered {
		state := "in attesa"
		if e.State == queue.Running {
			state = "in corso"
		}
		fmt.Fprintf(&b, "  [%d] %-9s %s\n", i+1, state, jobLine(e))
	}
	b.WriteString("Annulla con:  ytdl cancel <n>   ·   tutto:  ytdl cancel --all\n")
	b.WriteString("  (l'indice riflette la coda ora; negli script usa il prefisso dell'id)\n")
	return b.String()
}

// RenderRetryList numbers the failed jobs (FIFO) for `ytdl retry` with no target.
// Empty → a note.
func RenderRetryList(failed []queue.Entry) string {
	var b strings.Builder
	b.WriteString("RIPROVA — download falliti\n")
	if len(failed) == 0 {
		b.WriteString("  (nessun download fallito da riprovare)\n")
		return b.String()
	}
	for i, e := range failed {
		fmt.Fprintf(&b, "  [%d] %s\n", i+1, jobLine(e))
	}
	b.WriteString("Riprova con:  ytdl retry <n>   ·   tutti:  ytdl retry --all\n")
	b.WriteString("  (l'indice riflette la lista ora; negli script usa il prefisso dell'id)\n")
	return b.String()
}

// RecentSummary is a windowed tally of terminal outcomes from the log store,
// computed by the caller (RenderStatus stays pure over it).
type RecentSummary struct{ OK, Failed int }

// RenderStatus formats the `ytdl status` summary: daemon liveness (informational
// — an on-demand/session-scoped daemon is normally down, ADR-0008), the live
// queue, and a recent windowed outcome tally from the log store (which includes
// foreground downloads). retentionDays labels the window explicitly (ADR-0009).
func RenderStatus(snap queue.Snapshot, daemonActive bool, recent RecentSummary, retentionDays int) string {
	p, r, _, _ := snap.Counts()
	daemon := "inattivo (si avvia quando serve)"
	if daemonActive {
		daemon = "attivo"
	}
	var b strings.Builder
	b.WriteString("ytdl — stato\n")
	fmt.Fprintf(&b, "  daemon:    %s\n", daemon)
	fmt.Fprintf(&b, "  in attesa: %d\n", p)
	fmt.Fprintf(&b, "  in corso:  %d\n", r)
	fmt.Fprintf(&b, "  recenti (%s): %d ok · %d %s\n", retentionWindow(retentionDays), recent.OK, recent.Failed, Plural(recent.Failed, "fallito", "falliti"))
	return b.String()
}

// Plural returns one when n == 1, else many — for grammatically correct Italian
// count labels ("1 fallito" vs "2 falliti").
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// retentionWindow renders the human window label for a windowed count/list, from
// the effective log_retention_days: "ultimi N giorni", or "da sempre" for 0
// (keep-forever). A number is never shown without its window (ADR-0009 P2).
func retentionWindow(retentionDays int) string {
	switch {
	case retentionDays <= 0:
		return "da sempre"
	case retentionDays == 1:
		return "ultimo giorno"
	default:
		return fmt.Sprintf("ultimi %d giorni", retentionDays)
	}
}

// jobLine is the per-job "<url>  (accodato <time>)" cell, tolerant of an
// unreadable job spec (URL empty). The label is shortened (scheme trimmed +
// length-capped) so the whole line stays within one terminal row — a wrapped
// line would break the --watch region redraw's line accounting (it counts
// logical newlines, not physical rows).
func jobLine(e queue.Entry) string {
	label := e.Job.URL
	if label == "" {
		label = "job illeggibile: " + e.ID
	}
	label = shortenURL(label)
	if e.Job.EnqueuedAt.IsZero() {
		return label
	}
	return fmt.Sprintf("%s  (accodato %s)", label, e.Job.EnqueuedAt.Format(enqueuedTimeFormat))
}
