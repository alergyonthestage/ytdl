// Package config holds ytdl's settings, their built-in defaults, and the
// precedence resolution that overlays the config file, session, environment and
// CLI-flag layers onto those defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

	// Cycle 2A backend settings (logs, breadcrumb, notifications). None of these
	// are read by core.BuildArgs, so they never affect the yt-dlp argv or the
	// golden parity — they drive the runtime layer only.
	LogDir              string // default: StatePath()/logs ("" if $HOME unresolvable)
	LogRetentionDays    int    // default: 30 (0 = keep forever)
	BreadcrumbOnFailure bool   // default: true
	Notify              bool   // default: true
	NotifyOn            string // default: "both" (failure|success|both)
	NotifyForeground    bool   // default: false (notify only silent/background)
	NotifySound         bool   // default: true

	// Cycle 2B backend setting (queue + daemon). Like the 2A keys it is not read
	// by core.BuildArgs, so it never affects the yt-dlp argv or the golden parity;
	// it only bounds how many queued downloads the daemon runs at once.
	Concurrency int // default: 3; ConcurrencyUnlimited (0) = no cap (discouraged)

	// Cycle 2B-plus backend setting: a per-job wall-clock timeout in seconds for a
	// daemon-drained download. 0 (the default) means no limit — a hung yt-dlp is
	// only stopped by an explicit `ytdl cancel`. Not read by core.BuildArgs.
	JobTimeout int // default: 0 (no timeout); seconds

	// Cycle 5 UX setting: show the finished file in the file manager when a
	// FOREGROUND download succeeds (improvements.md U4). Off by default and
	// deliberately never applied to background/queued jobs — a Finder window
	// appearing by itself while the user is doing something else is hostile, and
	// a background completion already notifies (design §9.4). Not read by
	// core.BuildArgs.
	OpenFolderOnDone bool // default: false

	// Cycle 6-plus setting: may ytdl check, on its own, whether an update exists
	// (ADR-0016 §6). It is an outbound call on someone else's machine, so it gets
	// its own whitelist key — but it defaults to ON, because the person this
	// cycle exists for will never open a settings screen to switch it on, and a
	// default of off would deliver them nothing. The honesty cost of that default
	// is paid by the notice, which says the check exists and where to turn it off.
	//
	// It governs the AUTOMATIC probe only. A user who turns it off keeps the
	// manual check and `ytdl --update`: consent is about the machine phoning home
	// by itself, not about the user's right to ask. Not read by core.BuildArgs.
	UpdateCheck bool // default: true
}

// Defaults copied verbatim from the Bash ytdl (lines 31-41, 196).
const (
	DefaultFormat       = "mp3"
	DefaultAudioQuality = "0"

	// MaxAudioQuality is the worst end of yt-dlp's VBR scale: "a value between 0
	// (best) and 10 (worst)". AudioQualityList spells the whole domain out for
	// the surfaces that offer it as a choice.
	MaxAudioQuality  = 10
	AudioQualityList = "0-10"

	// DefaultLogRetentionDays is the central log store's age cap; 0 keeps forever.
	DefaultLogRetentionDays = 30
	// DefaultNotifyOn is the default set of completion events that notify.
	DefaultNotifyOn = "both"
	// DefaultConcurrency is the daemon's default parallel-download cap (roadmap
	// 4.6: "default cap 2–3"). ConcurrencyUnlimited removes the cap.
	DefaultConcurrency = 3
	// ConcurrencyUnlimited is the Concurrency value meaning "no cap", set via the
	// config keyword `unlimited`. It reintroduces the pre-2B unbounded background
	// behaviour (U5) and is deliberately discouraged.
	ConcurrencyUnlimited = 0
	// DefaultJobTimeout is the per-job wall-clock cap in seconds for daemon-drained
	// downloads; 0 (the default) disables it. Off by default so a legitimately long
	// download (a big playlist, a slow link) is never truncated unless the user
	// opts in (ADR-0011).
	DefaultJobTimeout = 0
	// ConcurrencyAdvisoryThreshold is the point above which a resolved concurrency
	// draws an advisory warning: many parallel downloads risk being throttled or
	// blocked by YouTube. It is only advice — there is no hard cap (a deliberate
	// design choice) — and sits well above the recommended 2–3 default so ordinary
	// settings stay quiet. The future config UI surfaces the same warning.
	ConcurrencyAdvisoryThreshold = 8
	// DefaultUpdateCheck is whether ytdl checks for its own updates unasked. On,
	// deliberately — see Settings.UpdateCheck for why the default carries the
	// cycle rather than the other way round (ADR-0016 §6).
	DefaultUpdateCheck = true

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

// validNotifyOnValues is the whitelist for the `notify_on` key.
var validNotifyOnValues = map[string]bool{"failure": true, "success": true, "both": true}

// ValidNotifyOn reports whether v is an accepted notify_on value.
func ValidNotifyOn(v string) bool { return validNotifyOnValues[v] }

// NotifyOnList is the human-readable notify_on list, for error messages.
const NotifyOnList = "failure|success|both"

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

		LogDir:              defaultLogDir(),
		LogRetentionDays:    DefaultLogRetentionDays,
		BreadcrumbOnFailure: true,
		Notify:              true,
		NotifyOn:            DefaultNotifyOn,
		NotifyForeground:    false,
		NotifySound:         true,

		Concurrency: DefaultConcurrency,
		JobTimeout:  DefaultJobTimeout,

		OpenFolderOnDone: false,
		UpdateCheck:      DefaultUpdateCheck,
	}
}

func defaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, "Music", "ytdl")
}

// defaultLogDir is the central log store: StatePath()/logs. If the state base
// cannot be determined ($HOME unresolvable and no $XDG_STATE_HOME) it is "",
// and the runner skips central logging (best-effort).
func defaultLogDir() string {
	base := StatePath()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "logs")
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

	LogDir              *string
	LogRetentionDays    *int
	BreadcrumbOnFailure *bool
	Notify              *bool
	NotifyOn            *string
	NotifyForeground    *bool
	NotifySound         *bool

	Concurrency *int
	JobTimeout  *int

	OpenFolderOnDone *bool
	UpdateCheck      *bool
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
	if p.LogDir != nil {
		s.LogDir = *p.LogDir
	}
	if p.LogRetentionDays != nil {
		s.LogRetentionDays = *p.LogRetentionDays
	}
	if p.BreadcrumbOnFailure != nil {
		s.BreadcrumbOnFailure = *p.BreadcrumbOnFailure
	}
	if p.Notify != nil {
		s.Notify = *p.Notify
	}
	if p.NotifyOn != nil {
		s.NotifyOn = *p.NotifyOn
	}
	if p.NotifyForeground != nil {
		s.NotifyForeground = *p.NotifyForeground
	}
	if p.NotifySound != nil {
		s.NotifySound = *p.NotifySound
	}
	if p.Concurrency != nil {
		s.Concurrency = *p.Concurrency
	}
	if p.JobTimeout != nil {
		s.JobTimeout = *p.JobTimeout
	}
	if p.OpenFolderOnDone != nil {
		s.OpenFolderOnDone = *p.OpenFolderOnDone
	}
	if p.UpdateCheck != nil {
		s.UpdateCheck = *p.UpdateCheck
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
	if !ValidAudioQuality(s.AudioQuality) {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved audio_quality %q is not %s", s.AudioQuality, AudioQualityList)})
	}
	if !ValidNotifyOn(s.NotifyOn) {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved notify_on %q is not one of %s", s.NotifyOn, NotifyOnList)})
	}
	if s.LogRetentionDays < 0 {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved log_retention_days %d is negative", s.LogRetentionDays)})
	}
	if s.Concurrency < 0 {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved concurrency %d is negative", s.Concurrency)})
	}
	if s.JobTimeout < 0 {
		w = append(w, Warning{Msg: fmt.Sprintf("resolved job_timeout %d is negative", s.JobTimeout)})
	}
	// Advisory only (no hard cap): warn when parallelism is aggressive enough to
	// risk YouTube throttling/blocking. Fires for `unlimited` and for values well
	// above the recommended default; the default (3) stays quiet.
	if s.Concurrency == ConcurrencyUnlimited {
		w = append(w, Warning{Msg: "concurrency 'unlimited': too many parallel downloads may be throttled or blocked by YouTube"})
	} else if s.Concurrency > ConcurrencyAdvisoryThreshold {
		w = append(w, Warning{Msg: fmt.Sprintf("concurrency %d is high: too many parallel downloads may be throttled or blocked by YouTube", s.Concurrency)})
	}
	return w
}

// ValidAudioQuality reports whether q is one of yt-dlp's VBR steps: an integer
// from 0 (best) to MaxAudioQuality (worst). The old check tested a SINGLE
// character, which silently put 10 — a real yt-dlp value — outside the domain
// (G7). yt-dlp also accepts a literal bitrate ("128K"); ytdl deliberately does
// not expose that, so the domain is the scale and nothing else.
//
// The canonical spelling is the only accepted one: strconv would take "+5",
// "-0" and "05", and the value is written back to the config file verbatim.
func ValidAudioQuality(q string) bool {
	n, err := strconv.Atoi(q)
	return err == nil && n >= 0 && n <= MaxAudioQuality && q == strconv.Itoa(n)
}
