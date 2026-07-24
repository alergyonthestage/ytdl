# Improvements & Evolutions

Findings from the initial analysis of `ytdl`, plus the evolutions requested by
the maintainer. Sequencing lives in [roadmap.md](roadmap.md).

Status since the initial analysis: **E1 (distribution) is essentially done**
(phase 1), and the engine will be rebuilt as a single Go binary — see
[ADR-0003](decisions/0003-engine-language-go.md). That decision re-homes several
findings below: the queue (U5), notifications (U6) and persistent logs (U7) are
delivered by the Go engine, and the maintainability item M2 (one yt-dlp call site)
is satisfied by the Go core rather than a Bash refactor.

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

## Backend evolution detail

<a id="u4"></a>

### U4 — persistent configuration & useful settings

A config file at `${XDG_CONFIG_HOME:-$HOME/.config}/ytdl/config`, parsed as strict
`key=value` with a **whitelist — never `source`d** (it will be written by the GUI;
sourcing user-writable shell is an injection risk). Precedence:
`flag > env > session override > config file > built-in default`.

| Group | Key | Purpose |
|---|---|---|
| Output | `output_dir` | default download directory |
| | `format` | `mp3\|flac\|m4a\|opus\|wav` (validated, C1) |
| | `audio_quality` | `0` = best |
| | `playlist_default` | download whole playlists by default |
| Naming | `name_template` | today hardcoded `%(artist,…)s - %(track,…)s`; the requested custom title formatting |
| | `strip_brackets` / `strip_tags` | cleanup regexes, today constants in the script |
| Behaviour | `background_default` | always run in background (requested) |
| | `concurrency` | max parallel downloads (see U5) |
| | `open_folder_on_done` | reveal the folder when finished |
| Notifications | `notify` / `notify_on` / `notify_sound` | see U6 |
| Logs | `log_dir` / `log_retention_days` / `breadcrumb_on_failure` | see U7 |
| Metadata | `embed_thumbnail` / `embed_metadata` / `overwrites` / `archive` | `archive` avoids re-downloading playlist items already fetched |

### U5 — queue & concurrency

A filesystem spool under `~/.local/state/ytdl/queue/{pending,running,done,failed}/`,
with **atomic `mv` state transitions** (maildir-style, no locks), driven by the Go
daemon `ytdld`. The spool *is* the state, so both the CLI and the GUI inspect it
directly. Default concurrency cap **3**: `unlimited` is allowed but discouraged
because parallel connections invite YouTube throttling/429s and saturate bandwidth
and ffmpeg CPU (a resolved concurrency that is `unlimited` or well above the default
draws an advisory warning).

**Shipped in Cycle 2B-core** ([ADR-0007](decisions/0007-cycle2b-queue-daemon.md)):
the daemon is **on-demand** — the same `ytdl` binary self-exec'd into a hidden
`__daemon` role, holding an exclusive `flock` (single instance), draining under the
cap, and idle-exiting when the queue empties; there is no always-on service or
launchd agent (deferred to the GUI cycle). `ytdl -b` enqueues (superseding the old
unbounded `Setsid` detach); `ytdl queue [--watch]` and `ytdl status` inspect the
spool, and `ytdl queue` also restarts a stalled daemon.

**Done in Cycle 2B-plus** ([ADR-0011](decisions/0011-cycle2b-plus-cancel-retry-hardening.md)):
`ytdl cancel`/`retry` (index or stable id-prefix, `--all`), a per-job execution
timeout (`job_timeout`, default off), a daemon diagnostics log, and a "no residue in
the destination" guarantee for failed/cancelled downloads. **Still deferred to a
dedicated Phase-5 cycle:** automatic retries/backoff and YouTube rate-limit handling.

### U6 — completion notification

`osascript -e 'display notification …'` on macOS — zero dependency. Config:
`notify` on/off, `notify_on=success|failure|both`, `notify_sound`. **Known limit:**
`display notification` is attributed to the calling app (Script Editor/terminal),
not "ytdl", and cannot attach a click action (e.g. open the folder) without
`terminal-notifier` (a dependency, avoided) or a signed `.app`. MVP is a plain
banner; the notification abstraction (`notify-send` on Linux later) keeps this
open/closed.

### U7 — persistent logs & failure-log auto-cleanup

Two levels: a **central persistent store** (`~/.local/state/ytdl/logs/`, every job,
retained by age) and an **in-destination breadcrumb** on failure. The maintainer's
auto-cleanup rule — "a later successful download of the same URL into the same
folder deletes the stale `.log`" — requires identifying which breadcrumb maps to
which URL. The old name derived from the *title* (fragile, C5) and did not encode
the URL. Fix: name the breadcrumb after **hash(normalised URL)** (the URL is stable
across retries, the title is not); on success, compute `hash(url)` and delete the
matching breadcrumb. Playlists: one breadcrumb per failed item, keyed by item id;
the item's success removes its own.

> **As built (Cycle 2A, [ADR-0006](decisions/0006-cycle2a-logs-breadcrumbs-notifications.md)):**
> the breadcrumb ships **on by default** (opt-out `breadcrumb_on_failure=true`), not
> off — auto-cleanup makes it self-limiting (the folder shows only *unresolved*
> failures) and it is the CLI walk-away user's only in-place signal. Its name keeps a
> readable title **plus** a `hash8` suffix, so it fixes the M2 title collision while
> preserving at-a-glance discovery. Retention is by **age** (`log_retention_days`,
> default 30; `0` = keep forever). The breadcrumb is scoped to silent/background
> modes; foreground already shows the error live.

## Requested evolutions

### E1 — Distribution (primary objective)

Ship `ytdl` to non-developer macOS users with a fast, low-friction install that
provisions yt-dlp and ffmpeg into a user-writable location, works on older macOS
(Mojave 10.14 is the stated floor), and fails with actionable messages — ideally
falling back to older or alternative builds rather than erroring out.

Design and constraints: [distribution.md](distribution.md).

### E2 — GUI for non-developer users (high value, last priority)

A GUI so users who do not know the Terminal can:

- start a **foreground** download from a link and watch live progress,
- pick format and other available settings,
- change the output directory **per run / per session / as a global setting**,
- edit user settings from the GUI (not by hand-editing the config file),
- launch **background** downloads and view the queue and in-progress jobs.

Runtime is settled by [ADR-0003](decisions/0003-engine-language-go.md): a **web UI
served by the Go daemon** (`net/http` + SSE for live progress) — no extra runtime,
no signing, no payment. Delivered in two stages (roadmap phase 6): an AppleScript
MVP (paste/drop URL, pick folder, enqueue) for immediate zero-dependency value,
then the full web UI.

The GUI is a **front-end over the engine**, not a reimplementation: it depends on
U4 (config) and U5 (queue) existing first. Editing settings from the GUI is safe
because the config is parsed with a whitelist, not `source`d (see U4).
