package cli

import (
	"fmt"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/logstore"
)

// historyTimeFormat is the compact local-time stamp shown for each history row.
const historyTimeFormat = "02/01 15:04"

// RenderHistory formats `ytdl history`: the durable log-store records (foreground
// and background), newest first, title-first. retentionDays labels the window
// (ADR-0009 P2). Pure — the caller loads and filters the records.
func RenderHistory(entries []logstore.Entry, retentionDays int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "STORICO ytdl — %s\n", retentionWindow(retentionDays))
	if len(entries) == 0 {
		b.WriteString("  (nessun download registrato)\n")
		return b.String()
	}
	for _, e := range entries {
		mark := "✓"
		if !e.Success {
			mark = "✗"
		}
		fmt.Fprintf(&b, "  %s %s  %s\n", mark, e.Time.Format(historyTimeFormat), historyLabel(e))
	}
	return b.String()
}

// historyLabel prefers the resolved track title; a record with no title (a
// failure that never resolved metadata) falls back to a shortened URL, flagged
// as failed so the row still reads clearly.
func historyLabel(e logstore.Entry) string {
	if e.Title != "" {
		return e.Title
	}
	if !e.Success {
		return "(fallito) " + shortenURL(e.URL)
	}
	return shortenURL(e.URL)
}

// shortenURL trims the scheme/www and caps the length (rune-safe, so a multi-byte
// character is never split into invalid UTF-8), so a bare URL fits one line
// without wrapping.
func shortenURL(u string) string {
	s := strings.TrimPrefix(u, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	const max = 40
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}
