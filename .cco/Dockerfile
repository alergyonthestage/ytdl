# Custom image for this project, declared as docker.image in .cco/project.yml.
# Rebuild it after every `cco build` of the base image, or the session runs on a
# stale entrypoint:
#   cd yt-download
#   docker build -t claude-orchestrator-ytdl:latest -f .cco/Dockerfile .
#
FROM claude-orchestrator:latest

# ── Never switch the final USER back ─────────────────────────────────────────
# The cco entrypoint MUST start as root. It fixes the docker group GID
# (groupmod/usermod), locks the internal-store privilege boundary at
# /var/lib/cco-internal (install/chown/chmod 0700 cco-svc, ADR-0047), and only
# then drops to `claude` with `gosu` for everything after — global setup,
# project setup, and Claude itself.
#
# An image whose last instruction is `USER claude` starts that entrypoint
# unprivileged, and it dies on the first chown:
#
#   chown: changing ownership of '/var/lib/cco-internal': Operation not permitted
#
# So: `USER root` here, and nothing switching it back at the end of the file.
USER root

# ── Go toolchain ─────────────────────────────────────────────────────────────
# Baked in so every session opens with a working build/test loop instead of
# spending its first minutes re-downloading the SDK.
#
# It installs into /usr/local, NOT under /home/claude: the home directory is
# covered by volume mounts at runtime, which would hide anything a build put
# there.
#
# Keep GO_VERSION in step with .github/workflows (both use `go-version: stable`),
# so the -race suite and the parity gate run here on what CI runs on.
ARG GO_VERSION=1.26.5
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" -o /tmp/go.tgz; \
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz; \
    ln -sf /usr/local/go/bin/go /usr/local/bin/go; \
    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt; \
    go version

# ── ffmpeg (optional, ~200 MB with dependencies) ─────────────────────────────
# Uncomment to run real conversions and end-to-end checks in-container, instead
# of deferring every real verification to the maintainer's Mac at gate C.
# RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg \
#     && rm -rf /var/lib/apt/lists/*
#
# yt-dlp is deliberately NOT baked in — see .cco/setup.sh for why.
