package cli

import (
	"fmt"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/queue"
	"github.com/alergyonthestage/ytdl/internal/term"
)

// enqueuedTimeFormat is the compact local-time stamp shown next to each job.
const enqueuedTimeFormat = "02/01 15:04"

// QueueView is everything RenderQueue needs besides the snapshot, so the
// renderer stays pure — no $HOME lookup and no terminal probing inside the
// formatting code. Same shape, and same reason, as HistoryView.
type QueueView struct {
	// Full prints whole URLs: the one-shot view, where a wrapped line is
	// harmless. When false every line is capped to Width for the in-place
	// --watch redraw, whose region math counts logical newlines and would
	// desync if a line wrapped.
	Full  bool
	Width int    // terminal columns; <= 0 means do not clip
	Home  string // for ~-contraction of the destination; "" prints it absolute
}

// RenderQueue formats a spool Snapshot for `ytdl queue`: only the LIVE work —
// running then pending (oldest first) — and a live-only footer. Lifetime
// completed/failed counts are deliberately absent: they belong to history
// (`ytdl history`/`status`), not to a "what is happening now" view (ADR-0009).
// It is pure — the caller reads the spool and handles the --watch redraw loop.
//
// Each job is shown title-first (the resolved "Artist - Track", once known) with
// its full URL on the line below, so the user can tell which video a job is
// (Cycle 4), and its destination under that: the queue was the one list that
// never said where a file was going (G1).
func RenderQueue(snap queue.Snapshot, v QueueView) string {
	var b strings.Builder
	b.WriteString("CODA ytdl\n")

	if len(snap.Running) == 0 && len(snap.Pending) == 0 {
		b.WriteString("  (coda vuota) · accoda con  ytdl -b <url>\n")
	} else {
		if len(snap.Running) > 0 {
			fmt.Fprintf(&b, "  in corso (%d):\n", len(snap.Running))
			for _, e := range snap.Running {
				writeJobEntry(&b, "    ▸ ", e)
				writeJobDest(&b, e, v.Home)
			}
		}
		if len(snap.Pending) > 0 {
			fmt.Fprintf(&b, "  in attesa (%d):\n", len(snap.Pending))
			for _, e := range snap.Pending {
				writeJobEntry(&b, "    • ", e)
				writeJobDest(&b, e, v.Home)
			}
		}
		p, r, _, _ := snap.Counts()
		fmt.Fprintf(&b, "In coda: %d in attesa · %d in corso\n", p, r)
	}

	out := b.String()
	// In --watch (Full=false), clip EVERY line — job lines and the fixed header/
	// section/footer alike — to the terminal width, so nothing wraps onto a second
	// physical row and desyncs the region redraw's logical-line accounting.
	if !v.Full && v.Width > 0 {
		out = clipLines(out, v.Width)
	}
	return out
}

// clipLines caps each line of s to width terminal COLUMNS (term.Clip), counting
// East-Asian-wide and emoji runes as two columns. It is the single choke point that
// guarantees the --watch redraw never wraps, whatever the content or terminal size.
func clipLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = term.Clip(ln, width)
	}
	return strings.Join(lines, "\n")
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
		writeJobEntry(&b, fmt.Sprintf("  [%d] %-9s ", i+1, state), e)
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
		writeJobEntry(&b, fmt.Sprintf("  [%d] ", i+1), e)
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

// contIndent aligns a job's continuation (URL) line under its title line.
const contIndent = "      "

// jobTitle is the display title for an entry: the resolved "Artist - Track" once
// known. It is deliberately empty for a playlist (its per-item before_dl title
// misrepresents the whole job) and for an unreadable spec, so the caller shows the
// URL as the primary label instead.
func jobTitle(e queue.Entry) string {
	if e.Job.Playlist {
		return ""
	}
	return e.Job.Title
}

// jobURLCell is the "<full-url>  (accodato <time>)" detail for an entry, tolerant
// of an unreadable spec (URL empty → the job id). A playlist is flagged, since it
// pulls more than the single URL implies. The URL is NOT shortened here — callers
// cap the whole line to the terminal width only for the --watch redraw (see clip).
func jobURLCell(e queue.Entry) string {
	url := e.Job.URL
	switch {
	case url == "":
		url = "job illeggibile: " + e.ID
	case e.Job.Playlist:
		url += "  (playlist)"
	}
	if e.Job.EnqueuedAt.IsZero() {
		return url
	}
	return fmt.Sprintf("%s  (accodato %s)", url, e.Job.EnqueuedAt.Format(enqueuedTimeFormat))
}

// writeJobDest writes a queued job's destination under its URL line. The dir is
// the one RESOLVED AT ENQUEUE and frozen in the job's settings snapshot, so the
// line states where THIS job will land — not where the configuration would send
// it now, which is a different question once the settings have been edited.
// An unreadable spec (no settings) writes nothing rather than an empty label.
func writeJobDest(b *strings.Builder, e queue.Entry, home string) {
	dir := e.Job.Settings.OutputDir
	if dir == "" {
		return
	}
	fmt.Fprintf(b, "%scartella: %s\n", contIndent, contractHome(dir, home))
}

// writeJobEntry writes one job under prefix: a title line plus an indented URL line
// when the title is known, else a single URL line. It always writes the FULL text;
// the --watch caller caps each finished line to the terminal width (RenderQueue's
// clipLines), so clipping lives in one place instead of being threaded per field.
func writeJobEntry(b *strings.Builder, prefix string, e queue.Entry) {
	if title := jobTitle(e); title != "" {
		fmt.Fprintf(b, "%s%s\n", prefix, title)
		fmt.Fprintf(b, "%s%s\n", contIndent, jobURLCell(e))
		return
	}
	fmt.Fprintf(b, "%s%s\n", prefix, jobURLCell(e))
}
