package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// historyHome points HOME and XDG_STATE_HOME at a temp dir and appends the given
// jobs to the log store, returning the log dir. It also stubs the daemon spawn,
// since several of these commands would otherwise launch a real detached process
// from the test binary.
func historyHome(t *testing.T, jobs ...logstore.Job) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("YTDL_OUT_DIR", "")

	old := spawnQueueDaemon
	spawnQueueDaemon = func() error { return nil }
	t.Cleanup(func() { spawnQueueDaemon = old })
	fakeLauncher(t)

	logDir := config.Defaults().LogDir
	for _, j := range jobs {
		if err := logstore.Append(logDir, j); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return logDir
}

// fakeLauncher puts a no-op `open`/`xdg-open` on PATH. Since the review,
// open.Supported() checks that the platform's launcher is actually installed —
// which it is not in a minimal container — so without this the open command
// would bail out before reaching the targeting logic these tests are about. A
// stub also keeps the tests deterministic on a machine that DOES have one: no
// window opens.
func fakeLauncher(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// audioJob is a successful download of a real file on disk, so the open path's
// existence checks have something to find.
func audioJob(t *testing.T, url, title string, when time.Time) logstore.Job {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, title+".mp3")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return logstore.Job{
		URL: url, Title: title, Mode: "default", Format: "mp3", Success: true,
		Time: when, Path: path, Dir: dir, Count: 1,
	}
}

// TestRealMainHistoryShowsLocationAndReason is the user-visible payoff of steps
// 1, 2 and 5 together: a real history listing that says where files went and why
// one failed.
func TestRealMainHistoryShowsLocationAndReason(t *testing.T) {
	now := time.Now()
	historyHome(t,
		audioJob(t, "https://youtu.be/A", "Artista - Traccia", now.Add(-time.Hour)),
		logstore.Job{
			URL: "https://youtu.be/B", Mode: "default", Format: "mp3", Success: false,
			Time: now.Add(-2 * time.Hour), Dir: "/music", Error: "HTTP Error 403: Forbidden",
		},
	)

	rc, out := captureStdout(t, func() int { return realMain([]string{"history"}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "[1] ") || !strings.Contains(out, "[2] ") {
		t.Errorf("rows are not numbered:\n%s", out)
	}
	if !strings.Contains(out, "HTTP Error 403: Forbidden") {
		t.Errorf("the failure reason is not shown:\n%s", out)
	}
	if !strings.Contains(out, "ytdl open <n>") {
		t.Errorf("the footer does not point at the new commands:\n%s", out)
	}
}

func TestRealMainHistorySearch(t *testing.T) {
	now := time.Now()
	historyHome(t,
		logstore.Job{URL: "https://youtu.be/A", Title: "Mina - Grande", Mode: "default", Format: "mp3", Success: true, Time: now.Add(-time.Hour)},
		logstore.Job{URL: "https://youtu.be/B", Title: "Battisti - Il tempo", Mode: "default", Format: "mp3", Success: true, Time: now.Add(-2 * time.Hour)},
	)

	rc, out := captureStdout(t, func() int { return realMain([]string{"history", "--search", "battisti"}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "Battisti") {
		t.Errorf("the matching record is missing:\n%s", out)
	}
	if strings.Contains(out, "Mina") {
		t.Errorf("a non-matching record was listed:\n%s", out)
	}
}

// TestRealMainOpenWithNoTargetLists mirrors a bare `ytdl cancel`: print the
// numbered list so the user can read an index off it.
func TestRealMainOpenWithNoTargetLists(t *testing.T) {
	historyHome(t, audioJob(t, "https://youtu.be/A", "Artista - Traccia", time.Now()))

	rc, out := captureStdout(t, func() int { return realMain([]string{"open"}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "STORICO ytdl") {
		t.Errorf("a bare `ytdl open` should print the numbered history:\n%s", out)
	}
}

// TestRealMainOpenUnknownTarget: an index or id that resolves to nothing must be
// reported, not silently ignored.
func TestRealMainOpenUnknownTarget(t *testing.T) {
	historyHome(t, audioJob(t, "https://youtu.be/A", "Artista - Traccia", time.Now()))

	rc, errOut := captureStderr(t, func() int { return realMain([]string{"open", "99"}) })
	if rc == 0 {
		t.Error("rc = 0 for an out-of-range index, want a failure")
	}
	if !strings.Contains(errOut, "Non trovato") {
		t.Errorf("stderr does not report the miss:\n%s", errOut)
	}
}

// TestRealMainOpenFailedRecordExplains: the primary action on a failure row is
// "riscarica", and the message must say so rather than reporting a bare error.
func TestRealMainOpenFailedRecordExplains(t *testing.T) {
	historyHome(t, logstore.Job{
		URL: "https://youtu.be/B", Mode: "default", Format: "mp3", Success: false,
		Time: time.Now(), Dir: "/music", Error: "HTTP Error 403",
	})

	rc, errOut := captureStderr(t, func() int { return realMain([]string{"open", "1"}) })
	if rc == 0 {
		t.Error("rc = 0 for a failed record with no file, want a failure")
	}
	if !strings.Contains(errOut, "ytdl again") {
		t.Errorf("the message does not say what to do next:\n%s", errOut)
	}
}

// TestRealMainAgainEnqueuesFromHistory is the "riscarica" flow end to end.
func TestRealMainAgainEnqueuesFromHistory(t *testing.T) {
	historyHome(t, audioJob(t, "https://youtu.be/A", "Artista - Traccia", time.Now()))

	rc, out := captureStdout(t, func() int { return realMain([]string{"again", "1"}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0. stdout:\n%s", rc, out)
	}
	if !strings.Contains(out, "Rimesso in coda") {
		t.Errorf("no confirmation line:\n%s", out)
	}

	sp := queue.Open(config.StatePath())
	snap, err := sp.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Pending) != 1 {
		t.Fatalf("pending = %d, want the re-queued job", len(snap.Pending))
	}
	if snap.Pending[0].Job.URL != "https://youtu.be/A" {
		t.Errorf("queued URL = %q, want the record's", snap.Pending[0].Job.URL)
	}
}

// TestRealMainAgainRefusesADuplicate surfaces jobs.ErrAlreadyQueued as a message
// that points at `ytdl queue`, not as a stack of identical jobs.
func TestRealMainAgainRefusesADuplicate(t *testing.T) {
	historyHome(t, audioJob(t, "https://youtu.be/A", "Artista - Traccia", time.Now()))

	if rc, _ := captureStdout(t, func() int { return realMain([]string{"again", "1"}) }); rc != 0 {
		t.Fatalf("first again: rc = %d", rc)
	}
	rc, errOut := captureStderr(t, func() int { return realMain([]string{"again", "1"}) })
	if rc == 0 {
		t.Error("a second `again` succeeded; it must refuse a duplicate")
	}
	if !strings.Contains(errOut, "già in coda") {
		t.Errorf("stderr does not explain the refusal:\n%s", errOut)
	}

	sp := queue.Open(config.StatePath())
	snap, _ := sp.List()
	if len(snap.Pending) != 1 {
		t.Errorf("the queue holds %d copies, want 1", len(snap.Pending))
	}
}

// TestRealMainAgainByIDPrefix: the id is the stable handle, and unlike an index
// it survives the list being renumbered.
func TestRealMainAgainByIDPrefix(t *testing.T) {
	when := time.Now().Add(-time.Hour)
	logDir := historyHome(t, audioJob(t, "https://youtu.be/A", "Artista - Traccia", when))

	entries, err := logstore.Load(logDir, logstore.QueryOpts{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("history fixture: %v (%d entries)", err, len(entries))
	}
	id := entries[0].ID()

	rc, out := captureStdout(t, func() int { return realMain([]string{"again", id[:8]}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0. stdout:\n%s", rc, out)
	}
	sp := queue.Open(config.StatePath())
	snap, _ := sp.List()
	if len(snap.Pending) != 1 {
		t.Errorf("pending = %d, want the job queued by id prefix", len(snap.Pending))
	}
}

func TestRealMainConfigShowsProvenance(t *testing.T) {
	historyHome(t)
	cfgPath := config.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("format = flac\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc, out := captureStdout(t, func() int { return realMain([]string{"config"}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "IMPOSTAZIONI ytdl") {
		t.Errorf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "flac") || !strings.Contains(out, "file di configurazione") {
		t.Errorf("a file-set value is not attributed to the file:\n%s", out)
	}
	if !strings.Contains(out, "(predefinito)") {
		t.Errorf("untouched values are not attributed to the defaults:\n%s", out)
	}
}

// TestRealMainConfigPathIsScriptable: `--path` prints the path and nothing else,
// so a script can do `$EDITOR "$(ytdl config --path)"`.
func TestRealMainConfigPathIsScriptable(t *testing.T) {
	historyHome(t)
	rc, out := captureStdout(t, func() int { return realMain([]string{"config", "--path"}) })
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	got := strings.TrimSpace(out)
	if got != config.ConfigPath() {
		t.Errorf("output = %q, want exactly the config path %q", got, config.ConfigPath())
	}
	if strings.Contains(out, "IMPOSTAZIONI") {
		t.Errorf("--path printed the whole table:\n%s", out)
	}
}

// TestResolveRecordGrammarMatchesCancelRetry: one targeting idiom across the
// tool — a short integer is an index into the list just printed, anything else
// is an id-prefix, and an ambiguous prefix is refused rather than guessed.
func TestResolveRecordGrammarMatchesCancelRetry(t *testing.T) {
	base := time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)
	logDir := historyHome(t,
		logstore.Job{URL: "https://youtu.be/A", Title: "A", Time: base, Success: true},
		logstore.Job{URL: "https://youtu.be/B", Title: "B", Time: base.Add(time.Minute), Success: true},
	)
	entries, err := logstore.Load(logDir, logstore.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("fixture has %d records, want 2", len(entries))
	}

	// Index 1 is the NEWEST, matching the listing order.
	if e, res := resolveRecord("1", entries, logDir); res != targetOK || e.Title != "B" {
		t.Errorf("index 1 → %q (%v), want the newest record B", e.Title, res)
	}
	if _, res := resolveRecord("9", entries, logDir); res != targetNotFound {
		t.Errorf("out-of-range index → %v, want targetNotFound", res)
	}
	// A full id resolves.
	if e, res := resolveRecord(entries[0].ID(), entries, logDir); res != targetOK || e.Title != "B" {
		t.Errorf("full id → %q (%v), want B", e.Title, res)
	}
	// An id that matches nothing.
	if _, res := resolveRecord("ffffffffffffffff", entries, logDir); res != targetNotFound {
		t.Errorf("unknown id → %v, want targetNotFound", res)
	}
}

// TestResolveRecordFindsBeyondTheListingLimit: an index only reaches the rows on
// screen, but an id is a stable handle into the whole retention window — which
// is exactly why the listing tells scripts to use it.
func TestResolveRecordFindsBeyondTheListingLimit(t *testing.T) {
	base := time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)
	old := logstore.Job{URL: "https://youtu.be/OLD", Title: "Vecchio", Time: base, Success: true}
	logDir := historyHome(t, old)
	for i := 0; i < 5; i++ {
		if err := logstore.Append(logDir, logstore.Job{
			URL: "https://youtu.be/N", Title: "Nuovo", Time: base.Add(time.Duration(i+1) * time.Minute), Success: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := logstore.Load(logDir, logstore.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	oldID := all[len(all)-1].ID() // the oldest record

	// A listing capped at 2 rows does not contain the old record...
	page, err := logstore.Load(logDir, logstore.QueryOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, res := resolveRecord("6", page, logDir); res == targetOK {
		t.Error("an index beyond the printed page resolved; it must not")
	}
	// ...but its id still resolves.
	if e, res := resolveRecord(oldID, page, logDir); res != targetOK || e.Title != "Vecchio" {
		t.Errorf("id lookup → %q (%v), want the old record", e.Title, res)
	}
}
