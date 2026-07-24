package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestLooksLikeURL(t *testing.T) {
	for _, s := range []string{
		"https://youtu.be/x", "http://x/y", "youtube.com/watch?v=x",
		"youtube.com", "youtu.be/x", "ytsearch:foo bar", "u.co", "/local/path",
	} {
		if !looksLikeURL(s) {
			t.Errorf("looksLikeURL(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"queu", "status", "xyzzy", "u", "download", ""} {
		if looksLikeURL(s) {
			t.Errorf("looksLikeURL(%q) = true, want false (bare word)", s)
		}
	}
}

func TestNearestCommand(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"queu", "queue"},
		{"staus", "status"},
		{"histry", "history"},
		{"cancl", "cancel"},
		{"retyr", "retry"},
		{"guii", "gui"},
		{"Queue", "queue"}, // case-insensitive
		{"xyzzy", ""},      // nothing close
		{"jobs", ""},
		{"q", ""}, // a single char is not close enough to anything
	} {
		if got := nearestCommand(c.in); got != c.want {
			t.Errorf("nearestCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A mistyped subcommand is rejected with a "did you mean" hint, not forwarded to
// yt-dlp as a URL (the reported UX bug: `ytdl queu` tried to download "queu").
func TestParseBareWordSuggestsCommand(t *testing.T) {
	_, err := Parse([]string{"queu"})
	var pe *ParseError
	if !errors.As(err, &pe) || !pe.Usage {
		t.Fatalf("Parse([queu]) err = %v, want a usage ParseError", err)
	}
	if !strings.Contains(pe.Msg, "queue") {
		t.Errorf("message %q does not suggest 'queue'", pe.Msg)
	}
}

// A bare word close to nothing gets the generic not-a-command-or-URL message.
func TestParseBareWordNoSuggestion(t *testing.T) {
	_, err := Parse([]string{"xyzzy"})
	var pe *ParseError
	if !errors.As(err, &pe) || !pe.Usage {
		t.Fatalf("Parse([xyzzy]) err = %v, want a usage ParseError", err)
	}
	if !strings.Contains(pe.Msg, "URL") {
		t.Errorf("message %q should explain it is not a URL", pe.Msg)
	}
}

// Scheme-less URLs and search prefixes must still be accepted as downloads — the
// whole point of not requiring an http:// prefix.
func TestParseAcceptsURLishForms(t *testing.T) {
	for _, u := range []string{"youtube.com/watch?v=x", "youtu.be/x", "ytsearch:foo", "https://x/y"} {
		p, err := Parse([]string{u})
		if err != nil {
			t.Fatalf("Parse([%q]) = %v, want ActionRun", u, err)
		}
		if p.Action != ActionRun || p.URL != u {
			t.Errorf("Parse([%q]) = %+v, want ActionRun with URL preserved", u, p)
		}
	}
}

// The bare-word guard also fires when the URL slot follows flags.
func TestParseBareWordAfterFlags(t *testing.T) {
	if _, err := Parse([]string{"-f", "mp3", "queu"}); err == nil {
		t.Fatal("Parse([-f mp3 queu]) = nil error, want the bare-word guard to fire")
	}
}

// `--` forces a bare token through as the URL: the documented escape hatch.
func TestParseDoubleDashForcesBareWord(t *testing.T) {
	p, err := Parse([]string{"--", "queu"})
	if err != nil {
		t.Fatalf("Parse([-- queu]) = %v, want ActionRun", err)
	}
	if p.Action != ActionRun || p.URL != "queu" {
		t.Errorf("Parse([-- queu]) = %+v, want ActionRun URL=queu", p)
	}
}
