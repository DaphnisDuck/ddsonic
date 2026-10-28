# The music catalog

ddsonic keeps your Spotify library and an index of your local music folder in one SQLite database, `~/.local/share/ddsonic/library.db`. The library screens read from it, so lists open instantly and keep working offline. Playback still goes to Spotify, or to the file itself.

## What it holds

- **Spotify:** saved albums, followed artists, liked songs, and the playlists you own or follow, with their tracks. Album track lists are cached as you open albums, and in the background for the rest of your saved albums.
- **YouTube Music:** your music playlists (and those listed in `youtube_playlists`) with their tracks, and Liked Music. Each track's artist, album and year is read in the background, which gives YouTube its Albums from tracks and Artists from tracks; an album holds only the tracks of it you have. Saved YouTube Music Library Albums and followed artists are not synchronized. See [youtube.md](youtube.md).
- **Local:** every audio file under the music folder, grouped into albums, artists and genres. The folder is `initial_directory` in `config.toml`, else `$XDG_MUSIC_DIR`, else `~/Music`.
- **Radio:** your favorite stations and the built-in and `radios.toml` stations, so [search](search.md) finds them. They are read from local files at every start and whenever you star or unstar a station. Browsing Radio still reads them directly.
- **Search index:** a full-text index of everything above, kept in step with every change. See [search.md](search.md).
- **Not in the catalog:** the radio directory, local playlists, and an artist's full Spotify discography. These are always loaded live. A Spotify artist page shows the albums the catalog knows at once; its **Full discography…** row loads the rest from Spotify when you open it.

No account passwords or tokens are stored in the catalog: Spotify and YouTube credentials stay in `~/.config/ddsonic/` (`spotify_credentials.json`, `ytmusic_credentials.json`). A radio station's URL is stored as written, so a station URL that carries a password or token puts it in the catalog. The catalog file and its folder are readable only by you.

## When it updates

| Source | At startup | On `r` | After a failure |
|---|---|---|---|
| Spotify | if the last successful sync is older than `spotify_refresh` (2 hours by default) | yes | retries after 1 minute, doubling up to every 30 minutes |
| YouTube Music | if the last successful sync is older than `youtube_refresh` (2 hours by default) | yes | the same retries |
| Local | always; only files whose size or modification time changed are read again | yes | the same retries |

A Spotify sync first asks for your saved albums' and liked songs' count and newest item (one request each). When both match the catalog, that list is not read again; it is still read in full at least once a day, so renamed titles and new artwork arrive. Playlists are refetched only when Spotify's version marker changed.

`r` syncs the source you're browsing. At the Music root it syncs every source. Syncs run in the background, and the screen refreshes in place when one lands, keeping the cursor on the same item.

**Spotify album tracks:** opening an album whose tracks aren't cached yet loads it from Spotify and caches it. A background fill then caches your other saved albums one at a time, newest saved first, one every 10 seconds. It pauses while you open an album or a sync runs, and when Spotify rate-limits it, it waits as long as Spotify asks (at least 5 seconds, doubling up to 10 minutes).

**Spotify rate limits:** Spotify can block a client for hours when it asks too much (seen: 20 hours on a development-mode client ID). While a block lasts, ddsonic sends Spotify nothing: syncs, the fill and album opens fail at once with "rate limited until" and the time, and a failed sync retries when the block ends. Short waits (30 seconds or less) are waited out, or retried by the fill, and not remembered. A longer block is kept in `library.db` (at most 48 hours), so restarting ddsonic does not ask Spotify again early.

## Status

The top of the library shows each source's state:

- `↻ syncing`
- `✓ synced 2m ago`
- `sync failed · cached`: the last attempt failed, and you're browsing what the catalog already holds.

With both Spotify and Local, each status is prefixed with its source. Details of a failure are in `~/.config/ddsonic/ddsonic.log`.

## Failure safety

A sync either applies completely or changes nothing:

- **Spotify:** if any page of a collection fails, or the library changes while it's being read, that collection keeps what it had.
- **Local:** if the music folder is missing, or is empty while the catalog holds files (an unmounted drive, for example), the index is left as it is. Files under a folder ddsonic can't read are kept too.
- **Sign-in:** a Spotify sign-in that fails because Spotify is unreachable or down is reported as that error, not as "sign-in required". The sync retries by itself. When the sync does need you to sign in (never signed in, reset, or revoked by Spotify), the heading reads `Spotify sign-in required` and Spotify's menu offers **Sign in to Spotify** ([navigation.md](navigation.md)); the catalog keeps what it had. A sign-in Spotify revokes while ddsonic runs is noticed at the next call; the stored credential is replaced only when you have signed in again.
- **Sign-in, two known limits in 1.0:**
  - Spotify hands out a new refresh token with some renewals, and ddsonic stores it only once the renewal has been accepted by a session that is still open. If you quit, or the work is cancelled, in the moment between Spotify issuing it and ddsonic accepting it, the stored sign-in is left as it was, and the next start may ask you to sign in again. The stored sign-in is never left half-written.
  - A session that a sign-in replaces is retired at once: nothing uses it any more. Its network connection is closed by the Spotify library, which can take a while when that library is in the middle of reconnecting (a network fault, say). Until then the old connection, and what belongs to it, stays in memory; several can pile up if you sign in repeatedly during such a fault. They go when the library finishes, or when you quit.

## Settings

In `~/.config/ddsonic/config.toml`:

```toml
[ddsonic]
# sync Spotify at startup if the last sync is older than this; "0s" syncs every time
spotify_refresh = "2h"
# the same for YouTube Music
youtube_refresh = "2h"
# other people's YouTube playlists to sync, by link (see youtube.md)
youtube_playlists = []
```

Keep each comment on a line of its own, as here: ddsonic does not read a comment that follows a value.

Durations use Go's format: `90s`, `30m`, `2h`. An invalid value keeps the default.

## Starting over

The catalog can always be rebuilt from Spotify and your music folder. To reset it, quit ddsonic and delete `~/.local/share/ddsonic/library.db`, along with the `library.db-wal` and `library.db-shm` files next to it. The next start syncs everything again.
