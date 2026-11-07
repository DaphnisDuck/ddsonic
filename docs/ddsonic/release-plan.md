# ddsonic 1.0 release plan (proposal)

Oct 1, 2026 · @Steve

> **Renamed on 2026-10-04.** This plan was written and approved while the project was called ddmus, and `v1.0.0-rc.1` was published under that name. It still directs the work to 1.0, so it now uses the name 1.0 ships with, ddsonic, including in decisions dated before the rename.

ddsonic is feature-complete for 1.0. This plan proves, packages, documents and releases what is built. Nothing here starts until you approve it. Revised after Codex's independent review: its corrections are applied throughout.

## The plan at a glance

(Roadmap diagram, 4 phases and 4 gates: Gate A, clean baseline → release preparation → Gate B, ready for RC → v1.0.0-rc.1 and the RC audit → Gate C → v1.0.0 at Gate D. The diagram itself doesn't export to Markdown.)

Four phases, each opened by a gate you sign off. Release work starts only after `review-fixes` lands, and from rc.1 on only fixes go in.

## Now vs. later

Until `review-fixes` merges, only work that leaves that branch alone goes ahead: finishing its own items, and read-only research. Every change to code, docs or packaging waits for a fresh branch.

**Before `review-fixes` lands** (done 2026-10-01; merged to `main` as 9084b3a):

- [x] Codex verifies R7 (due 9:22)
- [x] Dependency refresh: low-effort Codex review
- [x] Your Kitty checks (listed under the release gate)
- [x] Decide on Codex's untracked review report. My recommendation: commit it under `docs/ddsonic/reviews/`, since `plan.md` cites it by finding number and it records why the fixes exist
- [x] Your approval, then commit, merge, push

**Safe meanwhile, read-only:** the CLI inventory, the license scan, and packaging research. They produce findings, not changes.

**Waits for the merge:** recording the freeze in `plan.md`, every CLI fix, removals, packaging, README, website, and fresh-user tests. All of that goes on a new `release-1.0` branch.

## What the brief overlooked

Eleven findings from inspecting the repository, four of them corrected by Codex. You decided the three that set the audit's scope on 2026-10-01: remove the hidden providers' entry points, `--daemon`, and `cliamp://` links.

| # | Finding | Why it matters for 1.0 |
| --- | --- | --- |
| 1 | **16 hidden providers are still public**: `--provider` lists them, `setup` offers 10 of them, `qobuz` and `tidal` have commands, and their docs ship in `docs/` | The biggest audit decision. Removing names from help isn't enough: config and IPC can still reach them. The README even promises they "come back one per release", which contradicts the freeze |
| 2 | **`--daemon` already exists**: cliamp's headless mode, inherited | The backlog calls daemon mode post-1.0, but the CLI advertises it now |
| 3 | **Unversioned builds hide `--version`**: `make build` injects the version and the flag works, but a plain `go build` has no version and the flag disappears | Make every build answer `--version` ("dev" at least), and test it |
| 4 | **`go install` by your repository path is unsupported**: the module path is `github.com/bjarneo/cliamp` | Renaming the module would make upstream merges costly; don't offer it |
| 5 | **Version tags**: this clone holds upstream's `v2.0.1`–`v2.3.0` tags; your GitHub has only `v0.x` | Pushing all tags at once would make "latest" `v2.3.0`, not `v1.0.0`. Push single tags only |
| 6 | **cliamp's identity is still in the repo**: `CNAME` (www.cliamp.stream), `site/` (cliamp's website, and an `install.sh` that downloads cliamp), the Nix flake (builds this checkout under cliamp's name), `.desktop`, icons, a Windows `.rc`, and an MP3 | Pages and the install script would publish or install cliamp under your name |
| 7 | **`cliamp://` links**: the `open` and `protocol` commands register cliamp's URL scheme | Would collide with an installed cliamp |
| 8 | **Setup is cliamp's wizard**: it offers 12 providers (10 besides Spotify and YouTube Music) and never sets up Local. It writes to ddsonic's own folder, but its help names cliamp's path | It is the onboarding path; today it onboards cliamp's providers |
| 9 | **License**: MIT, copyright line only Bjarne Øverli; the binary links GPL-3.0 code | See licensing below: settle it before any public binary, the RC included |
| 10 | **Only Linux has been exercised**: macOS and Windows code is inherited and untested by us | Decides which platforms 1.0 can honestly claim |
| 11 | **Native libraries**: besides ALSA, the binary links FLAC, mpg123 (Spotify playback) and Vorbis/Ogg through cgo | Build and runtime prerequisites, and part of the GPL source obligation |

Also worth deciding: what 1.0 promises about config and catalog compatibility, and whether to add `SECURITY.md`, `CONTRIBUTING.md` and issue templates.

## Workstream 1: CLI audit

`commands.go` registers 33 root commands (two of them hidden, plus the framework's `help`; about 50 subcommands) and 25 global options, almost all inherited. My first-pass recommendation is below; each line becomes a verified decision during the audit.

| Group | Commands and options | First-pass recommendation |
| --- | --- | --- |
| Remote control | `play` `pause` `toggle` `next` `prev` `stop` `status` `volume` `seek` `load` `queue` `shuffle` `repeat` `speed` `eq` `device` | Verify each against a running ddsonic; keep what works |
| UI-removed controls | `mono`, `--mono` (M8 dropped mono from the UI) | Remove, or keep as CLI-only and document |
| Scripting | `remote` (IPC v2), `visstream`, `--daemon` | Keep `remote`; remove `--daemon` (decided; finding 2) |
| Library and history | `playlist` (11 subcommands), `history`, `--playlist` | Verify; keep |
| Accounts | `youtube signin`, `spotify reset` | Keep |
| Hidden providers | `qobuz`, `tidal`, most of `--provider`, 10 of `setup`'s 12 providers, the provider-switching hotkeys | Remove the public entry points: help, setup, config activation, IPC and hotkeys (decided; finding 1). Leave existing users' config intact |
| Self-update | `upgrade` (hidden; already refuses with an error, tested), the unused `upgrade/` package | Remove the stub and the package, or keep the refusal with advice that fits every install method (its message says `git pull && make install`, wrong for AUR and release installs) |
| cliamp links | `open`, `protocol` (`cliamp://`) | Remove (decided); a `ddsonic://` scheme would be a new feature |
| Plugins | `plugins` (install, trust, call…) | Remove (decided): the command, help, plugin loading at startup (`main.go` initializes plugins from config) and the IPC plugin operations (`plugin.call`, `plugin.commands`). Existing plugin config stays inert |
| Appearance and audio flags | `theme` `vis` `--start-theme` `--visualizer*` `--simplified` `--expanded` `--help-bar` `--low-power` `--sample-rate` `--buffer-ms` `--bit-depth` … | Verify each still means something under the library UI |
| Missing | `--version` | Add (finding 3) |

**Environment variables users can set:** `DDSONIC_CONFIG_DIR`, `CLIAMP_CONFIG_DIR` (upstream's test isolation), the XDG directories, proxy variables, and `NAVIDROME_*`/`LYRION_*` (hidden providers). The many `CLIAMP_LIVE_*` variables are test-only and stay out of the docs.

**Procedure**

1. **Freeze the surface in a test.** A golden-file test walks the CLI definition and pins every command, alias, option and default. Help prose stays out of it, so wording fixes don't churn the test; behavior gets its own tests. After 1.0, any change to the public surface shows up in review.
2. **One audit row per item**, answering five questions: does it work, is it still relevant, is it tested, is it documented accurately, does it carry cliamp assumptions. Each row ends in one of your four categories: verified and supported; fixed and supported; intentionally retained and documented; removed before 1.0.
3. **Test behavior, not exit codes.** Run each command in an isolated environment (see fresh-user tests for what that takes), and IPC commands against a live ddsonic in tmux on its own socket. Check what changed, the message and the exit code, including invalid commands, flags and values.
4. **Setup as onboarding**, against each scenario from the brief: fresh, existing, rerun, missing credentials, provider, local-only, Ctrl+C, invalid input, malformed config, unwritable paths. Setup should configure what ddsonic uses: Spotify, YouTube Music and the Local folder.
5. **One reference**: `docs/ddsonic/cli.md` documents the final surface, and the help texts match it.
6. **Review**: one medium-effort Codex review of the resulting change set.

## Workstream 2: packaging, distribution and licensing

I propose three ways to install 1.0, all on Linux x86-64, because that is the only platform we can test: a GitHub Release (the canonical source), an AUR package, and building from source. Everything else waits until someone can test it.

| Channel | 1.0? | Why | Test it ourselves? | Upkeep |
| --- | --- | --- | --- | --- |
| GitHub Release, Linux amd64 `.tar.gz` + `SHA256SUMS` | **Yes, canonical** | One artifact with checksums, notices and the complete corresponding source beside it, built by CI from the tag; states the tested distributions | Yes | Low |
| AUR `ddsonic-bin`, from the release | **Yes** | You run Arch; pacman handles upgrade and uninstall. One package only, with you as named maintainer; RCs are tested from a local recipe, and the AUR updates only for stable releases | Yes | Low: a version bump per stable release |
| Build from source (`make install`) | **Yes, documented** | Already how ddsonic is used; documented as one pinned, tested build path with every native prerequisite | Yes | None |
| Linux arm64 | Later | Builds with a cross toolchain, but we have no arm64 machine | No | Medium |
| macOS (amd64, arm64) | Later | Code inherited, never run by us; needs a macOS runner | No | Medium |
| Homebrew | Later | Only sensible once macOS is supported | No | Medium |
| `go install`, plain `go build` | No | Unsupported for this fork while the module declares upstream's path (finding 4): by repository path Go refuses the install, and in a checkout both name the executable `cliamp`. `make build` and `make install` are the source build | — | — |
| `.deb`, `.rpm` | No | Easy to generate, but we can't test them | No | Medium |
| Flatpak, Snap | No | Sandboxing fights a terminal app that needs ALSA, `yt-dlp` and the browser cookie stores | No | High |
| Windows | No | Untested; upstream supports it, ddsonic doesn't claim to | No | High |
| Nix flake (in the repo) | Removed (2026-10-04) | It built this checkout, but as `cliamp` (package, executable and desktop names), and nobody tested it | No | — |

**What a Linux install involves**

- **Build:** Go 1.27.3 with cgo, `pkg-config`, and the headers for ALSA, FLAC, mpg123, Vorbis and Ogg. The build runs in one pinned environment (a container image), not on whatever machine is at hand.
- **Runtime:** the shared libraries for ALSA, FLAC, mpg123, Vorbis and Ogg, unless the release links some statically (a decision for the packaging work), plus `pipewire-alsa` or `pulseaudio-alsa`. Optional: `ffmpeg` (AAC, ALAC, Opus, WMA) and `yt-dlp` (YouTube Music). The release states the distributions and glibc baseline it was tested on.
- **Paths:**
  - binary: `/usr/bin/ddsonic` (AUR) or `~/.local/bin` (manual)
  - config: `~/.config/ddsonic`
  - data: `~/.local/share/ddsonic`, with `library.db`
  - cache: `~/.cache/ddsonic`, with artwork
  - downloads: `~/Music/ddsonic`
- **Upgrade:** replace the binary. The catalog migrates forward when opened, and an older binary refuses a newer catalog.
- **Uninstall:** removing the package leaves config, data and cache. The docs list them for a full clean-up.

**Release automation.** Replace upstream's `release.yml` with a ddsonic one. Pushing a `v*` tag builds Linux amd64 in the pinned build environment, then attaches the archive, `SHA256SUMS`, the notices, the corresponding source and the notes from `CHANGELOG.md`. Prerelease tags (`-rc.N`) publish as GitHub prereleases and never update the AUR or the website. The workflow's Go comes from `go.mod` today (1.26.6); it must use 1.27.1, as `mise.toml` pins. Reproducibility is checked inside that pinned environment: `-trimpath`, pinned Go and `SOURCE_DATE_EPOCH` alone don't pin the C compiler, the linker or the native libraries. I suggest the existing shell workflow over GoReleaser, a new tool to learn for one target. This needs Actions turned on for your fork. Push single tags only (finding 5).

**Licensing and hygiene** (my reading, not legal advice):

- **When:** settled before any public binary, the RC included, not just before 1.0.
- **ddsonic's source** stays MIT. Keep Bjarne Øverli's copyright line and add yours.
- **The executable is GPL-3.0** as a combined work, because it links go-librespot. The README and release say so plainly, rather than calling the shipped product simply "MIT".
- **What GPL-3.0 asks for each binary:** the GPL text, clear license notices, and the complete corresponding source for that exact build beside the download. That means ddsonic's source plus the Go module and native-library sources used and the build scripts, not just a link to the tag. Byte-identical rebuilds are not required; that is a separate engineering goal.
- **Native libraries** count too: mpg123 (LGPL-2.1), FLAC, Vorbis and Ogg (BSD-style), ALSA (LGPL). A Go-module scan misses them.
- **Dependencies** are otherwise permissive: 30 MIT, 20 BSD, 18 Apache-2.0 and a few public-domain packages. Apache-2.0 asks us to pass on its NOTICE files.
- **`xlab/vorbis-go` has no license file** at the pinned revision. It is auto-generated bindings to libvorbis (BSD), pulled in by go-librespot's decoder and already shipped in every cliamp binary. Decided 2026-10-01: accept this inherited risk as upstream does, and name it in `THIRD_PARTY_NOTICES`. The release-gate verification still looks for an applicable grant, including the generated code's provenance (c-for-go output from libvorbis headers); if none is found, the release record says permission remains unresolved, and the release ships with that risk accepted (owner's decision, 2026-10-01, made knowing that upstream's use and a notice don't amount to permission).
- **Notices:** a `THIRD_PARTY_NOTICES` file in each archive, generated from the shipped dependency graph, native libraries included.
- **Assets:** check the inherited icons, the MP3 and `site/` images before shipping, the MP3 especially. Album art is fetched at runtime, never bundled.
- **Version:** every build answers `--version` (finding 3).
- **Changelog:** a hand-written `CHANGELOG.md` (Keep a Changelog style), seeded from `plan.md`'s milestone history.
- **SBOM:** a minimal generated SBOM (SPDX or CycloneDX, generated in CI) attached to every release, native libraries included. Decided 2026-10-01.

**Trust and maintenance** (decided 2026-10-01, with the AUR package):

- The README and docs name the official project and its GitHub Releases as the official binaries, and ask forks to use another name.
- Releases carry `SHA256SUMS`, the SBOM and a GitHub build-provenance attestation (`gh attestation verify`); `ddsonic-bin` pins each release's checksum.
- A "stepping away" section: if the owner stops maintaining ddsonic, the repo is archived with a notice and `ddsonic-bin` goes to a named co-maintainer or a deletion request, never just orphaned. Linked from the AUR page.
- Two-factor authentication on GitHub; a dedicated passphrase-protected SSH key for the AUR.
- `SECURITY.md`: how to report a vulnerability privately, and what response to expect.

## Workstream 3: README refresh

Today's README reads as a development diary. Its first screen is the fork's history and a per-version changelog, it says "Version 0.6", and it promises providers that 1.0 won't have. Rewrite it for a first-time visitor, and move the history into `CHANGELOG.md`.

**First screenful, in order**

1. Name and a one-line thesis. Your "library, not services" idea, worded only after you approve it.
2. One screenshot: the Track Info view in Kitty with a track playing, album artwork, the spectrum visualizer and the EQ (owner, 2026-10-05; it replaces "the library with a search open and artwork showing"). Taken from a clean committed build, so the header shows no `-dirty` version. Search, the queue and navigation are left to the demo. In the README since 2026-10-05: `docs/ddsonic/images/library.png`, from a build of `2bcd0e5`.
3. Three or four lines on why it is different: one library across Spotify, YouTube Music, local files and radio, keyboard-first, and fast because it reads a local catalog.
4. Install: the AUR line and the release download, two commands at most.

**Then:** a feature list (only claims verified against current behavior), sources and what each needs, a short first-run walkthrough, configuration basics, links to `docs/ddsonic/`, credit to cliamp, license.

**Claims to verify before they go in**

- [ ] "Instant and offline" browsing: time a cold start against a real catalog
- [ ] Search operators: list exactly the ones that work
- [ ] Background sync: what syncs, and how often
- [ ] Artwork: name only terminals we have tested (Kitty; Ghostty as claimed today)
- [ ] Radio and Local: work with no accounts at all

**Visual demo.** A static screenshot heads the README, and a short animated WebP or GIF (15 seconds or less: browse, search, play) sits further down. I'd record with `vhs`, a scripted terminal recorder: the script lives in the repo, so the demo can be re-recorded identically for each release. Neither vhs nor asciinema can draw Kitty images, so the artwork screenshot is taken in a real Kitty window.

**Docs:** `docs/ddsonic/` becomes the documentation. Most of upstream's `docs/` covers hidden providers; the README stops linking to it, and the website ignores it.

## Workstream 4: website

The smallest useful site is one hand-written HTML page on GitHub Pages, no generator, published from the repo's `site/` folder by the existing `pages.yml`. It reuses the README's screenshot, demo and install lines, and links into `docs/ddsonic/` on GitHub for everything deeper.

- **One page:** what ddsonic is, the demo, features, supported sources, install, a keys cheat-sheet, three or four FAQ items (audio silent on PipeWire, YouTube sign-in, artwork terminals), and links to releases and issues.
- **Replace, don't adapt, cliamp's site:** `site/` today is cliamp's page and `install.sh`, and `CNAME` points at www.cliamp.stream. Delete the CNAME and serve from `daphnisduck.github.io/ddsonic` unless you want a domain of your own.
- **No install script:** AUR and the release download cover 1.0, and a `curl | sh` installer is one more thing to secure and test.
- **Deployment trigger:** replace `pages.yml`'s trigger, which deploys after any successful Release run (release candidates included), with a deploy from the exact approved stable tag. Removing its upstream-only guard alone would publish RCs to the site.
- **Upkeep:** the page changes only at a release. The release checklist includes "site matches the README".
- **Timing:** decided 2026-10-01: the site is live no later than the v1.0.0 release (confirmed 2026-10-05; it does not hold up `v1.0.0-rc.3`). It is built last among the docs, after the README, so the two say the same things. Removing cliamp's site, `CNAME` and install script is separate cleanup and happens regardless.

## Workstream 5: fresh-user tests

Each scenario starts from the published README and docs only. Whenever the tester needs something the docs don't say, that is a bug in the docs. Isolation needs more than a scratch `HOME`: unset `DDSONIC_CONFIG_DIR` and `CLIAMP_CONFIG_DIR`; point `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` and the music folder into the scratch home (artwork honours `XDG_CACHE_HOME`); keep `XDG_RUNTIME_DIR` and the rest of the desktop session so audio works; and give ddsonic its own IPC socket. Don't run your everyday ddsonic beside a test instance: both would claim the same MPRIS name. Installs are tested in a clean container or VM, so missing dependencies show up; playback needs a real terminal and audio session. E and F need you and a real account.

| # | Scenario | Setup | Passes when |
| --- | --- | --- | --- |
| A | Local only | Clean `HOME`, a folder of tagged music, no accounts | Install, launch, Local's albums appear, a track plays, search finds it, queue and track info work |
| B | Radio only | Clean `HOME`, no music folder | Radio favourites and the directory work, a station plays |
| C | Missing helpers | Without `ffmpeg` and `yt-dlp` on `PATH` | Missing pieces are explained, not silent: AAC files, YouTube Music |
| D | No artwork support | A terminal without Kitty graphics | Track info shows metadata alone; no escape codes leak |
| E | Spotify | Your account, following the docs | Sign-in, first sync, browsing offline afterwards, playback |
| F | YouTube Music | Cookies or OAuth, following the docs | Playlists and Liked Music sync, a track plays |
| G | Upgrade | Your real v0.10 config and catalog, copied after ddsonic has shut down (or through an SQLite backup), never while its WAL may be live | 1.0 opens them, the migrations run, nothing is lost |
| H | Setup | Fresh, then rerun, then Ctrl+C mid-way | The config written is what ddsonic reads; a rerun keeps existing answers |

A to D and G run after the README and docs are written and before the RC. E, F and the artwork checks go on your list. I suggest a Codex low-effort review afterwards of the docs only: does a stranger have enough?

## RC audit and release gate

Each gate is a checklist; the next phase starts only when every box is ticked or explicitly waived by you, with one exception: the box for [the adversarial security audit before `v1.0.0`](#before-v100-the-adversarial-security-audit) may not be waived. Only the RC audit gets a high-effort comprehensive Codex review; ordinary fixes stay low or medium.

**Gate A: clean baseline** (end of `review-fixes`)

- [x] R7 verified by Codex; dependency refresh reviewed
- [x] Steve's Kitty check: interrupt an album load with Stop or `n`
- [x] Steve's Kitty check: the info view while the queue changes
- [x] Steve's Kitty check: an artist page (cached albums first, Full discography…)
- [x] Steve's Kitty check: the TUI after the renderer update (resize, queue, info view)
- [x] Review report filed; `plan.md` current
- [x] You approve; commit, merge and push

**Gate B: ready for RC** (end of release preparation)

- [ ] Freeze recorded in `plan.md`; backlog moved to its post-1.0 section
- [x] Your decisions taken (see "Decisions that are yours" below)
- [ ] CLI audit complete: every item in one of the four categories, golden-file test of names and options in place, every build answers `--version`
- [ ] Hidden-provider, daemon and `cliamp://` decisions implemented, including config, IPC and hotkey entry points; `upgrade` resolved
- [ ] Release-hardening checks of inherited behavior ddsonic keeps, each with a regression test or your waiver: cliamp's Spotify search ("Search Spotify for …") appends where library playback replaces the queue (decided 2026-10-01: Enter on an album or track replaces the queue; `a` and `q` keep appending and queueing next), and Stop may not cancel its album load (fixed either way)
- [ ] Setup onboards ddsonic (Spotify, YouTube Music, Local)
- [ ] User-facing branding and distribution identity replaced (repo, site, Nix, install script), keeping cliamp's attribution and copyright and the intentionally retained module path
- [ ] Packaging matrix (approved 2026-10-01) implemented
- [ ] Licensing settled before any public binary: GPL-3.0 presentation, corresponding source beside each download, `THIRD_PARTY_NOTICES` including native libraries and the unlicensed `vorbis-go`
- [ ] Release workflow produces archive, checksums, notices, source and notes from a test tag, in the pinned build environment
- [ ] AUR recipe builds and installs from a test release
- [ ] README, `CHANGELOG.md` and `docs/ddsonic/` done; website drafted (live no later than v1.0.0)
- [ ] Fresh-user scenarios A–D, G and H pass in isolation and a clean container or VM; E and F pass with you
- [ ] Upstream fix triage (decided 2026-10-02): upstream's fix commits up to the cutoff: upstream's latest release tag if one newer than v2.3.0 exists when the session starts, else `9d9e55ab` (2026-10-02); the triage doesn't wait for a release, checked against ddsonic's code in one session. Plugin fixes apply only if plugins are reachable, which the 1.0 decision rules out; the triage confirms that rather than porting them. Severity is set from evidence that the defect exists and is reachable in ddsonic, not from commit titles. Accepted fixes are ported (with their prerequisites) to one integration branch, each with a regression test that fails without it, then `make check`, `-race` and one Codex round. Log: `docs/ddsonic/upstream-triage.md`, a row per plausibly relevant fix (commit, area, behaviour, applies (evidence), severity, decision). Not a full merge. A serious upstream fix during the soak is assessed on its own; accepting it means a new candidate

**Gate C: v1.0.0-rc.1.** The candidate is built as a draft and published as a prerelease only after the checks marked "before publishing" pass; the audit and your own use follow publication:

- [ ] One Codex high-effort audit of everything changed since its last comprehensive review (ea0a4a3), given the CLI disposition table, the platform matrix, earlier findings, changed files and test evidence. Inherited high-risk boundaries get a selective look
- [ ] Before publishing, once per candidate, in CI: `make fmt-check` (not `make check`, which rewrites files), `go vet`, `go test -race ./...`, staticcheck, govulncheck, dependency licenses
- [ ] Before publishing: the release archive has dependencies, version, checksums, notices, source and the SBOM; its build attestation verifies (`gh attestation verify`); it installs cleanly in a fresh container
- [ ] A release record per candidate (`docs/ddsonic/releases/<tag>.md`): candidate commit, artifact checksums, check results, findings and waivers with their rationale
- [ ] Clean clone builds the same binary in the pinned environment (checksum match)
- [ ] Install from the release and the AUR recipe; upgrade from v0.10; uninstall; a prerelease never reaches the stable AUR or website
- [ ] Migrations and setup: interrupted runs, recovery from backup, and an older binary refusing a newer catalog
- [ ] Failure paths: no network, provider errors, unwritable config/data/cache, a corrupt catalog
- [ ] IPC socket permissions, that plugins cannot activate (config inert, nothing loaded, no IPC plugin operations), and that credentials stay out of logs
- [ ] Cancellation and shutdown while playback or a sync is active
- [ ] Large library (the 100k benchmark set plus your real library), rapid navigation and search
- [ ] Long run: an hour of playback, measuring memory and goroutines against a baseline for a trend
- [ ] Terminal resizing, and artwork in Kitty and Ghostty
- [ ] Docs and website re-walked against the RC

**Before v1.0.0-rc.4: the human acceptance checklist** (required, decided 2026-10-09). The rc.3 report that albums saved in YouTube Music never appeared was found by using ddsonic, not by a test or an audit. A reusable human release-candidate acceptance checklist is therefore created and executed before the next candidate, and at every candidate after it: a repeatable gate in place of ad-hoc exploratory testing. It complements the automated tests and does not duplicate them.

That checklist has become **Release Acceptance** ([acceptance.md](acceptance.md)): a case catalog, scripted cases that run on the candidate's own binary, live-provider and human runbooks, and one gate over all of them. The order is automated code validation, then agent and workflow acceptance, then the human exploratory pass, which starts only when the agent pass is green. For `v1.0.0-rc.4`, live acceptance of both Spotify and YouTube Music is required. The first slice of the tooling exists (2026-10-09); no release run and no live or human pass has been made yet.

- [ ] The known rc.3 bugs are triaged or resolved
- [ ] The checklist is drafted and independently reviewed
- [ ] Steve executes it against the release candidate
- [ ] Vorbis decoder retention is measured with the MP3 measurement and brought to the owner ([acceptance.md](acceptance.md)); a material cost stops release progression
- [ ] The owner handles the upstream report on go-librespot's `vorbis.Decoder.Close` (outside this repository)
- [ ] Anything it uncovers is triaged before rc.4 is published

It emphasizes real workflows and state transitions. The pattern, for each synced source: add content upstream; verify it is absent; refresh; verify it appears; restart; verify it persisted; remove it upstream; refresh; verify it is gone.

It is to cover, at minimum: install, first run and setup; the Local library; Spotify; YouTube Music; Internet Radio; search; playback; queue and transport controls; metadata; artwork and info views; synchronization; persistence; manual refresh; configuration; hotkeys; degraded and error states; authentication failure and recovery; upgrades; restart and recovery; shutdown. The fresh-user scenarios A–H (Workstream 5) are a starting point, not the checklist.

### Before v1.0.0: the adversarial security audit

Required, decided by the owner on 2026-10-10. It has not been run: nothing here records a finding, an approval or an execution.

**When.** Only after the current candidate has completed its intended deterministic, live-provider, platform and human acceptance and is otherwise believed ready to be `v1.0.0`. It is an audit of the actual candidate that is intended to ship, not of an earlier one and not of the source in general.

**What it is not.** It is separate from ordinary code review, staticcheck, govulncheck, the race tests, Release Acceptance, the SBOM, artifact verification and dependency scans. **It is not skipped because those are green.**

**This gate may not be waived.**

**Stance.** Adversarial. Assume hostile inputs and look actively for ways to leak data, run unintended commands, expose credentials, deliver malicious content, or reach the network or the file system unexpectedly.

**Scope.** All of:

- Spotify and YouTube Music credentials and tokens
- config contents and file-system permissions
- SQLite, the cache and the database
- secrets in logs, diagnostics and crash output; redaction
- network and TLS
- radio and arbitrary URLs
- remote, untrusted metadata
- terminal escape and control-sequence injection
- subprocess and shell execution; command injection
- path traversal and hostile file names
- temporary files
- archive extraction
- input handling and malformed data
- the trust boundaries between providers, local files and the UI
- the install, build and release scripts
- CI/CD permissions and release credentials
- dependencies and the supply chain
- unexpected telemetry, outbound connections or data collection
- persistence mechanisms, and backdoor or spyware-like behavior
- generated and third-party code whose network, file-system, process or credential operations are broader than they need to be

**The release chain.** Source-to-artifact identity, dependency pins, the SBOM's accuracy, the build image and the trust placed in the toolchain, artifact integrity, what installing the package does, and whether the build and release tooling downloads or executes anything unexpected.

**Who.** Recommended: an independent Codex GPT-6.1-Sol review at high effort, or the strongest appropriate independent reviewer available at the time.

**If a finding needs a code change,** `v1.0.0` is not authorized. Then: Claude implements the fix by the normal workflow; Codex reviews it independently; focused security regression tests are added and run; the affected acceptance is rerun; validation is broadened if the change materially alters release behavior; and the final, corrected snapshot is reviewed again before release authorization.

**Complete only when** all of these hold:

- [ ] Every release-blocking finding is resolved
- [ ] Residual risks are documented explicitly
- [ ] No HIGH security concern is unresolved
- [ ] The code and the artifact that were finally audited are exactly the ones intended to be `v1.0.0`

**Gate D: v1.0.0.** Every RC-audit finding fixed or waived by you, by criteria you set in advance; no open BLOCKER or HIGH; and a 7-day soak of your own use with no new release-blocking regression. Any fix to code that runs (not docs-only) means a new candidate and restarts the soak. Otherwise cut rc.2, and review its fixes at effort proportional to their risk: a migration or concurrency fix can warrant high effort even then. The stable tag is built as a draft too: its own artifacts pass the CI, archive, SBOM, attestation and install checks, and its release record is written, before it is published.

- [ ] Required before `v1.0.0`: [the adversarial security audit](#before-v100-the-adversarial-security-audit) of the candidate intended to ship is complete. It is separate from Gate C's Codex audit and from every automated check, is not skipped because they are green, and may not be waived

## Post-1.0 backlog

Historical freeze-time inventory: these items were transferred to [plan.md](../../plan.md#post-10-backlog). The backlog is now GitHub Issues ([where each earlier entry went](POST-1.0-ISSUES.md)); this table is history and is not extended. Nothing here is built before 1.0.

| Idea | Notes |
| --- | --- |
| Auto EQ from normalized genre metadata | Builds on the catalog's genre data |
| Ambient or generated sounds | New feature area |
| More providers | Only one you use and can test; the 16 hidden ones are candidates. Since 2026-10-09, only after the [Provider Admission Contract](POST-1.0-IDEAS.md#provider-admission-contract) |
| Sixel, iTerm2 and other artwork protocols | WezTerm, Konsole, foot |
| InnerTube playlist discovery | Saved YouTube Music playlists the API doesn't list. Since 2026-10-09 part of [YouTube Music Provider v2](POST-1.0-IDEAS.md#youtube-music-provider-v2) |
| Cross-source playlists | ddsonic-owned playlists mixing sources |
| Clear the whole queue with X | Ctrl+Z undoes it, like x; X is reused: cliamp's provider-switching hotkeys (X was Mixcloud) have no place in ddsonic's library workflow. Today, playing from the library replaces the queue |
| YouTube cookie playlist count check | Guard against a short playlist feed. Since 2026-10-09 part of [YouTube Music Provider v2](POST-1.0-IDEAS.md#youtube-music-provider-v2) |
| Separate Local scan folder from file-browser folder | Open question in `plan.md` |
| Daemon or headless persistent playback | Likely a larger 2.0 architectural project; affects how the inherited `--daemon` is treated now |
| Broad-prefix search speed (P3) | Codex's ranking-preserving idea (about 22% faster), only if search speed becomes a complaint |
| Linux arm64, macOS, Homebrew, `.deb`/`.rpm` | Once someone can test them |
| Replace `xlab/vorbis-go` (no license file) | Feasibility study: Spotify playback decodes through it inside go-librespot. Candidate: `jfreymuth/oggvorbis` (MIT, pure Go, already in `go.mod` for local Ogg files). Likely needs a go-librespot change upstream or a maintained patch |
| `go install`, and `ddsonic` as the name a plain `go build` gives | Needs the module renamed to `github.com/DaphnisDuck/ddsonic`, which would make upstream merges costly. A decision for when tracking upstream is no longer worth that |
| Full upstream merge | The first 1.1 work: a `sync/upstream-YYYYMMDD` merge of upstream's latest release tag, never `main`. Fixes ported before 1.0 were cherry-picked, which gives no merge ancestry: the triage log says which conflicts keep ours |

## Decisions that are yours

Codex pointed out that an approved plan shouldn't settle these by default, so each was put to you. All were decided on 2026-10-01.

| Decision | Needed before | Outcome |
| --- | --- | --- |
| Which providers 1.0 supports, and what happens to existing config for the others | CLI audit changes | **Decided 2026-10-01:** Spotify, YouTube Music, Local, Radio; leave other config untouched but inert |
| Whether plugins stay supported (`--daemon` is decided: removed) | CLI audit changes | **Decided 2026-10-01:** unsupported in 1.0; removed from help, docs and the CLI surface, and never loaded at runtime or reachable over IPC; existing plugin config stays inert |
| Distribution and runtime baseline, the packaging matrix, and you as AUR maintainer | Packaging work | **Decided 2026-10-01:** GitHub Release + AUR (`ddsonic-bin`, you as maintainer) + source, Linux amd64, tested on Arch and Debian 13 (trixie); with the trust items under Workstream 2 |
| What 1.0 promises about config and catalog compatibility, backups and recovery | README and docs | **Decided 2026-10-01:** forward migrations within 1.x; no downgrade. Backup advice follows a classification of persistent state by recoverability (what a sync rebuilds, what only the user holds), made before the docs are written |
| How the license is presented, and accepting GPL-3.0's distribution duties | Any public binary | **Decided 2026-10-01:** MIT source, GPL-3.0 executable, said plainly; `xlab/vorbis-go` ships as an inherited dependency (via go-librespot, as in cliamp) and is named in the notices as having no license file; accepted pending the final dependency and license verification at the release gate |
| Website timing, demo tooling, and whether an SBOM can wait | Docs phase | **Decided 2026-10-01:** website ready no later than the v1.0.0 release; one Kitty screenshot with artwork; minimal SBOM in every release; the animated demo recorded with `vhs` from a script in the repo, against a demo library |
| Release-blocker criteria, and which lower findings may be waived | The RC | **Decided 2026-10-01:** BLOCKER and HIGH block; a MEDIUM is waived only with a short written rationale; LOW and NIT never block |

## Recommended first step

`review-fixes` merged on 2026-10-01. The three scope decisions that set the size of everything after them are taken:

1. **Hidden providers:** decided 2026-10-01: remove their public entry points (help, setup, config activation, IPC, and the provider-switching hotkeys) for 1.0.
2. **`--daemon`:** decided 2026-10-01: remove it until the post-1.0 daemon work.
3. **`cliamp://` links:** decided 2026-10-01: remove registration and dispatch for 1.0, with cleanup notes for registrations ddsonic made.

The next step is your approval of this plan. Then a fresh `release-1.0` branch begins with recording the freeze and backlog in `plan.md`, then the CLI audit, because setup, docs and the README all describe the surface it settles. Licensing comes right after, since it gates every public binary, RC included; packaging follows.
