// Package notify sends best-effort desktop completion notifications (U6). It is
// open/closed for future platforms (e.g. notify-send on Linux); today the only
// real backend is macOS osascript and every other platform gets a no-op, so the
// same runner code path is exercised unchanged in the Linux dev container and
// CI. Delivery never blocks on user interaction and never propagates an error:
// a failed banner must not fail the download.
package notify

import (
	"os/exec"
	"runtime"
	"strings"
)

// Notification is one completion event to surface.
type Notification struct {
	Success bool   // outcome, so callers can gate on notify_on
	Title   string // banner heading (the runner uses "ytdl")
	Body    string // the message line (track name / failure detail)
	Sound   bool   // play the default alert sound
}

// Notifier delivers a Notification. Implementations must be best-effort.
type Notifier interface {
	Notify(n Notification)
}

// Default returns the platform notifier: osascript on macOS, a no-op elsewhere.
func Default() Notifier {
	if runtime.GOOS == "darwin" {
		return OsaNotifier{}
	}
	return NopNotifier{}
}

// NopNotifier drops every notification. Used off macOS and injectable in tests.
type NopNotifier struct{}

// Notify implements Notifier.
func (NopNotifier) Notify(Notification) {}

// OsaNotifier delivers via `osascript -e 'display notification ...'`.
type OsaNotifier struct{}

// Notify implements Notifier, ignoring any osascript failure (best-effort).
func (OsaNotifier) Notify(n Notification) {
	_ = exec.Command("osascript", "-e", osascript(n)).Run()
}

// osascript builds the AppleScript for n. Kept separate and pure so it is unit
// tested without executing osascript, which is absent off macOS.
func osascript(n Notification) string {
	s := `display notification "` + escapeAS(n.Body) + `" with title "` + escapeAS(n.Title) + `"`
	if n.Sound {
		s += ` sound name "Glass"`
	}
	return s
}

// escapeAS escapes a string for an AppleScript double-quoted literal: backslash
// and double-quote are escaped; CR/LF become spaces so a multi-line yt-dlp
// message cannot break the single-line -e script.
func escapeAS(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\r", " ").Replace(s)
}
