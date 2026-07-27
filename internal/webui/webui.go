// Package webui serves ytdl's local web GUI (Cycle 3, roadmap phase 6): a
// self-contained single-page app plus a small JSON + SSE API over the SAME
// engine the CLI drives — the queue spool for jobs, the config file for
// settings, the log store for history. It is a front-end, not a reimplementation
// (ADR-0003): every download it starts is an ordinary spool job the daemon
// drains, and every setting it edits goes through config.Save (whitelist-only,
// never sourced).
//
// The server is meant to run INSIDE the queue daemon (cmd/ytdl's __daemon role):
// jobs it enqueues are claimed by the same process's drain loop, and live
// progress flows back through PublishProgress. HasClients is the "GUI connected"
// signal the daemon's exit test ANDs in (ADR-0008), keeping the daemon alive
// while a browser tab is open even if the queue is empty.
package webui

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// DefaultPort is the loopback port the GUI listens on unless $YTDL_GUI_PORT
// overrides it. It is not a config-file key on purpose: editing the GUI's own
// port from within the GUI would be circular, and keeping it out of Settings
// avoids touching the golden-parity surface.
const DefaultPort = 8765

// Deps are the engine seams the server needs, injected so tests can supply fakes
// and cmd/ytdl wires the production resolution (config precedence, spool, log
// store).
type Deps struct {
	Spool      *queue.Spool
	ConfigPath string
	// Resolve overlays the given flag layer onto env + config + defaults —
	// exactly cmd/ytdl's resolveWithFlags — so the GUI honours the same
	// precedence as the CLI.
	Resolve func(config.Partial) (config.Settings, []config.Warning)
	// LogDir / RetentionDays locate the durable history the GUI reads. They are
	// the FALLBACK only: every read re-resolves them through Resolve, because
	// they are user-editable in the very settings view this server serves.
	// Snapshotting them at construction meant that after saving a new log dir the
	// GUI answered "✓ salvate", wrote new records to the new dir, and went on
	// reading the old one for the daemon's whole life — so the user's new
	// downloads simply never appeared, while the CLI (which re-resolves per
	// invocation) showed them.
	LogDir        string
	RetentionDays int
	// DaemonRunning reports queue-daemon liveness for the status panel
	// (informational, per ADR-0008). Optional; nil is treated as "unknown/false".
	DaemonRunning func() bool
	// Token is the per-session secret that authorises API access. `ytdl gui`
	// reads it from a 0600 file and opens the page with ?t=<token>, which sets a
	// SameSite=Strict cookie; every /api/ route then requires it.
	//
	// It is what makes the local API safe. A loopback bind and a Host check stop
	// DNS rebinding but NOT ordinary CSRF: any page the user visits can POST to
	// http://127.0.0.1:<port>/ and the browser supplies the right Host itself.
	// Without a token, such a page could enqueue arbitrary yt-dlp arguments and
	// keep the daemon alive indefinitely. A SameSite=Strict cookie is not sent on
	// cross-site requests, and the token file is unreadable by other local users.
	//
	// An empty Token disables the check — for tests only; production always sets it.
	Token string
}

// Progress is one running job's live-progress snapshot, streamed to the browser
// over SSE. cmd/ytdl's per-job sink converts run.ProgressEvent into this DTO, so
// webui stays independent of the runtime layer.
type Progress struct {
	Status  string  `json:"status"`
	Percent float64 `json:"percent"`
	Speed   string  `json:"speed,omitempty"`
	ETA     string  `json:"eta,omitempty"`
	Title   string  `json:"title,omitempty"`
}

// Server is the web GUI handler set plus the SSE hub and the per-session
// output-dir override. It is safe for concurrent use.
type Server struct {
	deps Deps
	hub  *hub

	mu      sync.Mutex
	sessOut string // per-session output-dir override, lives with the daemon
}

// New builds a Server over the injected deps.
func New(d Deps) *Server { return &Server{deps: d, hub: newHub()} }

// Handler returns the GUI's HTTP handler: the SPA at "/", the JSON API under
// "/api/", and the SSE stream at "/api/events". Every request passes the loopback
// Host guard; every /api/ request additionally needs the session token and a
// same-origin-looking request (see guardAPI).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	api := http.NewServeMux()
	api.HandleFunc("/api/state", s.handleState)
	api.HandleFunc("/api/downloads", s.handleDownloads)
	api.HandleFunc("/api/events", s.handleEvents)
	api.HandleFunc("/api/settings", s.handleSettings)
	api.HandleFunc("/api/session", s.handleSession)
	// Cycle 5. Every one of these is addressed by ID — a record id or a spool
	// id — and never by path or URL; see handleHistoryOpen for why that is the
	// load-bearing property rather than a style choice.
	api.HandleFunc("/api/history", s.handleHistory)
	api.HandleFunc("/api/history/again", s.handleHistoryAgain)
	api.HandleFunc("/api/history/open", s.handleHistoryOpen)
	api.HandleFunc("/api/history/log", s.handleHistoryLog)
	api.HandleFunc("/api/queue/cancel", s.handleQueueCancel)
	mux.Handle("/api/", s.guardAPI(api))
	return s.securityHeaders(s.guardLocalHost(mux))
}

// tokenCookie is the name of the SameSite=Strict cookie carrying the session
// token once the page has been opened with ?t=<token>.
const tokenCookie = "ytdl_session"

// tokenHeader lets a non-browser client (curl, a test) present the token
// directly instead of via the cookie.
const tokenHeader = "X-Ytdl-Token"

// guardAPI authorises API access. Three independent layers, because each covers
// a case the others do not:
//
//  1. the session token (cookie or header) — the primary gate; a cross-site page
//     cannot read it, and another local user cannot read the 0600 token file;
//  2. Origin — rejected when present and not loopback, so a cross-site fetch is
//     refused even if a token ever leaked into a URL;
//  3. Content-Type: application/json on writes — this is what stops a plain
//     <form> or a text/plain fetch, the two CORS-"simple" shapes that get no
//     preflight and would otherwise reach the handler unannounced.
func (s *Server) guardAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !localOrigin(origin) {
			writeErr(w, http.StatusForbidden, "richiesta da un'origine esterna")
			return
		}
		if !s.tokenOK(r) {
			writeErr(w, http.StatusUnauthorized, "sessione non autorizzata: riapri l'interfaccia con `ytdl gui`")
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, "richiesto Content-Type: application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK reports whether the request carries the session token. An empty
// configured token disables the check (tests only).
func (s *Server) tokenOK(r *http.Request) bool {
	if s.deps.Token == "" {
		return true
	}
	if v := r.Header.Get(tokenHeader); v != "" {
		return s.tokenMatches(v)
	}
	if c, err := r.Cookie(tokenCookie); err == nil {
		return s.tokenMatches(c.Value)
	}
	return false
}

// tokenMatches compares a presented value with the session token in constant
// time, so a local attacker cannot recover it byte-by-byte through timing.
func (s *Server) tokenMatches(v string) bool {
	return s.deps.Token != "" &&
		subtle.ConstantTimeCompare([]byte(v), []byte(s.deps.Token)) == 1
}

// localOrigin reports whether an Origin header is one of ours.
func localOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return localHost(u.Host)
}

// MarkerHeader identifies a response as coming from a ytdl GUI. `ytdl gui` uses
// it to tell "our server is already up" from "some unrelated program holds the
// port", instead of assuming a successful TCP connect means the GUI is there.
const MarkerHeader = "X-Ytdl-Gui"

// securityHeaders locks the page down: it is entirely self-contained, so a CSP
// that forbids every external origin costs nothing and blocks a whole class of
// injection consequences.
//
// Cycle 5 moved the script out of the document, which lets script-src drop
// 'unsafe-inline' for 'self'. That is the difference between "an injected
// <script> would run" and "it would not": the page renders user-controlled
// strings (titles, URLs, failure reasons) and this is the backstop for a missed
// escape. style-src keeps 'unsafe-inline' because the markup still uses a couple
// of style attributes; 'self' is added for the now-external stylesheet.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(MarkerHeader, "1")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; style-src 'self' 'unsafe-inline'; script-src 'self'; "+
				"connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// HasClients reports whether any GUI client holds a live SSE connection — the
// "GUI connected" clause of the daemon's exit test (ADR-0008).
func (s *Server) HasClients() bool { return s.hub.clientCount() > 0 }

// PublishProgress forwards one running job's progress to every connected client,
// keyed by its spool id. cmd/ytdl builds the sink that calls this from the
// daemon's per-job runner.
func (s *Server) PublishProgress(id string, p Progress) {
	data, err := json.Marshal(progressPayload{ID: id, Progress: p})
	if err != nil {
		return
	}
	s.hub.broadcast(sseMsg{event: "progress", data: data})
}

// guardLocalHost rejects any request whose Host header is not loopback, the
// standard defence against DNS-rebinding: a malicious web page can point a
// hostname it controls at 127.0.0.1, but the browser then sends THAT hostname as
// Host, which this blocks. Same-origin requests from the GUI itself carry
// 127.0.0.1/localhost and pass.
func (s *Server) guardLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// localHost reports whether host (an HTTP Host header, possibly "name:port" or a
// bracketed IPv6 literal) is a loopback address. An empty host is allowed
// (HTTP/1.0 / test requests). net.SplitHostPort handles the :port and the [..]
// brackets; a value with no port falls through unchanged.
func localHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	// Host is case-insensitive and may carry a fully-qualified trailing dot.
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || h == "localhost" {
		// "" covers HTTP/1.0 and synthetic test requests; Go rejects an HTTP/1.1
		// request with no Host before it ever reaches a handler.
		return true
	}
	// Accept the whole loopback range (127.0.0.0/8, ::1, ::ffff:127.0.0.1), not
	// just 127.0.0.1, and reject anything that is not a literal IP.
	return net.ParseIP(h).IsLoopback()
}

func (s *Server) sessionOut() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessOut
}

func (s *Server) setSessionOut(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessOut = dir
}
