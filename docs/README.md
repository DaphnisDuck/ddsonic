# Documentation

ddsonic's own documentation is in [ddsonic/](ddsonic/); start with the [README](../README.md).

Every other page in this folder is **cliamp's documentation**, kept as it is upstream so that ddsonic can keep following cliamp. ddsonic is a fork: it shares cliamp's player, but not its interface or all of its sources, so these pages fit ddsonic only in part. They have not been rewritten for ddsonic. Wherever one applies, read `ddsonic` for `cliamp`, `~/.config/ddsonic` for `~/.config/cliamp`, and `ddsonic.sock` and `ddsonic.log` for `cliamp.sock` and `cliamp.log`.

## ddsonic's pages

| Page | What it covers |
| --- | --- |
| [ddsonic/navigation.md](ddsonic/navigation.md) | The library, the queue, and every key |
| [ddsonic/search.md](ddsonic/search.md) | Search and its query language |
| [ddsonic/catalog.md](ddsonic/catalog.md) | What the catalog stores, when it syncs, Spotify rate limits |
| [ddsonic/youtube.md](ddsonic/youtube.md) | YouTube Music setup |
| [ddsonic/artwork.md](ddsonic/artwork.md) | Album artwork and terminals |
| [ddsonic/layout.md](ddsonic/layout.md) | How ddsonic uses the window |
| [ddsonic/files.md](ddsonic/files.md) | Files and folders, running beside cliamp |
| [ddsonic/upstream.md](ddsonic/upstream.md), [ddsonic/releasing.md](ddsonic/releasing.md) | For contributors: following cliamp, cutting a release |

The other files in `ddsonic/` are records of how 1.0 was prepared.

## cliamp's pages that describe something ddsonic has

Each describes a part ddsonic shares with cliamp. The key presses they mention are cliamp's; ddsonic's keys are in [ddsonic/navigation.md](ddsonic/navigation.md).

| Page | In ddsonic |
| --- | --- |
| [spotify.md](spotify.md) | Creating your own Spotify Developer app applies. Browsing Spotify is the library's job: see [ddsonic/catalog.md](ddsonic/catalog.md) |
| [audio-quality.md](audio-quality.md) | The audio settings in `config.toml` apply |
| [configuration.md](configuration.md) | The player settings, custom radio stations (`radios.toml`) and the WSL2 notes apply. The sections on other providers and plugins do not. ddsonic's own settings are the `[ddsonic]` section: see `config.toml.example` |
| [themes.md](themes.md) | The theme files and settings apply. Choose a theme with `--start-theme` or `ddsonic theme`; the `t` picker is not in ddsonic |
| [playlists.md](playlists.md) | The playlist file format and the `ddsonic playlist` commands apply. The playlist-manager keys do not |
| [lyrics.md](lyrics.md) | Lyrics open with `y` in the queue |
| [history.md](history.md) | `ddsonic history` applies. In ddsonic the history is at Local → Playlists → Recently Played |
| [mediactl.md](mediactl.md) | Media keys and desktop controls apply; ddsonic's MPRIS name is `org.mpris.MediaPlayer2.ddsonic` |
| [remote-control.md](remote-control.md), [upgrading-ipc-v2.md](upgrading-ipc-v2.md) | The socket API applies, except the `mono` operation and the plugin operations, which ddsonic does not have |
| [cli.md](cli.md) | Many commands are the same, but `--daemon`, `--mono`, `upgrade`, `protocol`, `open` and `plugins` are not in ddsonic. `ddsonic --help` is the accurate list |
| [radio.md](radio.md) | The station directory and the cliamp radio channels are under Radio → Browse Stations. The radio pane, its keys and its location feature are cliamp's |
| [youtube-music.md](youtube-music.md) | The two ways of signing in are the same. ddsonic has no separate YouTube (non-music) source: read [ddsonic/youtube.md](ddsonic/youtube.md) instead |
| [streaming.md](streaming.md), [yt-dlp.md](yt-dlp.md), [ssh-streaming.md](ssh-streaming.md) | Playing a URL given on the command line. ddsonic inherits this from cliamp and does not test it; podcast feeds are not part of ddsonic |

## cliamp's pages that do not apply to ddsonic

ddsonic 1.0 does not include what these describe. The code may still be in the tree, unused, so that ddsonic can keep merging cliamp's changes.

| Pages | Why |
| --- | --- |
| [audiobookshelf.md](audiobookshelf.md), [emby.md](emby.md), [jellyfin.md](jellyfin.md), [lyrion.md](lyrion.md), [mixcloud.md](mixcloud.md), [navidrome.md](navidrome.md), [netease.md](netease.md), [plex.md](plex.md), [podcasts.md](podcasts.md), [qobuz.md](qobuz.md), [soundcloud.md](soundcloud.md), [tidal.md](tidal.md), [yandex.md](yandex.md) | Sources ddsonic does not offer. Its sources are Spotify, YouTube Music, local files and radio |
| [plugins.md](plugins.md), [community-plugins.md](community-plugins.md) | ddsonic loads no plugins |
| [headless.md](headless.md) | ddsonic has no headless mode |
| [url-scheme.md](url-scheme.md) | ddsonic neither registers nor handles `cliamp://` links |
| [keybindings.md](keybindings.md) | cliamp's keys. ddsonic's are in [ddsonic/navigation.md](ddsonic/navigation.md) |
| [provider-development.md](provider-development.md) | Written for cliamp's contributors |
