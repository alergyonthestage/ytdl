# Review 002 — Cycle 6-plus, the fix session reviewed

> **Provenance.** Extracted verbatim on 2026-08-26 from `docs/improvements.md` (lines 403–514).
> The second pass, which reviewed the fixes themselves: findings `V10`–`V18`,
> including four regressions the first pass had introduced.

## Fix-session review — Cycle 6-plus, second pass (2026-08-18)

<a id="cycle6plus-fixreview"></a>

The session that fixed `V1`–`V9` was itself reviewed, against
`feat/update-path/implementation` at `3fa1870`, by four independent lenses (the
V1 mechanism, the V2 semantics, the installer's bash, and the surface plus
conformance). Every finding below was **reproduced by execution**; none is
reported from reading alone.

Four of them are **regressions the fix session introduced**. That is the reason
this register exists rather than a line in a commit message: a fix that creates a
defect of the class it was fixing has to be visible to the gate, not buried in
the change that caused it.

The baseline still holds — `git diff main -- internal/core/ internal/daemon/` is
empty, `internal/core` is byte-unchanged and the installer suite is 95/95 — so
nothing here touches the frozen surface either.

> **Prefix continued, not restarted.** These come from reviewing the same cycle
> before the same gate C, so they carry on the `V` sequence rather than opening a
> new one. `V1`–`V9` are the first pass; `V10`–`V18` are what fixing them cost.

### V — what the fixes broke, and what they left standing

| # | Finding | Severity | Verified cause |
|---|---|---|---|
| V10 | **A successful update reads as `abandoned` at the end of every run** | **blocker** | `finish` (`internal/update/runner.go:269`) clears `r.running` under the mutex and only writes the terminal record ~200 µs later, after `LoadMarker` and `Invalidate`. Throughout that window the record on disk still says `running`, `Running()` is already false, and `sh.Wait()` has reaped the installer so `processAlive` answers gone — every branch of `Abandoned` is satisfied for a run that succeeded. `Start` does not have the defect: it holds `r.mu` across its own `SaveRun`. **Reproduced: 1309 abandoned frames in 515504 samples over 40 runs; window measured 137–392 µs.** Three consequences, all confirmed: a GUI poll landing there takes `app.js`'s terminal `abandoned` branch, which **stops the poll timer**, prints a sentence that is false, and for a `changed` run means the one legitimate reload never happens — leaving the old script against the new server, which ADR-0016 §10 names as worse than reloading; a `POST /api/update` in the same window passes both `updateBlocked` and `Runner.Start` and **launches a second installer** while the first is still replacing binaries with `mv` (reproduced first attempt); and `settle()` returns at the *start* of the window, so `TestRunnerRunsTheInstallerAndRecordsSuccess`, `TestRunnerDetectsThatTheYtdlBinaryChanged` and `TestAFailedRunRecordsNoVersion` are genuinely flaky, not unlucky. Introduced by `98e3572`. |
| V11 | **The marker still attests bytes nothing checksummed, and this time it never heals** | **blocker** | `write_marker` is the last call in `main` (`install.sh:770`), while `extract_binary` replaces `ffmpeg` and `ffprobe` *inside* `install_ffmpeg`'s loop. Any abort in between — the ytdl asset 404s, `verify_install` fails, the disk fills, Ctrl-C — leaves a marker describing a copy that is no longer on disk, and the copy on disk is the **unverified fallback** one. Reachable today: the GUI's *Riprova* passes `--force` (`internal/update/runner.go:215`), which bypasses the skip and runs the ffmpeg path. Because the recorded build id still equals the pin, every later run then skips ffmpeg and re-asserts `ffmpeg_pinned = true`. **V3 closed the skip door; this is the second door, and unlike V3's route it converges on the lie instead of self-correcting.** The invariant V3's fix depends on — the marker never describes bytes other than the ones on disk — is not held. ADR-0016 §15, "the degradation is stated, never silent". Pre-existing; V3 made it the only remaining route to a false `true`. |
| V12 | **The GUI tells a user with a Homebrew ffmpeg that ffmpeg is not installed** | **blocker** | `renderUpdateVersions` (`internal/webui/assets/app.js:1047`) renders a falsy version as the literal `"non installato"`. `23bc7e1` correctly emptied `Installed.FFmpeg` for a foreign copy — that shape is the one built for *comparison* — and `omitempty` then drops the key from `/api/state`. The screen contradicts itself in adjacent rows: «ffmpeg — non installato» directly above «ffmpeg — non installato **da ytdl**: la versione verificata non è quella in uso», which only means anything if one is installed. The CLI on the same machine prints `ffmpeg (versione non registrata) (da /opt/homebrew/bin/ffmpeg — non installata da ytdl)`, so this is also a fresh `ux-principles.md` §7 disagreement — the very clause V2 was raised under. Two neighbouring shapes have the same symptom and are older: an **unattested** copy (since `3e140c2`) and an *ours* copy whose marker carries no `ffmpeg_build` — the state V5 says a timed-out `ffmpeg_current_build` produces. Foreign case introduced by `23bc7e1`. |
| V13 | **`serveGUI` never consults the new lifetime rule** | **blocker** | The liveness test exists **twice** in the composition root and `6dd1d5d` fixed one. `daemon.drain` reads it through `cfg.LiveClients` and gets the installer clause; `serveGUI` (`cmd/ytdl/main.go:356`) asks `srv.HasClients()` directly. On the contended-lock path — an earlier `ytdl -b` left a headless daemon holding the queue, so the GUI process serves the UI and retries the lock — clicking *Aggiorna* and closing the tab makes the process return within one second, with the `setsid`'d installer still running and nobody left to call `finish`. That is V1's symptom minus its permanence, on a path `6dd1d5d`'s own message claims to have closed ("stated once, as `daemonAlive`" — it is stated twice). |
| V14 | **`6dd1d5d`'s only real content is untested** | major | Reverting `cmd/ytdl/main.go:304` to the pre-V1 `cfg.LiveClients = srv.HasClients` leaves **the entire suite green**. `TestTheDaemonOutlivesTheUpdateItLaunched` exercises a one-line pure `||` and nothing asserts it is connected to anything, so a revert or a refactor that drops the second argument ships silently. Related: `TestAnAbandonedRunDoesNotBlockTheUpdatePath` and `TestUpdateStatusCarriesAnAbandonedRun` still pass when the derivation at `runner.go:352` is deleted — they pin the seam through a fake `Updater`, so `webui` would not notice the engine losing the state. |
| V15 | **A `StartedAt` in the future defeats the backstop entirely** | major | `Abandoned` (`internal/update/runner.go:107`) tests `now.Sub(r.StartedAt) >= StaleAfter`, which is negative and therefore never true for a future timestamp; and a record written before `V1`'s fix has no pid at all, which is exactly the population the clock half exists to rescue. A state dir restored from a backup, a clock corrected by NTP, or a VM resumed with a wrong RTC then re-creates the permanent block V1 exists to end — for a 48 h skew, two days of a GUI refusing every update. The code's own argument covers it: a record that cannot age out on the clock is the one shape that gets no benefit of the doubt, and a future start time is that same shape. |
| V16 | **The fifth panel state is unreachable from a page load, which is its canonical case** | major | `showUpdatePanel` has exactly two callers, `startUpdate` and `pollUpdate`, and `pollUpdate` is only ever scheduled by `startUpdate`; `applyUpdate`/`loadState` never call it, and `#updatePanel` is `hidden` in the markup. So of the ways a record becomes abandoned — reboot mid-install, daemon killed, backstop fires — **none reaches the user**, and the only path that does is V10's window, where the sentence is false. V1's blocking half is genuinely fixed; V1's other complaint (design §7.3: "the page says it cannot tell how it went and points at the log") is still not discharged for the cause §7.3 names. |
| V17 | **Reload the page during a real update and V1's §4 symptom returns in full** | major | `renderUpdateBanner` (`app.js:1093`) and `renderUpdateAction` (`:1112`) both test `updateInfo.busy` **before** `updateInfo.blocked`, and the two are derived from the same run state (`internal/webui/update.go:96`, `:143`) — perfectly correlated, so the `blocked` branch is unreachable while an update runs. A second tab, or any reload, then shows «È disponibile un aggiornamento» with **no control and no reason**, which is V1's own sentence verbatim (`ux-principles.md` §4). `blockedText`'s `reason === "running"` branch is dead code the server still computes and ships, and `TestTheBlockedReasonNamesTheCountInEveryCase` pins a string no user can see. |
| V18 | **The new Italian string over-claims a cause and offers no next step** | major | «Non so come sia andato questo aggiornamento: ytdl si è chiuso prima che finisse» (`app.js:1250`). The sentence refuses to guess the outcome and then guesses the **cause**: after a reboot the machine went down and took ytdl with it, after a `kill` or an OOM it was killed, after a crash it did not *close* — and in the `StaleAfter` case the installer may still be running, so ytdl neither closed nor finished. It also stops at what went wrong: the sibling `failed` branch one line below says «ytdl è rimasto quello di prima», which is what makes its *Riprova* a decision rather than a gamble, while this branch offers a `--force` reinstall to a user who cannot tell whether it is needed. |

### Deferred to a later cycle, with the reason

Recorded rather than fixed, by the maintainer's scoping decision on 2026-08-18.
None is a regression and none is reachable without an unusual precondition:

- **`-r 0-0` can restore V5's own symptom** against a front end that answers `400`
  to a ranged request (proven locally; RFC 9110 says a server *should* ignore a
  `Range` it does not support, so it needs a non-conformant CDN in front of
  `ffmpeg.martin-riedl.de`, which cannot be tested from the container). The
  in-file precedent for the same question is `latest_tag`'s `-sSI`, which
  downloads zero bytes on every server; the cheap hardening is one retry without
  the range.
- **A mixed run records a build id the installed ffmpeg is not.** If ffmpeg
  verifies at the pin and ffprobe falls back, `FFMPEG_TARGET` is overwritten for
  the pair, so `ytdl --version` shows the fallback build beside a pinned ffmpeg.
  The `ffmpeg_pinned` half stays honest. Needs a partial upstream state, both
  zips living in the same directory.
- **The cheap CLI path speaks for a yt-dlp that is absent or foreign**
  (`cmd/ytdl/main.go:186`). Structurally V2 one field over — the flattener leaves
  `YtDlp` empty and the edge re-fills it from a previous round without checking
  the round still describes the copy that is there. Fixable for free with what
  `Dependencies` already resolved; **pre-existing, fails identically on `30ae9ef`**.
- **`Dependency.Attested` no longer matches its doc comment** for a foreign copy:
  the value is right (a foreign copy is described by `Foreign`, not dragged into
  `Unattested`) but the field now means "no claim to the contrary", which is only
  meaningful when `Ours`.
- **`install_ffmpeg` is not re-entrant**: `FFMPEG_TARGET` is consumed in place by
  the fallback, so a second call in one shell would check the fallback build
  against the pinned build's sha256. A hard `fail`, not a false attestation, and
  `main` calls it once.
- **Two marker-parsing divergences between bash and Go**, both requiring a
  hand-edited marker: a duplicated key (first wins in bash, last in Go), and an
  unparseable file (the installer fails closed and re-fetches, Go fails open and
  claims attestation).
- **`pollUpdate` polls for ever on any state that is not `done`/`failed`/
  `abandoned`**, including `idle` — the state `Progress` returns for a missing or
  corrupt run file.

### What this pass confirmed sound

Recorded because a review that only lists defects tells the gate half the truth:

- **`V4`–`V9` are all fully and correctly fixed**, verified individually.
- **`V2`'s core fix is right and its evidence holds**: both new tests fail on the
  parent commit, and the CLI's rich path and the GUI produce an **identical**
  `Installed` across all 45 combinations of marker state × ffmpeg provenance ×
  yt-dlp provenance. The two channels cannot drift.
- **`V3`'s re-download converges in exactly one extra download** and creates no
  recurring obligation: the fix only bites when the marker's build equals the pin
  *and* records `false`, and in every other branch the pre-V3 build-id comparison
  already forced a re-fetch. The maintainer's single action — re-pinning — now
  terminates the degraded state instead of freezing it.
- **`processAlive` is correct**: `errors.Is` works on the bare `syscall.Errno`
  that `syscall.Kill` returns, `pid <= 0` is guarded, and EPERM answering "alive"
  is the conservative direction.
- **`--ffmpeg-location` (ADR-0016 §14.3) is untouched**, `internal/core` is
  byte-unchanged, and the parity gate is empty.
- **The marker's `true`/`false` vocabulary agrees exactly between bash and Go**
  for every value `write_marker` can actually produce, including the absent-key
  case that predates ADR-0016 §15.

### Obligations this pass hands to the documentation phase

- **ADR-0008's lifetime rule is now a three-way union.** ADR-0016 §9 recorded the
  *exit* cause it added; the new **keep-alive** clause is recorded nowhere.
- **design §7.3 now says the opposite of the code**: "the run state stays
  `running`" — it is derived as `abandoned`.
- **The `abandoned` state is GUI-only**, and `ux-principles.md` §7 requires either
  both channels or an ADR that records the asymmetry and its reason. The
  asymmetry is defensible — `ytdl --update` is synchronous and never writes a run
  record, so the CLI has no run it could fail to follow — but ADR-0016 does not
  say so.
- `StateAbandoned` and `StaleAfter` are new public API of `internal/update`;
  `docs/go-engine.md` and `CHANGELOG.md` do not mention them.
