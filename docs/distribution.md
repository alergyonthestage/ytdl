# Distribution — Constraints & Design

Primary objective: ship `ytdl` to **non-developer macOS users** with a fast,
low-friction install that provisions its dependencies, works on older macOS, and
fails with actionable messages.

All version and URL claims below were verified against upstream sources on
**2026-07-21**. Claims that could not be verified are marked *unverified*.

## The central constraint

yt-dlp **dropped its legacy macOS builds**. Since the end of August 2025 no
`yt-dlp_macos_legacy` asset is produced; the release of 2026-07-04 confirms only
`yt-dlp_macos` (universal, **macOS 10.15+**) and the platform-independent
`yt-dlp` zipimport script (**needs Python 3.10+**).
([announcement](https://github.com/yt-dlp/yt-dlp/issues/13856),
[release assets](https://github.com/yt-dlp/yt-dlp/releases/tag/2026.07.04))

The reason is structural, not a policy whim: GitHub retired the Intel `macos-13`
runners, and PyInstaller supports only macOS 10.15+. It will not be reversed.

So **Mojave 10.14 cannot run the standard yt-dlp binary.** The upstream advice to
those users is "upgrade macOS, or use a Python install".

That second path still works, and it is the one this project takes.

## Support matrix

The viable combinations, all from **official or signed sources**:

| Target | yt-dlp | ffmpeg | Extra step |
|---|---|---|---|
| macOS 11+ Apple Silicon | `yt-dlp_macos` (universal) | martin-riedl `arm64` (signed + notarized) | — |
| macOS 10.15+ Intel | `yt-dlp_macos` (universal) | martin-riedl `amd64`, or evermeet.cx | — |
| **macOS 10.13–10.14** (always Intel) | `yt-dlp` zipimport | evermeet.cx (10.13+, Intel) | **install Python 3.13** (python.org, 10.13+) |
| macOS ≤ 10.12 | none | — | out of support |

Two facts make the old-macOS row work out better than expected:

- **Python 3.13's installer supports macOS 10.13+**, so it installs on Mojave.
  The minimum was raised from 10.9 to 10.13, not to 11 as the `macos11.pkg`
  filename suggests. ([3.13.9 release notes](https://www.python.org/downloads/release/python-3139/))
  That `.pkg` is signed and notarized by the Python Software Foundation, so it
  passes Gatekeeper with a normal double-click — an acceptable extra step.
- **evermeet.cx being Intel-only is not a limitation here.** No Apple Silicon Mac
  can run Mojave, so every Mojave machine is Intel by definition. The ARM gap only
  affects macOS 11+, where martin-riedl provides signed arm64 builds.

The realistic floor with official sources is therefore **macOS 10.13 High Sierra**,
one version *below* the requested Mojave target.

## Verified download endpoints

Each was checked with an HTTP request on 2026-07-21:

| What | URL | Result |
|---|---|---|
| yt-dlp (10.15+) | `https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos` | 200 |
| yt-dlp (zipimport) | `https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp` | 200 |
| ffmpeg arm64 | `https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/ffmpeg.zip` | 307 → asset |
| ffmpeg amd64 | `https://ffmpeg.martin-riedl.de/redirect/latest/macos/amd64/release/ffmpeg.zip` | 307 → asset |
| ffmpeg Intel/legacy | `https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip` | 302 → asset |

The `releases/latest/download/…` form matters: it always resolves to the current
release, so **no version is hardcoded** in the installer and updates need no
installer change.

yt-dlp also publishes `SHA2-256SUMS` per release — the installer must verify the
downloaded binary against it. This is not optional hygiene: the installer runs
`curl | bash` and drops an executable into the user's PATH.

## Gatekeeper

- Files downloaded with `curl` are **not** quarantined; only browser downloads get
  `com.apple.quarantine`. An installer fetching binaries via `curl` therefore
  avoids Gatekeeper entirely — this is the decisive argument for the curl channel
  over any `.pkg`/`.dmg`.
- Since Sequoia, the Control-click bypass is gone; unsigned apps need a trip
  through System Settings → Privacy & Security. `xattr -d com.apple.quarantine`
  still works. *(Sequoia/Tahoe specifics: unverified in detail — relevant only if
  we later ship a `.app`, see [roadmap](roadmap.md) phase 4.)*
- Signing/notarizing anything requires an Apple Developer ID at $99/year. Not
  needed for the shell-script channel; it becomes a real question only for the GUI.

## Channel comparison

| Channel | Friction for a non-developer | Prerequisites | Signing | Old macOS |
|---|---|---|---|---|
| **`curl \| bash`** | one paste into Terminal | none | not required | ✓ works |
| npm | needs Node.js installed first | Node.js | not required | ✗ Node on Mojave is itself a problem |
| Homebrew tap | needs Homebrew installed first | Homebrew | not required | ✗ Homebrew supports only recent macOS |
| `.pkg` unsigned | blocked, manual override | none | required in practice | ✗ |
| `.dmg`/`.app` | blocked, manual override | none | required in practice | ✗ |

Correcting a claim that circulates: `yt-dlp` **is** a normal Homebrew *formula*
and is not deprecated ([formulae.brew.sh](https://formulae.brew.sh/formula/yt-dlp)).
Homebrew is unsuitable here not because of signing, but because it requires
Homebrew itself — which no longer supports Mojave and which the target audience
does not have. Its bottles are built only for Sonoma and newer.

### On third-party legacy builds

[`alex-free/yt-dlp-macos-legacy`](https://github.com/alex-free/yt-dlp-macos-legacy)
offers a standalone binary with ffmpeg bundled for macOS 10.12+, tracking upstream
closely (v1.0.2 on 2026-07-06).

**Not recommended as a default.** The repository was created 2026-06-08, has 1
star and ~13 downloads, ships a 43 MB opaque binary under the Unlicense, with no
checksums or signature. Asking non-technical users to execute that — with full
access to their account — is a materially worse trade than one extra Python
install from python.org. Worth revisiting if it gains adoption and publishes
verifiable checksums.

## Installer design

```mermaid
flowchart TD
    A[curl -fsSL … | bash] --> B[detect macOS version + arch]
    B --> C{macOS ≥ 10.15?}
    C -->|yes| D[fetch yt-dlp_macos]
    C -->|no| E{macOS ≥ 10.13?}
    E -->|no| F[abort: clear message,<br/>state the floor and why]
    E -->|yes| G{python3 ≥ 3.10 present?}
    G -->|yes| H[fetch yt-dlp zipimport]
    G -->|no| I[instruct: install Python 3.13<br/>from python.org, then re-run]
    D --> J[verify SHA2-256SUMS]
    H --> J
    J --> K[fetch ffmpeg for arch]
    K --> L[install into ~/.local/bin]
    L --> M[add to PATH: .zprofile + .bash_profile]
    M --> N[verify: ytdl --version]
```

Design commitments:

- **Install prefix `~/.local/bin`** — no `sudo`, conventional, works identically
  across all target versions.
- **PATH written to both `~/.zprofile` and `~/.bash_profile`**, idempotently.
  macOS defaults to zsh from Catalina and bash on Mojave, and Terminal.app starts
  *login* shells — so the `*profile` files are the ones that matter, not `.zshrc`.
- **Version detection must handle the `10.x` → `11+` scheme change.** Taking the
  first dot-component alone misreads 10.15 as "10" and would route Catalina down
  the Mojave path. Compare major *and* minor when major is 10.
- **Checksum verification before install**, using the published `SHA2-256SUMS`.
- **Every failure names the next action** — never `brew install …`, which the
  audience cannot act on.
- **Updates ship with the first version**, not later: `ytdl --update` refreshes
  yt-dlp and the script itself. yt-dlp breaks whenever YouTube changes, and a
  user with no update path is a user with a broken tool within months. This is the
  single most important durability decision in the plan.

The one-liner is published from a public GitHub repository:

```
curl -fsSL https://raw.githubusercontent.com/<owner>/yt-download/main/install.sh | bash
```

A downloadable `install.command` (double-clickable in Finder) can wrap the same
script for users who will not open Terminal at all — it inherits the quarantine
flag when downloaded via browser, so it needs the Privacy & Security override on
modern macOS. Evaluate in phase 2.
