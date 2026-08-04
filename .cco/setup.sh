#!/bin/bash
# Project setup script — executed at every `cco start`, as user `claude`.
# Must be idempotent (runs on every session start).
#
# NOTE: the header cco generates here claims this runs "as root". It does not:
# the entrypoint invokes it with `gosu claude bash "$PROJECT_SETUP"`, so apt-get
# and anything else needing root belongs in .cco/Dockerfile instead.
#
# Split of responsibilities with .cco/Dockerfile: heavy and stable goes into the
# image (the Go toolchain, optionally ffmpeg); small and perishable stays here.

# ── yt-dlp ───────────────────────────────────────────────────────────────────
# Fetched at every start on purpose. yt-dlp is the dependency that breaks when
# YouTube changes — a version frozen into an image is the problem this project
# exists to work around (improvements.md U2), not the fix.
#
# The standalone binary, not pip: python3 in the base image is PEP 668
# "externally managed", so `pip3 install --user` refuses outright.
#
# A failure here never blocks the session — the build and test loop does not
# depend on yt-dlp, only real end-to-end checks do.
if ! command -v yt-dlp >/dev/null 2>&1; then
    case "$(uname -m)" in
        aarch64) _ytdlp_asset=yt-dlp_linux_aarch64 ;;
        x86_64)  _ytdlp_asset=yt-dlp_linux ;;
        *)       _ytdlp_asset= ;;
    esac
    if [ -n "$_ytdlp_asset" ]; then
        mkdir -p "$HOME/.local/bin"
        if curl -fsSL --max-time 120 -o "$HOME/.local/bin/yt-dlp" \
            "https://github.com/yt-dlp/yt-dlp/releases/latest/download/$_ytdlp_asset"; then
            chmod +x "$HOME/.local/bin/yt-dlp"
        else
            rm -f "$HOME/.local/bin/yt-dlp"
        fi
    fi
    unset _ytdlp_asset
fi
