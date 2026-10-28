# ddsonic files and names

DaphnisDuck's Music Player (`ddsonic`) keeps its own files and runtime names, so it can be installed and run alongside cliamp.

| What | ddsonic | cliamp |
|---|---|---|
| Config (config.toml, playlists, history, resume, radio favorites, credentials, log, IPC socket) | `~/.config/ddsonic` | `~/.config/cliamp` |
| Data (music catalog `library.db`, embedded album art extracted for MPRIS) | `~/.local/share/ddsonic` | `~/.local/share/cliamp` |
| Downloaded album artwork for the info view (`artwork/`, trimmed past 100 MB; safe to delete) | `$XDG_CACHE_HOME/ddsonic`, else `~/.cache/ddsonic` | — |
| Default downloads (`Ctrl+S`) | `~/Music/ddsonic` | `~/Music/cliamp` |
| Launcher entry and icon (from the AUR package, `make install`, or the release archive's `share/`; remove them to uninstall) | `applications/ddsonic.desktop` and `icons/hicolor/<size>/apps/ddsonic.png`, under `~/.local/share` or, from the package, `/usr/share` | `cliamp.desktop` and cliamp's icon |
| MPRIS (media keys; its `DesktopEntry` is `ddsonic`, the launcher entry above) | `org.mpris.MediaPlayer2.ddsonic` | `org.mpris.MediaPlayer2.cliamp` |
| Client name and device ID reported to Plex, Navidrome, Emby, Jellyfin and podcast servers | `ddsonic` | `cliamp` |
| PulseAudio/PipeWire stream matched when switching output device | `ddsonic` | `cliamp` |
| Config override variable | `DDSONIC_CONFIG_DIR` | `CLIAMP_CONFIG_DIR` |

All of these come from `internal/appdir/ddsonic.go` (`Name`, `ConfigDirEnv`, `DownloadsDir`); `internal/appmeta` derives its client name from `appdir.Name`.

- **Config directory order:** `CLIAMP_CONFIG_DIR`, then `DDSONIC_CONFIG_DIR`, then `$XDG_CONFIG_HOME/ddsonic`, then `~/.config/ddsonic`. `CLIAMP_CONFIG_DIR` comes first because upstream's tests set it to isolate themselves, and exporting `DDSONIC_CONFIG_DIR` must not send them to your real config. So don't export `CLIAMP_CONFIG_DIR` globally if you run both players. Under `go test`, `DDSONIC_CONFIG_DIR` is ignored entirely, so tests never touch your real config.
- **File names:** the log is `ddsonic.log` and the IPC socket `ddsonic.sock` (with `ddsonic.sock.pid`). Before 1.0 they kept upstream's names, `cliamp.log` and `cliamp.sock`, and in `v1.0.0-rc.1` they were `ddmus.log` and `ddmus.sock`; a script that opens the socket by path needs the new name, and an old log can be deleted.
- **CLI subcommands:** `ddsonic pause`, `ddsonic next` and the rest talk to the ddsonic socket, so they control ddsonic and never cliamp.
- **`config.toml` must be valid TOML.** With no config file ddsonic starts on its defaults, and a valid file is read as before. A file that is not valid TOML stops ddsonic from starting: it prints the file, the line and column, and the parser's message, exits with an error, and leaves the file exactly as it was. It never starts on the defaults in place of a file it could not read. This checks syntax only: a key or section ddsonic doesn't know is still ignored, as before. What counts as malformed is what TOML says: a string or array left open, a line that is not `key = value`, a value without quotes that is not a number, `true` or `false` (`theme = Tokyo Night`), a key or section given twice. `ddsonic setup` still opens and edits such a file, and the remote commands (`ddsonic pause` and the rest) don't read the config at all.
  - **One exception, for now:** choosing an output device used to save `audio_device = <name>` without quotes, which is not TOML. That one line, exactly as ddsonic wrote it (at the top of the file before any `[section]`, `audio_device = ` and the device's name to the end of the line), is still accepted, so a config from an earlier version starts. ddsonic now writes the name in quotes; you can add them yourself. The exception will be removed in a later version.
  - **Valid TOML that is still read differently.** The check and the reader are two things. Three forms are valid TOML and pass the check, but the reader, unchanged from cliamp, does not read them as TOML means them: a comment after a `[section]` (the section is not seen), a comment after a value (it becomes part of the value), and an array written over several lines (only its first line is read). Keep each comment on a line of its own and each array on one line.
- **The socket's path has a short limit:** a Unix socket's path may be at most 107 bytes on Linux and 103 on macOS, counted in bytes, so a character outside ASCII takes two or more. `/ddsonic.sock` takes 13 of them, which leaves the config directory 94 bytes on Linux and 90 on macOS. That is a `DDSONIC_CONFIG_DIR` or `CLIAMP_CONFIG_DIR` of 94 bytes on Linux (90 on macOS), an `XDG_CONFIG_HOME` of 86 (82 on macOS), or a `HOME` of 78 (74 on macOS). With a longer path ddsonic does not start: it prints the two lengths, the path and how many bytes to remove, and exits with an error before it creates or reads anything. `ddsonic pause`, `ddsonic status`, `ddsonic vis list`, `ddsonic remote …` and the other commands that use the socket print the same message, not "ddsonic is not running". The socket has no second location; use a shorter directory. (If no home directory can be found at all, the socket goes to the temporary directory, and the message then names `TMPDIR`.) Commands that never use the socket (`setup`, `playlist`, `history`, `theme list`) are not affected. This matters mostly for test setups that put the config deep inside a scratch directory.

## Moving from ddmus (1.0.0-rc.1 and earlier)

Until `v1.0.0-rc.1` this player was called ddmus and kept its files under that name. ddsonic does not read, move or delete them: started without its own folders, it begins empty, with no settings, sign-ins or library, and the ddmus folders stay as they are. To carry everything over, quit ddmus and move the folders once:

```sh
mv -T ~/.config/ddmus ~/.config/ddsonic
mv -T ~/.local/share/ddmus ~/.local/share/ddsonic
[ -d ~/.cache/ddmus ] && mv -T ~/.cache/ddmus ~/.cache/ddsonic
sed -i -E 's/^[[:space:]]*\[ddmus\][[:space:]]*$/[ddsonic]/I' ~/.config/ddsonic/config.toml
```

- **Quit ddmus first.** `library.db` is an SQLite database with a write-ahead log (`library.db-wal`, `library.db-shm`); moving it while ddmus runs can lose its latest changes. `ddmus status` says whether one is running.
- **If you already started ddsonic once**, it created `~/.config/ddsonic` and `~/.local/share/ddsonic`. Remove them first: `mv -T` refuses to move onto a folder that holds anything.
- **The section header matters.** ddsonic reads `[ddsonic]` and ignores `[ddmus]` without a message, so `spotify_refresh`, `youtube_refresh`, `youtube_playlists`, `border` and `artwork` fall back to their defaults until the header is changed. The `sed` line does it, however the header is capitalised or indented.
- **What comes along:** `config.toml`, the Spotify and YouTube Music sign-ins, radio favorites, playlists, history, the resume position, themes, and the catalog with everything it has synced. Nothing inside these files names the config, data or cache folders, so no file needs editing but `config.toml`.
- **Leave `~/Music/ddmus` where it is.** Tracks saved with `Ctrl+S` went there, and the history, the resume position, playlists and the catalog remember each of them by its full path; moving or renaming the folder breaks every one of those. ddsonic saves new tracks to `~/Music/ddsonic`. To keep saving into the old folder instead, set it in `config.toml`, as a full path (`~` is not expanded here):

  ```toml
  [downloads]
  directory = "/home/you/Music/ddmus"
  ```

  If you do rename the folder, expect to fix those paths by hand or let them go: press `r` in Local to index the files again, and remove the stale entries from your playlists and history.
- **Leftovers to delete:** `~/.config/ddsonic/ddmus.log`, and `ddmus.sock` and `ddmus.sock.pid` if ddmus did not exit cleanly. ddsonic writes `ddsonic.log` and `ddsonic.sock`.
- **The variable** `DDMUS_CONFIG_DIR` is now `DDSONIC_CONFIG_DIR`. A script that opens the socket by path, or addresses the player over MPRIS as `org.mpris.MediaPlayer2.ddmus`, needs the new name.
- **The binary and the package:** remove the old `ddmus` executable (`~/.local/bin/ddmus` after a `make install`; `rm` it, or uninstall a locally built `ddmus-bin`). ddsonic installs as `ddsonic`, and its AUR package is `ddsonic-bin`.

To start over instead, do nothing: ddsonic starts empty, `ddsonic setup` sets up Spotify, YouTube Music and the Local music folder, and the ddmus folders can be deleted when you no longer want them.

## Moving from omatunes (before v0.6)

Until v0.6 this player was called omatunes. It does not read or move those files either, since `~/.config/omatunes` may belong to the unrelated omatunes player. The steps are the ones above with `omatunes` in place of `ddmus` (the section header was `[omatunes]`, the variable `OMATUNES_CONFIG_DIR`, and the log `cliamp.log`). Check first that the folders are this player's: they hold `config.toml`, `library.db` and a log.

## Moving settings from cliamp

ddsonic does not read cliamp's directory. To start from your cliamp settings, copy them once:

```sh
rsync -a --exclude='*.log' --exclude='*.sock' ~/.config/cliamp/ ~/.config/ddsonic/
rsync -a ~/.local/share/cliamp/ ~/.local/share/ddsonic/
```

## Still shared

- **Spotify sign-in:**
  - A copied `spotify_credentials.json` holds the same Spotify refresh token as cliamp's. If Spotify rotates the token for one app, the other asks you to sign in again. Run `ddsonic spotify reset` for a separate sign-in.
  - Both apps use the fixed OAuth callback port `127.0.0.1:19872`, so don't sign in to both at the same moment.
- **`cliamp://` links:** ddsonic 1.0 neither registers nor handles them; they are cliamp's. If you ran `protocol register` with a version before 1.0 (as ddmus or omatunes), it wrote `cliamp-url-handler.desktop` in `$XDG_DATA_HOME/applications` (usually `~/.local/share/applications`), the same file cliamp uses. Look at its `Exec=` line: if it runs ddmus or omatunes, delete the file and run `update-desktop-database ~/.local/share/applications`. If it runs cliamp, leave it. If you use cliamp, `cliamp protocol register` writes its own entry again.

## After an upstream merge

Upstream tests hard-code `cliamp` as a path segment under a temporary HOME. ddsonic rewrites those literals. If a merge brings in new ones, re-apply the rewrite:

```sh
files=$(grep -rl --include=*_test.go -E '"\.config", "cliamp"|"Music", "cliamp"|share", "cliamp"|\.config/cliamp' . | grep -v upgrade/)
sed -i -E 's/"\.config", "cliamp"/".config", "ddsonic"/g; s/"Music", "cliamp"/"Music", "ddsonic"/g; s/("share", )"cliamp"/\1"ddsonic"/g; s#\.config/cliamp#.config/ddsonic#g' $files
```
