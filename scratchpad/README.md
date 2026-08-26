# `scratchpad/` — the human's, not the agent's

Working files belonging to the **maintainer**: prompts being drafted, notes for a
future session, intermediate results, anything not ready to be a document.

The rules, and the third is the one that prevents damage rather than annoyance:

1. **The agent reads this directory always.** Declared, it gets looked at; undeclared,
   it gets stumbled upon.
2. **The agent writes here only when explicitly asked, and never deletes or
   reorganizes anything in it.** Elsewhere the agent has a deletion rule — the
   previous handoff is removed before the next is written — and without this
   category an agent doing hygiene would tidy these files away in good faith.
3. ⚠️ **Confidentiality runs one way.** Everything here is gitignored, so an
   automated check that enumerates *tracked* files never sees it — by construction.
   But the protection is *"it never gets committed"*, **not** *"its contents are safe
   to reuse"*. **Content comes in freely and goes out only laundered.**

When a note here becomes durable it is **promoted to a versioned document** under
[`docs/`](../docs/README.md) — it is not left to rot here. A scratchpad does not
survive a clean clone and does not travel to anyone else.

This README is the one file in here that **is** committed: ignoring the directory
wholesale would mean that in a fresh clone it does not exist and the convention is
invisible to whoever adopts the repo next.

**Not to be confused with `tmp/`**, which is also gitignored but is not yours: it is
where `hack/ytdl-dev.sh` writes development builds.
