# Changelog

Notable changes to ddsonic (DaphnisDuck's Music Player). The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Within 1.x, a newer ddsonic opens an older config and catalog; going back to an older ddsonic is not supported.

ddsonic is a fork of [cliamp](https://github.com/bjarneo/cliamp). Versions before 0.6 were released as omatunes, and 0.6 up to the candidate 1.0.0-rc.1 as ddmus; their entries below keep the name they were released under.

## [1.0.0] - not yet released

The first release for people who don't build from source. 1.0 adds no features to 0.10: it settles the command line, removes what ddsonic doesn't support, and ships a tested Linux build.

### Added
- A release build for Linux x86-64 with checksums, third-party notices, an SBOM, a build attestation and the complete corresponding source. An AUR package, `ddsonic-bin`, follows with the stable release.
- `ddsonic setup` sets up Spotify, YouTube Music and the Local music folder, and keeps every other line of an existing `config.toml`.
- `--version` answers in every build.
- A logo and an icon for ddsonic: a duck at a terminal. The release archive (in `share/`), the AUR package and `make install` carry a launcher entry, `ddsonic.desktop`, and the icon in eight sizes from 16 to 512 pixels, so ddsonic appears in application menus and opens in a terminal. The artwork is raster (PNG); its masters and layered sources are in `assets/branding`.
- The release archive and the AUR package carry the logo and the screenshot the README shows, beside it, so their copy of the README displays them.
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
- In "Search Spotify for …" and its YouTube Music twin, Enter replaces the queue, as playing from the library does.
- YouTube Music's "Albums" and "Artists" lists are now "Albums from tracks" and "Artists from tracks", and each says above its rows, when space permits, what it holds: the albums and artists found in your Liked Music and synced playlists (albums may be incomplete). Saved YouTube Music Library Albums and followed artists are not synchronized, and never were; the old names read as if they were. In All Music and search, these rows are labelled `YouTube · from tracks`. Nothing about what syncs has changed.
- By default, Spotify syncs at startup when its last successful sync is more than two hours old (it was 30 minutes).
- ddsonic names itself, not cliamp, in the HTTP requests it makes (radio streams, the station directory, lyrics): `ddsonic/<version>`.
- `shuffle` and `repeat` reject values they don't know.
- `ddsonic upgrade`, which has never updated ddsonic, now points to the package manager and the releases page.
- The executable is distributed under GPL-3.0, because it links go-librespot; ddsonic's own source stays MIT.

### Removed
- Lua plugins: none is loaded, and the `plugins` command and its IPC operations are gone. Plugin files and `[plugins.*]` config stay where they are, unused.
- `--daemon` (headless mode), `--mono`, `--expanded`, `--simplified`, and the `mono`, `open`, `protocol`, `qobuz`, `radio` and `tidal` commands. `cliamp://` links are neither registered nor handled; see [docs/ddsonic/files.md](https://github.com/DaphnisDuck/ddsonic/blob/main/docs/ddsonic/files.md#still-shared) if an earlier version (as ddmus or omatunes) registered them.
- The Logo visualizer, which draws cliamp's name. A config that names it starts with the default visualizer.
- cliamp's Nix flake, desktop entry, icons, website and install script, which built or installed cliamp. ddsonic installs from the release archive, the AUR package or source, and has a launcher entry and an icon of its own.

### Fixed
- Two quick volume changes from a desktop media control could freeze ddsonic; a dropped session bus could crash it.
- A stalled YouTube Music download froze every control until yt-dlp gave up.
- Opening a Spotify track on a stalled network froze ddsonic for up to 30 seconds.
- A Local playlist that lists a track more than once lost a copy on every save.
- A YouTube Music sign-in made inside ddsonic stopped working about an hour later, until a restart.
- A stalled Spotify request could hold a library sync until quit.
- A Spotify connection that stalled while starting up held every Spotify action behind it: a track stayed on "Buffering" and the sync never finished. Setting up the session now gives up after 30 seconds, and the next action tries again.
- Stop now cancels an album that is still opening from search.
- A sync requested while another ran could run twice, and a sync that had just finished could cancel the retry of a newer one that failed, leaving that source unsynced until the next refresh.
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

[1.0.0]: https://github.com/DaphnisDuck/ddsonic/compare/v0.10.0...main
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
