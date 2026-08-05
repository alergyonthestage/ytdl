package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/core"
)

func TestParseBasicURL(t *testing.T) {
	p, err := Parse([]string{"https://youtu.be/x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Action != ActionRun || p.RunMode != core.ModeDefault || p.URL != "https://youtu.be/x" {
		t.Errorf("got %+v", p)
	}
	if p.OutputDir != nil || p.Format != nil || p.Playlist {
		t.Errorf("unexpected flags set: %+v", p)
	}
}

func TestParseFlags(t *testing.T) {
	p, err := Parse([]string{"-o", "/out", "-f", "flac", "-p", "https://youtu.be/x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.OutputDir == nil || *p.OutputDir != "/out" {
		t.Errorf("OutputDir = %v", p.OutputDir)
	}
	if p.Format == nil || *p.Format != "flac" {
		t.Errorf("Format = %v", p.Format)
	}
	if !p.Playlist {
		t.Errorf("Playlist not set")
	}
	// long forms (u.co is a URL-like placeholder: a bare word is now rejected, C4)
	p, _ = Parse([]string{"--output", "/o", "--format", "m4a", "--playlist", "u.co"})
	if *p.OutputDir != "/o" || *p.Format != "m4a" || !p.Playlist {
		t.Errorf("long forms: %+v", p)
	}
}

func TestParseModePriority(t *testing.T) {
	cases := []struct {
		args []string
		want core.Mode
	}{
		{[]string{"-n", "u.co"}, core.ModeDryRun},
		{[]string{"-b", "u.co"}, core.ModeBackground},
		{[]string{"-v", "u.co"}, core.ModeVerbose},
		{[]string{"-s", "u.co"}, core.ModeSilent},
		{[]string{"u.co"}, core.ModeDefault},
		// priority when several are set: dry > background > verbose > silent
		{[]string{"-n", "-b", "-v", "-s", "u.co"}, core.ModeDryRun},
		{[]string{"-b", "-v", "-s", "u.co"}, core.ModeBackground},
		{[]string{"-v", "-s", "u.co"}, core.ModeVerbose},
	}
	for _, c := range cases {
		p, err := Parse(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if p.RunMode != c.want {
			t.Errorf("%v: RunMode = %d, want %d", c.args, p.RunMode, c.want)
		}
	}
}

func TestParseShortCircuits(t *testing.T) {
	for _, c := range []struct {
		args []string
		want Action
	}{
		{[]string{"-h"}, ActionHelp},
		{[]string{"--help"}, ActionHelp},
		{[]string{"-V"}, ActionVersion},
		{[]string{"--version"}, ActionVersion},
		{[]string{"--update"}, ActionUpdate},
		// short-circuit fires even with no URL and even after other flags
		{[]string{"-f", "mp3", "-h"}, ActionHelp},
		{[]string{"--update", "ignored"}, ActionUpdate},
	} {
		p, err := Parse(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if p.Action != c.want {
			t.Errorf("%v: Action = %d, want %d", c.args, p.Action, c.want)
		}
	}
}

func TestParseDoubleDash(t *testing.T) {
	p, err := Parse([]string{"--", "-weird-url"})
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != "-weird-url" {
		t.Errorf("URL = %q, want -weird-url", p.URL)
	}
	// -- overwrites an earlier positional and ignores the remainder (first.co is
	// URL-like so it is accepted before -- replaces it; a bare word would be C4-rejected)
	p, _ = Parse([]string{"first.co", "--", "second", "third"})
	if p.URL != "second" {
		t.Errorf("URL = %q, want second", p.URL)
	}
}

func TestParsePostURLFlags(t *testing.T) {
	p, err := Parse([]string{"https://youtu.be/x", "-s"})
	if err != nil {
		t.Fatal(err)
	}
	if p.RunMode != core.ModeSilent {
		t.Errorf("post-URL flag not parsed: RunMode = %d", p.RunMode)
	}
}

func TestParseSubcommands(t *testing.T) {
	t.Run("queue", func(t *testing.T) {
		p, err := Parse([]string{"queue"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Action != ActionQueue || p.QueueWatch {
			t.Errorf("got %+v, want ActionQueue without watch", p)
		}
	})
	t.Run("queue --watch / -w", func(t *testing.T) {
		for _, a := range []string{"--watch", "-w"} {
			p, err := Parse([]string{"queue", a})
			if err != nil {
				t.Fatal(err)
			}
			if p.Action != ActionQueue || !p.QueueWatch {
				t.Errorf("%s: got %+v, want ActionQueue with watch", a, p)
			}
		}
	})
	t.Run("queue unknown option", func(t *testing.T) {
		_, err := Parse([]string{"queue", "--bogus"})
		var pe *ParseError
		if !errors.As(err, &pe) || !pe.Usage {
			t.Fatalf("queue --bogus: err = %v, want a usage ParseError", err)
		}
	})
	t.Run("status", func(t *testing.T) {
		p, err := Parse([]string{"status"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Action != ActionStatus {
			t.Errorf("got %+v, want ActionStatus", p)
		}
	})
	t.Run("status takes no args", func(t *testing.T) {
		_, err := Parse([]string{"status", "extra"})
		var pe *ParseError
		if !errors.As(err, &pe) || !pe.Usage {
			t.Fatalf("status extra: err = %v, want a usage ParseError", err)
		}
	})
	// `--` is the escape hatch for a URL literally named like a subcommand: it
	// routes to the download path, not the queue front-end.
	t.Run("-- escapes subcommand keywords", func(t *testing.T) {
		for _, kw := range []string{"queue", "status"} {
			p, err := Parse([]string{"--", kw})
			if err != nil {
				t.Fatalf("-- %s: %v", kw, err)
			}
			if p.Action != ActionRun || p.URL != kw {
				t.Errorf("-- %s: action=%d url=%q, want ActionRun url=%q", kw, p.Action, p.URL, kw)
			}
		}
	})
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		msg   string
		usage bool
	}{
		{"no url", []string{"-s"}, MsgNoURL, true},
		// NOTE: bare `ytdl` (nil args) is NOT here. Since Cycle 5 it prints the
		// short usage and exits 0 — see TestBareInvocationShowsShortUsage. Flags
		// without a URL, like the -s case above, are still an error.
		{"missing -o arg", []string{"-o"}, MsgMissingOutputDir, false},
		{"missing -f arg", []string{"-f"}, MsgMissingFormat, false},
		{"empty -o arg (${2:?} fires on null)", []string{"-o", "", "u"}, MsgMissingOutputDir, false},
		{"empty --output arg", []string{"--output", "", "u"}, MsgMissingOutputDir, false},
		{"empty -f arg (${2:?} fires on null)", []string{"-f", "", "u"}, MsgMissingFormat, false},
		{"empty --format arg", []string{"--format", "", "u"}, MsgMissingFormat, false},
		{"unknown flag", []string{"-z", "u"}, "✗ Opzione sconosciuta: -z", true},
		{"lone dash is unknown (Bash -* matches -)", []string{"-"}, "✗ Opzione sconosciuta: -", true},
		// C3: URL-like tokens so the too-many-args branch is what fires (a bare word
		// near a command would fire the Cycle-4 did-you-mean guard first instead).
		{"second positional (C3)", []string{"u1.co", "u2.co"}, fmt.Sprintf(MsgTooManyArguments, "u1.co"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.args)
			if err == nil {
				t.Fatal("expected an error")
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error is not a *ParseError: %v", err)
			}
			if c.msg != "" && pe.Msg != c.msg {
				t.Errorf("Msg = %q, want %q", pe.Msg, c.msg)
			}
			if pe.Usage != c.usage {
				t.Errorf("Usage = %v, want %v", pe.Usage, c.usage)
			}
		})
	}
}

// `ytdl gui` is a bare subcommand: it takes no options, because every setting
// lives inside the interface it opens.
func TestParseGUI(t *testing.T) {
	p, err := Parse([]string{"gui"})
	if err != nil {
		t.Fatalf("Parse(gui): %v", err)
	}
	if p.Action != ActionGUI {
		t.Errorf("Action = %v, want ActionGUI", p.Action)
	}
}

func TestParseGUIRejectsOptions(t *testing.T) {
	if _, err := Parse([]string{"gui", "--port", "9000"}); err == nil {
		t.Error("gui must reject unknown options")
	}
}

// The GUI must be discoverable from --help.
func TestUsageDocumentsGUI(t *testing.T) {
	if !strings.Contains(Usage, "ytdl gui") {
		t.Error("Usage does not document `ytdl gui`")
	}
}
