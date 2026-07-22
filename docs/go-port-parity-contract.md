# ytdl Parity Contract: Bash → Go Port

**Document Status:** Specification for exact behavioral equivalence  
**Reference Script:** `/workspace/yt-download/ytdl` (346 lines)  
**Script Commit:** 46421ba  
**Version:** 1.0.0  

---

## Overview

This document specifies the EXACT behavior that a Go port must reproduce. The Bash script is a thin wrapper around `yt-dlp` that:
1. Parses CLI arguments and environment variables
2. Builds metadata normalization arguments (`meta_args`)
3. Dispatches to one of five execution modes
4. Constructs and invokes `yt-dlp` with precise argument vectors
5. Handles errors and reports success/failure

Precision is critical: a Go developer must be able to construct identical `yt-dlp` command lines and achieve identical file names, tags, and error handling.

---

## 1. CONSTANTS & DEFAULTS

### Version
- `YTDL_VERSION = "1.0.0"` (line 12)

### Output Directory
- **Default:** `$HOME/Music/ytdl` (line 31)
- **Override precedence:** `-o` flag > `$YTDL_OUT_DIR` env var > hardcoded default
- **Normalization:** trailing `/` stripped before use (line 169: `OUTPUT_DIR="${OUTPUT_DIR%/}"`)

### Audio Format
- **Default:** `"mp3"` (line 32)
- **Supported formats (documented in help):** `mp3|flac|m4a|opus|wav`
- **Override:** `-f/--format` flag only (no env var)

### Repository (for `--update`)
- `YTDL_REPO = "${YTDL_REPO:-alergyonthestage/ytdl}"` (line 27)
- `YTDL_BRANCH = "${YTDL_BRANCH:-main}"` (line 28)
- Used to construct installer URL: `https://raw.githubusercontent.com/$YTDL_REPO/$YTDL_BRANCH/install.sh`

### Regex Constants (Python `re` syntax)

**STRIP_BRACKETS** (line 37):
```
\s*\[[^]]*\]
```
Matches: `[BAS012]`, `[Premiere]`, `[Free Download]`, etc. — zero or more spaces followed by any `[...]` block.

**STRIP_TAGS** (line 41):
```
\s*\((?i:original mix|original|extended mix|extended|radio edit|radio version|free download|free dl|official (video|audio)|lyric video|visualizer|hd|hq|audio)\)
```
Case-insensitive. Matches: `(Original Mix)`, `(Original)`, `(Extended Mix)`, `(Extended)`, `(Radio Edit)`, `(Radio Version)`, `(Free Download)`, `(Free DL)`, `(Official Video)`, `(Official Audio)`, `(Lyric Video)`, `(Visualizer)`, `(HD)`, `(HQ)`, `(Audio)`.  
**Does NOT match:** `(Remix)`, `(feat. ...)`, `(Qualcuno Remix)` — these are preserved intentionally (architecture.md, line 115).

---

## 2. COMMAND-LINE INTERFACE

### Argument Parsing (lines 146–166)

Parsing is single-pass, left-to-right, with explicit precedence:

| Flag | Alias | Argument | Behavior | Line |
|------|-------|----------|----------|------|
| `-o` | `--output` | `DIR` | Set `OUTPUT_DIR` to DIR | 148 |
| `-f` | `--format` | `FMT` | Set `FORMAT` to FMT | 149 |
| `-p` | `--playlist` | (none) | Set `NO_PLAYLIST=0` (enable playlist mode) | 150 |
| `-n` | `--dry-run` | (none) | Set `DRY_RUN=1` | 151 |
| `-s` | `--silent` | (none) | Set `SILENT=1` | 152 |
| `-b` | `--background` | (none) | Set `BACKGROUND=1` | 153 |
| `-v` | `--verbose` | (none) | Set `VERBOSE=1` | 154 |
| `-h` | `--help` | (none) | Print usage and `exit 0` | 155 |
| `-V` | `--version` | (none) | Show version and `exit 0` | 156 |
| (none) | `--update` | (none) | Run `do_update()` and `exit $?` | 157 |
| (none) | `--` | remainder | Remaining args are treated as URL (line 158) | 158 |
| (none) | `-*` | (any) | Unknown flag: error message and `exit 1` | 159 |
| (none) | (positional) | (none) | Treated as URL | 160 |

### Parsing Details

1. **Missing argument error:** If `-o` or `-f` flag has no argument, the expansion `${2:?message}` causes a bash error (lines 148–149). Go must implement identical validation.

2. **URL requirement:** If no URL is provided by end of parsing, print error to stderr and `exit 1` (lines 164–166).

3. **Mutual exclusivity:** The modes `-n`, `-b`, `-v`, `-s` are **NOT enforced** by the parser — they are checked at execution time. A user can pass multiple; the last one wins (or earlier modes run first if not mutually exclusive at runtime). However, the execution flow is mutually exclusive: only ONE mode runs.

### Help Text (lines 46–96)
```
ytdl — scarica musica da YouTube / YT Music con yt-dlp, naming e tag puliti.

USO
  ytdl [opzioni] URL

OPZIONI
  -o, --output DIR    Cartella di destinazione   (default: $YTDL_OUT_DIR o ~/Music/ytdl)
  -p, --playlist      Scarica l'INTERA playlist        (default: solo la traccia)
  -f, --format FMT    Formato audio: mp3|flac|m4a|opus|wav   (default: mp3)
  -n, --dry-run       Mostra i nomi file risultanti SENZA scaricare
  -s, --silent        Nessun output (per lanciarlo in background con &)
  -b, --background    Esegui in background e torna subito (implica -s)
  -v, --verbose       Mostra tutto l'output di yt-dlp (per debug)
  -h, --help          Questo messaggio
  -V, --version       Mostra la versione di ytdl e yt-dlp
      --update        Aggiorna ytdl e yt-dlp all'ultima versione

AGGIORNAMENTI
  yt-dlp smette di funzionare quando YouTube cambia qualcosa: succede ogni
  pochi mesi. Se i download iniziano a fallire, la prima cosa da provare è:
       ytdl --update

ERRORI IN -s / -b
  Senza output non vedi i fallimenti: se un download fallisce viene scritto
  "<titolo>.log" (col dettaglio) nella cartella di output, al posto del file
  audio. Controllo a posteriori:  ls *.log  nella cartella.

NB: METTI SEMPRE L'URL TRA VIRGOLETTE. Contiene & che altrimenti la shell
    interpreta come "esegui in background" (es. ...&list=...&index=2).

TIP: per fissare la cartella di output per la sessione corrente:
       export YTDL_OUT_DIR="/percorso/output"
     (il flag -o ha comunque la precedenza)

ESEMPI
  ytdl "https://youtu.be/XXXX"                        # singola traccia
  ytdl -p "https://youtube.com/playlist?list=YYYY"    # playlist intera
  ytdl -f flac -o ~/Desktop "https://youtu.be/XXXX"   # FLAC sul Desktop
  ytdl -n "https://youtu.be/XXXX"                     # anteprima dei nomi
  ytdl -b "https://youtu.be/XXXX"                     # in background, torna subito

COSA FA AL TITOLO
  • Se ci sono metadati strutturati (YT Music / Topic) usa SEMPRE quelli:
    artist e track nativi hanno la precedenza sul titolo del video.
  • Altrimenti ricava "Artista - Traccia" splittando il titolo su " - ".
  • In entrambi i casi rimuove [..] e (Original Mix)/(Extended)/ecc.,
    mantenendo le info utili tipo (Qualcuno Remix) e (feat. …).
```

---

## 3. DEPENDENCY CHECKING

Lines 187–188. Checked before any download is attempted.

```bash
command -v yt-dlp >/dev/null 2>&1 || missing_dep "yt-dlp"
command -v ffmpeg >/dev/null 2>&1 || missing_dep "ffmpeg (serve per estrarre l'audio e incorporare la copertina)"
```

**Missing dependency behavior (lines 176–185):**
- Stderr prints:
  ```
  ✗ <tool> non trovato.

    Le dipendenze di ytdl sembrano mancanti o incomplete.
    Per reinstallarle:

        ytdl --update

  ```
- `exit 1`

**Note:** The Go port must also check for these tools. Behavior must be identical: if either is missing, abort with the same error message.

---

## 4. METADATA PIPELINE

This is the core feature — the precise ordering and transformation that makes metadata correct.

### Design Principle

**Structured metadata always wins over title parsing.** The pipeline:
1. Cleans title/track fields (remove `[...]` and unwanted `(...)`)
2. Splits title into **helper fields** `xartist` and `xtrack` (NOT overwriting native `artist`/`track`)
3. Constructs fallback chains with native fields having priority

### Filename Template (line 196)

```
%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s
```

This uses yt-dlp's **first-present** semantics: `%(a,b,c)s` expands to the first non-empty field. In order:
- **Artist slot:** `artist` → `creator` → `xartist` → `uploader`
- **Track slot:** `track` → `xtrack` → `title`

### meta_args Array (lines 206–212)

```bash
meta_args=(
  --replace-in-metadata "title,track" "$STRIP_BRACKETS" ""
  --replace-in-metadata "title,track" "$STRIP_TAGS"     ""
  --parse-metadata "title:%(xartist)s - %(xtrack)s"
  --parse-metadata "%(artist,creator,xartist,uploader)s:%(meta_artist)s"
  --parse-metadata "%(track,xtrack,title)s:%(meta_title)s"
)
```

**Ordering is CRITICAL** (architecture.md, lines 109–119). Each step:

1. **Line 207:** `--replace-in-metadata "title,track" "$STRIP_BRACKETS" ""`
   - Applies regex `\s*\[[^]]*\]` to BOTH `title` and `track` fields
   - Replaces matches with empty string
   - In-place transformation

2. **Line 208:** `--replace-in-metadata "title,track" "$STRIP_TAGS" ""`
   - Applies regex `\s*\((?i:original mix|...)\)` to BOTH `title` and `track` fields
   - Replaces matches with empty string
   - Now both fields are cleaned

3. **Line 209:** `--parse-metadata "title:%(xartist)s - %(xtrack)s"`
   - **KEY STEP:** Splits the cleaned `title` field on ` - ` and assigns:
     - Left side (before ` - `) → helper field `xartist`
     - Right side (after ` - `) → helper field `xtrack`
   - **DOES NOT touch** the native `artist` or `track` fields
   - If title contains no ` - `, splits as: `xartist=""`, `xtrack=<title>`
   - This is why structured metadata (artist/track from YT Music) never gets overwritten

4. **Line 210:** `--parse-metadata "%(artist,creator,xartist,uploader)s:%(meta_artist)s"`
   - Constructs `meta_artist` field using the same fallback chain as the filename
   - Used for ID3 tag writing (line 260: `--embed-metadata`)

5. **Line 211:** `--parse-metadata "%(track,xtrack,title)s:%(meta_title)s"`
   - Constructs `meta_title` field using the same fallback chain as the filename
   - Used for ID3 tag writing (line 260: `--embed-metadata`)

**Design rationale (architecture.md, line 115):** A video titled `"Label - Artist — Track"` from a Label account would otherwise cause `artist` field to be overwritten with `"Label"`, defeating YT Music's structured metadata. The helper-field indirection preserves this invariant.

### Playlist Args (lines 215–219)

```bash
if [[ "$NO_PLAYLIST" -eq 1 ]]; then
  playlist_args=(--no-playlist)
else
  playlist_args=(--yes-playlist -i)   # -i: non fermarti al primo errore
fi
```

- **Default (single track):** `NO_PLAYLIST=1` (line 139) → `playlist_args=(--no-playlist)`
- **With `-p/--playlist` flag:** `NO_PLAYLIST=0` → `playlist_args=(--yes-playlist -i)`
- The `-i` flag tells yt-dlp to skip errors and continue with next item in playlist

---

## 5. EXECUTION MODES

After dependency check and building `meta_args`, the script branches to exactly one mode. Each mode constructs and invokes yt-dlp with different flags and output handling.

### 5.1 DRY-RUN Mode (`-n/--dry-run`)

**Condition:** `DRY_RUN=1` (line 224)

**Behavior:** Preview filenames without downloading.

**yt-dlp invocation (lines 228–234):**
```bash
yt-dlp \
  --no-warnings \
  "${meta_args[@]}" \
  "${playlist_args[@]}" \
  --skip-download \
  --print "$NAME_TEMPLATE.$FORMAT" \
  "$URL"
```

**Argument vector (expanded):**
1. `yt-dlp`
2. `--no-warnings`
3. `--replace-in-metadata` `"title,track"` `"\s*\[[^]]*\]"` `""`
4. `--replace-in-metadata` `"title,track"` `"\s*\((?i:...)\)"` `""`
5. `--parse-metadata` `"title:%(xartist)s - %(xtrack)s"`
6. `--parse-metadata` `"%(artist,creator,xartist,uploader)s:%(meta_artist)s"`
7. `--parse-metadata` `"%(track,xtrack,title)s:%(meta_title)s"`
8. `--no-playlist` OR `--yes-playlist` + `-i` (depending on `-p`)
9. `--skip-download`
10. `--print` `"$NAME_TEMPLATE.$FORMAT"` (expands to `"%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.mp3"` or other format)
11. `"$URL"`

**Output:** Prints one line per matched video: the computed filename (with extension, NO path).

**Exit code:** 0 on success, non-zero on yt-dlp error.

**After exit:** Script terminates with `exit 0` (line 235).

### 5.2 BACKGROUND Mode (`-b/--background`)

**Condition:** `BACKGROUND=1` (line 245)

**Behavior:** Re-execute the script with `-s` (silent) via `nohup`, disconnect from terminal, return immediately.

**yt-dlp is NOT invoked by this path** — the script re-execs itself.

**Re-execution (lines 246–248):**
```bash
reexec=(-s -f "$FORMAT" -o "$OUTPUT_DIR")
[[ "$NO_PLAYLIST" -eq 0 ]] && reexec+=(-p)
nohup "$0" "${reexec[@]}" "$URL" >/dev/null 2>&1 &
```

**Reconstructed command line:**
```
nohup /path/to/ytdl -s -f <format> -o <output_dir> [-p] <URL> >/dev/null 2>&1 &
```

- **`-s` always added** (silent mode)
- **`-f <FORMAT>`** always added (preserves format)
- **`-o <OUTPUT_DIR>`** always added (preserves output dir)
- **`-p` added only if** `NO_PLAYLIST=0` (i.e., playlist mode was enabled)
- **`URL` always added** (the target URL)
- Redirects stdout and stderr to `/dev/null`
- Runs in background (`&`)

**Output to user (lines 249–251):**
```
▸ Avviato in background (PID $!).
  Audio → $OUTPUT_DIR
  Se un download fallisce, troverai <titolo>.log nella stessa cartella.
```

**Exit code:** Always 0 (line 252).

**Note:** The background process runs in SILENT mode, which means it will write `.log` files on failure (see section 5.4).

### 5.3 VERBOSE Mode (`-v/--verbose`)

**Condition:** `VERBOSE=1` (line 256)

**Behavior:** Full download with all yt-dlp output visible.

**yt-dlp invocation (lines 258–264):**
```bash
yt-dlp \
  -x --audio-format "$FORMAT" --audio-quality 0 \
  --embed-metadata --embed-thumbnail \
  "${meta_args[@]}" \
  "${playlist_args[@]}" \
  -o "$OUTPUT_DIR/$NAME_TEMPLATE.%(ext)s" \
  "$URL"
```

**Argument vector (expanded):**
1. `yt-dlp`
2. `-x` (extract audio only)
3. `--audio-format` `<format>` (e.g., `mp3`)
4. `--audio-quality` `0` (highest quality)
5. `--embed-metadata`
6. `--embed-thumbnail`
7. `--replace-in-metadata` `"title,track"` `"\s*\[[^]]*\]"` `""`
8. `--replace-in-metadata` `"title,track"` `"\s*\((?i:...)\)"` `""`
9. `--parse-metadata` `"title:%(xartist)s - %(xtrack)s"`
10. `--parse-metadata` `"%(artist,creator,xartist,uploader)s:%(meta_artist)s"`
11. `--parse-metadata` `"%(track,xtrack,title)s:%(meta_title)s"`
12. `--no-playlist` OR `--yes-playlist` + `-i`
13. `-o` `<OUTPUT_DIR>/%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.%(ext)s`
14. `"$URL"`

**Output:** Full yt-dlp output (progress, warnings, info).

**After success (line 265):**
```
echo; echo "✓ Fatto → $OUTPUT_DIR"
```

**Exit code:** yt-dlp's exit code (uncaught; script doesn't wrap it).

**After exit:** Script terminates with `exit 0` (line 266).

### 5.4 SILENT Mode (`-s/--silent`)

**Condition:** `SILENT=1` (line 282)

**Behavior:** No terminal output. On failure, write `<title>.log` to output directory.

**Temporary files (line 283):**
```bash
saved="$(mktemp)"       # will contain filepaths of downloaded files
titlefile="$(mktemp)"   # will contain title/name for use in error log
errlog="$(mktemp)"      # will contain stderr from yt-dlp
```

**Cleanup trap (line 284):**
```bash
trap 'rm -f "$saved" "$titlefile" "$errlog"' EXIT
```

**yt-dlp invocation (lines 286–291):**
```bash
set +e
yt-dlp "${base_args[@]}" \
  --quiet --no-warnings --no-progress \
  --print-to-file "before_dl:%(artist,creator,uploader)s - %(track,title)s" "$titlefile" \
  --print-to-file "after_move:%(filepath)s" "$saved" \
  "$URL" >/dev/null 2>"$errlog"
rc=$?
set -e
```

**Argument vector (with expanded `base_args`):**
1. `yt-dlp`
2. `-x --audio-format` `<format>` `--audio-quality 0`
3. `--embed-metadata --embed-thumbnail --no-simulate`
4. `--replace-in-metadata` `"title,track"` `"\s*\[[^]]*\]"` `""`
5. `--replace-in-metadata` `"title,track"` `"\s*\((?i:...)\)"` `""`
6. `--parse-metadata` `"title:%(xartist)s - %(xtrack)s"`
7. `--parse-metadata` `"%(artist,creator,xartist,uploader)s:%(meta_artist)s"`
8. `--parse-metadata` `"%(track,xtrack,title)s:%(meta_title)s"`
9. `--no-playlist` OR `--yes-playlist` + `-i`
10. `-o` `<OUTPUT_DIR>/%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.%(ext)s`
11. `--quiet`
12. `--no-warnings`
13. `--no-progress`
14. `--print-to-file` `"before_dl:%(artist,creator,uploader)s - %(track,title)s"` `$titlefile`
15. `--print-to-file` `"after_move:%(filepath)s"` `$saved`
16. `"$URL"`

Redirects: stdout → `/dev/null`, stderr → `$errlog`

**Error reporting logic (lines 295–306):**
```bash
if [[ "$rc" -ne 0 || ! -s "$saved" ]]; then
  # Extract title from before_dl output for error log filename
  label="$(tail -n1 "$titlefile" 2>/dev/null | tr '/:' '__')"
  [[ -z "$label" ]] && label="ytdl-failed-$(date +%Y%m%d-%H%M%S)"
  {
    echo "ytdl — download FALLITO"
    echo "data: $(date)"
    echo "url:  $URL"
    echo "rc:   $rc"
    echo "----------------------------------------"
    cat "$errlog"
  } >"$OUTPUT_DIR/$label.log"
fi
exit "$rc"
```

**Error log file creation:**
- **Filename:** `<OUTPUT_DIR>/<title>.log`
- **Title extraction:** Last line of `before_dl` output (which contains `"Artist - Track"`)
- **Character replacement:** `/` and `:` in title → `__` (to avoid invalid paths)
- **Fallback title:** If title extraction fails, use `ytdl-failed-YYYYMMDD-HHMMSS`
- **File contents:**
  ```
  ytdl — download FALLITO
  data: <current datetime>
  url:  <original URL>
  rc:   <exit code>
  ----------------------------------------
  <stderr output from yt-dlp>
  ```

**Exit code:** `$rc` (yt-dlp's exit code, or 0 if download succeeded).

---

### 5.5 DEFAULT Mode (Normal)

**Condition:** None of the above flags set (lines 310–345)

**Behavior:** Download with progress bar, show title immediately, report saved file paths at end.

**Setup (lines 313–314):**
```bash
saved="$(mktemp)"
trap 'rm -f "$saved"' EXIT
```

**yt-dlp invocation (lines 316–321):**
```bash
set +e
yt-dlp "${base_args[@]}" \
  --quiet --no-warnings --progress \
  --print "before_dl:  ♪ $NAME_TEMPLATE.$FORMAT" \
  --print-to-file "after_move:%(filepath)s" "$saved" \
  "$URL"
rc=$?
set -e
```

**Argument vector (with expanded `base_args`):**
1. `yt-dlp`
2. `-x --audio-format` `<format>` `--audio-quality 0`
3. `--embed-metadata --embed-thumbnail --no-simulate`
4. `--replace-in-metadata` `"title,track"` `"\s*\[[^]]*\]"` `""`
5. `--replace-in-metadata` `"title,track"` `"\s*\((?i:...)\)"` `""`
6. `--parse-metadata` `"title:%(xartist)s - %(xtrack)s"`
7. `--parse-metadata` `"%(artist,creator,xartist,uploader)s:%(meta_artist)s"`
8. `--parse-metadata` `"%(track,xtrack,title)s:%(meta_title)s"`
9. `--no-playlist` OR `--yes-playlist` + `-i`
10. `-o` `<OUTPUT_DIR>/%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.%(ext)s`
11. `--quiet`
12. `--no-warnings`
13. `--progress`
14. `--print` `"before_dl:  ♪ %(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.<FORMAT>"`
15. `--print-to-file` `"after_move:%(filepath)s"` `$saved`
16. `"$URL"`

**Output before download:**
```
▸ Scarico (.<FORMAT>) → $OUTPUT_DIR
```
Then a blank line, then yt-dlp's progress bar.

**Saved file paths parsing (lines 326–333):**
```bash
first=""; count=0
if [[ -f "$saved" ]]; then
  while IFS= read -r f; do
    [[ -z "$f" ]] && continue
    count=$((count + 1))
    [[ -z "$first" ]] && first="$f"
  done <"$saved"
fi
```

- Reads lines from `$saved` (written by `--print-to-file after_move:...`)
- Skips empty lines
- Counts non-empty lines
- Stores first non-empty line

**Confirmation output (lines 335–343):**
```bash
echo
if [[ "$count" -eq 1 ]]; then
  echo "✓ Salvata in: $first"
elif [[ "$count" -gt 1 ]]; then
  echo "✓ $count tracce salvate in: $OUTPUT_DIR"
else
  echo "✗ Nessun file scaricato (rc=$rc)."
  echo "  (se l'URL conteneva &, controlla che fosse tra virgolette)"
fi

exit "$rc"
```

**Exit code:** `$rc` (yt-dlp's exit code).

---

## 6. SHARED ARGUMENT BUILDER: base_args

Used by both SILENT and DEFAULT modes (lines 272–278).

```bash
base_args=(
  -x --audio-format "$FORMAT" --audio-quality 0
  --embed-metadata --embed-thumbnail --no-simulate
  "${meta_args[@]}"
  "${playlist_args[@]}"
  -o "$OUTPUT_DIR/$NAME_TEMPLATE.%(ext)s"
)
```

**Expanded:**
1. `-x` — extract audio only
2. `--audio-format` `<FORMAT>` — e.g., `mp3`, `flac`, etc.
3. `--audio-quality` `0` — highest quality
4. `--embed-metadata` — write ID3 tags
5. `--embed-thumbnail` — embed cover art
6. `--no-simulate` — **CRITICAL** (line 270–271 comment): `--print`/`--print-to-file` would otherwise imply `--simulate` (no download)
7–11. `meta_args[@]` — the metadata normalization pipeline (5 arguments)
12–13. `playlist_args[@]` — either `(--no-playlist)` or `(--yes-playlist -i)`
14. `-o` `<OUTPUT_DIR>/%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.%(ext)s`

---

## 7. ERROR HANDLING

### Global Error Policy (line 10)
```bash
set -euo pipefail
```
- `-e`: exit on unhandled error
- `-u`: error on unbound variable
- `-o pipefail`: pipeline fails if any command fails

### Per-yt-dlp-call Pattern (examples: lines 286–292, 316–322)
```bash
set +e
yt-dlp ...
rc=$?
set -e
```

Around each yt-dlp invocation:
1. `set +e` — disable error exit (yt-dlp failures are caught)
2. Invoke yt-dlp
3. Capture exit code into `rc`
4. `set -e` — re-enable error exit
5. Check `rc` and handle accordingly

**Why needed:** Even in modes that exit without checking `rc` (VERBOSE, DRY-RUN), the script must not abort on yt-dlp failure — it should let the exit code propagate.

### Temporary file cleanup (examples: lines 284, 314)
```bash
trap 'rm -f "$saved" "$titlefile" "$errlog"' EXIT
```

Ensures temporary files are deleted when script exits, even on error.

### Silent mode failure handling (lines 295–306)
Writes `.log` file if:
- `$rc -ne 0` (yt-dlp failed), OR
- `! -s "$saved"` (no files were written)

### Dependency check failures (lines 187–188)
Calls `missing_dep()` which prints error to stderr and exits 1.

---

## 8. EXIT CODES

| Scenario | Exit Code |
|----------|-----------|
| `-h/--help` flag | 0 |
| `-V/--version` flag | 0 |
| `--update` success | 0 |
| `--update` failure | 1 |
| No URL provided | 1 |
| Unknown flag | 1 |
| Dependency missing (yt-dlp or ffmpeg) | 1 |
| `-b/--background` (always) | 0 |
| `-n/--dry-run` success | 0 |
| `-n/--dry-run` yt-dlp error | non-zero (from yt-dlp) |
| `-v/--verbose` success | 0 |
| `-v/--verbose` yt-dlp error | non-zero (from yt-dlp) |
| `-s/--silent` success | 0 |
| `-s/--silent` yt-dlp error | non-zero (from yt-dlp) |
| Default mode success | 0 |
| Default mode yt-dlp error | non-zero (from yt-dlp) |

---

## 9. VERSION AND UPDATE

### `-V/--version` flag (lines 101–108)

```bash
show_version() {
  echo "ytdl $YTDL_VERSION"
  if command -v yt-dlp >/dev/null 2>&1; then
    echo "yt-dlp $(yt-dlp --version 2>/dev/null || echo "(non risponde)")"
  else
    echo "yt-dlp non installato"
  fi
}
```

**Output:**
```
ytdl 1.0.0
yt-dlp 2024.XX.XX
```

or if yt-dlp is missing:
```
ytdl 1.0.0
yt-dlp non installato
```

or if yt-dlp doesn't respond:
```
ytdl 1.0.0
yt-dlp (non risponde)
```

### `--update` flag (lines 112–131)

Constructs installer URL from `$YTDL_REPO` and `$YTDL_BRANCH`, fetches via curl, pipes to bash:
```bash
url="https://raw.githubusercontent.com/$YTDL_REPO/$YTDL_BRANCH/install.sh"
curl -fsSL --retry 3 --connect-timeout 20 "$url" | bash
```

**Success:** Exit 0  
**Curl missing:** Print error to stderr, exit 1  
**Download/execution failure:** Print error to stderr, exit 1

---

## 10. yt-dlp Print/Simulate Subtleties

### The `--no-simulate` Requirement

**Context:** Lines 270–271 comment and line 274.

When using `--print` or `--print-to-file` with a `before_dl` key (which prints info about a video before it is downloaded), yt-dlp normally switches to simulate mode (no download). The `--no-simulate` flag **explicitly forces download mode to remain on**.

**Without `--no-simulate`:**
```bash
yt-dlp --print "before_dl:..." "$URL"
# → prints filename, then exits (no download)
```

**With `--no-simulate`:**
```bash
yt-dlp --print "before_dl:..." --no-simulate "$URL"
# → prints filename, then downloads the file
```

In silent and default modes, `base_args` includes `--no-simulate` (line 274). This is **critical** and easily broken by accident.

### Print Keys Used

**DRY-RUN mode (line 233):**
```
--print "$NAME_TEMPLATE.$FORMAT"
```
Prints filename (no `before_dl` or `after_move` key, so no special timing semantics).

**SILENT mode (lines 289–290):**
```
--print-to-file "before_dl:..." "$titlefile"
--print-to-file "after_move:%(filepath)s" "$saved"
```
- `before_dl:` + template → filename written **before download** → for use in error log naming
- `after_move:%(filepath)s` → full path written **after file is moved** to final location

**DEFAULT mode (lines 319–320):**
```
--print "before_dl:  ♪ $NAME_TEMPLATE.$FORMAT"
--print-to-file "after_move:%(filepath)s" "$saved"
```
- `before_dl:` + template → printed immediately for user visibility
- `after_move:%(filepath)s` → full path written after move

### Print-to-File Output Format

The `after_move:%(filepath)s` key produces one line per downloaded file, containing the **absolute file path**. Example:
```
/home/user/Music/ytdl/Artist - Track.mp3
/home/user/Music/ytdl/Another Artist - Song.flac
```

---

## 11. CRITICAL IMPLEMENTATION NOTES FOR GO PORT

### 1. Helper Fields and Meta Fields Must Exist

yt-dlp creates fields on-demand via `--parse-metadata`. The Go port **must not assume these fields exist before the pipeline runs**. The order matters:

1. `xartist`, `xtrack` are created by line 209 (title split)
2. `meta_artist`, `meta_title` are created by lines 210–211

Before line 209, `xartist` and `xtrack` do not exist. After line 209, they are populated. This is implicit in yt-dlp's behavior.

### 2. Regex Syntax

STRIP_BRACKETS and STRIP_TAGS use **Python `re` syntax**, which yt-dlp's `--replace-in-metadata` expects. Not PCRE, not Bash glob.

### 3. The ` - ` Separator

The title split in line 209 (`title:%(xartist)s - %(xtrack)s`) splits on ` - ` (space-dash-space). This is **exact**: a title like `"Artist-Track"` (no spaces) will NOT split and will become `xtrack="Artist-Track"`.

### 4. Case-Insensitive STRIP_TAGS

The `(?i:...)` in STRIP_TAGS makes the entire group case-insensitive. So `(original mix)`, `(Original Mix)`, `(ORIGINAL MIX)` all match.

### 5. Fallback Chain Semantics

yt-dlp's `%(a,b,c)s` means: use `a` if present and non-empty, else `b`, else `c`. Not all-present values, strictly first-present.

### 6. Trailing Slash Normalization

Before any use, `OUTPUT_DIR` has trailing `/` removed (line 169). The Go port must do this.

### 7. mktemp Behavior

Bash `mktemp` without arguments creates a file in `/tmp` with a random name. The file already exists; Go must create these files or use equivalent behavior (temp file creation with cleanup).

### 8. File Path Reading

The default mode reads `$saved` line by line, skipping empty lines (lines 328–333). The Go port must handle:
- Empty lines (skip)
- Multiple lines (count and report plural)
- No lines (report failure)

### 9. Title Extraction for Error Logs

Silent mode extracts the title from `$titlefile` by taking the last line and translating `/` and `:` to `__` (line 296). This is the **only** place where character translation happens. Go must replicate this exactly.

### 10. Date Format for Fallback Title

If title extraction fails in silent mode, the fallback uses `$(date +%Y%m%d-%H%M%S)`, e.g., `20240715-143052`. Go must produce this format.

### 11. Playlist Error Handling

When playlist mode is enabled (`-p`), the `--yes-playlist -i` flags tell yt-dlp to:
- `-i`: skip videos that fail and continue with the next one
- `--yes-playlist`: treat the URL as a playlist

A single-track URL with these flags will still download the track (just treated as a playlist of 1 item).

### 12. Environment Variable PATH Prepending

Lines 20–23 prepend `$HOME/.local/bin` to `$PATH` if it's not already there. The Go port should do this at startup to ensure yt-dlp and ffmpeg (installed by the installer) are found.

### 13. Nohup Redirection in Background Mode

Line 248 redirects stdout and stderr to `/dev/null` **before** spawning the background process:
```bash
nohup ... >/dev/null 2>&1 &
```

The stdout/stderr of the `nohup` command itself (not the subprocess) are redirected. The Go port must ensure the background process truly detaches and does not inherit the parent's terminal.

---

## 12. FRAGILITY & GOTCHAS

### 1. Double Splitting

The filename template uses `-(artist, creator, xartist, uploader)s - %(track, xtrack, title)s`, which is `<artist> - <track>`. If the title was `"A - B - C"`, then `xartist="A"` and `xtrack="B - C"`. The filename becomes `A - B - C`, which is correct. But if the title has only one ` - `, there's no problem. Edge case: single-word titles without ` - ` → `xtrack=<entire title>`, `xartist=""`, so the name becomes `<uploader> - <title>`. This is intentional fallback behavior.

### 2. Structured Metadata Wins Silently

A video with both native `artist`/`track` fields AND a title like `"Other - Track"` will use native fields and ignore the title split. The helper fields are only fallback. This is by design (architecture.md).

### 3. Error Log Character Replacement

The replacement `tr '/:' '__'` replaces both `/` and `:` with `__`. A title like `A/B:C` becomes `A__B__C`. The Go port must use the same translation.

### 4. First-Present Semantics

If `artist`, `creator`, `xartist`, and `uploader` are all empty, the filename slot is empty. yt-dlp will likely fail or use a default. The Bash script does not handle this edge case — it delegates to yt-dlp.

### 5. No Validation of FORMAT

The `-f/--format` flag accepts any string without validation. If an invalid format is passed (e.g., `-f bogus`), yt-dlp will fail. The script does not pre-validate.

### 6. Env Var Expansion in Arguments

The `$STRIP_BRACKETS` and `$STRIP_TAGS` strings are expanded into the `meta_args` array. Go must ensure these regexes are passed as-is to yt-dlp, not interpreted by the shell.

### 7. No Quoting Issues in Bash

The Bash script builds arrays and passes them to yt-dlp via `"${array[@]}"` expansion, which safely preserves each element. Go must construct the argv similarly — each regex and template must be a separate argument, not concatenated into one string.

---

## 13. COMPREHENSIVE EXAMPLE WALK-THROUGH

### Example: `ytdl -f flac -p "https://www.youtube.com/playlist?list=PLxxxxxx"`

**Parsing:**
- `-f` → `FORMAT="flac"`
- `-p` → `NO_PLAYLIST=0`
- URL → `"https://www.youtube.com/playlist?list=PLxxxxxx"`

**Setup:**
- `OUTPUT_DIR="$HOME/Music/ytdl"` (default)
- `playlist_args=(--yes-playlist -i)`
- No mode flags → default mode

**Invoked yt-dlp command line (pseudo-code):**
```bash
yt-dlp \
  -x --audio-format flac --audio-quality 0 \
  --embed-metadata --embed-thumbnail --no-simulate \
  --replace-in-metadata "title,track" "\s*\[[^]]*\]" "" \
  --replace-in-metadata "title,track" "\s*\((?i:original mix|...)\)" "" \
  --parse-metadata "title:%(xartist)s - %(xtrack)s" \
  --parse-metadata "%(artist,creator,xartist,uploader)s:%(meta_artist)s" \
  --parse-metadata "%(track,xtrack,title)s:%(meta_title)s" \
  --yes-playlist -i \
  -o "$HOME/Music/ytdl/%(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.%(ext)s" \
  --quiet --no-warnings --progress \
  --print "before_dl:  ♪ %(artist,creator,xartist,uploader)s - %(track,xtrack,title)s.flac" \
  --print-to-file "after_move:%(filepath)s" /tmp/temp-filename-1234 \
  "https://www.youtube.com/playlist?list=PLxxxxxx"
```

**Output:**
```
▸ Scarico (.flac) → /home/user/Music/ytdl
before_dl:  ♪ Artist 1 - Song 1.flac
[yt-dlp progress bar and download...]
before_dl:  ♪ Artist 2 - Song 2.flac
[yt-dlp progress bar and download...]
...

✓ 3 tracce salvate in: /home/user/Music/ytdl

(exits with code 0)
```

---

## 14. SUMMARY TABLE: Mode Comparison

| Aspect | DRY-RUN | BACKGROUND | VERBOSE | SILENT | DEFAULT |
|--------|---------|------------|---------|--------|---------|
| **Invokes yt-dlp?** | Yes | No (re-exec) | Yes | Yes | Yes |
| **Downloads file?** | No (--skip-download) | Yes (in -s child) | Yes | Yes | Yes |
| **Shows output?** | Filenames only | "Started in background" | Full yt-dlp output | None (error log only) | Progress bar + paths |
| **Embeds metadata?** | No | Yes (via re-exec) | Yes | Yes | Yes |
| **Exit code** | 0 or yt-dlp error | 0 | 0 or yt-dlp error | yt-dlp error or 0 | yt-dlp error or 0 |
| **base_args used?** | No | No | No | Yes | Yes |
| **meta_args used?** | Yes | Yes (in child) | Yes | Yes | Yes |
| **--no-simulate?** | No (not needed, no --print) | No direct (in child) | No (not needed, no --print) | Yes | Yes |
| **Temp files?** | None | None | None | titlefile, saved, errlog | saved |
| **Error reporting** | Via exit code | Via .log in background | Via exit code | Via .log file | Via exit code |

---

## 15. FINAL CHECKLIST FOR GO PORT

- [ ] Parse all flags (`-o`, `-f`, `-p`, `-n`, `-s`, `-b`, `-v`, `-h`, `-V`, `--update`, `--`)
- [ ] Validate that URL is provided
- [ ] Implement environment variable precedence: flag > env var > default
- [ ] Strip trailing `/` from `OUTPUT_DIR`
- [ ] Check for `yt-dlp` and `ffmpeg` in PATH (prepend `$HOME/.local/bin` first)
- [ ] Build `meta_args` array with exact 5 elements in order (2 replace, 3 parse)
- [ ] Build `playlist_args` array (1 or 2 elements)
- [ ] Implement DRY-RUN mode: `--skip-download` + `--print` template
- [ ] Implement BACKGROUND mode: fork/spawn with `-s`, return immediately to user
- [ ] Implement VERBOSE mode: no `--quiet`, allow full output
- [ ] Implement SILENT mode: capture stderr, create `.log` on failure, handle `rc`
- [ ] Implement DEFAULT mode: progress bar, read saved file list, report 1-file vs N-files vs 0-files
- [ ] Use `--no-simulate` in `base_args` (used by SILENT and DEFAULT)
- [ ] Implement `--print-to-file before_dl:...` for title extraction in SILENT mode
- [ ] Implement `--print-to-file after_move:%(filepath)s` to track saved files
- [ ] Handle character replacement `/` and `:` → `__` for error log filename
- [ ] Use `date +%Y%m%d-%H%M%S` format for fallback error log name
- [ ] Implement `EXIT` trap equivalent to clean temp files
- [ ] Correctly expand template strings (NO shell interpolation when building yt-dlp argv)
- [ ] Respect `set -e` semantics (Go: panic or explicit error handling)
- [ ] Handle help and version output exactly as specified
- [ ] Implement `--update` (curl + bash) or equivalent

