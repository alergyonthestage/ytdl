# Documentation

Documentation for `ytdl`, a Bash wrapper around yt-dlp that downloads audio with
clean `Artist - Track` naming and correct ID3 tags.

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

## Reading order

Start with [roadmap.md](roadmap.md) for the current state and next steps. For the
distribution work specifically, read [distribution.md](distribution.md) then
[ADR-0001](decisions/0001-distribution-channel.md).
