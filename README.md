# ytdl

Download music from YouTube and YouTube Music with clean filenames and proper
tags. Files come out as `Artist - Track.mp3` with artist, title and cover art
already embedded — ready for your music library.

A wrapper around [yt-dlp](https://github.com/yt-dlp/yt-dlp) that handles the
metadata work for you.

> 🇮🇹 **Guide in italiano:** [installazione](docs/guida-installazione.md) ·
> [uso quotidiano](docs/guida-uso.md) — scritte per chi non ha mai aperto il
> Terminale.

## Install

Open **Terminal** (press `⌘ Space`, type `Terminal`, press Enter), then paste
this line and press Enter:

```bash
curl -fsSL https://raw.githubusercontent.com/alergyonthestage/ytdl/main/install.sh | bash
```

Everything is installed into your own user folder — no password, no admin
rights, nothing touched system-wide.

When it finishes, **open a new Terminal window** and you are ready to go.

### If you are on macOS 10.13 or 10.14

The installer will ask you to install Python first, then run it again. This is
because current yt-dlp builds need macOS 10.15 or newer, so on older systems it
runs through Python instead. The Python installer is the official one from
python.org and opens without any security warning.

macOS 10.12 and older cannot be supported.

## Use

```bash
ytdl "https://youtu.be/XXXX"                        # one track
ytdl -p "https://youtube.com/playlist?list=YYYY"    # a whole playlist
ytdl -f flac -o ~/Desktop "https://youtu.be/XXXX"   # FLAC onto the Desktop
ytdl -n "https://youtu.be/XXXX"                     # preview names, download nothing
ytdl -b "https://youtu.be/XXXX"                     # background, returns immediately
```

**Always put the URL in quotes.** YouTube URLs contain `&`, which the terminal
would otherwise read as "run this in the background".

Music is saved to `~/Music/ytdl` by default. Run `ytdl --help` for every option.

To change the default destination permanently, add this to your `~/.zprofile`:

```bash
export YTDL_OUT_DIR="/path/to/your/folder"
```

## Keeping it working

yt-dlp stops working whenever YouTube changes something — expect this every few
months. When downloads start failing:

```bash
ytdl --update
```

That updates ytdl, yt-dlp and ffmpeg together. It is almost always the fix.

## Supported systems

| System | Status |
|---|---|
| macOS 10.15 Catalina → current, Intel or Apple Silicon | supported |
| macOS 10.13 High Sierra – 10.14 Mojave | supported, requires Python (installer guides you) |
| macOS 10.12 and older | not supported |
| Windows, Linux | planned |

## Documentation

Design notes, distribution constraints and the roadmap are in [docs/](docs/).

## Requirements

The installer sets these up for you:

- **yt-dlp** — the downloader
- **ffmpeg** — audio extraction and cover art embedding

## Licence

[PolyForm Strict 1.0.0](LICENSE.md) — you may use this software for any
noncommercial purpose. Redistributing it, or building on it, is not permitted.
