# Changelog

Notable changes to ytdl. The format follows [Keep a Changelog](https://keepachangelog.com/),
and versions follow [Semantic Versioning](https://semver.org/).

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
