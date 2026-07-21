#!/bin/bash
#
# ytdl installer — provisions ytdl and its dependencies on macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/OWNER/yt-download/main/install.sh | bash
#
# Installs into ~/.local/bin without sudo. See docs/distribution.md for the
# constraints behind the choices made here.
#
set -euo pipefail

# ──────────────────────────────────────────────────────────────────
#  Configuration
# ──────────────────────────────────────────────────────────────────
# TODO: set to the real GitHub slug before publishing the repository.
REPO_SLUG="${YTDL_REPO:-OWNER/yt-download}"
REPO_BRANCH="${YTDL_BRANCH:-main}"
INSTALL_DIR="${YTDL_INSTALL_DIR:-$HOME/.local/bin}"

YTDLP_BASE="https://github.com/yt-dlp/yt-dlp/releases/latest/download"
PYTHON_DOWNLOAD_URL="https://www.python.org/downloads/macos/"
MIN_PYTHON="3.10"

# Populated by detect_platform()
OS_MAJOR=""; OS_MINOR=""; TIER=""; ARCH=""; PYTHON=""

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
# Reading only the first component misreads 10.15 as "10" and would route
# Catalina down the legacy path — compare minor too while major is 10.
detect_platform() {
  command -v sw_vers >/dev/null 2>&1 || fail \
    "This installer only supports macOS." \
    "Windows and Linux support is planned but not implemented yet."

  local ver rest
  ver="$(sw_vers -productVersion)"
  OS_MAJOR="${ver%%.*}"
  rest="${ver#*.}"
  OS_MINOR="${rest%%.*}"
  case "$OS_MINOR" in ''|*[!0-9]*) OS_MINOR=0 ;; esac

  if [ "$OS_MAJOR" -ge 11 ]; then
    TIER="modern"
  elif [ "$OS_MAJOR" -eq 10 ] && [ "$OS_MINOR" -ge 15 ]; then
    TIER="modern"
  elif [ "$OS_MAJOR" -eq 10 ] && [ "$OS_MINOR" -ge 13 ]; then
    TIER="legacy"
  else
    TIER="unsupported"
  fi

  ARCH="$(uname -m)"

  if [ "$TIER" = "unsupported" ]; then
    fail "macOS $ver is too old to run a current yt-dlp." \
      "The oldest supported version is macOS 10.13 High Sierra." \
      "" \
      "yt-dlp stopped building for older systems in August 2025, and no" \
      "supported Python is available for macOS below 10.13."
  fi

  info "macOS $ver ($ARCH) — using the ${TIER} install path"
}

# ──────────────────────────────────────────────────────────────────
#  Python (only needed on the legacy path)
# ──────────────────────────────────────────────────────────────────
# The zipimport yt-dlp is a Python script. Its shebang points at whatever
# python3 is first in PATH, which on old macOS is missing or too old — so we
# locate a suitable interpreter ourselves and call it explicitly.
python_is_new_enough() {
  "$1" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 10) else 1)' 2>/dev/null
}

find_python() {
  local candidate
  for candidate in \
    /Library/Frameworks/Python.framework/Versions/3.*/bin/python3 \
    /usr/local/bin/python3 \
    "$(command -v python3 2>/dev/null || true)"
  do
    [ -n "$candidate" ] || continue
    [ -x "$candidate" ] || continue
    if python_is_new_enough "$candidate"; then
      PYTHON="$candidate"
      return 0
    fi
  done
  return 1
}

require_python() {
  if find_python; then
    ok "Found Python $("$PYTHON" -c 'import platform;print(platform.python_version())') at $PYTHON"
    return 0
  fi

  fail "Python $MIN_PYTHON or newer is required on macOS 10.13–10.14." \
    "Your macOS is too old for the standalone yt-dlp build, so ytdl needs" \
    "Python to run yt-dlp instead. Installing it takes about two minutes:" \
    "" \
    "  1. Open  $PYTHON_DOWNLOAD_URL" \
    "  2. Download the latest Python 3.13 macOS installer" \
    "  3. Open the downloaded .pkg and click through it" \
    "  4. Run this installer again" \
    "" \
    "That installer is signed by the Python Software Foundation, so macOS" \
    "will open it without any security warning."
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

  if [ "$TIER" = "modern" ]; then
    # Universal standalone build with Python embedded — needs macOS 10.15+.
    local tmp="$TMPDIR_YTDL/yt-dlp_macos"
    info "Downloading the standalone build…"
    download "$YTDLP_BASE/yt-dlp_macos" "$tmp"
    verify_checksum "$tmp" "yt-dlp_macos" "$sums"
    mv "$tmp" "$INSTALL_DIR/yt-dlp"
    chmod +x "$INSTALL_DIR/yt-dlp"
  else
    # Platform-independent zipimport build, run through the Python we found.
    local tmp="$TMPDIR_YTDL/yt-dlp"
    info "Downloading the Python build…"
    download "$YTDLP_BASE/yt-dlp" "$tmp"
    verify_checksum "$tmp" "yt-dlp" "$sums"
    mv "$tmp" "$INSTALL_DIR/yt-dlp.pyz"
    chmod +x "$INSTALL_DIR/yt-dlp.pyz"

    # Wrapper with an absolute interpreter path: the shebang inside the
    # zipimport build would otherwise pick up an unusable system python3.
    cat > "$INSTALL_DIR/yt-dlp" <<EOF
#!/bin/bash
# Generated by the ytdl installer — do not edit.
exec "$PYTHON" "$INSTALL_DIR/yt-dlp.pyz" "\$@"
EOF
    chmod +x "$INSTALL_DIR/yt-dlp"
  fi

  ok "yt-dlp installed"
}

# ──────────────────────────────────────────────────────────────────
#  ffmpeg
# ──────────────────────────────────────────────────────────────────
# ffmpeg is required, not optional: ytdl extracts audio and embeds cover art.
# Source depends on the target — see docs/distribution.md.
ffmpeg_url_for() {
  local tool="$1"
  if [ "$TIER" = "legacy" ]; then
    # evermeet.cx: macOS 10.13+, Intel only. Every Mojave Mac is Intel, so
    # this is not a limitation on the legacy path.
    echo "https://evermeet.cx/ffmpeg/getrelease/$tool/zip"
  elif [ "$ARCH" = "arm64" ]; then
    echo "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/$tool.zip"
  else
    echo "https://ffmpeg.martin-riedl.de/redirect/latest/macos/amd64/release/$tool.zip"
  fi
}

install_ffmpeg() {
  step "Installing ffmpeg"

  local tool zip
  for tool in ffmpeg ffprobe; do
    zip="$TMPDIR_YTDL/$tool.zip"
    info "Downloading $tool…"
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
install_ytdl() {
  step "Installing ytdl"
  local tmp="$TMPDIR_YTDL/ytdl"
  download "https://raw.githubusercontent.com/$REPO_SLUG/$REPO_BRANCH/ytdl" "$tmp"

  head -1 "$tmp" | grep -q '^#!' || fail \
    "The downloaded ytdl script looks wrong." \
    "Check that YTDL_REPO points at the right repository."

  mv "$tmp" "$INSTALL_DIR/ytdl"
  chmod +x "$INSTALL_DIR/ytdl"
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
  ok "ytdl $v"

  PATH="$INSTALL_DIR:$PATH" "$INSTALL_DIR/yt-dlp" --version >/dev/null 2>&1 \
    || fail "yt-dlp was installed but does not run." \
       "If you are on macOS 10.13 or 10.14, check that Python is still installed."
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
  [ "$TIER" = "legacy" ] && require_python

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
