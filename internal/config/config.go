// Package config holds ytdl's settings, their built-in defaults, and the
// precedence resolution that overlays the config file, session, environment and
// CLI-flag layers onto those defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings is the fully-resolved configuration BuildArgs consumes. Every field
// maps to one whitelist key in the config file (see file.go).
type Settings struct {
	OutputDir       string // default: $HOME/Music/ytdl
	Format          string // default: "mp3"
	AudioQuality    string // default: "0"
	PlaylistDefault bool   // default: false (single track)
	NameTemplate    string // default: NameTemplate
	StripBrackets   string // default: StripBrackets
	StripTags       string // default: StripTags
	EmbedThumbnail  bool   // default: true
	EmbedMetadata   bool   // default: true
}

// Defaults copied verbatim from the Bash ytdl (lines 31-41, 196).
const (
	DefaultFormat       = "mp3"
	DefaultAudioQuality = "0"

	// NameTemplate is the filename template with yt-dlp first-present fallback
	// chains: artist -> creator -> xartist -> uploader, and track -> xtrack ->
	// title (ytdl line 196).
	NameTemplate = `%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s`

	// StripBrackets removes any [..] token, e.g. [BAS012], [Premiere] (ytdl line 37).
	StripBrackets = `\s*\[[^]]*\]`

	// StripTags removes redundant descriptors in parentheses, case-insensitively,
	// while preserving (Remix) and (feat. ...) (ytdl line 41).
	StripTags = `\s*\((?i:original mix|original|extended mix|extended|radio edit|radio version|free download|free dl|official (video|audio)|lyric video|visualizer|hd|hq|audio)\)`
)

// validFormats is the C1 whitelist enforced for -f and the config `format` key.
var validFormats = map[string]bool{
	"mp3": true, "flac": true, "m4a": true, "opus": true, "wav": true,
}

// ValidFormat reports whether f is one of the supported audio formats. Used
// fail-fast for the -f flag and drop-and-warn for the config `format` key.
func ValidFormat(f string) bool { return validFormats[f] }

// FormatList is the human-readable supported-format list, for error messages.
const FormatList = "mp3|flac|m4a|opus|wav"

// Defaults returns the built-in settings. The output directory is
// $HOME/Music/ytdl, matching the Bash tool; callers that need a placeholder
// (golden tests) overwrite OutputDir afterwards.
func Defaults() Settings {
	return Settings{
		OutputDir:       defaultOutputDir(),
		Format:          DefaultFormat,
		AudioQuality:    DefaultAudioQuality,
		PlaylistDefault: false,
		NameTemplate:    NameTemplate,
		StripBrackets:   StripBrackets,
		StripTags:       StripTags,
		EmbedThumbnail:  true,
		EmbedMetadata:   true,
	}
}

func defaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, "Music", "ytdl")
}

// Partial carries one optional value per Settings field. A nil pointer means
// "this layer says nothing about this field", so it falls through to the next
// lower-precedence layer.
type Partial struct {
	OutputDir       *string
	Format          *string
	AudioQuality    *string
	PlaylistDefault *bool
	NameTemplate    *string
	StripBrackets   *string
	StripTags       *string
	EmbedThumbnail  *bool
	EmbedMetadata   *bool
}

// Env is the environment layer. For parity with the Bash tool only
// $YTDL_OUT_DIR is honoured; a general YTDL_<KEY> scheme is out of scope.
type Env struct {
	OutDir string // value of $YTDL_OUT_DIR ("" if unset)
}

// Warning is a non-fatal problem found while building a layer (a malformed
// line, an unknown key, or an invalid value on a known key). Warnings are
// surfaced to the user; the offending input is ignored, never fatal.
type Warning struct {
	Line int // 1-based config-file line, or 0 if not file-related
	Msg  string
}

func (w Warning) String() string {
	if w.Line > 0 {
		return fmt.Sprintf("config line %d: %s", w.Line, w.Msg)
	}
	return w.Msg
}

// Resolve overlays the layers onto Defaults() low-to-high and returns the
// resolved settings. Precedence per field: flag > env > session > config file >
// built-in default. Each layer is expected to have been validated as it was
// built (LoadFile drops invalid values to nil with a warning; the flag layer
// validates fail-fast), so the returned warnings are only the defensive checks.
func Resolve(flags, session, file Partial, env Env) (Settings, []Warning) {
	s := Defaults()
	apply(&s, file)    // 1. config file
	apply(&s, session) // 2. session override (empty in Cycle 1)
	applyEnv(&s, env)  // 3. env: only YTDL_OUT_DIR
	apply(&s, flags)   // 4. CLI flags (win)

	// Trailing-slash strip on the final output dir, once, mirroring the Bash
	// tool's `OUTPUT_DIR="${OUTPUT_DIR%/}"` (line 169) — a single trailing "/".
	s.OutputDir = strings.TrimSuffix(s.OutputDir, "/")

	return s, validate(s)
}

// apply sets each field of s for which p provides a non-nil value.
func apply(s *Settings, p Partial) {
	if p.OutputDir != nil {
		s.OutputDir = *p.OutputDir
	}
	if p.Format != nil {
		s.Format = *p.Format
	}
	if p.AudioQuality != nil {
		s.AudioQuality = *p.AudioQuality
	}
	if p.PlaylistDefault != nil {
		s.PlaylistDefault = *p.PlaylistDefault
	}
	if p.NameTemplate != nil {
		s.NameTemplate = *p.NameTemplate
	}
	if p.StripBrackets != nil {
		s.StripBrackets = *p.StripBrackets
	}
	if p.StripTags != nil {
		s.StripTags = *p.StripTags
	}
	if p.EmbedThumbnail != nil {
		s.EmbedThumbnail = *p.EmbedThumbnail
	}
	if p.EmbedMetadata != nil {
		s.EmbedMetadata = *p.EmbedMetadata
	}
}

func applyEnv(s *Settings, env Env) {
	if env.OutDir != "" {
		s.OutputDir = env.OutDir
	}
}

// validate is the defensive final check. Because every layer validates as it is
// built, a warning here indicates a programming error rather than user input.
func validate(s Settings) []Warning {
	var w []Warning
	if !ValidFormat(s.Format) {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved format %q is not one of %s", s.Format, FormatList)})
	}
	if !validAudioQuality(s.AudioQuality) {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved audio_quality %q is not 0-9", s.AudioQuality)})
	}
	return w
}

func validAudioQuality(q string) bool {
	return len(q) == 1 && q[0] >= '0' && q[0] <= '9'
}
