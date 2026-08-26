# Review 004 — Cycle 6-plus, gate C on real hardware

> **Provenance.** Extracted verbatim on 2026-08-26 from `docs/improvements.md` (lines 577–1469).
> Four sittings by hand on the maintainer's Mac: findings `V20`–`V29`, three of
> them blocking, and the gate's outcome. The record that measured what a green
> suite could not see.

<a id="cycle6plus-gatec"></a>

## Gate-C findings — Cycle 6-plus, by hand on real hardware (2026-08-21)

The maintainer's by-hand pass, on a MacBook Pro (arm64) carrying an installation
that predates the cycle. It found in its first ten minutes what two review passes,
a green `-race` suite and 101 installer assertions did not — because the defect
needs a real yt-dlp on a real filesystem, and the container has neither.

### V20 — a slow `yt-dlp` silently disables half the update path, and the surface says «sei aggiornato» — **BLOCKING**

**Reproduced by measurement and by execution, 2026-08-21.**

`update.toolVersion` bounds `yt-dlp --version` with `versionTimeout = 3s`
(`internal/update/install.go:26`). On the maintainer's Mac that call takes:

```
$ time ~/.local/bin/yt-dlp --version
2026.07.04
real 0m7.436s   user 0m0.611s   sys 0m0.252s   exit 0
```

**7.4 s, exit 0, stdout a clean `2026.07.04\n`, stderr empty.** The parse is fine
and the tool is fine; ytdl simply gives up before it answers.

**Corrected 2026-08-21, same day.** This entry first said the 7.4 s applied to
*every* run. It does not, and the correction matters because it changes when the
defect bites. Only `user`+`sys` = 0.86 s of that 7.4 s was work; the rest was
waiting — a cold page cache and macOS's first-run Gatekeeper verification. Once
warm, the same machine reads the version fine, which the maintainer's next
`--version` demonstrated. Measured in this container for comparison: **~800 ms**
warm, three runs in a row.

So the defect is **intermittent, not permanent**, and its trigger is a *cold*
invocation — the first after an install, a copy, a reboot, or cache eviction.
That is not a mitigation: the first `ytdl --version` a new user ever runs is cold
by definition, and so is the first startup probe after a machine is switched on.
It does mean a warm machine can look fine and hide it, which is exactly how it
survived to this point.

(Also corrected: the 11,442 leftover `/tmp/_MEI*` directories found in the
container are real, but this entry attributed them to `--version`, and that is not
established — three `--version` runs here left **none**. Their origin is an open
question for whoever looks at the container image.)

**Where the 3 s came from.** It was measured in this container — `yt-dlp
--version` costs about 650 ms here — where the bundle was already extracted, and
where the 11,442 leftover `/tmp/_MEI*` directories found on 2026-08-21 are the
evidence of how many times that had happened. The measurement was real and the
conclusion drawn from it was not: it did not describe the target platform.

**It is a regression this cycle introduced.** `main`'s `run.ShowVersion` calls
`exec.Command(ytDlp, "--version").Output()` with **no context and no timeout**,
so the released v2.1.0 waits the 7.4 s and prints the version — which is exactly
what the maintainer's terminal shows it doing, beside the branch build failing.

#### Two harms, and the second is the blocking one

**1. The surface states something untrue.** `toolVersion` returns `""` on any
failure, and `""` already means "nobody recorded a version" — the legitimate
state of an ffmpeg installed before the marker existed. So a timeout renders as:

```
yt-dlp (versione non registrata)
```

about a tool that is installed, working, and reports its version on demand.
"Nobody wrote it down" and "I asked and gave up" are different facts with
different remedies, and the surface cannot tell them apart.

**2. The comparison hole.** `Installed.YtDlp` is empty, and `appendChange`
deliberately skips a component with an empty side — "an empty side is a question
nobody answered, never a licence to guess". But `Known()` tests the **pin**, not
the installed facts, so it stays true. Executed:

```
Known()     = true
Available() = false
Changes()   = []
=> the surface renders: "sei aggiornato"
=> but the pin requires yt-dlp 2026.08.99 and nothing compared it
```

On every Mac where yt-dlp is slower than 3 s, **ytdl reports "sei aggiornato"
while structurally unable to see its own yt-dlp version.** ADR-0016 §2's whole
purpose — ytdl owns the versions it drives, and one commit to `deps.conf` reaches
the fleet — is silently inert, and §8's rule that the three states never collapse
is broken in the worst direction: a machine that cannot answer is reported as one
that is current.

#### Why nothing caught it

- The suite injects shim dependencies that answer instantly.
- `tests/test-installer.sh` is pure bash and never execs the real yt-dlp.
- Both review passes read this code and found `V2`/`V12` around it — the empty
  `YtDlp` field was examined *as a flattening question* and its timeout origin
  was never considered.
- The container cannot reproduce it: the same binary prints `yt-dlp 2026.07.04`
  here.

This is the case for a by-hand gate C, and it is worth recording as such: the
maintainer's stated reason for wanting one — "i test passano, ma voglio
verificare a mano" — was correct.

#### The fix — ratified and applied 2026-08-21, **not yet committed**

The maintainer authorised leaving the documentation phase to fix this, and
ratified part 3 below (a separate clause; the three verdict states stay three).
The change is in the working tree across `internal/update`, `internal/cli` and
`internal/webui`; see "Open cost" at the end, which must be settled before it is
committed.

1. **Remove the regression.** 3 s does not describe real hardware. The budget has
   to cover a PyInstaller bundle on a machine that may also be running antivirus,
   while still bounding a genuine hang. Measure on macOS, not here.
2. **Separate "asked and failed" from "never recorded".** A new, distinct state
   on `Dependency`, rendered as what actually happened rather than borrowed from
   the ffmpeg case.
3. **A local fact that could not be obtained must not read as "sei aggiornato".**
   Deliberately uncompared (an unattested ffmpeg, ADR-0016 §15) and
   *unobtainable* are different, and only the first may be silent. This is the
   part that needs a ruling, and probably an ADR-0016 §16.5.

An optimisation worth weighing at the same time, **not** as a substitute for the
above: the installer already records `yt_dlp_version` in `installed.conf`, and
nothing reads it. Using the marker as the fast answer for a copy that is ours,
with the exec as the authority off the critical path, would remove the 7 s from
`ytdl --version` entirely. It carries its own staleness question (a user running
`yt-dlp -U` behind ytdl's back), which is why it is a separate decision.
Deliberately **not** taken as part of this fix: it would have answered the
honesty question with a guess.

#### What was verified before the container died

Run and green, in this order:

- `go build` · `go vet` · `gofmt -l` clean.
- The new tests pass, and — the discipline this cycle exists for — they were
  **run against the code before the fix and shown to fail there**, with exactly
  the defect's own strings: `"yt-dlp (versione non registrata)\n"` and
  `"  aggiornamenti: sei aggiornato · verificato il 21/08/2026\n"`.
- `go test -race -count=1 ./...` **green, every package**.

One earlier full-suite run reported `TestRunQueuedCancelKillsProcessGroup`
failing. It is **not** a regression: it passes in isolation both with and without
the fix, and the run that failed was contending with a `sleep 300` and a shim
`yt-dlp` left behind by a `go test` the harness had killed on a timeout. Recorded
because "a test failed once" must not be discovered later and mistaken for one.

#### Open cost — settle before committing

**`cmd/ytdl` went from ~13 s to 87 s**, and `TestRealMainStatusNoDaemon` alone
accounts for ~49 s of it. The cause is this fix: that test runs `ytdl status`,
which walks the dependencies **with versions**, and a tool that never answers now
costs the full 30 s instead of 3 s. The suite is correct, just slow — but
`CLAUDE.md` advertises "~12 s" for the whole suite, and CI pays this on every
run.

The fix is to stop tests paying a production timeout: make the budget injectable
(a package variable, or a field beside `BinDirEnv`) so the suite sets something
small, while the shipped default stays generous. That was **not** done here
because the container's filesystem went read-only mid-investigation — `/tmp`
filled with yt-dlp's PyInstaller extractions again — and an unverified change is
exactly what this cycle's register keeps warning about.

### V21 — the GUI update loops: "Aggiornato. Non serve riavviare nulla." while nothing changed — **BLOCKING**

**Observed on the Mac, 2026-08-22**, in the first A1b run that got far enough to
press *Aggiorna*. The sandbox was correct: the page showed `ytdl v2.0.9` and
*Cosa cambia* `v2.0.9 → v2.2.0-rc1`, so both builds were the intended pair.

What happened instead of a handover:

1. *Aggiorna* → the panel ends at **«Aggiornato. Non serve riavviare nulla.»**
2. The versions block still reads `ytdl v2.0.9`, and the banner «È disponibile un
   aggiornamento» never goes away.
3. *Controlla ora* → the same update is offered again.
4. Pressing *Aggiorna* again during the run answers «un aggiornamento è già in
   corso»; afterwards it loops back to step 1.

So the update can be applied indefinitely and never completes.

#### What is established

«Aggiornato. Non serve riavviare nulla.» is the page's rendering of
`state == "done" && !changed`, so the installer **exited 0** and
`Run.Changed` was **false**. `shouldHandOver` requires `Changed`, so no handover
was attempted — which is consistent with everything else observed: the daemon
kept its old inode, `Installed.Ytdl` is `buildinfo.Version` of the *running*
build, and the banner is therefore correct rather than stuck.

`Changed` is derived in `Runner.finish`:

```go
if m, ok := LoadMarker(r.StateDir); ok { run.Version = m[markerYtdlVersion] }
if run.Version != "" && run.Version != buildinfo.Version { run.Changed = true }
```

Exercised, to enumerate the possibilities exhaustively:

| marker `ytdl_version` | `Changed` | handover |
|---|---|---|
| same as the running build | false | **no** |
| empty | false | **no** |
| any other value | true | yes |

So exactly two things can produce what was seen: **the installer skipped ytdl**
(marker still records the old version), or **`write_marker` recorded no version at
all**. `write_marker` fills that key with `$(ytdl_version "$INSTALL_DIR/ytdl")`,
which execs the freshly installed binary — so a binary that was replaced but could
not be run yields an empty marker value and a permanently "unchanged" update.

#### The design fragility underneath, independent of which case bit

**`Changed` is inferred from the marker rather than from what the installer did.**
The marker is written by a separate process, at the end of a run, by exec'ing the
new binary; every one of those steps can fail while the install itself succeeded.
When it does, the failure mode is not an error — it is a silent, repeatable
"nothing changed" that no surface can distinguish from a genuine no-op update.
Whatever the immediate cause turns out to be, that inference is worth revisiting:
the installer knows whether it replaced ytdl, and could say so directly.

#### The diagnostic that settles it — needs the Mac

Run **after** an *Aggiorna* that ends in «Non serve riavviare nulla», before
pressing anything else:

```bash
cat ~/.ytdl-dev/state/ytdl/installed.conf      # what ytdl_version did the marker get?
cat ~/.ytdl-dev/state/ytdl/update-run.json     # state, changed, version, pid
~/.ytdl-dev/bin/ytdl --version                 # which build is actually ON DISK now?
tail -60 ~/.ytdl-dev/state/ytdl/update.log     # did install_ytdl run, skip, or fail?
```

The four together decide it in one pass:

- `ytdl --version` reporting **v2.2.0-rc1** ⇒ the binary WAS replaced, and the
  fault is in the marker or in reading it.
- reporting **v2.0.9** ⇒ the installer never replaced it; `update.log` says
  whether it skipped (`ytdl … is already the newest`) or never reached that step.
- `installed.conf` with an empty or absent `ytdl_version` is the second case
  above, and points straight at `ytdl_version` failing on the new binary.

### V22 — immediately after an update, the page says "nessun controllo ancora eseguito"

Same session, lower severity, and possibly a consequence of `V21` rather than a
defect of its own — recorded separately so it is not lost if `V21` is fixed.

After *Aggiorna* the verdict line read:

> Aggiornamenti non verificati: nessun controllo ancora eseguito.

while the panel beside it said «Aggiornato.» That is `Runner.finish`'s
`Invalidate(r.StateDir)` doing exactly what it says — the cached verdict now
describes a machine that no longer exists, so it is discarded — but the *sequence*
a user reads is "I updated" followed by "nothing has ever been checked", which
invites precisely the conclusion the maintainer drew: that the update had not
worked.

Discarding the cache is right. What is missing is that nothing replaces it: no
round is run, and the surface has no state for "the machine just changed and has
not been re-examined". Either the finish should trigger a check, or the page
should say what actually happened. Worth deciding with `V21`, since a successful
handover reloads the page and may hide it.

### V21 — DIAGNOSED and fixed in the tree; **never once in force** — see [`V24`](#V24)

> **Read [`V24`](#V24) before this entry.** The fix below is committed locally and
> is correct. It was never pushed, and the update path fetches `install.sh` from
> `origin` — so every run on the Mac after this entry was written still executed
> the unfixed line. "Fixed" here means "fixed in the working tree", nothing more.


**Diagnosed on the Mac, 2026-08-22**, by the four commands the register asked for.
Neither of the two hypotheses above was right, which is why they were written as
hypotheses.

```
Installing yt-dlp
bash: line 541: YTDLP_TARGET<U+2026>: unbound variable
```

Line 541 was:

```bash
info "Downloading yt-dlp $YTDLP_TARGET…"
```

**macOS ships bash 3.2**, whose parser keeps reading the bytes of a multi-byte
character as part of an identifier — so the expansion names
`YTDLP_TARGET` + the three bytes of `…`, a variable that does not exist, and
`set -u` aborts. bash 5, which the container runs, stops at the first non-ASCII
byte and expands it correctly.

That is why **101 green assertions and a green `-race` suite never saw it**: the
only shell that reproduces it is the one this project never tests on. The abort
happens after `deps.conf` is read and **before anything is installed**, which
matches every observation — no marker (`installed.conf` absent), the binary
untouched at v2.0.9, `Changed` false, no handover, and the same update offered
for ever.

**Fixed** by bracing the expansion, plus a **portability check** in
`tests/test-installer.sh` that refuses the shape outright: any unbraced expansion
followed by a non-ASCII byte now fails the suite. Verified both ways — 102/0 with
the fix, 101/1 against the original line. It is a static check of the file
deliberately, because that is the part testable without the other shell.

### V23 — an installer that aborted was recorded as a successful run — **mechanism now proven**

> **Superseded in part.** The experiment this entry asks for was run in the
> container against a real bash 3.2 on 2026-08-22; the mechanism is established
> and the hardening below turns out **not** to fix it. Read the resolution
> entry two sections down before acting on anything here. The `curl.Wait()`
> line of enquiry at the end is **ruled out** — the parent never reads the pipe,
> so the ordering is harmless, and the status is lost inside bash, not in Go.


Split out of `V21` because fixing `V21` does not fix this, and it is the more
dangerous of the two.

The installer died at line 541 with a non-zero status, and the run record says:

```json
{"state":"done","exit_code":0,"changed":false}
```

So the GUI reported **«Aggiornato. Non serve riavviare nulla.»** for an install
that installed nothing. A failed update presented as a successful no-op is worse
than a failure: the failure path offers the log and *Riprova*, and this offers
neither, because nothing knows anything went wrong.

**The mechanism is NOT established.** Replicating `Runner.Start`'s exact pipeline
and `Wait` ordering in this container gives `shErr = exit status 1` and would
record `failed` — correctly. The difference has to lie in macOS's bash 3.2, and
the leading candidate is the EXIT trap: `cleanup` ended in `return 0`, and older
bash is not relied upon to preserve `$?` across a trap the way bash 5 does.

**Hardening applied, not a proven fix.** `cleanup` now captures `$?` and exits
with it, which is correct under both shells and costs nothing, and
`tests/test-installer.sh` pins the invariant that an aborted install exits
non-zero. Recorded honestly: **bash 5 preserves the status by itself, so that test
passes with or without the change** — it guards the invariant, it does not
reproduce the difference.

**The experiment that settles it**, three lines on the Mac:

```bash
bash --version | head -1
printf 'set -euo pipefail\ncleanup(){ return 0; }\ntrap cleanup EXIT\necho hi\necho "$NOPE"\n' | bash -s --
echo "exit=$?"
```

`exit=0` confirms the trap hypothesis and the hardening is the fix. `exit=1`
refutes it, and the fault is somewhere in the runner's pipeline on macOS — in
which case the next place to look is `curl.Wait()` being called before
`sh.Wait()`, which Go's own documentation calls incorrect when `StdoutPipe` is
used.

**Worth deciding either way**: the runner trusts an exit status for a question it
could verify. `install.sh` writes `installed.conf` on every completed run, so a
run that reports success without advancing the marker's `installed_at` did not
finish — an invariant the runner could check instead of inferring, and one that
would have caught this regardless of the shell.

<a id="V23"></a>

### V23 — RESOLVED as to mechanism, and the applied hardening does **not** fix it

**Established in the container, 2026-08-22**, by building **GNU bash 3.2 from
source** and running the reproduction under both shells. The experiment the
register asked the maintainer to run on the Mac is no longer needed: it was run
here, against the real shell, and it settles both halves.

#### The mechanism

**bash 3.2 enters the `EXIT` trap with `$? = 0` after a `set -u` abort.** bash 5
enters it with `$? = 1`. That is the whole difference, and it is the only case
that differs — measured:

| how the script dies | `$?` seen by the trap, and the exit status | |
|---|---|---|
| | **bash 3.2** | **bash 5.2** |
| `set -u` on an unbound variable | **0** | 1 |
| `set -e` on a failing command | 1 | 1 |
| explicit `exit 3` | 3 | 3 |
| `command not found` | 127 | 127 |

So `V21`'s abort — a `set -u` unbound variable — is the one shape that arrives at
the trap looking like success, and it is exactly the shape this installer hit.

#### The hardening is inert for precisely this case

`cleanup` was changed to `local rc=$?; …; exit "$rc"`. Under bash 3.2 that `rc`
**is 0**, so the script still exits 0. Run against the real 3.2:

| trap | bash 3.2 | bash 5.2 |
|---|---|---|
| `cleanup(){ …; return 0; }` (before) | **exit 0** | exit 1 |
| `cleanup(){ local rc=$?; …; exit "$rc"; }` (applied) | **exit 0** | exit 1 |

The change is still correct and still costs nothing — it is simply not the fix,
and the register must not carry it as one.

**The test does not reach the case either.** `tests/test-installer.sh`'s "an
aborted install exits non-zero through the EXIT trap" aborts through `fail()`,
which is an explicit `exit 1` — the row bash 3.2 gets right. It passes under
bash 3.2 today, over an installer that still loses the status.

#### A fix that is proven, under both shells

An explicit completion flag, because a zero from bash 3.2 is not evidence of
anything:

```bash
COMPLETED=0                       # set to 1 as the last line of main()
cleanup() {
  local rc=$?
  [ -n "$TMPDIR_YTDL" ] && rm -rf "$TMPDIR_YTDL"
  # bash 3.2 enters this trap with $?=0 after a `set -u` abort, so a zero here
  # is not evidence that anything succeeded. Reaching the end of main() is.
  [ "$COMPLETED" -eq 1 ] || [ "$rc" -ne 0 ] || rc=1
  exit "$rc"
}
```

Measured: aborted run → **exit 1** under 3.2 **and** 5.2; completed run → exit 0
under both. An `ERR` trap was tried as an alternative and **does not work**:
bash 3.2 does not fire `ERR` for a `set -u` abort.

**Still worth doing beside it**, unchanged from the original entry: the runner
infers success from an exit status when it could verify it. `install.sh` writes
`installed.conf` on every completed run, so a run reporting success without
advancing `installed_at` did not finish — and that check holds even for an
installer killed outright, where no trap runs at all.

<a id="V24"></a>

### V24 — the branch was never pushed, so **none of the fixes were ever under test** — **BLOCKING**

**Established in the container, 2026-08-22**, and it explains every observation in
the maintainer's 2026-08-22 session at once.

The update runner does not run the installer from the working tree. It fetches it
over the network (`internal/update/runner.go`):

```go
url := fmt.Sprintf("%s/%s/%s/install.sh", rawBase, r.slug(), r.branch())
```

— that is `raw.githubusercontent.com/<slug>/<branch>/install.sh`, i.e. **whatever
is on `origin`**. And `origin` is eight commits behind:

```
local  HEAD                                   8d3a20e
origin feat/update-path/implementation        0b33f33
```

Fetched, and compared against the working tree:

```
$ curl -fsSL .../feat/update-path/implementation/install.sh | sed -n '541p'
  info "Downloading yt-dlp $YTDLP_TARGET…"          ← V21, unfixed
$ … | grep -A1 '^cleanup'
  cleanup() { [ -n "$TMPDIR_YTDL" ] && rm -rf "$TMPDIR_YTDL"; return 0; }
                                                     ← V23 hardening, absent
```

**So every press of *Aggiorna* on the Mac downloaded and ran the pre-`V21`
installer.** It aborted at line 541 under bash 3.2 exactly as diagnosed, its trap
turned the status into 0 exactly as `V23` describes, and the GUI reported
«Aggiornato. Non serve riavviare nulla.» for an install that installed nothing —
three times in a row, with the banner never clearing, because nothing ever
changed. The «un aggiornamento è già in corso» on the second press is the
in-flight run of the press before it.

`V21` and the `V23` hardening have **never been exercised on the Mac.** Neither
has `V20`'s fix as branch code — though the `v2.2.0-rc1` release binary *does*
carry it, because the tag was pushed and points at `8b80b66`. That asymmetry is
worth naming: a **tag** was pushed from a local commit while the **branch** it
came from was not, so the release and the branch describe different code.

#### Why the existing check did not catch it

Prerequisite `P3` of the verification checklist asks whether the branch is on
`origin` and whether `deps.conf` answers `200` there. Both were true — at
`0b33f33`, pushed 2026-08-21. The check verifies that the branch **resolves**, not
that it **is current**, and eight commits landed after it was last run.

**The check has to compare content, not existence.** What settles it in one line
is whether the file the network serves is the file that is being tested:

```bash
diff <(curl -fsSL "https://raw.githubusercontent.com/alergyonthestage/ytdl/$YTDL_BRANCH/install.sh") install.sh \
  && echo "the installer under test IS the one the update path will run"
```

#### The lesson, and it is a third instance of the same one

Twice already this cycle the container answered a question truthfully while the
question was the wrong one (`V20`'s warm measurement, `V21`'s bash 5 parse). This
is the same shape once more, and the widest: **the working tree is not what the
update path runs.** For anything reached over the network — `install.sh`,
`deps.conf`, the release assets — the artefact under test is the published one,
and a local commit is invisible to it.

<a id="V25"></a>

### V25 — one `go test -race ./...` leaves 91 GB in `/tmp`, and this is what took the container down

**Measured in the container, 2026-08-22.** It also closes the open question in
`V20` about where the `/tmp/_MEI*` directories come from.

A single clean run of the suite:

```
go test -race -count=1 ./...     4 m 26 s   (cmd/ytdl alone: 252 s)
/tmp/_MEI* directories left behind:   1715
disk used:                             9.9 GB  →  100 GB
```

The mechanism, isolated:

```
yt-dlp --version, allowed to finish     → leaves 0 directories
yt-dlp --version, killed after 0.4 s    → leaves 1 directory  (~50 MB)
```

yt-dlp is a PyInstaller one-file bundle: it unpacks itself into `/tmp/_MEI…` and
removes it **on its own exit**. A process killed before that never cleans up, and
`versionTimeout` — raised to 30 s by `V20`'s fix — is what makes `cmd/ytdl`'s
tests exec and kill it in bulk.

So the "open cost" carried from `V20` is worse than recorded, and it is not only
about time:

- the handoff recorded `cmd/ytdl` at ~87 s; under `-race` it is **252 s**, and the
  whole suite **4 m 26 s** against the ~12 s `CLAUDE.md` advertises;
- **each run costs ~91 GB of disk**, which is what filled the container twice —
  once turning the filesystem read-only mid-edit, with `mktemp -d` failing and
  `tests/test-installer.sh` dying on `line 159: /SHA2-256SUMS: Permission denied`.

The remedy already identified stands and is now clearly blocking for the merge:
**make the budget injectable** (a package variable, or a field beside
`BinDirEnv`), so the tests set something small while the shipped default stays
generous. Anything that stops the tests from *killing* yt-dlp fixes both symptoms
at once. Until then, after any full suite run:

```bash
find /tmp -maxdepth 1 -name '_MEI*' -type d -print0 | xargs -0 -r rm -rf
```

<a id="bash32"></a>

### The container can run bash 3.2 — and what that does and does not buy

Built here on 2026-08-22, in about two minutes:

```bash
curl -fsSLO https://ftp.gnu.org/gnu/bash/bash-3.2.tar.gz && tar xzf bash-3.2.tar.gz && cd bash-3.2
CFLAGS="-O1 -w -std=gnu89" ./configure --build=aarch64-unknown-linux-gnu \
  --without-bash-malloc --disable-nls --disable-readline
touch y.tab.c y.tab.h        # ship-provided parser; there is no yacc/bison here
make -j4                     # -> ./bash, "GNU bash, version 3.2.0(2)-release"
```

Use `bash-3.2.57` and it will **not** build: its bundled `y.tab.c` predates the
patched `parse.y` and there is no `yacc` in the image. The unpatched 3.2 release
is self-consistent, and the semantics under test are the same.

`tests/test-installer.sh` runs clean under it — **103 passed, 0 failed**, the same
as under bash 5.

**What it catches**: everything semantic. It is what proved `V23` and what proved
the applied hardening does not fix it — neither of which any bash 5 test can show.

**What it does NOT catch, and this matters**: `V21`. The reproduction was run here
under bash 3.2 in `C`, `C.UTF-8` and `en_US.UTF-8`, and the ellipsis is parsed
correctly in every one of them. The defect is not bash 3.2's version, it is
**macOS's libc**: `legal_variable_char` is `isalpha()`, and macOS's `isalpha()`
marks high bytes alphabetic in a UTF-8 locale where glibc never does. So the
static shape check in `tests/test-installer.sh` remains the only guard for that
class, and it is the right one.

Worth making permanent in `.cco/Dockerfile` and running the installer suite under
both shells — a decision for the fix pass, not for gate C.

<a id="V26"></a>

### V26 — during an update the *Conferma* button stays live, and pressing it answers «un aggiornamento è già in corso»

**Observed by the maintainer on the Mac, 2026-08-22 and again 2026-08-23**, and
traced to its cause here. It is the defect `ux-principles.md` §5 names first — **a
control that cannot work is still offered** — and it is why the same session
pressed *Aggiorna* three times believing nothing had happened.

**The cause is one missing call.** Two different elements draw update actions:

| element | drawn by | cleared while a run is in flight? |
|---|---|---|
| `updatePanelActions` (inside the panel) | `showUpdatePanel` | **yes** — `replaceChildren(actions, [])` |
| `updateActionSlot` (in the versions block) | `renderUpdateAction` / `confirmUpdate` | **no** |

`startUpdate` posts, calls `showUpdatePanel(st)` and starts polling — and never
touches `updateActionSlot`. So the **Conferma** and **Annulla** buttons
`confirmUpdate` put there a moment earlier stay in the document, focused and
clickable, for the whole install. `renderUpdateAction` *would* clear them: it
returns empty when `updateInfo.busy`. But `updateInfo` is only refreshed by
`applyUpdate`, which runs on `loadState`, on the SSE state push and on *Controlla
ora* — and `pollUpdate` calls `loadState` only once the run reaches `done`,
`failed` or `abandoned`.

Pressing *Conferma* again therefore re-enters `startUpdate`, the server answers
`409` from `updateBlocked`, and the page shows «un aggiornamento è già in corso» —
a correct message for an action that should never have been reachable.

**One line closes it**, in `startUpdate`, after the POST succeeds:

```js
updateInfo.busy = true;
renderUpdateAction();          // the slot goes empty while the run is in flight
```

**Deferred by the maintainer to the UX cycle** (2026-08-23), together with the
larger question it raises — whether an update in flight deserves a surface of its
own rather than a panel beside controls that keep working. Recorded here so the
deferral is a decision and not an oversight, and pinned in the roadmap under
Cycle 10.

### C1 — the four ffmpeg `sha256` are attested, 2026-08-23

Closing the checklist's one **blocking** Part-C item. It was the last thing in
this cycle that had been *computed* and never *checked*.

**arm64 — attested by execution, on the target platform.** A1b ran the real
`install.sh` on the maintainer's Mac against the real server. It took the
**pinned** path, and `verify_pinned_checksum` is a refusal, not a warning: a wrong
sum aborts the install. It did not abort — the marker recorded the pinned build
and the surface reports `ffmpeg 9.0` with **no** «non verificato» qualifier, which
is only rendered when `ffmpeg_pinned = false`. Both arm64 sums are therefore
correct against the bytes upstream currently serves, and both binaries were then
**run** by `verify_install`. That is a stronger attestation than hashing by hand.

**All four — re-fetched and re-hashed 2026-08-23**, from the container, against
the live URLs `ffmpeg_url_for` builds:

```
ffmpeg_sha256_arm64_ffmpeg   = 5267ef14…73c603   [http 200, 28 440 078 bytes]
ffmpeg_sha256_arm64_ffprobe  = 7778fbb5…41d42f50  [http 200, 28 364 088 bytes]
ffmpeg_sha256_amd64_ffmpeg   = 79d14663…cf1c02c   [http 200, 33 842 767 bytes]
ffmpeg_sha256_amd64_ffprobe  = a2dd3f2e…72d3898d  [http 200, 33 741 166 bytes]
```

**Four for four against `deps.conf`, and four `200`s** — so neither build has been
withdrawn, and no sum in the file makes ytdl uninstallable. The archives were also
unpacked and identified: the amd64 pair is `Mach-O 64-bit x86_64`, the arm64 pair
`Mach-O 64-bit arm64`, so each build id resolves to the architecture it claims.

**The recorded limit stands, narrowed.** The amd64 pair still cannot be *run* on
an Apple Silicon Mac, so its attestation is of immutability and architecture, not
of behaviour — which is exactly what ADR-0016 §12 claims and all it claims. If
that ever needs closing, the cheapest route is Rosetta 2 rather than an Intel
machine: `softwareupdate --install-rosetta` and then run the unpacked binary once.

<a id="V27"></a>

### V27 — la GUI descrive lo stesso ffmpeg due volte, e una delle due è falsa — **la domanda del gate C**

**Osservato dal maintainer il 2026-08-23**, durante `A3a`. Con un ffmpeg non
attestato — cioè dopo un fallback, che è lo stato che `A3` produce apposta — il
blocco *Versione e aggiornamenti* legge:

```
ffmpeg
versione non registrata
ffmpeg
non verificato: la versione attestata non è più disponibile
```

Il marker, nello stesso istante, dice `ffmpeg_build = 1787073674_9.0.1`, e il CLI
sulla stessa macchina stampa la cosa giusta:

```
ffmpeg 9.0.1   (non verificata: la versione attestata non è più disponibile)
```

Quindi **«versione non registrata» è falso**: la versione è registrata, è nel
marker, e un'altra superficie dello stesso binario la mostra.

#### La causa, ed è la terza volta che è la stessa

La GUI prende la versione **mostrata** da `Installed`, che è la forma della
**comparazione**. `InstalledFrom` svuota `Installed.FFmpeg` quando la copia non è
attestata — deliberatamente, perché una copia non attestata deve restare
**non confrontata** (ADR-0016 §15). `buildUpdateDTO` riusa quello stesso campo
per il display, e un campo vuoto ricade su «versione non registrata».

Il CLI non ci casca perché rende da `Dependencies()` — cioè dal `Dependency`, che
ha `Version`, `Attested`, `Probed`, `Ours` e `Path` separati.

Il commento accanto a `Missing` nel DTO **nomina già la trappola**:

> `Installed` is the COMPARISON shape, so a copy we cannot vouch for carries no
> version, and without this the page called an installed ffmpeg "non installato"
> (`V12`).

`V12` era la stessa radice e fu chiusa aggiungendo `Missing`; `V20` era la stessa
radice e fu chiusa aggiungendo `Unreadable`; questa è la stessa radice sul campo
che nessuna delle due ha toccato — **la versione**. Ogni volta che un componente
diventa legittimamente non confrontato, la superficie ne perde la versione e dice
qualcosa di falso su di lui.

**La riga doppia è un secondo difetto, più lieve**: lo stesso strumento compare
due volte nella lista, una con un valore e una con un avviso. Il CLI lo dice in
una riga sola perché l'avviso è una *qualifica* della versione, non un'altra voce.

#### Il rimedio

Prendere la versione mostrata da `u.Deps()` invece che da `v.Installed`, in
`buildUpdateDTO` — `Deps()` è già chiamata lì accanto, per `Missing`. `Installed`
resta puramente la forma della comparazione, che è ciò che deve essere. Poi la
riga di `unattested` diventa una qualifica della riga di ffmpeg invece di una voce
a sé, come nel CLI.

**Blocca il gate secondo la regola del progetto** (`ux-principles.md` §5, e il
punto 5 dei non-negoziabili di `CLAUDE.md`): una superficie non afferma mai il
falso. È una decisione del maintainer se correggerlo adesso o rinviarlo con la
motivazione a verbale — ma non può diventare «verificato» per silenzio.

<a id="V28"></a>

### V28 — un'installazione che non installa niente costa 45 secondi

**Misurato dal maintainer il 2026-08-23**, in `A2`, cioè il run di idempotenza:

```
real    0m44.675s     user 0m3.801s     sys 0m1.444s
```

Il run è **corretto** — salta tutti e tre i componenti, non scarica niente, arriva
a `✓ Done.`. Ma `user`+`sys` sono 5,2 s dei 44,7: **il resto è attesa**, e la
maggior parte è lo stesso identico `yt-dlp --version` rieseguito da capo.

Contati sul codice, in un run in cui tutto è già corrente, `yt-dlp` viene eseguito
**sette volte** e `ytdl --version` **quattro** (ognuna delle quali ne esegue una
di yt-dlp):

| dove | esecuzioni |
|---|---|
| `ytdlp_is_current` → `tool_version` | 1 |
| `ytdl_is_current` → `ytdl_version` → `ytdl --version` | 2 |
| `verify_install` → `ytdl --version`, `yt-dlp --version`, `ffmpeg -version` | 4 |
| `write_marker` → `ytdl_version` + `tool_version` | 3 |

È lo stesso costo di [`V25`](#V25) visto dall'altro lato: là riempie `/tmp` nella
suite, qui sono 45 secondi davanti a cui **sta una persona non tecnica**, mentre
[ADR-0016 §11](../decisions/0016-cycle6plus-update-path.md) promette che
«l'aggiornamento comune dura secondi». Non è falso ciò che la GUI dice, quindi non
è un difetto di onestà — è la promessa di progetto che non regge sul ferro vero.

**Il rimedio era già stato individuato e rinviato**, in `V20`: `installed.conf`
registra `yt_dlp_version` e **nessuno lo legge**. Usare il marker come risposta
veloce per una copia che è nostra, tenendo l'exec come autorità fuori dal percorso
critico, toglierebbe quasi tutti e sette gli exec. Porta con sé la sua domanda di
staleness (un utente che lancia `yt-dlp -U` alle spalle di ytdl), che è il motivo
per cui fu tenuta separata.

Va deciso insieme a `V25`: sono la stessa causa e la stessa correzione.

<a id="V29"></a>

### V29 — *Controlla ora* resta attivo durante un aggiornamento, e può far ricomparire *Aggiorna*

**Osservato dal maintainer il 2026-08-23**, durante `B5`, mentre un installer era
in volo:

> Rimane il bottone «Controlla ora» e dopo che ha controllato ricompare il bottone
> «Aggiorna» cliccabile.

**Quel che è certo dal codice.** `$("checkUpdate")` viene disabilitato solo per la
durata della propria richiesta e da nient'altro: nessuno lo tocca in funzione dello
stato del run. Quindi *Controlla ora* è **sempre** premibile, anche mentre
un'installazione sta sostituendo i binari — un controllo che, in quel momento, non
ha alcun senso da offrire.

E la sua risposta ridisegna l'azione: `applyUpdate` chiama `renderUpdateAction`,
che mostra *Aggiorna* quando `available && !busy`.

**Quel che NON è stabilito, e va riprodotto prima di correggere.** `busy` è
`Progress().State == "running"`, e durante il run quello è `running`, quindi il
bottone **non dovrebbe** poter ricomparire. La finestra che il codice spiega è
un'altra: fra l'istante in cui l'installer **finisce** e quello in cui la pagina si
ricarica sul binario nuovo, `busy` è già falso e `available` è ancora vero, perché
il daemon che risponde è ancora quello vecchio. Lì *Aggiorna* torna, legittimamente
secondo il codice e assurdamente per chi guarda.

Se invece è ricomparso **davvero durante** il run, allora `busy` era falso mentre
il record diceva `running`, ed è un difetto più profondo di quello descritto qui.
Da distinguere con un `curl /api/state | jq .update.busy` durante un'installazione
lunga.

**Stessa famiglia di [`V26`](#V26)**, e insieme dicono la stessa cosa: la pagina
tratta un aggiornamento in corso come uno stato *di un pannello*, mentre è uno
stato **del documento**. Il maintainer lo ha detto due volte in due sedute:

> conviene disabilitare tutto e mostrare una schermata ad-hoc quando l'utente
> avvia un update.

Rinviato al Ciclo 10 insieme a `V26` e `V27`, come una decisione sola.

<a id="cycle6plus-gatec-esito"></a>

## Gate C — esito, 2026-08-23

Quattro sedute a mano su hardware vero. **Tutto ciò che era eseguibile è stato
eseguito**, e ha prodotto dieci finding (`V19`–`V28`) che due review, una suite
verde sotto `-race` e 103 asserzioni bash non avevano raggiunto — fra cui tre
bloccanti.

### Passato

| | esito |
|---|---|
| **A1** la consegna, end to end | passata: la pagina si è ricaricata da sola su `v2.2.0-rc1` |
| **A2** installer contro la rete vera | passata al primo run; **idempotenza** passata (nessun download), costo in [`V28`](#V28) |
| **A3a** il fallback su build ritirata scatta e lo dichiara | passata: i tre avvisi, `installed (NOT verified …)`, marker `ffmpeg_pinned = false`, `ffmpeg_build = 1787073674_9.0.1` |
| **A3b** «non riesco a chiedere» non è «è stata ritirata» | **passata**, vedi sotto |
| **A3c** convergenza | passata: ffmpeg ri-scaricato **una volta**, entrambi i checksum verificati, run successivo che salta tutto; superficie tornata a `ffmpeg 9.0 (verificata con questo ytdl)` |
| **A4** un browser rende la GUI | passata per l'intero flusso di update |
| **B5** lo stato «abbandonato» | passata: il testo esatto, *Vedi il dettaglio* e *Riprova*, e *Riprova* riavvia davvero |
| **V17** il run adottato in una seconda scheda | passata: la seconda scheda dice «Aggiornamento in corso…» e non offre un secondo avvio |
| **C1** i quattro `sha256` | chiusa, arm64 attestata dall'esecuzione |
| **C2** il test di accettazione | passata: l'update è andato dalla sola GUI |

**`A3b` merita la trascrizione**, perché è servito `--force` per arrivarci e due
ricette prima erano sbagliate:

```
Reinstalling everything (--force)
Installing ffmpeg
▸ Downloading ffmpeg 1785863997_9.0…
✗ Download failed: https://ffmpeg.martin-riedl.de/download/macos/arm64/1785863997_9.0/ffmpeg.zip
  The server answered 000.
exit=1
```

Nessun fallback, stato non zero, marker intatto: **una connessione che non
risponde non degrada l'installazione a non verificata**, che è la proprietà
comprata da ADR-0016 §12.

**Cosa quell'`exit=1` prova, e cosa no.** Prova che il percorso di fallimento
funziona sul bash 3.2 vero per gli abort che `install.sh` produce
deliberatamente — cioè `fail()`, che è un `exit 1` esplicito. È esattamente la
riga che bash 3.2 già gestiva bene ([`V23`](#V23), tabella). **Non** prova
`V23`: quello riguarda l'abort da `set -u`, che nasce solo da un difetto come
`V21`. Il rischio residuo è invariato — un altro difetto di quella forma verrebbe
di nuovo registrato come successo.

### Non eseguito

- **Il secondo browser** (`A4`, seconda metà). Il flusso è stato reso in un solo
  browser. Da dichiarare, non da dare per fatto.
- **Il canary workflow** non è mai stato eseguito. Non blocca il merge.
- La coppia ffmpeg **amd64** è hashata e identificata, non eseguita: serve un Mac
  Intel o Rosetta 2 (limite già a verbale in `C1`).

### Rinviato, per decisione del maintainer del 2026-08-23

Registrato qui perché **nessuno di questi diventi «verificato» per silenzio**.

| # | cosa | dove va |
|---|---|---|
| [`V26`](#V26) | *Conferma* resta cliccabile durante l'installazione | **Ciclo 10** |
| [`V27`](#V27) | con un ffmpeg non attestato la GUI lo mostra due volte, la prima come «versione non registrata» — **falso** | **Ciclo 10** |
| [`V29`](#V29) | *Controlla ora* resta attivo durante un update e può far ricomparire *Aggiorna* | **Ciclo 10** |
| [`V23`](#V23) | l'abort da `set -u` è ancora registrato come successo; l'hardening applicato è inerte | fix pass |
| [`V25`](#V25) | una suite lascia 91 GB in `/tmp` e dura 4 m 26 s | fix pass |
| [`V28`](#V28) | un'installazione che non installa niente costa 45 s | fix pass, insieme a `V25` |
| [`V19`](#cycle6plus-gatec) · [`V22`](#cycle6plus-gatec) | commento di pacchetto inesatto · «nessun controllo ancora eseguito» subito dopo un update | cosmetico · Ciclo 10 |

**`V27` è un rinvio consapevole di una regola normativa.** `ux-principles.md` §5 e
il punto 5 dei non-negoziabili di `CLAUDE.md` dicono che una superficie non
afferma mai il falso, e «versione non registrata» su una copia la cui versione è
nel marker è falso. Il maintainer ha deciso di trattarlo con gli altri due difetti
della stessa superficie nel ciclo dedicato, perché la correzione giusta è la stessa
decisione di scope: un aggiornamento in volo è uno stato del **documento**, non di
un pannello. **La condizione perché resti accettabile è che il Ciclo 10 non parta
senza queste tre voci**, ed è per questo che sono fissate anche nella roadmap.

Vale la pena dire che `V27` si manifesta **solo** con un ffmpeg non attestato,
stato che oggi nessuna installazione ha e che si raggiunge solo dopo il ritiro di
una build a monte. Non è un attenuante permanente: upstream è già a `9.0.1`, quindi
quello stato arriverà.
