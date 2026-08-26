# Project: yt-download

## Overview

`ytdl` downloads audio from YouTube through yt-dlp, with clean `Artist - Track`
filenames and correct ID3 tags — the part the maintainer actually cares about.
Since Cycle 1 it is a **single Go binary** acting as three front-ends over one
core: a CLI, an on-demand queue daemon, and a local web GUI. Its audience is
**non-developer macOS users**, which is why it ships through a curl installer
that provisions yt-dlp and ffmpeg itself.

User-facing text is **Italian**; code, comments, identifiers and documentation
are **English** (`docs/users/guides/guida-*.md` are the deliberate exceptions,
by audience).

## Repositories

- `yt-download` at `/workspace/yt-download` — everything: the Go engine, the Bash
  installer, the tests and the docs. Single-repo project.

## Architecture

`cmd/ytdl` is a thin entry point over `internal/`: `core` (the argv builder — the
crown jewel), `config`, `run`, `queue` + `daemon`, `logstore`, `webui`, `jobs`,
`update`, `open`, `notify`, `term`, `cli`, `buildinfo`. See
[go-engine.md](../../docs/maintainers/engine/design/go-engine.md) for the as-built
layout and the dependency direction.

## Where to look before starting anything

The tree follows the `core-dev-framework` taxonomy: **audience → domain → type**.

| Document | Why |
|---|---|
| [`docs/README.md`](../../docs/README.md) | The index: two audiences, five domains, and every ADR |
| `docs/maintainers/roadmap.md` | **Single source of truth** for where the work stands and what comes next |
| `docs/maintainers/glossary.md` | The shared vocabulary. Use a term, or add it — never introduce a synonym silently |
| `docs/maintainers/ux/design/ux-principles.md` | **Normative** for GUI and CLI alike: conform to it, or amend it in the cycle's ADR |
| `docs/maintainers/<domain>/decisions/` | ADRs, per domain plus `foundation/` for the project-level ones. Read before re-opening a settled question |
| `docs/maintainers/engine/design/go-engine.md` · `.../ux/design/cli-reference.md` | As-built engine and CLI surface |
| `docs/maintainers/improvements.md` | Known, worth doing, **not** scheduled. Scheduled work is on the roadmap instead |
| `docs/maintainers/<domain>/reviews/` | What each review found, including the four of Cycle 6-plus |
| `docs/maintainers/handoff.md` | Where the last session left off. **Ephemeral** — nothing links to it, and it is deleted when its cycle closes |

## Key Commands

```bash
go build ./...
go test -race ./...                                  # whole suite, ~2-5 min (load-dependent)
rm -rf /tmp/_MEI*                                    # ALWAYS after the suite — V25, it has taken the disk down 3x
go vet ./... && gofmt -l .                           # gofmt output must be EMPTY
git diff main -- internal/core/ internal/daemon/     # must stay EMPTY — the parity gate
go test ./internal/core -update                      # regenerate goldens (needs the Bash ytdl)
bash tests/test-installer.sh                         # installer logic, pure bash
./hack/check-docs-links.sh                           # documentation links + ADR index
```

## Infrastructure

The toolchain is baked into a per-project container image (`.cco/Dockerfile`,
selected by `docker.image` in `.cco/project.yml`): **Go in `/usr/local/go`**, plus
**yt-dlp** fetched into `~/.local/bin` at every start by `.cco/setup.sh`.
**No ffmpeg** — real conversions and the GUI are verified by the maintainer on
macOS, never in here. If `go version` fails, the image needs rebuilding; the
command is in the header of `.cco/Dockerfile`.

Never end `.cco/Dockerfile` with `USER claude`: the cco entrypoint must start as
root and drops privileges itself via `gosu`.

## Project-Specific Instructions

Non-negotiables. Each one either cost a real bug or prevents one:

1. **`internal/core` stays byte-unchanged and `internal/daemon` untouched.** The
   golden argv tests are the parity contract with the original Bash tool. Anything
   new on the yt-dlp command line is **appended after `core.BuildArgs`**, the way
   `TempRedirectArgs` is.
2. **Open/reveal takes a record id, never a path**, and revalidates: absolute,
   exists, regular file, inside the record's own `dir`, audio extension, no shell.
3. **History records are `omitempty`; the record id and its `.log` name are
   derived, not stored.** Old records keep loading — never write a migration.
4. **The GUI is one document with hash routing and no reload** (an open SSE
   connection is the daemon's liveness clause, ADR-0008), and it uses **no
   `innerHTML`** — DOM plus `textContent` throughout. Tests enforce both.
5. **A surface never states something untrue.** No control that cannot work, no
   value displayed that is not in force, no count without its window. That is
   `ux-principles.md`, and Cycle 5's gate C exists because it slipped.
6. **Cycles run analysis → gate A → design → gate B → implementation → review →
   gate C → docs.** No implementation before gate B, and no phase advances without
   the maintainer saying so.
7. **A document goes where its audience and domain put it**, with the type as the
   leaf — never a new top-level bucket by kind. A closed unit leaves the roadmap
   for `roadmap-history.md` and keeps one line; a review report goes to its
   domain's `reviews/`; `decisions/` holds ADRs and nothing else. Historical
   documents move container, never content: a factual correction is a dated
   addition that leaves the original readable.
8. **`./hack/check-docs-links.sh` must be green before a documentation commit.**
   It also enforces that no document links *into* the ephemeral handoff, that ADR
   numbers stay unique across the per-domain folders, and that every ADR is listed
   in `docs/README.md`.
