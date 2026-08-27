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
#  The app bundle: pure functions
# ──────────────────────────────────────────────────────────────────
# Everything here is logic with no download behind it, which is exactly the part
# that is testable away from a Mac. What the bundle step DOES with these — the
# canary, the non-fatal failure, the write-only-when-different rule — is below.
printf '\nApp bundle: pure functions\n'

# The thin-arm64 defect ADR-0019 §1 closed: install.sh already derives ARCH_KEY
# for both architectures, so the launcher follows it rather than assuming one.
mock_platform "15.3.1" "arm64" >/dev/null 2>&1
check "arm64 selects ytdl_launch_macos_arm64" \
  "ytdl_launch_macos_arm64" "$(launcher_asset_for)"

mock_platform "15.3.1" "x86_64" >/dev/null 2>&1
check "Intel selects ytdl_launch_macos_amd64" \
  "ytdl_launch_macos_amd64" "$(launcher_asset_for)"

# ~/Applications needs no sudo, which is ADR-0001's whole shape.
#
# NOT in a subshell: check() increments PASS/FAIL, and a subshell would discard
# both — every assertion below would PRINT its result and count for nothing, so a
# real defect would leave the suite exiting 0. Saved and restored by hand instead.
APP_TEST_HOME="$(mktemp -d)"
APP_SANDBOX="$(mktemp -d)"
SAVED_HOME="$HOME"; SAVED_FORCE="$FORCE"
SAVED_APP_DIR="${YTDL_APP_DIR-}"; SAVED_APP_DIR_SET="${YTDL_APP_DIR+set}"

HOME="$APP_TEST_HOME"
unset YTDL_APP_DIR
check "the bundle defaults to ~/Applications/YTDL.app" \
  "$APP_TEST_HOME/Applications/YTDL.app" "$(app_dir)"

# YTDL_APP_DIR names the PARENT, so hack/ytdl-dev.sh can give the sandbox its own
# bundle and a dev launcher can never start the installed ytdl (design §4.6).
YTDL_APP_DIR="$APP_TEST_HOME/dev/Applications"
check "YTDL_APP_DIR moves the bundle for the dev sandbox" \
  "$APP_TEST_HOME/dev/Applications/YTDL.app" "$(app_dir)"
check "the sidecar sits in Contents/Resources" \
  "$APP_TEST_HOME/dev/Applications/YTDL.app/Contents/Resources/ytdl-path" \
  "$(app_sidecar_path)"
check "the launcher sits in Contents/MacOS" \
  "$APP_TEST_HOME/dev/Applications/YTDL.app/Contents/MacOS/YTDL" \
  "$(app_exe_path)"

# Every key §4.2 names has to be there: a plist missing CFBundleExecutable is a
# bundle macOS refuses to launch, and nothing before gate C would say so.
plist="$(render_app_plist "2.3.0")"
for key in CFBundleName CFBundleDisplayName CFBundleExecutable CFBundleIdentifier \
           CFBundlePackageType CFBundleInfoDictionaryVersion CFBundleShortVersionString \
           CFBundleVersion LSMinimumSystemVersion NSHighResolutionCapable; do
  printf '%s' "$plist" | grep -q "<key>$key</key>"
  check "Info.plist carries $key" "0" "$?"
done

printf '%s' "$plist" | grep -q '<string>2.3.0</string>'
check "Info.plist carries the installed ytdl version" "0" "$?"

printf '%s' "$plist" | grep -q "<string>io.github.alergyonthestage.ytdl</string>"
check "Info.plist carries the bundle identifier" "0" "$?"

printf '%s' "$plist" | grep -q '<string>10.15</string>'
check "Info.plist declares the 10.15 floor detect_platform enforces" "0" "$?"

# Byte-identical across two runs is what makes "write only when different" a real
# skip rather than a rewrite that happens to look the same.
check "render_app_plist is byte-identical across two runs" \
  "$(render_app_plist "2.3.0" | sha256_of /dev/stdin)" \
  "$(render_app_plist "2.3.0" | sha256_of /dev/stdin)"

check "a different version renders a different plist" "1" \
  "$([ "$(render_app_plist "2.3.0")" = "$(render_app_plist "2.4.0")" ] && echo 0 || echo 1)"

# app_is_current compares the launcher BY CHECKSUM, not by version: a release
# that does not change its bytes must not touch the bundle at all (design §4.3).
YTDL_APP_DIR="$APP_SANDBOX/Applications"
mock_platform "15.3.1" "arm64" >/dev/null 2>&1
mkdir -p "$(dirname "$(app_exe_path)")"
printf 'the launcher bytes\n' > "$(app_exe_path)"
app_sum="$(sha256_of "$(app_exe_path)")"
app_sums="$APP_SANDBOX/SHA2-256SUMS"
printf '%s  ytdl_launch_macos_arm64\n' "$app_sum" > "$app_sums"

FORCE=0
app_is_current "$app_sums"
check "a matching checksum skips the launcher" "0" "$?"

FORCE=1
app_is_current "$app_sums"
check "--force never skips the launcher" "1" "$?"

FORCE=0
printf '%s  ytdl_launch_macos_arm64\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$app_sums"
app_is_current "$app_sums"
check "a differing checksum installs" "1" "$?"

# A sums file with no entry for our asset cannot answer the question, so the safe
# direction is "not current" — the step then warns and leaves the bundle exactly
# as it found it.
: > "$app_sums"
app_is_current "$app_sums"
check "no published sum for the launcher is not 'current'" "1" "$?"

printf '%s  ytdl_launch_macos_arm64\n' "$app_sum" > "$app_sums"
rm -f "$(app_exe_path)"
app_is_current "$app_sums"
check "an absent bundle installs" "1" "$?"

HOME="$SAVED_HOME"; FORCE="$SAVED_FORCE"
if [ -n "$SAVED_APP_DIR_SET" ]; then YTDL_APP_DIR="$SAVED_APP_DIR"; else unset YTDL_APP_DIR; fi
rm -rf "$APP_TEST_HOME" "$APP_SANDBOX"

# ──────────────────────────────────────────────────────────────────
#  The app bundle: install_app_bundle
# ──────────────────────────────────────────────────────────────────
# No network: download_optional is replaced by a local copy, which is the only
# outbound call this step makes.
printf '\nApp bundle: install_app_bundle\n'

BUNDLE_HOME="$(mktemp -d)"
SAVED_HOME="$HOME"; SAVED_FORCE="$FORCE"; SAVED_INSTALL_DIR="$INSTALL_DIR"
SAVED_TMPDIR_YTDL="${TMPDIR_YTDL-}"

HOME="$BUNDLE_HOME"
YTDL_APP_DIR="$BUNDLE_HOME/Applications"
INSTALL_DIR="$BUNDLE_HOME/bin"
TMPDIR_YTDL="$BUNDLE_HOME/tmp"
FORCE=0
mkdir -p "$INSTALL_DIR" "$TMPDIR_YTDL" "$BUNDLE_HOME/release"
mock_platform "15.3.1" "arm64" >/dev/null 2>&1

# A stand-in ytdl, so ytdl_version has something to read.
printf '#!/bin/sh\necho "ytdl 2.3.0"\n' > "$INSTALL_DIR/ytdl"
chmod +x "$INSTALL_DIR/ytdl"

# The published launcher and the sums that vouch for it.
printf 'launcher v1\n' > "$BUNDLE_HOME/release/ytdl_launch_macos_arm64"
{
  printf '%s  ytdl_launch_macos_arm64\n' "$(sha256_of "$BUNDLE_HOME/release/ytdl_launch_macos_arm64")"
} > "$BUNDLE_HOME/release/SHA2-256SUMS"

# The step's only outbound call, replaced by a copy out of the fake release.
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  [ -f "$BUNDLE_HOME/release/$name" ] || return 1
  cp "$BUNDLE_HOME/release/$name" "$dest"
}

install_app_bundle >/dev/null 2>&1
check "a first run installs the bundle" "0" "$?"
check "the launcher is in place" "0" "$([ -x "$(app_exe_path)" ] && echo 0 || echo 1)"
check "Info.plist is in place"   "0" "$([ -f "$(app_plist_path)" ] && echo 0 || echo 1)"
check "PkgInfo is in place"      "0" "$([ -f "$(app_pkginfo_path)" ] && echo 0 || echo 1)"
check "the sidecar records the absolute ytdl path" \
  "$INSTALL_DIR/ytdl" "$(cat "$(app_sidecar_path)")"

# The anti-churn decision, as an assertion rather than an intention: the bundle
# directory is never deleted and never recreated, so the Dock entry, the
# Spotlight identity and any alias the user made survive an update.
printf 'canary\n' > "$(app_dir)/Contents/canary"
plist_before="$(cat "$(app_plist_path)")"
mtime_before="$(stat -c %Y "$(app_plist_path)" 2>/dev/null || stat -f %m "$(app_plist_path)")"
sleep 1

install_app_bundle >/dev/null 2>&1
check "a second run returns 0" "0" "$?"
check "the canary survives an update run" "canary" "$(cat "$(app_dir)/Contents/canary" 2>/dev/null)"
check "an unchanged plist keeps its content" "$plist_before" "$(cat "$(app_plist_path)")"
check "an unchanged plist is not rewritten (mtime)" \
  "$mtime_before" "$(stat -c %Y "$(app_plist_path)" 2>/dev/null || stat -f %m "$(app_plist_path)")"

# The sidecar follows INSTALL_DIR: an install with YTDL_INSTALL_DIR set must not
# leave a bundle pointing at the previous location.
INSTALL_DIR="$BUNDLE_HOME/elsewhere"
mkdir -p "$INSTALL_DIR"
printf '#!/bin/sh\necho "ytdl 2.3.0"\n' > "$INSTALL_DIR/ytdl"
chmod +x "$INSTALL_DIR/ytdl"
install_app_bundle >/dev/null 2>&1
check "the sidecar is rewritten when INSTALL_DIR changes" \
  "$INSTALL_DIR/ytdl" "$(cat "$(app_sidecar_path)")"

# §4.4: the app never fails the installation. A CLI that aborted because an icon
# could not be drawn would be a worse tool — and there is a real window where the
# asset is genuinely missing, between merging this cycle and cutting its release.
download_optional() { return 1; }
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "an unavailable checksum file warns and returns 0" "0" "$?"
check "and leaves the existing bundle alone" "canary" "$(cat "$(app_dir)/Contents/canary" 2>/dev/null)"

# The release has no launcher in it yet: the sums file arrives, our asset is not
# named in it, and the download 404s. Warn, leave everything as it was.
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  [ "$name" = "SHA2-256SUMS" ] || return 1
  printf '%s  ytdl_macos_arm64\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$dest"
}
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "a release with no launcher asset warns and returns 0" "0" "$?"
check "and still leaves the existing bundle alone" "canary" "$(cat "$(app_dir)/Contents/canary" 2>/dev/null)"

# Bytes we cannot vouch for are never written into the bundle — and still do not
# abort the install.
exe_before="$(sha256_of "$(app_exe_path)")"
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  case "$name" in
    SHA2-256SUMS) printf '%s  ytdl_launch_macos_arm64\n' \
      "1111111111111111111111111111111111111111111111111111111111111111" > "$dest" ;;
    *) printf 'tampered\n' > "$dest" ;;
  esac
}
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "a launcher failing its checksum returns 0" "0" "$?"
check "and is never written into the bundle" "$exe_before" "$(sha256_of "$(app_exe_path)")"

# A run that installs no app must leave NO app. An empty YTDL.app is not "no
# app": Finder treats any directory named *.app as an application, shows it in
# Applications, and then refuses to open it — a control that cannot work, which
# is what ux-principles §5 forbids.
#
# The window is dated and certain, not hypothetical: install.sh is served from
# the branch while the assets come from releases/latest, so between merging a
# cycle and cutting its release there is no launcher to fetch. Every fresh
# install in that window took this path.
YTDL_APP_DIR="$BUNDLE_HOME/first-install"
download_optional() { return 1; }
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "a first install with no checksums returns 0" "0" "$?"
check "and leaves no empty YTDL.app behind" "1" \
  "$([ -e "$BUNDLE_HOME/first-install/YTDL.app" ] && echo 0 || echo 1)"
# ~/Applications itself IS created: it does not exist by default, and creating it
# is how the step learns whether it may write there at all.
check "but ~/Applications itself is created" "0" \
  "$([ -d "$BUNDLE_HOME/first-install" ] && echo 0 || echo 1)"

# The same, one step further along: the sums arrive but name no launcher, so the
# download 404s. Still no husk.
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  [ "$name" = "SHA2-256SUMS" ] || return 1
  printf '%s  ytdl_macos_arm64\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$dest"
}
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "a first install with no launcher asset leaves no empty YTDL.app" "1" \
  "$([ -e "$BUNDLE_HOME/first-install/YTDL.app" ] && echo 0 || echo 1)"

# And once the launcher IS available, the same directory does get its bundle —
# so the assertions above pin "not yet", not "never".
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  [ -f "$BUNDLE_HOME/release/$name" ] || return 1
  cp "$BUNDLE_HOME/release/$name" "$dest"
}
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "the same first install builds the bundle once the launcher exists" "0" \
  "$([ -x "$BUNDLE_HOME/first-install/YTDL.app/Contents/MacOS/YTDL" ] && echo 0 || echo 1)"

YTDL_APP_DIR="$BUNDLE_HOME/Applications"

# An unwritable parent is warned about, not aborted on.
unset -f download_optional
YTDL_APP_DIR="/proc/nonexistent-and-unwritable"
install_app_bundle >/dev/null 2>&1
check "an unwritable app directory warns and returns 0" "0" "$?"

# The closing message must not name an app that is not there. APP_INSTALLED is
# what the message reads, and only a run that actually wrote the bundle sets it.
check "a skipped bundle leaves APP_INSTALLED at 0" "0" "$APP_INSTALLED"

YTDL_APP_DIR="$BUNDLE_HOME/Applications"
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  [ -f "$BUNDLE_HOME/release/$name" ] || return 1
  cp "$BUNDLE_HOME/release/$name" "$dest"
}
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
install_app_bundle >/dev/null 2>&1
check "a successful bundle sets APP_INSTALLED to 1" "1" "$APP_INSTALLED"

# home_display is the ONE place that folds $HOME to ~, and it exists because the
# idiom it replaces silently did nothing: in ${p/#$HOME/~} bash tilde-expands the
# REPLACEMENT, so ~ becomes $HOME again and the substitution puts back exactly
# what it took out. Measured on this bash, then written as assertions.
check "a path under \$HOME is folded to ~" \
  "~/Applications" "$(home_display "$HOME/Applications")"
check "\$HOME itself folds to ~" "~" "$(home_display "$HOME")"
check "a path outside \$HOME is left alone" \
  "/opt/elsewhere" "$(home_display /opt/elsewhere)"
# The trap a plain prefix test falls into: a sibling whose name merely STARTS
# with $HOME is not inside it.
check "a sibling that only shares the prefix is left alone" \
  "${HOME}-extra/App" "$(home_display "${HOME}-extra/App")"

# The closing note is what the user reads last, and it must be true in both
# directions: silent when this run wrote no app, and naming the folder the app is
# ACTUALLY in when it did. It used to hardcode "your Applications folder" while
# the step's own ok line printed the real directory — with YTDL_APP_DIR set, the
# development sandbox, the two disagreed and one of them was wrong.
APP_INSTALLED=1
# Outside $HOME on purpose: that is the case the two messages used to disagree
# about, and inside $HOME it is indistinguishable from the default.
ALT_APP_DIR="$(mktemp -d)"
YTDL_APP_DIR="$ALT_APP_DIR/elsewhere"
note="$(app_closing_note)"
check "the closing note names the folder the app was written to" "0" \
  "$(case "$note" in *"$ALT_APP_DIR/elsewhere"*) echo 0 ;; *) echo 1 ;; esac)"
check "and does not claim it is in Applications" "0" \
  "$(case "$note" in *Applications*) echo 1 ;; *) echo 0 ;; esac)"
rmdir "$ALT_APP_DIR"

# And under $HOME it is written the way this installer writes every other path.
unset YTDL_APP_DIR
note="$(app_closing_note)"
check "a default install is named with ~, like every other path printed" \
  "0" "$(case "$note" in *"~/Applications"*) echo 0 ;; *) echo 1 ;; esac)"
YTDL_APP_DIR="$BUNDLE_HOME/Applications"

APP_INSTALLED=0
check "and a run that wrote no app says nothing about one" "" "$(app_closing_note)"
APP_INSTALLED=1

# The refusal must say what actually happened. app_launcher_verified answers with
# two distinct non-zero codes for exactly that reason, and the caller picks its
# wording from them: 1 is a real mismatch — the bytes are not the published ones,
# which is alarming and is said so — while 2 is "there is nothing to compare
# against". Reporting a mismatch that never happened is the same untruth
# ux-principles §5 forbids of every other surface.
ZEROS="0000000000000000000000000000000000000000000000000000000000000000"
probe="$BUNDLE_HOME/probe-launcher"
probe_sums="$BUNDLE_HOME/probe-SHA2-256SUMS"
printf 'launcher bytes\n' > "$probe"

printf '%s  ytdl_launch_macos_arm64\n' "$(sha256_of "$probe")" > "$probe_sums"
verdict=0; app_launcher_verified "$probe" "ytdl_launch_macos_arm64" "$probe_sums" || verdict=$?
check "a launcher matching its published sum verifies" "0" "$verdict"

printf '%s  ytdl_launch_macos_arm64\n' "$ZEROS" > "$probe_sums"
verdict=0; app_launcher_verified "$probe" "ytdl_launch_macos_arm64" "$probe_sums" || verdict=$?
check "bytes that differ from the published sum are code 1 (a mismatch)" "1" "$verdict"

printf '%s  ytdl_macos_arm64\n' "$ZEROS" > "$probe_sums"
verdict=0; app_launcher_verified "$probe" "ytdl_launch_macos_arm64" "$probe_sums" || verdict=$?
check "a sums file naming no launcher is code 2 (nothing to compare)" "2" "$verdict"

# Neither shasum nor openssl on the machine: nothing can be computed, so nothing
# can mismatch. macOS always ships shasum, so this path is unreachable there — it
# is also the one that used to report a mismatch that had not happened.
#
# Saved and restored by file: overriding a function does not stack, and every
# check below still needs the real one.
declare -f sha256_of > "$BUNDLE_HOME/real-sha256_of.bash"
sha256_of() { printf ''; }
printf '%s  ytdl_launch_macos_arm64\n' "$ZEROS" > "$probe_sums"
verdict=0; app_launcher_verified "$probe" "ytdl_launch_macos_arm64" "$probe_sums" || verdict=$?
check "no way to compute a sum at all is code 2, not a mismatch" "2" "$verdict"

# The wording follows the code, at the surface the finding was about: same
# effect — nothing unverified is ever written — different words.
YTDL_APP_DIR="$BUNDLE_HOME/wording"
download_optional() {
  local url="$1" dest="$2" name
  name="${url##*/}"
  case "$name" in
    SHA2-256SUMS) printf '%s  ytdl_launch_macos_arm64\n' "$ZEROS" > "$dest" ;;
    *)            printf 'tampered\n' > "$dest" ;;
  esac
}
rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
uncheckable_msg="$(install_app_bundle 2>&1)"
unset -f sha256_of; . "$BUNDLE_HOME/real-sha256_of.bash"
check "the real sha256_of is back for everything below" "0" \
  "$([ -n "$(sha256_of "$probe")" ] && echo 0 || echo 1)"
check "an unverifiable launcher is not called a mismatch" "0" \
  "$(case "$uncheckable_msg" in *"could not be checked"*) echo 0 ;; *) echo 1 ;; esac)"

rm -f "$TMPDIR_YTDL/ytdl-SHA2-256SUMS"
mismatch_msg="$(install_app_bundle 2>&1)"
check "a real mismatch still IS called a mismatch" "0" \
  "$(case "$mismatch_msg" in *"did not match its published checksum"*) echo 0 ;; *) echo 1 ;; esac)"
check "and neither wording leaves an app behind" "1" \
  "$([ -e "$BUNDLE_HOME/wording/YTDL.app" ] && echo 0 || echo 1)"
YTDL_APP_DIR="$BUNDLE_HOME/Applications"

HOME="$SAVED_HOME"; FORCE="$SAVED_FORCE"; INSTALL_DIR="$SAVED_INSTALL_DIR"
TMPDIR_YTDL="$SAVED_TMPDIR_YTDL"
unset -f download_optional
unset YTDL_APP_DIR
APP_INSTALLED=0
rm -rf "$BUNDLE_HOME"

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
