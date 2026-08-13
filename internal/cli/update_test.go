package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/update"
)

const (
	testBuild      = "1785863997_9.0"
	testBuildNewer = "1900000000_9.1"
)

var checkedOn = time.Date(2026, 8, 12, 14, 2, 11, 0, time.UTC)

// view builds a complete, up-to-date view; each test bends the one field it is
// about.
func view() UpdateView {
	return UpdateView{
		Verdict: update.Verdict{
			CheckedAt:  checkedOn,
			LatestYtdl: "v2.1.0",
			Pin:        update.Pin{YtDlp: "2026.07.04", FFmpeg: testBuild},
			Installed: update.Installed{
				Ytdl: "v2.1.0", YtDlp: "2026.07.04", FFmpeg: testBuild,
			},
		},
		HaveVerdict: true,
		Enabled:     true,
		Deps: []update.Dependency{
			{Name: update.ComponentYtDlp, Version: "2026.07.04", Path: "/home/u/.local/bin/yt-dlp", Ours: true, Attested: true},
			{Name: update.ComponentFFmpeg, Version: testBuild, Path: "/home/u/.local/bin/ffmpeg", Ours: true, Attested: true},
		},
		Now: checkedOn.Add(time.Hour),
	}
}

// Everything the notice must NOT say. A failed probe is silent, and so is a user
// who turned the check off — this is a message nobody asked for, so the bar for
// printing it is that it carries news (ADR-0016 §8).
func TestUpdateNoticeIsSilentWhenItHasNothingToSay(t *testing.T) {
	cases := map[string]func(*UpdateView){
		"the sources agree":        func(v *UpdateView) {},
		"the check is turned off":  func(v *UpdateView) { v.Enabled = false; v.Verdict.LatestYtdl = "v2.2.0" },
		"no round ever completed":  func(v *UpdateView) { v.HaveVerdict = false; v.Verdict.LatestYtdl = "v2.2.0" },
		"a local build":            func(v *UpdateView) { v.Verdict.Installed.Ytdl = update.DevVersion },
		"the ytdl probe was quiet": func(v *UpdateView) { v.Verdict.LatestYtdl = "" },
		"the pin was quiet":        func(v *UpdateView) { v.Verdict.Pin = update.Pin{} },
	}
	for name, bend := range cases {
		t.Run(name, func(t *testing.T) {
			v := view()
			bend(&v)
			if got := RenderUpdateNotice(v); got != "" {
				t.Errorf("RenderUpdateNotice printed %q, want silence", got)
			}
		})
	}
}

func TestUpdateNoticeStatesWhatMoved(t *testing.T) {
	cases := []struct {
		name     string
		bend     func(*UpdateView)
		wantLine string
	}{
		{
			name:     "ytdl alone",
			bend:     func(v *UpdateView) { v.Verdict.LatestYtdl = "v2.2.0" },
			wantLine: "! Aggiornamento disponibile per ytdl v2.2.0 (hai v2.1.0).",
		},
		{
			name: "ytdl and its pinned yt-dlp",
			bend: func(v *UpdateView) {
				v.Verdict.LatestYtdl = "v2.2.0"
				v.Verdict.Pin.YtDlp = "2026.08.02"
			},
			wantLine: "! Aggiornamento disponibile per ytdl v2.2.0 (hai v2.1.0), con yt-dlp 2026.08.02.",
		},
		{
			// One axis, whatever moved: the user is offered an update to ytdl, and
			// the dependency comes with it rather than as a second decision.
			name:     "the pin alone",
			bend:     func(v *UpdateView) { v.Verdict.Pin.YtDlp = "2026.08.02" },
			wantLine: "! Aggiornamento disponibile per ytdl: richiede yt-dlp 2026.08.02 (hai 2026.07.04).",
		},
		{
			// The rollback lever. "richiede X (hai Y)" is true in both directions;
			// "è disponibile una versione più recente" would be a lie here.
			name:     "the pin rolls BACK to an older yt-dlp",
			bend:     func(v *UpdateView) { v.Verdict.Pin.YtDlp = "2026.06.01" },
			wantLine: "! Aggiornamento disponibile per ytdl: richiede yt-dlp 2026.06.01 (hai 2026.07.04).",
		},
		{
			// ffmpeg is compared by build id and shown by version: the build number
			// means nothing to the reader.
			name:     "ffmpeg, shown by version not by build id",
			bend:     func(v *UpdateView) { v.Verdict.Pin.FFmpeg = testBuildNewer },
			wantLine: "! Aggiornamento disponibile per ytdl: richiede ffmpeg 9.1 (hai 9.0).",
		},
		{
			name: "both dependencies",
			bend: func(v *UpdateView) {
				v.Verdict.Pin.YtDlp = "2026.08.02"
				v.Verdict.Pin.FFmpeg = testBuildNewer
			},
			wantLine: "! Aggiornamento disponibile per ytdl: richiede yt-dlp 2026.08.02 (hai 2026.07.04) e ffmpeg 9.1 (hai 9.0).",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := view()
			tc.bend(&v)
			got := RenderUpdateNotice(v)
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("want exactly two lines, got %d:\n%s", len(lines), got)
			}
			if lines[0] != tc.wantLine {
				t.Errorf("line 1:\n got=%q\nwant=%q", lines[0], tc.wantLine)
			}
			// The remedy, and where to stop being asked — the honesty cost of
			// update_check defaulting to on (ADR-0016 §6).
			if !strings.Contains(lines[1], "ytdl --update") {
				t.Errorf("line 2 does not say what to do: %q", lines[1])
			}
			if !strings.Contains(lines[1], "update_check = false") {
				t.Errorf("line 2 does not say where to turn the check off: %q", lines[1])
			}
		})
	}
}

// The wording must never claim newness, because the pin can legitimately move
// backwards. This is the phrasing rule stated as a test rather than as a comment.
func TestUpdateNoticeNeverClaimsNewness(t *testing.T) {
	banned := []string{"più recente", "piu recente", "nuova versione", "più nuovo"}
	for _, pin := range []string{"2026.08.02", "2026.06.01"} {
		v := view()
		v.Verdict.Pin.YtDlp = pin
		got := strings.ToLower(RenderUpdateNotice(v))
		for _, b := range banned {
			if strings.Contains(got, b) {
				t.Errorf("with pin %s the notice says %q:\n%s", pin, b, got)
			}
		}
	}
}

// Two lines that fit an 80-column terminal, which is what "at most two lines"
// buys: a notice that wraps is a notice that looks like an error.
func TestUpdateNoticeFitsAnOrdinaryTerminal(t *testing.T) {
	v := view()
	v.Verdict.LatestYtdl = "v2.2.0"
	v.Verdict.Pin.YtDlp = "2026.08.02"
	for _, line := range strings.Split(strings.TrimSuffix(RenderUpdateNotice(v), "\n"), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("line is %d columns:\n%s", n, line)
		}
	}
}

func TestForeignDependencyMessage(t *testing.T) {
	v := view()
	v.Deps[0] = update.Dependency{
		Name: update.ComponentYtDlp, Version: "2020.01.01",
		Path: "/opt/homebrew/bin/yt-dlp", Ours: false,
	}
	got := RenderForeignDependency(v)

	// It names the FILE, so the user can see which yt-dlp is meant rather than
	// hunting for it.
	if !strings.Contains(got, "/opt/homebrew/bin/yt-dlp") {
		t.Errorf("the message does not name the file:\n%s", got)
	}
	// And what this ytdl was actually verified against.
	if !strings.Contains(got, "2026.07.04") {
		t.Errorf("the message does not name the verified version:\n%s", got)
	}
	if !strings.Contains(got, "ytdl --update") {
		t.Errorf("the message does not say what to do:\n%s", got)
	}
	// It is a different problem from being out of date, so it must not borrow the
	// other message's words.
	if strings.Contains(got, "Aggiornamento disponibile") {
		t.Errorf("the foreign message reads as an update notice:\n%s", got)
	}
}

// Where a dependency came from is a LOCAL fact. Consent to phone home has nothing
// to do with it, so turning the check off must not hide it.
func TestForeignDependencyIsNotGatedOnConsent(t *testing.T) {
	v := view()
	v.Enabled = false
	v.HaveVerdict = false
	v.Deps[0] = update.Dependency{Name: update.ComponentYtDlp, Path: "/opt/homebrew/bin/yt-dlp"}

	got := RenderForeignDependency(v)
	if !strings.Contains(got, "/opt/homebrew/bin/yt-dlp") {
		t.Errorf("the foreign warning was suppressed by update_check = false:\n%q", got)
	}
	// With no pin answered there is no verified version to quote, so the message
	// says what it knows and stops.
	if strings.Contains(got, "La versione verificata") {
		t.Errorf("the message quotes a verified version it never learned:\n%s", got)
	}
}

func TestForeignDependencySilentWhenEverythingIsOurs(t *testing.T) {
	if got := RenderForeignDependency(view()); got != "" {
		t.Errorf("RenderForeignDependency printed %q", got)
	}
	// A tool that is not installed at all is missing, not foreign: CheckDeps owns
	// that message.
	v := view()
	v.Deps[0] = update.Dependency{Name: update.ComponentYtDlp}
	if got := RenderForeignDependency(v); got != "" {
		t.Errorf("a MISSING dependency was reported as foreign: %q", got)
	}
}

// The three verdict states never collapse into two — the defect Cycle 5's gate C
// existed for (ADR-0016 §8).
func TestUpdateStateKeepsTheThreeStatesDistinct(t *testing.T) {
	cases := []struct {
		name string
		bend func(*UpdateView)
		want string
	}{
		{"up to date, with its date", func(v *UpdateView) {}, "sei aggiornato · verificato il 12/08/2026"},
		{"an update is available", func(v *UpdateView) { v.Verdict.LatestYtdl = "v2.2.0" }, "disponibile un aggiornamento · ytdl --update"},
		{"the probe did not answer", func(v *UpdateView) { v.Verdict.Pin = update.Pin{} }, "non verificati (l'ultimo tentativo non ha ricevuto risposta)"},
		{"never checked", func(v *UpdateView) { v.HaveVerdict = false }, "non verificati (mai controllato)"},
		{"the check is off", func(v *UpdateView) { v.Enabled = false }, "controllo automatico disattivato"},
		{"a local build", func(v *UpdateView) { v.Verdict.Installed.Ytdl = update.DevVersion }, "non controllati (build locale)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := view()
			tc.bend(&v)
			got := RenderUpdateState(v)
			if !strings.Contains(got, tc.want) {
				t.Errorf("RenderUpdateState = %q, want it to contain %q", got, tc.want)
			}
			// "Up to date" is a claim about a MOMENT. Stating the day is what makes
			// it true rather than a guess about now.
			if strings.Contains(tc.want, "aggiornato") && !strings.Contains(got, "12/08/2026") {
				t.Errorf("\"sei aggiornato\" without the date it was checked: %q", got)
			}
		})
	}
}

// A failed probe is never rounded up to "up to date". Stated as its own test
// because it is the single rule this surface exists to keep.
func TestNotVerifiedIsNeverUpToDate(t *testing.T) {
	for _, bend := range []func(*UpdateView){
		func(v *UpdateView) { v.HaveVerdict = false },
		func(v *UpdateView) { v.Verdict.Pin = update.Pin{} },
		func(v *UpdateView) { v.Verdict.LatestYtdl = "" },
	} {
		v := view()
		bend(&v)
		if got := RenderUpdateState(v); strings.Contains(got, "sei aggiornato") {
			t.Errorf("a probe that did not answer rendered as up to date: %q", got)
		}
	}
}

func TestRenderVersion(t *testing.T) {
	got := RenderVersion(view())
	want := "ytdl v2.1.0\n" +
		"yt-dlp 2026.07.04   (verificata con questo ytdl)\n" +
		"ffmpeg 9.0   (verificata con questo ytdl)\n" +
		"Aggiornamenti: sei aggiornato · verificato il 12/08/2026\n"
	if got != want {
		t.Errorf("RenderVersion:\n got=%q\nwant=%q", got, want)
	}
}

// Every state a dependency line can be in, kept distinct.
func TestRenderVersionDependencyStates(t *testing.T) {
	cases := []struct {
		name string
		dep  update.Dependency
		want string
	}{
		{
			name: "absent from the machine",
			dep:  update.Dependency{Name: update.ComponentYtDlp},
			want: "yt-dlp non installato",
		},
		{
			// An install predating the marker: present, but nothing recorded which.
			// Neither "absent" nor a guess.
			name: "present, version unrecorded",
			dep:  update.Dependency{Name: update.ComponentFFmpeg, Path: "/home/u/.local/bin/ffmpeg", Ours: true, Attested: true},
			want: "ffmpeg (versione non registrata)",
		},
		{
			name: "ours, but not what this ytdl requires",
			dep:  update.Dependency{Name: update.ComponentYtDlp, Version: "2026.01.01", Path: "/home/u/.local/bin/yt-dlp", Ours: true, Attested: true},
			want: "yt-dlp 2026.01.01   (questo ytdl richiede la 2026.07.04)",
		},
		{
			// The foreign case states where it came from rather than claiming a
			// verdict about it: the pin simply is not in force.
			name: "somebody else's",
			dep:  update.Dependency{Name: update.ComponentYtDlp, Version: "2020.01.01", Path: "/opt/homebrew/bin/yt-dlp"},
			want: "yt-dlp 2020.01.01   (da /opt/homebrew/bin/yt-dlp — non installata da ytdl)",
		},
		{
			// Ours, but not the build the pin vouches for: that build was withdrawn
			// upstream and the installer chose staying installable over staying
			// verifiable (ADR-0016 §15). Saying "verificata" here would be the one
			// thing this surface exists not to do.
			name: "ours, but its attested build is gone",
			dep: update.Dependency{
				Name: update.ComponentFFmpeg, Version: "1900000000_9.1",
				Path: "/home/u/.local/bin/ffmpeg", Ours: true, Attested: false,
			},
			want: "ffmpeg 9.1   (non verificata: la versione attestata non è più disponibile)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := view()
			v.Deps = []update.Dependency{tc.dep}
			if got := RenderVersion(v); !strings.Contains(got, tc.want) {
				t.Errorf("RenderVersion:\n got=%s\nwant a line %q", got, tc.want)
			}
		})
	}
}

// The local facts print whether or not any probe ever succeeded (ADR-0016 §8).
func TestRenderVersionShowsLocalFactsWithoutAVerdict(t *testing.T) {
	v := view()
	v.HaveVerdict = false
	v.Verdict = update.Verdict{Installed: update.Installed{Ytdl: "v2.1.0"}}

	got := RenderVersion(v)
	for _, want := range []string{"ytdl v2.1.0", "yt-dlp 2026.07.04", "ffmpeg 9.0", "non verificati"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// An ffmpeg whose attested build was withdrawn is deliberately UNCOMPARED, so it
// can never become a permanent phantom update: the difference could not be
// resolved by updating, because the installer would fall back again.
func TestAnUnattestedDependencyIsNeverAPhantomUpdate(t *testing.T) {
	v := view()
	// What the machine reports after a fallback: no ffmpeg build in the comparison.
	v.Verdict.Installed.FFmpeg = ""
	v.Verdict.Installed.Unattested = []string{update.ComponentFFmpeg}
	v.Deps[1] = update.Dependency{
		Name: update.ComponentFFmpeg, Version: "1900000000_9.1",
		Path: "/home/u/.local/bin/ffmpeg", Ours: true, Attested: false,
	}

	if v.Verdict.Available() {
		t.Errorf("an unattested ffmpeg reads as an available update: %+v", v.Verdict.Changes())
	}
	if got := RenderUpdateNotice(v); got != "" {
		t.Errorf("the notice nags about something updating cannot fix:\n%s", got)
	}
	// But the screen still SAYS it is unverified — silence would be the dishonest
	// half of the trade.
	if got := RenderVersion(v); !strings.Contains(got, "non verificata") {
		t.Errorf("RenderVersion hides that the copy is unattested:\n%s", got)
	}
}
