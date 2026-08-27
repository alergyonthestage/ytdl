# ADR-0001 — Distribute via a curl-based installer script

- **Status:** accepted (support floor, and the `.app` rejection, superseded in part)
- **Date:** 2026-07-21
- **Context:** [distribution.md](../design/distribution.md)
- **Superseded in part:** the macOS support **floor** (10.13 → **10.15**) is
  superseded by [ADR-0005](../../foundation/decisions/0005-macos-floor-and-single-engine.md), which drops the
  legacy Python path. The distribution *channel* decided here (curl installer,
  install into `~/.local/bin`, mandatory checksum verification) stands unchanged.
- **Superseded in part:** the rejection of **`.app` bundles** is superseded by
  [ADR-0018](0018-desktop-launcher-app-bundle.md) for a bundle **generated on the
  machine**, which was measured on real hardware to carry no quarantine attribute.
  The rejection stands for a **downloaded** bundle, which is the case reasoned about
  below, and `.pkg`/`.dmg` are not reopened at all.

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

  > **Superseded in part by [ADR-0018](0018-desktop-launcher-app-bundle.md)
  > (2026-08-26):** this reasoning holds for a bundle that is *downloaded* — which
  > is what "distribute as" meant here. A bundle **generated on the user's machine
  > by the installer** never acquires `com.apple.quarantine`, so Gatekeeper never
  > assesses it and no Developer ID is needed. Measured on hardware, not assumed.
  > `.pkg` and `.dmg` are unaffected: both are downloaded by construction.
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

## Addition, 2026-08-27 — the downloadable installer, asked again and closed again

The question was re-raised during Cycle 6-launch's design: now that ytdl ships an
app icon, could the *install* also start from a double-click — a file downloaded from
the repository, linked from the README, that runs the installer for the user?

**The answer is no, on this ADR's own reasoning, and the reasoning got stronger
rather than weaker.** Recorded here so the question is not re-opened a third time:

- Anything executable **downloaded through a browser acquires `com.apple.quarantine`**
  — a `.command`, an `.app`, or an archive containing either, since what is extracted
  inherits the attribute. Without a Developer ID, `spctl` rejects it. That is exactly
  the case this ADR reasoned about, and [ADR-0018](0018-desktop-launcher-app-bundle.md)
  §4 states the boundary as an invariant: a bundle **generated** on the machine is a
  different case, and distributing a prebuilt one re-enters the territory ruled out here.
- **The escape hatch that made the block survivable is gone.** Earlier macOS offered
  right-click → *Open*; on macOS Tahoe it does not
  ([analysis §3](../analysis/2026-08-26-tech-choice-desktop-launcher.md)). What remains
  is System Settings → Privacy & Security → *Open Anyway* — the multi-step detour this
  ADR already judged the audience will not complete unaided. A downloaded file that
  does nothing when double-clicked is **worse** than a line to paste.
- **The Terminal cost is paid once per machine.** Since Cycle 6-plus, `ytdl --update`
  fetches and runs `install.sh` itself (`internal/update/runner.go`), so the one-liner
  is a first-install cost, not a recurring one. Cycle 6-launch removes the *daily*
  Terminal need; this would target the one moment that is already non-recurring.

**What would change the answer** is not a technique but the audience: a distribution
that does not pass through the maintainer. The resolution there is notarization with a
paid Developer ID — the scenario this ADR already named as its only revision trigger —
not an unsigned downloadable artefact.
