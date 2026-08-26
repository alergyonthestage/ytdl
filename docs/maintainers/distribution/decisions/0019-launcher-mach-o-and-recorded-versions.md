# ADR-0019 — The bundle's Mach-O is a dedicated launcher, and the daemon reads recorded versions instead of probing

- **Status:** accepted
- **Date:** 2026-08-26
- **Context:** [ADR-0018](0018-desktop-launcher-app-bundle.md) ·
  [analysis](../analysis/2026-08-26-tech-choice-desktop-launcher.md) ·
  [design](../design/cycle6launch-launcher.md) ·
  [roadmap § Cycle 6-launch](../../roadmap.md#cycle-6-launch)
- **Amends:** [ADR-0016 §5 and §8](0016-cycle6plus-update-path.md) — the *local*
  half of a verdict may be answered from the installer's record; the remote half
  and the consent rule are unchanged.

## Context

ADR-0018 fixed the artefact — an app bundle around a linker-signed Mach-O,
generated on the machine, named `YTDL` — and deliberately left two things to
this gate. First: **whether that Mach-O is `ytdl` itself or a dedicated
launcher**, with two defects of the probe build to carry over (`Identifier=a.out`,
and a thin arm64 binary where `install.sh` already handles both architectures).
Second: **the remedy for the cold start**, whose presence in this cycle it fixed
but whose shape it called a design question. The candidates are the analysis's
§7 D2 — (a) fill lazily, (b) answer from `installed.conf` (`T4` reopened),
(c) make yt-dlp cheap on macOS, (d) raise the cap.

## Decision

### 1. `Contents/MacOS` holds a dedicated launcher, built from `cmd/ytdl-launch`

Published per architecture as a release asset alongside `ytdl`, checksummed in
the same `SHA2-256SUMS`, and installed into the bundle by `install.sh` using the
`ARCH_KEY` it already derives. It resolves `ytdl` by absolute path, runs
`ytdl gui`, and reports a failure the user can see. It contains no engine code
and has no flags.

Two sub-rulings follow:

- **`Identifier=a.out` stands.** The identifier of a Go linker-signed binary is
  a literal in the toolchain and no build flag sets it. Changing it needs
  re-signing — `codesign_allocate`, which finding **F2b** proved absent from the
  user's Mac, or a third-party signer in CI. Bundle identity comes from
  `CFBundleIdentifier`, which the installer sets.
- **The artefact rule reads more precisely than ADR-0018 §2 stated it:** *a
  Mach-O produced by the Go toolchain, ad-hoc linker-signed where the platform
  requires a signature.* The linker signs `darwin/arm64` and not `darwin/amd64`,
  because x86_64 macOS requires no signature — confirmed on the probe artefacts
  themselves. `ytdl_macos_amd64` has shipped unsigned since 2.0.0 and runs. The
  case against a shell script was never the signature alone: macOS 26 refuses a
  script as `CFBundleExecutable` outright, on both architectures.

### 2. The GUI daemon fills the local facts from the installer's record, and reconciles with a probe once the port is open

`newGUIUpdater` no longer execs anything. `yt-dlp`'s version comes from
`installed.conf`'s `yt_dlp_version` — and, exactly as for ffmpeg, only when the
copy is ours; our record is never printed beside somebody else's binary. Once
`startWebUI` has bound the port, the existing probing walk runs in a goroutine
and replaces the recorded answer with the measured one.

This is **(b) then (a)**, and neither alone: (b) alone would believe a marker
that has gone stale — `T4`'s own retained question — and (a) alone would show
*versione non registrata* for a tool that is installed and working, which is the
defect `V20` closed.

**`T4` is thereby closed**, on the target platform's premise rather than the
container's.

### 3. (c) is rejected, in writing

Replacing the PyInstaller yt-dlp with the zipapp is the container's own fix
(ADR-0017) and it works there because the container has Python.
[ADR-0005](../../foundation/decisions/0005-macos-floor-and-single-engine.md)
deliberately removed Python from the user's machine, and
[ADR-0003](../../foundation/decisions/0003-engine-language-go.md) rejected it as
a universal dependency because it breaks the non-interactive install for the
majority to benefit only the GUI. Adopting (c) would trade a slow startup for an
installation that fails where it works today, and would make a development
convenience a user-facing requirement.

### 4. (d) is rejected as a symptom treatment

Raising `waitForGUI`'s cap leaves the 7.33 s on the path, leaves the user
waiting, and returns the failure on any machine slower than the one measured.
After decision 2 the port opens in well under a second, so the existing 10 s cap
becomes a large margin and stays exactly as it is.

## Alternatives considered

- **`ytdl` itself in `Contents/MacOS`** — rejected. LaunchServices runs the
  bundle executable with no arguments, so it would need `argv[0]` dispatch —
  behaviour `--help` cannot show and that changes with a file's name. Serving
  the GUI in process from the bundle copy would spawn its daemon from
  `os.Executable()`, i.e. from the binary the last update did not replace.
  Avoiding that leaves 9.5 MB of engine doing a 30-line launcher's job. And
  because its bytes move on every ytdl release, ADR-0018's *left alone when
  unchanged* would be false by construction, on the very path that runs on every
  update.
- **A universal (fat) launcher** — rejected. It buys nothing: the bundle is
  built on the machine, where the architecture is known, and `install.sh`
  already selects per architecture for `ytdl`.
- **Re-signing the launcher to fix the identifier** — rejected. On the user's
  Mac it needs tooling F2b showed is absent; in CI it needs a third-party
  signer, for a string no user-facing surface reads.
- **Probing at startup but with a shorter timeout** — rejected. A shorter
  timeout does not make the answer arrive; it makes it arrive empty, which is
  (a)'s weakness with a worse failure mode.
- **A resident Dock application with a Quit** — rejected by the design (§6.3): it
  needs a lifetime rule competing with
  [ADR-0008](../../engine/decisions/0008-daemon-lifecycle.md)'s, and the
  launcher shape forecloses it at no cost.

## Rationale

1. **The bundle must be cheap to leave alone.** `install.sh` runs on every
   update since Cycle 6-plus. The only artefact whose bytes do not move on every
   release is one that is not the engine — so the launcher is what makes
   ADR-0018's anti-churn consequence achievable rather than aspirational.
2. **The launcher must not become a second front-end.** Every rule about ports,
   tokens, daemons and browsers already lives in `ytdl gui`, read by both
   channels. The launcher transports that command's own messages to a surface
   Finder can show; it authors none of them.
3. **The authority was already on disk.** `installed.conf` has recorded
   `yt_dlp_version` since Cycle 6-plus. The 7.33 s exec was answering a question
   a file read answers, on the one path where the answer was being waited on.
4. **ADR-0016 §5's doctrine is restored, not weakened.** It said no probe is ever
   on a path a user is waiting on; the remote half honoured it and the local half
   did not. After this, both do — and §8's promise that installed versions are
   always shown is strengthened, because they now appear instantly.

## Consequences

- A second release asset, `ytdl_launch_macos_{arm64,amd64}`, with its entries in
  `SHA2-256SUMS` and a release-time assertion that the arm64 one carries
  `LC_CODE_SIGNATURE`.
- `internal/update` gains one exported function (`RecordedDependencies`) and no
  new policy; `Dependencies` keeps its signature and its behaviour.
- The two comments in `internal/update` that the measurements falsified are
  corrected in the same change: one becomes true because the code changes, the
  other because it stops modelling a warm path that macOS does not have.
- `install.sh` gains a bundle step that **never fails the install** — an app
  that cannot be written is warned about, not aborted on (the discipline of
  ADR-0016 §15).
- `YTDL_APP_DIR` joins the development sandbox's variables, so a dev bundle can
  never launch the installed `ytdl`.
- Gate C carries what no container and no maintainer Mac can settle: Spotlight
  and Launchpad, macOS 26, a Mac that is not the maintainer's — and now also
  whether an `osascript` alert launched from Finder reaches the front of the
  screen. The launcher's log does not depend on that answer.
