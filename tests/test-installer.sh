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
# reports "10" for Catalina, routing 10.15 below the floor.
mock_platform "10.15.7" "x86_64" >/dev/null 2>&1
check "10.15.7 Catalina is supported"     "supported"   "$TIER"
check "10.15.7 parses minor as 15"        "15"          "$OS_MINOR"

# The floor is now 10.15 Catalina: 10.13 and 10.14 abort (detect_platform exits,
# so these run in a subshell).
( mock_platform "10.14.6" "x86_64" ) >/dev/null 2>&1
check "10.14.6 Mojave aborts (below floor)"      "1" "$?"

( mock_platform "10.13.6" "x86_64" ) >/dev/null 2>&1
check "10.13.6 High Sierra aborts (below floor)" "1" "$?"

( mock_platform "10.12.6" "x86_64" ) >/dev/null 2>&1
check "10.12.6 Sierra aborts the install"        "1" "$?"

unsupported_msg="$( ( mock_platform "10.14.6" "x86_64" ) 2>&1 || true )"
case "$unsupported_msg" in
  *10.15*) check "the abort message names the 10.15 floor" "0" "0" ;;
  *)       check "the abort message names the 10.15 floor" "0" "1" ;;
esac

mock_platform "11.0" "arm64" >/dev/null 2>&1
check "11.0 Big Sur is supported"         "supported"   "$TIER"
check "11.0 on Apple Silicon is arm64"    "arm64"       "$ARCH"

mock_platform "15.3.1" "arm64" >/dev/null 2>&1
check "15.3.1 Sequoia is supported"       "supported"   "$TIER"

mock_platform "26.1" "arm64" >/dev/null 2>&1
check "26.1 Tahoe is supported"           "supported"   "$TIER"

# A bare major with no minor component must not break parsing.
mock_platform "26" "arm64" >/dev/null 2>&1
check "26 (no minor) is supported"        "supported"   "$TIER"

printf '\nffmpeg source selection\n'

ARCH="arm64"
check "arm64 uses the signed arm64 build" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"

ARCH="x86_64"
check "Intel uses the signed amd64 build" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/amd64/release/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"

ARCH="arm64"
check "ffprobe uses the same source as ffmpeg" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/ffprobe.zip" \
  "$(ffmpeg_url_for ffprobe)"

printf '\nytdl binary asset selection\n'

ARCH="arm64"
check "arm64 selects ytdl_macos_arm64"    "ytdl_macos_arm64" "$(ytdl_asset_for)"

ARCH="x86_64"
check "Intel selects ytdl_macos_amd64"    "ytdl_macos_amd64" "$(ytdl_asset_for)"

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

# ──────────────────────────────────────────────────────────────────
#  Own-asset checksum hard-fail (install_ytdl)
# ──────────────────────────────────────────────────────────────────
# Our own ytdl binary is not upstream's unpinned asset: a missing checksum entry
# must hard-fail, the opposite of verify_checksum's tolerant warn-and-continue.
printf '\nOwn-asset checksum hard-fail\n'

printf '%s  ytdl_macos_arm64\n' "$real_hash" > "$sums"
require_own_checksum "ytdl_macos_arm64" "$sums" >/dev/null 2>&1
check "present own-asset checksum passes" "0" "$?"

# A name missing from SHA2-256SUMS is a packaging bug — require_own_checksum
# calls fail (which exits), so run it in a subshell to capture the code.
printf '%s  ytdl_macos_amd64\n' "$real_hash" > "$sums"
( require_own_checksum "ytdl_macos_arm64" "$sums" >/dev/null 2>&1 )
check "missing own-asset checksum hard-fails" "1" "$?"

printf '\n%d passed, %d failed\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
