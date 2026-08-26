# Improvements — known, worth doing, not scheduled

What has been **measured and left undone**: a defect that was reproduced and not
repaired, a tool worth sharpening. Nothing here is a task — nothing is scheduled on
it and nothing depends on it. When an entry is picked up it becomes an ordinary
[roadmap](roadmap.md) entry and leaves this list.

**The boundary with the roadmap.** An item assigned to a cycle lives in the roadmap,
not here: `V26`, `V27`, `V29` and `V22` are fixed to
[Cycle 10](roadmap.md#cycle-10--visual-language-s--planned) and are that cycle's
precondition, not a backlog. What is below has **no cycle**.

Each entry is one line. Its analysis — how it was reproduced, and what a fix would
have to do — is in the review that found it; that is the link.

## Correctness, still open

| # | What is wrong | Analysis |
|---|---|---|
| `V23` | An installer aborted by `set -u` is still recorded as a **successful** run, and the hardening that was applied is inert under bash 3.2. A fix proven under both shells is written down and was not applied | [review 004](distribution/reviews/004-cycle6plus-gate-c.md#V23) |
| `V19` | A package comment claims an import the package does not have — `internal/update` imports `buildinfo` plus stdlib, not `config`. Cosmetic, and recorded rather than corrected because the documentation phase does not touch code | [review 003](distribution/reviews/003-cycle6plus-documentation.md#cycle6plus-docs) |

## Cost, not correctness

| # | What it costs | Analysis |
|---|---|---|
| `V25` | One `go test -race ./...` leaves **91 GB** in `/tmp` and runs 4 m 26 s. This is what took the container down once | [review 004](distribution/reviews/004-cycle6plus-gate-c.md#V25) |
| `V28` | An installation that installs nothing still costs **45 seconds** | [review 004](distribution/reviews/004-cycle6plus-gate-c.md#V28) |

**`V25` — re-measured 2026-08-26, unchanged.** Verifying this branch reproduced it:
4 m 23 s and **1657** `/tmp/_MEI*` directories, 77 GB. Nothing new — the mechanism
and the remedy are already established in
[review 004](distribution/reviews/004-cycle6plus-gate-c.md#V25): yt-dlp is a
PyInstaller bundle that removes its own `/tmp/_MEI…` **on exit**, and cannot when
the test kills it first, so the fix is to make the version-probe budget injectable
rather than to shrink the tests. Recorded here only because it is still true one
release later. Until it is fixed, `rm -rf /tmp/_MEI*` after a suite run reclaims all
of it.

## The seven minors of the fix session

Recorded rather than fixed by the maintainer's scoping decision of 2026-08-18. None
is a regression, and **none is reachable without an unusual precondition** — which is
why they are here and not on the roadmap. Every one of them is set out in full in
[review 002](distribution/reviews/002-cycle6plus-fix-session.md#cycle6plus-fixreview);
the lines below exist so that nobody has to open it to know they are there.

| What | Precondition it needs |
|---|---|
| `-r 0-0` can restore `V5`'s own symptom; the cheap hardening is one retry without the range | a front end answering `400` to a ranged request |
| A mixed run records a build id the installed ffmpeg is not | ffmpeg verifies at the pin while ffprobe falls back |
| The cheap CLI path speaks for a yt-dlp that is absent or foreign — structurally `V2` one field over, and **pre-existing** | a previous round describing a copy that is no longer there |
| `Dependency.Attested` no longer matches its doc comment for a foreign copy | a foreign copy |
| `install_ffmpeg` is not re-entrant: a second call in one shell checks the fallback build against the pinned sha256 | two calls in one shell — `main` calls it once |
| Two marker-parsing divergences between bash and Go: a duplicated key, and an unparseable file (the installer fails closed, Go fails open) | a hand-edited marker |
| `pollUpdate` polls for ever on any state that is not `done`/`failed`/`abandoned`, `idle` included | a missing or corrupt run file |

## Where the older registers went

The initial analysis of the Bash tool (`C`/`U`/`M`) and the evolutions asked for
(`E1`/`E2`) are in
[the initial findings](engine/analysis/2026-07-21-code-initial-findings.md). The
Cycle 5 gate-C register (`G1`–`G26`) is in
[review 001](ux/reviews/001-cycle5-gate-c.md#gate-c); **every `G` finding is claimed
by a cycle**, so none of them is listed here.
