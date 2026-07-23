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
