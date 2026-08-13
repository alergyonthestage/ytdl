#!/bin/bash
#
# ytdl installer — provisions ytdl and its dependencies on macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/alergyonthestage/ytdl/main/install.sh | bash
#
# Installs into ~/.local/bin without sudo. See docs/distribution.md for the
# constraints behind the choices made here.
#
set -euo pipefail

# ──────────────────────────────────────────────────────────────────
#  Configuration
# ──────────────────────────────────────────────────────────────────

REPO_SLUG="${YTDL_REPO:-alergyonthestage/ytdl}"
BRANCH="${YTDL_BRANCH:-main}"
INSTALL_DIR="${YTDL_INSTALL_DIR:-$HOME/.local/bin}"

# deps.conf declares what ytdl requires, and is fetched from the same place this
# script is (ADR-0016 §2). It is the only thing that decides which yt-dlp and
# which ffmpeg end up on this machine.
DEPS_URL="https://raw.githubusercontent.com/$REPO_SLUG/$BRANCH/deps.conf"

# yt-dlp is fetched per TAG now, not from /latest/: the pin is what decides the
# version, so the URL has to name it.
YTDLP_RELEASES="https://github.com/yt-dlp/yt-dlp/releases/download"
FFMPEG_BASE="https://ffmpeg.martin-riedl.de/download/macos"
# The compiled ytdl binaries are published per release; /latest/ gives the
# newest without pinning a version (see docs/decisions/0005).
RELEASE_BASE="https://github.com/$REPO_SLUG/releases/latest/download"

# --force (or YTDL_FORCE=1) reinstalls every component regardless of what is
# already current: what a retry after a failed update uses, and what the
# maintainer uses to reproduce an install from scratch.
#
# Normalised to exactly 0 or 1 here, so the arithmetic comparisons below cannot
# be handed a word. An unrecognised value means off — the same direction as not
# setting it at all.
case "${YTDL_FORCE:-0}" in
  1|true|yes) FORCE=1 ;;
  *)          FORCE=0 ;;
esac

# Populated by detect_platform(). ARCH_KEY is deps.conf's spelling of ARCH.
OS_MAJOR=""; OS_MINOR=""; TIER=""; ARCH=""; ARCH_KEY=""

# Populated by load_deps() and resolve_targets(): the concrete versions this run
# is aiming at. "latest" never survives past resolve_targets, so every comparison
# below is between two concrete strings.
DEPS_FILE=""; YTDLP_TARGET=""; FFMPEG_TARGET=""; YTDL_TARGET=""

TMPDIR_YTDL=""
cleanup() { [ -n "$TMPDIR_YTDL" ] && rm -rf "$TMPDIR_YTDL"; return 0; }
trap cleanup EXIT

# ──────────────────────────────────────────────────────────────────
#  Output helpers
# ──────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  B=$(printf '\033[1m'); DIM=$(printf '\033[2m'); R=$(printf '\033[0m')
  GREEN=$(printf '\033[32m'); RED=$(printf '\033[31m'); YELLOW=$(printf '\033[33m')
else
  B=""; DIM=""; R=""; GREEN=""; RED=""; YELLOW=""
fi

info() { printf '%s▸%s %s\n' "$B" "$R" "$*"; }
ok()   { printf '%s✓%s %s\n' "$GREEN" "$R" "$*"; }
warn() { printf '%s!%s %s\n' "$YELLOW" "$R" "$*" >&2; }
step() { printf '\n%s%s%s\n' "$B" "$*" "$R"; }

# Every failure must tell the user what to do next, not just what broke.
fail() {
  printf '\n%s✗ %s%s\n' "$RED" "$1" "$R" >&2
  shift
  [ $# -gt 0 ] && printf '\n' >&2
  for line in "$@"; do printf '  %s\n' "$line" >&2; done
  printf '\n' >&2
  exit 1
}

# ──────────────────────────────────────────────────────────────────
#  Platform detection
# ──────────────────────────────────────────────────────────────────
# macOS changed its version scheme at 11: "10.15.7" then "11.0", "26.1".
# Reading only the first component misreads 10.15 as "10", so we compare the
# minor too while the major is 10. The floor is 10.15 Catalina — the Go engine
# needs it (docs/decisions/0005).
detect_platform() {
  command -v sw_vers >/dev/null 2>&1 || fail \
    "This installer only supports macOS." \
    "Windows and Linux support is planned but not implemented yet."

  local ver rest
  ver="$(sw_vers -productVersion)"
  OS_MAJOR="${ver%%.*}"
  rest="${ver#*.}"
  OS_MINOR="${rest%%.*}"
  case "$OS_MAJOR" in ''|*[!0-9]*) OS_MAJOR=0 ;; esac
  case "$OS_MINOR" in ''|*[!0-9]*) OS_MINOR=0 ;; esac

  if [ "$OS_MAJOR" -ge 11 ]; then
    TIER="supported"
  elif [ "$OS_MAJOR" -eq 10 ] && [ "$OS_MINOR" -ge 15 ]; then
    TIER="supported"
  else
    TIER="unsupported"
  fi

  ARCH="$(uname -m)"
  case "$ARCH" in
    arm64) ARCH_KEY="arm64" ;;
    *)     ARCH_KEY="amd64" ;;
  esac

  if [ "$TIER" = "unsupported" ]; then
    fail "macOS $ver is too old for ytdl." \
      "The oldest supported version is macOS 10.15 Catalina." \
      "" \
      "ytdl is a compiled binary and its toolchain targets macOS 10.15 or" \
      "newer, so macOS 10.13–10.14 are no longer supported."
  fi

  info "macOS $ver ($ARCH) — supported"
}

# ──────────────────────────────────────────────────────────────────
#  The pin: deps.conf
# ──────────────────────────────────────────────────────────────────
# `key = value`, one per line, # for comments — the same strict format ytdl's own
# config file uses, and read the same way: parsed with awk, never sourced, every
# key checked against a whitelist. That discipline is why a pin cannot execute
# anything, and why it parses on a stock Mac where jq does not exist.

# kv_get KEY FILE — the value for KEY, or empty. Splits on the FIRST '=' so a
# value containing '=' survives intact, and trims surrounding whitespace only.
kv_get() {
  local key="$1" file="$2"
  [ -f "$file" ] || return 0
  awk -v k="$key" '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    {
      eq = index($0, "=")
      if (eq == 0) next
      name = substr($0, 1, eq - 1)
      val  = substr($0, eq + 1)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
      if (name == k) { print val; exit }
    }
  ' "$file"
}

deps_get() { kv_get "$1" "$DEPS_FILE"; }

# validate_deps FILE — every line is a comment, blank, or a whitelisted
# `key = value`. Anything else is refused.
#
# The installer is fetched from the same commit as deps.conf, so a key it does not
# recognise is a typo in the pin, not a version skew — refusing costs nothing and
# catches the mistake at the only moment it can still be caught. (The Go probe,
# which is arbitrarily older than the file it reads, is deliberately tolerant of
# the same thing; see internal/update/probe.go for why the two differ.)
validate_deps() {
  local file="$1"
  awk '
    BEGIN {
      split("yt_dlp_version ffmpeg_build_arm64 ffmpeg_build_amd64 " \
            "ffmpeg_sha256_arm64_ffmpeg ffmpeg_sha256_arm64_ffprobe " \
            "ffmpeg_sha256_amd64_ffmpeg ffmpeg_sha256_amd64_ffprobe", a, " ")
      for (i in a) allowed[a[i]] = 1
      bad = 0
    }
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    {
      eq = index($0, "=")
      if (eq == 0) { printf "line %d: not a key = value pair\n", NR > "/dev/stderr"; bad = 1; next }
      name = substr($0, 1, eq - 1)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
      if (name == "") { printf "line %d: empty key\n", NR > "/dev/stderr"; bad = 1; next }
      if (!(name in allowed)) { printf "line %d: unknown key \"%s\"\n", NR, name > "/dev/stderr"; bad = 1 }
    }
    END { exit bad ? 1 : 0 }
  ' "$file"
}

# load_deps downloads and validates the pin. It ABORTS on any problem and installs
# nothing.
#
# It never falls back to "latest". A silent fallback would be indistinguishable
# from the policy currently BEING "latest" — so the day the maintainer pins a
# rollback to stop a broken yt-dlp reaching people, that equivalence would quietly
# ignore it and keep installing the broken one. Failing loudly is the only
# behaviour that keeps the lever real (ADR-0016 §2).
load_deps() {
  step "Reading what ytdl requires"

  DEPS_FILE="$TMPDIR_YTDL/deps.conf"
  curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 20 -o "$DEPS_FILE" "$DEPS_URL" || fail \
    "Could not read what ytdl requires — nothing was installed." \
    "Tried: $DEPS_URL" \
    "" \
    "Check your internet connection and run the installer again."

  validate_deps "$DEPS_FILE" || fail \
    "What ytdl requires could not be read — nothing was installed." \
    "The problem is listed above, in $DEPS_URL." \
    "" \
    "This is a mistake in the ytdl repository, not on your machine." \
    "Please report it."
}

# latest_tag SLUG — a repository's newest release tag, read from the redirect
# github.com answers to releases/latest. The redirect target IS the answer, so
# this deliberately does not follow it: no API, no token, no rate limit.
latest_tag() {
  local slug="$1" loc
  loc="$(curl -sSI --max-time 20 --retry 2 "https://github.com/$slug/releases/latest" 2>/dev/null \
         | awk 'tolower($1) == "location:" { print $2 }' | tr -d '\r' | tail -1)" || return 1
  case "$loc" in
    */releases/tag/?*) printf '%s\n' "${loc##*/releases/tag/}" ;;
    *) return 1 ;;
  esac
}

# resolve_targets turns the policy into concrete versions, BEFORE anything is
# fetched. That ordering is the whole basis of idempotence: "latest" cannot be
# compared against what is installed, a tag can, so resolving first is what makes
# "do I need to?" a question with an answer (ADR-0016 §11).
resolve_targets() {
  YTDLP_TARGET="$(deps_get yt_dlp_version)"
  [ -n "$YTDLP_TARGET" ] || fail \
    "What ytdl requires does not name a yt-dlp version — nothing was installed." \
    "This is a mistake in the ytdl repository. Please report it."

  if [ "$YTDLP_TARGET" = "latest" ]; then
    YTDLP_TARGET="$(latest_tag yt-dlp/yt-dlp)" || fail \
      "Could not work out which yt-dlp to install — nothing was installed." \
      "Check your internet connection and run the installer again."
  fi

  FFMPEG_TARGET="$(deps_get "ffmpeg_build_$ARCH_KEY")"
  [ -n "$FFMPEG_TARGET" ] || fail \
    "What ytdl requires does not name an ffmpeg build for $ARCH_KEY — nothing was installed." \
    "This is a mistake in the ytdl repository. Please report it."

  # ytdl's own target is best-effort: when the redirect cannot be read we simply
  # do not skip, and install from /latest/. Not skipping is the safe direction.
  YTDL_TARGET="$(latest_tag "$REPO_SLUG" 2>/dev/null)" || YTDL_TARGET=""

  info "yt-dlp $YTDLP_TARGET · ffmpeg $FFMPEG_TARGET"
}

# ──────────────────────────────────────────────────────────────────
#  What is already here
# ──────────────────────────────────────────────────────────────────
marker_dir()  { printf '%s/ytdl\n' "${XDG_STATE_HOME:-$HOME/.local/state}"; }
marker_path() { printf '%s/installed.conf\n' "$(marker_dir)"; }
marker_get()  { kv_get "$1" "$(marker_path)"; }

# tool_version BIN — the first line a tool prints for --version, or empty when it
# is not there or does not answer. yt-dlp prints exactly its tag, which is what
# makes every comparison here a string equality.
tool_version() {
  local bin="$1" out
  [ -x "$bin" ] || return 0
  out="$("$bin" --version 2>/dev/null | head -1)" || out=""
  printf '%s' "$out" | tr -d '\r\n'
}

# ytdl_version BIN — ytdl prints "ytdl v2.1.0"; the version is the second field.
ytdl_version() {
  local bin="$1" out
  [ -x "$bin" ] || return 0
  out="$("$bin" --version 2>/dev/null | head -1 | awk '{print $2}')" || out=""
  printf '%s' "$out" | tr -d '\r\n'
}

# The three skip decisions, kept separate from the installs so they can be tested
# without a network. Each returns 0 for "already current, nothing to do".
#
# --force answers no to all three: a retry after a failed update must not skip the
# component that failed just because its version string happens to look right.
ytdlp_is_current() {
  [ "$FORCE" -eq 0 ] || return 1
  [ "$(tool_version "$INSTALL_DIR/yt-dlp")" = "$YTDLP_TARGET" ]
}

# ffmpeg cannot describe its own build — `ffmpeg -version` says 9.0 while the pin
# says 1785863997_9.0 — so the marker is the only exact answer. Both binaries must
# also actually be there: a marker without the files it describes is a record of an
# install that no longer exists.
ffmpeg_is_current() {
  [ "$FORCE" -eq 0 ] || return 1
  [ -x "$INSTALL_DIR/ffmpeg" ] || return 1
  [ -x "$INSTALL_DIR/ffprobe" ] || return 1
  [ "$(marker_get ffmpeg_build)" = "$FFMPEG_TARGET" ]
}

# An unresolved target means "do not skip": we would be comparing against nothing.
ytdl_is_current() {
  [ "$FORCE" -eq 0 ] || return 1
  [ -n "$YTDL_TARGET" ] || return 1
  [ "$(ytdl_version "$INSTALL_DIR/ytdl")" = "$YTDL_TARGET" ]
}

# write_marker records what is on this machine now, so the next run can answer
# "do I need to?" without asking the network, and so ytdl can SHOW which ffmpeg it
# has. Written atomically, like everything else in the state dir.
write_marker() {
  local dir tmp
  dir="$(marker_dir)"
  mkdir -p "$dir" || return 0
  tmp="$dir/installed.conf.tmp.$$"
  {
    printf '# Written by the ytdl installer. Read by ytdl; do not edit.\n'
    printf 'ytdl_version = %s\n'   "$(ytdl_version "$INSTALL_DIR/ytdl")"
    printf 'yt_dlp_version = %s\n' "$(tool_version "$INSTALL_DIR/yt-dlp")"
    printf 'ffmpeg_build = %s\n'   "$FFMPEG_TARGET"
    printf 'installed_at = %s\n'   "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } > "$tmp" && mv "$tmp" "$dir/installed.conf"
}

# ──────────────────────────────────────────────────────────────────
#  Download helpers
# ──────────────────────────────────────────────────────────────────
download() {
  local url="$1" dest="$2"
  curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 20 -o "$dest" "$url" || fail \
    "Download failed: $url" \
    "Check your internet connection and run the installer again." \
    "If it keeps failing, the download server may be temporarily unavailable."
}

# yt-dlp publishes SHA2-256SUMS per release. This installer executes remote
# code by design, so verifying what we just downloaded is not optional.
verify_checksum() {
  local file="$1" name="$2" sums="$3" expected actual

  expected="$(awk -v n="$name" '$2 == n {print $1; exit}' "$sums")"
  if [ -z "$expected" ]; then
    warn "No published checksum for $name — skipping verification."
    return 0
  fi

  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  elif command -v openssl >/dev/null 2>&1; then
    actual="$(openssl dgst -sha256 "$file" | awk '{print $NF}')"
  else
    warn "No checksum tool available — skipping verification."
    return 0
  fi

  [ "$actual" = "$expected" ] || fail \
    "Checksum mismatch for $name — refusing to install." \
    "expected: $expected" \
    "actual:   $actual" \
    "" \
    "The download was corrupted or tampered with. Try again; if it happens" \
    "repeatedly, do not use the downloaded file and report the issue."

  ok "Checksum verified ($name)"
}

# sha256_of FILE — the file's sha256, or empty when no tool can compute one.
sha256_of() {
  local file="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | awk '{print $NF}'
  fi
}

# verify_pinned_checksum FILE EXPECTED LABEL — verify against a sum declared in
# deps.conf rather than published by the upstream.
#
# Unlike verify_checksum this NEVER warns and continues. That function's
# tolerance is correct for yt-dlp, whose SHA2-256SUMS we do not control; here the
# sum is the maintainer's own attestation of a build they fetched and tested, and
# an ffmpeg installed without checking it is exactly the gap ADR-0016 §12 closes.
# A missing sum, or no tool to compute one, is a refusal.
verify_pinned_checksum() {
  local file="$1" expected="$2" label="$3" actual

  [ -n "$expected" ] || fail \
    "No checksum is declared for $label — refusing to install it." \
    "This is a mistake in the ytdl repository. Please report it."

  actual="$(sha256_of "$file")"
  [ -n "$actual" ] || fail \
    "No way to check the download for $label — refusing to install it." \
    "Neither shasum nor openssl is available on this Mac, which is unusual." \
    "Please report this."

  [ "$actual" = "$expected" ] || fail \
    "Checksum mismatch for $label — refusing to install." \
    "expected: $expected" \
    "actual:   $actual" \
    "" \
    "The download was corrupted or tampered with. Try again; if it happens" \
    "repeatedly, do not use the downloaded file and report the issue."

  ok "Checksum verified ($label)"
}

# Our own release assets must carry a checksum entry: a name missing from
# SHA2-256SUMS is a release.yml packaging bug, so hard-fail rather than fall
# through verify_checksum's tolerant warn-and-skip (which is correct only for the
# unpinned upstream yt-dlp release, whose SHA2-256SUMS we do not control).
require_own_checksum() {
  local name="$1" sums="$2"
  awk -v n="$name" '$2 == n {found = 1} END {exit found ? 0 : 1}' "$sums" || fail \
    "The release is missing a checksum for $name — refusing to install." \
    "This is a packaging error in the ytdl release. Please report it."
}

# Extract a single named binary out of a downloaded zip, wherever it sits.
extract_binary() {
  local zip="$1" name="$2" dest="$3" workdir found
  workdir="$TMPDIR_YTDL/unzip-$name"
  mkdir -p "$workdir"
  unzip -o -q "$zip" -d "$workdir" || fail \
    "Could not unpack $name." \
    "The downloaded archive appears to be damaged. Run the installer again."

  found="$(find "$workdir" -type f -name "$name" 2>/dev/null | head -1)"
  [ -n "$found" ] || fail \
    "$name was not found inside the downloaded archive." \
    "The upstream archive layout may have changed. Please report this."

  mv "$found" "$dest"
  chmod +x "$dest"
}

# ──────────────────────────────────────────────────────────────────
#  yt-dlp
# ──────────────────────────────────────────────────────────────────
install_ytdlp() {
  step "Installing yt-dlp"

  if ytdlp_is_current; then
    ok "yt-dlp $YTDLP_TARGET is already what ytdl requires"
    return 0
  fi

  # Fetched by TAG, so a pin that names an OLDER yt-dlp than the installed one
  # reinstalls through this same path: the comparison is equality, so a rollback
  # and an upgrade are the same operation.
  local base="$YTDLP_RELEASES/$YTDLP_TARGET"
  local sums="$TMPDIR_YTDL/SHA2-256SUMS"
  download "$base/SHA2-256SUMS" "$sums"

  # Universal standalone build with Python embedded — needs macOS 10.15+, which
  # is now the floor, so it serves every supported target.
  local tmp="$TMPDIR_YTDL/yt-dlp_macos"
  info "Downloading yt-dlp $YTDLP_TARGET…"
  download "$base/yt-dlp_macos" "$tmp"
  verify_checksum "$tmp" "yt-dlp_macos" "$sums"
  mv "$tmp" "$INSTALL_DIR/yt-dlp"
  chmod +x "$INSTALL_DIR/yt-dlp"
  # Binaries fetched with curl are not quarantined, but clear the attribute
  # defensively, consistent with ffmpeg and ytdl.
  xattr -d com.apple.quarantine "$INSTALL_DIR/yt-dlp" 2>/dev/null || true

  ok "yt-dlp installed"
}

# ──────────────────────────────────────────────────────────────────
#  ffmpeg
# ──────────────────────────────────────────────────────────────────
# ffmpeg is required, not optional: ytdl extracts audio and embeds cover art.
# Source depends on the target — see docs/distribution.md.
# The URL now names an exact, immutable build rather than following a "latest"
# redirect. That is what makes the checksum in deps.conf mean something: a
# redirect can point somewhere new tomorrow, a build id cannot (ADR-0016 §12).
# martin-riedl's amd64 build covers every supported Intel Mac (10.15+).
ffmpeg_url_for() {
  printf '%s/%s/%s/%s.zip\n' "$FFMPEG_BASE" "$ARCH_KEY" "$FFMPEG_TARGET" "$1"
}

install_ffmpeg() {
  step "Installing ffmpeg"

  if ffmpeg_is_current; then
    ok "ffmpeg $FFMPEG_TARGET is already what ytdl requires"
    return 0
  fi

  local tool zip
  for tool in ffmpeg ffprobe; do
    zip="$TMPDIR_YTDL/$tool.zip"
    info "Downloading ${tool} ${FFMPEG_TARGET}…"
    download "$(ffmpeg_url_for "$tool")" "$zip"
    verify_pinned_checksum "$zip" "$(deps_get "ffmpeg_sha256_${ARCH_KEY}_${tool}")" "$tool.zip"
    extract_binary "$zip" "$tool" "$INSTALL_DIR/$tool"
  done

  # Binaries fetched with curl are not quarantined, but a previous manual
  # download might have left the attribute behind.
  xattr -d com.apple.quarantine "$INSTALL_DIR/ffmpeg" 2>/dev/null || true
  xattr -d com.apple.quarantine "$INSTALL_DIR/ffprobe" 2>/dev/null || true

  ok "ffmpeg and ffprobe installed"
}

# ──────────────────────────────────────────────────────────────────
#  ytdl itself
# ──────────────────────────────────────────────────────────────────
# The compiled ytdl binary is published per-arch on each release.
ytdl_asset_for() {
  case "$ARCH" in
    arm64) echo "ytdl_macos_arm64" ;;
    *)     echo "ytdl_macos_amd64" ;;
  esac
}

install_ytdl() {
  step "Installing ytdl"

  if ytdl_is_current; then
    ok "ytdl $YTDL_TARGET is already the newest"
    return 0
  fi

  local asset sums tmp
  asset="$(ytdl_asset_for)"
  sums="$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
  tmp="$TMPDIR_YTDL/$asset"

  info "Downloading ${asset}…"
  download "$RELEASE_BASE/$asset" "$tmp"
  download "$RELEASE_BASE/SHA2-256SUMS" "$sums"

  # This is our own asset (unlike the unpinned upstream yt-dlp): a missing
  # checksum entry is a packaging bug, so hard-fail instead of warn-and-skip.
  require_own_checksum "$asset" "$sums"
  verify_checksum "$tmp" "$asset" "$sums"

  # Replacing a running binary via rename is safe on macOS: the running process
  # keeps its open inode until it exits, so --update can replace ytdl in place.
  # Relies on $TMPDIR and $INSTALL_DIR being on the same filesystem (true on
  # stock macOS) so mv is an atomic rename, not a copy+unlink.
  mv "$tmp" "$INSTALL_DIR/ytdl"
  chmod +x "$INSTALL_DIR/ytdl"
  # Binaries fetched with curl are not quarantined, but clear the attribute
  # defensively, consistent with ffmpeg.
  xattr -d com.apple.quarantine "$INSTALL_DIR/ytdl" 2>/dev/null || true

  ok "ytdl installed"
}

# ──────────────────────────────────────────────────────────────────
#  PATH
# ──────────────────────────────────────────────────────────────────
# Terminal.app starts *login* shells, so the profile files are what matter:
# .zprofile for zsh (default since Catalina) and .bash_profile for bash
# (default through Mojave).
setup_path() {
  step "Setting up your PATH"

  local marker='# added by the ytdl installer'
  local line='export PATH="$HOME/.local/bin:$PATH"'
  local rc bash_rc updated=0

  # bash: don't create .bash_profile if .profile is the file already in use —
  # creating it would stop bash from reading .profile at all.
  if [ -f "$HOME/.bash_profile" ]; then
    bash_rc="$HOME/.bash_profile"
  elif [ -f "$HOME/.profile" ]; then
    bash_rc="$HOME/.profile"
  else
    bash_rc="$HOME/.bash_profile"
  fi

  for rc in "$HOME/.zprofile" "$bash_rc"; do
    if [ -f "$rc" ] && grep -Fq "$marker" "$rc"; then
      continue
    fi
    printf '\n%s\n%s\n' "$marker" "$line" >> "$rc"
    updated=1
    info "Updated ${rc/#$HOME/~}"
  done

  if [ "$updated" -eq 0 ]; then
    ok "PATH already configured"
  fi
}

# ──────────────────────────────────────────────────────────────────
#  Verification
# ──────────────────────────────────────────────────────────────────
verify_install() {
  step "Verifying"

  local v
  if ! v="$(PATH="$INSTALL_DIR:$PATH" "$INSTALL_DIR/ytdl" --version 2>&1)"; then
    fail "ytdl was installed but does not run." \
      "Please report this along with the output above."
  fi
  # `ytdl --version` prints its own labelled lines (ytdl x.y.z / yt-dlp …);
  # show just the first so the status line reads "✓ ytdl 1.0.0", not
  # "✓ ytdl ytdl 1.0.0" with the yt-dlp line dangling under the checkmark.
  ok "$(printf '%s\n' "$v" | head -1)"

  PATH="$INSTALL_DIR:$PATH" "$INSTALL_DIR/yt-dlp" --version >/dev/null 2>&1 \
    || fail "yt-dlp was installed but does not run." \
       "Please report this along with your macOS version."
  ok "yt-dlp works"

  "$INSTALL_DIR/ffmpeg" -version >/dev/null 2>&1 \
    || fail "ffmpeg was installed but does not run." \
       "Please report this."
  ok "ffmpeg works"
}

# ──────────────────────────────────────────────────────────────────
#  Main
# ──────────────────────────────────────────────────────────────────
main() {
  local arg
  for arg in "$@"; do
    case "$arg" in
      --force) FORCE=1 ;;
      *) fail "Unknown option: $arg" "The only option is --force." ;;
    esac
  done

  printf '\n%sytdl installer%s\n' "$B" "$R"
  printf '%sInstalling into %s%s\n' "$DIM" "$INSTALL_DIR" "$R"
  [ "$FORCE" -eq 1 ] && printf '%sReinstalling everything (--force)%s\n' "$DIM" "$R"

  detect_platform

  TMPDIR_YTDL="$(mktemp -d)"
  mkdir -p "$INSTALL_DIR"

  # The pin is read and resolved BEFORE anything is fetched, so each component can
  # be compared against a concrete target and skipped when it already matches
  # (ADR-0016 §11). An unreadable pin aborts here, having installed nothing.
  load_deps
  resolve_targets

  install_ytdlp
  install_ffmpeg
  install_ytdl
  setup_path
  verify_install
  write_marker

  printf '\n%s%s✓ Done.%s\n\n' "$B" "$GREEN" "$R"
  printf 'Open a new Terminal window, then try:\n\n'
  printf '  %sytdl "https://youtu.be/..."%s\n\n' "$B" "$R"
  printf 'Music is saved to ~/Music/ytdl by default.\n'
  printf 'Run %sytdl --help%s for all options, %sytdl --update%s to update later.\n\n' \
    "$B" "$R" "$B" "$R"
}

# Sourcing with YTDL_INSTALLER_LIB=1 loads the functions without running the
# install, so tests can exercise them directly.
if [ "${YTDL_INSTALLER_LIB:-0}" != "1" ]; then
  main "$@"
fi
