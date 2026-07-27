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
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u"})
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
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u"})
	if !strings.Contains(got, "~/Music/ytdl/Playlist") {
		t.Errorf("playlist row does not show its own subfolder:\n%s", got)
	}
}

// TestRenderHistoryShowsWhyItFailed: previously a failure was a bare ✗ and the
// user had to find the .log by hand (T6).
func TestRenderHistoryShowsWhyItFailed(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u"})
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
	got := RenderHistory(entries, HistoryView{RetentionDays: 30})
	if !strings.Contains(got, ".log") {
		t.Errorf("a reasonless failure should point at the per-job log:\n%s", got)
	}
}

// TestRenderHistoryCountsMultipleTracks: one title cannot represent five files,
// so a multi-file job says how many.
func TestRenderHistoryCountsMultipleTracks(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u"})
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
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Home: "/home/u"})
	for _, want := range []string{"[1] ", "[2] ", "[3] "} {
		if !strings.Contains(got, want) {
			t.Errorf("row marker %q missing:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "ytdl open <n>") || !strings.Contains(got, "ytdl again <n>") {
		t.Errorf("the footer does not tell the user what the numbers are for:\n%s", got)
	}
}

// TestRenderHistoryOffsetContinuesTheNumbering keeps a paged listing honest: row
// 1 of page 2 is not "[1]".
func TestRenderHistoryOffsetContinuesTheNumbering(t *testing.T) {
	got := RenderHistory(sampleHistory(), HistoryView{RetentionDays: 30, Offset: 20})
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
		{"nothing at all", HistoryView{RetentionDays: 30}, "ytdl <url>"},
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
	got := RenderHistory(entries, HistoryView{RetentionDays: 30, Width: width, Home: "/home/u"})
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
	got := RenderHistory(entries, HistoryView{RetentionDays: 30})
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
