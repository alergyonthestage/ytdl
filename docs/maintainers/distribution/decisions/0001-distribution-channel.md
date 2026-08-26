# ADR-0001 — Distribute via a curl-based installer script

- **Status:** accepted (support floor superseded in part)
- **Date:** 2026-07-21
- **Context:** [distribution.md](../design/distribution.md)
- **Superseded in part:** the macOS support **floor** (10.13 → **10.15**) is
  superseded by [ADR-0005](../../foundation/decisions/0005-macos-floor-and-single-engine.md), which drops the
  legacy Python path. The distribution *channel* decided here (curl installer,
  install into `~/.local/bin`, mandatory checksum verification) stands unchanged.

## Context

`ytdl` must reach a handful of non-developer macOS users. They have no Homebrew,
no Node.js, an unconfigured Terminal, and in at least one case macOS Mojave 10.14.
The installer has to provision yt-dlp and ffmpeg, put them somewhere usable
without `sudo`, and keep working over time.

Two constraints shape the decision:

1. yt-dlp's legacy macOS builds ended in August 2025; the standard binary needs
   macOS 10.15+, while older systems must go through a Python install.
2. Unsigned `.pkg`/`.dmg`/`.app` artifacts are blocked by Gatekeeper unless
   notarized, which costs $99/year — but binaries fetched with `curl` are never
   quarantined at all.

## Decision

Distribute through a **shell installer fetched with `curl` from a public GitHub
repository**, installing into `~/.local/bin`, provisioning yt-dlp and ffmpeg from
official or signed upstream sources, and shipping a self-update command from the
first release.

On macOS 10.13–10.14 the installer uses the zipimport yt-dlp and directs the user
to the signed python.org Python 3.13 installer. Below 10.13 it aborts with an
explicit explanation.

> **Superseded by [ADR-0005](../../foundation/decisions/0005-macos-floor-and-single-engine.md):** the legacy
> 10.13–10.14 Python path is removed. The Go engine requires macOS 10.15+, so the
> floor is raised to 10.15 Catalina and Python is dropped from the project. The
> abort-below-floor behaviour remains, only the threshold moves (10.13 → 10.15).

## Alternatives considered

- **Homebrew tap** — rejected. Requires Homebrew, which the audience lacks and
  which no longer supports the older macOS in scope. (It is *not* rejected for
  signing reasons: `yt-dlp` is a healthy Homebrew formula.)
- **npm package** — rejected as primary. Requires Node.js, adding a heavier
  prerequisite than the one it removes; on Mojave, installing Node is its own
  problem. Reconsider only if a user turns out to have Node already.
- **Unsigned `.pkg` / `.dmg` / `.app`** — rejected. Highest apparent convenience,
  worst real friction: Gatekeeper blocks them, and the workaround is a multi-step
  detour through System Settings that this audience will not complete unaided.
  Revisit only alongside a paid Developer ID, most plausibly for the GUI.
- **Third-party legacy binaries** (`alex-free/yt-dlp-macos-legacy`) — rejected as
  default. Solves Mojave in one step, but means asking non-technical users to run
  an unverifiable 43 MB binary from a one-month-old repository with 1 star and no
  checksums. The security trade is not worth saving one signed Python install.
  Keep as a documented fallback if the Python path proves too hard in practice.
- **Vendoring our own legacy builds** — rejected for now. It is what upstream
  stopped doing for good structural reasons (no Intel CI runners); we have no
  better position to sustain it.

## Consequences

- Users need to paste one command into Terminal. Acceptable, and the lowest
  friction available without a Developer ID.
- Mojave users have a two-step install (Python, then the one-liner). Documented
  explicitly rather than hidden.
- The installer carries real responsibility: it executes remote code by design,
  so **checksum verification against the published `SHA2-256SUMS` is mandatory**,
  not a refinement.
- Using `releases/latest/download/…` URLs means no version pinning in the
  installer — upstream releases require no change on our side.
- A future GUI (`.app`) reopens the notarization question. That decision is
  deferred, and the installer's design does not depend on it.
