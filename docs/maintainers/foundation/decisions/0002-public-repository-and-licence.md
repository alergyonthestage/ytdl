# ADR-0002 — Public repository with a restrictive licence

- **Status:** accepted
- **Date:** 2026-07-21
- **Supersedes nothing; complements** [ADR-0001](0001-distribution-channel.md)

## Context

The tool is meant for a small circle of people the maintainer chooses, not for
the general public. The preference was for a private repository, with a public
one acceptable if privacy proved costly.

It proves costly. The install channel chosen in ADR-0001 fetches
`install.sh` — and `ytdl --update` fetches it again later — over unauthenticated
HTTPS from `raw.githubusercontent.com`. A private repository refuses those
requests. Making it work would mean embedding a GitHub token in the command
users paste, which:

- hands every user a credential that grants access to the repository,
- has to be distributed, rotated and revoked per person,
- is a worse security posture than simply publishing the source.

The alternative of hosting `install.sh` in a secret gist keeps the repository
private but leaves the code readable to anyone with the URL, while adding a
second place to keep in sync. It buys obscurity, not privacy.

## Decision

Publish `alergyonthestage/ytdl` as a **public repository**, and express the
intended restriction through the **licence** rather than through visibility:
[PolyForm Strict 1.0.0](../../LICENSE.md).

The repository is not promoted anywhere. Discovery without a direct link is
effectively nil.

## Rationale

Visibility and permission are different things. A public repository makes the
one-liner and self-update work with no credentials and no maintenance; the
licence is what states that this software may be used but not redistributed or
built upon. PolyForm Strict says exactly that, is professionally drafted, and
costs nothing.

MIT was rejected: it would permit redistribution and commercial reuse, which is
the opposite of the intent. Publishing with no licence at all was rejected too —
it would leave even the intended users without a legal right to use the tool.

## Consequences

- The source is readable by anyone who finds the URL. Accepted deliberately:
  there is nothing secret in it, and the installer being auditable is a feature
  given that users are asked to pipe it into `bash`.
- No costs, no accounts, no tokens for anyone.
- `ytdl --update` keeps working indefinitely without maintenance.
- Occasional strangers may find and install it. The licence sets the terms, and
  there is no obligation to support them.
- If the repository is ever made private, the install and update paths break.
  That constraint should be re-read before changing visibility.
