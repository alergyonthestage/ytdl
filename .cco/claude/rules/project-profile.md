---
scope: project
---

# Project profile — yt-download

Instantiated 2026-08-26 from `knowledge/project-profile.template.md`, at the
adoption of `core-dev-framework`. This file carries the **choices**; the matrix
behind them is `knowledge/autonomy-model.md`.

It lives in `.cco/claude/rules/` because that is the directory cco mounts into every
session's context. A copy in a repo-level `.claude/` would be a policy the harness
might never read.

## 1. Autonomy

- **Preset:** `delegato` — `O` wherever the matrix allows it **and its precondition
  holds**; `U` untouched on the three cells no profile can lower.

**Cell overrides** — only what deviates from the preset, each with the precondition
that decides it:

| Phase × scope level | Value | Precondition, and whether it holds |
|---|---|---|
| Design × Feature | **`U`**, not `O` | `delegato` allows `O` only when the criteria are on disk **and solution-free**. This project's analyses are *code* and *tech-choice* analyses, never requirements analyses, so the precondition **does not hold**. The cell returns to `O` the first time a cycle produces a solution-free requirements artifact — not before |
| Impl + Test × Feature | `O` (preset) | **Holds.** Criteria are frozen in the design before implementation, and the oracle is unusually strong: `go test -race ./...`, `go vet`, `gofmt -l` empty, `tests/test-installer.sh`, `./hack/check-docs-links.sh`, and the **parity gate** — `git diff main -- internal/core/ internal/daemon/` must come back empty |
| Merge × Feature | **`U`**, not `O` | The matrix gives `O` *while unpublished* and `U` at publication. Here a merge to `main` **is** publication: `install.sh` is served from `main`, and a commit to `deps.conf` reaches every installation within a day without a tag. There is no unpublished window to exploit |
| Cleanup after merge | `O` (preset) | **Holds.** Its gate *was* the merge, and its oracle — `git branch -d`, which refuses when the commits are reachable from no other ref — is free and installed. `-D` is never used: forcing it removes the substitute and the cell belongs back on `U` |

**Never lowerable, and an override there would have no meaning:** the criteria, the
golden rule, and `review-refactoring`.

## 2. Branch strategy

- Integration branch: `main`. **Protected — never committed to directly.**
- Feature work: `feat/<scope>/<description>` · fixes: `fix/<scope>/<description>` ·
  documentation-only work: `docs/<scope>/<description>`.
- One branch per feature or fix; parallel features in separate worktrees.

## 3. Remote policy

- **Push after merge: `never — host step`.** Detected, not assumed: `gh` is not
  authenticated in the container and no credential helper is configured, so a push
  from a session cannot succeed. The agent reports the exact command and carries on.
- **Force-push: denied**, in every permission mode.
- Remote branch deletion after merge: never automatic — it is outward-facing.

➡️ The consequence is stated rather than discovered each time: **branch cleanup
always defers by one session**, because a local branch may only be dropped once the
work is on the remote. That is correct behaviour, not a failure.

## 4. Pull requests

Not required. The merge itself carries the human gate.

## 5. Changelog

- **Kept: yes** — `CHANGELOG.md` at the repo root. This project releases versioned
  artefacts to a real non-maintainer user.
- **Assembled at the release, derived from the conventional commits.** Never written
  line by line as the work goes: that duplicates `git log` with a copy that diverges.
- Format: *Keep a Changelog* with SemVer, already in force from `v2.0.0`.
- The procedure, and the two failures that fixed its order, are in
  `docs/maintainers/distribution/guides/releasing.md`.

## 6. Permission modes and execution gates

- Permission mode per phase: **`none`**.
- Execution gates as `ask` rules: **`none`** — `knowledge/enforcement.template.md`
  is not adopted.

The sessions of this project run under bypass permissions, where plan mode's blocks
are not enforced and a forked skill inherits the parent's mode. Recording a mode
would be recording a promise the harness does not keep. What holds here is the human
gates above.

## 7. Scope resolution

Single-repo project; no repo carries an override of this profile.

## 8. Waivers, and what the measurement loop already knows

A lowered cell is recorded in the next artifact with **what the substitute found**.
This project starts the loop with data rather than an assumption:

⚠️ **Cycle 6-plus's gate C, by hand on real hardware, returned ten findings — three
of them blocking — on a tree whose suite was green.** Two are outside anything an
oracle here can see:

- `V20` — a slow `yt-dlp` silently disabled half the update path while the surface
  said «sei aggiornato»;
- `V24` — the branch had never been pushed, so **none of the fixes had ever been
  under test**; the working tree is not what the update executes.

**Gate C is therefore not a cell the oracle replaces**, and no `delegato` reading of
this profile lowers it. The suite is a precondition of gate C, never its evidence.
