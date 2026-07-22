#!/usr/bin/env bash
#
# capture-goldens.sh — capture the Bash ytdl's exact yt-dlp argument vectors as
# golden references for the Go port's parity tests.
#
# Strategy (design-cycle1-core.md §6, golden-test-design.md §2):
#   * fake yt-dlp / ffmpeg / nohup shims are placed first on PATH; each shim
#     writes its argv with `printf '%s\0'` and exits 0, so nothing is downloaded
#     or re-executed;
#   * ytdl is run across the test matrix with a pinned HOME/output dir;
#   * the captured NUL-delimited argv is normalized (absolute output/home paths
#     and the runtime temp-file paths become placeholders) and written verbatim
#     as the golden byte stream — no newline conversion, because the pipeline
#     passes an empty-string argument that a newline format cannot represent.
#
# The golden files are byte-compared by internal/core/args_test.go.
#
#   ./tests/harness/capture-goldens.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
YTDL="$REPO_ROOT/ytdl"
TESTDATA="$REPO_ROOT/internal/core/testdata"

[ -f "$YTDL" ] || { echo "reference script not found: $YTDL" >&2; exit 1; }

# ──────────────────────────────────────────────────────────────────
#  Pinned, portable environment
# ──────────────────────────────────────────────────────────────────
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

TEST_HOME="$WORK/home"          # normalized to {{HOME}}
TEST_OUT="$WORK/out"            # normalized to {{OUTPUT_DIR}}
TEST_BIN="$WORK/bin"            # holds the shims, first on PATH
TEST_TMP="$WORK/tmp"            # ytdl's mktemp files land here
YTDLP_CAP="$WORK/cap-ytdlp"     # yt-dlp shim writes the captured argv here
NOHUP_CAP="$WORK/cap-nohup"     # nohup shim writes the re-exec argv here
mkdir -p "$TEST_HOME" "$TEST_OUT" "$TEST_BIN" "$TEST_TMP"

# ──────────────────────────────────────────────────────────────────
#  Shims
# ──────────────────────────────────────────────────────────────────
# yt-dlp / ffmpeg: record argv, exit 0. ffmpeg is only ever probed with
# `command -v` by ytdl (yt-dlp would call it, but yt-dlp is a shim), so its
# capture is unused — it exists so the dependency check passes.
cat > "$TEST_BIN/yt-dlp" <<'SHIM'
#!/usr/bin/env bash
for arg in "$@"; do printf '%s\0' "$arg"; done > "$YTDLP_CAP"
exit 0
SHIM
cat > "$TEST_BIN/ffmpeg" <<'SHIM'
#!/usr/bin/env bash
exit 0
SHIM
# nohup: ytdl's background mode runs `nohup "$0" <reexec-args> "$URL" & `. The
# shim records that argv (the re-exec vector, which is what we golden) and
# returns without launching anything. Written via a temp + atomic rename so the
# reader never sees a half-written file despite the caller backgrounding it.
cat > "$TEST_BIN/nohup" <<'SHIM'
#!/usr/bin/env bash
tmp="$(mktemp)"
for arg in "$@"; do printf '%s\0' "$arg"; done > "$tmp"
mv -f "$tmp" "$NOHUP_CAP"
exit 0
SHIM
chmod +x "$TEST_BIN/yt-dlp" "$TEST_BIN/ffmpeg" "$TEST_BIN/nohup"

# ──────────────────────────────────────────────────────────────────
#  Normalizer
# ──────────────────────────────────────────────────────────────────
# Reads a raw NUL-delimited capture and rewrites it into a portable golden:
#   * <out_dir>            -> {{OUTPUT_DIR}}   (substring, covers the -o template)
#   * <home_dir>           -> {{HOME}}         (covers the default $HOME/Music/ytdl)
#   * <self_path>          -> {{SELF}}         (the re-exec's argv[0], background)
#   * the path argument of each `--print-to-file KEY PATH` pair becomes
#     {{TITLEFILE}} (before_dl:) or {{SAVEDFILE}} (after_move:), keyed off the
#     template so the non-deterministic mktemp path is pinned.
NORMALIZER="$WORK/normalize.py"
cat > "$NORMALIZER" <<'PY'
import sys

cap, out_dir, home_dir, self_path = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
data = open(cap, "rb").read()
toks = data.split(b"\0")
if toks and toks[-1] == b"":     # drop the trailing empty left by the final \0
    toks = toks[:-1]

out_b, home_b, self_b = out_dir.encode(), home_dir.encode(), self_path.encode()

def sub(tok):
    tok = tok.replace(out_b, b"{{OUTPUT_DIR}}")
    tok = tok.replace(home_b, b"{{HOME}}")
    if self_b:
        tok = tok.replace(self_b, b"{{SELF}}")
    return tok

res, i = [], 0
while i < len(toks):
    t = toks[i]
    if t == b"--print-to-file" and i + 2 < len(toks) + 1 and i + 2 <= len(toks):
        # guard: need a key and a path following
        if i + 2 < len(toks):
            key, path = toks[i + 1], toks[i + 2]
            res.append(t)
            res.append(sub(key))
            if key.startswith(b"before_dl:"):
                res.append(b"{{TITLEFILE}}")
            elif key.startswith(b"after_move:"):
                res.append(b"{{SAVEDFILE}}")
            else:
                res.append(sub(path))
            i += 3
            continue
    res.append(sub(t))
    i += 1

sys.stdout.buffer.write(b"".join(t + b"\0" for t in res))
PY

# ──────────────────────────────────────────────────────────────────
#  Capture driver
# ──────────────────────────────────────────────────────────────────
export PATH="$TEST_BIN:$PATH"
export HOME="$TEST_HOME"
export TMPDIR="$TEST_TMP"
export YTDLP_CAP NOHUP_CAP
unset YTDL_OUT_DIR YTDL_REPO YTDL_BRANCH 2>/dev/null || true

COUNT=0

# capture NAME [ytdl args...] — records the yt-dlp argv for a foreground mode.
capture() {
  local name="$1"; shift
  : > "$YTDLP_CAP"
  rm -f "$TEST_OUT"/*.log 2>/dev/null || true
  bash "$YTDL" "$@" >/dev/null 2>&1 || true
  [ -s "$YTDLP_CAP" ] || { echo "no yt-dlp capture for '$name' (args: $*)" >&2; exit 1; }
  python3 "$NORMALIZER" "$YTDLP_CAP" "$TEST_OUT" "$TEST_HOME" "" > "$TESTDATA/$name.args"
  COUNT=$((COUNT + 1))
  printf '  captured %s\n' "$name.args"
}

# capture_bg NAME [ytdl args...] — records the background re-exec argv (nohup).
capture_bg() {
  local name="$1"; shift
  rm -f "$NOHUP_CAP"
  bash "$YTDL" "$@" >/dev/null 2>&1 || true
  # nohup runs backgrounded; wait (bounded, no sleep) for the atomic rename.
  local n=0
  while [ ! -e "$NOHUP_CAP" ] && [ "$n" -lt 2000000 ]; do n=$((n + 1)); done
  [ -s "$NOHUP_CAP" ] || { echo "no nohup capture for '$name' (args: $*)" >&2; exit 1; }
  python3 "$NORMALIZER" "$NOHUP_CAP" "$TEST_OUT" "$TEST_HOME" "$YTDL" > "$TESTDATA/$name.args"
  COUNT=$((COUNT + 1))
  printf '  captured %s\n' "$name.args"
}

mkdir -p "$TESTDATA"
rm -f "$TESTDATA"/*.args 2>/dev/null || true

SINGLE="https://youtu.be/dQw4w9WgXcQ"
PLAYLIST="https://www.youtube.com/playlist?list=PLtest0123456789"

printf 'Capturing golden references into %s\n\n' "${TESTDATA/#$REPO_ROOT\//}"

# ── dry-run (no base_args, no --no-simulate, --skip-download --print) ──
capture dryrun-mp3-single       -n            -o "$TEST_OUT" "$SINGLE"
capture dryrun-mp3-playlist     -n -p         -o "$TEST_OUT" "$PLAYLIST"
capture dryrun-flac-single      -n -f flac    -o "$TEST_OUT" "$SINGLE"
capture dryrun-flac-playlist    -n -f flac -p -o "$TEST_OUT" "$PLAYLIST"

# ── verbose (no --no-simulate, no print, -o template) ──
capture verbose-mp3-single      -v            -o "$TEST_OUT" "$SINGLE"
capture verbose-m4a-single      -v -f m4a     -o "$TEST_OUT" "$SINGLE"
capture verbose-flac-playlist   -v -f flac -p -o "$TEST_OUT" "$PLAYLIST"

# ── silent (base_args + print-to-file before_dl[simple]+after_move) ──
capture silent-mp3-single       -s            -o "$TEST_OUT" "$SINGLE"
capture silent-mp3-playlist     -s -p         -o "$TEST_OUT" "$PLAYLIST"
capture silent-opus-single      -s -f opus    -o "$TEST_OUT" "$SINGLE"
capture silent-wav-playlist     -s -f wav -p  -o "$TEST_OUT" "$PLAYLIST"

# ── default (base_args + print before_dl[full]+print-to-file after_move) ──
capture normal-mp3-single                     -o "$TEST_OUT" "$SINGLE"
capture normal-mp3-playlist     -p            -o "$TEST_OUT" "$PLAYLIST"
capture normal-flac-single      -f flac       -o "$TEST_OUT" "$SINGLE"
capture normal-m4a-playlist     -f m4a -p     -o "$TEST_OUT" "$PLAYLIST"

# ── output-dir shape: default $HOME/Music/ytdl (no -o, no env) ──
capture normal-mp3-defaultdir                 "$SINGLE"
capture silent-mp3-defaultdir   -s            "$SINGLE"

# ── trailing slash on -o must be stripped (parity, ytdl line 169) ──
capture silent-mp3-trailingslash -s           -o "$TEST_OUT/" "$SINGLE"

# ── background (re-exec argv via nohup; argv[0] normalized to {{SELF}}) ──
capture_bg background-mp3-single    -b            -o "$TEST_OUT" "$SINGLE"
capture_bg background-flac-single   -b -f flac    -o "$TEST_OUT" "$SINGLE"
capture_bg background-playlist      -b -p         -o "$TEST_OUT" "$PLAYLIST"

printf '\n%d golden files written.\n' "$COUNT"
