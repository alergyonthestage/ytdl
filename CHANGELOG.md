# Changelog

Notable changes to ytdl. The format follows [Keep a Changelog](https://keepachangelog.com/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

Cycle 2A — the first slice of the backend work (logs + notifications). All of it
lives in the runtime layer and leaves the yt-dlp argument vector (and the golden
parity tests) byte-unchanged. See
[ADR-0006](docs/decisions/0006-cycle2a-logs-breadcrumbs-notifications.md).

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
See [ADR-0007](docs/decisions/0007-cycle2b-queue-daemon.md).

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
See [ADR-0009](docs/decisions/0009-cycle2c-cli-ux.md) and the
[CLI reference](docs/cli-reference.md).

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
[ADR-0010](docs/decisions/0010-cycle3-web-gui.md).

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

## [2.0.0] — 2026-07-23

A clean break from the Bash `1.x` line: ytdl is now a single compiled Go binary
(one engine, thin front-ends), shipped through the same installer. Behaviour is at
parity with the Bash tool for valid inputs, verified by golden tests.

### Added

- **Go engine** — a reusable `internal/` core (config, yt-dlp arg builder, runner)
  behind a thin `cmd/ytdl` CLI, so the future daemon and web UI share one metadata
  pipeline ([ADR-0004](docs/decisions/0004-go-engine-package-layout.md)).
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
  provisioning path ([ADR-0005](docs/decisions/0005-macos-floor-and-single-engine.md)).

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
