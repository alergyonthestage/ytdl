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
# The ZIPAPP asset, not the PyInstaller one the installer ships to macOS. This is
# a container-only divergence and it is deliberate — see ADR-0017.
#
# The PyInstaller one-file bundle unpacks ~78 MB into $TMPDIR at EVERY invocation,
# under a fresh random name, and removes it only on a clean exit. A killed probe
# therefore leaks 78 MB that nothing ever collects, which is `V25`; multiplied by
# a suite that re-executed itself it filled the overlay and took four sessions
# down. The zipapp does not unpack at all: it is 3 MB on disk, runs from the
# image's own python3, and leaves nothing behind however it dies. There is no
# cleanup to schedule because there is nothing to clean up.
#
# Not pip: python3 in the base image is PEP 668 "externally managed", so
# `pip3 install --user` refuses outright and `python3 -m venv` needs a
# python3.11-venv that is not installed. The zipapp needs neither.
#
# The shebang is rewritten to the interpreter's ABSOLUTE path. Upstream ships
# `#!/usr/bin/env python3`, which resolves through $PATH — and the Go suite empties
# $PATH deliberately in several tests, where an env-shebang would fail for a reason
# that has nothing to do with what the test is asserting. The rewrite is byte-safe:
# only the first line changes, and the zip payload is located from the end of the
# file, so it survives.
#
# Fetched only when absent or in the wrong FORM (an ELF binary is the old bundle,
# a previous container's leftover: `~/.local/bin` is a host mount and persists
# across image rebuilds). Replacing in place, never adding alongside.
#
# A failure here never blocks the session — the build and test loop does not
# depend on yt-dlp, only real end-to-end checks do.
_ytdlp_bin="$HOME/.local/bin/yt-dlp"
_ytdlp_wanted=no
if [ ! -x "$_ytdlp_bin" ]; then
    _ytdlp_wanted=yes                                    # absent
elif [ "$(head -c 2 "$_ytdlp_bin" 2>/dev/null)" != '#!' ]; then
    _ytdlp_wanted=yes                                    # present, but not a zipapp
fi
if [ "$_ytdlp_wanted" = yes ]; then
    mkdir -p "$HOME/.local/bin"
    _ytdlp_tmp="$_ytdlp_bin.new.$$"
    if curl -fsSL --max-time 120 -o "$_ytdlp_tmp" \
        "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp"; then
        # Absolute-path shebang, payload untouched.
        { printf '#!%s\n' "$(command -v python3)"; tail -n +2 "$_ytdlp_tmp"; } > "$_ytdlp_tmp.sb" \
            && mv -f "$_ytdlp_tmp.sb" "$_ytdlp_tmp" \
            && chmod +x "$_ytdlp_tmp" \
            && mv -f "$_ytdlp_tmp" "$_ytdlp_bin"
    fi
    rm -f "$_ytdlp_tmp" "$_ytdlp_tmp.sb"
fi
unset _ytdlp_bin _ytdlp_wanted _ytdlp_tmp
