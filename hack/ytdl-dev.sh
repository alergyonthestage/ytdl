#!/usr/bin/env bash
#
# ytdl-dev.sh — run a development build of ytdl in a sandbox that cannot touch
# the installed one.
#
# WHY THIS EXISTS
#
# `ytdl` on your $PATH is the RELEASED build. A development build is a different
# binary, and running it against your real state directory would mix the two:
# the same queue, the same history, the same config, the same ~/.local/bin — and,
# since Cycle 6-plus, the same update marker and cached verdict. An installer run
# started from a dev build would overwrite the real ytdl.
#
# Everything needed to separate them already existed as environment variables;
# nothing here is a new engine capability. This script only sets them together,
# consistently, so it cannot be got half-right by hand:
#
#   XDG_STATE_HOME     queue, logs, update.json, update-run.json, installed.conf
#   XDG_CONFIG_HOME    the config file
#   YTDL_INSTALL_DIR   where install.sh WRITES     ─┐ same directory,
#   YTDL_BIN_DIR       where the engine READS      ─┘ two different names
#   YTDL_OUT_DIR       downloads
#   YTDL_GUI_PORT      so a dev GUI never collides with the real one
#   YTDL_APP_DIR       where install.sh puts YTDL.app — the sandbox gets its own,
#                      so a dev bundle can never launch the installed ytdl
#
# The daemon inherits the environment (internal/daemon's spawn does not set
# cmd.Env), so a GUI daemon started from here stays inside the sandbox too.
#
# THE VERSION STAMP IS LOAD-BEARING, NOT COSMETIC
#
# A build with no -ldflags reports "dev", and a "dev" build is never checked and
# never reported stale (update.DevVersion) — every update surface goes inert and
# reads as broken. So this script always stamps a version. The default is
# derived from git and is deliberately NOT "dev".
#
# Usage:
#   hack/ytdl-dev.sh build [GOOS/GOARCH]   cross-compile a stamped dev binary
#   hack/ytdl-dev.sh seed                  copy yt-dlp/ffmpeg into the sandbox
#   hack/ytdl-dev.sh run <args...>         run the dev binary in the sandbox
#   hack/ytdl-dev.sh install               put the build where a real install lives
#                                          (REQUIRED to test the update handover)
#   hack/ytdl-dev.sh stop                  stop the sandbox daemon (never the real one)
#   hack/ytdl-dev.sh env                   print the exports (for eval)
#   hack/ytdl-dev.sh status                what is set, and what is in the sandbox
#   hack/ytdl-dev.sh reset                 delete the sandbox (never the real one)
#
# Overrides: YTDL_DEV_HOME, YTDL_DEV_VERSION, YTDL_DEV_PORT.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEV_HOME="${YTDL_DEV_HOME:-$HOME/.ytdl-dev}"
DEV_PORT="${YTDL_DEV_PORT:-8790}"
BUILD_DIR="$REPO_ROOT/tmp/dev"
MODULE="github.com/alergyonthestage/ytdl"

die()  { printf '✗ %s\n' "$*" >&2; exit 1; }
info() { printf '▸ %s\n' "$*"; }
ok()   { printf '✓ %s\n' "$*"; }

# ──────────────────────────────────────────────────────────────────
#  Guard: never operate on the real directories
# ──────────────────────────────────────────────────────────────────
# A mistyped YTDL_DEV_HOME must not be able to point the sandbox at the real
# install and then have `reset` delete it. Checked before anything is written or
# removed, not only in reset.
assert_sandbox() {
  case "$DEV_HOME" in
    ""|"/"|"$HOME") die "YTDL_DEV_HOME=$DEV_HOME is not a sandbox path." ;;
  esac
  [ "$DEV_HOME" != "$HOME/.local" ] || die "YTDL_DEV_HOME must not be ~/.local."
  [ "$DEV_HOME" != "$HOME/.config" ] || die "YTDL_DEV_HOME must not be ~/.config."
}

# dev_version is the stamp. It must differ from the released tag (so the update
# path is live) and must not be the literal "dev" (which switches it off).
#
# The charset is constrained by update.validVersion — letters, digits and
# . - _ + only — so the short sha is used raw and nothing else is interpolated.
dev_version() {
  if [ -n "${YTDL_DEV_VERSION:-}" ]; then
    printf '%s' "$YTDL_DEV_VERSION"
    return
  fi
  local sha
  sha="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  printf 'v0.0.0-dev.%s' "$sha"
}

host_goos()   { case "$(uname -s)" in Darwin) echo darwin ;; *) echo linux ;; esac; }
host_goarch() { case "$(uname -m)" in arm64|aarch64) echo arm64 ;; *) echo amd64 ;; esac; }

bin_for() { printf '%s/ytdl-%s-%s' "$BUILD_DIR" "$1" "$2"; }

# dev_daemon_pids lists daemons started FROM THE SANDBOX BUILD, and only those.
#
# The match is the build directory's absolute path, which the real installed
# daemon (~/.local/bin/ytdl) can never contain — so `stop` cannot reach it.
#
# This exists because `ytdl gui` reuses a daemon that is already listening
# (cmd/ytdl/main.go: `if !listening { spawn }`). Rebuilding therefore does NOT
# replace a running one: the old inode keeps serving, the page keeps reporting
# the old version, and the rebuild looks like it did nothing.
dev_daemon_pids() {
  # Both sandbox locations: the build directory (what `run` executes) and the
  # sandbox bin dir (what `install` places, and what install.sh replaces). Neither
  # can ever be ~/.local/bin, so the real daemon is out of reach by construction.
  pgrep -f "($BUILD_DIR/ytdl-|$DEV_HOME/bin/ytdl)" 2>/dev/null || true
}

# ──────────────────────────────────────────────────────────────────
#  The sandbox environment — one definition, used by run/env/status
# ──────────────────────────────────────────────────────────────────
emit_env() {
  cat <<EOF
export XDG_STATE_HOME="$DEV_HOME/state"
export XDG_CONFIG_HOME="$DEV_HOME/config"
export YTDL_INSTALL_DIR="$DEV_HOME/bin"
export YTDL_BIN_DIR="$DEV_HOME/bin"
export YTDL_OUT_DIR="$DEV_HOME/downloads"
export YTDL_GUI_PORT="$DEV_PORT"
export YTDL_APP_DIR="$DEV_HOME/Applications"
EOF
}

apply_env() {
  export XDG_STATE_HOME="$DEV_HOME/state"
  export XDG_CONFIG_HOME="$DEV_HOME/config"
  export YTDL_INSTALL_DIR="$DEV_HOME/bin"
  export YTDL_BIN_DIR="$DEV_HOME/bin"
  export YTDL_OUT_DIR="$DEV_HOME/downloads"
  export YTDL_GUI_PORT="$DEV_PORT"
  export YTDL_APP_DIR="$DEV_HOME/Applications"
}

make_dirs() {
  assert_sandbox
  mkdir -p "$DEV_HOME/state/ytdl" "$DEV_HOME/config/ytdl" \
           "$DEV_HOME/bin" "$DEV_HOME/downloads" "$DEV_HOME/Applications"
}

# ──────────────────────────────────────────────────────────────────
#  Commands
# ──────────────────────────────────────────────────────────────────
cmd_build() {
  local target="${1:-$(host_goos)/$(host_goarch)}"
  local goos="${target%%/*}" goarch="${target##*/}"
  [ "$goos" != "$target" ] || die "target must be GOOS/GOARCH, e.g. darwin/arm64"

  local version out
  version="$(dev_version)"
  out="$(bin_for "$goos" "$goarch")"
  mkdir -p "$BUILD_DIR"

  info "building $goos/$goarch stamped $version"
  # CGO_ENABLED=0 keeps the binary static, which is what lets the Linux
  # container produce a Mach-O the Mac can run.
  ( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -ldflags "-X $MODULE/internal/buildinfo.Version=$version" \
      -o "$out" ./cmd/ytdl )
  ok "$out"

  case "$version" in
    dev) die "refusing a build stamped \"dev\": every update surface would be inert" ;;
  esac

  # A rebuild does not reach a daemon that is already serving. Saying so here is
  # the difference between "my change did nothing" and one more command.
  if [ -n "$(dev_daemon_pids)" ]; then
    info "a sandbox daemon is still running the PREVIOUS build — it will keep serving it"
    info "run: hack/ytdl-dev.sh stop"
  fi
}

# cmd_install puts the built binary where a real installation would keep it.
#
# It matters for exactly one thing, and that thing is the update handover. The
# daemon re-execs os.Executable(), and install.sh replaces $YTDL_INSTALL_DIR/ytdl
# — so unless the running binary IS that file, the handover restarts the OLD
# build, the page never sees a new version, and the sequence fails after 60 s for
# a reason that has nothing to do with the code under test.
#
# `run` deliberately keeps executing the build directory: that is the predictable
# thing for ordinary CLI checks, where nothing replaces the binary underneath.
cmd_install() {
  make_dirs
  local bin
  bin="$(bin_for "$(host_goos)" "$(host_goarch)")"
  [ -x "$bin" ] || die "no dev binary for this machine — run: hack/ytdl-dev.sh build"
  [ -z "$(dev_daemon_pids)" ] || die "a sandbox daemon is running; stop it first: hack/ytdl-dev.sh stop"
  cp "$bin" "$DEV_HOME/bin/ytdl"
  chmod +x "$DEV_HOME/bin/ytdl"
  ok "installed into the sandbox: $DEV_HOME/bin/ytdl"
  info "run it with the sandbox environment:"
  info "  eval \"\$(hack/ytdl-dev.sh env)\" && $DEV_HOME/bin/ytdl gui"
}

# cmd_stop ends the sandbox daemon so the next `run gui` starts the new binary.
cmd_stop() {
  local pids
  pids="$(dev_daemon_pids)"
  if [ -z "$pids" ]; then
    ok "no sandbox daemon running"
    return 0
  fi
  # TERM, not KILL: the daemon releases the queue lock and removes its token on
  # the way out, and a KILL would leave both behind for the next run to trip on.
  # shellcheck disable=SC2086
  kill $pids 2>/dev/null || true
  local n=0
  while [ -n "$(dev_daemon_pids)" ] && [ "$n" -lt 30 ]; do
    n=$((n + 1))
    perl -e 'select(undef,undef,undef,0.2)' 2>/dev/null || true
  done
  if [ -n "$(dev_daemon_pids)" ]; then
    die "the sandbox daemon did not stop; pids: $(dev_daemon_pids | tr '\n' ' ')"
  fi
  ok "sandbox daemon stopped (the installed ytdl was not touched)"
}

cmd_seed() {
  make_dirs
  local copied=0 t
  for t in yt-dlp ffmpeg ffprobe; do
    if [ -x "$HOME/.local/bin/$t" ]; then
      cp "$HOME/.local/bin/$t" "$DEV_HOME/bin/$t"
      copied=$((copied + 1))
    fi
  done
  # Copied, not symlinked: install.sh replaces binaries with mv, and a mv onto a
  # symlink would leave the real copy standing but no longer referenced — which
  # looks like it worked and is not what the sandbox is for.
  ok "$copied of 3 dependencies copied into $DEV_HOME/bin"
  [ "$copied" -eq 3 ] || info "missing ones will read as \"non installato\" — expected if you never installed them"
}

cmd_run() {
  [ "${1:-}" != "--" ] || shift
  local goos goarch bin
  goos="$(host_goos)"; goarch="$(host_goarch)"
  bin="$(bin_for "$goos" "$goarch")"
  [ -x "$bin" ] || die "no dev binary for $goos/$goarch — run: hack/ytdl-dev.sh build"

  make_dirs
  apply_env
  exec "$bin" "$@"
}

cmd_env() { assert_sandbox; emit_env; }

cmd_status() {
  assert_sandbox
  printf 'sandbox   %s\n' "$DEV_HOME"
  printf 'stamp     %s\n' "$(dev_version)"
  printf 'gui port  %s\n' "$DEV_PORT"
  local pids
  pids="$(dev_daemon_pids)"
  if [ -n "$pids" ]; then
    printf 'daemon    RUNNING (pids: %s) — `stop` before rebuilding\n' "$(echo "$pids" | tr '\n' ' ')"
  else
    printf 'daemon    not running\n'
  fi
  printf '\nbuilt binaries:\n'
  if [ -d "$BUILD_DIR" ] && [ -n "$(ls -A "$BUILD_DIR" 2>/dev/null)" ]; then
    ls -1 "$BUILD_DIR" | sed 's/^/  /'
  else
    printf '  (none — hack/ytdl-dev.sh build)\n'
  fi
  printf '\nsandbox state dir:\n'
  if [ -d "$DEV_HOME/state/ytdl" ] && [ -n "$(ls -A "$DEV_HOME/state/ytdl" 2>/dev/null)" ]; then
    ls -1 "$DEV_HOME/state/ytdl" | sed 's/^/  /'
  else
    printf '  (empty)\n'
  fi
  printf '\nsandbox bin dir:\n'
  if [ -d "$DEV_HOME/bin" ] && [ -n "$(ls -A "$DEV_HOME/bin" 2>/dev/null)" ]; then
    ls -1 "$DEV_HOME/bin" | sed 's/^/  /'
  else
    printf '  (empty — hack/ytdl-dev.sh seed)\n'
  fi
}

cmd_reset() {
  assert_sandbox
  [ -d "$DEV_HOME" ] || { ok "nothing to reset"; return 0; }
  rm -rf "$DEV_HOME"
  ok "removed $DEV_HOME (the real install was not touched)"
}

usage() {
  sed -n '3,45p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

case "${1:-}" in
  build)  shift; cmd_build "$@" ;;
  seed)   shift; cmd_seed ;;
  run)     shift; cmd_run "$@" ;;
  install) shift; cmd_install ;;
  stop)    shift; cmd_stop ;;
  env)    shift; cmd_env ;;
  status) shift; cmd_status ;;
  reset)  shift; cmd_reset ;;
  ""|-h|--help|help) usage ;;
  *) die "unknown command: $1  (try: hack/ytdl-dev.sh help)" ;;
esac
