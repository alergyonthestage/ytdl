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
eval(extract(/function playlistDefault\(\) \{[\s\S]*?\n\}/));
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

// TestASuccessfulSubmitReturnsThePlaylistBoxToItsDefault: left ticked, the next
// link carrying "&list=" turns one track into the whole playlist (G4). It goes
// back to the DEFAULT, not to unticked — the server's own playlist_default.
func TestASuccessfulSubmitReturnsThePlaylistBoxToItsDefault(t *testing.T) {
	off := runNode(t, submitHarness+`
global.api = async () => ({ queue: null });
savedSettings = { playlistDefault: false };
nodes.url.value = "https://youtu.be/A";
nodes.playlist.checked = true;                       // ticked for this one download
handler({ preventDefault: () => {} }).then(() => console.log(state()));
`)
	if !strings.Contains(off, "playlist=false") {
		t.Errorf("a one-shot playlist tick survived its own download:\n%s", off)
	}

	on := runNode(t, submitHarness+`
global.api = async () => ({ queue: null });
savedSettings = { playlistDefault: true };
nodes.url.value = "https://youtu.be/A";
nodes.playlist.checked = false;                      // unticked against the default
handler({ preventDefault: () => {} }).then(() => console.log(state()));
`)
	if !strings.Contains(on, "playlist=true") {
		t.Errorf("the box did not return to a configured default of true:\n%s", on)
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

// sessionHarness evaluates the real session-folder logic plus applyState, with
// stand-ins for everything else applyState touches.
const sessionHarness = harness + `
const nodes = {
  sessionOut: { value: "" },
  sessionPending: { hidden: true },
  historyWindow: { textContent: "" },
  saveSession: { disabled: false, addEventListener: () => {} },
};
const $ = (id) => nodes[id];
const applyQueue = () => {}, renderRecent = () => {}, setDaemon = () => {};
const setOpenFolderAvailability = () => {}, retentionLabel = () => "da sempre";
const setMsg = () => {};
let retentionDays = 0;
let appliedSessionOut = "";
let sessionEpoch = 0;

eval(extract(/function sessionDirty\(\) \{[\s\S]*?\n\}/));
eval(extract(/function refreshSessionPending\(\) \{[\s\S]*?\n\}/));
eval(extract(/function applyState\(s, sessionTrusted\) \{[\s\S]*?\n\}/));
eval(extract(/async function loadState\(\) \{[\s\S]*?\n\}/));
// The click handler, anchored on its own control's id.
const saveSrc = extract(/\$\("saveSession"\)\.addEventListener\("click", async \(\) => \{[\s\S]*?\n\}\);/)
  .replace(/^\$\("saveSession"\)\.addEventListener\("click", /, "").replace(/\);$/, "");
const saveSession = eval("(" + saveSrc + ")");
const state = () => "field=" + nodes.sessionOut.value + " pending=" + !nodes.sessionPending.hidden;
`

// TestTypingASessionFolderMarksItPending: only "Applica alla sessione" puts the
// value in force. Typed and left, it used to sit there looking authoritative
// while the download went to the old folder (G2, ux-principles.md §8.4).
func TestTypingASessionFolderMarksItPending(t *testing.T) {
	out := runNode(t, sessionHarness+`
nodes.sessionOut.value = "/tmp/altrove";   // typed, never applied
refreshSessionPending();
console.log("typed:" + state());
appliedSessionOut = "/tmp/altrove";        // now applied
refreshSessionPending();
console.log("applied:" + state());
`)
	if !strings.Contains(out, "typed:field=/tmp/altrove pending=true") {
		t.Errorf("an unapplied session folder is not marked:\n%s", out)
	}
	if !strings.Contains(out, "applied:field=/tmp/altrove pending=false") {
		t.Errorf("the mark survives the value being applied:\n%s", out)
	}
}

// TestAnUntouchedSessionFieldIsNeverMarkedPending: the field is compared
// trimmed, so the value it is compared AGAINST must be trimmed too. The session
// endpoint stores whatever it is given, and a padded value pinned the marker
// open for ever on a field nobody had touched — the same "a warning that is
// always on is a warning nobody reads" failure the settings bar already had.
func TestAnUntouchedSessionFieldIsNeverMarkedPending(t *testing.T) {
	out := runNode(t, sessionHarness+`
applyState({ queue: {}, history: [], sessionOutputDir: " /tmp/con-spazi " });
console.log("padded:" + state());
`)
	if !strings.Contains(out, "pending=false") {
		t.Errorf("an untouched field was marked as changed:\n%s", out)
	}
}

// TestAStateRefreshNeverOverwritesAPendingSessionEdit: /api/state lands on a
// reconnect, at any moment. It must not throw away what the user is typing, and
// it must not make a pending edit look applied.
func TestAStateRefreshNeverOverwritesAPendingSessionEdit(t *testing.T) {
	out := runNode(t, sessionHarness+`
nodes.sessionOut.value = "/tmp/che-sto-scrivendo";
refreshSessionPending();
applyState({ queue: {}, history: [], sessionOutputDir: "" });
console.log("dirty:" + state());

// A field that agrees with what is in force does adopt the server's value.
nodes.sessionOut.value = "";
appliedSessionOut = "";
applyState({ queue: {}, history: [], sessionOutputDir: "/srv/musica" });
console.log("clean:" + state());
`)
	if !strings.Contains(out, "dirty:field=/tmp/che-sto-scrivendo pending=true") {
		t.Errorf("a state refresh discarded an edit in progress, or cleared its mark:\n%s", out)
	}
	if !strings.Contains(out, "clean:field=/srv/musica pending=false") {
		t.Errorf("an untouched field did not follow the server:\n%s", out)
	}
}

// TestAStaleStateFrameCannotUndoAJustAppliedSessionFolder is the race the review
// pass found in the G2 fix: /api/state is fetched on every reconnect, and a
// frame requested BEFORE the user pressed "Applica alla sessione" carries the
// old override. Adopting it blanks the field while the daemon keeps downloading
// into the folder they set — G2 in the opposite direction, and with no marker.
func TestAStaleStateFrameCannotUndoAJustAppliedSessionFolder(t *testing.T) {
	out := runNode(t, sessionHarness+`
// The state frame is requested first and resolves LAST, carrying no override.
global.fetch = (url) => new Promise((res) => {
  if (url === "/api/state") {
    setTimeout(() => res({ ok: true, json: async () => ({ queue: {}, history: [], sessionOutputDir: "" }) }), 30);
  } else {
    setTimeout(() => res({ ok: true, json: async () => ({ sessionOutputDir: "/tmp/A" }) }), 5);
  }
});

(async () => {
  const inFlight = loadState();               // a reconnect, already on the wire
  nodes.sessionOut.value = "/tmp/A";          // the user types and applies
  await saveSession();
  console.log("applied:" + state());
  await inFlight;                             // the stale frame lands afterwards
  console.log("after stale frame:" + state());
})();
`)
	if !strings.Contains(out, "applied:field=/tmp/A pending=false") {
		t.Fatalf("the PUT did not take effect:\n%s", out)
	}
	if !strings.Contains(out, "after stale frame:field=/tmp/A pending=false") {
		t.Errorf("a stale state frame silently undid the applied session folder:\n%s", out)
	}
}

// TestApplyingDoesNotDiscardWhatTheUserKeptTyping: the success branch used to
// overwrite the field unconditionally, so anything typed while the PUT was in
// flight vanished — and the pending marker was hidden at the same time, leaving
// the loss with no trace at all.
func TestApplyingDoesNotDiscardWhatTheUserKeptTyping(t *testing.T) {
	out := runNode(t, sessionHarness+`
global.fetch = () => new Promise((res) => {
  setTimeout(() => res({ ok: true, json: async () => ({ sessionOutputDir: "/tmp/A" }) }), 10);
});

(async () => {
  nodes.sessionOut.value = "/tmp/A";
  const put = saveSession();
  nodes.sessionOut.value = "/tmp/AB";   // the user keeps typing while it is in flight
  await put;
  console.log("after:" + state());
})();
`)
	if !strings.Contains(out, "after:field=/tmp/AB pending=true") {
		t.Errorf("the in-flight PUT discarded what the user was typing, or hid the marker:\n%s", out)
	}
}

// TestOpenFolderControlFollowsThePlatformCapability: where ytdl has no desktop
// launcher the setting can do nothing on any channel, so the control is disabled
// WITH the reason — and the reason goes away again when it can work, rather than
// staying pinned as a permanent error (G5).
func TestOpenFolderControlFollowsThePlatformCapability(t *testing.T) {
	out := runNode(t, harness+`
const fn = extract(/function setOpenFolderAvailability\(canOpen\) \{[\s\S]*?\n\}/);
const nodes = {
  s_openFolderOnDone: { disabled: false },
  openFolderHint: { hidden: false },
  openFolderUnavailable: { hidden: true },
};
const $ = (id) => nodes[id];
const state = () => nodes.s_openFolderOnDone.disabled +
  ":normale=" + !nodes.openFolderHint.hidden +
  ":motivo=" + !nodes.openFolderUnavailable.hidden;

eval(fn);
setOpenFolderAvailability(false);
console.log("off:" + state());
setOpenFolderAvailability(true);
console.log("on:" + state());
`)

	if !strings.Contains(out, "off:true:normale=false:motivo=true") {
		t.Errorf("with no launcher the control is left live, or gives no reason:\n%s", out)
	}
	if !strings.Contains(out, "on:false:normale=true:motivo=false") {
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
const runFn = extract(/function runningRow\(job\) \{[\s\S]*?\n\}/);
const paintFn = extract(/function paintProgress\(id\) \{[\s\S]*?\n\}/);
const lineFn = extract(/function progressLine\(p\) \{[\s\S]*?\n\}/);

// itemRow does NOT filter here: a row that pushes a null would show up as an
// empty label, which is exactly what the assertions below must be able to see.
// textContent, not a private field: paintProgress writes through it too, so the
// running row's live line is observed exactly as the browser would see it.
const el = (tag, cls, text) => ({ tag, cls, textContent: text == null ? "" : text, kids: [], style: {}, appendChild(n) { this.kids.push(n); } });
const itemRow = (title, metaNodes, actions) => ({ title, meta: metaNodes, querySelector: () => ({ textContent: "", appendChild() {} }) });
const cancelButton = () => ({ tag: "button" });
const progress = new Map();
const liveRows = new Map();
const document = { createTextNode: (t) => ({ textContent: t }) };

eval(destFn);
eval(lineFn);
eval(paintFn);
eval(pendFn);
eval(runFn);

const shape = (row) => row.meta.map((m) => (m === null ? "NULL" : m.textContent)).join("|");
console.log("pending-with:" + shape(pendingRow({ id: "1", url: "u", format: "mp3", location: "~/Music/ytdl" })));
console.log("pending-without:" + shape(pendingRow({ id: "2", url: "u", format: "mp3" })));
console.log("running-with:" + shape(runningRow({ id: "3", url: "u", format: "mp3", location: "/srv/musica" })));
console.log("running-without:" + shape(runningRow({ id: "4", url: "u", format: "mp3" })));
`)

	for _, want := range []string{
		"pending-with:.mp3|cartella: ~/Music/ytdl",
		"running-with:|avvio… · .mp3|cartella: /srv/musica",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a queued row does not say where the file will land (want %q):\n%s", want, out)
		}
	}
	// A job with no known destination renders no label at all — not an empty one,
	// and not a null the row builder leaves for someone else to filter.
	if !strings.Contains(out, "pending-without:.mp3\n") || !strings.Contains(out, "running-without:|avvio… · .mp3\n") {
		t.Errorf("a job with no known destination rendered an empty or null label:\n%s", out)
	}
}

// TestAnOlderLogResponseCannotOverwriteANewerOne: open one failure's log, then
// another before the first has arrived, and the slow first response used to
// land last — putting download A's log under download B's title, and since G8
// next to B's hint. Same race, and same guard, as loadHistory.
func TestAnOlderLogResponseCannotOverwriteANewerOne(t *testing.T) {
	out := runNode(t, harness+`
const revealFn = extract(/function revealPanel\(panel\) \{[\s\S]*?\n\}/);
const showFn = extract(/async function showLog\(h\) \{[\s\S]*?\n\}/);
// Declared here rather than eval'd: eval("let x") creates x in its OWN scope,
// where the eval'd function cannot see it.
let logSeq = 0;

const panel = { hidden: true, scrollIntoView: () => {}, focus: () => {} };
const nodes = { logPanel: panel, logTitle: { textContent: "" }, logBody: { textContent: "" } };
const $ = (id) => nodes[id];
global.window = { matchMedia: () => ({ matches: false }) };
// A's log is big and slow; B's is small and fast.
const bodies = { A: { delay: 40, text: "LOG-DI-A" }, B: { delay: 5, text: "LOG-DI-B" } };
global.fetch = (url) => new Promise((res) => {
  const b = bodies[url.endsWith("A") ? "A" : "B"];
  setTimeout(() => res({ ok: true, text: async () => b.text }), b.delay);
});

eval(revealFn);
eval(showFn);

(async () => {
  const first = showLog({ id: "A", title: "Titolo A" });
  const second = showLog({ id: "B", title: "Titolo B" });
  await Promise.all([first, second]);
  console.log("title:" + nodes.logTitle.textContent);
  console.log("body:" + nodes.logBody.textContent);
})();
`)
	if !strings.Contains(out, "title:Dettaglio: Titolo B") {
		t.Fatalf("the panel does not name the log the user asked for last:\n%s", out)
	}
	if !strings.Contains(out, "body:LOG-DI-B") {
		t.Errorf("an older log response overwrote a newer one — the panel shows one download's log under another's title:\n%s", out)
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
let logSeq = 0;
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
