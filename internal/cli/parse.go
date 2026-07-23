// Package cli parses ytdl's command line, mirroring the Bash while/case parser
// (ytdl lines 146-166) with the two baked-in fixes C1 and C3, and owns all the
// parse-time user-facing text.
package cli

import (
	"fmt"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/core"
)

// Action is what the parsed command line asks ytdl to do. Help, Version and
// Update short-circuit during parsing (like the Bash `exit` inside the loop),
// before the URL is even required.
type Action int

const (
	ActionRun Action = iota
	ActionHelp
	ActionVersion
	ActionUpdate
	ActionQueue  // `ytdl queue [--watch]` — inspect the queue
	ActionStatus // `ytdl status` — queue + daemon summary
)

// Parsed is the result of parsing. When Action == ActionRun, RunMode, URL and
// the flag pointers describe the download; for ActionQueue only QueueWatch is
// meaningful; otherwise only Action matters.
type Parsed struct {
	Action     Action
	RunMode    core.Mode // valid when Action == ActionRun
	URL        string
	OutputDir  *string // -o/--output, nil if not given
	Format     *string // -f/--format, nil if not given
	Playlist   bool    // -p/--playlist seen
	QueueWatch bool    // `queue --watch`: redraw on an interval
}

// ParseError is a user-facing parse failure. Usage requests that the help text
// be shown alongside the message (matching the Bash error paths).
type ParseError struct {
	Msg   string
	Usage bool
}

func (e *ParseError) Error() string { return e.Msg }

// Parse walks args left-to-right exactly like the Bash parser: -o/-f consume the
// next token (error if absent OR empty); -h/-V/--update short-circuit immediately;
// `--` takes the next single token as the URL and stops; an unknown -flag errors; a
// positional is the URL. Post-URL flags still parse. C3: a second positional is
// rejected rather than silently overwriting the first.
//
// An empty argument to -o/-f is rejected exactly like a missing one, matching the
// Bash `${2:?msg}` (colon) form, which fires on both "unset" and "null" — so
// `ytdl -o "" URL` (e.g. an unset shell variable) fails fast with exit 1 rather
// than silently resolving an empty output dir.
func Parse(args []string) (*Parsed, error) {
	// Subcommand dispatch: a reserved first token routes to the queue front-end
	// instead of the download parser. A URL never collides with these keywords.
	// The hidden `__daemon` role is intercepted by main before Parse is reached.
	if len(args) > 0 {
		switch args[0] {
		case "queue":
			return parseQueue(args[1:])
		case "status":
			return parseStatus(args[1:])
		}
	}

	p := &Parsed{}
	var dry, background, verbose, silent, haveURL bool

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-o", "--output":
			if i+1 >= len(args) || args[i+1] == "" {
				return nil, &ParseError{Msg: MsgMissingOutputDir}
			}
			v := args[i+1]
			p.OutputDir = &v
			i += 2
		case "-f", "--format":
			if i+1 >= len(args) || args[i+1] == "" {
				return nil, &ParseError{Msg: MsgMissingFormat}
			}
			v := args[i+1]
			p.Format = &v
			i += 2
		case "-p", "--playlist":
			p.Playlist = true
			i++
		case "-n", "--dry-run":
			dry = true
			i++
		case "-s", "--silent":
			silent = true
			i++
		case "-b", "--background":
			background = true
			i++
		case "-v", "--verbose":
			verbose = true
			i++
		case "-h", "--help":
			p.Action = ActionHelp
			return p, nil
		case "-V", "--version":
			p.Action = ActionVersion
			return p, nil
		case "--update":
			p.Action = ActionUpdate
			return p, nil
		case "--":
			// The next single token is the URL; the remainder is ignored (parity
			// with `--) shift; URL="${1:-}"; break`). A bare `--` leaves URL empty.
			if i+1 < len(args) {
				p.URL = args[i+1]
				haveURL = true
			}
			i = len(args)
		default:
			// A lone "-" is an unknown option, not a URL: the Bash `case -*)`
			// glob matches a bare "-" too (the `*` matches zero characters).
			if strings.HasPrefix(a, "-") {
				return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, a), Usage: true}
			}
			if haveURL {
				return nil, &ParseError{Msg: fmt.Sprintf(MsgTooManyArguments, p.URL), Usage: true}
			}
			p.URL = a
			haveURL = true
			i++
		}
	}

	if !haveURL || p.URL == "" {
		return nil, &ParseError{Msg: MsgNoURL, Usage: true}
	}

	// Runtime mode priority, matching the Bash dispatch order (dry-run is checked
	// first, then background, verbose, silent, else default). When several mode
	// flags are set, the highest-priority one runs.
	switch {
	case dry:
		p.RunMode = core.ModeDryRun
	case background:
		p.RunMode = core.ModeBackground
	case verbose:
		p.RunMode = core.ModeVerbose
	case silent:
		p.RunMode = core.ModeSilent
	default:
		p.RunMode = core.ModeDefault
	}
	p.Action = ActionRun
	return p, nil
}

// parseQueue parses `queue [--watch]`. The only accepted option is --watch/-w;
// anything else is an unknown-option error (with usage), like the download parser.
func parseQueue(rest []string) (*Parsed, error) {
	p := &Parsed{Action: ActionQueue}
	for _, a := range rest {
		switch a {
		case "--watch", "-w":
			p.QueueWatch = true
		default:
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, a), Usage: true}
		}
	}
	return p, nil
}

// parseStatus parses `status`, which takes no arguments in 2B-core.
func parseStatus(rest []string) (*Parsed, error) {
	if len(rest) > 0 {
		return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, rest[0]), Usage: true}
	}
	return &Parsed{Action: ActionStatus}, nil
}
