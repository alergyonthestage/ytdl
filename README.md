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

ytdl runs on **macOS 10.15 Catalina or newer** (Intel or Apple Silicon). Older
systems are not supported.

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
| macOS 10.14 Mojave and older | not supported |
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
