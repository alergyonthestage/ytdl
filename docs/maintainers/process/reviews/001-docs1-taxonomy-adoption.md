# Review 001 — `DOCS-1`, the move onto the `core-dev-framework` taxonomy

**Cadence:** `/review-docs`, run as `DOCS-1` task **D8**, on branch
`docs/framework/adopt-taxonomy` before the merge into `main`.
**Date:** 2026-08-26. **Verdict:** **updated in place** — merge-ready. Two items are
escalated to the maintainer, and neither blocks the merge.

This is the review the reorganization deliberately deferred to. Its
[design §10](../design/docs-reorganization.md) put *"whether any statement inside a
document is still true"* out of scope for D1–D7: **containers moved, contents were
not judged.** Judging them is this document.

## 1. What was verified, and how

Nothing here was taken on the branch's word. Every claim the reorganization made
about itself was re-measured.

| Check | Method | Result |
|---|---|---|
| Moves are verbatim | for each renamed file, diff `main:<old>` against `<new>` with **link targets stripped** | **clean** — every moved document differs only in its links |
| Splits are verbatim | extract each measured source range, strip the provenance banner, diff against the destination | **clean** — the only deltas are three `<a id=…>` anchors relocated to the file that now owns them |
| No content lost in the roadmap split | sort-compare the old `roadmap.md` against `roadmap.md` + `roadmap-history.md` + `releasing.md` | **clean** — the 35 unmatched lines are all prose the design said would be rewritten into one line per closed unit |
| Mapping complete | `git ls-tree main -- docs` against the new tree | **complete** — all 34 files accounted for, none orphaned |
| ADR placement | the 16 ADRs against the [design §4](../design/docs-reorganization.md) table | **matches**, including the two judgment calls (0003 → `foundation`, 0010 → `ux`) |
| `go build` · `go vet` · `gofmt -l` | run | clean · clean · **empty** |
| `go test -race ./...` | run | **green**, all 15 packages |
| Parity gate | `git diff main -- internal/core/ internal/daemon/` | **empty** |
| `tests/test-installer.sh` | run | **103 passed, 0 failed** |
| `./hack/check-docs-links.sh` | run | **green**, 52 files |

The verbatim proof is the one worth stating plainly: **the historical record came
through this reorganization intact.** That was the reorganization's central risk and
it did not materialise.

## 2. Corrected in place

Five stale statements. All are **facts** the code or the filesystem settles, not
decisions — which is what made them this review's to fix rather than to escalate.

### 2.1 The suite is minutes, not seconds — `engine/design/go-engine.md`

The document advertised the **whole suite under `-race` green in ~12 s from a cold
cache**, measured 2026-08-04. Re-measured on this branch: **2 m 24 s**, `cmd/ytdl`
alone ~100 s. The figure was not merely drifting — it described the tree *before*
Cycle 6-plus, whose update-path tests spawn a real `yt-dlp`.

Corrected to the measured value, with the cause named and the reader told to treat
it as an order of magnitude rather than a threshold. The `rm -rf /tmp/_MEI*` warning
was added beside it, because the two facts have one cause
([`V25`](../../distribution/reviews/004-cycle6plus-gate-c.md#V25)) and the document
that states the cost should state the remedy.

⚠️ **This reproduced during the review and took the container's disk down**, which
is the third time `V25` has been paid for. It is correctly recorded as unscheduled
in [improvements.md](../../improvements.md); this review adds only that it now costs
a review session too, not just a suite run.

### 2.2 A user guide contradicting the shipped binary — `users/guides/guida-installazione.md`

**The widest finding, and the only user-facing one.** Step 3 — the moment a new
user confirms the install worked — read:

> Se vedi **due righe** — la versione di `ytdl` … e quella di `yt-dlp` —
> l'installazione è riuscita.

Since Cycle 6-plus, `cli.RenderVersion` prints **four** lines: `ytdl`, one per
dependency (`yt-dlp`, `ffmpeg`), and `Aggiornamenti:`. v2.2.0 is released and
installed, so this is what a new user actually sees.

The guide therefore told a first-time user to expect one thing and the program
showed another, at the single step where they have no way to tell a real failure
from a documentation error. It also disagreed with two documents that were right:
[guida-uso.md](../../../users/guides/guida-uso.md) shows the four-line output, and
[dev-testing.md](../../guides/dev-testing.md) uses "four lines" as its checklist.

Corrected to four lines, the example version moved `v2.1.0` → `v2.2.0`, and a
pointer added to the guide that explains what the lines mean — the installation
guide's job is to say *they should be there*, not to teach the update model.

**Not caused by `DOCS-1`**: the file moved byte-identical. It was missed by Cycle
6-plus's documentation phase and is the reason this review's remit is contents
rather than containers.

### 2.3 Three link labels naming files that no longer exist

The bulk link rewrite repointed 321 **targets** correctly; it could not know that
some **labels** were themselves filenames.

The targets are unchanged in all three; only the visible label moved.

| File | Label was | Label now |
|---|---|---|
| `engine/design/go-engine.md` | `architecture.md` | `2026-07-21-code-bash-as-built.md` |
| `ux/design/ux-principles.md` | `design-cycle5-ux.md` | `cycle5-ux.md` |
| [`roadmap.md`](../../roadmap.md) | `improvements.md` | the Cycle 5 gate-C register |

⚠️ **The checker cannot catch this class**, and that is not a defect in it: it
validates that a target resolves, and all three of these resolved. A label that
names the wrong file is a *semantic* dangle. Worth knowing before trusting a green
run to mean "every link is right".

### 2.4 The index diagram promised folders the tree does not have

[`docs/README.md`](../../../README.md)'s Mermaid diagram gave all five domains the
same five type folders. The tree deliberately does not work that way — the
[design §2](../design/docs-reorganization.md) rule is that a domain gets a type
folder **only when it has a document to put in it**. `foundation/` has only
`decisions/`; `process/` had only `analysis/` and `design/`.

Corrected to the real per-domain leaves, with the rule stated under the diagram so
the next reader does not "fix" the asymmetry back. `process/reviews/` is added by
this very report and the diagram now says so.

### 2.5 The roadmap's own status — `roadmap.md`

Updated as the cadence requires: D8 `next` → `done`, D9 promoted to `next`, the
"awaits `/review-docs`" sentence retired, and this report linked. The branch's
**commit count was removed** rather than corrected (it said 24, the branch had 25):
per the documentation rules a commit-by-commit fact belongs to `git log`, and a
number that is wrong one commit later should not be in a document at all.

## 3. Escalated — REVIEW NEEDED

Both are in `.cco/claude/CLAUDE.md`. It is **user-owned configuration**, so this
review reports and does not touch it, even though the content is stale in exactly
the way §2 would otherwise fix in place. The maintainer applies these, or says to.

**E1 — the ~12 s figure, second copy.** Line 49:

```
go test -race ./...                                  # whole suite, ~12 s
```

Same defect as §2.1, and the more damaging copy: this file is loaded into **every**
session, so an agent budgets seconds for a job that takes minutes and fills the
disk. Suggested: `# whole suite, ~2-3 min; then: rm -rf /tmp/_MEI*`.

**E2 — the architecture line omits `internal/update`.** Lines 23–25 enumerate
`core`, `config`, `run`, `queue` + `daemon`, `logstore`, `webui`, `jobs`, `open`,
`notify`, `term`, `cli` — but **not `update`**, the package Cycle 6-plus was
entirely about, nor `buildinfo`. An agent reading only this file does not learn the
update path exists. Suggested: add `update` and `buildinfo` to the list.

Neither is a decision; both are omissions in a file this review may not write to.
They are flagged rather than fixed **only** because of who owns the file.

**Resolved 2026-08-26.** The maintainer approved both, and both were applied on this
branch. E1 went further than the suggestion: `rm -rf /tmp/_MEI*` is a **command in
the block**, not a trailing comment, because the comment form is what had been
ignored for three sessions. The figure reads `~2-5 min`, the measured range, rather
than a point value — see §6, which is why.

## 4. Recorded, not acted on

**The `tmp/` → `scratchpad/` ruling was implemented as a split, not a rename.**
[Design D3](../design/docs-reorganization.md) says `tmp/` is *renamed* `scratchpad/`
and `.gitignore` *becomes* `scratchpad/*`. What shipped keeps **both**: `tmp/` stays
ignored for `hack/ytdl-dev.sh` build output, and `scratchpad/*` is added beside it.

**The implementation is right and the ruling was wrong** — a straight rename would
have un-ignored `tmp/dev/`, leaving cross-compiled Mach-O binaries as untracked
files and feeding them to a checker that enumerates untracked files. The outcome is
documented where it counts: `.gitignore` comments, `scratchpad/README.md`'s closing
line, and [dev-testing.md](../../guides/dev-testing.md).

Recorded here because the **design still reads as if `tmp/` were gone**, and that
document is historical — it is not edited to match. This note is the correction.

## 5. What was checked and found correct

Worth naming, because these were the places most likely to have rotted:

- **The glossary** honours its content contract exactly — all 18 seeded terms
  present, each one sentence, each pointing at the document that owns it.
- **`improvements.md`** is accurate as rewritten, including its boundary paragraph
  with the roadmap. Its `V19` entry still reproduces: `internal/update`'s package
  comment does claim a `config` import the package does not have.
- **The 16 ADRs** are all listed in the index with the right domain, and the
  numbering is one unbroken global sequence.
- **`releasing.md`** absorbed both *Lesson learned* blocks without losing a word,
  and gained the ordering diagram the roadmap never had.
- **`project-profile.md`** and **`maintenance.md`** match the design's recorded
  values and are honest where honesty costs something — §1's refusal to take `O` on
  `Design × Feature`, and §8's record that gate C is not a cell the oracle replaces.
- **The four Cycle 6-plus reviews and the Cycle 5 one** are intact and were not
  edited, which is what a historical document requires.

<a id="dev1"></a>

## 6. A defect that is not documentation — the unexplained part of `V25`

**Out of this review's scope, recorded here because this is where it surfaced.** It
is scheduled as [`DEV-1`](../../roadmap.md#dev-1); nothing was changed in code.

Correcting §2.1's ~12 s figure meant asking *why* the suite got slow, and the answer
on file does not add up. `V25`'s recorded cause is
[review 004](../../distribution/reviews/004-cycle6plus-gate-c.md#V25): yt-dlp is a
PyInstaller one-file bundle that unpacks into `/tmp/_MEI…` and removes it **on its
own exit**, and `versionTimeout` — 30 s since `V20`'s fix — makes the tests kill it
first. Review 004 measured the unit cost precisely: allowed to finish, 0 directories;
killed after 0.4 s, 1 directory of ~50 MB.

But the suite execs yt-dlp **three** times per run — once in `TestRealMainStatusNoDaemon`,
twice in `TestUpdaterEnabledFollowsTheResolvedSetting`'s `[]bool{true,false}` loop.
Three extractions is ~150 MB. **1657 were measured, 77 GB.**

### The candidate

`TestRealMainQueueListsEnqueued` (`cmd/ytdl/main_test.go`) enqueues a job and calls
`realMain(["queue"])`. That reaches `printQueueOnce` → `resumeIfStalled`
(`cmd/ytdl/main.go`), which sees one pending job and no daemon holding the lock, and
calls `daemon.Spawn()`. `spawn` (`internal/daemon/daemon.go`) re-execs
`os.Executable()` — **under `go test`, that is the test binary** — detached with
`Setsid` and `Release()`d. Nothing in the package intercepts the `__daemon`
argument: there is no `TestMain`, and `flag.Parse` ignores a positional. So the child
runs the whole `cmd/ytdl` package again, with none of `go test`'s timeouts and
outliving the parent, and reaches the same test about a second in.

### Why it is worth taking seriously

- **The tree already knows.** `TestRunRetryCmdRequeues` takes the daemon flock
  deliberately, commented *"Hold the daemon lock so `resumeIfStalled` sees a live
  daemon and does NOT self-exec a real one from the test binary."* The hazard was
  found before; **one** test was guarded.
- **The seam is asymmetric.** `run.spawnDaemon` and `spawnQueueDaemon` are both
  stubbable variables, and tests do stub them. `daemon.Spawn` inside `resumeIfStalled`
  is called straight on the package and cannot be.
- **It explains what the recorded cause does not**: the three-orders-of-magnitude
  gap, the near-2× spread between two same-day measurements of the same suite (§2.1),
  and the 11,442 directories once found accumulated *across* sessions — orphans
  nothing ever kills.

### What this is not

⚠️ **It is code reading, not a measurement.** By the time it surfaced, this session
had no working shell: `V25` had taken the container's overlay read-only mid-review,
and `rm` could not clear it from inside while the host could not reach it — the
maintainer recovered by rebuilding the image. So the chain above is falsifiable and
unfalsified. One command settles it, and it is `DEV-1`'s first analysis step:

```bash
go test -race -count=1 ./cmd/ytdl/ ; pgrep -af 'ytdl.test'
```

Processes surviving the suite confirm it. ⚠️ Run it only behind `DEV-1`'s
containment step — reproducing `V25` to confirm `V25` is how this session was lost.

## 7. Verdict

**Updated in place; merge-ready.** No finding blocks D9. The two escalations are
outside this review's write scope rather than unresolved questions, and both are
one-line edits to a file the maintainer owns.

The reorganization did what it said: containers moved, contents survived, and the
gate it introduced is green. The contents that were *already* stale before it began
are now corrected — which is the half of the work D1–D7 explicitly left open.
