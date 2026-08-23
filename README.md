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
ytdl gui                                            # open the web interface in a browser
```

**Always put the URL in quotes.** YouTube URLs contain `&`, which the terminal
would otherwise read as "run this in the background".

Prefer not to use the Terminal? `ytdl gui` opens a local web interface (on your
own machine, at `127.0.0.1`) to paste a link, watch live progress, see the queue
and history, and edit settings — no Terminal needed.

A background download (`-b`) joins a queue that keeps draining after you close the
Terminal window:

```bash
ytdl queue                                          # what is downloading and what is waiting
ytdl cancel                                         # stop a download (no argument: pick from a list)
ytdl history                                        # what has been downloaded, newest first
ytdl retry                                          # re-queue a failed download
```

Music is saved to `~/Music/ytdl` by default. Run `ytdl --help` for the essentials
and `ytdl help` for the topic index.

To change the default destination permanently, add this to your `~/.zprofile`:

```bash
export YTDL_OUT_DIR="/path/to/your/folder"
```

## Keeping it working

yt-dlp stops working whenever YouTube changes something — expect this every few
months. **ytdl notices for you.**

It checks while you are already using it — at most once a day, never on a timer,
never while it sits idle — and then tells you wherever you happen to be: a line
after a download in the Terminal, a banner in the web interface. A check that
fails is silent, and never reported as "up to date": *"I could not check"* and
*"you are current"* stay different answers.

```bash
ytdl --update      # updates ytdl, yt-dlp and ffmpeg together
ytdl --version     # what you have, how each part stands, and whether an update exists
```

From the web interface there is no Terminal at all: **Impostazioni → Versione e
aggiornamenti → Aggiorna**. It starts only once the queue is empty, and if the
update replaced ytdl itself the page closes and reopens on the new build by
itself.

ytdl decides which yt-dlp and which ffmpeg it drives — it builds an exact
command line for them, so the working combination is a property of ytdl rather
than a user preference. Nothing to configure, and a bad combination can be fixed
for everyone by changing one line, without waiting for a release.

To stop it checking on its own, put `update_check = false` in
`~/.config/ytdl/config`, or untick the box in the interface. `ytdl --update` and
the manual check keep working either way.

The full picture, in Italian, is in
[guida-uso.md](docs/guida-uso.md#tenere-ytdl-aggiornato).

## Supported systems

| System | Status |
|---|---|
| macOS 10.15 Catalina → current, Intel or Apple Silicon | supported |
| macOS 10.14 Mojave and older | not supported |
| Windows, Linux | planned |

## Documentation

Design notes, distribution constraints and the roadmap are in [docs/](docs/).
Release history is in the [changelog](CHANGELOG.md).

## Requirements

The installer sets these up for you:

- **yt-dlp** — the downloader
- **ffmpeg** — audio extraction and cover art embedding

## Licence

[PolyForm Strict 1.0.0](LICENSE.md) — you may use this software for any
noncommercial purpose. Redistributing it, or building on it, is not permitted.
