# Maintenance & evolution policy — yt-download

Instantiated 2026-08-26 from `knowledge/maintenance-policy.template.md`. The pack
ships no default here on purpose: what is right for a pre-launch MVP is wrong for a
tool people have installed.

## 1. Project phase

- **Current phase: `production-with-users`.**
- Since **2026-08-12** there is a real user who is **not** the maintainer, on a Mac
  the maintainer does not sit at. Since v2.2.0 that user receives releases through
  the update path rather than by hand.
- **Implication:** stability wins over velocity. A change that breaks an installed
  copy costs a physical visit or an unrecoverable installation, so the trade-off
  that applied through Cycle 5 — break it, it is only us — no longer applies.

## 2. Breaking changes

- **Versioning: SemVer**, in force since `v2.0.0`.
- **What may not change without a major**, because an installed copy depends on it:
  the CLI surface, the config file keys, the history record format, and the
  `~/.local/bin` install location.
- **History records are `omitempty` and never migrated.** Old records must keep
  loading; a new field is added, never a rename or a migration pass.
- **Deprecation window:** one minor release carrying a warning before removal.
- A change that requires the user to *do* something needs a line in the Italian
  guides under `docs/users/guides/`, not only in the changelog.

## 3. Legacy code

- **The Bash `ytdl` is frozen and isolated**, and that is a strategy, not neglect:
  it is the **golden parity reference** the argv tests are measured against.
- It is **not maintained** and **may not be deleted**. Deleting it would leave
  `internal/core`'s goldens unregenerable — `go test ./internal/core -update` needs
  it to run.
- `internal/core` and `internal/daemon` stay byte-unchanged. Anything new on the
  yt-dlp command line is appended **after** `core.BuildArgs`.

## 4. Documentation archival

- A **living** document that stops reflecting the system is **updated**, not
  archived: that is what living means.
- A **design** document superseded by a newer one gets a one-line banner naming its
  successor and moves to `<domain>/design/archive/`, keeping its git history.
- **No `archive/` folder is created before something needs it.** The Cycle 1 and
  Cycle 5 designs are *realised*, not superseded, and stay in their domain's
  `design/`.
- A historical document is **never** silently corrected. A factual correction is a
  dated addition that leaves the original readable — an errata beside the passage,
  and a note at the top saying what was corrected and when.

## 5. Refactoring triggers

Observable signals, not opinions. Any one of them opens a `review-refactoring`
before the next feature that touches the area:

- the same defect had to be fixed in **two places** for one bug;
- a **surface states something untrue** — no control that cannot work, no value
  displayed that is not in force, no count without its window;
- a module is blocking **two or more** pending cycles from starting;
- a review finds the same class of defect it found in the previous cycle. It has
  happened three times: *the container answers truthfully to questions that do not
  matter*, of which `V24` is the widest — the working tree is not what the update
  executes.

## 6. Cadence

| Review | When |
|---|---|
| `review-implementation` | at the end of each cycle's implementation, before gate C |
| `review-docs` | right after it, on the same branch |
| `review-refactoring` | before each release, and on any trigger in §5 |
| Roadmap | at every `/plan`, when `/implement` marks a unit done, at `/review-docs` and at `/handoff` |

⚠️ **Gate C is not a review and no cadence covers it.** It is the maintainer's
hands-on verification on real hardware, and this project has measured what happens
without it: see `project-profile.md` §8.

## 7. Revisit this file

The policy above is valid **for the phase recorded in §1**. Revisit it when that
phase changes — a second non-maintainer user, or a distribution channel that is not
a curl installer, would both change what §2 costs.
