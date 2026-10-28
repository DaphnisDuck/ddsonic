# Album artwork

`i` in the queue opens the track's info. When the album has artwork and the terminal can draw images, the artwork shows beside the metadata. Nothing else in ddsonic shows artwork.

## Where it comes from

| Source | Artwork |
|---|---|
| Spotify | the album cover (300×300) the catalog stores with each saved album |
| Local files | the picture embedded in the file, else a `cover`, `folder`, `front`, `album` or `albumart` .jpg/.jpeg/.png in its folder |
| Any track with a cover URL (as MPRIS reports it) | that image |
| YouTube Music, radio, other streams | none yet |

Downloaded images are kept in `~/.cache/ddsonic/artwork` (`$XDG_CACHE_HOME/ddsonic/artwork`), named by a hash of their URL, so each is downloaded once; the folder is trimmed, oldest first, past 100 MB, and can be deleted at any time. Local artwork is read from the files each session.

## Terminals

Artwork uses the Kitty graphics protocol's Unicode placeholders: the image is sent to the terminal once and drawn as ordinary text cells, so it moves, resizes and disappears with the view and leaves nothing behind.

- **Kitty** and **Ghostty**: artwork shows.
- **Other terminals** (Alacritty, WezTerm, Konsole, the Linux console, …) and **tmux**: the info view shows the metadata alone, as before.

## Layout

The artwork sits left of the metadata and keeps its shape: its size in cells comes from the terminal's cell size in pixels (assumed twice as tall as wide until the terminal reports it). It takes at most half the width and leaves the metadata at least 32 columns; smaller than 8 rows, it is left out.

## Settings

```toml
[ddsonic]
# never show artwork (default: true, in terminals that can)
artwork = false
```

## For contributors

- `artwork/` resolves a `playlist.Track` to a `Ref` (a URL, an image file, or a local audio file) and loads it: the disk cache and download, embedded pictures and cover files, decoding, and scaling to at most 512 px. A new source is a new case in `Resolve` or `Loader.Load`.
- `artwork/` refuses images over 8,000 px a side before decoding them, reads at most 10 MB per image, drops a cached file that no longer decodes, and refuses redirects from https to http.
- `ui/kittyimg/` is the protocol: sending an image (as PNG, which keeps transparent covers' colors), giving it a virtual placement (removing the old ones first), deleting it, and the placeholder rows. Nothing else writes graphics sequences.
- `ui/model/library_info.go` runs it: opening `i` starts a background load, which also builds the sequence that sends the image, so the UI never encodes; results are kept for the session by artwork key (the view shows only the selected track's, so a late result never lands on another track). A hook at the end of `Update`, for the messages that can change the artwork's box (keys, resizes, the cell size, a finished load), sends the image once, places it anew when its box changes, asks for the cell size again after a resize, and frees the image before ddsonic quits; `main` frees it too when a signal ends ddsonic without reaching `Update`.
