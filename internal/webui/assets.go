package webui

import (
	"embed"
	"net/http"
)

// assetFS holds the GUI's three static files. They are compiled into the binary,
// so there is nothing extra for the installer to place on disk and the page
// works with no network and nothing to provision (ADR-0003 — the GUI must add no
// runtime dependency).
//
// They were one inline blob until Cycle 5. The UI roughly triples in size this
// cycle, and a single file mixing markup, style and behaviour would have become
// unreviewable; splitting it also lets the CSP drop 'unsafe-inline' for scripts.
//
//go:embed assets/index.html assets/app.css assets/app.js
var assetFS embed.FS

// asset is one servable file. The set is an explicit ALLOW-LIST rather than a
// directory handler: an http.FileServer over an embed.FS would serve whatever
// happened to be added to the directory later, including a file added by
// accident. Three names in, three names out.
type asset struct {
	name        string
	contentType string
}

var assets = map[string]asset{
	"/app.css": {name: "assets/app.css", contentType: "text/css; charset=utf-8"},
	"/app.js":  {name: "assets/app.js", contentType: "application/javascript; charset=utf-8"},
}

// indexHTML is the shell document, read once at startup.
var indexHTML = mustAsset("assets/index.html")

func mustAsset(name string) []byte {
	b, err := assetFS.ReadFile(name)
	if err != nil {
		// Unreachable: the files are embedded at build time, so a failure here
		// means the binary itself is malformed.
		panic("webui: missing embedded asset " + name + ": " + err.Error())
	}
	return b
}

// serveAsset serves one allow-listed static file, or reports false when the path
// is not one of them (the caller then 404s).
//
// no-store matters more than it looks: an upgraded ytdl serves a new app.js
// against the same URL, and a cached old one paired with a new API is a mix that
// only ever appears on a user's machine, never in testing.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) bool {
	a, ok := assets[r.URL.Path]
	if !ok {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return true
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(mustAsset(a.name))
	return true
}
