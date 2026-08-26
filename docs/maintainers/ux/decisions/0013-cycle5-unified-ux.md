# ADR-0013 — Cycle 5: one task model for GUI and CLI, an actionable history, and a safe open primitive

- **Status:** accepted
- **Date:** 2026-07-27
- **Context:** [roadmap.md](../roadmap.md) ("GUI multi-page + broader UX
  overhaul", brought forward), [ADR-0009](0009-cycle2c-cli-ux.md) (command roles
  and the spool-vs-log-store state model), [ADR-0010](0010-cycle3-web-gui.md)
  (single binary, SSE, the local-API security model),
  [ADR-0011](0011-cycle2b-plus-cancel-retry-hardening.md) (cancel/retry
  mechanics), [ADR-0012](0012-cycle4-cli-ux-title-disambiguation.md) (job titles,
  display-width clipping). Full design:
  [design-cycle5-ux.md](../design-cycle5-ux.md).
- **Refines:** [ADR-0010](0010-cycle3-web-gui.md) — lifts its "the GUI queue is
  read-only" decision and its deferred settings/job-timeout workaround.

## Context

Five cycles built capability; none designed the *surface* as a whole. The result
is a GUI that shows every control at once — one scrolling column with a
seventeen-field settings form — and a CLI whose help is a sixty-line wall. Worse,
the two channels grew different vocabularies and different capabilities: cancel
and retry exist only in the CLI, live progress only in the GUI, and **neither
tells the user where the downloaded file went or lets them open it**.

The maintainer asked for a cycle that designs both channels from the user's tasks
rather than from the existing commands, and that gives the history real actions
with a proper visual hierarchy rather than a row of equal buttons.

Four questions had to be settled.

## Decisions

### 1. One task model, one vocabulary, both channels

The design is derived from eight user tasks (design §2), and every concept gets
exactly one name and one behaviour in both channels (design §3). Where the
channels differed, the gap is closed rather than documented: the GUI gains cancel
and the open/reveal actions, the CLI gains an actionable history and a way to see
its effective configuration.

Two verbs stay deliberately distinct because they act on different objects:
**Riprova** requeues a job the spool still holds (keeping its settings snapshot
and identity), **Riscarica** creates a new job from a history record that may
long outlive the spool entry. Each appears only where its object is shown.

### 2. The history record is extended once, and identity is derived

`logstore.Entry` gains `path`, `dir`, `count`, `playlist` and a capped one-line
`error`. Without the saved path, "open the file" and "show me where it went" are
unimplementable — the runner already computes it and throws it away. The change
is made **once, covering every action in the cycle**, all fields `omitempty`, so
old records keep loading and simply offer fewer actions.

Two things are **derived rather than stored**: a record's `ID()` (a hash of its
timestamp and URL), so records written before this cycle are addressable too, and
its per-job `LogName()`, which `Record()` already composes from the same job
fields. Fewer fields, no migration, and nothing to keep in sync.

### 3. The open/reveal endpoint takes an id, never a path

An HTTP endpoint that causes `open(1)` to run is the one new attack surface. The
adversary is unchanged from ADR-0010: a web page the user visits while the GUI is
open. The client therefore sends a **history record id**; the server resolves the
path from its own store. There is no code path from request body to exec
argument. Additional constraints — the path must be absolute, exist, live under
the record's own `dir`, and (for the file target) carry one of the five audio
extensions ytdl produces — mean that even a hand-tampered `history.jsonl` cannot
turn this into "open any file". No shell is involved, and the existing token /
`Origin` / content-type guards still gate the route.

Reveal is macOS `open -R`; Linux opens the containing directory with `xdg-open`;
elsewhere the capability is advertised as false and the buttons are not rendered.

### 4. Multi-page means three client-side views, not three page loads

The GUI becomes three task-scoped views (Download · Cronologia · Impostazioni)
routed on the URL hash inside **one document that never reloads**. This is a
correctness decision as much as a UX one: `HasClients()` — the count of live SSE
connections — is the "GUI connected" clause of the daemon's exit test (ADR-0008),
so a real page load per navigation would drop it to zero between views and could
let the daemon idle-exit while the user is still working.

The asset is split into three embedded files (`index.html`, `app.css`, `app.js`)
served from an explicit allow-list. The UI roughly triples in size this cycle; a
single inline blob would be unreviewable. The split also lets the CSP tighten
from `script-src 'unsafe-inline'` to `script-src 'self'`.

## Alternatives considered

- **Separate HTML pages per view** — rejected: an SSE reconnect on every
  navigation fights ADR-0008's liveness signal for no benefit.
- **Passing the file path in the open request** — rejected: it is precisely the
  shape that turns a local API into an arbitrary-file-open primitive, the same
  class of mistake ADR-0010 caught in the enqueue path.
- **Extending `ytdl retry` to also address history records** — rejected: one
  command with two index spaces is a footgun; `again` is a separate verb with its
  own list, mirroring the GUI's separate button.
- **A writable `ytdl config set`** — deferred: it duplicates `config.Save`'s
  validation surface for a task the GUI already serves.
- **Adding fields per feature as needed** — rejected in favour of one additive
  schema change, so the history format churns once, not four times.

## Consequences

- **Parity preserved.** `internal/core` is untouched and the golden argv
  references stay byte-identical; `internal/daemon` is untouched again — GUI
  cancel reuses the spool marker its watcher already polls.
- **ADR-0010 amended:** the GUI queue is no longer read-only, and the
  `job_timeout` carry-through workaround in `handleSettings` is removed by giving
  the key a real control.
- **New surface:** `internal/open` (the only package that shells out to
  `open`/`xdg-open`) and `internal/jobs` (shared cancel/again, so the channels
  cannot drift); CLI `open`, `again`, `config`, `help <topic>` and per-command
  `--help`; four new API routes; the `open_folder_on_done` config key, at last
  implemented, foreground-only and off by default.
- **The conventions become normative and durable.** The vocabulary, the
  information hierarchy, the action-ranking rule and the message conventions are
  extracted into [ux-principles.md](../ux-principles.md), which every later cycle
  either conforms to or amends in its own ADR — including the rule that a new
  capability lands in both channels or records why it does not.
- **The history file grows** by roughly a hundred bytes per record. At the
  default 30-day retention this is immaterial; retention already governs it.
- **Records written before this cycle stay useful but offer fewer actions** —
  they have no path, so "Apri" and "Mostra nel Finder" are unavailable on them.
  This is visible in the UI as a disabled action, not as an error.
- **Deferred, recorded so they are not rediscovered:** a writable CLI config
  editor; reordering the pending queue; a click action on the completion
  notification (still impossible without a signed `.app`, U6); and the Phase-5
  hardening cycle plus the 6a AppleScript MVP, which this cycle jumped ahead of.
