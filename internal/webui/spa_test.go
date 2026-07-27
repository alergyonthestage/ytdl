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
		"location.reload(", "window.open(", "document.write(",
	} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js navigates with %q; the document must never reload", bad)
		}
	}
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
	// "Riprova" belongs to the queue (a spool job), never to history.
	if strings.Contains(js, "Riprova") {
		t.Error("the GUI offers 'Riprova' on history rows; that verb acts on spool jobs")
	}
}
