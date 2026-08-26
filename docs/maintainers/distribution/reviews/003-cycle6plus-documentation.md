# Review 003 — Cycle 6-plus, the documentation phase

> **Provenance.** Extracted verbatim on 2026-08-26 from `docs/improvements.md` (lines 515–576).
> What the documentation phase discharged, and the defect it revealed without
> touching (`V19`).

<a id="cycle6plus-docs"></a>

## Documentation phase — Cycle 6-plus (2026-08-21)

The phase the two review passes handed their obligations to. It wrote no code, by
the handoff's rule: a defect the documentation reveals is **recorded here and
left**, which is how `V17` was found and how `V19` below was.

### The obligations, discharged

Each of the four the second pass handed over:

| Obligation | Where it landed |
|---|---|
| ADR-0008's lifetime rule is a three-way union | [ADR-0008](decisions/0008-daemon-lifecycle.md) — the rule restated, the mermaid given the third branch and the dotted exit edge, and a "Cycle 6-plus" section separating the **keep-alive** clause from the **exit** cause, which are easy to confuse |
| design §7.3 says the opposite of the code | [design-cycle6plus-update.md](design-cycle6plus-update.md) §7.3 — the row corrected to `abandoned`, with what it used to say and why it was wrong kept in place rather than erased |
| the GUI-only asymmetry is recorded nowhere | [ADR-0016](decisions/0016-cycle6plus-update-path.md) §16.4, and registered as a table in [ux-principles.md](ux-principles.md) §7 so the rule carries its own register |
| `StateAbandoned` / `StaleAfter` are undocumented public API | [go-engine.md](go-engine.md) — a full `internal/update` entry; `CHANGELOG.md` gained the `[Unreleased]` section it did not have at all |

The four decisions ratified on 2026-08-18 are now **ADR-0016 §16**. §16.1 is
stated as the code implements it rather than as the handoff summarised it: the
pid decides *within* the backstop, while a start time that is absent or in the
future is abandoned at once — which is `V15`, and which a "pid first, clock
second" reading would lose.

### V19 — a package comment claims an import the package does not have

**Found by writing `go-engine.md`'s dependency direction, 2026-08-21.**
Severity: cosmetic. **Not fixed** — it is a code change, and this phase does not
make them.

`internal/update/update.go:3` states the package "imports buildinfo, config and
the standard library only". It imports **`buildinfo` and the standard library**;
`internal/config` is not among its imports:

```bash
$ for f in internal/update/*.go; do case "$f" in *_test.go) continue;; esac; \
    grep -H alergyonthestage "$f"; done
internal/update/install.go:  .../internal/buildinfo
internal/update/probe.go:    .../internal/buildinfo
internal/update/refresh.go:  .../internal/buildinfo
internal/update/runner.go:   .../internal/buildinfo
```

The comment is wrong in the **safe** direction — the package is *more* isolated
than advertised, because the state dir arrives as a parameter rather than being
resolved from config — so nothing depends on it and no surface is affected.
`go-engine.md` documents what the code does, not what the comment says.

The one-line fix belongs to whichever code session comes next; ADR-0016 never
made the claim, so nothing normative needs amending.

### What this phase deliberately did not do

- **It did not fix `V19`**, nor any of the seven findings deferred above.
- **It assigned no version number.** `[Unreleased]` stayed unreleased until gate
  C passed and the four `sha256` were attested; the release is `2.2.0`.
- **It did not verify anything by hand.** The by-hand pass was the maintainer's,
  and it ran over four sittings — its checklist was a transient document, deleted
  at the cycle's close; its outcome is
  [§ Gate C — esito](#cycle6plus-gatec-esito).
