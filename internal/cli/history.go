package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/jobs"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/term"
)

// historyTimeFormat is the compact local-time stamp shown for each history row.
const historyTimeFormat = "02/01 15:04"

// labelMaxCols caps the title column so a long title cannot push the location
// (or the failure reason) off a normal terminal. Rows shorter than the cap are
// padded to the widest label present, so the columns line up.
const labelMaxCols = 34

// HistoryView is everything RenderHistory needs besides the records themselves.
// Passing it in keeps the renderer pure: no clock, no $HOME, no terminal probing
// inside the formatting code, so a test can pin every column exactly.
type HistoryView struct {
	RetentionDays int    // labels the window (ADR-0009 P2: never a list without its period)
	Width         int    // terminal columns; <= 0 means do not clip
	Home          string // for ~-contraction of paths; "" prints them absolute
	Search        string // the active --search query, echoed in the header
	OnlyFailed    bool   // whether --failed is active, echoed in the header
	Offset        int    // rows already listed before this page; the numbering starts after it

	// IndexUsable says whether `ytdl open <n>` / `ytdl again <n>` would resolve
	// <n> against THIS listing. Those commands always reload the default,
	// unfiltered history, so the index only means what the reader thinks it
	// means when no filter narrowed this list. When it is false the footer
	// advertises the id instead — see the row rendering below.
	IndexUsable bool
	// ShowIDs adds the stable id column. Forced on when IndexUsable is false
	// (the id is then the only handle that works) and available on demand via
	// `ytdl history --ids` for scripts.
	ShowIDs bool
}

// hintIndent aligns a failure's "what to do next" line under its row, far
// enough in that it reads as belonging to the row above rather than as another
// record.
const hintIndent = "        "

// hintMinCols is the floor for the wrapped hint's text column. Below it — a
// terminal narrower than the indent plus a couple of words — wrapping produces a
// column of fragments, so the text is left to the final width clip instead.
const hintMinCols = 24

// writeHintLines writes hint under a failed row, wrapped to the terminal width
// so an instruction is never truncated into a command that does not exist. An
// empty hint (no honest remedy) writes nothing at all, and width 0 (not a
// terminal — a pipe or a file) writes it on one line, since there is no width
// to wrap to.
func writeHintLines(b *strings.Builder, hint string, width int) {
	if hint == "" {
		return
	}
	cols := width - len(hintIndent)
	if width <= 0 || cols < hintMinCols {
		fmt.Fprintf(b, "%s%s\n", hintIndent, hint)
		return
	}
	for _, line := range term.Wrap(hint, cols) {
		fmt.Fprintf(b, "%s%s\n", hintIndent, line)
	}
}

// idCols is how much of the 16-hex record id is shown. Eight characters is
// already far past the point of ambiguity for one user's history, and Find
// accepts any unambiguous prefix.
const idCols = 8

// RenderHistory formats `ytdl history`: the durable log-store records (foreground
// and background), newest first, one line each, NUMBERED so the number can be
// handed straight to `ytdl open` / `ytdl again` (design §9.2). The numbering and
// the id-prefix grammar are the same ones cancel/retry use, so the tool has one
// way to name a thing, not three.
//
// Each row answers, in order: did it work · when · what · what format · and
// then the one fact that differs by outcome — WHERE it landed for a success,
// WHY it failed for a failure. That last column is the whole point of the
// redesign: previously a failure row was a bare ✗.
func RenderHistory(entries []logstore.Entry, v HistoryView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "STORICO ytdl — %s%s\n", retentionWindow(v.RetentionDays), historyFilterSuffix(v))

	if len(entries) == 0 {
		b.WriteString(historyEmptyLine(v))
		return clipTo(b.String(), v.Width)
	}

	labels := make([]string, len(entries))
	labelCols := 0
	for i, e := range entries {
		labels[i] = historyLabel(e)
		if w := term.DisplayWidth(labels[i]); w > labelCols {
			labelCols = w
		}
	}
	if labelCols > labelMaxCols {
		labelCols = labelMaxCols
	}

	// The index column is padded to the widest index, so row 10 does not shift
	// every column to its right by one (the whole point of the term.Pad work).
	idxCols := len(strconv.Itoa(v.Offset + len(entries)))

	for i, e := range entries {
		mark := "✓"
		if !e.Success {
			mark = "✗"
		}
		id := ""
		if v.ShowIDs || !v.IndexUsable {
			id = shortID(e) + " "
		}
		fmt.Fprintf(&b, "  [%*d] %s%s %s  %s  %-5s  %s\n",
			idxCols, v.Offset+i+1, id, mark, e.Time.Format(historyTimeFormat),
			term.Pad(labels[i], labelCols), formatCell(e), detailCell(e, v.Home))
		// The next step goes on its own line: the row above is already a full
		// terminal width of columns, and a hint appended to it would be the
		// first thing clipped away (G8, ux-principles.md §5). It WRAPS rather
		// than clipping, because clipping an instruction is worse than clipping
		// a title: at 80 columns the ffmpeg hint ended in "ytdl --updat…", a
		// command the user cannot run. `history` does not redraw in place, so
		// extra lines cost nothing here.
		if !e.Success {
			writeHintLines(&b, jobs.FailureHint(e.Error), v.Width)
		}
	}
	b.WriteString(historyFooter(v))
	return clipTo(b.String(), v.Width)
}

// shortID is the printed prefix of a record's stable id.
func shortID(e logstore.Entry) string {
	id := e.ID()
	if len(id) > idCols {
		return id[:idCols]
	}
	return id
}

// historyFooter tells the user what to type — and, crucially, only advertises
// the grammar that actually works for the list above it.
//
// `ytdl open`/`again` reload the DEFAULT history, so an index read off a
// filtered or re-limited listing would act on a different record. Rather than
// letting the footer promise something false (it used to say "l'indice riflette
// questa lista" under every listing, including filtered ones), a narrowed
// listing prints ids on its rows and points at those instead.
func historyFooter(v HistoryView) string {
	if !v.IndexUsable {
		return "Apri:  ytdl open <id>   ·   riscarica:  ytdl again <id>\n" +
			"  (questa lista è filtrata: l'indice vale solo per  ytdl history  senza filtri —\n" +
			"   qui usa l'id, che resta valido comunque)\n"
	}
	return "Apri:  ytdl open <n>   ·   riscarica:  ytdl again <n>   ·   solo falliti:  ytdl history --failed\n" +
		"  (l'indice riflette questa lista; negli script usa l'id:  ytdl history --ids)\n"
}

// historyFilterSuffix states the active filters in the header, so a short list
// never reads as "you have downloaded almost nothing".
func historyFilterSuffix(v HistoryView) string {
	var parts []string
	if v.OnlyFailed {
		parts = append(parts, "solo non riusciti")
	}
	if q := strings.TrimSpace(v.Search); q != "" {
		parts = append(parts, fmt.Sprintf("ricerca %q", q))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// historyEmptyLine teaches the next step instead of only reporting emptiness
// (ux-principles.md §5) — and, when a filter is active, says that the filter is
// why the list is empty rather than letting the user conclude the tool lost
// their downloads.
func historyEmptyLine(v HistoryView) string {
	switch {
	case strings.TrimSpace(v.Search) != "":
		return "  (nessun download corrisponde) · prova senza --search\n"
	case v.OnlyFailed:
		return "  (nessun download non riuscito) · tutti:  ytdl history\n"
	default:
		return "  (nessun download registrato) · scaricane uno con  ytdl <url>\n"
	}
}

// formatCell is the audio format as an extension (".mp3"), or blank for a record
// that never recorded one.
func formatCell(e logstore.Entry) string {
	if e.Format == "" {
		return ""
	}
	return "." + e.Format
}

// detailCell is the row's outcome-dependent column: why it failed, or where it
// landed. A failure with no recorded reason (a pre-Cycle-5 record) points at the
// per-job log rather than showing an empty cell.
func detailCell(e logstore.Entry, home string) string {
	if !e.Success {
		if r := strings.TrimSpace(e.Error); r != "" {
			return r
		}
		return "(motivo non registrato — vedi il .log)"
	}
	if loc := historyLocation(e); loc != "" {
		return contractHome(loc, home)
	}
	return ""
}

// historyLocation is the folder a successful job's files went to: the directory
// of the saved file when known (which is what shows a playlist's own subfolder),
// falling back to the job's configured output dir.
func historyLocation(e logstore.Entry) string {
	if e.Path != "" {
		return filepath.Dir(e.Path)
	}
	return e.Dir
}

// contractHome rewrites a path under home as "~/…", the form a user recognises
// at a glance and which keeps the column narrow. An empty home (unresolvable
// $HOME) leaves the path absolute.
func contractHome(path, home string) string {
	if home == "" || path == "" {
		return path
	}
	if path == home {
		return "~"
	}
	prefix := home
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + path[len(prefix):]
	}
	return path
}

// historyLabel prefers the resolved track title; a record with no title (a
// failure that never resolved metadata) falls back to a shortened URL. A job
// that saved several files says so, since one title cannot represent them.
func historyLabel(e logstore.Entry) string {
	base := e.Title
	if base == "" {
		base = shortenURL(e.URL)
	}
	if e.Count > 1 {
		return fmt.Sprintf("%s (%d tracce)", base, e.Count)
	}
	return base
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

// clipTo caps every line to width columns, so a long title or failure reason
// never wraps onto a second physical row and breaks the table. width <= 0 leaves
// the text alone (a pipe or a file has no width).
func clipTo(s string, width int) string {
	if width <= 0 {
		return s
	}
	return clipLines(s, width)
}
