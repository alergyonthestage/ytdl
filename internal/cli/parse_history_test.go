package cli

import (
	"errors"
	"testing"
)

func TestParseHistoryDefaults(t *testing.T) {
	p, err := Parse([]string{"history"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Action != ActionHistory {
		t.Fatalf("Action = %v, want ActionHistory", p.Action)
	}
	if p.HistoryLimit != DefaultHistoryLimit || p.HistoryFailed {
		t.Errorf("defaults wrong: limit=%d failed=%v", p.HistoryLimit, p.HistoryFailed)
	}
}

func TestParseHistoryFlags(t *testing.T) {
	p, err := Parse([]string{"history", "--failed", "--limit", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HistoryFailed || p.HistoryLimit != 5 {
		t.Errorf("got failed=%v limit=%d, want true/5", p.HistoryFailed, p.HistoryLimit)
	}
}

func TestParseHistoryErrors(t *testing.T) {
	cases := map[string][]string{
		"missing limit arg": {"history", "--limit"},
		"empty limit arg":   {"history", "--limit", ""},
		"non-numeric limit": {"history", "--limit", "x"},
		"negative limit":    {"history", "--limit", "-3"},
		"unknown option":    {"history", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(args); err == nil {
				t.Fatalf("Parse(%v) should have errored", args)
			} else {
				var pe *ParseError
				if !errors.As(err, &pe) {
					t.Errorf("want a *ParseError, got %T", err)
				}
			}
		})
	}
}
