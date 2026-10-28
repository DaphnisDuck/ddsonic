# Search

Press `/` anywhere in the library to search everything the catalog holds: your Spotify and YouTube Music libraries, your local music and your radio stations. Results appear as you type, and search works offline. On a library of 26,000 local tracks and 4,800 albums, typing a query took about 2 ms per keystroke typically and under 30 ms at worst (for two-letter prefixes such as "th").

## Keys

| Key | Where | Action |
|---|---|---|
| `/` | Anywhere in the library | Open search, or return to it, with your last query |
| Typing | Query | Search as you type; every key is text, including `j`, `q` and space |
| `Enter` `↓` `Tab` | Query | Move into the results |
| `Esc` | Query | Close search |
| `Ctrl+U` | Query | Clear the query |
| `/` `Esc` `h` | Results | Back to the query |
| `Enter` | Results | Open or play the highlighted result |

## Results

Results come in sections, best match first:

| Section | Shown | What |
|---|---|---|
| Artists | 5 | Followed Spotify artists, local artists, and artists credited on your albums and tracks. YouTube Music artists are always the credited kind, labelled `YouTube · from tracks`: followed YouTube Music artists are not synchronized |
| Albums | 8 | Saved Spotify albums and local albums, labelled with their source, and albums found in your YouTube Music tracks, labelled `YouTube · from tracks` (not your saved Library Albums, and possibly incomplete) |
| Tracks | 20 | Liked, playlist, cached-album and local tracks, each labelled with its source after its name (`Local`, `Spotify`, `YouTube`) |
| Playlists | 5 | Spotify playlists you own or follow |
| Stations | 5 | Your favorite radio stations first, then the built-in and `radios.toml` stations, labelled `Radio` |

A full section ends in **More…**, which lists up to 200 matches of that kind.

Opening a result works exactly as it does while browsing: an album opens its tracks, an artist their albums, a playlist its tracks. **Enter on a track plays its album from that track.** A Spotify album whose tracks aren't cached yet is fetched first. Offline, or for a track with no album, only the track plays. A station plays as a live stream. On a starred station, and on a station in the radio directory's results, `f` adds it to Radio → Favorites or takes it out; the results follow.

## Beyond your library

Search reads only the catalog, so it never waits on the network. Two rows at the end reach outside it when you choose them:

- **Search Spotify for "…"** (and the same for any source that can search live, such as YouTube Music signed in with cookies only) opens that service's own search with your query already running, in place of the library's results. The screen names the service and the query ("Searching Spotify for …", then "Spotify results for …"), since a recording you have locally is often on the service too and its rows carry no source label. There you can find music you haven't saved, and add it to a playlist. `Esc` goes back to the query, and `Esc` again to your library's results, as you left them. `Enter` on a track or an album replaces the queue and plays it, as the library does; `a` adds it to the end of the queue and `q` plays it next. Stopping playback while an album is still opening cancels it.
- **Search the radio directory for "…"** lists matching stations from the Radio Browser directory (about 58,000 stations).

## Query language

| Query | Finds |
|---|---|
| `bee sym` | Every word, as a prefix: "Beethoven Symphony No. 5" |
| `dvorak` | Accents are ignored: "Dvořák" |
| `"new world"` | The words in order |
| `artist:ozawa` | Only in the artist field (also `album:`, `title:`, `genre:`) |
| `album:"the planets"` | A phrase in one field |
| `source:local` | Only one source: `spotify`, `youtube`, `local` or `radio` (also `provider:`) |
| `type:album` | Only one kind: `artist`, `album`, `track`, `playlist` or `station` |

Everything must match. A single letter matches only a whole word: prefix matching starts at the second letter, where the results become useful. An operator you type half-way (`artist:`) is ignored until it has a value, and anything that isn't an operator is searched as text.

## Ranking

Within each section, a match in the title outweighs one in the artist, which outweighs one in the album, then the genre. Items in your library (saved albums, followed artists, liked and local tracks) rank above ones the catalog only knows of, and an exact title match ranks higher still. Among stations, favorites always come first.

## Without a catalog

If the catalog can't be opened, Search falls back to cliamp's provider search (Spotify if configured, else local files).
