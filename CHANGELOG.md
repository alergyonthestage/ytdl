# Changelog

Notable changes to ytdl. The format follows [Keep a Changelog](https://keepachangelog.com/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

Nothing yet.

## [2.2.0] — 2026-08-23

**ytdl now notices its own updates, and the web interface can apply one without a
Terminal.** Applying an update was never the gap — `ytdl --update` has existed
since 1.0.0. The gap was that nothing *detected* one, and that the interface built
for people who do not open a Terminal had no update surface at all, not even a
version number. See
[ADR-0016](docs/maintainers/distribution/decisions/0016-cycle6plus-update-path.md).

Additive: no command line changes meaning, and the yt-dlp argument vector is
byte-identical (the golden parity tests are untouched). Two behaviour changes are
observable, both listed under *Changed*.

### Added

- **Update detection.** ytdl checks whether a newer version exists — at **startup
  only, never on a timer**, at most once every 24 h, and always off the critical
  path: every surface reads a cached verdict, so no probe is ever on a path a user
  is waiting on. A round that gets no answer never overwrites a complete one.
- **Three verdict states that never collapse into two** — *an update is
  available* · *up to date, stated with the date it was checked* · *not verified,
  stated as such*. A machine behind a captive portal is not a machine that is up
  to date. A failed probe is **silent**: no error, no red banner, nothing on
  stderr the user did not ask for.
- **The update notice** — two lines on stderr after a download, naming what would
  move, how to apply it, and the key that turns the check off. Silent when there
  is nothing to say.
- **`update_check`** (config key, default `true`) — consent for the automatic
  probe. It governs the machine phoning home *by itself*: with it off,
  `ytdl --update` and the interface's *Controlla ora* keep working. Editable from
  the interface.
- **The web interface's update surface** — a banner above every view, and a
  *Versione e aggiornamenti* block in Impostazioni with the installed versions,
  the verdict, *Controlla ora*, a table of what would change, and **Aggiorna**.
  The update starts only with an empty queue (re-checked server-side at the
  click), but the **news is never withheld**: a user with a full queue is told
  there is an update and told what to do about it.
- **Applying an update from the interface, with no Terminal.** The old daemon
  keeps its open inode and survives its own replacement, so it can report how the
  install went; it then hands the session token to the new binary and exits, and
  the page reloads **once** onto the new build. When the update did not replace
  ytdl itself — the common case now that the installer is idempotent — nothing
  restarts at all.
- **`ytdl --version` reports the whole install**: ytdl, yt-dlp and ffmpeg, each
  with its state (verified against this ytdl · a different version required · not
  verified · not installed by ytdl · version not recorded · not installed),
  followed by the update state. `ytdl status` gains the same state line.
- **A failure hint that names updating**, fired only when a download fails with
  the signature of an outdated extractor — so a stranded user gets the one thing
  that helps without having to understand any of the above.
- **`deps.conf`** — ytdl now declares which yt-dlp and which ffmpeg it drives.
  Which versions work with the fixed argv ytdl builds is a property of ytdl, not a
  user preference. One commit reaches every existing installation within a day,
  with no release and no new binary.
- **ffmpeg is checksum-verified**, which was not previously possible: pinning an
  exact build gives an immutable URL to attest. The sums are the maintainer's
  attestation of builds they fetched and tested — that guarantees *immutability*,
  not provenance.
- **The installer is idempotent.** It resolves what is required, compares each
  component against it, and skips what already matches, recording what it actually
  installed. The common update becomes seconds instead of minutes — which is what
  makes it reasonable to ask a non-technical user to sit through one. `--force`
  reinstalls regardless, which is what *Riprova* uses.

### Changed

- **The update notice and the foreign-dependency warning print on `stderr`**, after
  the command's own output. A script parsing ytdl's **stdout** is unaffected.
- **ffmpeg may now report itself as `non verificata`.** The pin is a *preference,
  not a precondition*: upstream publishes only its current build, so when the
  attested one has been withdrawn (**404/410 only**) the installer installs the
  current one and says the copy could not be verified, rather than leaving the user
  unable to install anything. Anything other than a withdrawal — including "could
  not ask" — still **aborts**. An unattested copy is left **out of the update
  comparison**, so it can never become a permanent phantom update.
- **ytdl drives its own copies of its dependencies**, resolved by absolute path and
  no longer subject to `$PATH` ordering. yt-dlp is now told where our ffmpeg is
  (`--ffmpeg-location`, appended *after* the frozen argv builder, and only when the
  copy is ours). A dependency resolved from `$PATH` is reported as *not installed by
  ytdl* — a different problem from being out of date, with a different remedy.
- **The daemon has one more reason to stay alive**: an installer it launched is
  still running. Only the process that launched it can record how it went, so
  closing the tab mid-update no longer leaves a run that blocks every later one.
  ([ADR-0008](docs/maintainers/engine/decisions/0008-daemon-lifecycle.md), amended.)
- **The "never reload the document" rule is narrowed, not dropped**: exactly one
  reload, in the update handover, where the document is stale by definition
  because the server that answers its next request is a different build.

There is no *Fixed* section: this is new work, and no released version carried
any of it. The eighteen defects two review passes found in this cycle's own
implementation were fixed before it shipped, and are recorded in
[improvements.md](docs/maintainers/improvements.md) — a changelog documents what changed for
users of a release, not what a branch did to itself.

## [2.1.0] — 2026-08-09

Everything built on top of the 2.0.0 engine, released in one cut: a download queue
drained by an on-demand daemon, a local web interface, cancel/retry, and two UX
passes that brought the CLI and the GUI onto the same words. Additive for valid
input — a 2.0.0 command line keeps working — with two deliberate behaviour changes:
`-b` now enqueues instead of re-execing itself into the background, and the failure
`.log` written next to the audio is replaced by the central log store plus an
opt-out breadcrumb.

The sections below are grouped by the development cycle that produced them, in the
order they were built.

Cycle 2A — the first slice of the backend work (logs + notifications). All of it
lives in the runtime layer and leaves the yt-dlp argument vector (and the golden
parity tests) byte-unchanged. See
[ADR-0006](docs/maintainers/engine/decisions/0006-cycle2a-logs-breadcrumbs-notifications.md).

### Added

- **Central log store** at `${XDG_STATE_HOME:-~/.local/state}/ytdl/logs/` — one
  record per download (every job), with age-based retention
  (`log_retention_days`, default 30; `0` keeps forever).
- **In-destination failure breadcrumb** — a readable `"<title> — FALLITO.<hash8>.log"`
  marker dropped next to the audio on failure and removed automatically on a later
  success. Keyed by `hash(normalised URL)` (or per playlist-item id), so it fixes
  the old title-collision hazard. On by default (`breadcrumb_on_failure`), scoped to
  silent/background downloads.
- **Completion notifications** (macOS `osascript`) — toggleable via `notify`,
  `notify_on` (`failure|success|both`), `notify_foreground`, `notify_sound`. On by
  default for silent/background, where completion is otherwise invisible; foreground
  is opt-in. Best-effort and no-op off macOS.
- Config keys backing the above: `log_dir`, `log_retention_days`,
  `breadcrumb_on_failure`, `notify`, `notify_on`, `notify_foreground`,
  `notify_sound`.

### Changed

- The always-on, title-derived `<title>.log` written next to the audio on
  silent-mode failure is **replaced** by the central log store (always) plus the
  opt-out breadcrumb — removing the title-collision and filename-fragility issues.

Cycle 2B-core — the queue + on-demand daemon slice of the backend work. Runtime
layer only; the yt-dlp argument vector and golden parity tests stay byte-unchanged.
See [ADR-0007](docs/maintainers/engine/decisions/0007-cycle2b-queue-daemon.md).

### Added

- **Download queue** — a lock-free filesystem spool at
  `${XDG_STATE_HOME:-~/.local/state}/ytdl/queue/{pending,running,done,failed}/` with
  atomic `mv` state transitions (maildir-style, no locks).
- **On-demand `ytdld` daemon** — the same `ytdl` binary self-exec'd into a hidden
  `__daemon` role, holding an exclusive `flock` (single instance), draining the
  queue under a bounded worker pool, recovering jobs stranded by a killed daemon,
  and idle-exiting when the queue empties. No always-on service.
- **`ytdl queue [--watch]`** and **`ytdl status`** — inspect pending/running/done/
  failed jobs and daemon liveness; `ytdl queue` also restarts a stalled daemon.
- **`concurrency` config key** — parallel-download cap (default 3; `unlimited` is
  allowed but discouraged, and an aggressive value draws an advisory warning that
  YouTube may throttle many parallel downloads).

### Changed

- **`-b`/`--background` now enqueues** onto the queue and lets the daemon run it
  under the concurrency cap, superseding the previous unbounded `Setsid` detach
  (five `ytdl -b` no longer start five simultaneous downloads). Queued downloads
  run in silent mode, so they still get the 2A log store, breadcrumb and
  notification.
- Help text: the stale `<titolo>.log` wording is corrected and the new `queue`/
  `status` commands are documented.

Cycle 2C — a UX pass over the CLI surface exposed by 2B-core. Runtime/CLI layer
only; the yt-dlp argument vector and the golden parity tests stay byte-unchanged.
See [ADR-0009](docs/maintainers/ux/decisions/0009-cycle2c-cli-ux.md) and the
[CLI reference](docs/maintainers/ux/design/cli-reference.md).

### Added

- **`ytdl history [--failed] [--limit N]`** — the durable download history, from a
  new structured `history.jsonl` in the log store: foreground *and* background
  downloads, newest first, by track title (records now carry the title).
- **TTY-aware colour** for `queue`/`status`/`history` (honours `NO_COLOR`; off when
  the output is piped).

### Changed

- **`ytdl queue`** shows only *live* work (pending + running) with an empty-state
  hint — no more lifetime completed/failed counts.
- **`ytdl queue --watch`** redraws **in place** (region redraw + spinner) instead of
  clearing the whole screen, **auto-exits** when the queue drains, and degrades to a
  single snapshot when the output is piped.
- **`ytdl status`** reports daemon liveness informationally and shows a **recent,
  windowed** outcome summary from the log store (which includes foreground
  downloads); every windowed count states its window ("ultimi N giorni").
- **`ytdl -n`** (preview) failures are surfaced legibly — YouTube's anti-bot
  challenge gets a clear hint — instead of dumping raw yt-dlp output.
- The queue's terminal state (`done/`/`failed/`) is now **pruned by age**
  (`log_retention_days`), fixing its previously unbounded growth; the durable
  history lives in the log store.

### Fixed

- History reading tolerates a pathologically long or corrupt line (it no longer
  loses newer records or blocks pruning), URLs/titles are truncated on rune
  boundaries (valid UTF-8), and colour never splices into a track title.

Cycle 3 — the web GUI (roadmap Phase 6, improvement E2): a browser interface for
non-developer users, served by the queue daemon. A front-end over the existing
engine (spool + config + log store), standard library only. `internal/core` stays
byte-unchanged (golden parity holds). See
[ADR-0010](docs/maintainers/ux/decisions/0010-cycle3-web-gui.md).

### Added

- **`ytdl gui`** — opens a local web interface in the browser: paste a link and
  start a download (format, playlist, per-run folder), watch **live progress**
  bars, see the queue and recent history, and edit every setting — all without the
  Terminal. The engine stays up while the page is open and closes itself when the
  page is closed and the queue is empty (ADR-0008); the page warns on close if work
  is still queued.
- **`internal/webui`** — the interface: one self-contained embedded page (no CDN,
  works offline) plus a small JSON + Server-Sent-Events API over the same spool,
  config file and log store the CLI uses.
- **Live per-job progress** — the queued runner captures yt-dlp progress from both
  of its output streams and streams it over SSE; parsed from yt-dlp's raw numeric
  fields, so it is immune to a user's `--color always`.
- **`config.Save`** — the settings editor persists changes atomically, whitelist
  keys only, validating before writing; it follows a symlinked config, preserves
  the file mode, and writes `$HOME`-relative paths so a saved config stays portable.
- **Per-session output directory** — settable from the GUI separately from the
  global default, alongside per-run (this download) and global (config) folders.
- `$YTDL_GUI_PORT` overrides the default loopback port (8765).

### Security

- The local API is **authenticated** with a per-session token (a `0600` file
  exchanged for a `SameSite=Strict` cookie), plus an `Origin` check, a mandatory
  `application/json` content type on writes, and `http`/`https`-only URL
  validation. Together these close a cross-site-request path that could otherwise
  reach yt-dlp's argument vector from any web page the user visited. The server
  binds `127.0.0.1` only and rejects non-loopback `Host` headers.
- The web server runs **only** in a GUI daemon (`ytdl gui`). A daemon spawned by
  `ytdl -b` stays headless and opens no socket — unchanged from before Cycle 3.

Cycle 2B-plus — queue completion (cancel/retry) + phase-5 hardening. Runtime/CLI
layer; `core.BuildArgs` and the golden parity tests stay byte-unchanged (the
residue redirect is appended off the golden path). See
[ADR-0011](docs/maintainers/engine/decisions/0011-cycle2b-plus-cancel-retry-hardening.md).

### Added

- **`ytdl cancel [<n> | <id> | --all]`** — stop live work. A pending job is
  removed; a running one is torn down (yt-dlp *and* its ffmpeg child, via a
  process-group kill) by the daemon. `<n>` is the index from the no-argument
  listing; `<id>` is a stable id-prefix (prefer it in scripts).
- **`ytdl retry [<n> | <id> | --all]`** — re-queue a failed download and resume the
  daemon to drain it. No argument lists the retryable failures.
- **`job_timeout`** config key (seconds; `0` = no limit = default) — a per-job
  wall-clock cap; a job that exceeds it is killed and marked failed.
- **Daemon diagnostics log** at `${XDG_STATE_HOME:-~/.local/state}/ytdl/daemon.log`
  — lifecycle, orphan recovery, cancel/timeout/panic kills and persistent spool
  errors, otherwise invisible for a detached daemon. Size-capped; `ytdl status`
  points at it once it exists.

### Changed

- **No residue on failure/cancel.** Every download mode now routes yt-dlp's
  intermediate files (`.part`, fragments, the embed thumbnail, pre-conversion
  audio) to a per-job scratch directory, so the destination only ever receives the
  final file on success — or the `.log` breadcrumb on a genuine failure. A cancel
  or a timeout leaves nothing behind and drops no breadcrumb.
- The dead `core.ReExecArgs` and the three `background-*` golden references (unused
  since `-b` began enqueuing in Cycle 2B-core) are removed.

Cycle 4 — a second CLI UX pass (command/URL disambiguation + job titles). Runtime/CLI
layer only; the yt-dlp argument vector, the golden parity tests, and the daemon
package are all unchanged. See
[ADR-0012](docs/maintainers/ux/decisions/0012-cycle4-cli-ux-title-disambiguation.md).

### Added

- **"Did you mean" for a mistyped command.** A bare first argument close to a known
  subcommand (`ytdl queu`) is now reported as a probable typo with a suggestion
  (`Forse intendevi «queue»?`), instead of being forwarded to yt-dlp as a URL and
  failing with an opaque error. A bare YouTube video/playlist id still downloads (the
  guard keys on command similarity, not URL shape), and `ytdl -- <token>` forces any
  token through as the URL.
- **Job titles in the queue views.** `ytdl queue`, `ytdl retry` and `ytdl cancel` now
  show the resolved `Artist - Track` title with the **full, untruncated URL** below
  it, so a job is identifiable. The title is captured while the job runs and travels
  with it into the failed list; `retry` also recovers a title from history for a job
  that failed before its metadata resolved.

### Changed

- The queue/retry/cancel lists show the **full URL** (previously a 40-character
  scheme-trimmed stub). In `ytdl queue --watch` each line is capped to the terminal
  width — measured in **display columns**, so CJK/emoji titles do not wrap and corrupt
  the in-place redraw.

Cycle 5 — a unified GUI/CLI UX pass, designed from the user's task model rather than
from the existing commands, so the two front-ends stop diverging. Runtime/CLI/GUI
layers only; the yt-dlp argument vector, the golden parity tests and the daemon
package are unchanged. See
[ADR-0013](docs/maintainers/ux/decisions/0013-cycle5-unified-ux.md),
[ADR-0014](docs/maintainers/ux/decisions/0014-ux-scope-model-and-partial-outcome.md) and the
normative [ux-principles.md](docs/maintainers/ux/design/ux-principles.md).

### Added

- **Three task-scoped GUI views** in the one never-reloading document — *Download*
  (a new download and the live queue side by side, plus the last downloads),
  *Cronologia* (filters, search, pagination, ranked row actions) and *Impostazioni*
  (five groups, advanced keys behind a disclosure, a sticky unsaved-changes bar).
- **The queue is actionable from the GUI** — a pending or running download can be
  cancelled from the browser, lifting ADR-0010's read-only decision.
- **Open and reveal in both channels** — `ytdl open <n>` and the GUI row actions
  open a finished download or the folder it landed in. The API takes a **history
  record id, never a path**, and revalidates it before acting.
- **`ytdl again`** — re-run a past download straight from the history listing.
- **`ytdl config`** — a read-only view of the settings actually in force, each row
  annotated with **where its value comes from** (flag, environment, file, default),
  which is what answers "I changed the folder and nothing happened".
- **Task-oriented help** — a short `ytdl --help` that fits a screen, `ytdl help
  <argomento>` for the detail, and per-command `--help`.
- **A numbered, actionable `ytdl history`** — title first, carrying the index that
  `open`, `again` and `retry` accept.
- **Richer history records** — `path`, `dir`, `count`, `playlist` and a capped
  `error`, so both channels can finally answer *where did the file go* and *why did
  it fail*. Every field is `omitempty` and both the record id and its `.log` name are
  derived rather than stored, so records written by earlier versions keep loading
  unchanged.
- **`open_folder_on_done` is honoured** — the long-documented key now opens the
  destination folder when a download finishes.
- **A failure says what to do next** — a remedy line derived at render time from the
  stored error, so it appears on records written before it existed, and produced by
  one function, so the CLI and the GUI use the same words.
- **Queue rows state their destination** — `ytdl queue` and both GUI queue rows show
  the folder frozen at enqueue time: what *this* job will do, not what the settings
  would do now.

### Changed

- **The reveal actions name the folder, not the Finder**, in the GUI, the CLI
  messages, the help and `ytdl config` — a launcher that is not always the Finder
  should not be described as one. "Finder" survives only in the macOS guides.
- **The per-download folder and the playlist checkbox are genuinely per-download** —
  both return to their configured default after a *successful* submit (a failed one
  keeps what was typed), and the checkbox follows a default just changed in
  *Impostazioni*.
- **The session folder override can no longer look applied when it is not** — the
  field carries a pending marker whenever it disagrees with the value the server
  reports as in force.
- **`open_folder_on_done` moved behind the Download group's advanced disclosure**,
  relabelled to name the channel it affects, and disabled *with a stated reason*
  where the platform has no launcher.
- The close-the-tab warning no longer implies that closing the browser cancels the
  queue: the daemon keeps draining it either way.
- The failure-log panel is scrolled into view and focused before its content loads,
  instead of opening off screen.

### Fixed

- **`audio_quality = 10` is accepted.** The domain is yt-dlp's real `0`–`10` scale,
  validated in one place shared by the config parser, the API and the GUI select;
  the previous check tested a single character, so `10` — a valid yt-dlp value — was
  rejected as malformed and a config carrying it lost the value on the first save.

## [2.0.0] — 2026-07-23

A clean break from the Bash `1.x` line: ytdl is now a single compiled Go binary
(one engine, thin front-ends), shipped through the same installer. Behaviour is at
parity with the Bash tool for valid inputs, verified by golden tests.

### Added

- **Go engine** — a reusable `internal/` core (config, yt-dlp arg builder, runner)
  behind a thin `cmd/ytdl` CLI, so the future daemon and web UI share one metadata
  pipeline ([ADR-0004](docs/maintainers/engine/decisions/0004-go-engine-package-layout.md)).
- **Config file** at `${XDG_CONFIG_HOME:-~/.config}/ytdl/config`, **parsed, never
  `source`d**, with a tolerant 9-key whitelist and precedence
  flag > env > config > default. Unknown/malformed/invalid entries warn and fall
  through — a newer GUI's config never bricks an older binary.
- **Golden parity tests** — the Go argument vector is byte-compared against
  references captured from a live run of the Bash `ytdl`.
- **Release CI** — build/test on push/PR; a `v*` tag cross-compiles the macOS
  `arm64` and `amd64` binaries and publishes `SHA2-256SUMS`.
- Small correctness fixes over the Bash tool: `-f` is validated up front (C1), a
  second URL is rejected instead of silently dropped (C3), and the failure-`.log`
  filename is sanitised (C5).

### Changed

- The **installer** downloads the compiled, checksum-verified `ytdl` binary instead
  of the raw Bash script.
- **Minimum macOS raised to 10.15 Catalina**; the installer collapses to a single
  provisioning path ([ADR-0005](docs/maintainers/foundation/decisions/0005-macos-floor-and-single-engine.md)).

### Fixed

- **Empty `-o`/`-f` argument fails fast** (exit 1), matching the Bash `${2:?}`
  guard, instead of silently accepting an empty output dir / format.
- **A signal-killed yt-dlp/ffmpeg now surfaces the shell's `128+signal` exit code**
  (e.g. 137 for an OOM-kill) rather than a generic `1`.
- An empty `output_dir` line in the config file now warns and falls through to the
  default instead of overriding it with an empty path.
- The installer strips the quarantine attribute from `yt-dlp` too, consistent with
  `ffmpeg` and `ytdl`.

### Removed

- The Python interpreter provisioning and the zipimport yt-dlp path for macOS
  10.13–10.14, and the evermeet ffmpeg source they used.

## [1.0.0]

- Bash `ytdl`: a yt-dlp wrapper producing `Artist - Track` filenames with clean ID3
  tags, the curl-based macOS installer with checksum verification, and
  `--version` / `--update`.
