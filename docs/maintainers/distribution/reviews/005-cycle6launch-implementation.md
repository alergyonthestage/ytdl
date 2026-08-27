# Review 005 — Cycle 6-launch, the implementation

`/review-implementation` of `L1`–`L8` on `feat/launch/implementation`, run on
**2026-08-27** against `9889d37`. It is `L9`'s first half; `/review-docs` is the
second. Historical: what it asserts is what was measured on that day.

**Verdict: fixed in place.** Two objective defects corrected on the branch, one
question escalated to the maintainer as **review needed**, four findings left as
the author's judgement. The register continues the `V` series (verification) at
**`V30`**; `V1`–`V29` belong to Cycle 6-plus.

## The baseline, verified before anything else

Every gate the roadmap names, re-run without the test cache:

| Oracle | Result |
|---|---|
| `go build ./...` · `go vet ./...` · `gofmt -l .` | clean, empty |
| `go test -race -count=1 ./...` | green, 15 packages, ~7 s |
| `bash tests/test-installer.sh` | 148/148 before the fixes, **153/153** after |
| `./hack/check-docs-links.sh` | green, 59 files |
| `git diff main -- internal/core/ internal/daemon/` | **empty**, and empty at *every* one of the ten commits |

So nothing below is a regression in the frozen surface, and nothing below was
found by the suite — which is the point recorded in
`.cco/claude/rules/project-profile.md` §8: the suite is a precondition of a
review, never its evidence.

**Two findings were reached by executing a reproduction, and a third hypothesis
was killed the same way.** That third one matters as much as the two that
survived: see *What did not turn out to be a defect* below.

## Fixed in place

<a id="v30"></a>

### `V30` — a run that installed no app still left a `YTDL.app` behind · blocker

`install_app_bundle` created `Contents/MacOS` and `Contents/Resources` **before**
it knew whether it could obtain a launcher at all. Every early return after that
point — no checksums, no launcher asset in the release, a checksum mismatch,
an unwritable target — therefore left an empty `YTDL.app` in `~/Applications`.

Reproduced, not inferred:

```
$ install_app_bundle          # with the release carrying no launcher
! Could not fetch the checksums — leaving the app as it is.
--- what is left in ~/Applications ---
Applications/YTDL.app
Applications/YTDL.app/Contents
Applications/YTDL.app/Contents/MacOS
Applications/YTDL.app/Contents/Resources
```

**Why it is a defect and not a cosmetic detail.** An empty `YTDL.app` is not "no
app". Finder treats *any* directory named `*.app` as an application: it shows it
in Applications with a generic icon and then refuses to open it. That is a
control that cannot work, which
[ux-principles §5](../../ux/design/ux-principles.md) forbids and which
[design §4.4](../design/cycle6launch-launcher.md) rules out in as many words —
*an icon must never exist for an installation that does not work*. The
[roadmap](../../roadmap.md#cycle-6-launch) states the intended outcome for this
path as installing **no app**; the code installed a husk.

**And the window is dated and certain, not hypothetical.** `install.sh` is served
from `main` while assets come from `releases/latest`, so between `L10` (merge) and
`L11` (release) the newest release carries no launcher. Every fresh install, every
`deps.conf` change and every forced `ytdl --update` in that window took this path
— which is exactly the window the roadmap already flags as a surface.

**The realignment.** The parent (`~/Applications`) is still created up front:
that is how the step learns whether it may write there at all, and it is a
directory macOS itself expects to exist. The bundle's own directories are created
only with a **verified launcher in hand**. A `mv` that fails after that point
removes only what the run created, with `rmdir` — which refuses a non-empty
directory, so rule 1 (*the bundle is never deleted*) cannot be violated by the
cleanup.

Commit `54607ef`. Five new assertions in `tests/test-installer.sh`; two of them
fail against the unfixed installer and pass against the fixed one, and one of them
pins *not yet* rather than *never* by installing the same bundle successfully once
the launcher exists.

<a id="v31"></a>

### `V31` — the launcher would alert on a launch that worked · major

`run()` derived the exit status from the *error's type*: `errors.As(err,
&ee)` for an `*exec.ExitError`, falling back to 1 when it got anything else.

`CombinedOutput` reads the child through an `os.Pipe`, so anything the child
spawned that inherited that pipe keeps `Wait` reading after the child itself is
gone. `cmd.WaitDelay` — added deliberately, so a grandchild cannot strand a Dock
tile — ends that read and returns `exec.ErrWaitDelay`, which is **not** an
`*exec.ExitError`. Measured:

```
elapsed=1.002s out="ok\n" err=exec: WaitDelay expired before I/O complete
errors.As ExitError: false      ProcessState.ExitCode()=0
>>> launcher would report exit code 1 (child actually exited 0)
```

So a child that exited 0 was reported as exit 1: a **critical alert, in Italian,
on top of a browser that had just opened**, plus a `launcher.log` line recording
a failure that did not happen. [Design §3.2](../design/cycle6launch-launcher.md)
says the launcher's exit status mirrors the child's, and Appendix C's
`TestLauncherIsSilentOnSuccess` pins the alert half of it.

**Reachability, stated honestly.** `ytdl gui` does not do this today:
`daemon.spawn` gives its child `/dev/null` stdio, and `openBrowser` leaves
`Stdout` nil, which Go also sends to `/dev/null`. The defect is one inherited
descriptor away, in code whose whole job is to spawn things — and the `WaitDelay`
line proves the author already anticipated the grandchild; only the handling of
its consequence was incomplete.

**The realignment.** `cmd.ProcessState` is what knows the child's status, and it
is nil only when the child never started. `WaitDelay` becomes a field beside
`timeout` / `now` / `showAlert`, which is what lets the regression test reach the
path in half a second instead of five.

Commit `ff7cf9b`. The new test fails against the old logic with the exact alert
text quoted above, and passes against the new one.

## Review needed — the maintainer decides

<a id="v32"></a>

### `V32` — the reconcile never reaches a page that is already open

**What is suspended:** nothing — the branch is mergeable as it stands. This is a
surface question the design did not decide, raised before it becomes a habit.

**The evidence:** `cmd/ytdl/update.go`, `cmd/ytdl/main.go:331`,
`internal/webui/assets/app.js` (every call site of `applyUpdate`),
`internal/webui/webui.go` (the SSE event names).

ADR-0019 §2 says the probe *"replaces the recorded answer with the measured one"*.
It does — **in the daemon's memory**. The page does not learn:

- `applyUpdate` has exactly **two** call sites: `applyState` — i.e. whatever
  `loadState` fetched — and the explicit *Controlla aggiornamenti* button.
  `loadState` itself runs at page load, on an SSE reconnect, and after an update
  run. Enumerated exhaustively, not sampled.
- the SSE stream carries `queue` and `progress` events only. There is no `update`
  event.

The browser opens within about a second of the port opening; `refreshLocal` costs
7.33 s on macOS. So `/api/state` is served from the **recorded** walk essentially
always, and the reconciled answer is not shown until the user reloads, reconnects,
or clicks *Controlla aggiornamenti*.

**Who this is visible to.** For the common case — yt-dlp is ours and the marker is
accurate — recorded and probed are identical and nothing changes. It bites two
populations:

| Case | Before this cycle | After, until the page refetches |
|---|---|---|
| a **foreign** yt-dlp (Homebrew, `$PATH`) | the real version, beside the "non installata da ytdl" warning | **"versione non registrata"**, beside the same warning |
| a **stale** marker (`yt-dlp -U` by hand) | the real version | the recorded one |

Neither states a falsehood — "versione non registrata" is literally true of a copy
nobody recorded, and it is the state a Homebrew ffmpeg has shown since Cycle 6-plus.
It is *less* than the page used to show, for the whole life of that page load.

**Why I did not act.** Every remedy is a decision that was never made, and each
touches a user-facing surface — which stays a human gate even mid-implementation,
so a review may not choose one:

1. **push the reconciled state to the page** — a new SSE event and a new client
   path; new capability, and it changes what the GUI does while the user watches;
2. **probe foreign copies only, on the startup walk** — contradicts
   design §5.3 and Appendix C's `TestRecordedDependenciesAskNoTool`, which pins
   *no exec on the startup path*, and reintroduces the exec this cycle removed
   whenever the user has a Homebrew yt-dlp;
3. **accept it** — the page self-corrects on reload, and gate C would show whether
   anyone notices. This costs nothing and may well be right.

**What unblocks it:** the maintainer picks one. Option 3 needs no code; it needs a
line in the design or in `improvements.md` so the next reader knows it was seen
and decided rather than missed. **Gate C item to add either way:** open the
Aggiornamenti view on a Mac with a Homebrew yt-dlp and read what it says.

## What did not turn out to be a defect

Recorded because the reasoning is what is expensive, and because it is the third
time this project has learned the same lesson.

**The launcher's `$HOME`-less fallback.** `resolveYtdl` returns the bare name
`"ytdl"` when `os.UserHomeDir()` fails, and `executableFile` then stats it — which
looked like resolving against the working directory the launcher documents that it
ignores (`/`, for a Finder launch). A guard and a test were written, and the
**mutation check refused them**: the test passed against the unguarded code too.

The reason is `exec.Command` itself — a name with no path separator goes through
`LookPath`, which searches `$PATH` and never the working directory:

```
$ exec.Command("ytdl", "gui")   # with an executable ./ytdl in the CWD
err=exec: "ytdl": executable file not found in $PATH
```

Both the guard and its test were **reverted**. A test that cannot fail is what
`rules/testing.md` calls a false positive, and shipping one would have been worse
than shipping nothing: it would have advertised a protection that Go, not this
code, provides.

⚠️ **This is the same discipline the implementation session recorded and it paid
off in the opposite direction.** That session found a false positive by mutating
its own tests; this review avoided *creating* one the same way. Three of the four
claims in this report were settled by executing something, and the fourth
(`V32`) by enumerating call sites exhaustively rather than sampling them.

## Remaining findings — the author's judgement, none blocking

| id | Severity | Where | What |
|---|---|---|---|
| `V33` | minor | `install.sh`, `APP_INSTALLED` | Its comment says it records *"whether YTDL.app is actually on this machine when the run ends"*. It records whether **this run wrote or confirmed** the bundle. A run that skipped the step because the checksums did not arrive leaves it 0 while a perfectly good app sits in `~/Applications` — so the closing message stays silent about an app that is there. Silence is the safe direction and no surface lies; the comment claims more than the flag does. |
| `V34` | minor | `install.sh`, `main()` | The closing message hardcodes *"is in your Applications folder"* while `ok` correctly prints `${dir/#$HOME/~}`. With `YTDL_APP_DIR` set — the development sandbox — the two disagree. Developer-facing only. |
| `V35` | minor | `install.sh`, `app_launcher_verified` | When no checksum tool exists at all, `sha256_of` returns empty and the warning reads *"did not match its published checksum"*, which is not what happened. The **effect** is right — nothing unverified is ever written — and macOS always ships `shasum`, so this is a wording gap on an unreachable path. |
| `V36` | nit | `cmd/ytdl-launch/main.go`, `log` | `launcher.log` appends one line per launch and is never pruned, unlike the log store, which has a retention rule. One line per double-click is negligible for years; noted so it is a decision rather than an oversight. |

## What was checked and found sound

Not a courtesy list — each of these was a candidate defect that survived the check.

- **The parity gate holds at every one of the ten commits**, not only at the tip,
  which is what the roadmap's constraint actually asks for. Measured per commit.
- **The signature assertion works on the artefact it will judge.** Cross-compiled
  both launchers with the workflow's own `-ldflags "-s -w"` and parsed the load
  commands: `arm64` carries `LC_CODE_SIGNATURE`, `amd64` does not — ADR-0019 §1
  exactly. Stripping does not cost the ad-hoc signature, which was the risk.
  (The launcher is **1.93 MB** with those flags, not the 2.88 MB the
  implementation session measured without them. ADR-0019's *"9.5 MB of engine
  doing a 30-line launcher's job"* is unaffected.)
- **The workflow parses and its steps are ordered correctly** — the assertion runs
  before the checksums, so an unsigned launcher can never reach `SHA2-256SUMS`.
- **`escapeAS` really does escape what its comment claims.** The two suspicious
  replacer entries are U+2028 and U+2029 (verified byte by byte: `342 200 250`,
  `342 200 251`), not spaces — a space there would have folded every space in the
  child's message into a newline.
- **No grandchild of `ytdl gui` holds the launcher's pipe today**: `daemon.spawn`
  sets `/dev/null` stdio explicitly and `openBrowser` leaves `Stdout` nil, which Go
  routes to `/dev/null`. This is what makes `V31` latent rather than live.
- **`startWebUI` binds with `net.Listen` before returning**, so `go
  upd.refreshLocal()` genuinely is after the port is open — the placement the whole
  cold-start remedy depends on.
- **`update.RefreshAsync` was already asynchronous**, so it is not a second thing
  sitting on the startup path.
- **`RecordedDependencies` gates on `ours`** exactly as ffmpeg has since Cycle
  6-plus, and `Dependencies(_, true)` is behaviourally unchanged —
  `TestFFmpegSemanticsUnchangedAcrossSources` asserts all three walks agree on
  ffmpeg, which is the refactor's real risk.
- **`install_app_bundle` cannot abort the install.** Every command in it is
  guarded, `||`-chained or assignment-only under `set -euo pipefail`; audited line
  by line, because `fail()` reaching this step would be `V30`'s mirror image.
- **`docs/users/guides/`** already tells the truth about the `L10`→`L11` window
  (*"Se dopo l'installazione l'app non c'è…"*), and after `V30` that sentence is
  literally correct rather than nearly so.

## Good practices worth repeating

- **Every new contract was verified against a mutation that must break it.** The
  implementation session did this for its own 11 contracts and it is why the
  `check`-in-a-subshell false positive was caught. This review used the same rule
  and it killed one of its own three hypotheses.
- **`tests/test-installer.sh` saves and restores `PASS`/`FAIL` by hand rather than
  using a subshell**, with the reason written where the next person will read it.
  That is a bug fixed *and* made unrepeatable.
- **The comments carry the measurement, not the intention.** `versionTimeout`'s
  correction does not just change a number's justification; it names which
  environment the old number described and why it never described a user's.
- **Seams instead of mocks.** `timeout`, `now`, `showAlert` — and now `waitDelay` —
  make every branch of the launcher reachable with no macOS, no `osascript` and no
  Finder, which is what let a review add a regression test in minutes.

## Where this leaves `L9`

- `/review-implementation`: **done**, findings above, two fixed on the branch.
- `/review-docs`: still to run, on the same branch. `V32`'s outcome may give it a
  line to write; `V33`–`V35` are one-line corrections it can absorb.
- The branch is otherwise ready for `L10`. `V32` is the only open question, and it
  does not block the merge.
