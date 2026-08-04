package webui

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
)

// The other SPA tests grep the JavaScript source. That catches a deleted
// listener or a renamed id, but it cannot catch a function that is present,
// named correctly, and WRONG — the review found two of those (the settings
// dirty check compared key-ordered JSON and was always true; the history loader
// let an older response overwrite a newer one), and every grep-based test passed
// with both bugs in place.
//
// These tests therefore EXECUTE the shipped functions under node. node is not a
// build dependency of ytdl and is not guaranteed in CI, so they skip when it is
// absent rather than fail — a skipped check is honest, a check that cannot run
// pretending to pass is not.

// runNode evaluates script with node and returns its stdout.
func runNode(t *testing.T, script string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available; these tests execute the shipped app.js")
	}
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	return string(out)
}

// harness pulls the named declarations out of the real asset, so the test
// exercises what ships rather than a copy that can drift.
const harness = `
const fs = require("fs");
const src = fs.readFileSync("assets/app.js", "utf8");
function extract(re) {
  const m = src.match(re);
  if (!m) { console.log("EXTRACT-FAILED"); process.exit(1); }
  return m[0];
}
`

// TestSettingsDirtyCheckIsFalseOnAnUntouchedForm is the regression test for the
// review CRITICAL: readSettings() builds its object with concurrency appended
// LAST, while the server document carries it in Go struct order. Comparing the
// two by JSON.stringify made an untouched form permanently "dirty", so the
// sticky bar was pinned open in every state and a real unsaved change was
// indistinguishable from the steady state.
func TestSettingsDirtyCheckIsFalseOnAnUntouchedForm(t *testing.T) {
	out := runNode(t, harness+`
const SETTING_IDS = eval(extract(/const SETTING_IDS = \[[\s\S]*?\];/).replace(/^const SETTING_IDS = /, "").replace(/;$/, ""));
const dirtyFn = extract(/function settingsDirty\(\) \{[\s\S]*?\n\}/);

let savedSettings = null, current = {};
const readSettings = () => current;
eval(dirtyFn);

// The server document, in Go struct order.
const server = {};
for (const k of SETTING_IDS) {
  server[k] = k === "concurrency" ? 3 : k === "jobTimeout" ? 0
            : k === "logRetentionDays" ? 30 : k === "format" ? "mp3"
            : k === "notifyOn" ? "both" : k === "audioQuality" ? "0"
            : k === "outputDir" ? "/music" : false;
}
// The form's own rebuild order: concurrency skipped in the loop, appended last.
const form = {};
for (const k of SETTING_IDS) { if (k === "concurrency") continue; form[k] = server[k]; }
form.concurrency = server.concurrency;

savedSettings = server;
current = form;
console.log("untouched:" + settingsDirty());

current = Object.assign({}, form, { format: "flac" });
console.log("edited:" + settingsDirty());

// A number that came back from a form field as a string is not a change.
savedSettings = Object.assign({}, server, { logRetentionDays: 30 });
current = Object.assign({}, form, { logRetentionDays: "30" });
console.log("numeric-string:" + settingsDirty());
`)

	if !strings.Contains(out, "untouched:false") {
		t.Errorf("an untouched settings form reads as dirty — the unsaved-changes bar would never clear:\n%s", out)
	}
	if !strings.Contains(out, "edited:true") {
		t.Errorf("a real edit does not read as dirty — the bar would never appear:\n%s", out)
	}
	if !strings.Contains(out, "numeric-string:false") {
		t.Errorf("a number round-tripped through a form field reads as a change:\n%s", out)
	}
}

// TestAudioQualitySelectOffersTheWholeScale executes the shipped declaration
// rather than grepping it: the form must offer every value the server accepts.
// While the list stopped at 9 and the server took 0-10, a config file set to 10
// lost its value the first time the settings form was saved — the select had no
// matching option, so it fell back to the first one (G7).
func TestAudioQualitySelectOffersTheWholeScale(t *testing.T) {
	out := runNode(t, harness+`
const list = eval(extract(/const AUDIO_QUALITIES = [^;]+;/).replace(/^const AUDIO_QUALITIES = /, "").replace(/;$/, ""));
console.log(list.join(","));
`)
	var want []string
	for i := 0; i <= config.MaxAudioQuality; i++ {
		want = append(want, strconv.Itoa(i))
	}
	if got := strings.TrimSpace(out); got != strings.Join(want, ",") {
		t.Errorf("the quality select offers %q, want %q", got, strings.Join(want, ","))
	}
}

// submitHarness evaluates the REAL download-form handler with stand-ins for the
// DOM and the API, so the reset rules are tested on the code that ships rather
// than on a description of it.
const submitHarness = harness + `
const nodes = {
  url: { value: "", focus: () => {} },
  outDir: { value: "" },
  outDirBox: { open: true },
  format: { value: "mp3" },
  playlist: { checked: false },
  go: { disabled: false },
};
const $ = (id) => nodes[id];
const setMsg = () => {};
const applyQueue = () => {};
let savedSettings = null;

eval(extract(/function resetPerDownloadControls\(\) \{[\s\S]*?\n\}/));
// Anchored on the form's own id: the settings form's handler opens with the
// same two lines, and an unanchored match would silently test that one.
const handlerSrc = extract(/\$\("dl"\)\.addEventListener\("submit", async \(ev\) => \{[\s\S]*?\n\}\);/)
  .replace(/^\$\("dl"\)\.addEventListener\("submit", /, "").replace(/\);$/, "");
const handler = eval("(" + handlerSrc + ")");
const state = () => "url=" + nodes.url.value + " dir=" + nodes.outDir.value +
  " open=" + nodes.outDirBox.open + " playlist=" + nodes.playlist.checked;
`

// TestASuccessfulSubmitClearsThePerDownloadFolder: the field promises "vale solo
// per questo download" and used to keep its value, with the disclosure still
// open, so the NEXT download silently went to the same override (G3,
// ux-principles.md §8.1).
func TestASuccessfulSubmitClearsThePerDownloadFolder(t *testing.T) {
	out := runNode(t, submitHarness+`
global.api = async () => ({ queue: null });
nodes.url.value = "https://youtu.be/A";
nodes.outDir.value = "/tmp/una-tantum";
handler({ preventDefault: () => {} }).then(() => console.log(state()));
`)
	if !strings.Contains(out, `url= dir= open=false`) {
		t.Errorf("a one-shot folder survived its own download:\n%s", out)
	}
}

// A FAILED submit keeps everything: the user has to be able to fix the link and
// press again without retyping the folder they had chosen.
func TestAFailedSubmitKeepsWhatTheUserTyped(t *testing.T) {
	out := runNode(t, submitHarness+`
global.api = async () => { throw new Error("boom"); };
nodes.url.value = "https://youtu.be/A";
nodes.outDir.value = "/tmp/una-tantum";
handler({ preventDefault: () => {} }).then(() => console.log(state()));
`)
	if !strings.Contains(out, `url=https://youtu.be/A dir=/tmp/una-tantum`) {
		t.Errorf("a failed submit threw away what the user typed:\n%s", out)
	}
}

// TestOpenFolderControlFollowsThePlatformCapability: where ytdl has no desktop
// launcher the setting can do nothing on any channel, so the control is disabled
// WITH the reason — and the reason goes away again when it can work, rather than
// staying pinned as a permanent error (G5).
func TestOpenFolderControlFollowsThePlatformCapability(t *testing.T) {
	out := runNode(t, harness+`
const fn = extract(/function setOpenFolderAvailability\(canOpen\) \{[\s\S]*?\n\}/);
const nodes = { s_openFolderOnDone: { disabled: false }, openFolderHint: { textContent: "" } };
const $ = (id) => nodes[id];
const OPEN_FOLDER_HINT = "IL-TESTO-NORMALE";

eval(fn);
setOpenFolderAvailability(false);
console.log("off:" + nodes.s_openFolderOnDone.disabled + ":" + nodes.openFolderHint.textContent);
setOpenFolderAvailability(true);
console.log("on:" + nodes.s_openFolderOnDone.disabled + ":" + nodes.openFolderHint.textContent);
`)

	if !strings.Contains(out, "off:true:Non disponibile") {
		t.Errorf("with no launcher the control is left live, or gives no reason:\n%s", out)
	}
	if !strings.Contains(out, "on:false:IL-TESTO-NORMALE") {
		t.Errorf("the control does not come back when the platform can open a folder:\n%s", out)
	}
}

// TestQueueRowsShowWhereTheJobWillLand executes the row builders: both the
// running and the pending row must state the destination the server sent, and a
// job without one must not render an empty label (G1).
func TestQueueRowsShowWhereTheJobWillLand(t *testing.T) {
	out := runNode(t, harness+`
const destFn = extract(/function destinationLine\(job\) \{[\s\S]*?\n\}/);
const pendFn = extract(/function pendingRow\(job\) \{[\s\S]*?\n\}/);

const texts = [];
const el = (tag, cls, text) => { const n = { tag, cls, text, kids: [] }; if (text != null) texts.push(text); return n; };
const itemRow = (title, metaNodes) => ({ title, meta: metaNodes.filter(Boolean) });
const cancelButton = () => ({ tag: "button" });

eval(destFn);
eval(pendFn);

const withDir = pendingRow({ id: "1", url: "u", format: "mp3", location: "~/Music/ytdl" });
const without = pendingRow({ id: "2", url: "u", format: "mp3" });
console.log("with:" + withDir.meta.map((m) => m.text).join("|"));
console.log("without:" + without.meta.map((m) => m.text).join("|"));
`)

	if !strings.Contains(out, "with:.mp3|cartella: ~/Music/ytdl") {
		t.Errorf("a queued row does not say where the file will land:\n%s", out)
	}
	if !strings.Contains(out, "without:.mp3\n") {
		t.Errorf("a job with no known destination rendered an empty label:\n%s", out)
	}
}

// TestOpeningTheLogPanelBringsItIntoView: the panel sits above the list, so
// "Vedi errore" on a row far down un-hid it off-screen — a control that appears
// to do nothing (G6). It must be scrolled to, and focused, before the fetch, so
// the panel is on screen WHILE it loads.
func TestOpeningTheLogPanelBringsItIntoView(t *testing.T) {
	out := runNode(t, harness+`
const revealFn = extract(/function revealPanel\(panel\) \{[\s\S]*?\n\}/);
const showFn = extract(/async function showLog\(h\) \{[\s\S]*?\n\}/);

const events = [];
const panel = {
  hidden: true,
  scrollIntoView: (o) => events.push("scroll:" + o.block),
  focus: () => events.push("focus"),
};
const nodes = { logPanel: panel, logTitle: {}, logBody: {} };
const $ = (id) => nodes[id];
global.window = { matchMedia: () => ({ matches: false }) };
global.fetch = async () => { events.push("fetch"); return { ok: true, text: async () => "log" }; };

eval(revealFn);
eval(showFn);
showLog({ id: "abc", title: "t" }).then(() => {
  console.log("hidden:" + panel.hidden);
  console.log("events:" + events.join(","));
});
`)

	if !strings.Contains(out, "hidden:false") {
		t.Errorf("the panel was not shown:\n%s", out)
	}
	if !strings.Contains(out, "events:scroll:start,focus,fetch") {
		t.Errorf("the panel is not scrolled to and focused before the fetch:\n%s", out)
	}
}

// TestOlderHistoryResponseCannotOverwriteANewerOne is the regression test for the
// review's request-ordering finding: type a search, click a filter within the
// 200 ms debounce, and the older (unfiltered) response landed last. The chip
// showed pressed over the wrong rows, and BOTH responses added to the same
// offset, so the next "Carica altri" silently skipped a page the user never saw.
// The same race fires with no user haste when an SSE queue frame refreshes the
// view while a load is in flight.
func TestOlderHistoryResponseCannotOverwriteANewerOne(t *testing.T) {
	out := runNode(t, harness+`
const loadFn = extract(/let historySeq = 0;[\s\S]*?\nasync function loadHistory\(append\) \{[\s\S]*?\n\}/);

// Minimal stand-ins for the DOM and network the function touches.
const rendered = { rows: null };
const nodes = { historyList: {}, loadMore: { hidden: true }, logPanel: { hidden: false } };
const $ = (id) => nodes[id];
const el = (tag, cls, text) => ({ tag, cls, text });
const replaceChildren = (node, children) => { rendered.rows = children; };
const setMsg = () => {};
const historyRow = (h) => h.tag;
const historyEmptyText = () => "empty";
const historyQuery = { filter: "all", q: "", offset: 0 };
let historyPaged = false;
const historyURL = () => "/api/history?filter=" + historyQuery.filter;

// The slow response is the UNFILTERED one, requested first.
const responses = {
  "/api/history?filter=all":    { delay: 40, body: { items: [{tag:"ALL-1"},{tag:"ALL-2"}], offset: 0, more: true } },
  "/api/history?filter=failed": { delay: 5,  body: { items: [{tag:"FAILED-1"}], offset: 0, more: false } },
};
global.fetch = (url) => new Promise((res) => {
  const r = responses[url];
  setTimeout(() => res({ ok: true, json: async () => r.body }), r.delay);
});

eval(loadFn);

(async () => {
  const first = loadHistory(false);              // the search, unfiltered + slow
  historyQuery.filter = "failed";                // the user clicks a chip
  const second = loadHistory(false);             // filtered + fast
  await Promise.all([first, second]);
  console.log("rows:" + rendered.rows.map((r) => r).join(","));
  console.log("offset:" + historyQuery.offset);
})();
`)

	if !strings.Contains(out, "rows:FAILED-1") {
		t.Errorf("an older response overwrote the newer one — the list disagrees with the pressed filter:\n%s", out)
	}
	if !strings.Contains(out, "offset:1") {
		t.Errorf("the offset was accumulated from both responses, so the next page would skip records:\n%s", out)
	}
}
