// Package cli parses ytdl's command line, mirroring the Bash while/case parser
// (ytdl lines 146-166) with the two baked-in fixes C1 and C3, and owns all the
// parse-time user-facing text.
package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/core"
)

// DefaultHistoryLimit caps `ytdl history` when no --limit is given.
const DefaultHistoryLimit = 20

// Action is what the parsed command line asks ytdl to do. Help, Version and
// Update short-circuit during parsing (like the Bash `exit` inside the loop),
// before the URL is even required.
type Action int

const (
	ActionRun Action = iota
	ActionHelp
	ActionVersion
	ActionUpdate
	ActionQueue   // `ytdl queue [--watch]` — inspect the queue
	ActionStatus  // `ytdl status` — queue + daemon summary
	ActionHistory // `ytdl history [--failed] [--limit N]` — durable log
	ActionGUI     // `ytdl gui` — open the local web interface (Cycle 3)
	ActionCancel  // `ytdl cancel [<n>|<id>|--all]` — stop live work (Cycle 2B-plus)
	ActionRetry   // `ytdl retry [<n>|<id>|--all]` — re-queue a failed job (Cycle 2B-plus)
	ActionOpen    // `ytdl open [<n>|<id>] [--folder]` — open a downloaded file (Cycle 5)
	ActionAgain   // `ytdl again [<n>|<id>]` — download a history record again (Cycle 5)
	ActionConfig  // `ytdl config [--path]` — show the effective settings (Cycle 5)
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

	// History fields (valid when Action == ActionHistory).
	HistoryFailed bool   // --failed: only failures
	HistoryLimit  int    // --limit N: cap the rows (DefaultHistoryLimit if unset)
	HistorySearch string // --search Q: match title, link or saved file name
	HistoryIDs    bool   // --ids: show the stable record id on every row

	// Cancel/retry/open/again field (valid for those actions).
	Target string // the <n> index or <id-prefix>; "" with !All means "list them"
	All    bool   // --all: act on every matching job (cancel/retry only)

	// Cycle 5 fields.
	OpenFolder     bool // `open --folder`: show it in the file manager instead
	ConfigPathOnly bool // `config --path`: print only the config file path

	// Help fields (valid when Action == ActionHelp). Three shapes: the short
	// usage (both false/empty), the topic index (`ytdl help`), and one page
	// (`ytdl help <argomento>` or `ytdl <comando> --help`).
	HelpCommand bool   // invoked as `ytdl help …` rather than -h / no arguments
	HelpTopic   string // the requested page; "" = index (HelpCommand) or short usage
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
	// No arguments at all is someone finding out what the tool does, not a
	// misuse: show the short usage and exit cleanly. Flags WITHOUT a URL
	// (`ytdl -f mp3`) still fail with MsgNoURL further down — that one is a
	// genuine mistake and a script needs it to fail.
	if len(args) == 0 {
		return &Parsed{Action: ActionHelp}, nil
	}

	// Subcommand dispatch: a reserved first token routes to the queue front-end
	// instead of the download parser. A URL never collides with these keywords.
	// The hidden `__daemon` role is intercepted by main before Parse is reached.
	if len(args) > 0 {
		switch args[0] {
		case "help":
			return parseHelp(args[1:])
		case "queue":
			return parseQueue(args[1:])
		case "status":
			return parseStatus(args[1:])
		case "history":
			return parseHistory(args[1:])
		case "gui":
			return parseGUI(args[1:])
		case "cancel":
			return parseTargeted(ActionCancel, args[1:])
		case "retry":
			return parseTargeted(ActionRetry, args[1:])
		case "open":
			return parseOpen(args[1:])
		case "again":
			return parseAgain(args[1:])
		case "config":
			return parseConfig(args[1:])
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
			// A bare word (no `/ . :`) that is CLOSE to a known subcommand is almost
			// certainly a mistyped command (the reported UX bug: `ytdl queu` tried to
			// download "queu"): reject it with a "did you mean" hint. A bare word NOT
			// near any command is passed through to yt-dlp untouched — it may be a bare
			// YouTube video/playlist id, which the extractor accepts (an 11-char id like
			// dQw4w9WgXcQ resolves; verified against yt-dlp 2026.07.04), and rejecting
			// those would break a core use case. `ytdl -- <tok>` (handled above) forces
			// any token through as the URL. This is why the guard is command-similarity,
			// not a positive URL allow-list: a video id is neither URL-shaped nor a
			// command, yet must still reach yt-dlp.
			if !looksLikeURL(a) {
				if cmd := nearestCommand(a); cmd != "" {
					return nil, &ParseError{Msg: fmt.Sprintf(MsgDidYouMean, a, cmd, a), Usage: true}
				}
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

// parseHelp parses `help [argomento]`: with no argument the topic index, with
// one the page. An unknown topic is an ERROR rather than a silent fallback to
// the index, so it can suggest the nearest name — the same Levenshtein hint the
// Cycle 4 command guard gives, applied to help pages.
func parseHelp(rest []string) (*Parsed, error) {
	switch len(rest) {
	case 0:
		return &Parsed{Action: ActionHelp, HelpCommand: true}, nil
	case 1:
		topic := rest[0]
		if _, ok := LookupTopic(topic); !ok {
			if near := nearestTopic(topic); near != "" {
				return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownTopicNear, topic, near)}
			}
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownTopic, topic)}
		}
		return &Parsed{Action: ActionHelp, HelpCommand: true, HelpTopic: strings.ToLower(topic)}, nil
	default:
		return nil, &ParseError{Msg: MsgTooManyTopics}
	}
}

// wantsHelp reports whether a subcommand's arguments ask for its own help page.
// Every subcommand accepts -h/--help wherever it appears, so a user who has just
// been shown "ytdl <comando> --help" can type it without thinking about order.
func wantsHelp(rest []string) bool {
	for _, a := range rest {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// helpPage builds the parse result for one command's own help page.
func helpPage(command string) *Parsed {
	return &Parsed{Action: ActionHelp, HelpCommand: true, HelpTopic: command}
}

// parseQueue parses `queue [--watch]`. The only accepted option is --watch/-w;
// anything else is an unknown-option error (with usage), like the download parser.
func parseQueue(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("queue"), nil
	}
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
// parseGUI accepts `ytdl gui` with no options: the interface itself is where
// every setting lives, so the command only has to open it.
func parseGUI(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("gui"), nil
	}
	if len(rest) > 0 {
		return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, rest[0]), Usage: true}
	}
	return &Parsed{Action: ActionGUI}, nil
}

func parseStatus(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("status"), nil
	}
	if len(rest) > 0 {
		return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, rest[0]), Usage: true}
	}
	return &Parsed{Action: ActionStatus}, nil
}

// parseHistory parses `history [--failed] [--limit N] [--search Q]`. --failed
// filters to failures; --limit caps the rows (a non-negative integer); --search
// matches a substring of the title, the link or the saved file's name, mirroring
// the GUI's search box so the two channels find the same records. Unknown
// options error with usage, like the other subcommands.
func parseHistory(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("history"), nil
	}
	p := &Parsed{Action: ActionHistory, HistoryLimit: DefaultHistoryLimit}
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--failed":
			p.HistoryFailed = true
		case "--ids":
			p.HistoryIDs = true
		case "--search":
			// An empty query is rejected rather than treated as "no filter": the
			// user typed --search meaning to narrow, and silently listing
			// everything would look like the filter failed to work.
			if i+1 >= len(rest) || rest[i+1] == "" {
				return nil, &ParseError{Msg: MsgMissingSearch}
			}
			p.HistorySearch = rest[i+1]
			i++
		case "--limit":
			if i+1 >= len(rest) || rest[i+1] == "" {
				return nil, &ParseError{Msg: MsgMissingLimit}
			}
			n, err := strconv.Atoi(rest[i+1])
			if err != nil || n < 0 {
				return nil, &ParseError{Msg: fmt.Sprintf(MsgInvalidLimit, rest[i+1])}
			}
			p.HistoryLimit = n
			i++
		default:
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, rest[i]), Usage: true}
		}
	}
	return p, nil
}

// parseTargeted parses `cancel`/`retry [<n>|<id-prefix>|--all]`. It accepts at
// most one positional target and the --all flag, but not both. With neither, the
// command lists what it could act on (the caller decides), so an empty invocation
// is valid, not an error.
func parseTargeted(action Action, rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		if action == ActionCancel {
			return helpPage("cancel"), nil
		}
		return helpPage("retry"), nil
	}
	p := &Parsed{Action: action}
	for _, a := range rest {
		switch {
		case a == "--all":
			p.All = true
		case strings.HasPrefix(a, "-"):
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, a), Usage: true}
		case p.Target != "":
			return nil, &ParseError{Msg: MsgTooManyTargets, Usage: true}
		default:
			p.Target = a
		}
	}
	if p.All && p.Target != "" {
		return nil, &ParseError{Msg: MsgTargetAndAll, Usage: true}
	}
	return p, nil
}

// parseOpen parses `open [<n>|<id>] [--folder]`. The target grammar is the one
// cancel/retry already use — an index into the list the command prints, or an
// id-prefix — so the tool has ONE way to name a thing. --folder shows the file
// in the file manager instead of opening it. With no target the command prints
// the numbered history to read an index off, exactly as a bare `ytdl cancel`
// prints the queue.
//
// There is deliberately no --all: opening twenty files at once is not a thing
// anyone means to do, unlike cancelling twenty queued jobs.
func parseOpen(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("open"), nil
	}
	p := &Parsed{Action: ActionOpen}
	for _, a := range rest {
		switch {
		case a == "--folder":
			p.OpenFolder = true
		case strings.HasPrefix(a, "-"):
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, a), Usage: true}
		case p.Target != "":
			return nil, &ParseError{Msg: MsgTooManyTargets, Usage: true}
		default:
			p.Target = a
		}
	}
	return p, nil
}

// parseAgain parses `again [<n>|<id>]` — same target grammar, no options. Like
// open, no --all: "download all of these again" is a way to flood the queue by
// accident.
func parseAgain(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("again"), nil
	}
	p := &Parsed{Action: ActionAgain}
	for _, a := range rest {
		switch {
		case strings.HasPrefix(a, "-"):
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, a), Usage: true}
		case p.Target != "":
			return nil, &ParseError{Msg: MsgTooManyTargets, Usage: true}
		default:
			p.Target = a
		}
	}
	return p, nil
}

// parseConfig parses `config [--path]`. It is read-only by design (ADR-0013): a
// CLI write path would duplicate config.Save's validation surface for a task the
// GUI and a text editor already serve. --path prints only the file path, for
// scripts that want to open or back it up.
func parseConfig(rest []string) (*Parsed, error) {
	if wantsHelp(rest) {
		return helpPage("config"), nil
	}
	p := &Parsed{Action: ActionConfig}
	for _, a := range rest {
		switch a {
		case "--path":
			p.ConfigPathOnly = true
		default:
			return nil, &ParseError{Msg: fmt.Sprintf(MsgUnknownOption, a), Usage: true}
		}
	}
	return p, nil
}
