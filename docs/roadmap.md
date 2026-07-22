# Roadmap

Single source of truth for planned work. Updated at the end of each cycle.

Status: `done` · `in progress` · `planned` · `deferred`

The engine is being rebuilt as a single Go binary (CLI + queue daemon + web GUI),
shelling out to yt-dlp/ffmpeg; the installer stays Bash and Python is not adopted.
See [ADR-0003](decisions/0003-engine-language-go.md) for the rationale. The
confirmed sequence is **foundations → backend integrations → robustness → GUI**,
with the GUI last.

```mermaid
flowchart LR
    P0["Phase 0<br/>repo + docs"] --> P1["Phase 1<br/>distribution (Bash)"]
    P1 --> P2["Phase 2<br/>live Bash fixes<br/>(parallel)"]
    P1 --> P3["Phase 3<br/>Go engine<br/>foundations"]
    P3 --> P4["Phase 4<br/>backend integrations<br/>(config·logs·notify·queue)"]
    P4 --> P5["Phase 5<br/>robustness &amp; tests"]
    P5 --> P6["Phase 6<br/>GUI (web on Go)"]
    P6 --> P7["Phase 7<br/>Windows / Linux"]
    P1 -. "unblocks sharing" .-> U["users on their Macs"]
```

```mermaid
flowchart LR
    subgraph legacy["Bash — shipping now"]
        BASH["ytdl script"]
    end
    subgraph target["Go — target engine"]
        GO["ytdl binary + ytdld daemon"]
    end
    BASH -->|"phase 2: cheap fixes only"| BASH
    BASH -.->|"phase 3: port with golden tests,<br/>then supersede at parity"| GO
```

## Phase 0 — Repository & documentation — `done`

| # | Item | Status |
|---|---|---|
| 0.1 | git init, baseline commit of the existing script | done |
| 0.2 | Architecture of the current script | done |
| 0.3 | Improvements and evolutions register | done |
| 0.4 | Distribution constraints analysis + ADR-0001 | done |
| 0.5 | This roadmap | done |

Documentation lives in [docs/](README.md); user-facing install instructions are
in the top-level [README](../README.md).

## Phase 1 — Distribution — `in progress` — **immediate priority**

Goal: a user with no developer tooling installs `ytdl` by pasting one line, on
any macOS from 10.13 up, and can keep it working over time.

Status: published and working. The one-liner installs cleanly on current macOS
(Apple Silicon, verified on hardware). Two things remain before calling it done:
a real end-to-end download, and a test of the legacy path on an old Intel Mac.

| # | Item | Status | Notes |
|---|---|---|---|
| 1.1 | `ytdl --version` + version constant | done | Prerequisite for update and support ([U1](improvements.md)) |
| 1.2 | `install.sh`: macOS + arch detection | done | Handles the `10.x` → `11+` scheme; covered by tests |
| 1.3 | Provision yt-dlp (binary or zipimport per target) | done | `releases/latest/download/…`, no pinned version |
| 1.4 | Verify `SHA2-256SUMS` before installing | done | Mandatory, see ADR-0001 |
| 1.5 | Provision ffmpeg per architecture | done | martin-riedl (signed) for 10.15+, evermeet for 10.13–10.14 |
| 1.6 | Install into `~/.local/bin` + PATH in `.zprofile` / `.bash_profile` | done | Idempotent; login shells are what Terminal.app starts |
| 1.7 | Actionable failure messages (no `brew install`) | done | Now point at `ytdl --update` ([U3](improvements.md)) |
| 1.8 | Guided path for macOS 10.13–10.14 (python.org 3.13) | done | Aborts with an explanation below 10.13 |
| 1.9 | `ytdl --update` (updates yt-dlp **and** the script) | done | Re-runs the installer, single provisioning path ([U2](improvements.md)) |
| 1.10 | Public GitHub repo, first release, documented one-liner | done | Published at `alergyonthestage/ytdl`; one-liner install confirmed working |
| 1.11 | Real-hardware testing | in progress | Modern path verified on Apple Silicon (below); legacy path still pending |
| 1.12 | Licence (PolyForm Strict 1.0.0) | done | Use permitted, redistribution and derivatives are not |
| 1.13 | Italian user guides (install + usage) | done | [installazione](guida-installazione.md), [uso](guida-uso.md) |

**Definition of done:** a tester who has never opened Terminal completes the
install unaided and downloads a track.

### 1.11 — what is verified, what remains

**Verified on real hardware (2026-07-22, MacBook Pro, macOS 15.6, Apple Silicon):**
the full modern path end to end — checksum verification, ffmpeg/ffprobe download
and extraction from the martin-riedl arm64 zips, the signed ffmpeg actually
running, PATH written to `.zprofile` and `.bash_profile`, and the final
`ytdl --version` / `yt-dlp` / `ffmpeg` checks all passing.

**Still unverified — the legacy path.** macOS 10.13–10.14 (Intel), with
python.org Python 3.13 driving the zipimport yt-dlp, has only been checked on
paper. This is the remaining gap in phase 1, and needs an old Intel Mac to close.

**A real download** has not been run yet either — installation succeeded, but the
end-to-end "paste a URL, get a tagged file" test is the true definition of done.

### Lesson learned — the raw CDN cache

`raw.githubusercontent.com` serves through a CDN that caches a branch path for a
few minutes. Right after a push, `.../main/install.sh` can still return the
previous version, which looks exactly like "the fix didn't work". A commit-pinned
URL (`.../<sha>/install.sh`) bypasses it. `git ls-remote` and `codeload` are
authoritative for what GitHub actually holds. Not worth engineering around —
`--update` runs rarely and the window is minutes — but worth remembering when a
fresh push appears not to have taken effect.

## Phase 2 — Live Bash fixes — `planned` (parallel, optional)

Cheap correctness fixes to the **currently shipping** Bash tool, worth doing for
real users today while the Go engine (phases 3–6) is built. Kept deliberately
small: only what is low-cost on Bash and not superseded by the port. The
substantive versions of persistent logs, notifications, config and the queue are
delivered by the Go engine, not bolted onto the Bash script.

| # | Item | Ref |
|---|---|---|
| 2.1 | Validate `--format` against the supported list | C1 |
| 2.2 | Handle multiple URLs, or reject extra arguments explicitly | C3 |
| 2.3 | Harden the `-b` re-exec argument forwarding | C4 |
| 2.4 | Sanitise failure-log filenames | C5 |
| 2.5 | Check AtomicParsley when format is `m4a`, or restrict formats | C2 |

## Phase 3 — Go engine foundations — `planned` — **next**

The pivot to the Go engine ([ADR-0003](decisions/0003-engine-language-go.md)).
Establish the binary and reach parity with the Bash `ytdl`, so it can supersede it
without regressions. This is "foundations" in the confirmed sequence.

| # | Item | Ref |
|---|---|---|
| 3.1 | Go module + CI cross-compile (macOS arm64 + Intel) + checksum publication | ADR-0003 |
| 3.2 | Installer provisions the `ytdl` binary the same way as yt-dlp/ffmpeg | ADR-0003 |
| 3.3 | Port the yt-dlp arg builder + metadata pipeline; **golden tests** vs the Bash argv | ADR-0003 |
| 3.4 | Thin CLI front-end over the shared core (parity with today's flags) | — |
| 3.5 | Persistent config file (`~/.config/ytdl/config`), strict `key=value` **parse, not `source`** | U4 |
| 3.6 | Precedence: flag > env > session override > config file > built-in default | U4 |

Useful config settings — the full list lives in [improvements.md](improvements.md#u4)
(output dir, format, name template, background-by-default, concurrency, notify,
log dir/retention, embed options, …).

## Phase 4 — Backend integrations (Go) — `planned`

The maintainer's requested backend evolutions, built on the Go core.

| # | Item | Ref |
|---|---|---|
| 4.1 | Persistent central log store (`~/.local/state/ytdl/logs/`) + retention | U7 |
| 4.2 | Optional in-destination failure breadcrumb, keyed by **hash(URL)** | U7 |
| 4.3 | Auto-cleanup: a successful download removes the stale breadcrumb for that URL | U7 |
| 4.4 | System notification on completion, toggleable (`notify`, `notify_on`) | U6 |
| 4.5 | Real queue + daemon (`ytdld`): filesystem spool with atomic state transitions | U5 |
| 4.6 | Bounded concurrency (default cap 2–3; `unlimited` allowed but discouraged) | U5 |
| 4.7 | Job status inspection: `ytdl queue [--watch]`, `status`, `cancel`, `retry` | U5 |

## Phase 5 — Robustness & tests (Go) — `planned`

Hardening of the new engine once its features exist: comprehensive error handling,
edge cases, retries/backoff, YouTube rate-limit handling, and a real test suite
(Go's `testing`, beyond the golden tests of 3.3).

## Phase 6 — GUI — `planned` (last priority, high user value)

A GUI for non-developer users. It is a **front-end over the Go engine** (config +
queue + runner already built), not a reimplementation. Runtime is settled by
ADR-0003: a web UI served by the Go daemon.

| # | Item | Notes |
|---|---|---|
| 6a | AppleScript/Automator MVP: paste/drop URL, pick folder, enqueue | zero-dep, works to Mojave, immediate value for non-devs |
| 6b | Web UI served by `ytdld`: live foreground progress, format/settings, per-run/session/global output dir, settings editor, queue + in-progress view | `net/http` + SSE; needs 3–4 done |

The settings editor writing the config file is safe precisely because the config
is parsed with a whitelist, not `source`d (3.5).

## Phase 7 — Windows / Linux — `deferred`

Out of scope until macOS distribution is proven with real users. Now mostly a
second installer and dependency path, since the Go engine cross-compiles
(`GOOS`/`GOARCH`). Notifications abstract to `notify-send` on Linux.

## Deferred / evaluated, not scheduled

- **GUI installer window (`.dmg`/`.pkg`)** — deferred. A native installer window
  without Gatekeeper friction needs paid signing ($99/yr), which is out of scope.
  The constraint-respecting alternative is a double-clickable `install.command`
  (browser-quarantined → one-time Privacy & Security override), a shell wrapper
  over the same installer. Not a priority.
- **Python as a universal dependency** — evaluated and rejected
  ([ADR-0003](decisions/0003-engine-language-go.md)): it would break the
  non-interactive install for the majority to benefit only the GUI, which Go
  serves without a runtime dependency.

## Implementation via development cycles

Phases 3–6 are executed as **sequential development cycles — one per session**.
Each session orchestrates its cycle by delegating to subagents / workflows, under
one non-negotiable rule:

> **Implementation never starts without preliminary analysis + design + an explicit
> human-in-the-loop (HITL) approval of the design.**

### Per-session protocol

```mermaid
flowchart TD
    S0["0 · Kickoff (lead, inline)<br/>read docs, restate scope + DoD"] --> S1
    S1["1 · Analysis<br/>fan-out analyst/Explore, read-only"] --> GA{"HITL gate A<br/>open questions answered?"}
    GA -->|no| S1
    GA -->|yes| S2["2 · Design (/design)<br/>interfaces, packages, tests, ADR if needed"]
    S2 --> GB{"HITL gate B<br/>design approved? (MANDATORY)"}
    GB -->|no| S2
    GB -->|yes| S3["3 · Implementation<br/>workflow/subagents, worktree-isolated,<br/>tests alongside, atomic commits"]
    S3 --> S4["4 · Review &amp; verify<br/>adversarial reviewer agents + run tests"]
    S4 --> GC{"HITL gate C<br/>changes approved?"}
    GC -->|no| S3
    GC -->|yes| S5["5 · Docs &amp; close (/commit)<br/>update roadmap status, changelog"]
```

- **No file writes before gate B.** Stages 0–2 are analysis/design only (per the
  workflow rules). Stage 3 is the first that touches implementation files.
- **Skills/agents:** `/analyze` + `analyst`/`Explore` (stage 1), `/design` (stage 2),
  `reviewer` + `/review` (stage 4), `/commit` (stage 5).
- **Agent budget — balance by complexity, soft cap ~5–10 per cycle** (the workflow
  engine caps concurrency regardless): analysis 1–3, design 1–2, implementation
  3–8 (widest), review 2–3.
- **Workflow vs subagent:** use the Workflow tool for wide deterministic fan-out /
  pipelines and adversarial review panels (requires explicit opt-in per session —
  e.g. "orchestrate this cycle with a workflow"); use individual subagents for a
  handful of independent analysis/review tasks.

### Cycle 1 — Go engine foundations & parity (phase 3) — **first**

- **Scope:** Go module + CI cross-compile + checksums; installer provisions the
  binary; port the yt-dlp arg builder + metadata pipeline with **golden tests** vs
  the Bash argv; thin CLI at flag-parity; config file (parse-not-`source`) +
  precedence.
- **Entry:** none; the Bash tool keeps shipping in parallel.
- **Done when:** the Go `ytdl` reproduces the Bash tool's behaviour (golden tests
  green), reads config, and installs through the existing installer.
- **Main risk:** porting the metadata pipeline (the crown jewel) — mitigated by the
  golden tests.

### Cycle 2 — Backend integrations (phase 4 + phase 5 hardening)

- **Scope:** central persistent logs + retention; failure breadcrumb by hash(URL)
  + auto-cleanup; toggleable notifications; queue + `ytdld` daemon (spool, atomic
  transitions) + bounded concurrency + `status`/`cancel`/`retry`; hardening + tests.
- **Entry:** Cycle 1 done (Go core + config exist).
- **Done when:** queued/background downloads run under a concurrency cap with
  inspectable status; logs persist centrally and auto-clean; notifications fire per
  config; test suite green.

### Cycle 3 — GUI (phase 6)

- **Scope:** AppleScript MVP (paste/drop URL → pick folder → enqueue), then the web
  UI served by `ytdld` (live foreground progress via SSE, format/settings,
  per-run/session/global output dir, settings editor, queue + in-progress view).
- **Entry:** Cycles 1–2 done (config + queue + runner exist).
- **Done when:** a non-developer starts a foreground download and watches progress,
  launches background downloads, sees the queue, and edits settings — all without
  the Terminal.

Phase 7 (Windows/Linux) stays deferred and is not a scheduled cycle.

## Known open questions

- Does the Python 3.13 install path actually work end-to-end on a real Mojave
  machine? Verified from documentation, not from hardware (1.11). Unchanged by
  ADR-0003: Python remains the legacy path's yt-dlp runtime, not the engine's.
- Is ffmpeg strictly required for every format we advertise, or only for cover
  embedding and some conversions? Affects whether ffmpeg can be optional (C2).
- Sequoia/Tahoe Gatekeeper specifics matter only if a `.app`/`.pkg` ships — now
  only relevant to a possible `install.command`, not the web GUI.
