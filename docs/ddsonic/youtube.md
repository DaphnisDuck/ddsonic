# YouTube Music

ddsonic syncs your YouTube Music playlists and Liked Music into its catalog, so they browse and search offline next to Spotify and your local files. In the background it also reads each track's real artist, album and year, which gives YouTube Music its **Albums from tracks** and **Artists from tracks** lists. Tracks play through yt-dlp, as in cliamp.

```
Music → YouTube Music   Albums from tracks · Artists from tracks · Playlists · Liked Music
```

**Saved YouTube Music Library Albums are not synchronized.** Albums from tracks groups the albums found in your Liked Music and synchronized playlists, and albums may be incomplete. An album you save at music.youtube.com does not appear in ddsonic unless one of its tracks is in Liked Music or a synced playlist. See [Albums from tracks](#albums-from-tracks-and-artists-from-tracks-enrichment).

**Followed YouTube Music artists are not synchronized.** Artists from tracks groups the artists found in your Liked Music and synchronized playlists. An artist you follow (subscribe to) does not appear unless one of those tracks credits them, and an artist you don't follow appears when one does.

## Setup

You need [yt-dlp](https://github.com/yt-dlp/yt-dlp) (it plays every track) and at least one way of signing in. Both are cliamp's own, configured in `~/.config/ddsonic/config.toml` under `[ytmusic]`, and you can use both at once.

### Browser cookies (no Google setup)

yt-dlp borrows the YouTube session of a browser you are signed in to:

```toml
[ytmusic]
enabled = true
# or chrome, chromium, brave, edge, opera, safari
cookies_from = "firefox"
```

On Linux, Chrome-family browsers encrypt their cookies with the desktop keyring. Name it after a `+`, and install the keyring support yt-dlp needs:

```toml
# or "chromium+kwallet", …
cookies_from = "brave+gnomekeyring"
```

On Arch, the GNOME keyring needs `python-secretstorage` (`sudo pacman -S python-secretstorage`). Without it yt-dlp reports `cannot decrypt v11 cookies` and YouTube treats you as signed out.

### yt-dlp's own configuration is not read

ddsonic runs yt-dlp with `--ignore-config` and `--no-mark-watched`, every time: to sync, to play and to save a track.

- No yt-dlp configuration file takes part: not `yt-dlp.conf` beside the yt-dlp executable or in the folder you started ddsonic from, not `~/.config/yt-dlp/config` and its other user locations, not `/etc/yt-dlp.conf`. They stay as they are and keep working for yt-dlp on its own.
- ddsonic asks yt-dlp for no watch-history updates (`--no-mark-watched`), and ignores yt-dlp's configuration files, so a `--mark-watched` in one no longer applies. A sync reads your account signed in, so it would otherwise mark every video it reads. This is as far as ddsonic's own request goes: `yt-dlp` is a separate program that ddsonic finds on your `PATH`, and it still loads any yt-dlp plugins installed on your machine. ddsonic trusts both to do what they were asked.
- Cookies come from `cookies_from` above, which ddsonic passes to yt-dlp itself. Cookies named in a yt-dlp configuration file (`--cookies`, `--cookies-from-browser`) are not used.
- A proxy set in the environment (`HTTPS_PROXY`, `ALL_PROXY`) still applies. A `--proxy` in a configuration file does not.
- Other options you may have set there, such as `--extractor-args` or a format, do not reach ddsonic's yt-dlp. ddsonic has no setting that passes extra yt-dlp options.

### The temporary copy of your browser's cookies

To use `cookies_from`, yt-dlp copies the browser's cookie database into a temporary folder, reads the copy, and removes it. That is yt-dlp's own way of reading a database the browser may have open, and it takes a moment at the start of each run.

- ddsonic gives each yt-dlp run a temporary folder of its own (`ddsonic-yt-dlp-…` in your temporary folder, readable by you alone) and removes it when the run is over, however it ended. The copy never sits in the shared `/tmp` itself.
- When ddsonic has to stop yt-dlp (Stop, leaving a list that is still loading, quitting during a sync, a timeout), it interrupts it first, as Ctrl+C would, so that yt-dlp removes its copy itself. Only a yt-dlp still running three seconds later is killed. Stop ends current playback and cancels pending playback loads. Skipping cancels an obsolete loading request promptly. If the outgoing track is already playing, it continues until the replacement is ready, then its process is closed. If the replacement fails, outgoing playback may continue until stopped, finished or replaced. Seeking in a YouTube track starts a second yt-dlp at the new position. Until that one has audio the sound is muted, and the first is kept, not played, so that a seek that fails can put you back where you were; a seek you overtake with another seek, or with Stop, ends its yt-dlp at once.
- ddsonic removes only the folders it made. It does not search the temporary folder or delete anything by its name.
- Quitting stops every yt-dlp that is still running, the same way: interrupted, killed after three seconds if it has not stopped, its folder removed. So quitting can take a few seconds while that happens, and **a track that was still being saved when you quit is not saved**. Save it again after starting ddsonic. Whether anything of the first attempt is reused is yt-dlp's and the source's affair: there may be no file yet (yt-dlp may have been stopped before the download began), a partial file may stay in your downloads folder, and yt-dlp starts over when the server cannot resume.
- If ddsonic cannot make that folder (the temporary folder is full or not writable), it does not run yt-dlp at all and says why. If it cannot remove the folder afterwards, it says so and names it, in the status line and the log, and the sync, list or save that used it is reported as failed: the folder may hold the copy. A folder that is still there when you quit is named again in the terminal after ddsonic exits.
- What this cannot cover: ddsonic itself being killed (`kill -9`, a crash, power loss) while yt-dlp runs. Nothing of ddsonic's runs then, and a yt-dlp that was reading the cookies at that moment may leave its copy in its `ddsonic-yt-dlp-…` folder.
- Versions before 1.0 could leave `yt_dlp…` folders holding `temporary.sqlite` in your temporary folder, and so can yt-dlp run by hand and stopped at the wrong moment. ddsonic does not remove them. They are copies of your browser's cookies: delete them.

### Your own Google OAuth client

The official YouTube Data API, through a free Google Cloud OAuth client you create once (about 10 minutes):

1. At [console.cloud.google.com](https://console.cloud.google.com/), create a project and enable **YouTube Data API v3**.
2. **OAuth consent screen** (Google Auth Platform): user type **External**, scope `https://www.googleapis.com/auth/youtube.readonly`, and add **your Google account as a test user**. Without it, sign-in fails with "Access blocked: … has not completed the Google verification process".
3. **Credentials → Create Credentials → OAuth client ID**, application type **Desktop app**. Copy the client ID and secret:

```toml
[ytmusic]
enabled = true
client_id = "…apps.googleusercontent.com"
client_secret = "…"
```

Then sign in once with `ddsonic youtube signin`, which opens your browser; approve read-only access (past "Google hasn't verified this app"). The token is kept in `~/.config/ddsonic/ytmusic_credentials.json`. While the consent screen is in **Testing**, Google may expire the sign-in after about a week; publishing the app avoids that.

To switch to another Google account, run `ddsonic youtube signin --force`: it opens Google's account chooser even when you are signed in, and replaces the stored token only when the new sign-in succeeds. The next sync replaces the old account's playlists and Liked Music.

## What syncs, and how

| | With cookies | With OAuth |
|---|---|---|
| Your playlists | yes (the youtube.com playlists feed) | often none: the API lists few or none |
| Playlists saved from others | only those listed in `youtube_playlists` | only those listed in `youtube_playlists` |
| Liked Music | yes | yes, with cleaner artist names |
| Saved Library Albums | no | no |
| Followed artists (subscriptions) | no | no |
| Cost | about 1.5 s per playlist per sync | quota: about 3 units per sync (10,000 a day) |

With both configured, ddsonic reads Liked Music through OAuth and playlists through cookies. When OAuth needs signing in again, Liked Music falls back to cookies.

- **Only music:** each playlist is sampled once and kept only if YouTube files its videos under Music. Watch later and Liked videos are never listed. The answers are cached in `ytmusic_classification.json`, so only new playlists take a few seconds on the next sync. A new playlist that cannot be sampled yet (it is empty, or every sampled video is blocked) is left out until a later sync can tell; one already synced stays, with its tracks.
- **Playlists saved from others:** YouTube lists playlists you save or bookmark from other people nowhere a sync can read. Add them by link (one line), and they sync as followed playlists:

  ```toml
  [ddsonic]
  youtube_playlists = ["https://music.youtube.com/playlist?list=PL…", "PL…"]
  ```

- **When:** at startup when the last sync is older than `youtube_refresh` (2 hours by default), and when you press `r` in YouTube Music.

  ```toml
  [ddsonic]
  youtube_refresh = "6h"
  ```

## Albums from tracks, and Artists from tracks (enrichment)

ddsonic does not read your YouTube Music library's Albums or the artists you follow: neither current ddsonic integration (browser cookies or OAuth) enumerates them. Spotify's Albums are your saved albums and its Artists the artists you follow; YouTube Music's **Albums from tracks** and **Artists from tracks** are a different thing, built from the tracks ddsonic does sync. Each list says so above its rows when space permits (a terminal too short to keep a usable list shows the list alone), and where sources mix (All Music and search) their rows are labelled `YouTube · from tracks`.

A playlist knows each track only as a video title and the channel that uploaded it. After each sync, ddsonic reads the tracks you have, one at a time in the background (about 4–5 s each, newest first), for their real title, artists, album and year. As it goes:

- YouTube Music's **Albums from tracks** and **Artists from tracks** lists fill in, and they join **All Music** and search.
- An album there holds the tracks of it you have in Liked Music and your synced playlists, not the whole album; opening it plays those.
- An artist there is one a track credits. Opening one shows the albums of theirs found in your tracks.
- Until a track has been read, its album and artists are not listed. A list with nothing read yet shows "No albums found in your tracks yet" or "No artists found in your tracks yet".
- A track with no music details (a fan upload, say) keeps its video title and uploader.
- A track yt-dlp keeps failing to read is skipped, so the rest are still read. After three passes that read other tracks but failed on it, it is left as it is (video title and uploader). A pass that reads nothing, or fails on several tracks in a row, looks like an outage: ddsonic stops and tries again after the next sync, without holding it against those tracks.

The first pass over a large library takes a while (about half an hour for 360 tracks); it pauses while YouTube syncs, resumes after a restart, and slows right down if YouTube asks it to (a "not a bot" check).

### A whole album: add it as a playlist

A YouTube Music album is also a playlist, whose ID starts with `OLAK5uy_`. Open the album at music.youtube.com, copy its link from Share (`https://music.youtube.com/playlist?list=OLAK5uy_…`), and add it to `youtube_playlists`:

```toml
[ddsonic]
youtube_playlists = ["https://music.youtube.com/playlist?list=OLAK5uy_…"]
```

What this does, and does not:

- It uses the existing playlist setting; nothing about it is specific to albums.
- When readable through your configured sign-in mode, the album is synchronized under **Playlists** with the tracks the existing playlist path can import.
- Its tracks are then read like any others, so the album usually also appears in **Albums from tracks**. That list groups by what each track's details say: a compilation whose tracks credit different first artists is split over several rows, and a track without album details is in none.
- It is not saved-album discovery. ddsonic still does not know which albums are in your YouTube Music library; you list each one yourself.
- Removing the album from your YouTube Music library does not remove it from ddsonic. It stays until you take its link out of `youtube_playlists`.

## Troubleshooting

- **No YouTube Music in Music:** check that `[ytmusic]` does not say `enabled = false`, that it has a sign-in (cookies or both OAuth keys), and that `yt-dlp` is on your PATH.
- **"sync failed" for YouTube:** the log (`~/.config/ddsonic/ddsonic.log`) has yt-dlp's message. A 401, or `cannot decrypt`, means the cookies are not being read (see the keyring note above).
- **yt-dlp works in a terminal but not in ddsonic:** ddsonic does not read yt-dlp's configuration files, so an option that makes it work there (cookies, extractor arguments, a proxy) is missing here. See [yt-dlp's own configuration is not read](#yt-dlps-own-configuration-is-not-read).
- **A saved playlist is missing:** add its link to `youtube_playlists`.
- **An album saved in YouTube Music is missing:** saved Library Albums are not synchronized, and pressing `r` or restarting does not change that. See [Albums from tracks](#albums-from-tracks-and-artists-from-tracks-enrichment).
- **An artist you follow in YouTube Music is missing:** followed artists are not synchronized either. An artist appears in Artists from tracks once a track in Liked Music or a synced playlist credits them.
