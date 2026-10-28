# Upstream fix triage before 1.0

> **A dated record, kept as written.** The project was called ddmus when this triage was done and was renamed ddsonic on 2026-10-04, after `v1.0.0-rc.1`. `ddmus` below is ddsonic.

The pre-RC gate from the [release plan](release-plan.md): upstream's fixes checked against ddmus's code, without a full merge. Done 2026-10-02; two deferrals were reversed on 2026-10-03 after the Codex review.

- **Cutoff:** upstream `9d9e55ab` (2026-10-02). No release newer than v2.3.0 existed. ddmus's merge base is `4cef3a24` (v2.3.0 + 5).
- **Scope:** 271 fix commits. 62 touch only what ddmus no longer reaches: plugins (never loaded since CLI audit slice 2), the hidden providers, the daemon, `upgrade`, `cliamp://`, the radio globe. Of the remaining 209, subjects and messages picked the ones that could be a security problem, lose or corrupt data, or crash, hang or freeze; the rows below check each against ddmus's code.
- **Bar:** a fix is taken before 1.0 when the defect exists in ddmus, a ddmus user can reach it, and it is serious (a freeze, a crash, lost data, a security hole). Everything else waits for the 1.1 merge of upstream's next release, which brings all of them.

## Take before 1.0

| Commit | Area | Behavior | Applies (evidence) | Severity | How |
| --- | --- | --- | --- | --- | --- |
| `9556f9e2` | MPRIS | Two quick Volume changes from a desktop control can freeze the TUI: godbus runs the Volume callback holding its properties lock, the callback blocks in `prog.Send`, and the event loop needs that lock in `Update` | `mediactl/service_linux.go`: the callback calls `svc.send` synchronously; `Update` uses `SetMust` | HIGH | Cherry-pick (applies cleanly; `mediactl` and `player` race tests pass) |
| `23c66b70` | MPRIS | After the session bus drops, the next status, track or volume change panics in `SetMust` and ends ddmus, leaving the terminal raw | Same `SetMust` calls in `Update` | MEDIUM | Port by hand (conflicts: builds on later mediactl commits) |
| `70e5f79e`, `245e72e9` | Player, YouTube Music | A stalled yt-dlp download blocks the speaker lock in a pipe read; every tick reads the position under that lock, so the TUI freezes and no key, IPC or MPRIS command can skip or stop until yt-dlp gives up | `player/ytdl.go`: `buildYTDLPipeline` returns the bare pipe pipeline; `ytdlPipeStreamer` has no interrupt. ddmus already has `prefetchNetworkPipeline` | HIGH | `245e72e9` cherry-picks cleanly; `70e5f79e`'s core (wrap the yt-dlp pipeline in the live prefetch; seek from the prefetch's position) is ported by hand, without the 9 player refactors it sits on |
| `85a0f120` | YouTube Music | A sign-in from the library's sign-in screen builds the token source with the sign-in context, which is cancelled when the sign-in ends; the first refresh about an hour later fails until a restart | `external/ytmusic/session.go`: `newInteractiveSession` calls `conf.TokenSource(ctx, token)`; `initSession(true)` cancels ctx; `library/synced_catalog.go` offers the provider's `Authenticate` | MEDIUM | Port by hand (small) |
| `deb2f447` | Spotify | Web API and lyrics requests use `http.DefaultClient`, which never times out: a stalled connection holds a catalog sync until quit, and `r` is dropped while it runs | `external/spotify/session.go` `webApiWithBody` and `lyrics.go` use `http.DefaultClient` (ddmus bounded only the token requests) | MEDIUM | Port by hand (small)  The first port (2026-10-03) took the Web API and lyrics client and missed the commit's other half, "ensureSession stops after 30 s"; an owner's live check hit that hang on 2026-10-04 (Spotify stuck on Buffering), and the bound was ported then (`session_setup_ddmus.go`) |
| `095a56ec` (the stream open only) | Spotify | Opening a Spotify track runs on the UI goroutine: on a stalled network the TUI freezes for up to 30 s (the stream-setup timeout; longer with sign-in recovery), and no key, stop or quit works meanwhile | `ui/model/playback.go`: a Spotify track has `Stream=false`, so `playTrack` calls `PlayAt` synchronously, which reaches `SpotifyProvider.NewStreamer` | HIGH | Port by hand: `playTrack` starts a custom URI (`spotify:`) with the background play command streams already use; a stale session's `ErrNeedsAuth` still asks for sign-in. The rest of the commit waits (below). Raised by the Codex review, taken on the owner's decision (2026-10-03) |
| `5450f73c` | Local playlists | A playlist that lists a track more than once loses a copy on every later save (add, remove, save, update), with no second writer needed | `external/local/dirs.go`: `rebuildDoc` matched the caller's tracks onto the file's slots by path alone | HIGH | Port by hand (conflicts: upstream's sits on `306c8b93`): match by occurrence. The rest of the playlist fixes wait (below). Raised by the Codex review, taken on the owner's decision (2026-10-03) |

## Wait for the 1.1 merge

| Commit | Area | Behavior | Applies (evidence) | Severity | Why it waits |
| --- | --- | --- | --- | --- | --- |
| `095a56ec` (the rest) | Spotify | Sign-in rebuilds a stale session and a failed sign-in keeps the prompt; a stream that drops mid-track reconnects; clearer rate-limit and 403 messages; the add-to-playlist picker lists only writable playlists | Yes | LOW–MEDIUM | No freeze, crash or data loss once the stream open is taken (above); about 1,000 lines across 19 files |
| `306c8b93`, `06976869`, `8abba09a`, `a2ba51fe`, `877fa4f0` | Local playlists | A track inserted between two lands at the end; concurrent writers (TUI and `ddmus playlist`) can lose a change | Yes | LOW–MEDIUM | A misplaced track is not lost; the lost change needs two writers at once; the fixes touch the playlist store throughout |
| `7e70ce48`, `2354eda2`, `0d3d8bc6`, `763e0aac` | config.toml | Concurrent saves can lose one; a duplicate top-level key's second line wins over a save; key placement | Yes | LOW | Needs two writers at once or a hand-duplicated key. ddmus's setup writes with its own writer (see the CLI audit) |
| `0a1d3b44` | Signals | SIGTERM skips the exit save (resume position, pending EQ and speed); SIGHUP leaves the socket file | Likely | LOW–MEDIUM | Touches `main.go` and the headless path ddmus removed; a stale socket is replaced at the next start |
| `bbc3b4bb`, `d0de4eb8` | Local playback | An m4a, opus or wma file starts on the UI goroutine; a stalled mount or hung ffprobe freezes the TUI | Yes | LOW | Needs a stalled mount |
| `bd53b787` | IPC, playlists | A `provider_meta` key with a newline can inject lines into a saved playlist | Yes | LOW | The IPC socket is the user's own; no privilege boundary |
| `c9c3c085`, `57fe91fb`, `794ed930`, `18f20cce` | HTTP | No size cap on Radio Browser, now-playing and remote M3U bodies | Yes | LOW | Needs a hostile server; memory, not code execution |
| `d0056137` | YouTube Music | Denying access in the browser leaves the sign-in waiting its full 5 minutes | Yes | LOW | An inconvenience |
| `6b76878d`, `99b2be16`, `93475ccb`, `4889ba28` | Radio pins, history | Two ddmus processes can overwrite each other's pins or history; a failed pin save shows a pin that isn't saved | Yes | LOW | Needs two processes |
| `5747a3af`, `cb0166a9` | Player | Data races at the first start and on source registration | Yes | LOW | ddmus registers every source before the first play |
| `1c3bcd19` | YouTube Music | A silent check from another YouTube tab cancels a browser sign-in | No path: ddmus has no YouTube tab | — | — |

## Already fixed in ddmus, or not reachable

- `26482742` (Spotify session read under the lock): ddmus fixed it in its own review (`webAPIWithBody`).
- `d91f5eaa` (YouTube OAuth callback on every interface, empty state): ddmus binds 127.0.0.1 and checks a random state.
- `92f1c894` (unbounded YouTube token requests): ddmus bounds them (`tokenHTTPClient`).
- `e9062307` (MPRIS volume republish panic): the republish code isn't in ddmus.
- Plugin fixes (`luaplugin`, `pluginmgr`, Lua visualizers, the plugin trust fixes): no plugin loads (CLI audit slice 2).
- Hidden providers, the daemon and headless mode, `upgrade`, `cliamp://`: no entry point in ddmus.

For the 1.1 merge: the ported fixes are hand ports or cherry-picks, which give no merge ancestry. Where these files conflict, keep upstream's version unless ddmus's differs on purpose.
