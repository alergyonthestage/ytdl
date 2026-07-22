# Golden Test Strategy for ytdl Bash→Go Port

**Status:** Stage-1 analysis input — partially superseded (see note)  
**Purpose:** Deterministically capture Bash ytdl's yt-dlp argument vectors as golden references, then validate Go port against them  
**Target:** Session 3, Cycle 1

> **Superseded by [design-cycle1-core.md](design-cycle1-core.md) (Gate B, 2026-07-22)**
> on two points: the **package layout** (§7/§8/§11 here sketch `package main` +
> `cmd/ytdl/testdata`; the design mandates a reusable `internal/` core with goldens in
> `internal/core/testdata/` — see [ADR-0004](decisions/0004-go-engine-package-layout.md)),
> and the **golden file format** (§2/§7 here use newline-delimited; the design uses
> NUL-delimited with byte comparison, to represent the empty-string argument
> unambiguously). The test **matrix** (§4) and the shim **capture** approach stand.

---

## 1. Existing Tests Review

### Current Test Harness

- **Location:** `/workspace/yt-download/tests/test-installer.sh` (134 lines)
- **Framework:** Custom Bash (not bats/shunit2), inline functions
- **Pattern:**
  - Loads target script as a library (`YTDL_INSTALLER_LIB=1` export + `. $INSTALLER`)
  - Mocks external commands (e.g., `sw_vers()`, `uname()`) before calling the function under test
  - Simple assertion utility: `check "description" "$expected" "$actual"` with pass/fail counters
  - No external dependencies beyond Bash
- **Scope:** Platform detection, ffmpeg URL selection, checksum verification
- **Style:** Concise, readable, inline—no separate test data files

### Reusable Elements for Golden Tests

1. **Mocking pattern:** Function-level stubbing is effective and lightweight
2. **No test framework dependency:** Custom harness avoids adding bats/shunit2; fits the project's constraint of minimal non-macOS-native tooling
3. **Pass/fail accounting:** Simple counter-based reporting works for golden tests
4. **Library loading:** Can load ytdl's arg-building functions in isolation via conditional block

---

## 2. Reference-Capture Design: Shim Strategy

### Goal

Deterministically execute the Bash `ytdl` script with fixed inputs, capture every yt-dlp invocation's complete argument vector exactly as it would appear in `argv`, and write the result as a machine-parseable golden reference.

### Mechanism: Fake yt-dlp / ffmpeg Shims

**How it works:**

1. Create a temporary test directory with a fake `yt-dlp` and fake `ffmpeg` on PATH (before `/usr/bin`)
2. Both shims:
   - Log `"$@"` (every argument) to a predictable file
   - Exit with code 0 (success) to allow the script to continue
   - Do NOT actually download or transcode (skip=download applies)
3. Run `ytdl` with fixed inputs (URL, flags, env)
4. Read the captured `argv` from the shim log
5. Normalize paths/dates and write as golden reference

### Argument Format (Unambiguous Serialization)

**Why not simple shell-style quoting?** The Bash script builds arrays like:

```bash
base_args=(
  -x --audio-format "$FORMAT" --audio-quality 0
  ...
  -o "$OUTPUT_DIR/$NAME_TEMPLATE.%(ext)s"
)
yt-dlp "${base_args[@]}" "$URL"
```

When invoked, `"${base_args[@]}"` expands to separate shell words. Capturing this reliably needs unambiguous per-argument separation.

**Format: One argument per line, with shell quoting.**

```
# Example captured argv for a simple dry-run
--no-warnings
--replace-in-metadata
title,track
\s*\[[^]]*\]

--print
--dry-run-templates
...
https://youtu.be/XXXXX
```

**Why line-separated with quoting:** 
- Handles empty strings, spaces, special chars, and regex patterns (the `STRIP_BRACKETS` / `STRIP_TAGS` regexes contain literal newlines and backslashes)
- Unambiguous: each line is exactly one argv element
- Parseable in shell: `readarray` or `mapfile` reads the file directly

**Shim implementation (pseudocode):**

```bash
#!/bin/bash
# fake-yt-dlp
CAPTURE_FILE="${YTDL_TEST_CAPTURE:?set YTDL_TEST_CAPTURE}"
for arg in "$@"; do
  printf '%s\0' "$arg"  # NUL-separated, then convert on read
done >> "$CAPTURE_FILE"
exit 0
```

**Why NUL-separated in the file?** Raw `printf '%s\n'` breaks if an arg contains a literal newline (rare but possible in metadata regexes). NUL separators are safer; on read, `mapfile -d ''` or equivalent handles them.

**On read-side, normalize to one-per-line for golden files:**

```bash
# Convert NUL to newlines for the golden file
tr '\0' '\n' < "$CAPTURE_FILE" > "$GOLDEN_FILE"
```

---

## 3. Non-Determinism Sources and Normalization Rules

For golden tests to be stable and portable, every source of variation must be identified, pinned, or normalized.

### Sources of Non-Determinism

| Source | Location in Script | Pinned or Normalized? | How |
|---|---|---|---|
| `$HOME` | Lines 31, 41, 104, 187 (paths + installer URL) | **Pin** | Set `HOME=/tmp/ytdl-test-home` in test env |
| `$YTDL_OUT_DIR` | Line 31 default, line 32 usage | **Pin** | Always pass `-o /tmp/ytdl-test-out` |
| `mktemp` result | Lines 110, 282, 313 (temp file paths) | **Ignore** | Shim doesn't receive temp paths; stdout/stderr are not tested |
| `date` in error logs | Line 300 (inside silent-mode error log) | **Ignore** | Error logs are written to files, not passed to yt-dlp |
| `RANDOM` or `$$` | Not used in ytdl | **N/A** | — |
| Hostname | Not used in ytdl | **N/A** | — |
| PWD | Line 12 (SCRIPT_DIR inference); yt-dlp -o path | **Pin** | Run test in fixed directory; use absolute `-o` |
| `YTDL_REPO`, `YTDL_BRANCH` | Lines 27–28 | **Pin** | Don't test `--update` path (skip); test --version separately |
| yt-dlp output-dir path | Line 264, 278, 320 (NAME_TEMPLATE expansion) | **Normalize** | The `-o` dir itself is in the argv; normalize by substitution post-capture |

### Normalization Rules (Post-Capture)

After capturing raw argv, apply these transformations to produce stable golden files:

1. **Absolute output dir → placeholder:**
   - Capture: `-o /tmp/ytdl-test-out/%(artist)s - %(track)s.%(ext)s`
   - Golden: `-o {{OUTPUT_DIR}}/%(artist)s - %(track)s.%(ext)s`
   - Rationale: Test runs may use different temp locations; golden files stay portable

2. **Home directory → placeholder:**
   - Capture: `$HOME/.config/ytdl/` (if ever added to argv)
   - Golden: `{{HOME}}/...`

3. **Whitespace in regex args:** preserved as-is
   - The `STRIP_BRACKETS` regex contains literal `\s` and `[^]]*`—these must be preserved exactly as yt-dlp receives them

4. **UTF-8 and special chars:** preserved as-is
   - Italian characters in comments (if any) are part of the test contract

5. **Order of args:** preserved exactly
   - The metadata pipeline (lines 206–212) has fragile ordering documented in architecture.md; golden tests enforce it

---

## 4. Test Matrix: Scenarios to Capture as Goldens

The goal is **comprehensive coverage of the code paths that affect yt-dlp argv construction**, not exhaustive URL testing (URLs themselves don't change the argv structure).

### By Execution Mode

1. **Dry-run (`-n, --dry-run`)**
   - Flags: `--no-warnings`, `--skip-download`, `--print`
   - Calls: one yt-dlp invocation (line 228–234)
   - Scenarios:
     - `-n "https://youtu.be/XXXXX"` (single video)
     - `-n -p "https://youtube.com/playlist?list=YYYY"` (playlist)

2. **Background (`-b, --background`)**
   - Flags: re-execs self with `-s -f FORMAT -o DIR`
   - Special: Does NOT call yt-dlp directly; forks nohup
   - Scenarios:
     - `-b "https://youtu.be/XXXXX"` (should fork, parent exits immediately)
     - `-b -f flac "https://youtu.be/XXXXX"`
   - **Note:** Background mode re-execs; golden is the **re-exec argv, not yt-dlp argv**
   - Secondary golden: the re-exec'd silent mode then captures yt-dlp argv

3. **Verbose (`-v, --verbose`)**
   - Flags: `-x --audio-format`, `--embed-metadata`, `--embed-thumbnail`
   - Calls: one yt-dlp invocation (line 258–264)
   - Scenarios:
     - `-v "https://youtu.be/XXXXX"` (single)
     - `-v -f m4a "https://youtu.be/XXXXX"` (non-default format)

4. **Silent (`-s, --silent`)**
   - Flags: `--quiet`, `--no-warnings`, `--no-progress`, `--print-to-file`
   - Calls: one yt-dlp invocation (line 287–291)
   - Scenarios:
     - `-s "https://youtu.be/XXXXX"` (single)
     - `-s -p "https://youtu.be/XXXXX"` (playlist)

5. **Normal (default)**
   - Flags: `--quiet`, `--no-warnings`, `--progress`, `--print`
   - Calls: one yt-dlp invocation (line 317–321)
   - Scenarios:
     - `"https://youtu.be/XXXXX"` (single)
     - `-p "https://youtu.be/XXXXX"` (playlist)

### By Audio Format

Test `-f` flag combinations affecting `--audio-format`:

| Format | Coverage | Notes |
|---|---|---|
| `mp3` (default) | Essential | No flag means implicit default |
| `flac` | Essential | Different container format |
| `m4a` | Essential | May require AtomicParsley (phase 2 issue C2) |
| `opus` | Good-to-have | Less common |
| `wav` | Good-to-have | Lossless, uncompressed |

Scenarios:
- `-f mp3` (explicit, same as default)
- `-f flac`
- `-f m4a`
- Default (no `-f`, should use `mp3`)

### By Playlist Flag

| Case | Coverage | Description |
|---|---|---|
| Single URL, no `-p` | Essential | `--no-playlist` (line 216) |
| Single URL, `-p` | Essential | `--yes-playlist -i` (line 218) |
| Playlist URL, no `-p` | Essential | Still `--no-playlist` (treats as single) |
| Playlist URL, `-p` | Essential | `--yes-playlist -i` (full playlist) |

### By Flag Combinations (Interaction Matrix)

Combinatorial explosion is real; focus on **interactions that change argv:**

| Combo | Example | Tests | Notes |
|---|---|---|---|
| `-n` alone | `-n URL` | dry-run, dry-run+playlist | Skip-download path |
| `-v` alone | `-v URL` | verbose, verbose+format | Full output path |
| `-s` alone | `-s URL` | silent, silent+playlist | Quiet path |
| no flags | `URL` | default, default+playlist | Normal path |
| `-s -p` | `-s -p URL` | silent+playlist | Both quiet + playlist |
| `-f` + mode | `-f flac -v URL` | format in each mode | Format change verified |
| `-o` + mode | `-o /path -s URL` | output in each mode | Output dir in all paths |

### Environment-Variable Scenarios

| Variable | Test | Notes |
|---|---|---|
| `YTDL_OUT_DIR` | Set to `/custom/out`, run `ytdl URL` | Should use custom dir, can be overridden by `-o` |
| `YTDL_REPO` | Set to `user/fork`, run `ytdl --version` | Should NOT affect yt-dlp argv (version check separate) |
| `YTDL_BRANCH` | Set to `dev`, run `ytdl --version` | Should NOT affect yt-dlp argv |
| `HOME` | Pinned to test value | Required for portability |

### Modes NOT Calling yt-dlp (Skip These)

- `-h`, `--help` → prints usage, exits
- `-V`, `--version` → prints version, exits
- `--update` → downloads installer, doesn't call yt-dlp
- Missing URL → prints error, exits

---

### Concrete Test Case Registry

**Naming convention:** `golden-{mode}-{format}-{playlist}-{extra}.args`

| File | Command | Expected yt-dlp Invocation | Notes |
|---|---|---|---|
| `golden-dryrun-mp3-single.args` | `-n "https://youtu.be/XXXXX"` | `--no-warnings --no-playlist --skip-download --print ... URL` | Dry-run single |
| `golden-dryrun-flac-playlist.args` | `-n -p -f flac "https://youtube.com/playlist?list=YYYY"` | `--no-warnings --yes-playlist -i --skip-download --print ... URL` | Dry-run playlist, format |
| `golden-verbose-mp3-single.args` | `-v "https://youtu.be/XXXXX"` | `-x --audio-format mp3 ... URL` | Verbose single |
| `golden-verbose-m4a-playlist.args` | `-v -f m4a -p "https://youtube.com/playlist?list=YYYY"` | `-x --audio-format m4a ... --yes-playlist ... URL` | Verbose m4a playlist |
| `golden-silent-mp3-single.args` | `-s "https://youtu.be/XXXXX"` | `--quiet --no-warnings --no-progress --print-to-file ... URL` | Silent single |
| `golden-silent-opus-playlist.args` | `-s -f opus -p "https://youtube.com/playlist?list=YYYY"` | `--quiet --audio-format opus --yes-playlist ... URL` | Silent opus playlist |
| `golden-normal-mp3-single.args` | `"https://youtu.be/XXXXX"` | `--quiet --no-warnings --progress --print ... URL` | Default single |
| `golden-normal-flac-playlist.args` | `-f flac -p "https://youtube.com/playlist?list=YYYY"` | `--audio-format flac --yes-playlist ... --progress ... URL` | Default flac playlist |
| `golden-background-mp3-single.args` | `-b "https://youtu.be/XXXXX"` | (nohup re-exec) `./ytdl -s -f mp3 -o /tmp/... URL` | Background mode (re-exec argv) |
| `golden-env-ytdl-out-dir.args` | `YTDL_OUT_DIR=/custom ytdl "https://youtu.be/XXXXX"` | `... -o /custom/... URL` | Env var override |
| `golden-flag-override-env.args` | `YTDL_OUT_DIR=/custom ytdl -o /override "https://youtu.be/XXXXX"` | `... -o /override/... URL` | Flag overrides env |

**Minimum viable set (13 tests):**

1. Dry-run single
2. Dry-run playlist
3. Verbose single
4. Verbose non-default format (flac)
5. Silent single
6. Silent playlist
7. Normal single
8. Normal playlist
9. Normal non-default format (m4a)
10. Background (re-exec argv)
11. Flag -o override output
12. Env YTDL_OUT_DIR
13. Flag precedence (flag > env)

**Expanded set for robustness (20–25 tests):** Add combinations of format + mode, edge cases like empty YTDL_OUT_DIR, etc.

---

## 5. Environment Feasibility

### Available in This Container

✅ **Bash 5.2** — fully sufficient  
✅ **Python 3.11** — can run fake shim (bash is native, but Python can also write shims if desired)  
✅ **Standard tools** — `git`, `jq`, `curl`, `xxd`, `printf`, `tr`  
✅ **Temp directory** — `/tmp` available  

### NOT Available (Not Needed for Shim Capture)

❌ Real `yt-dlp` — not required; shim is a fake  
❌ Real `ffmpeg` — not required; shim is a fake  
❌ `bats` or `shunit2` — not available, not needed; custom Bash harness works  
❌ Go — not available in this container, but Go's `testing` package will run goldens in session 3 on the Go side  

### Feasibility: ✅ FULLY FEASIBLE

The shim approach requires **only Bash and the ability to capture subprocess arguments**, both present here. No real binaries needed. The test harness can:

1. Create a temp directory
2. Write fake `yt-dlp` and `ffmpeg` as Bash scripts
3. Prepend temp directory to PATH
4. Source ytdl and invoke its functions
5. Capture output
6. Compare against goldens

---

## 6. Shim Implementation (Executable Pseudocode)

### Fake yt-dlp Shim

```bash
#!/bin/bash
# fake-yt-dlp — logs all arguments for golden test capture
CAPTURE_FILE="${YTDL_TEST_CAPTURE:?YTDL_TEST_CAPTURE not set}"
for arg in "$@"; do
  printf '%s\0' "$arg"
done >> "$CAPTURE_FILE"
exit 0
```

### Fake ffmpeg Shim

```bash
#!/bin/bash
# fake-ffmpeg — same: logs and exits
CAPTURE_FILE="${YTDL_TEST_FFMPEG_CAPTURE:?YTDL_TEST_FFMPEG_CAPTURE not set}"
for arg in "$@"; do
  printf '%s\0' "$arg"
done >> "$CAPTURE_FILE"
exit 0
```

### Test Harness Pseudocode (Bash)

```bash
#!/bin/bash
set -euo pipefail

TEST_HOME="/tmp/ytdl-test-home-$$"
TEST_OUT="/tmp/ytdl-test-out-$$"
TEST_BIN="/tmp/ytdl-test-bin-$$"
CAPTURE_YT_DLP="/tmp/ytdl-capture-ytdlp-$$"
CAPTURE_FFMPEG="/tmp/ytdl-capture-ffmpeg-$$"

mkdir -p "$TEST_HOME" "$TEST_OUT" "$TEST_BIN"
trap 'rm -rf "$TEST_HOME" "$TEST_OUT" "$TEST_BIN" "$CAPTURE_YT_DLP" "$CAPTURE_FFMPEG"' EXIT

# Write fake shims
cat > "$TEST_BIN/yt-dlp" << 'SHIM'
#!/bin/bash
CAPTURE_FILE="${YTDL_TEST_CAPTURE:?}" 
for arg in "$@"; do printf '%s\0' "$arg"; done >> "$CAPTURE_FILE"
exit 0
SHIM
chmod +x "$TEST_BIN/yt-dlp"

cat > "$TEST_BIN/ffmpeg" << 'SHIM'
#!/bin/bash
CAPTURE_FILE="${YTDL_TEST_FFMPEG_CAPTURE:?}" 
for arg in "$@"; do printf '%s\0' "$arg"; done >> "$CAPTURE_FILE"
exit 0
SHIM
chmod +x "$TEST_BIN/ffmpeg"

# Set environment
export PATH="$TEST_BIN:$PATH"
export HOME="$TEST_HOME"
export YTDL_TEST_CAPTURE="$CAPTURE_YT_DLP"
export YTDL_TEST_FFMPEG_CAPTURE="$CAPTURE_FFMPEG"

# Run ytdl with fixed inputs
bash /workspace/yt-download/ytdl -n -o "$TEST_OUT" "https://youtu.be/test" 2>/dev/null || true

# Convert capture (NUL-separated) to golden (newline-separated)
if [ -f "$CAPTURE_YT_DLP" ]; then
  tr '\0' '\n' < "$CAPTURE_YT_DLP" > "$TEST_OUT/golden.args"
  # Normalize paths
  sed -i.bak "s|$TEST_OUT|{{OUTPUT_DIR}}|g" "$TEST_OUT/golden.args"
  sed -i.bak "s|$TEST_HOME|{{HOME}}|g" "$TEST_OUT/golden.args"
  cat "$TEST_OUT/golden.args"
fi
```

---

## 7. Go-Side Comparison Design

### Goal

The Go port's argument-builder function produces `[]string` with the same yt-dlp argv as the Bash reference. Golden tests validate this without actually invoking yt-dlp.

### Architecture

```
ytdl (Go binary)
├── package ytdl
│   └── func BuildArgs(opts *Options) []string  // Pure function, no exec
├── package config
│   └── type Options struct { ... }
└── package testdata/
    ├── golden-dryrun-mp3-single.args
    ├── golden-verbose-flac-playlist.args
    └── ... (23 more goldens)

_test.go files:
├── ytdl_test.go
│   └── TestBuildArgsAgainstGoldens(t *testing.T)
```

### Testing Approach in Go's `testing` Package

**Test structure:**

```go
// ytdl_test.go
func TestBuildArgsAgainstGoldens(t *testing.T) {
	testCases := []struct {
		name    string
		opts    *ytdl.Options
		golden  string  // filename in testdata/
	}{
		{
			name: "dryrun_mp3_single",
			opts: &ytdl.Options{
				DryRun: true,
				Format: "mp3",
				URL:    "https://youtu.be/test123",
				OutputDir: "{{OUTPUT_DIR}}",  // normalized
			},
			golden: "golden-dryrun-mp3-single.args",
		},
		// ... 24 more cases
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ytdl.BuildArgs(tc.opts)
			want := readGolden(t, "testdata/" + tc.golden)
			
			if !slices.Equal(got, want) {
				t.Errorf("argv mismatch\ngot:  %v\nwant: %v", got, want)
			}
		})
	}
}

func readGolden(t *testing.T, path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return lines
}
```

**With `-update` support (for regenerating goldens):**

```go
var updateGoldens = flag.Bool("update", false, "update golden files")

func TestBuildArgsAgainstGoldens(t *testing.T) {
	if *updateGoldens {
		t.Log("Regenerating goldens (run: go test ./... -update)")
		// Re-run Bash harness, capture new goldens, write testdata/
	}
	// ... compare as above
}
```

### Golden File Format in testdata/

```
testdata/
├── golden-dryrun-mp3-single.args
├── golden-verbose-flac-playlist.args
├── golden-silent-opus-playlist.args
├── golden-normal-mp3-single.args
├── golden-background-mp3-single.args
├── ...
```

Each `.args` file contains one argument per line, NUL-separators converted to newlines for readability:

**Example: `golden-dryrun-mp3-single.args`**

```
--no-warnings
--replace-in-metadata
title,track
\s*\[[^]]*\]

--replace-in-metadata
title,track
\s*\((?i:original mix|original|extended mix|extended|radio edit|radio version|free download|free dl|official (video|audio)|lyric video|visualizer|hd|hq|audio)\)

--parse-metadata
title:%(xartist)s - %(xtrack)s
--parse-metadata
%(artist,creator,xartist,uploader)s:%(meta_artist)s
--parse-metadata
%(track,xtrack,title)s:%(meta_title)s
--no-playlist
--skip-download
--print
%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.mp3
https://youtu.be/test123
```

### Comparison Logic

**Normalization on Go side before comparison:**

```go
func normalizeArgv(argv []string, testOutputDir string) []string {
	normalized := make([]string, len(argv))
	for i, arg := range argv {
		// Replace test-specific paths with placeholders
		normalized[i] = arg
		normalized[i] = strings.ReplaceAll(normalized[i], testOutputDir, "{{OUTPUT_DIR}}")
		normalized[i] = strings.ReplaceAll(normalized[i], os.Getenv("HOME"), "{{HOME}}")
	}
	return normalized
}
```

**Why this normalization?** The Bash capture process writes an absolute path (`/tmp/ytdl-test-out-12345`) into argv. The Go test rebuilds the same absolute paths. Before comparing, both are normalized to `{{OUTPUT_DIR}}`.

### Handling the `-update` Regeneration Workflow

Workflow for session 3 implementation:

1. **Initial run (before Go port):** Run Bash harness (in `tests/` or `tests/harness/`), capture goldens → `cmd/ytdl/testdata/*.args`
2. **Go port write:** Go's `BuildArgs()` outputs `[]string`, test reads goldens
3. **Iteration:**
   - Go test fails because implementation diverges from Bash
   - Fix Go code
   - Run test again
4. **Regenerate (if argv structure changes intentionally):**
   - Update Bash script
   - Re-run Bash harness: `bash tests/harness/capture-goldens.sh`
   - Commit new goldens alongside Go changes

---

## 8. Implementation Roadmap for Session 3

### Stage 1: Capture Golden References (Bash Harness)

**Deliverable:** `tests/harness/capture-goldens.sh` + 20–25 golden files in `cmd/ytdl/testdata/*.args`

**Steps:**
1. Write fake `yt-dlp` and `ffmpeg` shims (Bash)
2. Iterate over test matrix (13 scenarios minimum)
3. For each: run ytdl, capture argv, normalize, write golden
4. Commit goldens alongside Go implementation

**Estimated lines of code:** 200–300 for harness + shim setup

### Stage 2: Go Port's BuildArgs() Function

**Deliverable:** `cmd/ytdl/cli.go` + `cmd/ytdl/builder.go` (or similar) with `BuildArgs(opts *Options) []string`

**Structure:**
```go
package main

type Options struct {
	URL       string
	Format    string
	OutputDir string
	DryRun    bool
	Verbose   bool
	Silent    bool
	Playlist  bool
	// ...
}

func BuildArgs(opts *Options) []string {
	args := []string{}
	args = append(args, metadataArgs()...)
	args = append(args, playlistArgs(opts.Playlist)...)
	// ... more arg construction
	args = append(args, opts.URL)
	return args
}
```

**Key:** `BuildArgs()` is a **pure function**, no exec, no I/O—just builds and returns the argv slice.

### Stage 3: Golden-File Test in Go

**Deliverable:** `cmd/ytdl/builder_test.go` with `TestBuildArgsAgainstGoldens(t *testing.T)`

**Implementation:**
- Define test matrix as table-driven tests
- For each case: call `BuildArgs()`, compare result to golden file
- Use `slices.Equal()` for comparison (Go 1.21+)
- Optional: `-update` flag for regeneration

### Stage 4: Validation

**Validation checklist:**
- ✅ All 20–25 goldens pass (argv match)
- ✅ Metadata pipeline ordering preserved (xartist/xtrack helper fields in place)
- ✅ Regex patterns (STRIP_BRACKETS, STRIP_TAGS) are exact
- ✅ No extraneous args introduced; no args omitted

---

## 9. Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Regex patterns contain special chars that don't serialize cleanly | Golden captures corrupt or ambiguous | Use NUL-separated format; test with examples containing `\n`, `'`, `"` early |
| Bash array expansion differs from Go slice construction | Argv order or content mismatches | Test a single complex case end-to-end before running full matrix |
| Non-determinism source discovered late (e.g., locale affecting sort order) | Golden files differ across machines | Review ytdl for locale dependencies; set `LC_ALL=C` in harness if found |
| `-b` (background) is hard to test (forks) | Can't capture re-exec argv directly | Capture the `nohup` command line itself (parent's argv), or run the re-exec'd process in foreground under test |
| Go's `BuildArgs()` requires significant refactoring of arg construction logic | Estimate balloon | Accept that this is the session's main work; design module interfaces early (gate B) |

---

## 10. Acceptance Criteria

A complete golden-test design is ready for implementation when:

1. ✅ Bash harness written and captures argv correctly for one test case
2. ✅ All 20+ golden files written and committed to `testdata/`
3. ✅ Go `BuildArgs()` function exists and matches structure defined above
4. ✅ Go golden-file test runs and reports mismatches clearly
5. ✅ `go test ./cmd/ytdl/... -v` shows all golden tests green
6. ✅ Metadata pipeline (xartist/xtrack precedence) verified in goldens
7. ✅ Normalization rules applied consistently (paths, case-sensitivity, regex escaping)

---

## 11. Appendix: File Structure

Post-implementation file layout:

```
/workspace/yt-download/
├── ytdl                           # Bash reference (unchanged)
├── tests/
│   ├── test-installer.sh          # Existing installer tests
│   └── harness/
│       ├── capture-goldens.sh     # NEW: Bash harness to generate goldens
│       └── shims/
│           ├── fake-yt-dlp        # NEW: Fake shim
│           └── fake-ffmpeg        # NEW: Fake shim
│
├── cmd/ytdl/                      # NEW: Go module
│   ├── main.go
│   ├── cli.go                     # Flag parsing
│   ├── builder.go                 # BuildArgs() + helper functions
│   ├── builder_test.go            # Golden-file tests
│   └── testdata/
│       ├── golden-dryrun-mp3-single.args
│       ├── golden-verbose-flac-playlist.args
│       ├── golden-silent-opus-playlist.args
│       ├── ...
│       └── (20+ golden files)
│
├── go.mod                         # NEW: Go module
└── docs/
    ├── roadmap.md                 # Phase 3 status
    ├── decisions/
    │   └── 0003-engine-language-go.md
    └── architecture.md
```

---

**Document Status:** Ready for gate B approval (design review) before session 3 implementation.

