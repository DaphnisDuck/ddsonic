# Developing ddsonic

ddsonic is a fork of [cliamp](https://github.com/bjarneo/cliamp) (Go + Bubbletea) that turns the provider-oriented UI into a library-oriented one (Music → Spotify/Local/Radio/Search). GitHub Issues hold the backlog and decisions. [plan.md](../../plan.md) is the design record: read "Architecture invariants" before changing `library/`, the catalog, sync or provider capability detection, and "Vision" and "Non-goals" for scope questions.

## Fork rules

These keep upstream merges cheap ([upstream.md](upstream.md)):

- `upstream` is `https://github.com/bjarneo/cliamp.git`. Sync by merging upstream's latest reviewed release tag on a `sync/upstream-YYYYMMDD` branch; never merge `upstream/main`.
- The module path stays `github.com/bjarneo/cliamp`. The binary, UI branding and on-disk identity are ddsonic's, derived from `internal/appdir.Name` and `internal/appmeta` ([files.md](files.md)).
- New code goes in new files or packages (`library/`, `ui/model/library_*.go`, `external/spotify/library_browse.go`). Unavoidable edits to upstream files stay small and are tagged `// ddsonic:`.
- ddsonic docs live in `docs/ddsonic/`; upstream's `docs/` is not edited for ddsonic-only changes. ddsonic removed upstream's `site/`, Nix files and branding assets, and a sync keeps them deleted.

## What cliamp is

A TUI music player inspired by Winamp. Plays local files, HTTP streams, podcasts, and content from many providers: YouTube / YouTube Music, SoundCloud, Bilibili, Spotify, Xiaoyuzhou, Navidrome, Plex, Jellyfin, and a curated radio directory. Ships with a spectrum visualizer, 10-band parametric EQ, Lua plugin system, IPC remote control, and MPRIS/MediaRemote integration.

- Entry point: `main.go` → `run(...)` wires providers, player, playlist, Lua plugin manager, IPC server, and the Bubbletea program.
- CLI built with `urfave/cli/v3` in `commands.go`.

---

## Architecture map

Top-level layout (each subdirectory below is a Go package):

| Path | Responsibility |
|------|----------------|
| `main.go`, `commands.go` | Entry point, CLI definition, subcommands (IPC clients: play/pause/next/seek/…) |
| `config/` | TOML config load/save, CLI overrides, provider-specific config blocks |
| `player/` | Audio engine. Decoding (FFmpeg, yt-dlp), DSP pipeline, 10-band EQ, ICY, gapless, platform audio devices (`audio_device_*.go`) |
| `playlist/` | Playlist model, shuffle/repeat, M3U/PLS encoding, tag reading, queue navigation |
| `provider/` | `interfaces.go` + `types.go`: the `Provider` contract every source implements |
| `external/<name>/` | One Go package per provider: `local`, `radio`, `navidrome`, `plex`, `jellyfin`, `spotify`, `ytmusic` |
| `resolve/` | Expand user arguments (files, dirs, M3U/PLS, URLs, search queries) into playable tracks |
| `ui/` | Bubbletea view layer: visualizers (`vis_*.go`), global styles, tick loop |
| `ui/model/` | The Bubbletea `Model`: state, update, keymap, overlays, file browser, search, EQ, seek, notifications, providers. This is the biggest directory — always start here for UI behavior |
| `luaplugin/` | Gopher-Lua VM wrapper + sandbox + plugin APIs (`api_player.go`, `api_fs.go`, `api_http.go`, `api_message.go`, `api_crypto.go`, …). Plugin visualizers live here too |
| `plugins/` | First-party bundled Lua plugins (`now-playing.lua`, `auto-eq.lua`, …) |
| `pluginmgr/` | `cliamp plugins install/remove/list` CLI: resolves GitHub/GitLab/Codeberg sources and a direct URL |
| `ipc/` | Unix-socket IPC: `server.go` listens; `client.go` + `protocol.go` drive subcommands like `cliamp pause` from outside the TUI |
| `mediactl/` | MPRIS (Linux, dbus) + NowPlaying (macOS) integration. `service_linux.go`, `service_darwin.go`, `service_stub.go` for other OSes |
| `lyrics/` | LRC parsing / fetching |
| `theme/` | Theme loading, default theme, `themes/` subfolder |
| `internal/` | Shared helpers not meant for import by plugins: `appdir`, `appmeta` (version), `browser`, `control`, `fileutil`, `httpclient`, `playback` (message types like `NextMsg`, `PrevMsg`), `resume`, `sshurl`, `tomlutil` |
| `cmd/` | Subcommand implementations that the CLI wires up (e.g. playlist subcommands) |
| `upgrade/` | Self-update logic for `cliamp upgrade` |
| `tools/` | Developer tooling: pinned tools and preflight (`devtools.sh`), the source-build version (`version.sh`), the symbols gate (`sdkgate/`) |
| `docs/` | User-facing docs. Each feature has its own `.md`. Source of truth for keybindings, providers, plugin API |

### Runtime flow (read this before touching `main.go`)

1. `main()` builds the CLI (`buildApp()` in `commands.go`) and dispatches — most subcommands are thin IPC clients.
2. `run(overrides, positional)` is the TUI entry path:
   - Load config → apply CLI overrides.
   - Instantiate providers conditionally (radio + local always; navidrome/plex/jellyfin/spotify/ytmusic when configured).
   - Resolve positional args into tracks via `resolve/`.
   - Construct the `player.Player` (platform-specific audio device picked at compile time via build tags).
   - Build the Bubbletea `model.Model` with the player, playlist, providers, themes, and the Lua plugin manager.
   - Wire Lua state/control/UI providers (so plugins can read state and post `prog.Send(...)` messages).
   - Start the IPC server on a Unix socket.
   - Hand off to the media-control service (dbus / NowPlaying) which owns the event loop.

### Provider contract

All providers implement the interfaces in `provider/interfaces.go` (Browse, Tracks, Search where relevant). When adding a provider, add a package under `external/<name>/`, then register it in `main.go` behind a config check. Follow existing providers (Navidrome / Plex / Jellyfin) as templates — they share a lot of shape.

### Plugin surface

Lua plugins run in isolated `gopher-lua` VMs. Crashes are sandboxed. Hooks fire on playback events; plugins can register custom visualizers. When you add or change a plugin API, update **both** of:
1. `luaplugin/api_*.go` (implementation + tests)
2. `docs/plugins.md` (user-facing reference)
(Upstream also updates `site/index.html`; ddsonic has no `site/`.)

---

## Build, test, and local workflow

```sh
make build        # go build -trimpath with version ldflags → ./ddsonic (BINARY=cliamp for the upstream name)
make test         # go test ./...
make vet          # go vet ./...
make lint         # vet + staticcheck (if installed)
make fmt          # gofmt -l -w .
make check        # fmt + vet + test
make install      # installs binary into ~/.local/bin
```

- Go version: **1.27.4**, pinned in `mise.toml` (bumped deliberately; `go.mod` keeps upstream's minimum, 1.26).
- Linux build needs `libasound2-dev` / `alsa-lib` at build time.
- For audio at runtime on PipeWire or PulseAudio, install `pipewire-alsa` or `pulseaudio-alsa` (README, Troubleshooting).
- Optional runtime deps: `ffmpeg` (AAC/ALAC/Opus/WMA), `yt-dlp` (YT/SC/Bandcamp/Bilibili).

Tests are colocated with sources (`*_test.go`). Favor table-driven tests — the codebase already uses them heavily in `player/`, `playlist/`, `config/`, `ui/model/`, and `luaplugin/`.

Config lives at `~/.config/ddsonic/config.toml` (`DDSONIC_CONFIG_DIR` overrides; example at `config.toml.example`); plugins, `radios.toml` and themes live beside it. See `docs/ddsonic/files.md`.

---

## Conventions to follow

- **Package naming:** lowercase, single-word, matches directory. No internal suffix gymnastics — use `internal/` for genuinely private helpers.
- **Error handling:** wrap with `fmt.Errorf("context: %w", err)`. Surface user-facing messages from `main.go` / `run(...)` only.
- **Build tags:** platform-specific audio and media-control files use `*_linux.go` / `*_darwin.go` / `*_stub.go` suffixes — follow the existing pattern, don't invent new conditional-compile styles.
- **Bubbletea messages:** put shared message types in `internal/playback/` so UI code and non-UI callers (Lua, IPC) can both send them via `prog.Send(...)`.
- **Keep the docs in sync** on any user-visible change (keybindings, plugin APIs, providers, config keys): `docs/` for upstream-style behaviour, `docs/ddsonic/` for ddsonic's own, in the same change.
- **Don't add emojis** to code or docs unless the user asks for them.
- **Minimal diffs:** prefer editing in place over rewriting files. No speculative abstractions.

## Where to look first

| Question | Start here |
|----------|-----------|
| "How does the player decode X?" | `player/decode.go`, `player/ffmpeg.go`, `player/ytdl.go` |
| "How does the EQ work?" | `player/eq.go` + `player/eq_test.go` |
| "How is the UI laid out?" | `ui/model/model.go`, `ui/model/view.go`, `ui/styles.go` |
| "How do keybindings work?" | `ui/model/keymap.go`, `ui/model/keys*.go`, user-facing `docs/keybindings.md` |
| "How do I add a provider?" | `provider/interfaces.go` → copy `external/navidrome/` as a template |
| "How does IPC work?" | `ipc/protocol.go` (request/response types), `ipc/server.go`, `ipc/client.go` |
| "How are Lua plugins sandboxed?" | `luaplugin/sandbox.go`, `luaplugin/luaplugin.go` |
| "Where are bundled plugins?" | `plugins/` (first-party) |
| "What visualizers exist?" | `ui/vis_*.go` |
| "How is configuration resolved?" | `config/config.go` (load), `config/flags.go` (CLI overrides), `config/saver.go` (save) |
| "Why does my audio break silently on Linux?" | README, Troubleshooting (ALSA must reach the sound server: `pipewire-alsa` or `pulseaudio-alsa`) |

## Before a pull request

- `make check` (gofmt, vet, tests) while iterating; `make tools` once, then `make ci` for the full validation CI runs (about ten minutes). CI runs in full on every pull request.
- Keep the docs in sync with user-visible changes, in the same change.
- Avoid adding top-level dependencies casually: the `go.mod` list is intentional.
