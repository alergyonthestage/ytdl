#!/bin/bash
#
# Tests for the installer's platform detection and URL selection.
#
# These are the parts that are pure logic and can be verified anywhere. The
# actual install has to be tested on real macOS hardware — see roadmap 1.11.
#
#   ./tests/test-installer.sh
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALLER="$SCRIPT_DIR/../install.sh"

PASS=0; FAIL=0

check() {
  local desc="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    printf '  ✓ %s\n' "$desc"
    PASS=$((PASS + 1))
  else
    printf '  ✗ %s\n      expected: %s\n      actual:   %s\n' "$desc" "$expected" "$actual"
    FAIL=$((FAIL + 1))
  fi
}

# Load the installer's functions without running it.
YTDL_INSTALLER_LIB=1
export YTDL_INSTALLER_LIB
# shellcheck source=../install.sh
. "$INSTALLER"
# The installer sets -e for its own run; the tests need to inspect failures.
set +e

# ──────────────────────────────────────────────────────────────────
#  Platform detection
# ──────────────────────────────────────────────────────────────────
# detect_platform reads sw_vers and uname; stub both to drive it.
mock_platform() {
  local version="$1" machine="$2"
  sw_vers() { printf '%s\n' "$version"; }
  uname()   { printf '%s\n' "$machine"; }
  detect_platform
}

printf '\nPlatform detection\n'

# The bug this guards against: reading only the first component of the version
# reports "10" for Catalina, routing 10.15 down the legacy path.
mock_platform "10.15.7" "x86_64" >/dev/null 2>&1
check "10.15.7 Catalina is modern"        "modern"      "$TIER"
check "10.15.7 parses minor as 15"        "15"          "$OS_MINOR"

mock_platform "10.14.6" "x86_64" >/dev/null 2>&1
check "10.14.6 Mojave is legacy"          "legacy"      "$TIER"

mock_platform "10.13.6" "x86_64" >/dev/null 2>&1
check "10.13.6 High Sierra is legacy"     "legacy"      "$TIER"

# Below 10.13 the installer aborts, so this runs in a subshell.
( mock_platform "10.12.6" "x86_64" ) >/dev/null 2>&1
check "10.12.6 Sierra aborts the install" "1" "$?"

unsupported_msg="$( ( mock_platform "10.12.6" "x86_64" ) 2>&1 || true )"
case "$unsupported_msg" in
  *10.13*) check "the abort message names the oldest supported version" "0" "0" ;;
  *)       check "the abort message names the oldest supported version" "0" "1" ;;
esac

mock_platform "11.0" "arm64" >/dev/null 2>&1
check "11.0 Big Sur is modern"            "modern"      "$TIER"
check "11.0 on Apple Silicon is arm64"    "arm64"       "$ARCH"

mock_platform "15.3.1" "arm64" >/dev/null 2>&1
check "15.3.1 Sequoia is modern"          "modern"      "$TIER"

mock_platform "26.1" "arm64" >/dev/null 2>&1
check "26.1 Tahoe is modern"              "modern"      "$TIER"

# A bare major with no minor component must not break parsing.
mock_platform "26" "arm64" >/dev/null 2>&1
check "26 (no minor) is modern"           "modern"      "$TIER"

printf '\nffmpeg source selection\n'

TIER="legacy"; ARCH="x86_64"
check "legacy uses evermeet (10.13+, Intel)" \
  "https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip" "$(ffmpeg_url_for ffmpeg)"

TIER="modern"; ARCH="arm64"
check "modern arm64 uses signed arm64 build" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"

TIER="modern"; ARCH="x86_64"
check "modern Intel uses signed amd64 build" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/amd64/release/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"

TIER="modern"; ARCH="arm64"
check "ffprobe uses the same source as ffmpeg" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/ffprobe.zip" \
  "$(ffmpeg_url_for ffprobe)"

# ──────────────────────────────────────────────────────────────────
#  Checksum verification
# ──────────────────────────────────────────────────────────────────
printf '\nChecksum verification\n'

TMPDIR_YTDL="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_YTDL"' EXIT

payload="$TMPDIR_YTDL/payload"
sums="$TMPDIR_YTDL/SHA2-256SUMS"
printf 'hello\n' > "$payload"
real_hash="$(shasum -a 256 "$payload" | awk '{print $1}')"

printf '%s  yt-dlp_macos\n' "$real_hash" > "$sums"
verify_checksum "$payload" "yt-dlp_macos" "$sums" >/dev/null 2>&1
check "matching checksum passes" "0" "$?"

printf '%s  yt-dlp_macos\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$sums"
( verify_checksum "$payload" "yt-dlp_macos" "$sums" >/dev/null 2>&1 )
check "mismatched checksum aborts" "1" "$?"

# A name absent from the sums file must not be silently treated as a match.
printf '%s  some-other-file\n' "$real_hash" > "$sums"
verify_checksum "$payload" "yt-dlp_macos" "$sums" >/dev/null 2>&1
check "unlisted file warns but continues" "0" "$?"

printf '\n%d passed, %d failed\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
