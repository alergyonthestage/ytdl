package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/term"
)

// ---- history rendering ---------------------------------------------------

func sampleHistory() []logstore.Entry {
	at := time.Date(2026, 7, 27, 18, 4, 0, 0, time.Local)
	return []logstore.Entry{
		{
			Time: at, Title: "Artista - Traccia", Format: "mp3", Success: true, Count: 1,
			Path: "/home/u/Music/ytdl/Artista - Traccia.mp3", Dir: "/home/u/Music/ytdl",
		},
		{
			Time: at.Add(-6 * time.Minute), Title: "Altro - Pezzo", Format: "mp3", Success: false,
			URL: "https://youtu.be/x", Dir: "/home/u/Music/ytdl", Error: "HTTP Error 403: Forbidden",
		},
		{
			Time: at.Add(-20 * time.Hour), Title: "Terzo - Brano", Format: "mp3", Success: true, Count: 3,
			Path: "/home/u/Music/ytdl/Playlist/Terzo - Brano.mp3", Dir: "/home/u/Music/ytdl",
		},
	}
}

// TestRenderHistoryShowsWhereFilesLanded is the gap the cycle exists to close:
// a successful row must say where the audio went (T3).
func TestRenderHistoryShowsWhereFilesLanded(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u", IndexUsable: true})
	if !strings.Contains(got, "~/Music/ytdl") {
		t.Errorf("no home-relative location in the rows:\n%s", got)
	}
	if strings.Contains(got, "/home/u/Music") {
		t.Errorf("location printed absolute instead of ~-contracted:\n%s", got)
	}
}

// TestRenderHistoryShowsAPlaylistSubfolder: the location comes from the saved
// FILE, so a playlist shows the subfolder it actually landed in, not the
// configured parent.
func TestRenderHistoryShowsAPlaylistSubfolder(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u", IndexUsable: true})
	if !strings.Contains(got, "~/Music/ytdl/Playlist") {
		t.Errorf("playlist row does not show its own subfolder:\n%s", got)
	}
}

// TestRenderHistoryShowsWhyItFailed: previously a failure was a bare ✗ and the
// user had to find the .log by hand (T6).
func TestRenderHistoryShowsWhyItFailed(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u", IndexUsable: true})
	if !strings.Contains(got, "HTTP Error 403: Forbidden") {
		t.Errorf("the failure reason is not shown inline:\n%s", got)
	}
}

// TestRenderHistoryFailureWithoutAReason: a pre-Cycle-5 failure has no recorded
// reason. The cell must point somewhere rather than be blank.
func TestRenderHistoryFailureWithoutAReason(t *testing.T) {
	entries := []logstore.Entry{{
		Time: time.Now(), URL: "https://youtu.be/x", Format: "mp3", Success: false,
	}}
	got := RenderHistory(entries, HistoryView{RetentionDays: 30, IndexUsable: true})
	if !strings.Contains(got, ".log") {
		t.Errorf("a reasonless failure should point at the per-job log:\n%s", got)
	}
}

// TestRenderHistoryCountsMultipleTracks: one title cannot represent five files,
// so a multi-file job says how many.
func TestRenderHistoryCountsMultipleTracks(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u", IndexUsable: true})
	if !strings.Contains(got, "(3 tracce)") {
		t.Errorf("a 3-file job does not report its count:\n%s", got)
	}
	if strings.Contains(got, "(1 tracce)") {
		t.Errorf("a single-file job should not report a count:\n%s", got)
	}
}

// TestRenderHistoryIsNumberedForOpenAndAgain: the number in the listing is the
// argument to the next command, so it must be there and start at 1.
func TestRenderHistoryIsNumberedForOpenAndAgain(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u", IndexUsable: true})
	for _, want := range []string{"[1] ", "[2] ", "[3] "} {
		if !strings.Contains(got, want) {
			t.Errorf("row marker %q missing:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "ytdl open <n>") || !strings.Contains(got, "ytdl again <n>") {
		t.Errorf("the footer does not tell the user what the numbers are for:\n%s", got)
	}
}

// TestFilteredListingAdvertisesTheIDNotTheIndex is the review CRITICAL, as a
// regression test. `ytdl open`/`again` resolve <n> against the DEFAULT history
// query, so an index read off a filtered listing points at a different record.
// The footer used to promise "l'indice riflette questa lista" under every
// listing — actively instructing the user into the bug.
func TestFilteredListingAdvertisesTheIDNotTheIndex(t *testing.T) {
	for _, tc := range []struct {
		name string
		view HistoryView
	}{
		{"filtered by outcome", HistoryView{RetentionDays: 30, OnlyFailed: true}},
		{"narrowed by search", HistoryView{RetentionDays: 30, Search: "mina"}},
		{"a different limit", HistoryView{RetentionDays: 30}}, // IndexUsable false
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderHistory(sampleHistory(), tc.view)
			if strings.Contains(got, "ytdl open <n>") || strings.Contains(got, "ytdl again <n>") {
				t.Errorf("a narrowed listing still advertises the index:\n%s", got)
			}
			if !strings.Contains(got, "ytdl open <id>") {
				t.Errorf("a narrowed listing does not advertise the id:\n%s", got)
			}
			if strings.Contains(got, "l'indice riflette questa lista") {
				t.Errorf("the false promise is still printed:\n%s", got)
			}
		})
	}
}

// TestNarrowedListingPrintsTheIDs: telling the user to use the id is only useful
// if the id is on screen. Before this fix no CLI surface ever printed one.
func TestNarrowedListingPrintsTheIDs(t *testing.T) {
	entries := sampleHistory()
	got := RenderHistory(entries, HistoryView{RetentionDays: 30, OnlyFailed: true})
	for _, e := range entries {
		if !strings.Contains(got, e.ID()[:idCols]) {
			t.Errorf("record id %q is not printed:\n%s", e.ID()[:idCols], got)
		}
	}
}

// TestIDsOnDemand: scripts can ask for the stable handle without narrowing the
// list, and the everyday listing stays free of hex noise.
func TestIDsOnDemand(t *testing.T) {
	entries := sampleHistory()
	plain := RenderHistory(entries, HistoryView{RetentionDays: 30, IndexUsable: true})
	if strings.Contains(plain, entries[0].ID()[:idCols]) {
		t.Errorf("the default listing shows ids; it should stay clean:\n%s", plain)
	}
	withIDs := RenderHistory(entries, HistoryView{RetentionDays: 30, IndexUsable: true, ShowIDs: true})
	for _, e := range entries {
		if !strings.Contains(withIDs, e.ID()[:idCols]) {
			t.Errorf("--ids did not print %q:\n%s", e.ID()[:idCols], withIDs)
		}
	}
	if !strings.Contains(plain, "ytdl history --ids") {
		t.Errorf("the footer does not tell scripts how to get an id:\n%s", plain)
	}
}

// TestIndexColumnIsPadded: [9] is three characters and [10] is four, so an
// unpadded index shifts every column to its right from row 10 on — defeating the
// term.Pad work two lines below it.
func TestIndexColumnIsPadded(t *testing.T) {
	at := time.Date(2026, 7, 27, 18, 0, 0, 0, time.Local)
	var entries []logstore.Entry
	for i := 0; i < 12; i++ {
		entries = append(entries, logstore.Entry{
			Time: at.Add(-time.Duration(i) * time.Minute), Title: "Traccia", Format: "mp3",
			Success: true, Path: "/m/a.mp3", Dir: "/m",
		})
	}
	got := RenderHistory(entries, HistoryView{RetentionDays: 30, IndexUsable: true})

	var cols []int
	for _, line := range strings.Split(got, "\n") {
		if idx := strings.Index(line, "✓"); idx >= 0 {
			cols = append(cols, term.DisplayWidth(line[:idx]))
		}
	}
	if len(cols) != 12 {
		t.Fatalf("found %d data rows, want 12", len(cols))
	}
	for i, c := range cols {
		if c != cols[0] {
			t.Fatalf("row %d starts its mark at column %d, row 1 at %d — the table shifts:\n%s",
				i+1, c, cols[0], got)
		}
	}
}

// TestRenderHistoryOffsetContinuesTheNumbering keeps a paged listing honest: row
// 1 of page 2 is not "[1]".
func TestRenderHistoryOffsetContinuesTheNumbering(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Offset: 20, IndexUsable: true})
	if !strings.Contains(got, "[21] ") {
		t.Errorf("offset numbering wrong:\n%s", got)
	}
}

// TestRenderHistoryEmptyStatesTeach — an empty list must say why it is empty and
// what to do (ux-principles.md §5), not just report emptiness.
func TestRenderHistoryEmptyStatesTeach(t *testing.T) {
	tests := []struct {
		name string
		view HistoryView
		want string
	}{
		{"nothing at all", HistoryView{RetentionDays: 30, IndexUsable: true}, "ytdl <url>"},
		{"no failures", HistoryView{RetentionDays: 30, OnlyFailed: true}, "ytdl history"},
		{"no search hit", HistoryView{RetentionDays: 30, Search: "zzz"}, "--search"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderHistory(nil, tc.view)
			if !strings.Contains(got, tc.want) {
				t.Errorf("empty state does not mention %q:\n%s", tc.want, got)
			}
		})
	}
}

// TestRenderHistoryHeaderStatesTheFilters: a short list under an active filter
// must not read as "you have downloaded almost nothing".
func TestRenderHistoryHeaderStatesTheFilters(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, OnlyFailed: true, Search: "mina"})
	head := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(head, "solo non riusciti") || !strings.Contains(head, `"mina"`) {
		t.Errorf("header does not state the active filters: %q", head)
	}
}

// TestRenderHistoryClipsByDisplayWidth is the Cycle 4 rule applied to the new
// table: CJK and emoji count as two columns, so clipping by runes would still
// wrap and break the alignment.
func TestRenderHistoryClipsByDisplayWidth(t *testing.T) {
	entries := []logstore.Entry{{
		Time: time.Now(), Title: strings.Repeat("日", 60), Format: "mp3", Success: true,
		Path: "/home/u/Music/ytdl/a.mp3", Dir: "/home/u/Music/ytdl",
	}}
	const width = 60
	got := RenderHistory(entries, HistoryView{RetentionDays: 30, Width: width, Home: "/home/u", IndexUsable: true})
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if w := term.DisplayWidth(line); w > width {
			t.Errorf("line is %d columns wide, want at most %d: %q", w, width, line)
		}
	}
}

// TestRenderHistoryAlignsColumnsWithWideTitles: fmt's %-Ns pads by BYTES, which
// misaligns the moment a title is not ASCII. The location column must start at
// the same offset on every row.
func TestRenderHistoryAlignsColumnsWithWideTitles(t *testing.T) {
	at := time.Date(2026, 7, 27, 18, 0, 0, 0, time.Local)
	entries := []logstore.Entry{
		{Time: at, Title: "ASCII title", Format: "mp3", Success: true, Path: "/m/a.mp3", Dir: "/m"},
		{Time: at, Title: "日本語タイトル", Format: "mp3", Success: true, Path: "/m/b.mp3", Dir: "/m"},
	}
	got := RenderHistory(entries, HistoryView{RetentionDays: 30, IndexUsable: true})
	lines := strings.Split(got, "\n")
	var cols []int
	for _, line := range lines {
		if idx := strings.Index(line, ".mp3   "); idx >= 0 {
			cols = append(cols, term.DisplayWidth(line[:idx]))
		}
	}
	if len(cols) != 2 {
		t.Fatalf("expected two data rows, found %d in:\n%s", len(cols), got)
	}
	if cols[0] != cols[1] {
		t.Errorf("format column starts at %d and %d columns: the table is misaligned:\n%s", cols[0], cols[1], got)
	}
}

// ---- config rendering ----------------------------------------------------

func configView() ConfigView {
	s := config.Defaults()
	s.OutputDir = "/home/u/Music/ytdl"
	s.LogDir = "/home/u/.local/state/ytdl/logs"
	return ConfigView{
		Path:     "/home/u/.config/ytdl/config",
		Exists:   true,
		Settings: s,
		Home:     "/home/u",
	}
}

// TestRenderConfigNamesTheFileAndHowToChangeIt: "where do I change this?" is the
// question the command exists to answer.
func TestRenderConfigNamesTheFileAndHowToChangeIt(t *testing.T) {
	got := RenderConfig(configView())
	if !strings.Contains(got, "~/.config/ytdl/config") {
		t.Errorf("config path missing or not ~-contracted:\n%s", got)
	}
	if !strings.Contains(got, "ytdl gui") {
		t.Errorf("output does not say how to change the settings:\n%s", got)
	}
}

// TestRenderConfigSaysWhenTheFileDoesNotExist: pointing a user at a file that
// isn't there, without saying so, is the most confusing thing a config command
// can do.
func TestRenderConfigSaysWhenTheFileDoesNotExist(t *testing.T) {
	v := configView()
	v.Exists = false
	if got := RenderConfig(v); !strings.Contains(got, "non ancora creato") {
		t.Errorf("a missing config file is not flagged:\n%s", got)
	}
}

// TestRenderConfigShowsProvenance is the point of the command: "I changed the
// folder and nothing happened" is answered by the source column.
func TestRenderConfigShowsProvenance(t *testing.T) {
	v := configView()
	format := "flac"
	v.File.Format = &format
	v.Env.OutDir = "/tmp/override"
	v.Settings.Format = "flac"
	v.Settings.OutputDir = "/tmp/override"

	got := RenderConfig(v)
	for _, want := range []string{
		"formato               flac  (file di configurazione)",
		"cartella              /tmp/override  (ambiente: YTDL_OUT_DIR)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing provenance %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "(predefinito)") {
		t.Errorf("an untouched value should be marked as a default:\n%s", got)
	}
}

// TestRenderConfigCoversEverySettingsGroup: the command is the CLI's answer to
// the "rare but reachable" tier (ux-principles.md §2), so nothing may be hidden
// from it.
func TestRenderConfigCoversEverySettingsGroup(t *testing.T) {
	got := RenderConfig(configView())
	for _, group := range []string{"DOWNLOAD", "NOTIFICHE", "NOMI E METADATI", "LOG E MANUTENZIONE"} {
		if !strings.Contains(got, group) {
			t.Errorf("group %q missing:\n%s", group, got)
		}
	}
	for _, key := range []string{"cartella", "formato", "download paralleli", "apri al termine", "timeout per job", "conservazione"} {
		if !strings.Contains(got, key) {
			t.Errorf("setting %q missing:\n%s", key, got)
		}
	}
}

// TestRenderConfigSpellsOutTheZeroValues: the same digit means "unlimited",
// "keep forever" and "no timeout" in three different keys.
func TestRenderConfigSpellsOutTheZeroValues(t *testing.T) {
	v := configView()
	v.Settings.Concurrency = config.ConcurrencyUnlimited
	v.Settings.LogRetentionDays = 0
	v.Settings.JobTimeout = 0

	got := RenderConfig(v)
	for _, want := range []string{"senza limite", "per sempre", "nessuno"} {
		if !strings.Contains(got, want) {
			t.Errorf("zero value not spelled out (%q missing):\n%s", want, got)
		}
	}
}

func TestContractHome(t *testing.T) {
	tests := []struct {
		path, home, want string
	}{
		{"/home/u/Music/ytdl", "/home/u", "~/Music/ytdl"},
		{"/home/u", "/home/u", "~"},
		{"/home/user2/Music", "/home/u", "/home/user2/Music"}, // prefix, not a path boundary
		{"/opt/music", "/home/u", "/opt/music"},
		{"/home/u/Music", "", "/home/u/Music"}, // unresolvable $HOME
		{"", "/home/u", ""},
	}
	for _, tc := range tests {
		if got := contractHome(tc.path, tc.home); got != tc.want {
			t.Errorf("contractHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}

// ---- parsing -------------------------------------------------------------

func TestParseOpen(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantTarget string
		wantFolder bool
	}{
		{"no target lists", []string{"open"}, "", false},
		{"an index", []string{"open", "2"}, "2", false},
		{"an id prefix", []string{"open", "3f2a"}, "3f2a", false},
		{"the folder flag", []string{"open", "2", "--folder"}, "2", true},
		{"flag before target", []string{"open", "--folder", "2"}, "2", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse(tc.args)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.args, err)
			}
			if p.Action != ActionOpen {
				t.Fatalf("Action = %v, want ActionOpen", p.Action)
			}
			if p.Target != tc.wantTarget {
				t.Errorf("Target = %q, want %q", p.Target, tc.wantTarget)
			}
			if p.OpenFolder != tc.wantFolder {
				t.Errorf("OpenFolder = %v, want %v", p.OpenFolder, tc.wantFolder)
			}
		})
	}
}

func TestParseOpenRejects(t *testing.T) {
	for _, args := range [][]string{
		{"open", "1", "2"},
		{"open", "--all"},
		{"open", "-x"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) accepted; want an error", args)
		}
	}
}

func TestParseAgain(t *testing.T) {
	p, err := Parse([]string{"again", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Action != ActionAgain || p.Target != "3" {
		t.Errorf("got action %v target %q, want ActionAgain/3", p.Action, p.Target)
	}
	if _, err := Parse([]string{"again", "1", "2"}); err == nil {
		t.Error("two targets accepted; want an error")
	}
	if _, err := Parse([]string{"again", "--all"}); err == nil {
		t.Error("--all accepted; 'download everything again' is a way to flood the queue")
	}
}

func TestParseConfig(t *testing.T) {
	p, err := Parse([]string{"config"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Action != ActionConfig || p.ConfigPathOnly {
		t.Errorf("got action %v pathOnly %v, want ActionConfig/false", p.Action, p.ConfigPathOnly)
	}
	p, err = Parse([]string{"config", "--path"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ConfigPathOnly {
		t.Error("--path not parsed")
	}
	if _, err := Parse([]string{"config", "set", "format=flac"}); err == nil {
		t.Error("`config set` accepted; the command is read-only by design")
	}
}

func TestParseHistorySearch(t *testing.T) {
	p, err := Parse([]string{"history", "--search", "mina"})
	if err != nil {
		t.Fatal(err)
	}
	if p.HistorySearch != "mina" {
		t.Errorf("HistorySearch = %q, want %q", p.HistorySearch, "mina")
	}
	// An empty query is an error, not a silent no-op: the user typed --search
	// meaning to narrow, and listing everything would look like a broken filter.
	for _, args := range [][]string{{"history", "--search"}, {"history", "--search", ""}} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) accepted an empty query", args)
		}
	}
}

// TestNewCommandsAreSuggestedOnATypo extends the Cycle 4 guard to the commands
// this cycle adds — the whole point of that guard was that a mistyped command
// must not be downloaded as a URL.
func TestNewCommandsAreSuggestedOnATypo(t *testing.T) {
	tests := map[string]string{"opne": "open", "agian": "again", "confg": "config"}
	for typo, want := range tests {
		t.Run(typo, func(t *testing.T) {
			_, err := Parse([]string{typo})
			if err == nil {
				t.Fatalf("Parse(%q) accepted it as a URL", typo)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not suggest %q", err.Error(), want)
			}
		})
	}
}

// ---- help ----------------------------------------------------------------

// TestBareInvocationShowsShortUsage: someone typing `ytdl` is finding out what
// the tool does, not misusing it. Flags without a URL remain an error.
func TestBareInvocationShowsShortUsage(t *testing.T) {
	p, err := Parse(nil)
	if err != nil {
		t.Fatalf("bare `ytdl` errored: %v", err)
	}
	if p.Action != ActionHelp {
		t.Fatalf("Action = %v, want ActionHelp", p.Action)
	}
	if got := HelpText(p); got != ShortUsage {
		t.Errorf("bare `ytdl` does not print the short usage:\n%s", got)
	}
	if _, err := Parse([]string{"-f", "mp3"}); err == nil {
		t.Error("flags without a URL were accepted; that is still a mistake")
	}
}

// TestShortUsageFitsItsBudget: the whole point of the restructuring is that the
// default screen is readable at a glance. A regression here is the wall coming
// back one line at a time.
func TestShortUsageFitsItsBudget(t *testing.T) {
	lines := strings.Split(strings.TrimRight(ShortUsage, "\n"), "\n")
	if len(lines) > 22 {
		t.Errorf("the short usage is %d lines; the design budgets ~18", len(lines))
	}
	for _, line := range lines {
		if w := term.DisplayWidth(line); w > 80 {
			t.Errorf("line is %d columns wide, want ≤ 80: %q", w, line)
		}
	}
}

// TestShortUsageCoversEveryTaskGroup: the five things people actually type, the
// recovery path, and where to find more. Missing any of them means a user never
// discovers that part of the tool.
func TestShortUsageCoversEveryTaskGroup(t *testing.T) {
	for _, want := range []string{
		`ytdl "<url>"`,  // T1 download
		"ytdl -b",       // T1 in background
		"ytdl queue",    // T2 is it working
		"ytdl history",  // T3/T5 where is my file
		"ytdl gui",      // T7 settings
		"ytdl --update", // T8 it broke
		"VIRGOLETTE",    // the most-reported breakage
		"ytdl help",     // where the rest lives
	} {
		if !strings.Contains(ShortUsage, want) {
			t.Errorf("the short usage never mentions %q", want)
		}
	}
}

// TestEveryTopicIsReachableAndNonEmpty: a listed topic that prints nothing is
// worse than no topic at all.
func TestEveryTopicIsReachableAndNonEmpty(t *testing.T) {
	for _, tp := range topics {
		t.Run(tp.Name, func(t *testing.T) {
			if strings.TrimSpace(tp.Body) == "" {
				t.Fatal("empty body")
			}
			got, ok := LookupTopic(tp.Name)
			if !ok {
				t.Fatalf("topic %q is in the registry but does not resolve", tp.Name)
			}
			if got.Body != tp.Body {
				t.Error("lookup returned a different page")
			}
			p, err := Parse([]string{"help", tp.Name})
			if err != nil {
				t.Fatalf("`ytdl help %s` errored: %v", tp.Name, err)
			}
			if HelpText(p) != tp.Body {
				t.Error("`ytdl help <name>` printed a different page")
			}
		})
	}
}

// TestTopicIndexListsTheTopics: the index is the map. A topic missing from it is
// undiscoverable even though it works.
func TestTopicIndexListsTheTopics(t *testing.T) {
	idx := TopicIndex()
	for _, tp := range topics {
		if !tp.Listed {
			continue
		}
		if !strings.Contains(idx, tp.Name) {
			t.Errorf("topic %q missing from the index:\n%s", tp.Name, idx)
		}
		if !strings.Contains(idx, tp.Title) {
			t.Errorf("topic %q has no description in the index", tp.Name)
		}
	}
	p, err := Parse([]string{"help"})
	if err != nil {
		t.Fatal(err)
	}
	if HelpText(p) != idx {
		t.Error("`ytdl help` does not print the index")
	}
}

// TestHelpTuttoIsTheFullReference: nothing was deleted in the restructuring, it
// moved one step away — and this is the text docs/cli-reference.md tracks.
func TestHelpTuttoIsTheFullReference(t *testing.T) {
	p, err := Parse([]string{"help", "tutto"})
	if err != nil {
		t.Fatal(err)
	}
	if HelpText(p) != Usage {
		t.Error("`ytdl help tutto` is not the full reference text")
	}
	for _, cmd := range []string{"ytdl open", "ytdl again", "ytdl config", "--search"} {
		if !strings.Contains(Usage, cmd) {
			t.Errorf("the full reference never mentions %q", cmd)
		}
	}
}

// TestUnknownTopicSuggestsTheNearest — the Cycle 4 hint applied to help pages.
func TestUnknownTopicSuggestsTheNearest(t *testing.T) {
	tests := map[string]string{"stroico": "storico", "opzoini": "opzioni", "impostazoini": "impostazioni"}
	for typo, want := range tests {
		t.Run(typo, func(t *testing.T) {
			_, err := Parse([]string{"help", typo})
			if err == nil {
				t.Fatalf("`ytdl help %s` was accepted", typo)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not suggest %q", err.Error(), want)
			}
		})
	}
	// A word near nothing gets the index pointer instead of a wrong suggestion.
	_, err := Parse([]string{"help", "zzzzzzzzzz"})
	if err == nil {
		t.Fatal("an unrelated topic was accepted")
	}
	if !strings.Contains(err.Error(), "ytdl help") {
		t.Errorf("error %q does not point at the topic index", err.Error())
	}
}

// TestEveryCommandHasItsOwnHelp: `ytdl <comando> --help` must work for every
// command, in either argument position, and print that command's page.
func TestEveryCommandHasItsOwnHelp(t *testing.T) {
	for _, cmd := range []string{"queue", "status", "history", "gui", "cancel", "retry", "open", "again", "config"} {
		t.Run(cmd, func(t *testing.T) {
			page, ok := LookupTopic(cmd)
			if !ok {
				t.Fatalf("no help page for %q", cmd)
			}
			for _, flag := range []string{"--help", "-h"} {
				p, err := Parse([]string{cmd, flag})
				if err != nil {
					t.Fatalf("`ytdl %s %s` errored: %v", cmd, flag, err)
				}
				if p.Action != ActionHelp {
					t.Fatalf("`ytdl %s %s` → action %v, want ActionHelp", cmd, flag, p.Action)
				}
				if HelpText(p) != page.Body {
					t.Errorf("`ytdl %s %s` printed the wrong page", cmd, flag)
				}
			}
			// The flag wins wherever it appears, so a user does not have to
			// think about argument order.
			p, err := Parse([]string{cmd, "--help", "2"})
			if err != nil || p.Action != ActionHelp {
				t.Errorf("`ytdl %s --help 2` → %v (%v), want the help page", cmd, p, err)
			}
		})
	}
}

// TestPerCommandPagesAreNotInTheIndex: listing nine more names would rebuild the
// wall this restructuring removed.
func TestPerCommandPagesAreNotInTheIndex(t *testing.T) {
	idx := TopicIndex()
	if !strings.Contains(idx, "ytdl <comando> --help") {
		t.Error("the index does not say how to reach a single command's help")
	}
	lines := strings.Split(strings.TrimRight(idx, "\n"), "\n")
	if len(lines) > 14 {
		t.Errorf("the topic index is %d lines; it must stay a short map", len(lines))
	}
}

// TestBareVideoIDStillPasses is the Cycle 4 CRITICAL, re-asserted now that three
// more commands are in the similarity table: a bare YouTube id is valid yt-dlp
// input and must reach it.
func TestBareVideoIDStillPasses(t *testing.T) {
	for _, id := range []string{"dQw4w9WgXcQ", "PLabcdefghijklmnop", "aBcDeFgHiJk"} {
		p, err := Parse([]string{id})
		if err != nil {
			t.Errorf("Parse(%q) rejected a bare id: %v", id, err)
			continue
		}
		if p.Action != ActionRun || p.URL != id {
			t.Errorf("Parse(%q) → action %v url %q, want a download of the id", id, p.Action, p.URL)
		}
	}
}
