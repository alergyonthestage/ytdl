# ADR-0018 — ytdl ships an app bundle, built on the machine, around a linker-signed Mach-O

- **Status:** accepted
- **Date:** 2026-08-26
- **Context:** [analysis](../analysis/2026-08-26-tech-choice-desktop-launcher.md) ·
  [roadmap § Cycle 6-launch](../../roadmap.md#cycle-6-launch)
- **Supersedes in part:** [ADR-0001](0001-distribution-channel.md) — its rejection of
  `.app` bundles, and only that. The distribution *channel* it decided (curl
  installer, `~/.local/bin`, mandatory checksum verification) stands unchanged.

## Context

`ytdl gui` is the only way to open the interface and it requires a Terminal, which is
the one thing the GUI exists to avoid. `C2`, ADR-0016's acceptance test, passed with
exactly this exception.

[ADR-0001](0001-distribution-channel.md) rejected `.pkg`/`.dmg`/`.app` because
Gatekeeper blocks unsigned bundles unless notarized, at $99/year. That reasoning is
sound **for a downloaded artefact**. This cycle rested on the claim that a bundle
**generated on the machine** never acquires the quarantine attribute and is therefore
a different case — a claim the development container cannot evaluate, because it is
not the target platform ([ADR-0017 §3](../../foundation/decisions/0017-dev-container-oracle.md)).

Three candidate bundles were built and measured on real hardware. The numbers are in
the [analysis](../analysis/2026-08-26-tech-choice-desktop-launcher.md); this ADR
records what they decided.

## Decision

**1. ytdl installs an app bundle at `~/Applications/YTDL.app`, created by
`install.sh` on the machine.** No `sudo`, so not `/Applications`. It resolves `ytdl`
by absolute path and never by its own location, so it survives being moved to the
Dock, the Desktop, or anywhere the user prefers.

**2. `Contents/MacOS` holds a Mach-O signed by the Go linker**, not a shell script
and not an `osacompile` applet. The ad-hoc signature is applied at build time and
travels with the binary; no developer tooling is required on the user's machine.
Whether that Mach-O is `ytdl` itself or a dedicated launcher is a design question,
deferred to gate B — this ADR fixes the *kind* of artefact, not which binary fills it.

**3. The user-facing name is `YTDL`.** The icon is deliberately left open: it is
changeable after the fact and blocks nothing.

**4. "Never downloaded" is an invariant to protect, not a happy accident.** `spctl`
rejects the bundle in every candidate shape. The design holds only while the bundle
is *generated* rather than *shipped*, and any future change that would distribute a
prebuilt bundle re-enters the territory ADR-0001 ruled out.

**5. The GUI's cold-start defect enters this cycle's scope.** See ADR §*Rationale*,
point 4.

## Alternatives considered

- **A shell script as `CFBundleExecutable`** — rejected. It launched on macOS 15.6,
  but it carries no signature at all (`codesign`: *code object is not signed at all*)
  and the pattern is [reported to fail outright on macOS 26](https://github.com/Ryubing/Issues/issues/422)
  with `_LSOpenURLsWithCompletionHandler() … error -54`. Machines update themselves;
  the target Mac being 15.6 today is not a property to build on. That the robust
  pattern is a Mach-O wrapper around a script is also why Platypus and `osacompile`
  are both built that way.
- **An `osacompile` applet** — rejected, for two independently sufficient reasons.
  Patching `Info.plist` to set the name **invalidates its signature** (measured:
  `valid on disk` before, `invalid Info.plist (plist or signature have been modified)`
  after), and name and icon are the whole point of the cycle. And the signature exists
  only because the maintainer's Mac has Xcode: `codesign` on stock macOS can verify
  but not sign, lacking `codesign_allocate`. What `osacompile` produces on a
  non-developer Mac is unknown and could be an unsigned bundle.
- **An Automator application** — rejected. Dominated by `osacompile`: same generic
  icon, same signature questions, and the bundle cannot be built non-interactively.
- **A `.command` file** — rejected by scope. It is not an app: it cannot sit in the
  Applications folder as one, cannot be pinned to the Dock as one, and
  double-clicking it opens a Terminal.
- **A notarized bundle with a paid Developer ID** — not reopened. It is what
  ADR-0001 declined, and the measurements show it is not needed: a bundle that is
  never downloaded is never assessed.

## Rationale

1. **The premise was verified, not assumed.** No locally created bundle carried
   `com.apple.quarantine`. Every candidate launched from Finder, opened the browser,
   showed no Terminal, and kept working after being moved.
2. **Only one candidate keeps a valid signature through the customisation the cycle
   requires.** That is the whole of the artefact argument.
3. **The maintainer's Mac is not the user's Mac.** Cycle 6-plus recorded three times
   that the container answers truthfully to questions that do not matter. The
   `osacompile` result is the same shape one machine further out: a Mac with Xcode
   cannot certify a property that depends on Xcode being absent. Rejecting a
   candidate whose security properties are *unmeasurable from here* is the
   conservative reading, and it is the one taken.
4. **A launcher on top of a first launch that fails silently would not deliver the
   cycle.** The GUI daemon takes ~7.5 s to open its port against `waitForGUI`'s 10 s
   cap — a 25% margin, lost often enough that the maintainer had hit it from the CLI
   without finding a pattern. From a Terminal the user reads the error and retries;
   from an icon there is nothing to read, which is a surface stating something untrue
   ([ux-principles](../../ux/design/ux-principles.md)). The cost is one
   `yt-dlp --version` — **7.33 s on macOS, warm or cold** — called synchronously by
   `newGUIUpdater` before the port opens. Its remedy is a design question; its
   presence in this cycle is not.

## Consequences

- `install.sh` grows the bundle: created when absent, **left alone when unchanged**.
  It now runs on every update, so churning the bundle would churn the user's Dock
  entry, their Spotlight index, and any alias they made.
- The uninstall path grows a fourth line, in
  [guida-installazione](../../../users/guides/guida-installazione.md) §*Come
  disinstallare* — user-facing Italian, not only the changelog.
- **`T4` is reopened** ([roadmap-history](../../roadmap-history.md)). It was deferred
  on the stated premise that its saving is "milliseconds rather than gigabytes" —
  measured in the container on the zipapp. On the target platform the saving is 7.33
  seconds on a path the user waits on. The deferral was not wrong; its premise did
  not cover this platform.
- Two comments in `internal/update` assert what the measurements contradict — that
  the version probe is never on a path a user is waiting on, and that a warm probe
  costs under a second. Recorded in the analysis, uncorrected: analysis does not
  touch code.
- **Gate C carries what no container and no maintainer Mac can settle:** Spotlight
  and Launchpad discoverability (indexing is disabled on the maintainer's machine),
  `osacompile`'s behaviour without developer tooling, and macOS 26.
