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
INSTALL_DIR="${YTDL_INSTALL_DIR:-$HOME/.local/bin}"

YTDLP_BASE="https://github.com/yt-dlp/yt-dlp/releases/latest/download"
# The compiled ytdl binaries are published per release; /latest/ gives the
# newest without pinning a version (see docs/decisions/0005).
RELEASE_BASE="https://github.com/$REPO_SLUG/releases/latest/download"

# Populated by detect_platform()
OS_MAJOR=""; OS_MINOR=""; TIER=""; ARCH=""

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

  local sums="$TMPDIR_YTDL/SHA2-256SUMS"
  download "$YTDLP_BASE/SHA2-256SUMS" "$sums"

  # Universal standalone build with Python embedded — needs macOS 10.15+, which
  # is now the floor, so it serves every supported target.
  local tmp="$TMPDIR_YTDL/yt-dlp_macos"
  info "Downloading the standalone build…"
  download "$YTDLP_BASE/yt-dlp_macos" "$tmp"
  verify_checksum "$tmp" "yt-dlp_macos" "$sums"
  mv "$tmp" "$INSTALL_DIR/yt-dlp"
  chmod +x "$INSTALL_DIR/yt-dlp"

  ok "yt-dlp installed"
}

# ──────────────────────────────────────────────────────────────────
#  ffmpeg
# ──────────────────────────────────────────────────────────────────
# ffmpeg is required, not optional: ytdl extracts audio and embeds cover art.
# Source depends on the target — see docs/distribution.md.
ffmpeg_url_for() {
  local tool="$1"
  if [ "$ARCH" = "arm64" ]; then
    echo "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/$tool.zip"
  else
    # martin-riedl's amd64 build covers every supported Intel Mac (10.15+).
    echo "https://ffmpeg.martin-riedl.de/redirect/latest/macos/amd64/release/$tool.zip"
  fi
}

install_ffmpeg() {
  step "Installing ffmpeg"

  local tool zip
  for tool in ffmpeg ffprobe; do
    zip="$TMPDIR_YTDL/$tool.zip"
    info "Downloading ${tool}…"
    download "$(ffmpeg_url_for "$tool")" "$zip"
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

  local asset sums tmp
  asset="$(ytdl_asset_for)"
  sums="$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
  tmp="$TMPDIR_YTDL/$asset"

  info "Downloading ${asset}…"
  download "$RELEASE_BASE/$asset" "$tmp"
  download "$RELEASE_BASE/SHA2-256SUMS" "$sums"

  # Unlike yt-dlp (an unpinned upstream release), this is our own asset: a name
  # missing from SHA2-256SUMS is a release.yml packaging bug, so hard-fail rather
  # than fall through verify_checksum's tolerant warn-and-skip.
  awk -v n="$asset" '$2 == n {found = 1} END {exit found ? 0 : 1}' "$sums" || fail \
    "The release is missing a checksum for $asset — refusing to install." \
    "This is a packaging error in the ytdl release. Please report it."
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
  printf '\n%sytdl installer%s\n' "$B" "$R"
  printf '%sInstalling into %s%s\n' "$DIM" "$INSTALL_DIR" "$R"

  detect_platform

  TMPDIR_YTDL="$(mktemp -d)"
  mkdir -p "$INSTALL_DIR"

  install_ytdlp
  install_ffmpeg
  install_ytdl
  setup_path
  verify_install

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
