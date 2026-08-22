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

# detect_platform derives ARCH_KEY — deps.conf's spelling of the architecture —
# from uname's, which says x86_64 where the pin says amd64.
mock_platform "15.3.1" "arm64" >/dev/null 2>&1
check "uname arm64 maps to the arm64 pin"  "arm64" "$ARCH_KEY"
mock_platform "15.3.1" "x86_64" >/dev/null 2>&1
check "uname x86_64 maps to the amd64 pin" "amd64" "$ARCH_KEY"

printf '\nffmpeg source selection\n'

# The URL names an EXACT build now, not a "latest" redirect: that is what makes
# the checksum in deps.conf mean anything, since a redirect can point somewhere
# new tomorrow and a build id cannot (ADR-0016 §12).
FFMPEG_TARGET="1785863997_9.0"
ARCH_KEY="arm64"
check "arm64 uses the pinned arm64 build" \
  "https://ffmpeg.martin-riedl.de/download/macos/arm64/1785863997_9.0/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"

# The two architectures carry DIFFERENT build ids upstream, so the pin declares
# one each and this must follow whichever was resolved.
FFMPEG_TARGET="1785871427_9.0"
ARCH_KEY="amd64"
check "Intel uses the pinned amd64 build" \
  "https://ffmpeg.martin-riedl.de/download/macos/amd64/1785871427_9.0/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"

FFMPEG_TARGET="1785863997_9.0"
ARCH_KEY="arm64"
check "ffprobe uses the same build as ffmpeg" \
  "https://ffmpeg.martin-riedl.de/download/macos/arm64/1785863997_9.0/ffprobe.zip" \
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

# ──────────────────────────────────────────────────────────────────
#  The pin: deps.conf
# ──────────────────────────────────────────────────────────────────
# Everything below is pure bash against files on disk — no network, and no
# machine state outside the temp dirs each block makes for itself.
printf '\nReading the pin\n'

VALID_DEPS='# What ytdl requires.
yt_dlp_version              = latest
ffmpeg_build_arm64          = 1785863997_9.0
ffmpeg_build_amd64          = 1785871427_9.0
ffmpeg_sha256_arm64_ffmpeg  = aaaa
ffmpeg_sha256_arm64_ffprobe = bbbb
ffmpeg_sha256_amd64_ffmpeg  = cccc
ffmpeg_sha256_amd64_ffprobe = dddd'

write_deps() { printf '%s\n' "$1" > "$TMPDIR_YTDL/deps.conf"; DEPS_FILE="$TMPDIR_YTDL/deps.conf"; }

write_deps "$VALID_DEPS"
validate_deps "$DEPS_FILE" 2>/dev/null
check "a well-formed pin validates"           "0" "$?"
check "yt_dlp_version is read"                "latest"         "$(deps_get yt_dlp_version)"
check "the arm64 build id is read"            "1785863997_9.0" "$(deps_get ffmpeg_build_arm64)"
check "the amd64 build id is read"            "1785871427_9.0" "$(deps_get ffmpeg_build_amd64)"
check "a checksum is read"                    "aaaa"           "$(deps_get ffmpeg_sha256_arm64_ffmpeg)"
check "an absent key reads as empty"          ""               "$(deps_get not_a_key)"

# Alignment, tabs and comments are cosmetic; the parser must not care.
write_deps "$(printf '\n   # comment\n\n\tyt_dlp_version\t=\t2026.07.04   \nffmpeg_build_arm64=1_9.0')"
validate_deps "$DEPS_FILE" 2>/dev/null
check "spacing and comments are tolerated"    "0" "$?"
check "a tab-separated value is trimmed"      "2026.07.04" "$(deps_get yt_dlp_version)"

# An unknown key is refused HERE, though the Go probe tolerates one. The installer
# arrives from the same commit as deps.conf, so a key it does not know is a typo
# in the pin, not a version skew — and a typo in the pin is exactly what must not
# reach anyone's machine.
write_deps "$VALID_DEPS
ytdl_version = v9.9.9"
( validate_deps "$DEPS_FILE" ) 2>/dev/null
check "an unknown key is refused"             "1" "$?"

write_deps "$VALID_DEPS
this line has no equals sign"
( validate_deps "$DEPS_FILE" ) 2>/dev/null
check "a malformed line is refused"           "1" "$?"

write_deps "$VALID_DEPS
 = orphan"
( validate_deps "$DEPS_FILE" ) 2>/dev/null
check "an empty key is refused"               "1" "$?"

write_deps '<!DOCTYPE html><html><body>404</body></html>'
( validate_deps "$DEPS_FILE" ) 2>/dev/null
check "an HTML error page is refused"         "1" "$?"

# The load path aborts when the pin cannot be fetched at all, having installed
# nothing. It must NEVER fall back to "latest": that would be indistinguishable
# from the policy currently BEING latest, so the day a rollback is pinned the
# fallback would quietly ignore it.
DEPS_URL="file://$TMPDIR_YTDL/there-is-no-such-file"
( load_deps ) >/dev/null 2>&1
check "an unfetchable pin aborts"             "1" "$?"

abort_msg="$( ( DEPS_URL="file://$TMPDIR_YTDL/nope"; load_deps ) 2>&1 || true )"
case "$abort_msg" in
  *"nothing was installed"*) check "the abort says nothing was installed" "0" "0" ;;
  *)                         check "the abort says nothing was installed" "0" "1" ;;
esac
case "$abort_msg" in
  *latest*) check "the abort never mentions falling back to latest" "0" "1" ;;
  *)        check "the abort never mentions falling back to latest" "0" "0" ;;
esac

# ──────────────────────────────────────────────────────────────────
#  Resolving the policy
# ──────────────────────────────────────────────────────────────────
# The policy is resolved to concrete versions BEFORE anything is fetched. That
# ordering is the whole basis of idempotence: "latest" cannot be compared against
# what is installed, a tag can (ADR-0016 §11).
printf '\nResolving the policy\n'

# Stub the one thing that would otherwise reach the network.
latest_tag() {
  case "$1" in
    yt-dlp/yt-dlp) printf '2026.08.02\n' ;;
    *)             printf 'v2.2.0\n' ;;
  esac
}

ARCH_KEY="arm64"
write_deps "$VALID_DEPS"
resolve_targets >/dev/null 2>&1
check "\"latest\" resolves to a concrete tag" "2026.08.02"     "$YTDLP_TARGET"
check "the policy placeholder does not survive" "0" "$([ "$YTDLP_TARGET" != "latest" ] && echo 0 || echo 1)"
check "the ffmpeg build for this arch is picked" "1785863997_9.0" "$FFMPEG_TARGET"
check "ytdl's own target is resolved too"     "v2.2.0"         "$YTDL_TARGET"

ARCH_KEY="amd64"
resolve_targets >/dev/null 2>&1
check "a different arch picks its own build"  "1785871427_9.0" "$FFMPEG_TARGET"

# An explicit tag is used verbatim — no redirect read, nothing to resolve.
write_deps "$(printf 'yt_dlp_version = 2026.01.01\nffmpeg_build_amd64 = 1_9.0')"
resolve_targets >/dev/null 2>&1
check "an explicit tag is taken as-is"        "2026.01.01" "$YTDLP_TARGET"

# A pin that names every architecture except this one cannot be acted on.
write_deps "$(printf 'yt_dlp_version = latest\nffmpeg_build_arm64 = 1_9.0')"
ARCH_KEY="amd64"
( resolve_targets ) >/dev/null 2>&1
check "no build for this arch aborts"         "1" "$?"

write_deps "$(printf 'ffmpeg_build_amd64 = 1_9.0')"
( resolve_targets ) >/dev/null 2>&1
check "no yt-dlp version at all aborts"       "1" "$?"

# When the policy IS "latest" but the redirect cannot be read, there is no pin —
# and the installer stops rather than guessing.
write_deps "$VALID_DEPS"
ARCH_KEY="arm64"
latest_tag() { return 1; }
( resolve_targets ) >/dev/null 2>&1
check "an unresolvable \"latest\" aborts"     "1" "$?"

# ytdl's own target is best-effort, though: not knowing it means not skipping,
# which is the safe direction.
write_deps "$(printf 'yt_dlp_version = 2026.01.01\nffmpeg_build_arm64 = 1_9.0')"
resolve_targets >/dev/null 2>&1
check "an unreadable ytdl tag is tolerated"   "" "$YTDL_TARGET"

# ──────────────────────────────────────────────────────────────────
#  Idempotence: skip what is already current
# ──────────────────────────────────────────────────────────────────
printf '\nSkipping what is already current\n'

INSTALL_DIR="$TMPDIR_YTDL/bin"
mkdir -p "$INSTALL_DIR"
XDG_STATE_HOME="$TMPDIR_YTDL/state"
export XDG_STATE_HOME

# fake_tool NAME "what --version prints"
fake_tool() {
  printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$2" > "$INSTALL_DIR/$1"
  chmod +x "$INSTALL_DIR/$1"
}

FORCE=0

fake_tool yt-dlp "2026.07.04"
YTDLP_TARGET="2026.07.04"
ytdlp_is_current
check "yt-dlp at the pinned version is skipped"     "0" "$?"

YTDLP_TARGET="2026.08.02"
ytdlp_is_current
check "yt-dlp behind the pin is reinstalled"        "1" "$?"

# The comparison is EQUALITY, never ordering, so a pin naming an OLDER yt-dlp —
# the rollback lever — reinstalls through the very same path as an upgrade.
YTDLP_TARGET="2026.06.01"
ytdlp_is_current
check "a pinned OLDER yt-dlp reinstalls (downgrade)" "1" "$?"

rm -f "$INSTALL_DIR/yt-dlp"
YTDLP_TARGET="2026.07.04"
ytdlp_is_current
check "an absent yt-dlp is installed"               "1" "$?"

# ffmpeg cannot describe its own build, so the marker is the only exact answer —
# and the binaries it describes must still be there.
mkdir -p "$(marker_dir)"
printf 'ffmpeg_build = 1785863997_9.0\n' > "$(marker_path)"
fake_tool ffmpeg "9.0"
fake_tool ffprobe "9.0"
FFMPEG_TARGET="1785863997_9.0"
ffmpeg_is_current
check "ffmpeg matching the marker is skipped"       "0" "$?"

FFMPEG_TARGET="1900000000_9.1"
ffmpeg_is_current
check "ffmpeg behind the pin is reinstalled"        "1" "$?"

FFMPEG_TARGET="1785863997_9.0"
rm -f "$INSTALL_DIR/ffprobe"
ffmpeg_is_current
check "a marker without ffprobe is not trusted"     "1" "$?"
fake_tool ffprobe "9.0"

# A copy recorded as NOT attested is never current, whatever its build id says.
# The trigger is the intended remedy: upstream withdraws a build, every machine
# falls back, the maintainer re-pins deps.conf to what they fell back TO — and
# skipping here would promote bytes that were never checksummed to "verificata".
# Not skipping re-fetches from the pinned URL and actually obtains the
# attestation (ADR-0016 §15).
printf 'ffmpeg_build = 1785863997_9.0\nffmpeg_pinned = false\n' > "$(marker_path)"
ffmpeg_is_current
check "an unattested ffmpeg is reinstalled, not skipped" "1" "$?"

printf 'ffmpeg_build = 1785863997_9.0\nffmpeg_pinned = true\n' > "$(marker_path)"
ffmpeg_is_current
check "an attested ffmpeg at the pin is still skipped"   "0" "$?"

# An install predating ADR-0016 §15 has no ffmpeg_pinned key at all. Absent means
# "nothing ever fell back", so it must not cost those machines a reinstall.
printf 'ffmpeg_build = 1785863997_9.0\n' > "$(marker_path)"
ffmpeg_is_current
check "a marker with no ffmpeg_pinned key still skips"   "0" "$?"

rm -f "$(marker_path)"
ffmpeg_is_current
check "no marker means ffmpeg is reinstalled"       "1" "$?"

# ytdl prints "ytdl v2.1.0"; the version is the second field.
fake_tool ytdl "ytdl v2.1.0"
YTDL_TARGET="v2.1.0"
ytdl_is_current
check "ytdl at the newest tag is skipped"           "0" "$?"

YTDL_TARGET="v2.2.0"
ytdl_is_current
check "ytdl behind the newest tag is reinstalled"   "1" "$?"

# An unresolved target is nothing to compare against, so we must not skip.
YTDL_TARGET=""
ytdl_is_current
check "an unresolved ytdl target never skips"       "1" "$?"

# --force answers no to all three: a retry after a failed update must not skip the
# component that failed just because its version string happens to look right.
FORCE=1
YTDLP_TARGET="2026.07.04"; FFMPEG_TARGET="1785863997_9.0"; YTDL_TARGET="v2.1.0"
fake_tool yt-dlp "2026.07.04"
printf 'ffmpeg_build = 1785863997_9.0\n' > "$(marker_path)"
ytdlp_is_current;  check "--force reinstalls yt-dlp" "1" "$?"
ffmpeg_is_current; check "--force reinstalls ffmpeg" "1" "$?"
ytdl_is_current;   check "--force reinstalls ytdl"   "1" "$?"
FORCE=0

# ──────────────────────────────────────────────────────────────────
#  The pin is a preference, not a precondition
# ──────────────────────────────────────────────────────────────────
# An exact build id is what makes the checksum mean anything, but it is also a
# URL that can stop existing. If a withdrawn build aborted the install, ytdl
# would become uninstallable until somebody re-pinned it — a recurring
# maintenance obligation this project refuses to take on (ADR-0016 §15).
printf '\nWhen the attested ffmpeg build is withdrawn\n'

FFMPEG_TARGET="1785863997_9.0"
ARCH_KEY="arm64"
check "the pinned URL names the exact build" \
  "https://ffmpeg.martin-riedl.de/download/macos/arm64/1785863997_9.0/ffmpeg.zip" \
  "$(ffmpeg_url_for ffmpeg)"
check "the fallback URL names no build at all" \
  "https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/ffmpeg.zip" \
  "$(ffmpeg_fallback_url_for ffmpeg)"

# The decision is the thing worth pinning. Only "upstream says this is gone" may
# lead to a fallback; "we could not ask" must abort, or a flaky connection would
# silently downgrade a verified install to an unverified one — giving away for
# free the property the checksum was introduced to buy.
check "200 verifies"                    "verify"   "$(ffmpeg_fetch_action 200)"
check "404 falls back"                  "fallback" "$(ffmpeg_fetch_action 404)"
check "410 falls back too"              "fallback" "$(ffmpeg_fetch_action 410)"
check "no answer at all ABORTS"         "abort"    "$(ffmpeg_fetch_action 000)"
check "a server error ABORTS"           "abort"    "$(ffmpeg_fetch_action 500)"
check "a proxy refusal ABORTS"          "abort"    "$(ffmpeg_fetch_action 403)"
check "an empty status ABORTS"          "abort"    "$(ffmpeg_fetch_action "")"

# And download_status really does report 000 rather than a status when it could
# not ask, which is what makes the rule above reachable.
check "an unreachable host reports no status" "000" \
  "$(download_status "http://127.0.0.1:1/gone" "$TMPDIR_YTDL/got")"

# ──────────────────────────────────────────────────────────────────
#  The marker
# ──────────────────────────────────────────────────────────────────
# What was actually installed has to survive across runs: it is how the next run
# answers "do I need to?" without asking the network, and how ytdl SHOWS which
# ffmpeg it has.
printf '\nThe installed-versions marker\n'

rm -f "$(marker_path)"
FFMPEG_TARGET="1785863997_9.0"
write_marker
check "the marker is written"        "0" "$([ -f "$(marker_path)" ] && echo 0 || echo 1)"
check "it records the ytdl version"  "v2.1.0"         "$(marker_get ytdl_version)"
check "it records the yt-dlp version" "2026.07.04"    "$(marker_get yt_dlp_version)"
check "it records the ffmpeg build"  "1785863997_9.0" "$(marker_get ffmpeg_build)"
# Whether the copy on disk is the one the pin vouches for. Without this the
# comparison would report a difference no update could ever resolve.
check "it records that ffmpeg is attested" "true" "$(marker_get ffmpeg_pinned)"
FFMPEG_PINNED=0
write_marker
check "a fallback is recorded as unattested" "false" "$(marker_get ffmpeg_pinned)"
FFMPEG_PINNED=1
write_marker
check "an unset marker key is empty" ""               "$(marker_get not_a_key)"
case "$(marker_get installed_at)" in
  20[0-9][0-9]-*T*Z) check "it records when" "0" "0" ;;
  *)                 check "it records when" "0" "1" ;;
esac

# ──────────────────────────────────────────────────────────────────
#  The pinned ffmpeg checksum
# ──────────────────────────────────────────────────────────────────
# Unlike yt-dlp's published sums, this one is the maintainer's own attestation,
# so there is no tolerant warn-and-skip: an ffmpeg installed without checking it
# is precisely the gap ADR-0016 §12 closes.
printf '\nPinned checksum verification\n'

verify_pinned_checksum "$payload" "$real_hash" "ffmpeg.zip" >/dev/null 2>&1
check "a matching pinned checksum passes"  "0" "$?"

( verify_pinned_checksum "$payload" "0000000000000000000000000000000000000000000000000000000000000000" "ffmpeg.zip" ) >/dev/null 2>&1
check "a mismatched pinned checksum aborts" "1" "$?"

( verify_pinned_checksum "$payload" "" "ffmpeg.zip" ) >/dev/null 2>&1
check "a MISSING pinned checksum aborts"    "1" "$?"

# ──────────────────────────────────────────────────────────────────
#  The committed pin
# ──────────────────────────────────────────────────────────────────
# The file that actually ships must parse under the rules above, and must name a
# build for every architecture the installer can select.
printf '\nThe committed deps.conf\n'

DEPS_FILE="$SCRIPT_DIR/../deps.conf"
check "deps.conf exists" "0" "$([ -f "$DEPS_FILE" ] && echo 0 || echo 1)"
validate_deps "$DEPS_FILE" 2>/dev/null
check "the committed pin validates" "0" "$?"
check "it names a yt-dlp policy"    "0" "$([ -n "$(deps_get yt_dlp_version)" ] && echo 0 || echo 1)"
for a in arm64 amd64; do
  check "it names an ffmpeg build for $a" "0" \
    "$([ -n "$(deps_get "ffmpeg_build_$a")" ] && echo 0 || echo 1)"
  for t in ffmpeg ffprobe; do
    # 64 hex characters — a placeholder or a truncated paste must not ship.
    case "$(deps_get "ffmpeg_sha256_${a}_${t}")" in
      [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f])
        check "$a/$t carries a real sha256" "0" "0" ;;
      *)
        check "$a/$t carries a real sha256" "0" "1" ;;
    esac
  done
done

# ──────────────────────────────────────────────────────────────────
#  The doubt has to outlive the run that incurred it
# ──────────────────────────────────────────────────────────────────
# write_marker runs only after every other step succeeds, while extract_binary
# replaces the binaries in the middle. Everything above tests the READ side —
# whether a recorded doubt is acted on. These test the WRITE side: that the doubt
# is recorded the moment it is incurred, so an aborted run cannot leave
# "verificata" standing over bytes nothing checksummed (ADR-0016 §15).
printf '\nThe unattested mark is durable\n'

mkdir -p "$(marker_dir)"
cat > "$(marker_path)" <<'MARKER'
ytdl_version = v2.1.0
yt_dlp_version = 2026.07.04
ffmpeg_build = 1785863997_9.0
ffmpeg_pinned = true
MARKER
marker_mark_unattested
check "the mark lands immediately"          "false"          "$(marker_get ffmpeg_pinned)"
check "it keeps the rest of the marker"     "1785863997_9.0" "$(marker_get ffmpeg_build)"
check "it keeps the ytdl version"           "v2.1.0"         "$(marker_get ytdl_version)"

# Applied twice — the fallback arm runs once per tool — it must not accumulate.
marker_mark_unattested
check "marking twice writes one key"        "1" \
  "$(grep -c '^ffmpeg_pinned' "$(marker_path)")"

# With no marker at all it still has to say something, or the doubt is lost.
rm -f "$(marker_path)"
marker_mark_unattested
check "it works with no marker to amend"    "false" "$(marker_get ffmpeg_pinned)"

# And the read side then refuses to skip, which is what makes the mark worth
# writing: the two halves only work together.
fake_tool ffmpeg "9.0"
fake_tool ffprobe "9.0"
printf 'ffmpeg_build = 1785863997_9.0\n' > "$(marker_path)"
marker_mark_unattested
FORCE=0; FFMPEG_TARGET="1785863997_9.0"
ffmpeg_is_current
check "a marked copy is never already current" "1" "$?"

# ──────────────────────────────────────────────────────────────────
#  Portability: the shell that will actually run this is bash 3.2
# ──────────────────────────────────────────────────────────────────
# Every check above runs under the CONTAINER's bash (5.x). The installer runs on
# macOS, which ships bash 3.2 — and one parser difference between them shipped a
# blocking defect that 101 green assertions did not see (V21).
#
# bash 3.2 keeps reading the bytes of a multi-byte character as part of an
# identifier, so an unbraced expansion followed directly by a non-ASCII character
# names a DIFFERENT variable. Under `set -u` that is a fatal error, and because it
# lives in a progress message it fires only on the real path, on a real Mac,
# mid-install.
#
# There is no way to exercise bash 3.2 from here, so this refuses the SHAPE. It is
# a static check of the file, deliberately, because that is the part that is
# testable without the other shell.
printf '\nbash 3.2 portability\n'

# LC_ALL=C makes [^ -~] mean "any byte outside printable ASCII", which is the
# actual hazard; a UTF-8 locale would fold multi-byte characters into one class
# and match nothing. Written to work under BSD grep too, since a maintainer may
# run this suite on the Mac.
unbraced="$(LC_ALL=C grep -n '\$[A-Za-z_][A-Za-z0-9_]*[^ -~]' "$INSTALLER" || true)"
if [ -n "$unbraced" ]; then
  printf '%s\n' "$unbraced" >&2
  check "no unbraced expansion is followed by a non-ASCII byte" "" "$unbraced"
else
  check "no unbraced expansion is followed by a non-ASCII byte" "0" "0"
fi

# The exit status has to survive the EXIT trap, because that status is the ONLY
# thing the GUI's update runner uses to decide "done" versus "failed" — and a
# failed install recorded as done is what V21/V23 looked like from the page.
#
# HONEST LIMIT: bash 5 preserves $? across an EXIT trap by itself, so this passes
# whether or not the trap propagates the status explicitly. It pins the INVARIANT
# and catches a gross break; it does not reproduce the shell difference that made
# the invariant worth pinning, and no test here can.
#
# Run as a separate process, deliberately: sourcing the script would install its
# trap into this shell. On a non-macOS host detect_platform calls fail(), which is
# a real abort through the real trap, and needs no network.
# YTDL_INSTALLER_LIB must be cleared for this one: the harness exports it so the
# script can be sourced for its functions, and it makes the script return before
# main() ever runs — which would make this assertion pass on nothing.
env -u YTDL_INSTALLER_LIB bash "$INSTALLER" >/dev/null 2>&1
check "an aborted install exits non-zero through the EXIT trap" "1" "$?"

printf '\n%d passed, %d failed\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
