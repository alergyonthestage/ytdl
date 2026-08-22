package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/update"
)

// updateDateFormat stamps every state that mentions a check with the day it
// happened. That date is what keeps "sei aggiornato" honest: the claim is about a
// moment, not about now, and saying which moment is the whole difference between
// reporting a check and pretending to one (ADR-0016 §8).
const updateDateFormat = "02/01/2006"

// UpdateView is everything the update surfaces need, assembled at the edge so the
// renderers stay pure — no clock, no config read, no exec inside the formatting
// code. Same shape as HistoryView and ConfigView.
type UpdateView struct {
	// Verdict is the cached round, zero when there is none. HaveVerdict tells the
	// two apart, because "never checked" is emphatically not "up to date".
	Verdict     update.Verdict
	HaveVerdict bool

	// Enabled is the resolved update_check. It gates the automatic probe and the
	// notice that comes out of it — never the local facts below, which cost no
	// network and are nobody's consent to give (ADR-0016 §6).
	Enabled bool

	// Deps is what this machine actually has. The screens a user deliberately
	// opened fill it with versions; the surfaces that interrupt a download fill in
	// only where each tool came from, because asking yt-dlp its version costs the
	// better part of a second.
	Deps []update.Dependency

	Now time.Time
}

// RenderUpdateNotice renders the cached verdict as at most two lines, or "" when
// there is nothing worth saying.
//
// It is deliberately quiet. Nothing prints when the sources agree, when the user
// turned the check off, when the build is a local one, or when no round has ever
// completed — a failed probe is silent, never an error the user did not ask for
// (ADR-0016 §8).
func RenderUpdateNotice(v UpdateView) string {
	if !v.Enabled || !v.HaveVerdict || v.Verdict.IsDev() {
		return ""
	}
	changes := v.Verdict.Changes()
	if len(changes) == 0 {
		return ""
	}
	return "! " + updateSentence(changes) + "\n" + msgUpdateHow
}

// msgUpdateHow is the second line: what to do, and where to stop being asked.
// Naming the key is the honesty cost of update_check defaulting to on — the
// person who never opens a settings screen learns of the check from the one
// message the check produces (ADR-0016 §6).
const msgUpdateHow = "  Aggiorna con:  ytdl --update   (per non controllare più: update_check = false)\n"

// updateSentence states what an update would move, in wording that stays true in
// BOTH directions.
//
// "richiede X (hai Y)" is the load-bearing phrasing: after a rollback the pin
// names an OLDER yt-dlp than the one installed, and "è disponibile una versione
// più recente" would then be a lie. The comparison is neutral, so the sentence is
// too (ADR-0016 §1, §2).
func updateSentence(changes []update.Change) string {
	var ytdl *update.Change
	var deps []update.Change
	for i, c := range changes {
		if c.Component == update.ComponentYtdl {
			ytdl = &changes[i]
			continue
		}
		deps = append(deps, c)
	}

	// One axis, whatever moved: the user is being offered an update to ytdl, and
	// the dependencies come with it rather than as a second decision (ADR-0016 §8).
	if ytdl != nil {
		s := fmt.Sprintf("Aggiornamento disponibile per ytdl %s (hai %s)", ytdl.To, ytdl.From)
		if len(deps) > 0 {
			s += ", con " + strings.Join(depNames(deps), " e ")
		}
		return s + "."
	}
	return "Aggiornamento disponibile per ytdl: richiede " + strings.Join(depRequirements(deps), " e ") + "."
}

// depNames names each moved dependency at its target version — what comes WITH
// the ytdl update, so the "hai" half would only be noise beside it.
func depNames(deps []update.Change) []string {
	out := make([]string, 0, len(deps))
	for _, c := range deps {
		out = append(out, c.Component+" "+displayVersion(c.Component, c.To))
	}
	return out
}

// depRequirements spells out both sides, for the case where ytdl itself is not
// moving: the only thing that changed is what this ytdl requires, so the sentence
// has to say what it requires and what is there.
func depRequirements(deps []update.Change) []string {
	out := make([]string, 0, len(deps))
	for _, c := range deps {
		out = append(out, fmt.Sprintf("%s %s (hai %s)",
			c.Component, displayVersion(c.Component, c.To), displayVersion(c.Component, c.From)))
	}
	return out
}

// displayVersion shows a version the way a person recognises it. Only ffmpeg
// differs: it is COMPARED by build id, because that is the only exact answer, and
// SHOWN by version, because the build number means nothing to the reader.
func displayVersion(component, value string) string {
	if component == update.ComponentFFmpeg {
		return update.FFmpegVersion(value)
	}
	return value
}

// RenderForeignDependency warns that ytdl is driving a tool it did not install,
// or "" when everything came from us.
//
// It is a DIFFERENT message from the update notice because it is a different
// problem with a different remedy: nothing is out of date, but the pin is not in
// force, so the version ytdl was tested against is not the version running. It is
// never gated on update_check — where a dependency came from is a local fact, and
// consent to phone home has nothing to do with it.
func RenderForeignDependency(v UpdateView) string {
	var b strings.Builder
	for _, d := range v.Deps {
		if d.Missing() || d.Ours {
			continue
		}
		fmt.Fprintf(&b, "! ytdl sta usando %s da %s, che non ha installato lui.\n", d.Name, d.Path)
		if want := pinnedVersion(v.pin(), d.Name); want != "" {
			fmt.Fprintf(&b, "  La versione verificata con questo ytdl è %s.  Ripristinala con:  ytdl --update\n",
				displayVersion(d.Name, want))
		} else {
			b.WriteString("  Ripristina la copia di ytdl con:  ytdl --update\n")
		}
	}
	return b.String()
}

// pin is what the cached round said this machine should be running, or nothing at
// all when no round ever completed. A verdict that was never obtained has no pin
// to quote, and quoting one anyway would state a requirement ytdl never learned.
func (v UpdateView) pin() update.Pin {
	if !v.HaveVerdict {
		return update.Pin{}
	}
	return v.Verdict.Pin
}

// pinnedVersion is what the pin says about one dependency, or "" when the pin was
// never answered — in which case the message says what it knows and stops.
func pinnedVersion(p update.Pin, name string) string {
	switch name {
	case update.ComponentYtDlp:
		return p.YtDlp
	case update.ComponentFFmpeg:
		return p.FFmpeg
	}
	return ""
}

// RenderVersion renders `ytdl --version`: the whole truth about this install,
// because that screen is where a user goes to ask for it (design §6.1).
//
// Every line here is a LOCAL fact except the last, and the local ones print
// whether or not any probe ever succeeded (ADR-0016 §8).
func RenderVersion(v UpdateView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ytdl %s\n", v.Verdict.Installed.Ytdl)
	pin := v.pin()
	for _, d := range v.Deps {
		b.WriteString(dependencyLine(d, pin))
	}
	fmt.Fprintf(&b, "Aggiornamenti: %s\n", updateState(v))
	b.WriteString(RenderUnreadable(v))
	return b.String()
}

// dependencyLine states one dependency and, in parentheses, how it stands against
// the pin. The states stay distinct: absent, present-but-unrecorded, ours and
// matching, ours but not what this ytdl requires, ours but UNATTESTED (its build
// was withdrawn upstream) — plus the foreign case, which says where it came from
// rather than claiming a verdict about it.
func dependencyLine(d update.Dependency, p update.Pin) string {
	if d.Missing() {
		return fmt.Sprintf("%s non installato\n", d.Name)
	}
	if d.VersionUnreadable() {
		// We asked and got nothing usable. Saying "(versione non registrata)" here
		// described a tool nobody had written a version down for — which was true
		// of ffmpeg without a marker and false of this, a tool that is installed,
		// works, and answers when asked with enough patience (`V20`).
		return fmt.Sprintf("%s (non sono riuscito a leggerne la versione)\n", d.Name)
	}
	shown := displayVersion(d.Name, d.Version)
	if shown == "" {
		shown = "(versione non registrata)"
	}
	want := pinnedVersion(p, d.Name)
	switch {
	case !d.Ours:
		return fmt.Sprintf("%s %s   (da %s — non installata da ytdl)\n", d.Name, shown, d.Path)
	case !d.Attested:
		// Installed by us, but not the build the pin vouches for: that build was
		// withdrawn upstream and the installer chose staying installable over
		// staying verifiable. Saying "verificata" here would be the one thing this
		// whole surface exists not to do.
		return fmt.Sprintf("%s %s   (non verificata: la versione attestata non è più disponibile)\n", d.Name, shown)
	case want == "" || d.Version == "":
		return fmt.Sprintf("%s %s\n", d.Name, shown)
	case want == d.Version:
		return fmt.Sprintf("%s %s   (verificata con questo ytdl)\n", d.Name, shown)
	default:
		return fmt.Sprintf("%s %s   (questo ytdl richiede la %s)\n", d.Name, shown, displayVersion(d.Name, want))
	}
}

// RenderUnreadable states, as its OWN sentence, that a dependency could not be
// asked its version and was therefore not compared — or "" when everything
// answered.
//
// It sits beside the verdict rather than inside it, which is the ruling in
// ADR-0016 §16.5. Folding it into the state line would force a choice between
// two wrong answers: rounding up to "sei aggiornato" (a certainty ytdl does not
// have — the defect `V20` was) or collapsing to "non verificato" (which would
// withhold an update ytdl HAS seen, and the news is never withheld).
//
// It is not gated on update_check: whether a local binary answered is a local
// fact, and consent to phone home has nothing to do with it.
func RenderUnreadable(v UpdateView) string {
	var names []string
	for _, d := range v.Deps {
		if d.VersionUnreadable() {
			names = append(names, d.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	// Phrased around the verb rather than the component: "yt-dlp non è stata
	// confrontata" forces a gender onto a tool name, and the plural form would have
	// to agree with it. This reads the same for one component and for several.
	return fmt.Sprintf("  Non ho potuto confrontare %s: non sono riuscito a leggerne la versione.\n",
		strings.Join(names, " e "))
}

// RenderUpdateState is the one-line update state for `ytdl status`, indented to
// sit with that screen's other footer facts.
//
// status shows the STATE, not the two-line notice: the state already reads
// "disponibile un aggiornamento · ytdl --update" when there is one, so printing
// the notice beside it would say the same thing twice — and it says something on
// every run, which is what "always in status" asks for.
func RenderUpdateState(v UpdateView) string {
	// The unreadable clause rides with the state on this screen too: `status` is
	// where a user goes to ask whether anything is wrong, and a verdict that was
	// reached without comparing everything must say so wherever it appears.
	return "  aggiornamenti: " + updateState(v) + "\n" + RenderUnreadable(v)
}

// updateState is the last line of `ytdl --version` and of `ytdl status`: which of
// the three verdict states is in force, and — whenever a check actually happened
// — the day it happened on.
//
// The three never collapse into two. "Non verificato" is not "sei aggiornato",
// and rounding one up to the other is the defect Cycle 5's gate C existed for
// (ADR-0016 §8, ux-principles.md §5).
func updateState(v UpdateView) string {
	switch {
	case v.Verdict.IsDev():
		return "non controllati (build locale)"
	case !v.Enabled:
		return "controllo automatico disattivato"
	case !v.HaveVerdict:
		return "non verificati (mai controllato)"
	case !v.Verdict.Known():
		return "non verificati (l'ultimo tentativo non ha ricevuto risposta)"
	case v.Verdict.Available():
		return "disponibile un aggiornamento · ytdl --update"
	default:
		return "sei aggiornato · verificato il " + v.Verdict.CheckedAt.Local().Format(updateDateFormat)
	}
}
