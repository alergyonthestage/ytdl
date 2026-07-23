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
	"encoding/json"
	"net"
	"net/http"
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
	// LogDir / RetentionDays locate the durable history the GUI reads. Taken from
	// the resolved settings at construction so history reads need no re-resolve.
	LogDir        string
	RetentionDays int
	// DaemonRunning reports queue-daemon liveness for the status panel
	// (informational, per ADR-0008). Optional; nil is treated as "unknown/false".
	DaemonRunning func() bool
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
// "/api/", and the SSE stream at "/api/events", all behind a loopback Host guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/downloads", s.handleDownloads)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/session", s.handleSession)
	return s.guardLocalHost(mux)
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
	switch h {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
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
