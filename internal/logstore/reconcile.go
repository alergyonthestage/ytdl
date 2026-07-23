package logstore

import (
	"bufio"
	"os"
	"strings"
	"time"
)

// itemFieldSep is a literal TAB byte separating a playlist item's id from its
// title in the AttemptedItemTemplate output. It is emitted verbatim by yt-dlp
// (literal template text, no escape interpretation involved), and the id
// charset never contains it, so splitting on the FIRST tab is unambiguous even
// when a title itself contains one.
const itemFieldSep = "\t"

// AttemptedItemTemplate and SucceededItemTemplate are the yt-dlp print
// templates the runner appends as extra --print-to-file sinks (AFTER
// core.BuildArgs, so the golden argv is untouched) for a silent/background
// playlist download with breadcrumbs enabled:
//
//   - AttemptedItemTemplate fires as each item's download starts → "<id>\t<title>";
//   - SucceededItemTemplate fires after each item is moved into place → "<id>".
//
// The set difference (attempted − succeeded) is exactly the failed items.
const (
	AttemptedItemTemplate = "before_dl:%(id)s" + itemFieldSep + "%(artist,creator,uploader)s - %(track,title)s"
	SucceededItemTemplate = "after_move:%(id)s"
)

// Item is a playlist entry observed during a download: its yt-dlp id (the stable
// breadcrumb key) and a human title (the breadcrumb's readable label).
type Item struct {
	ID    string
	Title string
}

// ReconcilePlaylist maintains one breadcrumb per failed playlist item, keyed by
// Hash(item id). Every succeeded item first has its own breadcrumb removed (U7
// per-item auto-cleanup — clearing a marker left by an earlier failed run);
// then every attempted-but-not-succeeded item gets a breadcrumb written.
// srcURL/rc/when are recorded in each failed item's breadcrumb body.
func ReconcilePlaylist(outDir, srcURL string, rc int, when time.Time, attempted []Item, succeededIDs []string) error {
	done := make(map[string]bool, len(succeededIDs))
	var firstErr error
	for _, id := range succeededIDs {
		done[id] = true
		if err := RemoveBreadcrumb(outDir, Hash(id)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, it := range attempted {
		if it.ID == "" || done[it.ID] {
			continue
		}
		if err := WriteBreadcrumb(outDir, it.Title, Hash(it.ID), srcURL, rc, when); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ParseAttempted reads AttemptedItemTemplate output ("<id>\t<title>" per line),
// splitting each line on its first tab. Blank lines and lines with an empty id
// are skipped. A missing file yields nil.
func ParseAttempted(path string) []Item {
	var items []Item
	forEachLine(path, func(line string) {
		id, title, _ := strings.Cut(line, itemFieldSep)
		if id == "" {
			return
		}
		items = append(items, Item{ID: id, Title: title})
	})
	return items
}

// ParseSucceededIDs reads SucceededItemTemplate output (one id per line),
// skipping blanks. A missing file yields nil.
func ParseSucceededIDs(path string) []string {
	var ids []string
	forEachLine(path, func(line string) {
		if line != "" {
			ids = append(ids, line)
		}
	})
	return ids
}

// forEachLine calls fn for each line of the file at path, with the scanner
// buffer widened for long title lines. A read error or missing file simply
// stops iteration (best-effort, like the rest of the package).
func forEachLine(path string, fn func(string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
}
