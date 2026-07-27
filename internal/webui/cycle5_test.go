package webui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/jobs"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// ---- harness -------------------------------------------------------------

// apiServer starts a test server and returns it with its spool and log dir.
func apiServer(t *testing.T) (*httptest.Server, *Server, *queue.Spool, string) {
	t.Helper()
	fakeLauncher(t)
	srv, sp, cfgPath := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, sp, filepath.Join(filepath.Dir(cfgPath), "logs")
}

// fakeLauncher puts a no-op open/xdg-open on PATH. open.Supported() checks that
// the platform's launcher is actually installed, which it is not in a minimal
// container — without this the capability flags would all be false and these
// tests would assert nothing. A stub also means no window opens on a machine
// that does have one.
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

// postJSON performs an API POST with the guards' required content type.
func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (*http.Response, []byte) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func getPath(t *testing.T, ts *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// seedHistory appends jobs to the server's log store and returns their records,
// newest first.
func seedHistory(t *testing.T, logDir string, js ...logstore.Job) []logstore.Entry {
	t.Helper()
	for _, j := range js {
		if err := logstore.Append(logDir, j); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := logstore.Load(logDir, logstore.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// savedJob is a successful download of a file that really exists on disk.
func savedJob(t *testing.T, url, title string, when time.Time) logstore.Job {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, title+".mp3")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return logstore.Job{
		URL: url, Title: title, Mode: "silent", Format: "mp3", Success: true,
		Time: when, Path: path, Dir: dir, Count: 1,
	}
}

func failedJob(url string, when time.Time, reason string) logstore.Job {
	return logstore.Job{
		URL: url, Mode: "silent", Format: "mp3", Success: false, RC: 1,
		Time: when, Dir: "/music", Error: reason,
	}
}

// ---- /api/history --------------------------------------------------------

func TestHistoryEndpointPagesAndReportsMore(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	base := time.Now().Add(-time.Hour)
	var js []logstore.Job
	for i := 0; i < 5; i++ {
		js = append(js, failedJob("https://youtu.be/x", base.Add(time.Duration(i)*time.Minute), "boom"))
	}
	seedHistory(t, logDir, js...)

	resp, body := getPath(t, ts, "/api/history?limit=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var page historyPageDTO
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Errorf("got %d items, want 2", len(page.Items))
	}
	if !page.More {
		t.Error("More = false with 5 records and a limit of 2")
	}

	// The last page reports no more, so "Carica altri" disappears rather than
	// loading nothing.
	resp, body = getPath(t, ts, "/api/history?limit=2&offset=4")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	page = historyPageDTO{}
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || page.More {
		t.Errorf("last page = %d items, more = %v; want 1 and false", len(page.Items), page.More)
	}
}

func TestHistoryEndpointFiltersAndSearches(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	now := time.Now().Add(-time.Hour)
	seedHistory(t, logDir,
		savedJob(t, "https://youtu.be/A", "Mina - Grande", now),
		failedJob("https://youtu.be/B", now.Add(time.Minute), "HTTP Error 403"),
	)

	_, body := getPath(t, ts, "/api/history?failed=1")
	var page historyPageDTO
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || page.Items[0].Success {
		t.Errorf("failed=1 returned %+v, want only the failure", page.Items)
	}

	// The complement: the view's three filters are tutti / completati / non
	// riusciti, and `failed` alone cannot express "only the ones that worked".
	_, body = getPath(t, ts, "/api/history?ok=1")
	page = historyPageDTO{}
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || !page.Items[0].Success {
		t.Errorf("ok=1 returned %+v, want only the success", page.Items)
	}

	_, body = getPath(t, ts, "/api/history?q=mina")
	page = historyPageDTO{}
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || page.Items[0].Title != "Mina - Grande" {
		t.Errorf("q=mina returned %+v, want the matching record", page.Items)
	}

	// Both halves at once is a contradiction; returning nothing is the honest
	// answer, not an arbitrary winner.
	_, body = getPath(t, ts, "/api/history?ok=1&failed=1")
	page = historyPageDTO{}
	json.Unmarshal(body, &page)
	if len(page.Items) != 0 {
		t.Errorf("ok=1&failed=1 returned %+v, want nothing", page.Items)
	}
}

// TestHistoryEndpointClampsAGarbageLimit: an unbounded or negative limit must
// not become an unbounded read of the whole retention window.
func TestHistoryEndpointClampsAGarbageLimit(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	now := time.Now().Add(-time.Hour)
	var js []logstore.Job
	for i := 0; i < 10; i++ {
		js = append(js, failedJob("https://youtu.be/x", now.Add(time.Duration(i)*time.Minute), "boom"))
	}
	seedHistory(t, logDir, js...)

	for _, q := range []string{"?limit=99999", "?limit=-5", "?limit=abc", "?offset=-1"} {
		t.Run(q, func(t *testing.T) {
			resp, body := getPath(t, ts, "/api/history"+q)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.StatusCode, body)
			}
			var page historyPageDTO
			json.Unmarshal(body, &page)
			if len(page.Items) > historyPageMax {
				t.Errorf("returned %d items, above the cap of %d", len(page.Items), historyPageMax)
			}
		})
	}
}

// TestHistoryDTOCarriesTheRowState: the browser cannot stat the disk, so the
// server has to tell it whether the file is still there — that is what decides
// each row's primary action (design §8.3).
func TestHistoryDTOCarriesTheRowState(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	now := time.Now().Add(-time.Hour)
	gone := savedJob(t, "https://youtu.be/G", "Sparito", now)
	if err := os.Remove(gone.Path); err != nil {
		t.Fatal(err)
	}
	seedHistory(t, logDir,
		savedJob(t, "https://youtu.be/A", "Presente", now.Add(-time.Minute)),
		gone,
		failedJob("https://youtu.be/B", now.Add(time.Minute), "HTTP Error 403"),
	)

	_, body := getPath(t, ts, "/api/history")
	var page historyPageDTO
	json.Unmarshal(body, &page)
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(page.Items))
	}
	byTitle := map[string]historyDTO{}
	for _, it := range page.Items {
		key := it.Title
		if key == "" {
			key = "failed"
		}
		byTitle[key] = it
	}

	if !jobs.CanOpen() {
		t.Skip("no launcher on this platform; the capability flags are all false by design")
	}
	if !byTitle["Presente"].CanOpenFile {
		t.Error("a file that exists is not reported as openable")
	}
	if byTitle["Sparito"].CanOpenFile {
		t.Error("a deleted file is reported as openable")
	}
	if !byTitle["Sparito"].CanOpenFolder {
		t.Error("a vanished file's folder should still be reachable")
	}
	if byTitle["failed"].CanOpenFile {
		t.Error("a failure with no file is reported as openable")
	}
	if byTitle["failed"].Error != "HTTP Error 403" {
		t.Errorf("the failure reason is missing from the DTO: %+v", byTitle["failed"])
	}
}

// TestHistoryDTOSendsNoAbsolutePath: every action is addressed by record id, and
// the location is a display string. Shipping the path would invite a future
// change to send it back.
func TestHistoryDTOSendsNoAbsolutePath(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	job := savedJob(t, "https://youtu.be/A", "Traccia", time.Now().Add(-time.Hour))
	seedHistory(t, logDir, job)

	_, body := getPath(t, ts, "/api/history")
	var page struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(body, &page)
	raw := page.Items
	if len(raw) != 1 {
		t.Fatalf("got %d items, want 1", len(raw))
	}
	for _, forbidden := range []string{"path", "dir"} {
		if _, ok := raw[0][forbidden]; ok {
			t.Errorf("the DTO exposes %q: %v", forbidden, raw[0])
		}
	}
	if raw[0]["id"] == "" || raw[0]["id"] == nil {
		t.Error("the DTO has no id, so no action can address the record")
	}
}

// ---- /api/history/open ---------------------------------------------------

// openRequest is one call the handler would have made to the platform layer.
type openRequest struct {
	entry  logstore.Entry
	target jobs.OpenTarget
}

// captureOpen replaces the handler's platform action with a recorder, so the
// HAPPY path can be exercised without opening windows on the machine running the
// tests. Refusal tests deliberately do NOT use it: they run the real jobs.Open,
// which is where every check lives, and refuse before reaching any launcher.
func captureOpen(t *testing.T) *[]openRequest {
	t.Helper()
	var calls []openRequest
	old := openRecord
	openRecord = func(e logstore.Entry, target jobs.OpenTarget) error {
		calls = append(calls, openRequest{e, target})
		return nil
	}
	t.Cleanup(func() { openRecord = old })
	return &calls
}

func TestOpenEndpointOpensByRecordID(t *testing.T) {
	if !jobs.CanOpen() {
		t.Skip("no launcher on this platform")
	}
	ts, _, _, logDir := apiServer(t)
	job := savedJob(t, "https://youtu.be/A", "Traccia", time.Now().Add(-time.Hour))
	entries := seedHistory(t, logDir, job)
	calls := captureOpen(t)

	resp, body := postJSON(t, ts, "/api/history/open", idReq{ID: entries[0].ID()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	resp, body = postJSON(t, ts, "/api/history/open", idReq{ID: entries[0].ID(), Target: "folder"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("folder status = %d: %s", resp.StatusCode, body)
	}

	if len(*calls) != 2 {
		t.Fatalf("got %d platform calls, want 2", len(*calls))
	}
	if (*calls)[0].target != jobs.OpenFile || (*calls)[1].target != jobs.OpenFolder {
		t.Errorf("targets = %v, %v; want file then folder", (*calls)[0].target, (*calls)[1].target)
	}
	// The handler must hand over the record it resolved from ITS OWN store — not
	// anything the request supplied.
	for i, c := range *calls {
		if c.entry.Path != job.Path {
			t.Errorf("call %d carried path %q, want the record's %q", i, c.entry.Path, job.Path)
		}
	}
}

// TestOpenEndpointRefusesEverythingButAKnownRecord is the security contract of
// design §7 exercised over HTTP. Nothing here may reach the launcher.
func TestOpenEndpointRefusesEverythingButAKnownRecord(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	home := t.TempDir()
	dir := filepath.Join(home, "ytdl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "secret.mp3")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "payload.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour)

	// A hand-edited history: one record points outside its own folder, another
	// at a non-audio file inside it. Both are exactly what an attacker who could
	// write history.jsonl would try.
	entries := seedHistory(t, logDir,
		logstore.Job{URL: "https://youtu.be/OUT", Mode: "silent", Format: "mp3", Success: true,
			Time: now, Path: outside, Dir: dir},
		logstore.Job{URL: "https://youtu.be/SH", Mode: "silent", Format: "mp3", Success: true,
			Time: now.Add(time.Minute), Path: script, Dir: dir},
	)
	byURL := map[string]logstore.Entry{}
	for _, e := range entries {
		byURL[e.URL] = e
	}

	tests := []struct {
		name string
		req  idReq
		want int
	}{
		{"an unknown record", idReq{ID: "ffffffffffffffff"}, http.StatusNotFound},
		{"an empty id", idReq{ID: ""}, http.StatusBadRequest},
		{"a path instead of an id", idReq{ID: "/etc/passwd"}, http.StatusNotFound},
		{"a traversal instead of an id", idReq{ID: "../../../etc/passwd"}, http.StatusNotFound},
		{"a record pointing outside its folder", idReq{ID: byURL["https://youtu.be/OUT"].ID()}, http.StatusBadRequest},
		{"a record pointing at a script", idReq{ID: byURL["https://youtu.be/SH"].ID()}, http.StatusBadRequest},
		{"an unknown target", idReq{ID: byURL["https://youtu.be/SH"].ID(), Target: "exec"}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately no fake here: the REAL jobs.Open runs, and refuses
			// before it would launch anything (proven in the jobs tests).
			resp, body := postJSON(t, ts, "/api/history/open", tc.req)
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d: %s", resp.StatusCode, tc.want, body)
			}
		})
	}
}

// ---- /api/history/again --------------------------------------------------

func TestAgainEndpointEnqueuesAndRefusesADuplicate(t *testing.T) {
	ts, _, sp, logDir := apiServer(t)
	entries := seedHistory(t, logDir, savedJob(t, "https://youtu.be/A", "Traccia", time.Now().Add(-time.Hour)))
	id := entries[0].ID()

	resp, body := postJSON(t, ts, "/api/history/again", idReq{ID: id})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(snap.Pending))
	}

	// The double-click case (design §6).
	resp, body = postJSON(t, ts, "/api/history/again", idReq{ID: id})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second request status = %d, want 409: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "già in coda") {
		t.Errorf("409 body does not explain the refusal: %s", body)
	}
	snap, _ = sp.List()
	if len(snap.Pending) != 1 {
		t.Errorf("the queue holds %d copies, want 1", len(snap.Pending))
	}
}

func TestAgainEndpointUnknownRecord(t *testing.T) {
	ts, _, sp, _ := apiServer(t)
	resp, _ := postJSON(t, ts, "/api/history/again", idReq{ID: "ffffffffffffffff"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 0 {
		t.Errorf("an unknown record enqueued %d jobs", len(snap.Pending))
	}
}

// ---- /api/queue/cancel ---------------------------------------------------

func TestCancelEndpointStopsAPendingJob(t *testing.T) {
	ts, _, sp, _ := apiServer(t)
	id, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/A", Settings: config.Defaults()})
	if err != nil {
		t.Fatal(err)
	}

	resp, body := postJSON(t, ts, "/api/queue/cancel", idReq{ID: id})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	snap, _ := sp.List()
	if len(snap.Pending) != 0 {
		t.Errorf("the job is still pending after a cancel: %+v", snap.Pending)
	}
}

func TestCancelEndpointMarksARunningJob(t *testing.T) {
	ts, _, sp, _ := apiServer(t)
	if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/A", Settings: config.Defaults()}); err != nil {
		t.Fatal(err)
	}
	claim, err := sp.ClaimNext()
	if err != nil || claim == nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	resp, body := postJSON(t, ts, "/api/queue/cancel", idReq{ID: claim.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	// The marker is HOW the daemon's watcher stops a running job.
	marker := filepath.Join(sp.Root(), "cancel", claim.ID)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("no cancel marker for the running job: %v", err)
	}
}

// TestCancelEndpointRefusesAnIDNotInTheQueue is the containment check: a job id
// becomes a FILENAME in the spool, so an arbitrary string from an HTTP body must
// never reach it. The 404 also keeps the answer honest for a job that has since
// finished.
func TestCancelEndpointRefusesAnIDNotInTheQueue(t *testing.T) {
	ts, _, sp, _ := apiServer(t)
	if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/A", Settings: config.Defaults()}); err != nil {
		t.Fatal(err)
	}
	root := sp.Root()

	for _, id := range []string{
		"nosuchjob",
		"../../../tmp/pwned",
		"00000000000000000001-0123456789abcdef-../../pwned",
		"00000000000000000009-0123456789abcdef-notqueued",
	} {
		t.Run(id, func(t *testing.T) {
			resp, body := postJSON(t, ts, "/api/queue/cancel", idReq{ID: id})
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404: %s", resp.StatusCode, body)
			}
		})
	}
	// Nothing was created outside the spool, and nothing odd inside it.
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "tmp", "pwned")); err == nil {
		t.Error("a traversal id created a file outside the spool")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "cancel")); err == nil {
		for _, e := range entries {
			if !jobs.ValidJobID(e.Name()) {
				t.Errorf("a malformed cancel marker was created: %q", e.Name())
			}
		}
	}
}

// ---- /api/history/log ----------------------------------------------------

func TestLogEndpointServesThePerJobLog(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	when := time.Now().Add(-time.Hour)
	job := failedJob("https://youtu.be/B", when, "HTTP Error 403")
	job.Stderr = []byte("ERROR: HTTP Error 403: Forbidden\n")
	if err := logstore.Record(logDir, job); err != nil {
		t.Fatal(err)
	}
	entries := seedHistory(t, logDir, job)

	resp, body := getPath(t, ts, "/api/history/log?id="+entries[0].ID())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain — a .log full of HTML must never render", ct)
	}
	if !strings.Contains(string(body), "HTTP Error 403") {
		t.Errorf("the log body is missing its content: %s", body)
	}
}

func TestLogEndpointMissingLogIs404(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	// A record with no .log written beside it (pruned, or log_dir unset at the time).
	entries := seedHistory(t, logDir, failedJob("https://youtu.be/B", time.Now().Add(-time.Hour), "boom"))

	resp, _ := getPath(t, ts, "/api/history/log?id="+entries[0].ID())
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	resp, _ = getPath(t, ts, "/api/history/log?id=ffffffffffffffff")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", resp.StatusCode)
	}
	resp, _ = getPath(t, ts, "/api/history/log")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing id status = %d, want 400", resp.StatusCode)
	}
}

// TestLogEndpointCapsTheBody: a pathological log must not be streamed whole into
// the browser.
func TestLogEndpointCapsTheBody(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	when := time.Now().Add(-time.Hour)
	job := failedJob("https://youtu.be/B", when, "boom")
	job.Stderr = bytes.Repeat([]byte("x"), 4*logMaxBytes)
	if err := logstore.Record(logDir, job); err != nil {
		t.Fatal(err)
	}
	entries := seedHistory(t, logDir, job)

	resp, body := getPath(t, ts, "/api/history/log?id="+entries[0].ID())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(body) > logMaxBytes {
		t.Errorf("body is %d bytes, above the %d cap", len(body), logMaxBytes)
	}
}

// ---- state + settings ----------------------------------------------------

// TestStateCarriesQueueTitlesAndCapability: the queue view names jobs instead of
// showing bare URLs, and the open controls are only rendered where they work.
func TestStateCarriesQueueTitlesAndCapability(t *testing.T) {
	ts, _, sp, _ := apiServer(t)
	if _, err := sp.Enqueue(queue.Job{
		URL: "https://youtu.be/A", Title: "Artista - Traccia", Settings: config.Defaults(),
	}); err != nil {
		t.Fatal(err)
	}

	_, body := getPath(t, ts, "/api/state")
	var st stateDTO
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Queue.Pending) != 1 || st.Queue.Pending[0].Title != "Artista - Traccia" {
		t.Errorf("queue row carries no title: %+v", st.Queue.Pending)
	}
	if st.CanOpen != jobs.CanOpen() {
		t.Errorf("canOpen = %v, want %v", st.CanOpen, jobs.CanOpen())
	}
}

// TestStateHistoryIsJustTheRecentRows: the full list moved to its own endpoint,
// so the payload every SSE tick carries no longer holds 25 records.
func TestStateHistoryIsJustTheRecentRows(t *testing.T) {
	ts, _, _, logDir := apiServer(t)
	now := time.Now().Add(-time.Hour)
	var js []logstore.Job
	for i := 0; i < 10; i++ {
		js = append(js, failedJob("https://youtu.be/x", now.Add(time.Duration(i)*time.Minute), "boom"))
	}
	seedHistory(t, logDir, js...)

	_, body := getPath(t, ts, "/api/state")
	var st stateDTO
	json.Unmarshal(body, &st)
	if len(st.History) != stateHistoryLimit {
		t.Errorf("state carries %d history rows, want %d", len(st.History), stateHistoryLimit)
	}
}

// TestSettingsRoundTripsTheNewKeys: both keys now have a control, which is why
// the handleSettings carry-through workaround could be deleted.
func TestSettingsRoundTripsTheNewKeys(t *testing.T) {
	srv, _, cfgPath := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var dto settingsDTO
	json.NewDecoder(resp.Body).Decode(&dto)
	resp.Body.Close()

	dto.JobTimeout = 900
	dto.OpenFolderOnDone = true
	buf, _ := json.Marshal(dto)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d: %s", putResp.StatusCode, body)
	}

	fp, _, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _ := config.Resolve(config.Partial{}, config.Partial{}, fp, config.Env{})
	if reloaded.JobTimeout != 900 {
		t.Errorf("job_timeout = %d, want 900", reloaded.JobTimeout)
	}
	if !reloaded.OpenFolderOnDone {
		t.Error("open_folder_on_done was not persisted")
	}
}

func TestSettingsRejectsNegativeJobTimeout(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var dto settingsDTO
	json.NewDecoder(resp.Body).Decode(&dto)
	resp.Body.Close()

	dto.JobTimeout = -1
	buf, _ := json.Marshal(dto)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", putResp.StatusCode)
	}
}

// ---- guards --------------------------------------------------------------

// TestNewEndpointsAreBehindTheGuards: every route added this cycle must sit
// behind the same ADR-0010 protections as the existing ones. A new route added
// outside guardAPI is the easiest way to lose them.
func TestNewEndpointsAreBehindTheGuards(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.deps.Token = "sekrit" // enable the token check, as production always does
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	routes := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/history"},
		{http.MethodGet, "/api/history/log?id=x"},
		{http.MethodPost, "/api/history/again"},
		{http.MethodPost, "/api/history/open"},
		{http.MethodPost, "/api/queue/cancel"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// No token at all.
			req, _ := http.NewRequest(rt.method, ts.URL+rt.path, strings.NewReader(`{"id":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("without a token: status = %d, want 401", resp.StatusCode)
			}

			// A cross-site Origin, with a valid token.
			req, _ = http.NewRequest(rt.method, ts.URL+rt.path, strings.NewReader(`{"id":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(tokenHeader, "sekrit")
			req.Header.Set("Origin", "https://evil.example")
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("cross-site Origin: status = %d, want 403", resp.StatusCode)
			}

			// A form-shaped POST (no preflight) with a valid token.
			if rt.method == http.MethodPost {
				req, _ = http.NewRequest(rt.method, ts.URL+rt.path, strings.NewReader(`{"id":"x"}`))
				req.Header.Set("Content-Type", "text/plain")
				req.Header.Set(tokenHeader, "sekrit")
				resp, err = http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusUnsupportedMediaType {
					t.Errorf("text/plain POST: status = %d, want 415", resp.StatusCode)
				}
			}
		})
	}
}

// ---- assets --------------------------------------------------------------

// TestAssetAllowListServesExactlyThreeFiles: the assets are served from an
// explicit map, not an http.FileServer over the embed.FS, so a file added to the
// directory later is not automatically published.
func TestAssetAllowListServesExactlyThreeFiles(t *testing.T) {
	ts, _, _, _ := apiServer(t)

	for _, path := range []string{"/", "/app.css", "/app.js"} {
		resp, body := getPath(t, ts, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Errorf("GET %s: empty body", path)
		}
	}
	for _, path := range []string{
		"/assets/app.js",        // the on-disk name, not the served one
		"/app.css/",             // trailing slash
		"/app.ts",               // near miss
		"/../assets/index.html", // traversal
		"/index.html",           // the shell has one URL, and it is "/"
	} {
		t.Run(path, func(t *testing.T) {
			resp, _ := getPath(t, ts, path)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s: status = %d, want 404", path, resp.StatusCode)
			}
		})
	}
}

// TestAssetsAreNotCached: an upgraded ytdl serves a new app.js against the same
// URL. A cached old script paired with a new API is a mix that only ever appears
// on a user's machine.
func TestAssetsAreNotCached(t *testing.T) {
	ts, _, _, _ := apiServer(t)
	for _, path := range []string{"/app.css", "/app.js"} {
		resp, _ := getPath(t, ts, path)
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q, want no-store", path, cc)
		}
	}
}

func TestAssetContentTypes(t *testing.T) {
	ts, _, _, _ := apiServer(t)
	want := map[string]string{"/app.css": "text/css", "/app.js": "application/javascript"}
	for path, prefix := range want {
		resp, _ := getPath(t, ts, path)
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, prefix) {
			t.Errorf("GET %s: Content-Type = %q, want %s…", path, ct, prefix)
		}
	}
}

// TestCSPForbidsInlineScript is the point of the split. The page renders
// user-controlled strings — titles, URLs, failure reasons — and this header is
// the backstop for a missed escape: with 'unsafe-inline' gone, an injected
// <script> does not run.
func TestCSPForbidsInlineScript(t *testing.T) {
	ts, _, _, _ := apiServer(t)
	resp, _ := getPath(t, ts, "/")
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy header")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP does not restrict scripts to same-origin files: %q", csp)
	}
	// The check that matters: 'unsafe-inline' must not appear in the script
	// directive. It legitimately remains under style-src.
	for _, directive := range strings.Split(csp, ";") {
		d := strings.TrimSpace(directive)
		if strings.HasPrefix(d, "script-src") && strings.Contains(d, "unsafe-inline") {
			t.Errorf("script-src still allows inline script: %q", d)
		}
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP no longer denies by default: %q", csp)
	}
}

// TestNewEndpointsRejectTheWrongMethod keeps a GET-shaped action (which a plain
// <img> or a link could trigger) from ever existing.
func TestNewEndpointsRejectTheWrongMethod(t *testing.T) {
	ts, _, _, _ := apiServer(t)
	for _, path := range []string{"/api/history/again", "/api/history/open", "/api/queue/cancel"} {
		t.Run(path, func(t *testing.T) {
			resp, _ := getPath(t, ts, path)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("GET %s: status = %d, want 405", path, resp.StatusCode)
			}
		})
	}
}
