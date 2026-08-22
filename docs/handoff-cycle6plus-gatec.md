# Handoff — Cycle 6-plus, gate C in progress

**Transient**, like the handoff it replaces (`handoff-cycle6plus-docs.md`, which
the documentation session consumed). Deleted at the cycle's close. Nothing is
decided here; everything normative is in the documents it points at.

## Where the cycle is

```mermaid
flowchart LR
  I["implementation<br/>✓"] --> R2["2 reviews + 2 fix passes<br/>✓ V1–V18"]
  R2 --> DOC["documentation<br/>✓"]
  DOC --> GC["gate C — BY HAND<br/>◀ you are here"]
  GC --> M["merge --no-ff"]
  GC -.->|"found V19–V23"| FIX["fix pass"]
  FIX --> GC
```

The documentation phase is **done**. Gate C is being run **by hand on macOS by the
maintainer**, at their explicit request ("i test passano, ma voglio verificare a
mano"), and it has already justified itself: **five findings in three sittings,
two of them blocking and both now fixed**, none of which the suite could have
reached.

**Branch `feat/update-path/implementation`, pushed, NOT merged.** `main` untouched.
Last commit at handoff: see `git log -1`.

| Check | State |
|---|---|
| `go build` · `go vet` · `gofmt -l` | clean |
| `go test -race -count=1 ./...` | green |
| `bash tests/test-installer.sh` | **103/103** (two added by the `V21` fix) |
| `git diff main -- internal/core/ internal/daemon/` | **empty** |

## Start here, in this order

1. **[verifica-cycle6plus.md](verifica-cycle6plus.md)** — the by-hand checklist.
   Read `Setup` and the five Prerequisites before anything: four assumptions that
   are *not* true by default, each of which silently invalidated an attempt.
2. **[improvements.md § Gate-C findings](improvements.md#cycle6plus-gatec)** —
   `V19`–`V23`, with what is established and what still needs the Mac.
3. **[dev-testing.md](dev-testing.md)** — the sandbox, permanent reference.

## V21 is diagnosed and fixed. V23 is what remains.

**`V21` — resolved (`70368cd`).** The maintainer ran the four diagnostic commands
and they settled it in one pass. Neither hypothesis the register carried was
right, which is why they were written as hypotheses.

`install.sh:541` was `info "Downloading yt-dlp $YTDLP_TARGET…"`. **macOS ships
bash 3.2**, whose parser keeps reading the bytes of a multi-byte character as part
of an identifier — so the expansion named `YTDLP_TARGET` plus the three bytes of
the ellipsis, a variable that does not exist, and `set -u` aborted the installer
there: after `deps.conf` was read, before anything was installed.

It explains every observation at once — no `installed.conf`, the binary untouched
at v2.0.9, `Changed` false, no handover, the same update offered for ever. And it
explains why nothing caught it: **the only shell that reproduces it is the one
this project never tests on.** bash 5 stops at the first non-ASCII byte.

Fixed by bracing, plus a portability check in `tests/test-installer.sh` that
refuses the shape outright (verified 102/0 with the fix, 101/1 against the
original line). Suite now **103/0**.

### The open one: V23 — a failed install recorded as a successful run

The installer died with a non-zero status and the run record said
`{"state":"done","exit_code":0,"changed":false}`. The GUI therefore reported
**«Aggiornato. Non serve riavviare nulla.»** for an install that installed
nothing — worse than a failure, because the failure path offers the log and
*Riprova* and this offers neither.

**The mechanism is NOT established, and the container cannot establish it.**
Replicating `Runner.Start`'s exact pipeline and `Wait` ordering here yields
`shErr = exit status 1` and would record `failed`, correctly. The difference is in
bash 3.2; the leading candidate is the EXIT trap, which ended in `return 0`.

**Hardening is applied, not a proven fix**: `cleanup` now captures `$?` and exits
with it — correct under both shells, costs nothing — and the suite pins the
invariant that an aborted install exits non-zero. Written down honestly beside the
test: **bash 5 preserves the status by itself, so that test passes with or without
the change.**

**Start the next session with this**, three lines on the Mac:

```bash
bash --version | head -1
printf 'set -euo pipefail\ncleanup(){ return 0; }\ntrap cleanup EXIT\necho hi\necho "$NOPE"\n' | bash -s --
echo "exit=$?"
```

`exit=0` confirms the trap hypothesis and the hardening is the fix. `exit=1`
refutes it, and the next place to look is `curl.Wait()` being called before
`sh.Wait()` in `Runner.Start` — which Go's own documentation calls incorrect when
`StdoutPipe` is used.

**Then re-run A1b from step 1**, with the fixed installer: publish `v2.2.0-rc1`
again (the tag was deleted), `stop`, rebuild stamped `v2.0.9`, `install`, and this
time the handover should actually happen. That is still the thing this cycle has
never done.

**Worth deciding at the same time**: the runner trusts an exit status for a
question it could verify. `install.sh` writes `installed.conf` on every completed
run, so a run reporting success without advancing the marker's `installed_at` did
not finish — an invariant the runner could check instead of inferring, and one
that would have caught this regardless of the shell.

**A `v2.2.0-rc1` release must exist on the real repo** for any of this to be
reachable; the maintainer publishes and deletes it (checklist A1b step 1 and step
9). `gh` is **not installed on the Mac either** — the release was deleted through
the GitHub web UI.

## What gate C has produced so far

| # | What | State |
|---|---|---|
| `V19` | `internal/update`'s package comment claims an import it does not have | cosmetic, **not fixed** by choice |
| `V20` | a cold `yt-dlp --version` exceeds the read budget; the surface then says «versione non registrata» and, worse, reports «sei aggiornato» while unable to compare yt-dlp at all | **fixed** (`8b80b66`) |
| `V21` | `install.sh` aborted on macOS's bash 3.2 (an unbraced expansion before `…`); the GUI update therefore looped for ever | **fixed** (`70368cd`) |
| `V22` | right after an update the verdict reads «nessun controllo ancora eseguito» | open, minor |
| `V23` | that aborted install was recorded as `done, exit 0` — a failure presented as a successful no-op | **open**, mechanism unproven, hardening applied |

**Two findings, one lesson, and it is about this container.** `V20`'s 3-second
budget came from a real 650 ms measurement taken here, where the invocation was
warm. `V21` survived 101 green assertions because bash 5 parses a line that
bash 3.2 does not. Both times the container answered a question truthfully and the
question was not the one that mattered: **it is not the target platform, and for
anything touching the shell or a cold process it cannot stand in for one.**

## Open cost, carried from the V20 fix — settle before merging

**`cmd/ytdl` went from ~13 s to 87 s**, `TestRealMainStatusNoDaemon` accounting for
~49 s. The 30-second budget is production-correct, but the suite pays it whenever
a test dependency never answers. `CLAUDE.md` advertises "~12 s" for the whole
suite.

Fix: make the budget injectable (a package variable, or a field beside
`BinDirEnv`) so tests set something small while the shipped default stays
generous. Not done because the container's filesystem went read-only mid-session.

## The environment, and the two traps that cost the most time

**The `ytdl` on the Mac's `$PATH` is the released v2.1.0**, not the branch. Two
lines of `--version` means the wrong binary. Everything runs through
`hack/ytdl-dev.sh` and a sandbox at `~/.ytdl-dev`.

- **A rebuild does not replace a RUNNING daemon.** `ytdl gui` reuses whatever is
  already listening, so the old binary keeps serving and the page keeps reporting
  the old version — which reads exactly like a build that did nothing. Use
  `hack/ytdl-dev.sh stop`. This silently invalidated a whole attempt.
- **Testing the handover needs `hack/ytdl-dev.sh install`.** The daemon re-execs
  `os.Executable()` and `install.sh` replaces `$YTDL_INSTALL_DIR/ytdl`; unless
  those are the same file the handover restarts the old build and fails on the
  60-second timeout for a reason unrelated to the code.

Container gotchas:

- git and `go build` need
  `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=/workspace/yt-download`
  on **every** invocation.
- **`/tmp` fills and takes the container down.** It reached 100 % twice in two
  sessions, the second time turning the filesystem read-only mid-edit — at which
  point even the harness could not write. The symptom does not look like a full
  disk: `mktemp -d` fails, and `tests/test-installer.sh` dies with
  `line 159: /SHA2-256SUMS: Permission denied`. Remedy:
  ```bash
  find /tmp -maxdepth 1 -name '_MEI*' -type d -print0 | xargs -0 -r rm -rf
  ```
  11,442 such directories were found the first time. **Their origin is not
  established** — three `yt-dlp --version` runs here leave none. The maintainer
  also reports the project image at ~80 GB, to be investigated after the cycle;
  note those are two different questions, since `/tmp` lives in the container's
  writable layer and not in the image.
- `gh` is not authenticated in the container, and not installed on the Mac.

## Still not exercised

Unchanged, and recorded so "gate C in progress" is not read as "verified":

- **The handover has never completed.** `V21` was why; with it fixed, A1b is the
  next thing to run, and it is still unproven.
- `install.sh` **has** now run against the real network on a real Mac (A2 is the
  first time), but the **withdrawn-build fallback** has never fired.
- The **canary workflow** has never executed.
- The four ffmpeg `sha256` in `deps.conf` are **computed, not attested**
  (checklist C1). A wrong sum aborts an install, so this must not ship unverified.
- `release.yml` ran for the first time for the `v2.2.0-rc1` tag.

## After gate C

- These findings are fixed in a code session, gate C is re-run for the parts they
  touch, and only then does the cycle merge with `--no-ff`.
- This file and `verifica-cycle6plus.md` are deleted at the close; the registers
  stay.
- The release is the maintainer's: a Mac, plus C1 attested first.
- Then **Cycle 6-launch** (the desktop launcher), from its own analysis.
