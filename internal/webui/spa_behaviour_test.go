package webui

import (
	"os/exec"
	"strings"
	"testing"
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
