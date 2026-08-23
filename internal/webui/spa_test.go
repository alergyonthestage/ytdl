package webui

import (
	"regexp"
	"strings"
	"testing"
)

// These tests read the shipped assets rather than drive a browser: the container
// has none, and the maintainer verifies the visuals on macOS before gate C. What
// they DO catch is the class of mistake that is invisible until someone opens the
// page — an id typo, a route that leads nowhere, a reintroduced page load — and
// which no Go test would otherwise notice.

func assetText(t *testing.T, name string) string {
	t.Helper()
	b, err := assetFS.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

var idRefRe = regexp.MustCompile(`\$\("([A-Za-z0-9_-]+)"\)`)
var idAttrRe = regexp.MustCompile(`id="([A-Za-z0-9_-]+)"`)

// TestEveryScriptedIDExistsInTheMarkup is the highest-value check here: a
// mistyped id makes $() return null and the whole script dies at the first use,
// leaving a blank page that every Go test still passes.
func TestEveryScriptedIDExistsInTheMarkup(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")

	present := map[string]bool{}
	for _, m := range idAttrRe.FindAllStringSubmatch(html, -1) {
		present[m[1]] = true
	}
	seen := map[string]bool{}
	for _, m := range idRefRe.FindAllStringSubmatch(js, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		// View containers are addressed as "view-" + name, built at runtime.
		if strings.HasPrefix(id, "view-") {
			continue
		}
		if !present[id] {
			t.Errorf("app.js uses $(%q) but index.html has no such id", id)
		}
	}
	if len(seen) == 0 {
		t.Error("no id references found in app.js; the test is not checking anything")
	}
}

// TestThreeViewsExist pins the information architecture: three task-scoped views
// (design §4.1), each with a container and a nav entry.
func TestThreeViewsExist(t *testing.T) {
	html := assetText(t, "assets/index.html")
	for _, id := range []string{"view-download", "view-history", "view-settings"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing view container %q", id)
		}
	}
	for _, href := range []string{`href="#/"`, `href="#/cronologia"`, `href="#/impostazioni"`} {
		if !strings.Contains(html, href) {
			t.Errorf("missing nav link %s", href)
		}
	}
}

// TestRoutesAndNavAgree: a nav link whose hash the router does not know silently
// falls back to the Download view, which looks like a dead link.
func TestRoutesAndNavAgree(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")

	navRe := regexp.MustCompile(`<a href="(#[^"]*)" data-view="([a-z]+)"`)
	matches := navRe.FindAllStringSubmatch(html, -1)
	if len(matches) != 3 {
		t.Fatalf("found %d nav links, want 3", len(matches))
	}
	for _, m := range matches {
		hash, view := m[1], m[2]
		if !strings.Contains(js, `"`+hash+`": "`+view+`"`) {
			t.Errorf("nav link %s → %s has no matching route in app.js", hash, view)
		}
	}
}

// TestSPANeverReloadsThePage is a correctness requirement, not a style one: a
// real navigation drops the SSE connection, and an open SSE connection is the
// "GUI connected" clause of the daemon's exit test (ADR-0008). A daemon could
// idle-exit in the gap between two page loads, mid-download.
func TestSPANeverReloadsThePage(t *testing.T) {
	js := assetText(t, "assets/app.js")
	for _, bad := range []string{
		"location.href =", "location.assign(", "location.replace(",
		"window.open(", "document.write(",
	} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js navigates with %q; the document must never reload", bad)
		}
	}
	assertOneReloadInTheHandover(t, js)
	html := assetText(t, "assets/index.html")
	// A form without a submit handler would navigate on Enter. Both forms have
	// one; an added action= attribute would defeat it.
	if strings.Contains(html, "<form") && strings.Contains(html, " action=") {
		t.Error("a form has an action attribute; submitting it would reload the page")
	}
	if !strings.Contains(js, "hashchange") {
		t.Error("no hashchange listener: navigation would not switch views")
	}
}

// TestSPABuildsDOMRatherThanHTML: every string the page renders — titles, URLs,
// failure reasons, server error messages — is user- or network-controlled.
// textContent makes escaping unnecessary rather than merely correct, and the
// tightened CSP is the backstop, not the defence.
func TestSPABuildsDOMRatherThanHTML(t *testing.T) {
	js := assetText(t, "assets/app.js")
	for _, bad := range []string{".innerHTML", ".outerHTML", "insertAdjacentHTML"} {
		// The word may legitimately appear in a comment explaining why it is not
		// used; only an actual assignment or call is a problem.
		for _, line := range strings.Split(js, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, bad) {
				t.Errorf("app.js uses %s: %q", bad, trimmed)
			}
		}
	}
}

// TestQueueRowsOfferCancel: making the queue actionable from the GUI is design
// goal 3 — a GUI-only user must be able to stop a stuck download without opening
// a terminal.
func TestQueueRowsOfferCancel(t *testing.T) {
	js := assetText(t, "assets/app.js")
	if !strings.Contains(js, "/api/queue/cancel") {
		t.Error("the SPA never calls the cancel endpoint")
	}
	if !strings.Contains(js, `button("Annulla"`) {
		t.Error("no Annulla control on queue rows")
	}
}

// TestHistoryRowsRankTheirActions: never a row of equal buttons
// (ux-principles.md §4). "Apri" only when the server says the file is there;
// otherwise "Riscarica".
func TestHistoryRowsRankTheirActions(t *testing.T) {
	js := assetText(t, "assets/app.js")
	if !strings.Contains(js, "canOpenFile") {
		t.Error("the row's primary action does not consult the server's file-present flag")
	}
	for _, label := range []string{`button("Apri"`, `button("Riscarica"`} {
		if !strings.Contains(js, label) {
			t.Errorf("missing action %s", label)
		}
	}
}

// TestFailedRowsShowTheNextStep: "why it failed" was already inline; what to do
// about it was nowhere (G8, ux-principles.md §5). The hint comes from the server,
// derived from the same stored reason, so both channels say the same thing.
func TestFailedRowsShowTheNextStep(t *testing.T) {
	js := assetText(t, "assets/app.js")
	if !strings.Contains(js, "h.hint") {
		t.Error("the history row never renders the failure hint")
	}
	if !strings.Contains(js, `!h.success && h.hint`) {
		t.Error("the hint is not scoped to failures")
	}
}

// TestOverflowMenuHoldsTheSecondaryActions: the maintainer's instruction was a
// visual hierarchy, not a row of equal buttons — one primary control, the rest
// behind ···.
func TestOverflowMenuHoldsTheSecondaryActions(t *testing.T) {
	js := assetText(t, "assets/app.js")
	for _, action := range []string{"Mostra nella cartella", "Apri la cartella", "Vedi errore", "Copia link"} {
		if !strings.Contains(js, action) {
			t.Errorf("overflow action %q missing", action)
		}
	}
	if !strings.Contains(js, "overflowMenu") {
		t.Error("no overflow menu; the row would show a stack of equal buttons")
	}
}

// TestRevealLabelNamesTheFolderNotTheFileManager pins ADR-0014 §3: the reveal
// action names the folder on every platform. "Mostra nel Finder" was false on
// Linux, and it teaches a non-developer a product name they do not need in
// order to find their music.
func TestRevealLabelNamesTheFolderNotTheFileManager(t *testing.T) {
	// Comments are stripped first: ADR-0014 §3 keeps "Finder" correct "in code
	// comments about macOS behaviour", so a test that banned the word outright
	// would be stricter than the rule it pins.
	js := stripLineComments(assetText(t, "assets/app.js"))
	if strings.Contains(js, "Finder") {
		t.Error("a user-facing label still names the Finder; ADR-0014 §3 names the folder instead")
	}
}

// stripLineComments removes // comments, keeping the code. It is deliberately
// simple — app.js has no string literal containing "//" other than in URLs,
// which carry no user-facing labels.
func stripLineComments(js string) string {
	var b strings.Builder
	for _, line := range strings.Split(js, "\n") {
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], "\"") {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestOverflowMenuIsDismissibleAndReachable: a menu that only closes by
// selecting something is a trap, and one reachable only by mouse excludes
// keyboard users (ux-principles.md §4).
func TestOverflowMenuIsDismissibleAndReachable(t *testing.T) {
	js := assetText(t, "assets/app.js")
	if !strings.Contains(js, `document.addEventListener("click", () => closeMenus(false))`) {
		t.Error("the overflow menu does not close on an outside click")
	}
	if !strings.Contains(js, `ev.key === "Escape"`) {
		t.Error("the overflow menu does not close on Escape")
	}
	for _, attr := range []string{"aria-haspopup", "aria-expanded", "aria-label"} {
		if !strings.Contains(js, attr) {
			t.Errorf("the overflow control has no %s", attr)
		}
	}
	// Items are real buttons, so they are tabbable and operable with Enter/Space.
	if !strings.Contains(js, "menu.appendChild(b)") {
		t.Error("overflow items are not buttons")
	}
}

// TestUnavailableActionsAreDisabledWithAReason: never rendered live only to
// fail, never silently missing (ux-principles.md §4).
func TestUnavailableActionsAreDisabledWithAReason(t *testing.T) {
	js := assetText(t, "assets/app.js")
	if !strings.Contains(js, "b.disabled = true") || !strings.Contains(js, "disabledReason") {
		t.Error("an unavailable overflow action is not disabled with a reason")
	}
	for _, reason := range []string{"il file non è più al suo posto", "nessun log disponibile"} {
		if !strings.Contains(js, reason) {
			t.Errorf("missing explanation %q for an unavailable action", reason)
		}
	}
}

// TestHistoryFiltersAndSearchGoToTheServer: filtering client-side would search
// only the rows already loaded, so a search would miss everything past the first
// page (design §8.3).
func TestHistoryFiltersAndSearchGoToTheServer(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")

	for _, filter := range []string{`data-filter="all"`, `data-filter="ok"`, `data-filter="failed"`} {
		if !strings.Contains(html, filter) {
			t.Errorf("missing filter control %s", filter)
		}
	}
	if !strings.Contains(js, `p.set("failed", "1")`) || !strings.Contains(js, `p.set("ok", "1")`) {
		t.Error("the filters do not reach the server as query parameters")
	}
	if !strings.Contains(js, `p.set("q"`) {
		t.Error("the search box does not reach the server as a query parameter")
	}
	if !strings.Contains(js, `p.set("offset"`) {
		t.Error("paging does not send an offset")
	}
}

// TestHistoryEmptyStatesTeach: three different reasons for an empty list, three
// different things to say (ux-principles.md §5).
func TestHistoryEmptyStatesTeach(t *testing.T) {
	js := assetText(t, "assets/app.js")
	for _, text := range []string{
		"Nessun download corrisponde a questa ricerca",
		"Nessun download non riuscito",
		"Nessun download registrato",
	} {
		if !strings.Contains(js, text) {
			t.Errorf("missing empty state %q", text)
		}
	}
	if !strings.Contains(js, "Incolla un link") {
		t.Error("the empty queue does not teach the next step")
	}
}

// TestFailureLogGoesIntoAPreAsText: the .log is arbitrary program output served
// as text/plain; it must be DISPLAYED, never interpreted.
func TestFailureLogGoesIntoAPreAsText(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")
	if !strings.Contains(html, `<pre id="logBody">`) {
		t.Error("the log panel is not a <pre>")
	}
	if !strings.Contains(js, `$("logBody").textContent = text`) {
		t.Error("the log body is not set as text")
	}
	if !strings.Contains(js, "/api/history/log?id=") {
		t.Error("the panel never fetches the log")
	}
}

// TestSettingsAreGrouped: the flat list of 17 fields was the reported problem
// (design §8.4). Order matters — Download first, Avanzate last and collapsed.
func TestSettingsAreGrouped(t *testing.T) {
	html := assetText(t, "assets/index.html")
	groups := []string{"Download", "Notifiche", "Nomi e metadati", "Log e manutenzione"}
	at := -1
	for _, g := range groups {
		i := strings.Index(html, "<h2>"+g+"</h2>")
		if i < 0 {
			t.Fatalf("settings group %q missing", g)
		}
		if i < at {
			t.Errorf("group %q is out of order", g)
		}
		at = i
	}
	// The two strip regexes are the rarest thing in the document; they belong
	// behind a disclosure, not on the main plane. LastIndex, because the Download
	// group grew its own Avanzate disclosure (G5) and this assertion is about the
	// one in "Nomi e metadati".
	adv := strings.LastIndex(html, "<summary>Avanzate</summary>")
	if adv < 0 {
		t.Fatal("no Avanzate disclosure")
	}
	for _, id := range []string{"s_stripBrackets", "s_stripTags"} {
		if strings.Index(html, id) < adv {
			t.Errorf("%s is outside the Avanzate disclosure", id)
		}
	}
}

// TestSettingsHaveAnUnsavedChangesBar: the form is long enough that a Save
// button at the bottom is easy to leave un-pressed.
func TestSettingsHaveAnUnsavedChangesBar(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")
	if !strings.Contains(html, `id="saveBar"`) || !strings.Contains(html, "Modifiche non salvate") {
		t.Error("no unsaved-changes bar")
	}
	if !strings.Contains(html, `id="revertSettings"`) {
		t.Error("the bar offers no way back")
	}
	if !strings.Contains(js, "settingsDirty") || !strings.Contains(js, "refreshSaveBar") {
		t.Error("the bar is not driven by a dirty check")
	}
	// Measured against what the SERVER says is persisted, not against the form's
	// initial paint — otherwise a value the server normalised reads as dirty for
	// ever.
	if !strings.Contains(js, "savedSettings = s") {
		t.Error("the saved snapshot does not come from the server response")
	}
}

// TestNewSettingsHaveControls is what let the handleSettings carry-through
// workaround be deleted: a key with no control gets silently reset on save.
func TestNewSettingsHaveControls(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")
	for _, id := range []string{"s_jobTimeout", "s_openFolderOnDone"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("no control for %s", id)
		}
	}
	for _, key := range []string{`"jobTimeout"`, `"openFolderOnDone"`} {
		if !strings.Contains(js, key) {
			t.Errorf("%s is not in SETTING_IDS, so it would not be sent", key)
		}
	}
}

// TestOpenFolderOnDoneIsNotALiveDownloadControl: every download started in the
// GUI is queued, and the setting applies to foreground downloads only — so as a
// live control in the Download group it could never do anything for the user
// looking at it (G5, ux-principles.md §4). It stays editable, because the CLI
// honours it and this form is the only settings editor, but it sits behind the
// Avanzate disclosure and its label names the channel it belongs to.
func TestOpenFolderOnDoneIsNotALiveDownloadControl(t *testing.T) {
	html := assetText(t, "assets/index.html")
	js := assetText(t, "assets/app.js")

	adv := strings.Index(html, "<summary>Avanzate</summary>")
	box := strings.Index(html, `id="s_openFolderOnDone"`)
	if adv < 0 || box < 0 {
		t.Fatal("the open_folder_on_done control or the disclosure is missing")
	}
	if box < adv {
		t.Error("open_folder_on_done is still a live control on the Download plane")
	}
	if !strings.Contains(html, "da terminale") {
		t.Error("the label does not say which channel the setting applies to")
	}
	// And where the platform has no launcher it cannot work on any channel: then
	// it is disabled with the reason.
	if !strings.Contains(js, "setOpenFolderAvailability") {
		t.Error("the control ignores the platform capability")
	}
}

// TestCloseWarningDoesNotImplyCancellation: closing the tab does NOT stop the
// queue — the daemon keeps draining until it empties (ADR-0008). The warning
// exists because the draining is then unattended, so it must say that, not the
// opposite (G10).
func TestCloseWarningDoesNotImplyCancellation(t *testing.T) {
	js := assetText(t, "assets/app.js")
	i := strings.Index(js, `window.addEventListener("beforeunload"`)
	if i < 0 {
		t.Fatal("no beforeunload warning; the GUI closes silently on a queue with work")
	}
	handler := js[i:]
	if j := strings.Index(handler, "\n});"); j >= 0 {
		handler = handler[:j]
	}
	if !strings.Contains(handler, "proseguono anche se chiudi") {
		t.Error("the close warning does not say the downloads continue; it reads as a threat to cancel them")
	}
}

// TestSPAUsesTheSharedVocabulary keeps the two channels from inventing synonyms
// (ux-principles.md §3).
func TestSPAUsesTheSharedVocabulary(t *testing.T) {
	js := assetText(t, "assets/app.js")
	html := assetText(t, "assets/index.html")
	both := js + html
	for _, word := range []string{"Annulla", "Riscarica", "Apri", "Cronologia", "Impostazioni"} {
		if !strings.Contains(both, word) {
			t.Errorf("the GUI never uses the agreed label %q", word)
		}
	}
	// "Riprova" belongs to the queue (a spool job), never to history — see
	// TestRiprovaStaysOffHistoryRows, which checks the region rather than the
	// whole file.
}

// assertOneReloadInTheHandover is the NARROWED form of the reload prohibition
// (ADR-0016 §10). The rule above is right and stays, with exactly one exception:
// the moment the update hands over to a new binary, where the document is stale
// BY DEFINITION because the server that will answer its next request is a
// different build from the one that served it. The alternative is worse — a page
// running the old script against the new server, looking updated without being
// it — and handleIndex already sends Cache-Control: no-store for exactly this
// pairing.
//
// So: exactly one location.reload(, and it must sit in the update poll. Anywhere
// else it is the old bug back again.
func assertOneReloadInTheHandover(t *testing.T, js string) {
	t.Helper()
	const reload = "location.reload("
	if n := strings.Count(js, reload); n != 1 {
		t.Fatalf("app.js has %d occurrences of %q; exactly one is allowed, in the update handover", n, reload)
	}
	body := functionBody(t, js, "async function pollUpdate() {")
	if !strings.Contains(body, reload) {
		t.Error("the one allowed reload is not in the update handover path; every other navigation must stay a hash route")
	}
}

// TestRiprovaStaysOffHistoryRows is the vocabulary guard, narrowed to what it
// actually protects. "Riprova" acts on a spool job the queue still holds, never
// on a history record (ux-principles.md §3) — so it must not appear in the
// history-row rendering. The update panel uses the same word for a different
// object entirely (a failed installer run, design §7.3), which no user could
// confuse with a download: it is in another view, under a sentence that names
// the update.
func TestRiprovaStaysOffHistoryRows(t *testing.T) {
	js := assetText(t, "assets/app.js")
	start := strings.Index(js, "function primaryAction(")
	end := strings.Index(js, "function renderRecent(")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot locate the history-row rendering; re-anchor this guard")
	}
	if region := js[start:end]; strings.Contains(region, "Riprova") {
		t.Error("the GUI offers 'Riprova' on history rows; that verb acts on spool jobs")
	}
}

// functionBody returns the source of the function whose header is given, from
// the header to the first line that closes it at column zero.
func functionBody(t *testing.T, js, header string) string {
	t.Helper()
	i := strings.Index(js, header)
	if i < 0 {
		t.Fatalf("cannot find %q in app.js; re-anchor this guard", header)
	}
	rest := js[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}
