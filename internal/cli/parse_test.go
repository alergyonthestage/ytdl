package cli

import (
	"errors"
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
	// long forms
	p, _ = Parse([]string{"--output", "/o", "--format", "m4a", "--playlist", "u"})
	if *p.OutputDir != "/o" || *p.Format != "m4a" || !p.Playlist {
		t.Errorf("long forms: %+v", p)
	}
}

func TestParseModePriority(t *testing.T) {
	cases := []struct {
		args []string
		want core.Mode
	}{
		{[]string{"-n", "u"}, core.ModeDryRun},
		{[]string{"-b", "u"}, core.ModeBackground},
		{[]string{"-v", "u"}, core.ModeVerbose},
		{[]string{"-s", "u"}, core.ModeSilent},
		{[]string{"u"}, core.ModeDefault},
		// priority when several are set: dry > background > verbose > silent
		{[]string{"-n", "-b", "-v", "-s", "u"}, core.ModeDryRun},
		{[]string{"-b", "-v", "-s", "u"}, core.ModeBackground},
		{[]string{"-v", "-s", "u"}, core.ModeVerbose},
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
	// -- overwrites an earlier positional and ignores the remainder
	p, _ = Parse([]string{"first", "--", "second", "third"})
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

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		msg   string
		usage bool
	}{
		{"no url", []string{"-s"}, MsgNoURL, true},
		{"empty args", nil, MsgNoURL, true},
		{"missing -o arg", []string{"-o"}, MsgMissingOutputDir, false},
		{"missing -f arg", []string{"-f"}, MsgMissingFormat, false},
		{"unknown flag", []string{"-z", "u"}, "✗ Opzione sconosciuta: -z", true},
		{"second positional (C3)", []string{"u1", "u2"}, "", true},
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
