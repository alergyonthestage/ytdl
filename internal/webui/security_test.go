package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

const testToken = "0123456789abcdef0123456789abcdef"

// newSecureServer is newTestServer with the session token enabled, i.e. the
// production configuration.
func newSecureServer(t *testing.T) (*Server, *queue.Spool) {
	t.Helper()
	dir := t.TempDir()
	sp := queue.Open(dir)
	if err := sp.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config")
	logDir := filepath.Join(dir, "logs")
	srv := New(Deps{
		Spool: sp, ConfigPath: cfgPath, Resolve: testResolve(cfgPath, logDir),
		LogDir: logDir, RetentionDays: 30,
		DaemonRunning: func() bool { return true },
		Token:         testToken,
	})
	return srv, sp
}

// THE attack the loopback bind and Host guard do NOT stop: a page the user
// visits posts to the real 127.0.0.1 URL, so the browser supplies a valid Host
// itself. Without a token this enqueued arbitrary yt-dlp arguments.
func TestAPIRequiresSessionToken(t *testing.T) {
	srv, sp := newSecureServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := `{"url":"https://youtu.be/attacker"}`
	resp, err := http.Post(ts.URL+"/api/downloads", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", resp.StatusCode)
	}
	snap, _ := sp.List()
	if p, _, _, _ := snap.Counts(); p != 0 {
		t.Errorf("an unauthorised request enqueued %d job(s)", p)
	}
}

// A cross-origin page must be refused even if it somehow held a token.
func TestAPIRejectsForeignOrigin(t *testing.T) {
	srv, _ := newSecureServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/state", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set(tokenHeader, testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

// text/plain and form encodings are the CORS-"simple" shapes that get no
// preflight; writes must refuse them so they can never reach a handler.
func TestAPIRejectsNonJSONWrites(t *testing.T) {
	srv, _ := newSecureServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, ct := range []string{"text/plain;charset=UTF-8", "application/x-www-form-urlencoded", ""} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/downloads",
			strings.NewReader(`{"url":"https://youtu.be/x"}`))
		req.Header.Set(tokenHeader, testToken)
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: status = %d, want 415", ct, resp.StatusCode)
		}
	}
}

// The page hands its token to the browser as a SameSite=Strict cookie, which is
// exactly what a cross-site request can never carry.
func TestIndexExchangesTokenForStrictCookie(t *testing.T) {
	srv, _ := newSecureServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/?t=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want a redirect that strips the token", resp.StatusCode)
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == tokenCookie {
			got = c
		}
	}
	if got == nil {
		t.Fatal("no session cookie set")
	}
	if got.SameSite != http.SameSiteStrictMode || !got.HttpOnly {
		t.Errorf("cookie must be HttpOnly + SameSite=Strict, got %+v", got)
	}
	// A wrong token must not mint a session.
	bad, err := client.Get(ts.URL + "/?t=wrong")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Body.Close()
	for _, c := range bad.Cookies() {
		if c.Name == tokenCookie {
			t.Error("a wrong token was accepted")
		}
	}
}

// core.BuildArgs appends the URL as the last argv element with no "--"
// separator and is frozen by the parity gate, so a "-" prefixed value would be
// parsed by yt-dlp as an OPTION — e.g. --update-to, which replaces its binary.
func TestDownloadsRejectsArgvInjection(t *testing.T) {
	srv, sp := newSecureServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	hostile := []string{
		"--update-to=attacker/yt-dlp@1.0",
		"--config-locations=/tmp/evil.conf",
		"-x",
		" --exec=touch /tmp/pwned",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"not a url",
	}
	for _, u := range hostile {
		body, _ := json.Marshal(map[string]string{"url": u})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/downloads", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(tokenHeader, testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("url %q: status = %d, want 400", u, resp.StatusCode)
		}
	}
	snap, _ := sp.List()
	if p, _, _, _ := snap.Counts(); p != 0 {
		t.Errorf("%d hostile url(s) reached the spool", p)
	}
}

// Settings that would silently destroy the user's library must be refused.
func TestValidateSettingsRejectsDestructiveValues(t *testing.T) {
	base := config.Defaults()
	base.OutputDir = "/tmp/out"
	cases := map[string]func(*config.Settings){
		"empty name template":      func(s *config.Settings) { s.NameTemplate = "" },
		"whitespace name template": func(s *config.Settings) { s.NameTemplate = "   " },
		"whitespace output dir":    func(s *config.Settings) { s.OutputDir = "   " },
		"empty output dir":         func(s *config.Settings) { s.OutputDir = "" },
		"bad format":               func(s *config.Settings) { s.Format = "mkv" },
		"bad notify_on":            func(s *config.Settings) { s.NotifyOn = "maybe" },
		"bad audio quality":        func(s *config.Settings) { s.AudioQuality = "12" },
		"negative retention":       func(s *config.Settings) { s.LogRetentionDays = -1 },
		"negative concurrency":     func(s *config.Settings) { s.Concurrency = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := base
			mutate(&s)
			if err := validateSettings(s); err == nil {
				t.Errorf("validateSettings accepted %s", name)
			}
		})
	}
	if err := validateSettings(base); err != nil {
		t.Errorf("valid settings rejected: %v", err)
	}
}
