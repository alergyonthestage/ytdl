# ADR-0008 — Daemon lifecycle: long-lived, session-scoped — no always-on service

- **Status:** accepted
- **Date:** 2026-07-23
- **Context:** [ADR-0003](../../foundation/decisions/0003-engine-language-go.md) (one engine; `ytdld` owns the
  queue *and* serves the GUI), [ADR-0007](0007-cycle2b-queue-daemon.md) (the
  on-demand daemon shipped in Cycle 2B-core, whose always-on question was
  explicitly deferred to Cycle 3), [roadmap.md](../../roadmap.md) Phase 6 (GUI)
- **Refines:** [ADR-0007](0007-cycle2b-queue-daemon.md) — resolves its deferred
  "always-on daemon? revisit in Cycle 3" question.
- **Amended:** 2026-08-04, Cycle 5's closing (gate-C finding
  [G10](../../ux/reviews/001-cycle5-gate-c.md#gate-c)) — the close warning this ADR prescribes is
  reworded below. The decision is unchanged; the old wording implied that closing
  the GUI cancelled the queue, which is the opposite of what this ADR decides.
- **Amended again:** 2026-08-21, Cycle 6-plus's documentation phase — the
  lifetime rule below gains a **third keep-alive clause** (an installer this
  process launched is still running), alongside the third **exit** cause
  [ADR-0016](../../distribution/decisions/0016-cycle6plus-update-path.md) §9 had already added. Both are
  recorded in "Cycle 6-plus" below. The decision is unchanged: the daemon still
  lives only as long as it has a reason to — this names one more reason it can
  have.

## Context

Cycle 2B-core ships an **on-demand** daemon: `ytdl -b` enqueues and self-execs
`ytdld`, which drains the spool under the concurrency cap and **idle-exits** after
~20 s of continuous emptiness (ADR-0007 §3–4). There is no resident process.

Cycle 3 (the GUI, roadmap Phase 6) makes `ytdld` also serve the local web UI
(`net/http` + SSE). A web server must stay up **across the whole browsing
session** — it cannot idle-exit after 20 s while the user is still looking at the
page. ADR-0003 already frames the queue daemon and the GUI server as *one*
process; ADR-0007 called that a "genuinely long-lived service" but **deferred**
the question of *how* long-lived: is it an always-on service started at login
(a `launchd` LaunchAgent with `RunAtLoad` + `KeepAlive`), or something lighter?

Maintainer constraints (confirmed 2026-07-23):

1. **No idle process** running when `ytdl` is not being used.
2. **Installation stays simple** — no `launchd` plist installed by default
   (consistent with ADR-0003's "installation must stay simple").

## Decision

**No always-on daemon.** `ytdld` is *long-lived but session-scoped*: it lives only
as long as it has a reason to. Its lifetime is the **union** of its reasons —
two as of Cycle 3, three since Cycle 6-plus:

> **daemon alive  ⟺  (a GUI client is connected)  OR  (the queue has pending/running work)  OR  (an installer this process launched is still running)**

Equivalently, the **exit condition** is a conjunction: the daemon exits only when
the **GUI is closed AND the queue is drained AND no installer of ours is in
flight**. The third clause is Cycle 6-plus's and is explained below; everything
that follows in this section is the original two-clause rule, unchanged.
Concretely:

- **CLI-only (no GUI)** — unchanged from Cycle 2B-core: spawned on enqueue,
  idle-exits when the queue drains. This is the special case with *no* GUI client.
- **GUI open** — the daemon stays up to serve the UI, regardless of queue state.
- **GUI closed with work still queued** — the daemon **persists** (keeps draining)
  until the queue empties, then exits. The GUI **warns on close** when work remains
  queued — and the warning says that the downloads *continue* unattended, not that
  closing stops them: *"I download proseguono anche se chiudi: qui smetteresti solo
  di vederli."* (Amended 2026-08-04, see the header: the original wording here,
  *"Vuoi uscire? Hai download in coda."*, implied the opposite.)

This is a clean generalization of 2B-core's idle-exit ("queue empty for ~20 s") —
it adds an **"AND no GUI client connected"** clause to the exit test. The daemon
gains a "GUI client connected" signal (e.g. an active SSE connection / session
count) alongside the existing queue-emptiness check.

```mermaid
flowchart TD
    START["ytdld running"] --> CHECK{"exit test<br/>(polled)"}
    CHECK -->|"queue has pending/running work"| DRAIN["keep draining"] --> CHECK
    CHECK -->|"a GUI client is connected"| SERVE["keep serving UI"] --> CHECK
    CHECK -->|"our installer is still running<br/>(Cycle 6-plus)"| WAIT["keep waiting on it,<br/>so its outcome gets written"] --> CHECK
    CHECK -->|"all three false"| EXIT["release flock, exit"]
    START -.->|"explicit request:<br/>handover after an update<br/>(ADR-0016 §9)"| EXIT
```

The dotted edge is the **third exit cause**, which does not consult the exit test
at all: see "Cycle 6-plus" below.

**No `launchd` LaunchAgent** with `RunAtLoad` + `KeepAlive` (always-resident) is
installed. The default install remains plist-free.

## Cycle 6-plus — a third keep-alive clause, and a third exit cause

Added 2026-08-21. [ADR-0016](../../distribution/decisions/0016-cycle6plus-update-path.md) gives the GUI the
ability to apply an update, and that touches this ADR's rule from **both** ends.
The two are easy to confuse, so they are stated apart:

**The third exit cause** (ADR-0016 §9, ruled at gate A). The daemon may exit
because it was **asked to**, without consulting the exit test. It is the handover:
the update replaced the ytdl binary, so this process is now running the wrong one.
It is not an idle-exit — and it has to bypass the test rather than satisfy it,
because the tab watching the update *is* a connected client and would otherwise
keep this daemon alive for ever. Implemented as `handOver` in `cmd/ytdl/update.go`.

**The third keep-alive clause** (found by building it, 2026-08-18; review finding
`V1`). The daemon owes something to a party the original rule does not name: the
**record of how the update went**. `install.sh` is `setsid`'d and survives its
launcher, but only the process that launched it waits on it and writes the
outcome. With the two-clause rule, closing the tab mid-update dropped the SSE
client, the drained queue idle-exited the daemon ~20 s later, the installer
finished anyway with nobody left to notice, and `update-run.json` stayed at
`running` for ever — which then refused every subsequent update, with a reason
the user could neither see nor act on.

So the exit test gains **"OR an installer this process launched is still
running"**. Two properties keep it narrow:

- It is the **in-memory** answer (`update.Runner.Running`), not the run record on
  disk. The record describes *any* process; this clause is about *ours*. A record
  left behind by some other process must never keep this daemon alive.
- It cannot outlive the installer, because the same goroutine that waits on the
  child clears it. The stuck-`running` case it was introduced to prevent is
  additionally covered at *read* time by the `abandoned` derivation
  (ADR-0016 §16.1), so neither mechanism depends on the other holding.

Both halves are **injected** from `cmd/ytdl` — `daemon.Config.LiveClients` takes
the composed predicate — so `internal/daemon` is untouched and the parity gate
holds.

## Alternatives considered

- **Always-on service (`launchd` LaunchAgent, `RunAtLoad` + `KeepAlive`)** —
  rejected. A resident process while the tool is unused, plus a plist in the
  installer, is disproportionate for a personal music downloader. Its only unique
  benefit — "the GUI is always reachable at a fixed URL without launching
  anything" — is better served by a menu-bar / "open GUI" action that starts the
  server on demand.
- **`launchd` start-on-work (`QueueDirectories` / `WatchPaths`)** — not now, but the
  sanctioned **future opt-in**. The OS starts `ytdld` when the spool receives a
  job; it drains and exits, with **no resident process**. This covers the niche
  "resume downloads after a reboot / unattended drain" cases without `KeepAlive`.
  Deferred as an explicit opt-in, never a default.
- **Pure idle-exit, ignoring the GUI (2B-core behaviour unchanged)** —
  insufficient for Cycle 3: the web server would idle-exit after 20 s while the
  user is still viewing the UI.

## Consequences

- **Cycle 3** splits `ytdld` into `cmd/ytdld` (per ADR-0003/0007) implementing this
  lifecycle: the exit test gains the "GUI client connected" clause; a
  "downloads still queued" guard lives in the GUI's close flow.
- **The installer stays plist-free by default.** Any `launchd` integration
  (start-on-work) is a later opt-in, not part of the base install.
- **The CLI UX (Cycle 2C) is designed daemon-lifecycle-agnostic:** live work is read
  from the spool, history from the log store, daemon liveness is *informational*
  (never "daemon down = error"). The same CLI surface is correct whether the daemon
  is on-demand, session-scoped, or a future start-on-work agent — no rework across
  Cycle 3.
- **Single source of state preserved.** The spool remains the queue's truth for both
  front-ends (ADR-0003/0007); this ADR only changes *when the process lives*, not
  *where the state lives*.
