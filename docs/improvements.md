# Improvements & Evolutions

Findings from the initial analysis of `ytdl`, plus the evolutions requested by
the maintainer. Nothing here is implemented yet; sequencing lives in
[roadmap.md](roadmap.md).

Severity is about *impact on a non-developer user*, which is the audience the
distribution work targets.

## Correctness & robustness

| # | Finding | Severity | Notes |
|---|---|---|---|
| C1 | `-f/--format` is never validated | medium | Help advertises `mp3\|flac\|m4a\|opus\|wav`, but any string is forwarded to yt-dlp, which fails with an opaque error. A `case` guard is a three-line fix. |
| C2 | `--embed-thumbnail` on `m4a`/`mp4` needs AtomicParsley | medium | Undeclared and unchecked dependency; only `mp3` is safe with ffmpeg alone. Must be confirmed against current yt-dlp behaviour before acting. |
| C3 | Only one URL per invocation | low | `*) URL="$1"` silently overwrites: `ytdl a b` downloads only `b`, with no warning. Either loop over URLs or reject extra arguments. |
| C4 | `-b` rebuilds its argument list by hand | low | The re-exec at line 174 enumerates flags manually, so every new flag must be added in two places. A `--` forwarding scheme or an internal env marker is sturdier. |
| C5 | Failure-log filename derived from raw title | low | `tail -n1 \| tr '/:' '__'` leaves `..` and stray whitespace intact. Sanitise to a conservative charset. |

## User experience

| # | Finding | Severity | Notes |
|---|---|---|---|
| U1 | No `--version` | **high** | Nothing identifies the installed build. This blocks self-update, blocks support ("which version do you have?"), and is a prerequisite for any distribution channel. |
| U2 | No update path for `ytdl` **or** yt-dlp | **high** | yt-dlp breaks periodically when YouTube changes; a non-developer left on a stale binary sees an incomprehensible error and no way forward. Any installer must ship an update mechanism, not just a first install. |
| U3 | Dependency errors assume Homebrew | **high** | `brew install yt-dlp` is useless advice to someone without Homebrew — exactly the target audience. Messages must point at whatever the installer actually provisioned. |
| U4 | No persistent configuration | medium | Output directory is flag-or-environment only. Setting `YTDL_OUT_DIR` permanently requires editing a shell profile, which the target audience will not do. Explicitly required by the GUI work (global output dir). |
| U5 | No queue; `-b` spawns unbounded concurrency | medium | Five `-b` invocations start five simultaneous downloads. Required by the GUI work (queued executions). |
| U6 | Silent/background success is invisible | medium | No completion signal. macOS `osascript -e 'display notification …'` is a cheap, dependency-free improvement. |
| U7 | Failure logs pile up in the output directory | low | `.log` files sit next to the audio with no rotation or cleanup. A dedicated log location would keep the music folder clean. |

## Maintainability

| # | Finding | Severity | Notes |
|---|---|---|---|
| M1 | No tests | medium | Argument parsing, format validation and mode selection are testable cheaply (e.g. bats). The metadata pipeline is only meaningfully testable end-to-end against real URLs. |
| M2 | Four separate `yt-dlp` invocations | low | dry-run, background, verbose, silent/normal each build their own call. Flags drift apart over time; consolidating behind one builder reduces that risk. |
| M3 | No README, no licence | low | Needed before sharing the repository or publishing releases. |
| M4 | Interface language is Italian | — | Help text and messages are Italian, comments and identifiers partly so. Fine for the current audience; revisit only if distribution widens beyond it. |

## Requested evolutions

### E1 — Distribution (primary objective)

Ship `ytdl` to non-developer macOS users with a fast, low-friction install that
provisions yt-dlp and ffmpeg into a user-writable location, works on older macOS
(Mojave 10.14 is the stated floor), and fails with actionable messages — ideally
falling back to older or alternative builds rather than erroring out.

Design and constraints: [distribution.md](distribution.md).

### E2 — Minimal GUI (secondary)

A clickable app in `/Applications` or on the Desktop opening a small window with:

- URL input and a run button,
- queued executions with an optional background flag,
- output directory configurable per run and globally.

This depends on U4 (persistent config) and U5 (real queue) being addressed first —
the GUI is a front-end over those, not a replacement for them.
