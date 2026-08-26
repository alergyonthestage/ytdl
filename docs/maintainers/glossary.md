# Glossary

One vocabulary for this project's documents and code. An entry is a **term**, one
sentence of what it means, and **where it is authoritative** — the document that
owns it and may change it. An entry that needs more than a sentence has a document
instead, and links to it.

Adding a term is part of writing the artifact that needed it. Introducing a synonym
for something already named here is how two names for one thing start drifting
apart; use the term, or add the new one explicitly.

## The engine

| Term | Meaning | Authoritative in |
|---|---|---|
| **front-end** | One of the three surfaces — CLI, on-demand queue daemon, web GUI — over a single shared core; not three programs | [go-engine.md](engine/design/go-engine.md) |
| **argv** | The yt-dlp argument vector `internal/core` builds; the thing the golden tests compare byte for byte | [go-port-parity-contract.md](engine/design/go-port-parity-contract.md) |
| **golden** | A recorded argv captured from the original Bash tool, kept in `internal/core/testdata` and compared NUL-delimited | [cycle1-core.md](engine/design/cycle1-core.md) |
| **parity gate** | The rule that `git diff main -- internal/core/ internal/daemon/` must come back **empty**: anything new on the yt-dlp command line is appended *after* `core.BuildArgs` | [go-engine.md](engine/design/go-engine.md) |
| **spool** | The filesystem queue holding **live** work — what is pending or running right now | [ADR-0007](engine/decisions/0007-cycle2b-queue-daemon.md) |
| **log store** | The persistent record of runs that have **finished**, and the source of history | [ADR-0006](engine/decisions/0006-cycle2a-logs-breadcrumbs-notifications.md) |
| **record id** | The identifier of a history record, **derived and never stored**; open and reveal take an id, never a path, and revalidate it | [cli-reference.md](ux/design/cli-reference.md) |
| **breadcrumb** | The file left in the destination folder when a download fails, so the failure is visible where the user was looking | [ADR-0006](engine/decisions/0006-cycle2a-logs-breadcrumbs-notifications.md) |

## The surface

| Term | Meaning | Authoritative in |
|---|---|---|
| **scope** | How far a per-download choice reaches: this **download**, this **browser tab**, or **global** — the middle one was a process field until ADR-0015 moved it into the page | [ADR-0015](ux/decisions/0015-cycle6-tab-scope-and-faithful-reruns.md) |
| **partial outcome** | A job where some items succeeded and some failed — a state in its own right, not a success with a caveat | [ADR-0014](ux/decisions/0014-ux-scope-model-and-partial-outcome.md) |
| **preset** | A named destination folder (Cycle 8). A preset is a **value**; a scope is a **duration** — they do not compete | [ADR-0015](ux/decisions/0015-cycle6-tab-scope-and-faithful-reruns.md) |
| **normative** | Said of a document a surface must conform to or amend in its cycle's ADR — in practice, `ux-principles.md` | [ux-principles.md](ux/design/ux-principles.md) |

## Distribution and updates

| Term | Meaning | Authoritative in |
|---|---|---|
| **`deps.conf`** | The pinned dependency versions and their checksums; it lives on `main` and reaches every installation **without a release** | [ADR-0016](distribution/decisions/0016-cycle6plus-update-path.md) |
| **marker** | The file the installer writes recording what it installed and whether each dependency was pinned | [ADR-0016](distribution/decisions/0016-cycle6plus-update-path.md) |
| **attested** · **unattested** | Whether a dependency copy's bytes were checksummed against `deps.conf`. An **unattested copy is UNCOMPARED, not stale** — it is excluded from the comparison rather than reported out of date | [ADR-0016](distribution/decisions/0016-cycle6plus-update-path.md) |
| **foreign** | A dependency copy ytdl did not install (Homebrew's, say): described as foreign, never dragged into *unattested* | [ADR-0016](distribution/decisions/0016-cycle6plus-update-path.md) |
| **verdict** | The cached answer to *is there an update*, and what the surfaces render | [ADR-0016](distribution/decisions/0016-cycle6plus-update-path.md) |

## How the work runs

| Term | Meaning | Authoritative in |
|---|---|---|
| **cycle** | This project's unit of work, run as analysis → gate A → design → gate B → implementation → review → gate C → docs | [roadmap.md](roadmap.md) |
| **gate A** | Approval of the analysis **direction**, which also promotes the analysis draft to the approved artifact | [roadmap.md](roadmap.md) |
| **gate B** | Approval of the design, before any implementation begins | [roadmap.md](roadmap.md) |
| **gate C** | The maintainer's hands-on verification **on real hardware**, after the implementation review. A green suite does not stand in for it | [releasing.md](distribution/guides/releasing.md) |
| **living** · **historical** · **ephemeral** | The three natures of a document: kept continuously true · written once and then frozen · consumed and then deleted | [docs-reorganization.md](process/design/docs-reorganization.md) |
| **handoff** | The one ephemeral document, bridging two sessions. It links **out** to durable documents and nothing links back to it, because it is deleted before the next one is written | [docs-reorganization.md](process/design/docs-reorganization.md) |
| **scratchpad** | The human's own gitignored working files. The agent reads them always, writes there only when asked, and never reorganizes them | [scratchpad/README.md](../../scratchpad/README.md) |
