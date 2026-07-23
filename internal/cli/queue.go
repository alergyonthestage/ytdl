package cli

import (
	"fmt"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/queue"
)

// enqueuedTimeFormat is the compact local-time stamp shown next to each job.
const enqueuedTimeFormat = "02/01 15:04"

// RenderQueue formats a spool Snapshot for `ytdl queue`: the running and pending
// jobs (oldest first), then a one-line count footer. It is pure — the caller
// reads the spool and handles the --watch redraw loop.
func RenderQueue(snap queue.Snapshot) string {
	var b strings.Builder
	b.WriteString("CODA ytdl\n")

	if len(snap.Running) == 0 && len(snap.Pending) == 0 {
		b.WriteString("  (nessun download in attesa o in corso)\n")
	} else {
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
	}

	b.WriteString(countsFooter(snap))
	return b.String()
}

// RenderStatus formats a Snapshot plus the daemon-liveness probe for
// `ytdl status`: a labelled summary.
func RenderStatus(snap queue.Snapshot, daemonActive bool) string {
	p, r, d, f := snap.Counts()
	daemon := "non attivo"
	if daemonActive {
		daemon = "attivo"
	}
	var b strings.Builder
	b.WriteString("ytdl — stato coda\n")
	fmt.Fprintf(&b, "  daemon:      %s\n", daemon)
	fmt.Fprintf(&b, "  in attesa:   %d\n", p)
	fmt.Fprintf(&b, "  in corso:    %d\n", r)
	fmt.Fprintf(&b, "  completati:  %d\n", d)
	fmt.Fprintf(&b, "  falliti:     %d\n", f)
	return b.String()
}

// jobLine is the per-job "<url>  (accodato <time>)" cell, tolerant of an
// unreadable job spec (URL empty).
func jobLine(e queue.Entry) string {
	url := e.Job.URL
	if url == "" {
		url = "(job illeggibile: " + e.ID + ")"
	}
	if e.Job.EnqueuedAt.IsZero() {
		return url
	}
	return fmt.Sprintf("%s  (accodato %s)", url, e.Job.EnqueuedAt.Format(enqueuedTimeFormat))
}

func countsFooter(snap queue.Snapshot) string {
	p, r, d, f := snap.Counts()
	return fmt.Sprintf("Totale: %d in attesa · %d in corso · %d completati · %d falliti\n", p, r, d, f)
}
