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
| [distribution.md](distribution.md) | Constraints and design for shipping to non-developer macOS users — **current focus** |
| [roadmap.md](roadmap.md) | Operational plan, phased, with status |
| [decisions/](decisions/) | Architecture Decision Records |

## Decisions

| ADR | Title | Status |
|---|---|---|
| [0001](decisions/0001-distribution-channel.md) | Distribute via a curl-based installer script | accepted |
| [0002](decisions/0002-public-repository-and-licence.md) | Public repository with a restrictive licence | accepted |

## Reading order

Start with [roadmap.md](roadmap.md) for the current state and next steps. For the
distribution work specifically, read [distribution.md](distribution.md) then
[ADR-0001](decisions/0001-distribution-channel.md).
