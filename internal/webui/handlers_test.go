package webui

import (
	"bufio"
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
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// testResolve mimics cmd/ytdl's resolveWithFlags: config file (at cfgPath) under
// the flag layer, no env. It is what the GUI honours for precedence.
func testResolve(cfgPath string) func(config.Partial) (config.Settings, []config.Warning) {
	return func(flags config.Partial) (config.Settings, []config.Warning) {
		var file config.Partial
		var warns []config.Warning
		if fp, fw, err := config.LoadFile(cfgPath); err == nil {
			file, warns = fp, fw
		}
		s, rw := config.Resolve(flags, config.Partial{}, file, config.Env{})
		return s, append(warns, rw...)
	}
}

func newTestServer(t *testing.T) (*Server, *queue.Spool, string) {
	t.Helper()
	dir := t.TempDir()
	sp := queue.Open(dir)
	if err := sp.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config")
	srv := New(Deps{
		Spool:         sp,
		ConfigPath:    cfgPath,
		Resolve:       testResolve(cfgPath),
		LogDir:        filepath.Join(dir, "logs"),
		RetentionDays: 30,
		DaemonRunning: func() bool { return true },
	})
	return srv, sp, cfgPath
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// The GUI must ship as one self-contained page: no CDN, no external stylesheet
// or script, nothing to provision. Anything else would add a runtime dependency
// the installer does not satisfy (ADR-0003) and break offline use.
func TestIndexIsSelfContained(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "<title>ytdl</title>") {
		t.Error("index does not look like the GUI page")
	}
	// Since Cycle 5 the CSS and JS are separate files, but they are still served
	// BY THIS BINARY from the same origin. What must never appear is a reference
	// off the machine: a CDN, a protocol-relative URL, an @import.
	for _, bad := range []string{`src="http`, `href="http`, `src='http`, `@import`, `src="//`, `href="//`} {
		if strings.Contains(body, bad) {
			t.Errorf("index references an external resource (%q)", bad)
		}
	}
	// Every asset the page references must actually be served, or the GUI is a
	// blank document.
	for _, path := range []string{"/app.css", "/app.js"} {
		if !strings.Contains(body, path) {
			t.Errorf("index does not reference %s", path)
		}
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		asset, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, r.StatusCode)
		}
		if len(asset) == 0 {
			t.Errorf("GET %s served an empty body", path)
		}
	}
}

func TestUnknownPathIs404(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStateReflectsQueue(t *testing.T) {
	srv, sp, _ := newTestServer(t)
	s := config.Defaults()
	s.OutputDir = t.TempDir()
	if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/A", Settings: s}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state stateDTO
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Queue.Counts.Pending != 1 || len(state.Queue.Pending) != 1 {
		t.Fatalf("pending = %+v, want 1", state.Queue)
	}
	if state.Queue.Pending[0].URL != "https://youtu.be/A" {
		t.Errorf("URL = %q", state.Queue.Pending[0].URL)
	}
	if !state.DaemonRunning {
		t.Error("daemonRunning should be true")
	}
}

func TestDownloadsEnqueues(t *testing.T) {
	srv, sp, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := `{"url":"https://youtu.be/B","format":"flac","playlist":true}`
	resp, err := http.Post(ts.URL+"/api/downloads", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	snap, _ := sp.List()
	if p, _, _, _ := snap.Counts(); p != 1 {
		t.Fatalf("pending = %d, want 1", p)
	}
	job := snap.Pending[0].Job
	if job.URL != "https://youtu.be/B" || !job.Playlist || job.Settings.Format != "flac" {
		t.Errorf("enqueued job = %+v", job)
	}
}

func TestDownloadsRejectsBadInput(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := map[string]string{
		"missing url":    `{"url":""}`,
		"invalid format": `{"url":"https://x","format":"mkv"}`,
		"bad json":       `{not json`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+"/api/downloads", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	srv, _, cfgPath := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET the defaults, change a couple, PUT them back.
	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var dto settingsDTO
	json.NewDecoder(resp.Body).Decode(&dto)
	resp.Body.Close()

	dto.Format = "flac"
	dto.Concurrency = 5
	dto.NotifyOn = "failure"
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
	// The config file must now reload to the edited values.
	fp, _, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _ := config.Resolve(config.Partial{}, config.Partial{}, fp, config.Env{})
	if reloaded.Format != "flac" || reloaded.Concurrency != 5 || reloaded.NotifyOn != "failure" {
		t.Errorf("config not persisted: %+v", reloaded)
	}
}

// The editor rewrites the whole config file, so a save must not reset a
// CLI-set job_timeout to 0. Until Cycle 5 this was guaranteed by an explicit
// carry-through in handleSettings, because the DTO had no such field; now the
// DTO round-trips it like every other setting and the workaround is gone. The
// user-visible contract is identical, which is why this test did not change.
func TestSettingsSavePreservesJobTimeout(t *testing.T) {
	srv, _, cfgPath := newTestServer(t)
	if err := os.WriteFile(cfgPath, []byte("job_timeout = 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET then PUT, exactly as the settings view does: it always sends back the
	// whole document it was given.
	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var dto settingsDTO
	json.NewDecoder(resp.Body).Decode(&dto)
	resp.Body.Close()
	dto.Format = "flac"
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
	if reloaded.JobTimeout != 300 {
		t.Errorf("GUI save reset job_timeout to %d, want 300 preserved", reloaded.JobTimeout)
	}
	if reloaded.Format != "flac" {
		t.Errorf("GUI edit not saved: format = %q", reloaded.Format)
	}
}

func TestSettingsRejectsInvalid(t *testing.T) {
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
	dto.Format = "mkv" // invalid

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

func TestSessionOverrideUsedForDownloads(t *testing.T) {
	srv, sp, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	sessDir := t.TempDir()
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/session", strings.NewReader(`{"outputDir":"`+sessDir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()

	// A download without an explicit outputDir must inherit the session override.
	resp, err := http.Post(ts.URL+"/api/downloads", "application/json", strings.NewReader(`{"url":"https://x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	snap, _ := sp.List()
	if len(snap.Pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(snap.Pending))
	}
	if got := snap.Pending[0].Job.Settings.OutputDir; got != sessDir {
		t.Errorf("output dir = %q, want session override %q", got, sessDir)
	}
}

func TestHostGuardBlocksNonLoopback(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/state", nil)
	req.Host = "evil.example.com" // DNS-rebinding style
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestLocalHost(t *testing.T) {
	ok := []string{"", "localhost", "127.0.0.1", "127.0.0.1:8765", "localhost:8765", "::1", "[::1]:8765"}
	bad := []string{"evil.example.com", "example.com:8765", "0.0.0.0", "192.168.1.5:8765"}
	for _, h := range ok {
		if !localHost(h) {
			t.Errorf("localHost(%q) = false, want true", h)
		}
	}
	for _, h := range bad {
		if localHost(h) {
			t.Errorf("localHost(%q) = true, want false", h)
		}
	}
}

func TestSSEProgressAndHasClients(t *testing.T) {
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	if srv.HasClients() {
		t.Fatal("HasClients should be false before any connection")
	}
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	waitFor(t, srv.HasClients, time.Second, "an SSE client to register")

	// A published progress event must reach the stream.
	srv.PublishProgress("job1", Progress{Status: "downloading", Percent: 42, Speed: "1MiB/s"})

	line := readUntil(t, resp.Body, `"id":"job1"`, 2*time.Second)
	if !strings.Contains(line, `"percent":42`) {
		t.Errorf("progress line missing percent: %q", line)
	}

	// Closing the stream drops the client → HasClients goes false.
	resp.Body.Close()
	waitFor(t, func() bool { return !srv.HasClients() }, time.Second, "the SSE client to deregister")
}

// readUntil scans body line-by-line until one contains marker, or fails on
// timeout. The reader goroutine ends when body is closed.
func readUntil(t *testing.T, body io.Reader, marker string, timeout time.Duration) string {
	t.Helper()
	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %q", marker)
			return ""
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream ended before %q", marker)
				return ""
			}
			if strings.Contains(line, marker) {
				return line
			}
		}
	}
}
