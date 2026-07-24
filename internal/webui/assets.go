package webui

import _ "embed"

// indexHTML is the whole GUI: one self-contained page with inline CSS and JS and
// no external references, so it works with no network and nothing to provision
// (ADR-0003 — the GUI must add no runtime dependency). It is compiled into the
// binary, so there is nothing extra for the installer to place on disk.
//
//go:embed assets/index.html
var indexHTML []byte
