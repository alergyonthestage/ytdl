// Command ytdl is a thin CLI front-end over the shared core: it parses the
// command line, resolves settings, checks dependencies, and dispatches to the
// runtime layer. It owns the process exit codes.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/alergyonthestage/ytdl/internal/cli"
	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/core"
	"github.com/alergyonthestage/ytdl/internal/run"
)

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
	// Ensure installer-provisioned tools are found even without a configured
	// PATH (Finder, an app, a never-reopened Terminal) — done first, like the
	// Bash tool, so it applies to every action.
	run.PrependLocalBin()

	parsed, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		var pe *cli.ParseError
		if errors.As(err, &pe) && pe.Usage {
			fmt.Fprintln(os.Stderr)
			fmt.Fprint(os.Stderr, cli.Usage)
		}
		return 1
	}

	switch parsed.Action {
	case cli.ActionHelp:
		fmt.Print(cli.Usage)
		return 0
	case cli.ActionVersion:
		return run.ShowVersion(os.Stdout)
	case cli.ActionUpdate:
		return run.Update()
	}

	// ActionRun. C1: a bad -f flag fails fast (unlike the config file, which
	// warns and falls through — the deliberate asymmetry of §2.4).
	if parsed.Format != nil && !config.ValidFormat(*parsed.Format) {
		fmt.Fprintf(os.Stderr, cli.MsgInvalidFormat+"\n", *parsed.Format, config.FormatList)
		return 1
	}

	settings, warns := resolveSettings(parsed)
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "! %s\n", w.String())
	}

	if err := run.CheckDeps(); err != nil {
		tool := err.Error()
		var de *run.DepError
		if errors.As(err, &de) {
			tool = de.Tool
		}
		fmt.Fprint(os.Stderr, run.MissingDepMessage(tool))
		return 1
	}

	o := core.Options{
		Mode:     parsed.RunMode,
		URL:      parsed.URL,
		Settings: settings,
		// Playlist mode is on via the -p flag or the persistent config default.
		Playlist: parsed.Playlist || settings.PlaylistDefault,
	}
	return run.Dispatch(o, os.Stdout, os.Stderr)
}

// resolveSettings builds the layer Partials and resolves them. The session layer
// is empty in Cycle 1; the env layer honours only $YTDL_OUT_DIR.
func resolveSettings(parsed *cli.Parsed) (config.Settings, []config.Warning) {
	flags := config.Partial{OutputDir: parsed.OutputDir, Format: parsed.Format}

	var file config.Partial
	var warns []config.Warning
	if path := config.ConfigPath(); path != "" {
		fp, fw, err := config.LoadFile(path)
		if err != nil {
			warns = append(warns, config.Warning{Msg: fmt.Sprintf("impossibile leggere il config %s: %v", path, err)})
		} else {
			file = fp
			warns = append(warns, fw...)
		}
	}

	env := config.Env{OutDir: os.Getenv("YTDL_OUT_DIR")}
	settings, rwarns := config.Resolve(flags, config.Partial{}, file, env)
	warns = append(warns, rwarns...)
	return settings, warns
}
