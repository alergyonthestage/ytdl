package logstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestExtendedFieldsRoundTrip asserts the Cycle 5 record survives the write/read
// cycle intact: Append copies every new field from the Job onto the Entry, and
// Load gives it back. Without this, "where did the file go?" has no answer.
func TestExtendedFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 7, 27, 18, 4, 0, 123456789, time.UTC)
	job := Job{
		URL: "https://youtu.be/A", Title: "Artista - Traccia", Mode: "default",
		Format: "mp3", RC: 0, Success: true, Time: when,
		Path: "/home/u/Music/ytdl/Artista - Traccia.mp3", Dir: "/home/u/Music/ytdl",
		Count: 3, Playlist: true,
	}
	if err := Append(dir, job); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	e := got[0]
	if e.Path != job.Path {
		t.Errorf("Path = %q, want %q", e.Path, job.Path)
	}
	if e.Dir != job.Dir {
		t.Errorf("Dir = %q, want %q", e.Dir, job.Dir)
	}
	if e.Count != 3 {
		t.Errorf("Count = %d, want 3", e.Count)
	}
	if !e.Playlist {
		t.Error("Playlist = false, want true")
	}
}

// TestPreCycle5RecordLoadsWithEmptyExtras is the backward-compatibility contract
// (design §5.3): a line written by an older ytdl must still load, with the new
// fields simply zero — that is what lets the actions be offered as unavailable
// instead of requiring a migration of history.jsonl.
func TestPreCycle5RecordLoadsWithEmptyExtras(t *testing.T) {
	dir := t.TempDir()
	old := `{"time":"2026-07-20T10:00:00Z","url":"https://youtu.be/OLD","title":"Vecchio","mode":"silent","format":"mp3","rc":0,"success":true}`
	if err := os.WriteFile(filepath.Join(dir, historyFile), []byte(old+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("a pre-Cycle-5 line must still load: got %d records", len(got))
	}
	e := got[0]
	if e.Title != "Vecchio" {
		t.Errorf("existing fields must survive: Title = %q", e.Title)
	}
	if e.Path != "" || e.Dir != "" || e.Count != 0 || e.Playlist || e.Error != "" {
		t.Errorf("new fields must be zero on an old record, got %+v", e)
	}
	if e.ID() == "" {
		t.Error("an old record must still be addressable by ID")
	}
}

// TestNewFieldsAreOmittedWhenEmpty guards the omitempty tags: a record with no
// extras must not grow five null/zero keys, or every existing history line would
// change shape on rewrite.
func TestNewFieldsAreOmittedWhenEmpty(t *testing.T) {
	line, err := json.Marshal(Entry{Time: time.Now(), URL: "u", Mode: "silent", Format: "mp3"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"path", "dir", "count", "playlist", "error"} {
		if strings.Contains(string(line), `"`+key+`"`) {
			t.Errorf("empty field %q was serialised: %s", key, line)
		}
	}
}

// TestIDStableAcrossMarshalRoundTrip is the load-bearing property of the derived
// id: the handle a user copies from one channel must resolve in the other, and
// must survive the JSON round trip through history.jsonl. time.Now() is used
// deliberately — it carries a monotonic reading and a named local zone, both of
// which the round trip drops.
func TestIDStableAcrossMarshalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	job := Job{URL: "https://youtu.be/A", Title: "T", Mode: "default", Format: "mp3", Success: true, Time: now}
	want := Entry{Time: now, URL: job.URL}.ID()

	if err := Append(dir, job); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	if got[0].ID() != want {
		t.Errorf("ID changed across the round trip: %q on disk, %q in memory", got[0].ID(), want)
	}
	if len(want) != hashLen {
		t.Errorf("ID length = %d, want %d hex chars", len(want), hashLen)
	}
}

// TestIDDistinctForSameURLDifferentTime: re-downloading the same link must not
// make the two records indistinguishable, or `ytdl open <id>` would be ambiguous
// exactly when a user retries something.
func TestIDDistinctForSameURLDifferentTime(t *testing.T) {
	base := time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)
	a := Entry{Time: base, URL: "https://youtu.be/A"}
	b := Entry{Time: base.Add(time.Nanosecond), URL: "https://youtu.be/A"}
	if a.ID() == b.ID() {
		t.Fatalf("two records one nanosecond apart share the id %q", a.ID())
	}
	// Same time, different URL must also differ.
	c := Entry{Time: base, URL: "https://youtu.be/B"}
	if a.ID() == c.ID() {
		t.Fatalf("two different URLs at the same instant share the id %q", a.ID())
	}
}

// TestLogNameMatchesRecordedFile is the contract that lets a history record find
// its own failure log with no stored path: the name Entry.LogName composes must
// be the name Record actually wrote for the same job.
func TestLogNameMatchesRecordedFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success bool
	}{{"success", true}, {"failure", false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			when := time.Date(2026, 7, 27, 18, 4, 5, 123456789, time.UTC)
			job := Job{URL: "https://youtu.be/A?x=1#frag", Mode: "default", Format: "mp3", Success: tc.success, Time: when, Stderr: []byte("boom")}
			if err := Record(dir, job); err != nil {
				t.Fatal(err)
			}
			if err := Append(dir, job); err != nil {
				t.Fatal(err)
			}
			entries, err := Load(dir, QueryOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("want 1 record, got %d", len(entries))
			}
			logPath := filepath.Join(dir, entries[0].LogName())
			if _, err := os.Stat(logPath); err != nil {
				names, _ := filepath.Glob(filepath.Join(dir, "*.log"))
				t.Fatalf("LogName() = %q does not exist; Record wrote %v", entries[0].LogName(), names)
			}
		})
	}
}

// TestCapReasonSanitises covers the derivation rule of design §5.2: the stored
// reason is one short, inert line. Escape sequences matter because the string is
// later replayed into a terminal and into an HTML page.
func TestCapReasonSanitises(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain line is kept", "ERROR: HTTP Error 403: Forbidden", "ERROR: HTTP Error 403: Forbidden"},
		{"empty stays empty", "", ""},
		{"ansi colour is stripped whole", "\x1b[0;31mERROR:\x1b[0m nope", "ERROR: nope"},
		{"osc string is stripped whole", "\x1b]0;title\x07ERROR", "ERROR"},
		{"control chars fold to spaces, not glue", "a\tb\x00c", "a b c"},
		{"whitespace runs collapse", "  ERROR:   too    spaced  ", "ERROR: too spaced"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := capReason(tc.in); got != tc.want {
				t.Errorf("capReason(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCapReasonTruncatesLongLines(t *testing.T) {
	got := capReason(strings.Repeat("x", 5000))
	if r := []rune(got); len(r) != reasonMaxRunes {
		t.Fatalf("capped reason is %d runes, want %d", len(r), reasonMaxRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated reason must be marked with an ellipsis, got %q", got[len(got)-8:])
	}
}

// TestCapReasonIsRuneSafe: truncation must never split a multi-byte character,
// which would put invalid UTF-8 into history.jsonl and into the API's JSON.
func TestCapReasonIsRuneSafe(t *testing.T) {
	got := capReason(strings.Repeat("é", 400))
	if !utf8.ValidString(got) {
		t.Fatalf("capReason produced invalid UTF-8: %q", got)
	}
	if r := []rune(got); len(r) != reasonMaxRunes {
		t.Errorf("capped reason is %d runes, want %d", len(r), reasonMaxRunes)
	}
}

// TestAppendSanitisesReason: the capping must be impossible to bypass by writing
// a raw stderr line into Job.Error — it happens on the way to disk, in one place.
func TestAppendSanitisesReason(t *testing.T) {
	dir := t.TempDir()
	job := Job{
		URL: "https://youtu.be/A", Mode: "default", Format: "mp3", Time: time.Now(),
		Error: "\x1b[31mERROR:\x1b[0m " + strings.Repeat("y", 5000),
	}
	if err := Append(dir, job); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	if strings.Contains(got[0].Error, "\x1b") {
		t.Errorf("stored reason still carries an escape sequence: %q", got[0].Error)
	}
	if r := []rune(got[0].Error); len(r) > reasonMaxRunes {
		t.Errorf("stored reason is %d runes, want at most %d", len(r), reasonMaxRunes)
	}
}

// searchFixture writes four records with distinct titles, URLs and file names.
func searchFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	jobs := []Job{
		{URL: "https://youtu.be/one", Title: "Mina - Grande grande", Time: base, Success: true, Path: "/m/ytdl/Mina - Grande grande.mp3", Dir: "/m/ytdl"},
		{URL: "https://youtu.be/two", Title: "Battisti - Il tempo", Time: base.Add(time.Minute), Success: true, Path: "/m/ytdl/Battisti - Il tempo.mp3", Dir: "/m/ytdl"},
		{URL: "https://youtu.be/three", Title: "", Time: base.Add(2 * time.Minute), Success: false, Error: "HTTP Error 403"},
		{URL: "https://vimeo.com/mina", Title: "Altro", Time: base.Add(3 * time.Minute), Success: true, Path: "/m/ytdl/Altro.mp3", Dir: "/m/ytdl"},
	}
	for _, j := range jobs {
		if err := Append(dir, j); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadSearchMatchesTitleURLAndFileName(t *testing.T) {
	dir := searchFixture(t)
	tests := []struct {
		name  string
		query string
		want  []string // expected URLs, newest first
	}{
		{"matches the title, case-insensitively", "GRANDE", []string{"https://youtu.be/one"}},
		{"matches the URL", "vimeo", []string{"https://vimeo.com/mina"}},
		{"matches title and URL across records", "mina", []string{"https://vimeo.com/mina", "https://youtu.be/one"}},
		{"matches the saved file name", "Battisti - Il tempo.mp3", []string{"https://youtu.be/two"}},
		{"no match yields nothing", "zzzz", nil},
		{"empty query is not a filter", "", []string{"https://vimeo.com/mina", "https://youtu.be/three", "https://youtu.be/two", "https://youtu.be/one"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Load(dir, QueryOpts{Search: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Search(%q) returned %d records, want %d", tc.query, len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].URL != w {
					t.Errorf("record %d = %q, want %q", i, got[i].URL, w)
				}
			}
		})
	}
}

// TestLoadSearchDoesNotMatchTheDownloadFolder: every record shares the output
// directory, so searching for it must not return the whole history (that would
// make the GUI search box useless for its actual purpose).
func TestLoadSearchDoesNotMatchTheDownloadFolder(t *testing.T) {
	dir := searchFixture(t)
	got, err := Load(dir, QueryOpts{Search: "/m/ytdl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("searching the shared output directory matched %d records, want 0", len(got))
	}
}

// TestLoadOffsetPagesStably walks the whole history one page at a time and
// asserts the pages tile it exactly — the contract "Carica altri" and the CLI
// both rely on.
func TestLoadOffsetPagesStably(t *testing.T) {
	dir := searchFixture(t)
	all, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("fixture has %d records, want 4", len(all))
	}
	var paged []Entry
	for off := 0; ; off += 2 {
		page, err := Load(dir, QueryOpts{Offset: off, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		paged = append(paged, page...)
	}
	if len(paged) != len(all) {
		t.Fatalf("paging returned %d records, want %d", len(paged), len(all))
	}
	for i := range all {
		if paged[i].URL != all[i].URL {
			t.Errorf("page order diverges at %d: %q vs %q", i, paged[i].URL, all[i].URL)
		}
	}
}

func TestLoadOffsetBeyondEndIsEmpty(t *testing.T) {
	dir := searchFixture(t)
	got, err := Load(dir, QueryOpts{Offset: 99})
	if err != nil {
		t.Fatalf("an offset past the end must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d records past the end, want 0", len(got))
	}
}

// TestLoadOffsetAppliesAfterFiltering: paging a filtered view must page the
// filtered set, not the raw file (otherwise page 2 of "solo falliti" shows
// successes).
func TestLoadOffsetAppliesAfterFiltering(t *testing.T) {
	dir := searchFixture(t)
	got, err := Load(dir, QueryOpts{OnlyFailed: true, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("only one failure exists, so offset 1 must be empty; got %+v", got)
	}
	got, err = Load(dir, QueryOpts{OnlyFailed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != "https://youtu.be/three" {
		t.Fatalf("OnlyFailed = %+v, want the single failed record", got)
	}
}

func TestFindResolvesFullIDAndPrefix(t *testing.T) {
	dir := searchFixture(t)
	all, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	target := all[1]

	got, err := Find(dir, target.ID())
	if err != nil {
		t.Fatalf("Find by full id: %v", err)
	}
	if got.URL != target.URL {
		t.Errorf("Find returned %q, want %q", got.URL, target.URL)
	}

	got, err = Find(dir, strings.ToUpper(target.ID()[:8]))
	if err != nil {
		t.Fatalf("Find by (upper-case) prefix: %v", err)
	}
	if got.URL != target.URL {
		t.Errorf("prefix Find returned %q, want %q", got.URL, target.URL)
	}
}

func TestFindUnknownAndEmptyID(t *testing.T) {
	dir := searchFixture(t)
	for _, id := range []string{"", "   ", "ffffffffffffffff", "zz"} {
		if _, err := Find(dir, id); err != ErrNotFound {
			t.Errorf("Find(%q) error = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := Find("", "abc"); err != ErrNotFound {
		t.Errorf("Find with no log dir = %v, want ErrNotFound", err)
	}
}

// TestFindAmbiguousPrefix: a prefix short enough to match several records must
// be refused, never silently resolved to the newest — acting on the wrong record
// is exactly what `ytdl open` must not do.
func TestFindAmbiguousPrefix(t *testing.T) {
	dir := searchFixture(t)
	all, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// A one-character prefix that at least two records share.
	counts := map[string]int{}
	for _, e := range all {
		counts[e.ID()[:1]]++
	}
	var shared string
	for prefix, n := range counts {
		if n > 1 {
			shared = prefix
			break
		}
	}
	if shared == "" {
		t.Skip("no two fixture ids share a first character")
	}
	if _, err := Find(dir, shared); err != ErrAmbiguous {
		t.Errorf("Find(%q) error = %v, want ErrAmbiguous", shared, err)
	}
}

// TestFindPrefersExactOverPrefix: a caller holding a full id — every API caller
// does — must never be told "ambiguous" because some other id starts with it.
func TestFindPrefersExactOverPrefix(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	if err := Append(dir, Job{URL: "https://youtu.be/one", Time: base, Success: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(dir, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	full := entries[0].ID()
	got, err := Find(dir, full)
	if err != nil {
		t.Fatalf("Find by exact id: %v", err)
	}
	if got.ID() != full {
		t.Errorf("Find returned id %q, want %q", got.ID(), full)
	}
}
