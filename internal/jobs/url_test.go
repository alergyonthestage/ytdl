package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// argvInjections are the strings that make this a security boundary rather than
// a nicety: core.BuildArgs appends the URL as the LAST argv element with no "--"
// separator, so anything starting with "-" reaches yt-dlp as an OPTION.
var argvInjections = []string{
	"--config-locations=/tmp/pwn.conf", // loads arbitrary options, including --exec
	"--update-to=attacker/yt-dlp@tag",  // replaces yt-dlp's own binary
	"--exec=touch /tmp/pwn",
	"-o/tmp/anywhere/%(title)s",
	"-",
	"--",
}

func TestValidateURLRejectsArgvInjection(t *testing.T) {
	for _, raw := range argvInjections {
		t.Run(raw, func(t *testing.T) {
			if _, err := ValidateURL(raw); !errors.Is(err, ErrBadURL) {
				t.Errorf("ValidateURL(%q) = %v, want ErrBadURL", raw, err)
			}
		})
	}
}

func TestValidateURLRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{
		"", "   ",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"javascript:alert(1)",
		"ytsearch:qualcosa", // valid for yt-dlp, but not something the GUI may enqueue
		"https://",          // no host
		"example.com/video", // scheme-less
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ValidateURL(raw); !errors.Is(err, ErrBadURL) {
				t.Errorf("ValidateURL(%q) = %v, want ErrBadURL", raw, err)
			}
		})
	}
}

func TestValidateURLAcceptsRealLinks(t *testing.T) {
	for _, raw := range []string{
		"https://youtu.be/dQw4w9WgXcQ",
		"http://youtube.com/watch?v=x&list=y",
		"https://music.youtube.com/playlist?list=OLAK5uy_x",
		"  https://youtu.be/x  ", // trimmed, not rejected
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := ValidateURL(raw)
			if err != nil {
				t.Fatalf("ValidateURL(%q) = %v, want it accepted", raw, err)
			}
			if got != strings.TrimSpace(raw) {
				t.Errorf("ValidateURL returned %q, want the trimmed input", got)
			}
		})
	}
}

// TestValidateURLMessagesAreUserFacing: both channels show these verbatim, so a
// Go package prefix must never appear in one.
func TestValidateURLMessagesAreUserFacing(t *testing.T) {
	for _, raw := range append(argvInjections, "", "file:///x") {
		_, err := ValidateURL(raw)
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "jobs:") {
			t.Errorf("ValidateURL(%q) error leaks a package prefix: %q", raw, err)
		}
	}
}

// TestAgainRefusesAnInjectedRecordURL is the review's CRITICAL, as a regression
// test: history.jsonl is untrusted input (design §7), and the "riscarica" path —
// reachable from the GUI's row button and from `ytdl again` — used to hand its
// URL to yt-dlp with no validation at all, while the new-download endpoint
// rejected the very same string. Nothing may be enqueued.
func TestAgainRefusesAnInjectedRecordURL(t *testing.T) {
	for _, raw := range argvInjections {
		t.Run(raw, func(t *testing.T) {
			sp := queue.Open(t.TempDir())
			e := logstore.Entry{
				Time: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
				URL:  raw, Format: "mp3", Success: false,
			}
			if _, err := Again(sp, e, settings()); !errors.Is(err, ErrBadURL) {
				t.Fatalf("Again(%q) error = %v, want ErrBadURL", raw, err)
			}
			snap, _ := sp.List()
			if len(snap.Pending) != 0 {
				t.Errorf("an injected URL was enqueued: %+v", snap.Pending[0].Job)
			}
		})
	}
}

// TestAgainAndNewDownloadAgreeOnWhatIsAcceptable: the asymmetry between the two
// enqueue paths WAS the bug. Whatever one refuses, the other must refuse.
func TestAgainAndNewDownloadAgreeOnWhatIsAcceptable(t *testing.T) {
	cases := append([]string{}, argvInjections...)
	cases = append(cases, "https://youtu.be/ok", "file:///etc/passwd", "")
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, directErr := ValidateURL(raw)

			sp := queue.Open(t.TempDir())
			e := logstore.Entry{Time: time.Now(), URL: raw, Format: "mp3"}
			_, againErr := Again(sp, e, settings())

			directRejected := directErr != nil
			againRejected := againErr != nil
			if directRejected != againRejected {
				t.Errorf("disagreement on %q: new-download rejected=%v, riscarica rejected=%v",
					raw, directRejected, againRejected)
			}
		})
	}
}
