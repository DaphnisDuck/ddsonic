# Library navigation

ddsonic opens on **Music**, a library hierarchy that sits above cliamp's playback chrome. Now playing, progress, volume, EQ and the visualizer stay where they were.

```
Music
├── All Music      Albums · Artists (every source together)
├── Spotify        Albums · Artists · Playlists · Liked Songs
├── YouTube Music  Albums from tracks · Artists from tracks · Playlists · Liked Music
├── Local          Albums · Artists · Genres · Folders · Playlists
├── Radio          Favorites · Browse Stations (cliamp radio · Countries · Genres & Tags)
└── Search
```

- **All Music:** Albums and Artists from Spotify, YouTube Music and Local in one list each, every row labelled with its source (YouTube Music's albums and artists as `YouTube · from tracks`). The same album or artist in both stays two rows, side by side. Opening a row goes to that source's own album or artist.
- **Spotify:**
  - **Albums** are your saved albums.
  - **Artists** are the artists you follow. Each one opens the albums of theirs the catalog knows, with no wait, and a **Full discography…** row that loads all their albums and singles from Spotify. An artist with no albums in the catalog opens the full discography directly.
  - **Playlists** are the ones you own or follow.
  - **Liked Songs** is your saved tracks.
- **YouTube Music:** your music playlists and Liked Music. **Albums from tracks** and **Artists from tracks** are read from those tracks in the background: saved YouTube Music Library Albums and followed artists are not synchronized, and an album there holds only the tracks of it you have. See [youtube.md](youtube.md).
- **Local:**
  - **Albums**, **Artists** and **Genres** come from an index of your music directory: `initial_directory` from config, else `$XDG_MUSIC_DIR`, else `~/Music`. Files are grouped into albums by album tag within a folder (per-disc folders like `CD1` stay one album); an album with several track artists is credited to Various Artists and listed under each of them.
  - **Folders** opens the file browser.
  - **Playlists** are your saved local playlists.
- **Radio:**
  - **Favorites** are your starred stations. Enter plays one.
  - **Browse Stations** lists the cliamp radio channels and the station directory by country or tag.
- **Search:** searches everything the catalog holds as you type; `/` opens it from anywhere. See [search.md](search.md). Without a catalog it is the provider's own search (Spotify if configured, else Local).

Lists sort A–Z as names are written ("The Planets" under T), ignoring case, accents and leading punctuation. Albums sort by title; press `o` to sort by artist instead. Radio favorites sort by name; stations under a country or tag keep the directory's most-voted-first order.

Enter on a track replaces the queue with the list it's in and starts at that track, the way an album plays. The library stays on screen.

## The catalog and sync

Spotify, YouTube Music and Local browsing read from a local catalog (`~/.local/share/ddsonic/library.db`), so lists open instantly and work offline; your radio stations are in it too, for search. Spotify and YouTube Music sync at startup when their last sync is older than their refresh setting, Local re-indexes changed files at every startup, and `r` syncs the source you're browsing. The header shows each source's status (`↻ syncing`, `✓ synced 2m ago`, `sync failed · cached`). See [catalog.md](catalog.md) for what's cached, failure safety, and the `[ddsonic]` settings.

## Keys

The bar at the bottom of each view lists every key that works there, and only those. It wraps onto more lines when the keys don't fit on one; `Ctrl+G` hides it. Keys that share a row are separated by spaces (`j k`, `[ ]`, `Esc h`); a `/` in the bar is always the `/` key, search (the filter, in the queue). A terminal too short for the whole bar keeps at least three rows for the list and shows the bar's first lines (the queue first takes rows from the visualizer); how the screen uses its size is in [layout.md](layout.md).

### Library

| Key | Action |
|---|---|
| `j` `k` / `↓` `↑` | Move (wraps) |
| `g` `G` / `Home` `End` | Top / bottom |
| `Ctrl+D` `Ctrl+U` / `PgDn` `PgUp` | Page down / up |
| `l` `Enter` `→` | Open, or play |
| `f` | On a station from the radio directory: add it to Radio → Favorites, or take it out |
| `h` `Esc` `Backspace` `←` | Back; at Music, nothing |
| `q` | Quit |
| `/` | Search |
| `r` | Sync the source you're browsing now (everything, at Music) |
| `o` | In an Albums list, switch between sorting by title and by artist |
| `Space` | Play / pause |
| `Tab` | Show the queue |
| `p` `n` | Previous / next track |
| `s` | Stop |
| `+` `-` | Volume |
| `Ctrl+G` | Hide or show the key bar |

`f` works on the highlighted station, whether or not anything is playing: in a country's or a tag's list, in the results of "Search the radio directory", in Favorites, and on a starred station among search results. The key bar shows `f Favorite`, or `f Unfavorite` when the station is already a favorite, and shows neither on a row that is not a directory station: a menu, a cliamp radio channel, a station from radios.toml, a track. A station is saved under the directory's name for it and its stream address. Favorites and search follow as soon as the change is saved: unstarred in Favorites, a station leaves the list. Saving happens in the background, so ddsonic stays responsive even while another ddsonic is writing the favorites file; until a station's change is saved the key bar shows no `f` for it and a second `f` on it does nothing.

On an error screen, Enter retries.

**Signing in to Spotify.** When Spotify needs you to sign in (the first time, after `ddsonic spotify reset`, or when Spotify has revoked the sign-in), the heading reads `Spotify sign-in required`, and Spotify's menu starts with **Sign in to Spotify**. Enter there, or on an empty Spotify list that reads "Sign-in required. Press Enter to sign in.", opens the sign-in in your browser; if no browser opens, the screen shows the address to open by hand. Its rows are one link to the whole address, for terminals that can open or copy a link. This fallback is best-effort: a terminal or multiplexer without link support may leave no usable way to take the address by hand, because selecting the rows picks up the line breaks between them; and below 80×20 not all of the address's text is on screen, though the link still carries all of it. The rest of the library stays usable meanwhile, and leaving the screen does not cancel the sign-in. When it succeeds, Spotify syncs at once and the lists fill when the sync ends; when it fails, the prompt stays and a line says why. What was cached before stays browsable throughout (`Spotify sign-in required · cached`). Spotify's own search and its playlist picker show the same prompt when a call is refused: Enter signs in, a search then runs again by itself, and a track you were adding to a playlist is not added until you ask again. A change you asked for stays with the sign-in that stood when you asked: if you sign in again before it starts or while it is under way (several tracks being added, a new playlist and its first track), what was done stays done, the rest is not sent, and the message says so. It says "was not added" only when that is certain; after a timeout or a lost connection it says the track may or may not have been added, and to check the playlist.

Three things about timing. If you ask to sign in while ddsonic is still setting Spotify up from the stored sign-in, the browser opens only once that attempt has ended, and not at all if it worked; Esc is not needed, and nothing is lost by waiting. While the browser sign-in is open, Spotify's sync says a sign-in is in progress instead of trying the stored sign-in beside it; the sync runs by itself as soon as you have signed in, also when an older sync was still running. And a track that was being opened at the moment its session was replaced by the sign-in fails with "the session changed while the track was opening": play it again. A track that was already playing keeps playing.

An empty Spotify list says which of four things it is: "Sign-in required. Press Enter to sign in."; "Syncing your library…" while the first sync runs; "Couldn't sync Spotify. Press r to retry." when the sync failed for another reason (the heading then reads `sync failed`, or `sync failed · cached` when there is a cache to browse); and "Nothing here." when the account has nothing in that list.

In search results, `/`, `Esc` and `h` return to the query; while typing, Enter or Tab moves to the results, `Esc` closes search and `Ctrl+U` clears the query. See [search.md](search.md).

### Queue

`Tab` shows the queue (Now Playing): the tracks playing, with the settings panel beside them. `Tab`, `Esc` or `b` returns to the library.

| Key | Action |
|---|---|
| `j` `k` / `↓` `↑`, `g` `G`, `PgUp` `PgDn` | Move, as in the library |
| `Enter` | Play the selected track |
| `Space` | Play / pause |
| `←` `→` | Seek 5 seconds; `Shift+←` `Shift+→` seek by the large step |
| `p` `n` | Previous / next track |
| `s` | Stop |
| `+` `-` | Volume |
| `/` | Filter the queue |
| `z` | Shuffle on / off |
| `r` | Repeat: off, all, one |
| `a` | Play the selected track next (again to take it back) |
| `A` | Up next: the tracks queued with `a` |
| `x` | Remove the selected track; `Ctrl+Z` undoes it |
| `f` | On a queued station from the radio directory: add it to Radio → Favorites, or take it out (a `★` marks a favorite, however the station was queued) |
| `Shift+↑` `Shift+↓` | Move the selected track up / down |
| `e` | Next EQ preset |
| `v` | Next visualizer (saved as `visualizer` in config.toml) |
| `[` `]` | Speed down / up |
| `i` | Track info, with the album's artwork where the terminal can draw it ([artwork.md](artwork.md)) |
| `y` | Lyrics |
| `Ctrl+J` | Jump to a time |
| `q` | Quit |
| `Ctrl+G` | Hide or show the key bar |

Track info, lyrics, Up next and Jump open over the queue; `Esc` returns to it.

`q` quits ddsonic at once from every library screen, the queue, and the overlays over it; only where you are typing (search, the queue filter, Jump) is it a letter. `Esc` always goes back one step (overlay → queue → Library, results → query → closed search, a list → its parent) and never quits. `r` means repeat in the queue and sync in the library.

Mono has no key while the library is enabled, and 1.0 removed `--mono` and `ddsonic mono`; `mono = true` in config.toml still turns it on, and the settings line shows `[M]` while it is.

The settings panel shows the queue's settings: **SRC** is the playing track's source (`[Spotify]`, `[YouTube]`, `[Local]`, `[Radio]`), then volume, EQ, shuffle, repeat and speed. It is display only; the keys above change it. For a track the library didn't start (a file or URL given on the command line, the file browser, a provider's own search), SRC shows what the track's path tells, or nothing.

## Disabled cliamp keys

Keys that jump to other parts of cliamp are disabled, including provider switching (`S`, `R`, `L`, `N`, …), the pickers (`t`, `Ctrl+V`, `d`), the full-screen visualizer (`V`), `u`, `w`, the playlist manager and the favorite-tracks playlist (their `p` and `n` are previous and next here; `f` stars radio stations, see above), Mono (`m`), Metadata (`Ctrl+I`, which terminals send as Tab) and the keymap overlay (`?`, `Ctrl+K`). The key bar replaces the overlay; `Ctrl+K` still opens it over cliamp's own screens.

Each view's keys come from one table in `ui/model/library_keymap.go`: the library's own keys, then the cliamp keys it passes through. The gate passes exactly the cliamp keys the table lists, and the key bar shows the same table, so a cliamp key works in a view if and only if its bar lists it. The library's own keys are handled in `handleLibraryKey` (`ui/model/library_nav.go`); a new one needs a row in the table too. To bring a cliamp key back, add a row to the view's table.
