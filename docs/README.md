# Documentation

Documentation for `ytdl`, a yt-dlp front-end that downloads audio with clean
`Artist - Track` naming and correct ID3 tags. As of Cycle 1 it is a compiled Go
binary (see [go-engine.md](go-engine.md)); the original Bash script remains the
golden parity reference.

## For users (Italian)

| Guida | Contenuto |
|---|---|
| [guida-installazione.md](guida-installazione.md) | Installazione passo passo, per chi non ha mai usato il Terminale |
| [guida-uso.md](guida-uso.md) | Uso quotidiano, opzioni, problemi comuni |

## For maintainers

| Document | What it covers |
|---|---|
| [go-engine.md](go-engine.md) | **As-built Go engine** (Cycle 1): package layout, the parity gate, build/test/release, deliberate divergences, Cycle 2 extension points |
| [architecture.md](architecture.md) | As-built design of the original Bash script (now the golden reference): execution modes, metadata pipeline, dependencies |
| [ux-principles.md](ux-principles.md) | **Normative UX reference for both channels** (Cycle 5): the task model, the information hierarchy, the shared GUI/CLI vocabulary, action ranking, message conventions |
| [cli-reference.md](cli-reference.md) | **CLI surface reference** (Cycle 2C): design principles, command→action map, output conventions, spool-vs-log-store state model, and how to extend the CLI |
| [improvements.md](improvements.md) | Two findings registers: the initial analysis (`C`/`U`/`M`/`E`) and the **Cycle 5 gate-C findings** (`G1`–`G26`, classified `R`/`M`/`F`/`S`, mapped onto cycles) |
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

### Cycle 5 — Unified GUI/CLI UX

| Document | What it covers |
|---|---|
| [design-cycle5-ux.md](design-cycle5-ux.md) | **Approved design** (Gate B, 2026-07-27): user task model, GUI information architecture, extended history record, new API routes and their security model, CLI help restructuring, package seams, test plan, implementation order |
| [improvements.md § Gate-C findings](improvements.md#gate-c) | What the maintainer's hands-on verification returned (2026-08-03), and the cycles that answer it |

## Decisions

| ADR | Title | Status |
|---|---|---|
| [0001](decisions/0001-distribution-channel.md) | Distribute via a curl-based installer script | accepted |
| [0002](decisions/0002-public-repository-and-licence.md) | Public repository with a restrictive licence | accepted |
| [0003](decisions/0003-engine-language-go.md) | Build the engine as a single Go binary | accepted |
| [0004](decisions/0004-go-engine-package-layout.md) | Go engine package layout: reusable `internal/` core | accepted |
| [0005](decisions/0005-macos-floor-and-single-engine.md) | Raise the macOS floor to 10.15; single Go engine, no Python | accepted |
| [0006](decisions/0006-cycle2a-logs-breadcrumbs-notifications.md) | Cycle 2A: central logs, failure breadcrumbs, completion notifications | accepted |
| [0007](decisions/0007-cycle2b-queue-daemon.md) | Cycle 2B-core: filesystem queue + on-demand `ytdld` daemon | accepted |
| [0008](decisions/0008-daemon-lifecycle.md) | Daemon lifecycle: long-lived, session-scoped — no always-on service | accepted |
| [0009](decisions/0009-cycle2c-cli-ux.md) | Cycle 2C: CLI UX pass (roles, count/history redesign, in-place watch) | accepted |
| [0010](decisions/0010-cycle3-web-gui.md) | Cycle 3: web GUI — single binary, SSE progress, authenticated local API | accepted |
| [0011](decisions/0011-cycle2b-plus-cancel-retry-hardening.md) | Cycle 2B-plus: cancel/retry, job timeout, no-residue downloads | accepted |
| [0012](decisions/0012-cycle4-cli-ux-title-disambiguation.md) | Cycle 4: command/URL disambiguation and job titles in queue views | accepted |
| [0013](decisions/0013-cycle5-unified-ux.md) | Cycle 5: one task model for GUI and CLI, actionable history, safe open primitive | accepted |
| [0014](decisions/0014-ux-scope-model-and-partial-outcome.md) | Scope of a per-download choice; partial outcome as a first-class state | accepted |

## Reading order

Start with [roadmap.md](roadmap.md) for the current state and next steps. For the
distribution work specifically, read [distribution.md](distribution.md) then
[ADR-0001](decisions/0001-distribution-channel.md).
