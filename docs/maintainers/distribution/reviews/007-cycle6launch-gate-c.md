# Review 007 — Cycle 6-launch, gate C

The maintainer's hands-on verification of `v2.3.0` on real hardware, run on
**2026-08-27** on a MacBook Pro, **macOS 15.6, arm64**, installing from the
published release the way a user would. Historical: what it asserts is what was
observed that day.

**Verdict: passed, with three exceptions recorded and one finding.** Eight of the
nine items pass, one was not exercised, and the launcher's own behaviour — the
thing this cycle exists for — was verified end to end including its failure path.
The finding is `V47`, and its option A was applied in the same closing unit.

Gate C is not a review the oracle can stand in for
(`.cco/claude/rules/project-profile.md` §8). What
follows was observed by a person on a machine, not measured by a suite.

## The nine items

| # | Item | Outcome |
|---|---|---|
| 1 | An **update** on an existing installation installs the app | **pass** — `ytdl --update` on a v2.2.0 machine ended with *"The YTDL app is in ~/Applications/YTDL.app"* |
| 2 | Version and sidecar | **pass** — `ytdl --version` → `v2.3.0`; the sidecar holds `/Users/…/.local/bin/ytdl`, absolute |
| 3 | **The first click on the first bundle ever installed**, twice in a row | **pass** — the interface opened in under a second, both times, with no Terminal. This is the scenario that failed at gate A |
| 4 | Dock, alias, move, copy | **pass** — all four work. This is where `V47` came from |
| 5 | **A failure is visible from Finder** | **pass** — with `ytdl` renamed aside, the alert appeared **in front**, in Italian, naming the path and the log; `launcher.log` recorded `exit=1` with the child's own words |
| 6 | **Anti-churn**: replacing only the inner binary | **pass** — after `rm …/Contents/MacOS/YTDL` the Dock entry and the alias stopped working, and after `ytdl --update` **they resumed without being recreated**. The bundle directory was not deleted and rebuilt (§4.3) |
| 7 | GUI → *Versione e aggiornamenti* | **pass** for the ordinary case: `v2.3.0`, `2026.08.19`, `9.0`, *"Sei aggiornato — verificato 27/08/26, 15:43"* |
| 8 | **Spotlight and Launchpad** (`F3`, ADR-0018) | **pass, better than expected** — Launchpad shows YTDL, and Spotlight offers it under *suggested*, on the machine whose indexing was believed to be off |
| 9 | *Aggiornamenti* with a `yt-dlp` **ytdl did not install** (`V32`) | **not exercised** — see below |

## The three exceptions

They are exceptions, not omissions: each is a question this machine could not put.

- **`V32` was not exercised.** The fix is visible only where `yt-dlp` resolves to a
  copy ytdl did not install. The maintainer's machine has Homebrew but its `yt-dlp`
  is ours, at `~/.local/bin`, so recorded and probed agree and the difference the
  fix makes cannot appear. The reversible procedure is recorded in the handoff of
  this session; until someone runs it, `V32`'s user-visible effect is **built,
  tested, and unobserved on hardware**.
- **A real download with a real conversion was not run.** The maintainer will run it
  on a second Mac — a student's — and reports findings in a later session. Nothing
  in this cycle touches the download path, and `internal/core` is byte-unchanged
  (the parity gate is empty at every commit), so the exposure is the release's
  binaries rather than this cycle's work.
- **macOS 26 was not exercised**, nor a Mac that is not the maintainer's. Every
  number in this cycle comes from one machine on 15.6. That was already ADR-0018's
  recorded limit and it is unchanged.

## `V47` — the installer knows exactly one path · minor

<a id="v47"></a>

**What was observed.** The app was copied to the Desktop and kept working; it was
moved out of `~/Applications` and kept working; and a later `ytdl --update`
recreated a fresh bundle at the canonical path.

**Why it happens.** `install.sh`'s `app_dir()` is
`${YTDL_APP_DIR:-$HOME/Applications}/YTDL.app` and nothing else. Two different
consequences follow, and only the second is felt:

- **A copy** carries its own launcher and its own sidecar — an absolute path to
  `ytdl` — so it keeps working indefinitely, but no release will ever replace its
  launcher. Today nothing is stale: the copy's bytes are the release's. It becomes
  stale the first time a release changes the launcher, which is exactly what `V31`
  and `V36` were.
- **A move** leaves the canonical path empty, so the next install creates a second
  bundle there. Two apps, both working, and no surface says so.

**Why it is a documentation finding first.** All three user-facing surfaces —
[`guida-installazione.md`](../../../users/guides/guida-installazione.md),
[`guida-uso.md`](../../../users/guides/guida-uso.md) and the README — said *"drag it
to the Dock or the Desktop — it works from anywhere"*. The Dock was never the
problem: it stores a shortcut and leaves the app in place. Dragging to the Desktop,
on the same volume, is a **move**. The sentence was true when read and false a
release later, which is `ux-principles.md` §5 measured over time rather than at an
instant.

**Options, and what was chosen.**

| | Option | Pro | Contra |
|---|---|---|---|
| **A** | Correct the three surfaces: Dock or **alias**, original stays in Applications | one sentence in three documents, no code, and the promise becomes true | does not follow an app somebody has already moved |
| **B** | Record the bundle's path and update **that**, if it still exists | *"works from anywhere"* true in the strong sense | a new lever with its own failure modes: two bundles, an unmounted volume, which one wins |
| **C** | Accept it, and have the installer announce when it **creates** an app where none was | free | the user discovers the duplicate after producing it |

**Chosen: A**, by the maintainer on 2026-08-27, and applied in the same closing
unit. **B is recorded in [`improvements.md`](../../improvements.md) and is not
scheduled** — it buys with a mechanism what a sentence buys honestly.

## What gate C confirmed that no suite could

- The launcher's **failure path works on the target platform**: the `osascript`
  alert reaches the front of the screen. That was an open question this design
  raised about itself, and the answer is yes.
- **Replacing only the inner binary preserves the Dock entry and an alias.** The
  code says the directory is never deleted; the alias resuming after the update is
  the observation consistent with it.
- **The cold start is fixed where it mattered** — the machine, not the container.
  Under a second, twice, on the first bundle ever installed.

## One process finding, and it is not about this cycle

`main`'s CI had been **red for ten days** and no local gate could have seen it:
`FetchPin` resolves the ffmpeg build for the machine it runs on, three tests
asserted one architecture's id unconditionally, the maintainer's container is arm64
and the runner was amd64. Each was green about the question it asked.

It surfaced only because the maintainer opened the Actions tab before continuing —
and `v2.3.0` had already been published from that tree. What shipped was sound,
because the failure was in the tests and not in the product, but that was luck.

Both halves were closed on 2026-08-27, before this report: the suite now runs on
**both architectures**, and `release.yml`'s publishing job **needs** a job that
calls `ci.yml`, so a release cannot be cut from a tree the suite fails. Neither
replaces gate C — [`releasing.md`](../guides/releasing.md) still says the suite is a
precondition and never the evidence.

## Where this leaves the cycle

`L12` is **done**, with the three exceptions above recorded rather than waived. The
cycle closes: `C2` — the exception Cycle 6-plus's gate C recorded, *opening the
interface still needs a Terminal* — is closed by an app that opens it in under a
second without one.
