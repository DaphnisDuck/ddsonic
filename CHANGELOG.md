# Changelog

Notable changes to ddsonic (DaphnisDuck's Music Player). The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Within 1.x, a newer ddsonic opens an older config and catalog; going back to an older ddsonic is not supported.

ddsonic is a fork of [cliamp](https://github.com/bjarneo/cliamp). Versions before 0.6 were released as omatunes, and 0.6 up to the candidate 1.0.0-rc.1 as ddmus; their entries below keep the name they were released under.

## [1.0.0] - 2026-10-31

The first release for people who don't build from source. 1.0 adds no features to 0.10: it settles the command line, removes what ddsonic doesn't support, and ships a tested Linux build.

### Added
- A release build for Linux x86-64 with checksums, third-party notices, an SBOM and the complete corresponding source. It has no build attestation: `v1.0.0` was built locally in the pinned build image, not by the release workflow ([SECURITY.md](SECURITY.md#official-builds)). An AUR package, `ddsonic-bin`, is planned; it is not in the AUR yet.
- `ddsonic setup` sets up Spotify, YouTube Music and the Local music folder, and keeps every other line of an existing `config.toml`.
- `--version` answers in every build.
- The release archive holds `RELEASE-MANIFEST.json`: the version, the exact source commit and the binary's SHA-256, for a machine to read. A release build's `ddsonic --version` names that commit too (`ddsonic version v1.0.0 (commit …)`); a build from source prints the version alone, as before.
- A logo and an icon for ddsonic: a duck at a terminal. The release archive (in `share/`) and `make install` carry a launcher entry, `ddsonic.desktop`, and the icon in eight sizes from 16 to 512 pixels, so ddsonic appears in application menus and opens in a terminal; the planned AUR package, which is not published yet, is prepared to carry them too. The artwork is raster (PNG); its masters and layered sources are in `assets/branding`.
- The release archive carries the logo and the screenshot the README shows, beside it, so its copy of the README displays them; the planned AUR package, which is not published yet, is prepared to do the same.
- Desktop media controls can show ddsonic's icon: ddsonic names its launcher entry to them (the MPRIS `DesktopEntry`).

### Changed
- Renamed from ddmus to ddsonic. The executable, the config, data, cache and download folders, the config section (`[ddsonic]`), the `DDSONIC_CONFIG_DIR` variable, the log and socket, the media-key name (`org.mpris.MediaPlayer2.ddsonic`), the release files, the AUR package (`ddsonic-bin`) and the GitHub repository all changed. The candidate `v1.0.0-rc.1` was published as ddmus and stays that way. ddsonic does not read or move ddmus's folders: settings, sign-ins and the library are moved by hand, once; see [Moving from ddmus](https://github.com/DaphnisDuck/ddsonic/blob/main/docs/ddsonic/files.md#moving-from-ddmus-100-rc1-and-earlier).
- The header reads `🦆 ddsonic` and the version, in place of "DaphnisDuck's Music Player", and desktop media widgets (the MPRIS identity) show `ddsonic`.
- The terminal's title reads `ddsonic` from the start, then the playing track as before. cliamp's intro line no longer scrolls through it at startup.
- `--provider`'s help calls `cliamp` the legacy provider key for Internet Radio. The key itself is unchanged.
- `ddsonic --help` opens with "music for your terminal", the logo's tagline, in place of cliamp's "retro terminal music player".
- The README says how the ddsonic name, logo and icon may be used: the code's licenses don't cover them, the artwork may be redistributed with ddsonic and its packages (resized or converted as packaging needs), and neither may identify a modified or unrelated project.
- ddsonic 1.0 supports Spotify, YouTube Music, local files and internet radio. cliamp's other providers (Navidrome, Plex, Jellyfin, SoundCloud and the rest) are not offered: `--provider` and `setup` no longer list them, and their sections in `config.toml` are left untouched but have no effect.
- The log and the IPC socket are named `ddsonic.log` and `ddsonic.sock` (they kept cliamp's names before, and were `ddmus.log` and `ddmus.sock` in 1.0.0-rc.1). A script that opens the socket by path needs the new name.
- In "Search Spotify for …" and its YouTube Music twin, Enter replaces the queue, as playing from the library does. The YouTube Music twin exists only when YouTube Music signs in with browser cookies alone; with an OAuth client configured, YouTube Music has no live search.
- YouTube Music's "Albums" and "Artists" lists are now "Albums from tracks" and "Artists from tracks", and each says above its rows, when space permits, what it holds: the albums and artists found in your Liked Music and synced playlists (albums may be incomplete). Saved YouTube Music Library Albums and followed artists are not synchronized, and never were; the old names read as if they were. In All Music and search, these rows are labelled `YouTube · from tracks`. Nothing about what syncs has changed.
- By default, Spotify syncs at startup when its last successful sync is more than two hours old (it was 30 minutes).
- A `config.toml` that is not valid TOML now stops ddsonic from starting, with the file, the line and column and the parser's message, and the file left as it was. Before, ddsonic skipped what it could not read and started on defaults for it, without a word. A missing config file still means the defaults, and a valid file is read as before; unknown keys and sections are still ignored. Values that are not TOML are now errors too, such as a string without quotes (`theme = Tokyo Night`) or a key given twice. One line is still accepted as older versions wrote it: `audio_device = <name>` without quotes, which choosing an output device used to save; ddsonic now writes it in quotes. See [ddsonic files and names](docs/ddsonic/files.md).
- ddsonic names itself, not cliamp, in the HTTP requests it makes (radio streams, the station directory, lyrics): `ddsonic/<version>`.
- `shuffle` and `repeat` reject values they don't know.
- `ddsonic upgrade`, which has never updated ddsonic, now points to the package manager and the releases page.
- The executable is distributed under GPL-3.0, because it links go-librespot; ddsonic's own source stays MIT.

### Removed
- Lua plugins: none is loaded, and the `plugins` command and its IPC operations are gone. Plugin files and `[plugins.*]` config stay where they are, unused.
- `--daemon` (headless mode), `--mono`, `--expanded`, `--simplified`, and the `mono`, `open`, `protocol`, `qobuz`, `radio` and `tidal` commands. `cliamp://` links are neither registered nor handled; see [docs/ddsonic/files.md](https://github.com/DaphnisDuck/ddsonic/blob/main/docs/ddsonic/files.md#still-shared) if an earlier version (as ddmus or omatunes) registered them.
- The Logo visualizer, which draws cliamp's name. A config that names it starts with the default visualizer.
- cliamp's Nix flake, desktop entry, icons, website and install script, which built or installed cliamp. ddsonic installs from the release archive or source (an AUR package is planned but not available yet), and has a launcher entry and an icon of its own.

### Known limitations
- Spotify sign-in: if ddsonic quits, or the work is cancelled, in the moment between Spotify issuing a new refresh token and ddsonic accepting it, the stored sign-in is left as it was, and the next start may ask you to sign in again. The stored sign-in is never left half-written.
- Spotify sign-in: the session a sign-in replaces is retired at once, but its network connection is closed by the Spotify library, which can take a while when that library is reconnecting during a network fault. Until then the old connection stays in memory, and several can pile up if you sign in repeatedly during such a fault. They go when the library finishes, or when you quit.
- A Spotify track that was being opened at the moment its session was replaced by a sign-in fails; play it again. A track already playing keeps playing.
- Spotify playback in Ogg Vorbis: when a track's stream has arrived but the track is never played, because opening it was cancelled or overtaken or a sign-in replaced its session at that moment, the memory of that stream's decoder may not be given back until you quit. The Spotify library ddsonic uses cannot yet release such a decoder safely. Nothing is played wrongly and the stored sign-in is not affected.

### Fixed
- A Spotify sign-in could be marked as refused, or its stored credential overwritten by an older one, when two renewals of the Spotify token overlapped. One part of ddsonic now holds the token and the stored sign-in, renews it one request at a time, and takes an answer only for the token it is about.
- A Spotify request that met a rate limit could renew its token a second time when the wait was over. It renews once.
- The sync a Spotify sign-in asks for could be dropped when an older sync was still finishing, leaving the library empty until the next retry; so could `r` pressed in the instant after a sync ended. Both now run as soon as the older sync has finished.
- A Spotify sign-in that had already been cancelled could still show its address or open the browser.
- Asking to sign in to Spotify while ddsonic was still starting Spotify from the stored sign-in could renew the stored sign-in twice at once. The sign-in now waits for that start, and opens no browser when it worked.
- Opening a Spotify track, or replacing or closing a Spotify session, could wait on the Spotify library for as long as it was reconnecting, with the library's screens waiting behind it. Neither waits on it now.
- Spotify could not be signed in to from a home that had never signed in. Spotify's lists have been read from the catalog since 0.2, and from then on nothing on screen asked for the sign-in: the heading read "sync failed · cached" and every list "Syncing your library…", for good. The same dead end followed `ddsonic spotify reset`, and a sign-in Spotify revoked while ddsonic ran could not be renewed without a restart. Now the heading reads `Spotify sign-in required`, Spotify's menu starts with **Sign in to Spotify**, an empty Spotify list says "Sign-in required. Press Enter to sign in.", and signing in syncs your library at once. Spotify's own search and the playlist picker ask for the sign-in the same way; an add to a playlist is never repeated for you. A sign-in Spotify refuses no longer deletes the stored credential: it stays until a sign-in that worked replaces it. A change you asked for stays with the sign-in that stood when you asked: if you sign in again before it starts, or part-way through adding several tracks or creating a playlist and adding to it, what was done stays, the rest is not sent, and nothing is repeated or undone for you. The message says what is known: a track "was not added" when its request was never sent or was refused, and "may or may not have been added" when the request timed out or lost its connection. If the browser does not open, the screen shows the sign-in address as a link; that fallback is best-effort (a terminal or multiplexer without link support may have no usable way to take the address by hand, and below 80×20 not all of its text is shown).
- An empty Spotify list said "Syncing your library…" whatever had happened. It now says whether it needs a sign-in, is syncing, could not sync ("Couldn't sync Spotify. Press r to retry."), or is simply empty, and the heading says `· cached` only when there is a cache to browse.
- A Spotify track that would not play because Spotify could not be reached during the reconnect was reported as "sign-in required". It is reported as the outage it is; so is Spotify's own search when the sign-in's token could not be refreshed at startup.
- Stop and skipping did not end a YouTube Music track that was still loading. Its yt-dlp ran on unseen until it produced audio or 30 seconds had passed, so holding the skip key piled up one yt-dlp per track skipped. Stop, the next track, and a dropped or replaced preload of the following track now end the yt-dlp of the track they make unwanted at once, together with its duration lookup; Stop and a newer seek do the same for a seek that is still loading its new position. A seek that was overtaken by a change of track could also leave the new track playing in silence; it no longer touches a track that is not the one it was made in. Other yt-dlp work (a sync, a list being read, a track being saved) is not touched.
- Stopping yt-dlp could leave a copy of your browser's cookies in the temporary folder. To sign in with `cookies_from`, yt-dlp copies the browser's cookie database into a `yt_dlp…` folder under `/tmp` while it reads it, and removes the copy afterwards. ddsonic stopped yt-dlp by killing it (skipping a track that had just started, Stop, quitting during a sync, a timeout), and a yt-dlp killed at that moment cannot remove its copy. ddsonic now interrupts yt-dlp first, so that it cleans up, and gives every yt-dlp run a temporary folder of its own, which ddsonic removes when the run is over however it ended; without that folder yt-dlp is not run, and a folder that cannot be removed is reported. Quitting now stops every yt-dlp still running and waits a few seconds for it, so a track that was still being saved when you quit is not saved; save it again (a partial file may or may not be left and reused, which is up to yt-dlp and the source). Not covered: ddsonic itself being killed while yt-dlp runs. Folders left by earlier versions are not touched: look for `yt_dlp*` in your temporary folder and delete them yourself.
- yt-dlp's own configuration files could change what ddsonic did. yt-dlp reads options from a `yt-dlp.conf` beside its executable or in the folder ddsonic was started from, and from your user and system yt-dlp configuration; ddsonic did nothing to stop that. A configuration with `--mark-watched` made every signed-in read, a library sync included, add to your YouTube watch history, and other options could change the format, the output file or the account used. ddsonic now runs yt-dlp with `--ignore-config` and `--no-mark-watched`, for syncing, playing and saving alike. If you relied on a yt-dlp configuration file to change how ddsonic plays (cookies, extractor arguments, a proxy), it no longer applies: sign in with `cookies_from` and set a proxy through the environment; see [docs/ddsonic/youtube.md](https://github.com/DaphnisDuck/ddsonic/blob/main/docs/ddsonic/youtube.md#yt-dlps-own-configuration-is-not-read).
- Radio: `f` stars and unstars the highlighted station again. Nothing in the library could add a station to Radio → Favorites or take one out. It works in a country's or a tag's list, the radio directory's search results, Favorites, search results and the queue, on the selected station whether or not it is playing; the key bar shows it there. Favorites and search follow the change at once.
- Search results: a track and a radio station now say which source they are from (`Local`, `Spotify`, `YouTube`, `Radio`), as albums, artists and playlists did. The same recording held in two sources was two rows nothing told apart.
- Two quick volume changes from a desktop media control could freeze ddsonic; a dropped session bus could crash it.
- A stalled YouTube Music download froze every control until yt-dlp gave up.
- Opening a Spotify track on a stalled network froze ddsonic for up to 30 seconds.
- A Local playlist that lists a track more than once lost a copy on every save.
- A YouTube Music sign-in made inside ddsonic stopped working about an hour later, until a restart.
- A stalled Spotify request could hold a library sync until quit.
- A Spotify connection that stalled while starting up held every Spotify action behind it: a track stayed on "Buffering" and the sync never finished. Setting up the session now gives up after 30 seconds, and the next action tries again.
- Stop now cancels an album that is still opening from search.
- "Search Spotify for …" and its YouTube Music twin (present only when YouTube Music signs in with browser cookies alone) now say whose results they show and for what ("Searching Spotify for …", "Spotify results for …"). The screen read "Search" and "Results", so its rows could be taken for the library's own.
- Editing the query of such a search while it was still running left it on "Searching..." with Enter doing nothing, until Esc. Editing a live provider search now cancels the pending request, clears the loading state and allows a new search to start.
- A sync requested while another ran could run twice, and a sync that had just finished could cancel the retry of a newer one that failed, leaving that source unsynced until the next refresh.
- With a long config directory (a `DDSONIC_CONFIG_DIR`, `XDG_CONFIG_HOME` or `HOME` that puts the IPC socket's path over 107 bytes on Linux, 103 on macOS), ddsonic started normally but without its socket, so `ddsonic pause`, `ddsonic status` and the rest failed with "connect: invalid argument", and the only notice appeared after quitting. ddsonic now refuses to start with such a path and says which path is too long and by how many bytes; the commands print the same message. See [ddsonic files and names](docs/ddsonic/files.md).
- Hardening from an independent review of playback, catalog identity and sync.

## [0.10.0] - 2026-09-30

Released as ddmus.

### Added
- Album artwork in the track info view (`i` in the queue), in Kitty and Ghostty, from Spotify covers and from local files' embedded pictures or cover files. Other terminals show the info view as before.

## [0.9.0] - 2026-09-30

Released as ddmus.

### Changed
- ddmus fills the terminal: a taller window lists more rows on every screen, a wide one lines rows up as a table instead of stretching them, and resizing reflows at once.
- A border frames the screen; `border = false` under `[ddmus]` turns it off.

## [0.8.0] - 2026-09-30

Released as ddmus.

### Changed
- One set of keys on every screen: `q` quits from anywhere, `Esc` only ever goes back, `p` and `n` skip tracks, and `/` is only ever search (the filter, in the queue).
- The key bar's labels no longer use `/` to join keys.

### Removed
- The mono key, and the far seek outside the queue.

### Fixed
- Spotify's long rate-limit blocks no longer hang album opens. While a block lasts ddmus sends Spotify nothing, and it waits the block out across restarts.

## [0.7.0] - 2026-09-30

Released as ddmus.

### Added
- The queue view works under the library: shuffle, repeat, play next, track info, queue editing, lyrics, EQ presets, speed and jump to a time.
- The settings panel's SRC names the playing track's source.
- Every view lists the keys that work there in a bar at the bottom, replacing cliamp's keymap overlay.

### Removed
- Favorite (`n`) in the queue.

## [0.6.0] - 2026-09-30

Released as ddmus.

### Changed
- Renamed from omatunes to DaphnisDuck's Music Player, `ddmus` for short, because omatunes is another player's name. The binary, the config and data folders, the config section (`[ddmus]`), the media-key name and the GitHub repository all changed. Settings and library move once, by hand: see [docs/ddmus/files.md](docs/ddsonic/files.md#moving-from-omatunes-before-v06).

## [0.5.0] - 2026-09-29

Released as omatunes. A cleanup release with no new sources.

### Changed
- Syncs are faster and quieter: a sync writes only what changed, and skips rereading saved albums and liked songs when their count and newest item are unchanged.
- The keymap shows only the keys that work.

### Fixed
- YouTube Music enrichment no longer stops for good at one track it cannot read, and a playlist whose sampled videos all refuse keeps its synced tracks.

## [0.4.0] - 2026-09-29

Released as omatunes.

### Added
- YouTube Music: your playlists and Liked Music sync into the catalog, signed in through browser cookies or your own Google OAuth client. Each track's artist, album and year is read in the background, giving YouTube Music its own Albums and Artists.

## [0.3.0] - 2026-09-29

Released as omatunes.

### Added
- Search as you type across Spotify, local files and radio stations, offline, with operators such as `artist:` and `source:`.
- All Music: every source's albums and artists in one list each.

## [0.2.0] - 2026-09-29

Released as omatunes.

### Added
- A local SQLite catalog of the Spotify library and the music folder, synced in the background, so browsing a large library is instant and works offline. `r` syncs by hand.

## [0.1.0] - 2026-09-29

Released as omatunes. The first release of the fork, based on cliamp just after its v2.3.0.

### Added
- Library navigation: Music → Spotify, Local, Radio and Search, with vim-style keys, above cliamp's player, queue, equalizer and visualizer.

### Removed
- cliamp's self-updater: `upgrade` refuses to run, because it would install cliamp over the fork.

[1.0.0]: https://github.com/DaphnisDuck/ddsonic/releases/tag/v1.0.0
[0.10.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/DaphnisDuck/ddsonic/releases/tag/v0.1.0
