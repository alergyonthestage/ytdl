// Package core is the crown jewel: it builds the exact yt-dlp argument vectors
// the Bash ytdl produced, from resolved settings. It is pure — no I/O, no exec —
// and gated by the golden parity tests in args_test.go. The daemon (Phase 4) and
// web UI (Phase 6) import this same builder (ADR-0004).
package core

import "github.com/alergyonthestage/ytdl/internal/config"

// Mode is an execution mode that produces a yt-dlp argument vector. Help,
// Version and Update are CLI concerns handled before reaching the builder.
type Mode int

const (
	ModeDefault Mode = iota
	ModeDryRun
	ModeVerbose
	ModeSilent
	ModeBackground
)

// Options is the resolved input to the builder.
type Options struct {
	Mode     Mode
	URL      string
	Settings config.Settings
	// Playlist is the resolved playlist decision (the -p flag or the config
	// playlist_default), already collapsed to a single boolean.
	Playlist bool
	// TitleFile and SavedFile are the runtime temp-file paths yt-dlp writes its
	// --print-to-file output to. The runner creates them and injects the paths;
	// the builder only places them, keeping this package free of I/O.
	TitleFile string // silent mode: before_dl title, for the .log filename
	SavedFile string // silent + default modes: after_move filepaths
}

// silentBeforeDL is the before_dl template used ONLY in silent mode. It
// deliberately omits the xartist/xtrack helper fields that the filename template
// and the default-mode before_dl include — reusing the filename template here is
// a silent parity break (ytdl line 289; design-cycle1-core.md §4 hazard 1).
const silentBeforeDL = "before_dl:%(artist,creator,uploader)s - %(track,title)s"

// afterMove writes the final path of each moved file, one per line.
const afterMove = "after_move:%(filepath)s"

// BuildArgs returns the yt-dlp argument vector for the download-ish modes
// (default, dry-run, verbose, silent). Background does not call yt-dlp directly:
// it enqueues onto the spool (run.runBackground), and the daemon later drains the
// job in silent mode — so there is no separate background argv to build.
func BuildArgs(o Options) []string {
	s := o.Settings
	switch o.Mode {
	case ModeDryRun:
		// Preview names only: no base_args, no --no-simulate, --skip-download.
		var a []string
		a = append(a, "--no-warnings")
		a = append(a, metaArgs(s)...)
		a = append(a, playlistArgs(o.Playlist)...)
		a = append(a, "--skip-download")
		a = append(a, "--print", s.NameTemplate+"."+s.Format)
		return append(a, o.URL)

	case ModeVerbose:
		// Full output: audio extraction + meta + playlist + -o, no --no-simulate,
		// no print keys (ytdl lines 258-264).
		a := audioExtractArgs(s)
		a = append(a, metaArgs(s)...)
		a = append(a, playlistArgs(o.Playlist)...)
		a = append(a, "-o", outputTemplate(s))
		return append(a, o.URL)

	case ModeSilent:
		// base_args + quiet/no-progress + before_dl(simple) & after_move to files.
		a := baseArgs(s, o.Playlist)
		a = append(a, "--quiet", "--no-warnings", "--no-progress")
		a = append(a, "--print-to-file", silentBeforeDL, o.TitleFile)
		a = append(a, "--print-to-file", afterMove, o.SavedFile)
		return append(a, o.URL)

	case ModeDefault:
		// base_args + quiet/progress + before_dl(full) to stdout & after_move to file.
		a := baseArgs(s, o.Playlist)
		a = append(a, "--quiet", "--no-warnings", "--progress")
		a = append(a, "--print", "before_dl:  ♪ "+s.NameTemplate+"."+s.Format)
		a = append(a, "--print-to-file", afterMove, o.SavedFile)
		return append(a, o.URL)
	}
	return nil
}

// metaArgs is the metadata-normalization pipeline, shared by every download
// mode, in the contract's exact order (ytdl lines 206-212). Order is critical:
// clean title/track, split the title into the xartist/xtrack helper fields
// WITHOUT touching native artist/track, then build the meta_* tags with the same
// fallback chains as the filename.
func metaArgs(s config.Settings) []string {
	return []string{
		"--replace-in-metadata", "title,track", s.StripBrackets, "",
		"--replace-in-metadata", "title,track", s.StripTags, "",
		"--parse-metadata", "title:%(xartist)s - %(xtrack)s",
		"--parse-metadata", "%(artist,creator,xartist,uploader)s:%(meta_artist)s",
		"--parse-metadata", "%(track,xtrack,title)s:%(meta_title)s",
	}
}

// playlistArgs selects between single-track and whole-playlist behaviour. The
// -i (ignore errors) only applies to playlist mode (ytdl lines 215-219).
func playlistArgs(playlist bool) []string {
	if playlist {
		return []string{"--yes-playlist", "-i"}
	}
	return []string{"--no-playlist"}
}

// audioExtractArgs is the audio-extraction prefix shared by verbose, silent and
// default: -x, format, quality, then the embed toggles in metadata-before-
// thumbnail order (ytdl base_args / verbose block).
func audioExtractArgs(s config.Settings) []string {
	a := []string{"-x", "--audio-format", s.Format, "--audio-quality", s.AudioQuality}
	if s.EmbedMetadata {
		a = append(a, "--embed-metadata")
	}
	if s.EmbedThumbnail {
		a = append(a, "--embed-thumbnail")
	}
	return a
}

// baseArgs is shared by silent and default modes (ytdl lines 272-278). The
// --no-simulate is load-bearing: --print/--print-to-file with a before_dl key
// otherwise implies --simulate (no download).
func baseArgs(s config.Settings, playlist bool) []string {
	a := audioExtractArgs(s)
	a = append(a, "--no-simulate")
	a = append(a, metaArgs(s)...)
	a = append(a, playlistArgs(playlist)...)
	a = append(a, "-o", outputTemplate(s))
	return a
}

// outputTemplate is the -o value: OutputDir + "/" + NameTemplate + ".%(ext)s".
// Plain concatenation (not filepath.Join) — the template's %(...) tokens must
// pass through to yt-dlp untouched.
func outputTemplate(s config.Settings) string {
	return s.OutputDir + "/" + s.NameTemplate + ".%(ext)s"
}
