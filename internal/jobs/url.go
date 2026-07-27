package jobs

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrBadURL classifies every ValidateURL rejection, so a caller can answer 400
// (GUI) or print a hint (CLI) with errors.Is while still showing the reason.
//
// Its text is Italian, unlike the rest of this package's errors: these strings
// are shown to the user verbatim by BOTH channels, so wrapping them in an
// English "jobs: …" prefix would put a package name in front of a message a
// person reads.
var ErrBadURL = errors.New("url non valido")

// badURL builds an ErrBadURL carrying a specific reason.
func badURL(reason string) error { return fmt.Errorf("%w: %s", ErrBadURL, reason) }

// ValidateURL rejects anything that is not a plain http(s) URL, and returns the
// trimmed value. This is a SECURITY BOUNDARY, not a nicety.
//
// core.BuildArgs appends the URL as the LAST argv element with no "--"
// separator, and is frozen byte-for-byte by the parity gate, so a value starting
// with "-" reaches yt-dlp as an OPTION. The two an attacker reaches for:
// --update-to=<repo>@<tag> makes yt-dlp replace its own binary from an arbitrary
// GitHub repo, and --config-locations=<file> loads arbitrary options (including
// --exec) from a planted file.
//
// It lives here, in the package both channels enqueue through, because it has to
// guard EVERY path onto that argv. It previously guarded only the GUI's new-download
// endpoint, which meant the Cycle 5 "riscarica" path — reachable from the GUI and
// from `ytdl again`, and fed by history.jsonl, a file the design treats as
// untrusted (§7) — reached yt-dlp unchecked. The same string was a 400 on one
// endpoint and a 202 on the other; that asymmetry was the bug.
//
// The interactive CLI download path is not exposed the same way (its flag parser
// rejects a leading "-" before a URL is ever assembled), but it costs nothing to
// hold the same rule everywhere.
func ValidateURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("%w: manca il link", ErrBadURL)
	}
	if strings.HasPrefix(v, "-") {
		return "", badURL("non può iniziare con '-'")
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", badURL("non è un indirizzo valido")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", badURL("sono ammessi solo http e https")
	}
	if u.Host == "" {
		return "", badURL("manca il dominio")
	}
	return v, nil
}
