# Documentation

Documentation for `ytdl`, a Bash wrapper around yt-dlp that downloads audio with
clean `Artist - Track` naming and correct ID3 tags.

## For users (Italian)

| Guida | Contenuto |
|---|---|
| [guida-installazione.md](guida-installazione.md) | Installazione passo passo, per chi non ha mai usato il Terminale |
| [guida-uso.md](guida-uso.md) | Uso quotidiano, opzioni, problemi comuni |

## For maintainers

| Document | What it covers |
|---|---|
| [architecture.md](architecture.md) | As-built design of the current script: execution modes, metadata pipeline, dependencies |
| [improvements.md](improvements.md) | Findings from the initial analysis, plus the requested evolutions |
| [distribution.md](distribution.md) | Constraints and design for shipping to non-developer macOS users |
| [roadmap.md](roadmap.md) | Operational plan, phased, with status |
| [decisions/](decisions/) | Architecture Decision Records |

### Cycle 1 — Go engine core (Phase 3)

| Document | What it covers |
|---|---|
| [go-port-parity-contract.md](go-port-parity-contract.md) | Exact behavioural spec the Go port must reproduce (flags, modes, argv, metadata pipeline, exit codes) |
| [golden-test-design.md](golden-test-design.md) | Strategy to capture the Bash argv as golden references and validate the Go port |
| [design-cycle1-core.md](design-cycle1-core.md) | **Approved core design** (Gate B): package layout, `BuildArgs`, golden tests, runner, config seam |
| [design-cycle1-remaining.md](design-cycle1-remaining.md) | **Approved remaining design** (Gate B): config file + precedence, release CI + checksums, simplified installer |

## Decisions

| ADR | Title | Status |
|---|---|---|
| [0001](decisions/0001-distribution-channel.md) | Distribute via a curl-based installer script | accepted |
| [0002](decisions/0002-public-repository-and-licence.md) | Public repository with a restrictive licence | accepted |
| [0003](decisions/0003-engine-language-go.md) | Build the engine as a single Go binary | accepted |
| [0004](decisions/0004-go-engine-package-layout.md) | Go engine package layout: reusable `internal/` core | accepted |
| [0005](decisions/0005-macos-floor-and-single-engine.md) | Raise the macOS floor to 10.15; single Go engine, no Python | accepted |

## Reading order

Start with [roadmap.md](roadmap.md) for the current state and next steps. For the
distribution work specifically, read [distribution.md](distribution.md) then
[ADR-0001](decisions/0001-distribution-channel.md).
