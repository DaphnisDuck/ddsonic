# ddsonic Post-1.0 Dream Catalog

> **Status:** a dream catalog, not a committed implementation backlog  
> **Scope:** after the 1.0 release (`v1.0.0`, 2026-10-31)  
> **Items:** 97, with stable IDs: P-01 to P-44 (product), E-01 to E-20 (engineering), R-01 to R-15 (reliability and security), D-01 to D-18 (distribution, documentation and infrastructure)

This is the single consolidated catalog of post-1.0 ideas, investigations and deferred work, together with the context and earlier decisions each one carries. It is still a dream catalog, not a committed backlog: it ranks and schedules nothing.

Its sources are retained as historical source and rationale:

- [POST-1.0-IDEAS.md](POST-1.0-IDEAS.md) is the original brainstorming source and the rationale for many of the ideas.
- [plan.md](../../plan.md#post-10-backlog)'s post-1.0 backlog, with its Status and Decisions log, records what was decided and when.
- The release records ([release-plan.md](release-plan.md), [releases/](releases/)) record what each release candidate found and deferred.

## The owner's direction (2026-11-02)

- **This is a dream catalog.** Nothing in it is removed, rejected, ranked or scheduled. Ambitious ideas stay in it even when their feasibility is uncertain, or when an earlier non-goal points the other way. Such tensions are recorded as notes, never as reasons to drop an idea.
- **Earlier "first" decisions are not automatically binding.** These are kept in full and marked HISTORICAL, requiring owner review:
  - YouTube Music Provider v2 as the first provider initiative;
  - the full upstream merge as the first 1.1 work;
  - Apple Music only after the Provider Admission Contract.
- **The major features remain possibilities.** YouTube Music Provider v2, Apple Music and other major features are among them.
- **One sequencing constraint is confirmed.** The existing Astro ddsonic website must be finished and launched before new product features start. It is developed in a separate project, whose repository and checkout locations are to be confirmed (D-01).
- **After the website,** the owner is interested in several small UX improvements to regain development momentum. No feature order is committed.

## How to read it

- **Order:** product ideas come first, then engineering, reliability and security, and distribution, documentation and infrastructure.
- **Item fields:** each item gives:
  - the outcome wanted;
  - where it is recorded;
  - its status and the decisions already made about it;
  - a category;
  - its dependencies;
  - what was merged into it;
  - flags.
- **Closing sections:** (a) the merged duplicates; (b) decisions that conflict, are outdated or are ambiguous; (c) gaps found while compiling; (d) decisions the owner will eventually make (not a ranking).
- **References:** `file:line` references are repository paths, and their line numbers are as of commit `efbc1eba` (`main` when this catalog was compiled). Short forms:
  - `IDEAS` is [docs/ddsonic/POST-1.0-IDEAS.md](POST-1.0-IDEAS.md);
  - `RP` is [docs/ddsonic/release-plan.md](release-plan.md);
  - `review` is [docs/ddsonic/reviews/independent-review-2026-09-30.txt](reviews/independent-review-2026-09-30.txt);
  - `rc.1`, `rc.2` and `rc.3` are the release records in [docs/ddsonic/releases/](releases/);
  - bare `plan.md`, `README`, `CHANGELOG`, `SECURITY`, `youtube.md`, `search.md`, `artwork.md`, `files.md`, `catalog.md`, `layout.md`, `acceptance.md`, `releasing.md`, `upstream.md` and `upstream-triage.md` mean those files at the repository root or in `docs/ddsonic/`.
  - "Private maintenance records" are the maintainer's working notes from the 1.0 release, which are not published.

Flags:
- `HIST` a historical decision or priority, kept as recorded and not a current commitment (requires owner review);
- `OWNER` needs an owner decision;
- `OOR` recorded only in private maintenance records, outside this repository;
- `GAP` no tracked entry found anywhere before this catalog;
- `STALE` text contradicted by later facts;
- `RESOLVED` closed, kept for traceability;
- `SEC-SEP` tracked separately; this catalog keeps it high-level.

## How this catalog was compiled

It was compiled on 2026-11-02 from a read-only review of the repository at `efbc1eba`:
- `plan.md`;
- `docs/ddsonic/`;
- `README.md`, `CHANGELOG.md` and `SECURITY.md`;
- the code's deferred-work markers;
- the git history;
- the maintainer's private records of the 1.0 release.

The owner then added P-40 to P-44 and the sleep-timer detail in P-01, and corrected the website entry (D-01). Item IDs are stable: later additions take new numbers.

---

# Part 1. Product wishlist

### P-01 Sleep timer
- **Outcome:** stop or fade out playback after a chosen time or at the end of the current track or album.
- **Source:** owner (ROADMAP-1 task, 2026-11-02).
- **Status / prior decisions:** none. Not in plan.md, IDEAS or any release record. "sleep" appears only as a use case of the noise source (IDEAS:423).
- **Category:** feature.
- **Dependencies:** none known. Relates to P-22 (noise source).
- **Duplicates merged:** none.
- **Flags:** GAP.
- **Owner detail (2026-11-02):**
  - preset durations and a custom duration;
  - cancel and extend a running timer;
  - an optional fade-out before stopping;
  - an option to finish the current track (rather than stop mid-track).

### P-02 Metadata sidebar: default visibility
- **Outcome:** decide whether the metadata (Track Info) panel is shown by default, and how it is toggled.
- **Source:** private maintenance records (an owner UI-polish idea, unscheduled).
- **Status / prior decisions:** unscheduled. "sidebar" does not appear in plan.md.
  - Related inconsistency: the README screenshot's key bar shows `Ctrl+I Metadata`, while `docs/ddsonic/navigation.md:116` says keys jumping to other parts of cliamp are disabled.
- **Category:** feature (UX).
- **Dependencies:** best decided with P-03.
- **Duplicates merged:** none.
- **Flags:** OOR, OWNER.

### P-03 Track Info / Metadata Redesign
- **Outcome:** an information architecture for track metadata beside artwork. It covers a metadata inventory across sources, overflow handling, and very narrow to very wide terminals.
- **Source:** IDEAS:169-197; plan.md:606-607, 883; plan.md:974.
- **Status / prior decisions:** "post-1.0 investigation/design item; unscheduled, not an approved implementation specification" (IDEAS:172).
  - HIST (2026-10-05): "marquee scrolling is not a preferred solution".
- **Category:** investigation (design).
- **Dependencies:** P-24 (artwork), E-12 (capabilities), P-14/P-15 (metadata), M9 `bodySize()` (plan.md:566, 738: "detail views later").
- **Duplicates merged:** none. P-02 is kept separate as a narrow default-visibility question.
- **Flags:** HIST.

### P-04 YouTube Music Provider v2
- **Outcome:** YouTube Music as a first-class provider with explicit, trustworthy and complete library semantics: true saved Library Albums, followed artists, complete playlists, stable identity and reconciliation.
- **Source:** IDEAS:79-119; plan.md:879, 976-977; `docs/ddsonic/youtube.md:9, 11, 74, 80-87, 135-136`; RP:312.
- **Status / prior decisions:**
  - HIST (owner, 2026-10-09): "the first major post-1.0 provider initiative" (plan.md:977), first in the provider order (plan.md:976-979).
  - HIST: the three approaches are to be compared. "ddsonic owns library semantics…, yt-dlp extracts media" is "not a decision" (IDEAS:91).
  - The 1.0 lists are "Albums from tracks" / "Artists from tracks".
  - The current owner direction calls it a strong candidate, undecided. **Requires owner review.**
- **Category:** feature (and investigation).
- **Dependencies:** E-13 (Provider Admission Contract, "with the provider-contract work"), E-14 (`PartialAlbums` rename).
- **Duplicates merged:**
  - InnerTube discovery of saved playlists the API does not list (plan.md:645, 917, 2026-09-29);
  - saved Library Albums (plan.md:880, 911; youtube.md:9);
  - followed artists (youtube.md:11);
  - cookie playlist feed count guard (plan.md:726, "deferred since M4");
  - own vs saved playlists in the cookie feed (plan.md:418, 645);
  - Data API lists few playlists (youtube.md:80-87);
  - Testing-status OAuth weekly expiry (youtube.md:74);
  - the `youtube_playlists`/`OLAK5uy_` workaround's limits (youtube.md:135-136);
  - IDEAS:720 "YouTube Music follow-ups".
- **Flags:** HIST, OWNER.

### P-05 YouTube Music live search in OAuth mode
- **Outcome:** live YouTube Music search also when an OAuth client is configured. Today it exists only with cookie-only sign-in.
- **Source:** `CHANGELOG.md:29`; `docs/ddsonic/search.md:37`; plan.md:380, 645. The 1.0.0 release notes record it as documented behaviour.
- **Status / prior decisions:** documented 1.0 behaviour; no backlog entry.
- **Category:** feature.
- **Dependencies:** P-04 (same provider, same auth questions).
- **Duplicates merged:** none.
- **Flags:** GAP (no tracked entry; listed by the owner as "post-1.0 OAuth YTM live search").

### P-06 Artwork for YouTube Music and radio
- **Outcome:** artwork for sources that have none today.
- **Source:** plan.md:589 ("YouTube skipped for now (its thumbnails are video frames…)"); `docs/ddsonic/artwork.md:12` ("none yet").
- **Status / prior decisions:** HIST deferral (2026-09-30).
- **Category:** feature.
- **Dependencies:** P-04, P-24.
- **Duplicates merged:** none.
- **Flags:** GAP (not in IDEAS or the backlog), HIST.

### P-07 Apple Music provider: feasibility
- **Outcome:** establish whether Apple Music can be integrated without hacks: API access, auth, playback restrictions and DRM, Linux. "A legitimate result… may be do not implement."
- **Source:** IDEAS:465-482; owner interest (2026-11-02).
- **Status / prior decisions:**
  - "Specifically a feasibility investigation, not a promised provider."
  - HIST: placed after the Provider Admission Contract (IDEAS:69-77).
  - Listed under Explicit Non-Commitments (IDEAS:741).
- **Category:** investigation.
- **Dependencies:** E-13 (HIST ordering), E-12.
- **Duplicates merged:** none.
- **Flags:** HIST, OWNER.
- **Related:** P-44 (external radio service integrations; another subscription-service feasibility study).

### P-08 Additional providers, including the hidden inherited ones
- **Outcome:** decide which further providers (for example the inherited Navidrome, Plex, Jellyfin, SoundCloud, Tidal or Qobuz code) are worth adding or restoring.
- **Source:** IDEAS:445-463; plan.md:710, 408/925, 949; `docs/ddsonic/cli-audit.md:101`.
- **Status / prior decisions:**
  - HIST: "Only providers the owner uses and can test"; "Inherited providers hidden for 1.0 are candidates, not promised restorations"; nothing added before the contract.
  - HIST (2026-09-29): no Plex/Jellyfin/Navidrome server in use (plan.md:408).
- **Category:** feature / investigation.
- **Dependencies:** E-13, E-12.
- **Duplicates merged:** plan.md:710 "the next provider (none in use yet)".
- **Flags:** HIST.
- **Related:** P-44 (external radio services), kept as a distinct idea.

### P-44 External radio service integrations
- **Outcome:** an investigation of whether Pandora, SiriusXM and AccuRadio can be integrated into ddsonic. Questions to answer:
  - APIs: whether usable access exists for a third-party terminal client;
  - authentication;
  - subscriptions;
  - playback access;
  - licensing and terms of use;
  - platform and regional restrictions.
  - It also covers the differences between traditional radio streams, curated stations and personalized radio.
  - And it weighs integration into the existing Internet Radio interface against a separate provider model.
- **Source:** owner / earlier discussion, not previously recorded in repo.
  - None of the three services is named anywhere in the repo at efbc1eba (searched with `git grep`), including IDEAS:445-463 (Additional Providers) and the radio items.
  - Relevant framing: IDEAS:445-463 weighs any provider against "API stability; authentication complexity; playback restrictions; maintenance burden; architectural exceptions required".
  - Today's Internet Radio is the user's favourite stations, the Radio Browser directory, cliamp's radio channels and user-added stations (`README.md:42`).
- **Status / prior decisions:** none for these services. Nothing about their APIs, authentication, licensing or availability has been verified; each is an open question for the investigation. HIST constraints that apply to provider work in general:
  - nothing is added before the Provider Admission Contract (E-13; IDEAS:445-447);
  - "only providers the owner uses and can test" (IDEAS:449).
  - IDEAS:59: "Radio may reasonably remain a somewhat separate concept" bears on the Internet Radio vs provider question. Recorded as a note, not a reason against.
- **Category:** investigation (feature).
- **Dependencies:** E-13 (Provider Admission Contract) and E-12 (capability-based provider architecture). A personalized-radio service may also fit the contract's "limited non-library source" outcome (IDEAS:153-159).
- **Duplicates merged:** none; a distinct idea. Related:
  - P-08 (additional providers);
  - P-07 (Apple Music feasibility, a similar subscription-service question);
  - E-13;
  - the radio items P-06 (radio artwork), P-27 (radio sorting) and R-13 (raw radio metadata).
- **Flags:** GAP.

### P-09 Provider-independent music library and cross-provider capabilities
- **Outcome:** "Music should belong to the user, not the provider". An album is an album whatever its source. This enables source-independent favourites, local-first resolution, provider substitution and unified identity.
- **Source:** IDEAS:33-59; plan.md:69, 77, 248, 904 (cross-provider dedup and merge deferred); owner item (2026-11-02).
- **Status / prior decisions:**
  - "This is a direction, not a requirement to build a giant universal music database" (IDEAS:57).
  - HIST (2026-09-29): "Duplicates stay separate… Merging is deferred."
  - Possible tension with plan.md non-goals (see b-3).
- **Category:** architecture / investigation.
- **Dependencies:** E-12, P-14.
- **Duplicates merged:** cross-source dedup (plan.md:69, 248, 904). P-10, P-11 and P-12 are this direction's applications, kept as separate items.
- **Flags:** HIST, OWNER.

### P-10 Portable / cross-provider playlists
- **Outcome:** ddsonic-owned playlists whose entries resolve at playback across sources. Provider-native playlists stay usable.
- **Source:** IDEAS:393-410; plan.md:710 ("omatunes-owned cross-source playlists"); RP:310-320 (historical table).
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** P-09.
- **Duplicates merged:** plan.md:710; the RP table row.
- **Flags:** none.

### P-11 Graceful provider failover
- **Outcome:** play the same recording from another source when the preferred one is unavailable. "Playing the wrong recording is worse than reporting that the requested track is unavailable."
- **Source:** IDEAS:383-391.
- **Status / prior decisions:** listed under non-commitments (IDEAS:741).
- **Category:** feature / investigation.
- **Dependencies:** P-09.
- **Duplicates merged:** none.
- **Flags:** none.

### P-12 Offline awareness
- **Outcome:** show what is playable without network, with an offline filter and search, and skip unavailable providers.
- **Source:** IDEAS:370-381.
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** P-09.
- **Duplicates merged:** none.
- **Flags:** none.

### P-13 Library health
- **Outcome:** find missing local files, unavailable provider items, duplicates, unresolved playlist entries, incomplete metadata and stale cache.
- **Source:** IDEAS:353-368.
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** P-09.
- **Duplicates merged:** none.
- **Flags:** none.

### P-14 Metadata enrichment
- **Outcome:** read-only enrichment of incomplete metadata (art, release info, composer/work, genres) with provenance and confidence.
- **Source:** IDEAS:218-250.
- **Status / prior decisions:**
  - Open: persist it, cache it with expiry, or resolve it dynamically.
  - Conflicts with the plan.md:17 non-goal "No automatic metadata correction or MusicBrainz enrichment" (see b-3).
- **Category:** investigation.
- **Dependencies:** P-03.
- **Duplicates merged:** none.
- **Flags:** OWNER.

### P-15 Classical music metadata
- **Outcome:** first-class composers, works, movements, performers and catalog numbers, with a work grouping its movements.
- **Source:** IDEAS:252-271; IDEAS:184, 229.
- **Status / prior decisions:** "Avoid designing a musicological database for its own sake." Not committed (IDEAS:741).
- **Category:** investigation.
- **Dependencies:** P-14, P-03.
- **Duplicates merged:** none.
- **Flags:** none.

### P-16 Genre normalization
- **Outcome:** a reversible normalized genre layer that keeps the original metadata.
- **Source:** IDEAS:273-289.
- **Status / prior decisions:** unscheduled.
- **Category:** investigation.
- **Dependencies:** none; it is a prerequisite of P-17 and P-18.
- **Duplicates merged:** none.
- **Flags:** none.

### P-17 Automatic genre-based EQ
- **Outcome:** EQ presets chosen from normalized genre, predictable and overridable.
- **Source:** IDEAS:291-301; RP:310-320.
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** P-16.
- **Duplicates merged:** "Auto EQ from normalized genre metadata"; the RP row.
- **Flags:** none.

### P-18 Generated / random playlists
- **Outcome:** listening sessions from the unified library by genre, artist, era, neglected music and so on. "Useful rediscovery rather than a recommendation engine."
- **Source:** IDEAS:303-320.
- **Status / prior decisions:** tension with the plan.md:17 non-goal "no smart playlists" (see b-3).
- **Category:** feature.
- **Dependencies:** P-16, P-19.
- **Duplicates merged:** none.
- **Flags:** OWNER.

### P-19 Listening history and rediscovery
- **Outcome:** enough history for "not played lately", forgotten favourites and never played, by transparent rules.
- **Source:** IDEAS:322-336.
- **Status / prior decisions:** tension with the plan.md:17 non-goal "no recommendations" (see b-3).
- **Category:** feature.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** OWNER.

### P-20 Smarter queue
- **Outcome:** continue after the queue ends, build queues from albums, artists, genres or playlists, and better manipulation of upcoming items. The user always knows why something plays.
- **Source:** IDEAS:338-351.
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** P-19 (optional).
- **Duplicates merged:** none.
- **Flags:** none.

### P-21 `X` clears the whole queue (Ctrl+Z undoes)
- **Outcome:** a one-key queue clear with undo.
- **Source:** plan.md:985; IDEAS:721; RP:310-320.
- **Status / prior decisions:** post-1.0 backlog. "`X` is free once the provider hotkeys go."
- **Category:** feature.
- **Dependencies:** none.
- **Duplicates merged:** all three sources.
- **Flags:** none.

### P-22 Generated noise source
- **Outcome:** white, pink, brown or simple ambient noise for sleep or focus. "Keep this implementation small."
- **Source:** IDEAS:412-427; plan.md:974.
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** none.
- **Duplicates merged:** half of the "ambient or generated sounds" backlog row (RP:310-320).
- **Flags:** none.

### P-23 Ambient / nature audio
- **Outcome:** licensed ambient material (rain, ocean), generated or CC0 preferred, with no large bundle.
- **Source:** IDEAS:429-443.
- **Status / prior decisions:** unscheduled.
- **Category:** feature.
- **Dependencies:** licensing review.
- **Duplicates merged:** the other half of the RP row.
- **Flags:** none.

### P-24 Album artwork across more terminals
- **Outcome:** a tested compatibility matrix (Sixel, iTerm2, WezTerm, Konsole, foot, tmux) and a recommendation on an image library.
- **Source:** IDEAS:201-216; plan.md:743, 983; RP:313; `docs/ddsonic/artwork.md:21`.
- **Status / prior decisions:**
  - HIST: "no library change approved".
  - The 2026-09-30 review listed "additional terminal image backends" as "can safely wait".
- **Category:** investigation.
- **Dependencies:** P-03.
- **Duplicates merged:** the Sixel/iTerm2 later candidate (plan.md:743); RP:313.
- **Flags:** HIST.

### P-25 Three-state keymap / key-hint display
- **Outcome:** Minimal / Full / Hidden key hints, cycled by the existing toggle.
- **Source:** IDEAS:484-514; plan.md:605, 881.
- **Status / prior decisions:** "unscheduled, not a committed feature". The default, cycle order and persistence are open (2026-10-08).
- **Category:** feature (UX).
- **Dependencies:** optionally P-26.
- **Duplicates merged:** none.
- **Flags:** OWNER (the default).

### P-26 Searchable command palette
- **Outcome:** a fuzzy-search action menu. Long term, one action registry for keys, the palette and CLI/IPC.
- **Source:** IDEAS:615-640.
- **Status / prior decisions:** unscheduled.
- **Category:** feature / architecture.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** none.

### P-27 Alphabetical radio station sorting
- **Outcome:** deterministic, case-insensitive name sort, applied consistently.
- **Source:** IDEAS:516-532; plan.md:604.
- **Status / prior decisions:** unscheduled. Check intentional orderings, such as favourites, first.
- **Category:** feature (UX).
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** none.

### P-28 Sign in from a Spotify play request
- **Outcome:** offer browser sign-in when a play needs it, and optionally resume the track afterwards.
- **Source:** IDEAS:552-560.
- **Status / prior decisions:** "LOW; post-1.0 UX enhancement. Not a defect." Does not resolve LIVE-SPOT-003.
- **Category:** feature.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** none.

### P-29 A ddsonic visualizer of its own (ASCII duck)
- **Outcome:** replace the hidden inherited Logo mode.
- **Source:** plan.md:991; IDEAS:727; plan.md:784, 794 (Logo hidden).
- **Status / prior decisions:** post-1.0 backlog.
- **Category:** feature.
- **Dependencies:** none.
- **Duplicates merged:** both sources.
- **Flags:** none.

### P-30 Favourite / like on the source
- **Outcome:** a favourite key that likes on the provider, or uses ddsonic-owned playlists.
- **Source:** plan.md:492 ("Favorite (`n`): removed… may bring it back later"), plan.md:710.
- **Status / prior decisions:** HIST (2026-09-29). Note that `n` is now next track (M8), so any return needs a new key.
- **Category:** feature.
- **Dependencies:** P-10 (for the owned-playlist variant).
- **Duplicates merged:** both plan lines.
- **Flags:** GAP (not in IDEAS), HIST.

### P-31 Per-band EQ editing
- **Outcome:** edit individual EQ bands in the TUI.
- **Source:** plan.md:500 ("needs panel focus and is out of M6").
- **Status / prior decisions:** HIST deferral (2026-09-29).
- **Category:** feature.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** GAP.

### P-32 YouTube Music sign-in inside the TUI (and CLI/IPC sign-in)
- **Outcome:** whether YouTube Music should get the in-TUI sign-in that Spotify has.
- **Source:** plan.md:866 ("YouTube Music gets no in-TUI sign-in. No CLI or IPC sign-in is added.", 2026-10-12).
- **Status / prior decisions:** HIST decision (no).
- **Category:** feature.
- **Dependencies:** P-04.
- **Duplicates merged:** none.
- **Flags:** HIST, GAP (as a future option).

### P-33 Daemon / persistent playback
- **Outcome:** playback that survives the TUI, with external control and several clients. Keeping playback in the TUI is a valid outcome.
- **Source:** IDEAS:642-672; RP:310-320 ("likely a larger 2.0 architectural project").
- **Status / prior decisions:** HIST: "likely a 2.0" is "a scale estimate, not a release commitment". Non-commitment (IDEAS:741).
- **Category:** architecture / investigation.
- **Dependencies:** the IPC design.
- **Duplicates merged:** the daemon/headless idea.
- **Flags:** HIST.

### P-34 Multi-device synchronization
- **Outcome:** sync playlists, favourites, history and config across installs. Synchronized playback is a separate, harder problem.
- **Source:** IDEAS:674-690.
- **Status / prior decisions:** needs a "compelling use case". Non-commitment.
- **Category:** investigation.
- **Dependencies:** P-10, P-19.
- **Duplicates merged:** none.
- **Flags:** none.

### P-35 Local audio format compatibility audit and documentation
- **Outcome:** know and document which local containers and codecs play. The AIFF failure is explained.
- **Source:** IDEAS:562-586.
- **Status / prior decisions:** "Post-1.0 investigation; unscheduled… no new release gate". Earlier per-format priorities were withdrawn (IDEAS:570).
- **Category:** investigation.
- **Dependencies:** FFmpeg cost question.
- **Duplicates merged:** none.
- **Flags:** none.

### P-36 Separate the Local scan folder from the file browser's start folder
- **Outcome:** a `music_dir` setting distinct from `initial_directory`.
- **Source:** plan.md:1007 (Open questions), plan.md:218; IDEAS:731; RP:310-320.
- **Status / prior decisions:** default "keep reusing `initial_directory` until someone needs them apart".
- **Category:** feature (config).
- **Dependencies:** none.
- **Duplicates merged:** all four sources.
- **Flags:** none.

### P-37 Offline artwork guarantee
- **Outcome:** decide whether any offline-artwork guarantee is needed beyond the existing cache.
- **Source:** plan.md:1008 ("cache… or store URLs only (M2 stores URLs only)"); IDEAS:731.
- **Status / prior decisions:** **STALE:** M10 caches remote covers in ddsonic's artwork cache (plan.md:592, 595), but the open question was never closed.
- **Category:** documentation / investigation.
- **Dependencies:** P-12.
- **Duplicates merged:** both sources.
- **Flags:** STALE.

### P-38 Smaller playback/network UX items set aside for 1.0
- **Outcome:** reconsider them if wanted:
  - automatic reopen, a network-recovery policy and a "Buffering" display (plan.md:847, "out of scope");
  - "Seek failed" accepted as a loss (plan.md:834, 847);
  - sync only on first use of Spotify (plan.md:957, "not built for now");
  - the uncached-album error wording (plan.md:674);
  - `e` inert in the minimal key tier (plan.md:714);
  - partial "from tracks" notes on opened albums and artists (plan.md:797, "Open");
  - `ddsonic vis list` exits with an error and prints no list (plan.md:873, "open to the owner").
- **Source:** as listed.
- **Status / prior decisions:** HIST, each left as is for 1.0.
- **Category:** feature (UX).
- **Dependencies:** none.
- **Duplicates merged:** the seven listed.
- **Flags:** HIST, GAP (none is in the backlog).

### P-39 Catalog-first artist view
- **Outcome:** an artist view built from the catalog. The 2026-09-30 independent review said it "would better support the direction".
- **Source:** `docs/ddsonic/reviews/independent-review-2026-09-30.txt:316-321`.
- **Status / prior decisions:** possibly addressed since (`docs/ddsonic/navigation.md:18`). Not verified.
- **Category:** feature.
- **Dependencies:** P-09.
- **Duplicates merged:** none.
- **Flags:** OWNER (confirm whether it is still open).

### P-40 Consistent alignment of Album and Artist columns across library views
- **Outcome:** album, artist and other detail columns line up consistently across library views (albums, artists, playlists, search results, track lists) at every terminal width.
- **Source:** owner, from earlier discussions; not previously recorded in the repository.
- **Status / prior decisions:** no record of the request. Today's layout follows the HIST M9 decision (plan.md:570, 2026-09-30; `docs/ddsonic/layout.md`):
  - Up to 100 columns, a browsable row's detail is right-aligned (`ui/model/library_view.go:373-384`).
  - Past 100 columns, browsable rows form a table whose title column fits 9 in 10 titles, and "a longer row runs past its column whole" (`ui/model/library_rows.go:20-38`; `ui/model/library_view.go:366-371`). So about 1 row in 10 is out of line, by design.
  - Track rows put their label after the name, not in a column (`ui/model/library_view.go:326-344`).
  - Note, not a reason against: M9 recorded that a max-width column "failed on the real library (249 of 2,667 local albums need over 96 columns)".
- **Category:** feature (UX).
- **Dependencies:** P-41 (truncation is the other side of alignment).
- **Duplicates merged:** none. Related: P-41, P-03 (layout around artwork), M9 `bodySize()`.
- **Flags:** GAP, HIST (the M9 layout decision).

### P-41 Word-boundary-aware truncation of long labels
- **Outcome:** long titles and details are cut at a word boundary, with an ellipsis, where that reads better.
  - Explicit rules cover when a boundary costs too much width, single words longer than the room, wide characters, and narrow terminals.
- **Source:** owner, from earlier discussions; not previously recorded in the repository.
- **Status / prior decisions:** no record of the request.
  - Today `truncate` cuts at the cell limit with "…", mid-word (`ui/model/view_helpers.go:140-150`, `ansi.Truncate`). Multi-line text is wrapped on spaces by `wrapText` (`ui/model/view_helpers.go:152` onward).
  - IDEAS:176-184 lists overflow options for Track Info (wrap, truncate, marquee, relocate, expandable). HIST: marquee is "not a preferred solution" there (P-03).
  - Tradeoffs to record, not reasons against: a word-boundary cut can give up most of a word's width per row; it moves the ellipsis by different amounts on neighbouring rows; and it competes with the detail column for the same width (P-40).
- **Category:** feature (UX).
- **Dependencies:** P-40.
- **Duplicates merged:** none. Related: P-03 (overflow options), P-40.
- **Flags:** GAP.

### P-42 Local > Folders: show playable audio files by default, keep directory navigation
- **Outcome:** Local's Folders view lists directories and playable audio files by default, without non-audio files such as covers, logs or cue sheets. Directory navigation is unchanged, and a way to show all files could remain.
- **Source:** owner, from earlier discussions; not previously recorded in the repository. Navigation: `docs/ddsonic/navigation.md:10, 24` ("Folders opens the file browser").
- **Status / prior decisions:** no record of the request.
  - Folders opens the inherited file browser (`library/sources.go:336-340`; `ui/model/library_nav.go:662-663`).
  - The browser lists every entry not starting with a dot: directories first, then all files. Audio is only flagged (`ui/model/filebrowser.go:140-181`, `isAudio` from `player.SupportedExts`).
  - Only audio files and directories are actionable (`ui/model/filebrowser.go:393, 454, 517`).
  - Note: `ui/model/filebrowser.go` is upstream code, so a change there is an upstream-file edit under the HIST upstream policy (plan.md:113-119).
- **Category:** feature (UX).
- **Dependencies:** P-35 (which formats count as playable).
- **Duplicates merged:** none. Related: P-35, P-36 (`music_dir`), E-10 (upstream file).
- **Flags:** GAP.

### P-43 Queue and session restoration after restarting ddsonic, with optional saved-position resume
- **Outcome:** after a restart, ddsonic can restore the session: the queue, the current track and its place in the queue, and possibly shuffle and repeat. Optionally it resumes the saved playback position.
- **Source:** owner, from earlier discussions; not previously recorded in the repository. Related inherited code below.
- **Status / prior decisions:** **INVESTIGATION, not a confirmed defect.** What the code at efbc1eba does was read only, not exercised:
  - **Inherited from cliamp:** `internal/resume/resume.go:15-21` stores `{Path, PositionSec, Playlist, Context, ContextIndex}` in `resume.json` in the config directory (`docs/ddsonic/files.md:7, 41-42`).
  - **Saved at a clean TUI exit:** `main.go:762-771` saves the last track's path, seconds and playlist name. The queue context is saved only for Jellyfin (`main.go:505-516`, `762-768`). `SaveState` skips a zero position without a context (`internal/resume/resume.go:39-41`).
  - **Restored at start:** `main.go:359` loads it. `main.go:644-653` sets a resume point only for:
    - a restored Jellyfin context;
    - a Mixcloud URL;
    - a launch with positional arguments (not radio).
  - **Applied:** the model seeks to the saved seconds once, when that same track path is played (`ui/model/playback.go:577-578, 700-712`).
  - **So:** a plain `ddsonic` launch restores no queue and sets no resume point, and the ddsonic queue itself is not persisted. Whether the path match works for Spotify or YouTube Music tracks is unverified.
  - **Related deferrals:**
    - "SIGTERM skips the exit save (resume position…)" is an upstream fix deferred to the 1.1 merge (`docs/ddsonic/upstream-triage.md:30`; E-10).
    - Upstream's headless daemon saves resume too (`daemon.go:1145`; `docs/headless.md`), but `--daemon` was removed from ddsonic's CLI (`docs/ddsonic/cli-audit.md:44`, "remove (decided)").
- **Category:** investigation.
- **Dependencies:** E-10 (the SIGTERM exit-save fix); provider track identity (P-09).
- **Duplicates merged:** none. Related:
  - P-33 (daemon: playback that outlives the TUI changes what a restart means);
  - P-34 (multi-device sync of queue and history);
  - P-19 (history);
  - P-12 (restored remote items may be unplayable offline).
- **Flags:** GAP (the owner idea), OWNER.

---

# Part 2. Engineering and architecture

### E-01 Repository structure and architecture review
- **Outcome:** a review of how the fork's packages, the `*_ddsonic.go` overlays and the inherited subsystems fit today, with recommended simplifications.
- **Source:** owner (2026-11-02).
- **Status / prior decisions:** no entry. Related: IDEAS:692 (cliamp independence), IDEAS:590 (capabilities), and review:406-409 ("Can safely wait: … replacing global layout state…").
- **Category:** architecture.
- **Dependencies:** informs E-08, E-10 and E-11.
- **Duplicates merged:** none.
- **Flags:** GAP.

### E-02 "Understanding ddsonic": a source-code learning guide
- **Outcome:** a guide for reading the codebase: runtime flow, packages, the library/catalog/provider layers, and the Spotify session and SDK boundary.
- **Source:** owner (2026-11-02).
- **Status / prior decisions:** none. CLAUDE.md has an architecture map, which is agent-oriented and partly stale (D-14).
- **Category:** documentation.
- **Dependencies:** best after E-01.
- **Duplicates merged:** none.
- **Flags:** GAP.

### E-03 CI performance: the Spotify SDK contract suite
- **Outcome:** a faster `make ci` without losing the gate's guarantees. The owner notes the suite takes about 13 minutes.
- **Source:** owner (2026-11-02).
  - Timing records: `docs/ddsonic/acceptance.md:134` says about 5 min 21 s on the 32-core dev machine (2026-10-16).
  - CI: the Linux `go` job took 21m14s with `make ci` 20m03s (run 41374046108); the timeout was raised to 40 minutes in commit e3af1044.
- **Status / prior decisions:** none. The gate is run "every release candidate" and on SDK-boundary changes (`docs/ddsonic/design/spotify-token-coordinator.md:1786-1790`).
- **Category:** infrastructure.
- **Dependencies:** E-05 (the SDK strategy changes what the gate must prove).
- **Duplicates merged:** none.
- **Flags:** GAP, OWNER (which guarantees stay mandatory per push).

### E-04 Streamline the AI development and release process
- **Outcome:** a faster agent workflow and release process without losing meaningful QA. This includes deciding which `/tmp` release and harness tooling belongs in the repo, and the agent-docs cleanup.
- **Source:** plan.md:1000 (owner request, 2026-10-19, "post-1.0, not started"); `docs/ddsonic/releasing.md:43` ("Agent docs cleanup (agreed, pending)… not started"); owner (2026-11-02).
- **Status / prior decisions:** HIST: agreed and not started.
- **Category:** infrastructure.
- **Dependencies:** D-14 (stale CLAUDE.md), D-12 (artifact cleanup).
- **Duplicates merged:** plan.md:1000; releasing.md:43; the `/tmp` tooling evaluation.
- **Flags:** HIST.

### E-05 Downstream Spotify SDK strategy (`third_party/go-librespot`)
- **Outcome:** regularize the local patched go-librespot (patches 0001-0003) by upstreaming, a formal maintained fork, or another strategy.
- **Source:** plan.md:998 (2026-10-15); plan.md:849 (2026-10-14: "tracked local downstream replacement (no fork, nothing published, no upstream contact)"); `docs/ddsonic/upstream.md:72`.
- **Status / prior decisions:** HIST: "the owner left this choice for after 1.0". It supersedes plan.md:770 ("stays at 0.9.1 until upstream moves").
- **Category:** architecture.
- **Dependencies:** coupled with E-15 and R-06 (kept high-level).
- **Duplicates merged:** none.
- **Flags:** HIST, OWNER.

### E-06 Simplify and encapsulate the Spotify lifecycle and recovery code
- **Outcome:** the proven behaviour is kept, with less code and clearer ownership.
- **Source:** plan.md:999; behaviour set in the Decisions log (2026-10-12 O1-O4; 2026-10-13 recovery).
- **Status / prior decisions:** post-1.0 note (2026-10-15).
- **Category:** architecture.
- **Dependencies:** E-05.
- **Duplicates merged:** none.
- **Flags:** HIST.

### E-07 Cancellation, reader ownership and prefetch maintainability
- **Outcome:** broader maintainability of these paths, including patch 0003.
- **Source:** plan.md:1001.
- **Status / prior decisions:** post-1.0 note.
- **Category:** architecture.
- **Dependencies:** E-05, E-06.
- **Duplicates merged:** none.
- **Flags:** HIST.

### E-08 Investigate accumulated unnecessary complexity
- **Outcome:** a survey of complexity that no longer earns its cost: test harnesses, guards, layered workarounds and release machinery.
- **Source:** owner (2026-11-02). Principle at IDEAS:12-27 ("Complexity is a cost, not a sign of maturity") and IDEAS:792 ("Keep rejecting complexity").
- **Status / prior decisions:** none.
- **Category:** investigation (architecture).
- **Dependencies:** E-01. Overlaps E-04, E-06 and E-07.
- **Duplicates merged:** none. E-06/E-07 stay as concrete instances.
- **Flags:** GAP.

### E-09 Bounded reasoning-level bug-hunting experiment (Extra High vs Max)
- **Outcome:** a tightly bounded comparison of bug-finding yield at the two reasoning levels.
- **Source:** owner (2026-11-02): "deferred until after meaningful feature progress".
- **Status / prior decisions:** deferred by the owner.
- **Category:** investigation.
- **Dependencies:** meaningful feature progress (owner condition).
- **Duplicates merged:** none.
- **Flags:** GAP, OWNER (scope and budget).

### E-10 Full upstream cliamp merge
- **Outcome:** merge upstream's latest release tag (never `main`) on `sync/upstream-YYYYMMDD`, keeping the triage decisions.
- **Source:** plan.md:989, 959 (2026-10-02), 779; RP:324; IDEAS:718, 725; `docs/ddsonic/upstream-triage.md:23-35, 48`.
- **Status / prior decisions:**
  - HIST: "as the first 1.1 work" (plan.md:989; restated at IDEAS:77, 718 and plan.md:981: "this order is among provider work; the full upstream merge… stays the first 1.1 work").
  - Deferred upstream fixes are waiting for it: Spotify 095a56ec, local playlist ordering and concurrent writers, config concurrent saves, SIGTERM/SIGHUP handling, m4a/opus on the UI goroutine, IPC newline, HTTP body size caps, the YouTube denied sign-in wait, radio pins with two processes, player data races (upstream-triage.md:23-35).
  - The ported fixes are hand ports with no merge ancestry (upstream-triage.md:48).
  - **Requires owner review** against the website-first constraint (b-1).
- **Category:** architecture (maintenance).
- **Dependencies:** none; it informs E-11.
- **Duplicates merged:** all listed sources.
- **Flags:** HIST, OWNER.

### E-11 Long-term cliamp independence, module path and `go install`
- **Outcome:** decide whether keeping upstream's module path and compatibility still earns its cost. That decision decides `go install` support and whether a plain `go build` names the binary `ddsonic`.
- **Source:** IDEAS:692-712, 724; plan.md:963, 988; RP:323; `docs/ddsonic/releases/v1.0.0-rc.3.md:91`; `docs/ddsonic/cli-audit.md:101` (hidden providers stay compiled).
- **Status / prior decisions:**
  - HIST: "Until then the path is intended, not debt" (plan.md:988).
  - IDEAS:692 "does not authorize a module rename or supersede the… first-1.1 upstream merge".
- **Category:** architecture.
- **Dependencies:** E-10, E-01.
- **Duplicates merged:** module naming, `go install`, the binary named `cliamp`, the hidden provider code.
- **Flags:** HIST, OWNER.

### E-12 Capability-based provider architecture
- **Outcome:** formal provider capabilities that the UI follows instead of provider conditionals.
- **Source:** IDEAS:590-613; plan.md:80-83 (M4: "Providers advertise capabilities"); architecture invariant 2 (plan.md:106-111).
- **Status / prior decisions:** the Admission Contract is "the first consumer".
- **Category:** architecture.
- **Dependencies:** none. Prerequisite of E-13.
- **Duplicates merged:** none.
- **Flags:** none.

### E-13 Provider Admission Contract
- **Outcome:** a minimum capability baseline, with authoritative and derived collections modeled separately. Every provider is recorded against it, and "partial provider semantics are never shipped silently".
- **Source:** IDEAS:121-165; plan.md:879, 976-979.
- **Status / prior decisions:**
  - HIST (owner, 2026-10-09): established "before any further provider is integrated"; "Providers are not added first and normalized later".
  - The candidate list is "not a settled contract".
- **Category:** architecture.
- **Dependencies:** E-12. Informed by P-04.
- **Duplicates merged:** none.
- **Flags:** HIST, OWNER.
- **Related:** P-44 (external radio services would be evaluated against it).

### E-14 Rename `library.SyncedSource.PartialAlbums`
- **Outcome:** a name that covers "Albums from tracks" and "Artists from tracks".
- **Source:** plan.md:984, 797; IDEAS:161.
- **Status / prior decisions:** HIST: left alone in 1.0, with "no correctness reason to change reviewed code".
- **Category:** architecture (cleanup).
- **Dependencies:** with E-13 / P-04.
- **Duplicates merged:** all three sources.
- **Flags:** HIST.

### E-15 Replacing the Vorbis dependency: feasibility
- **Outcome:** decide whether `xlab/vorbis-go` (no licence file) can be replaced by a properly licensed decoder, and what that means for the Spotify SDK.
- **Source:** plan.md:993, 970; RP:322, 121, 336; IDEAS:729; `docs/ddsonic/releasing.md:22`.
- **Status / prior decisions:** a post-1.0 feasibility study. HIST: the inherited licensing risk was accepted for 1.0. Kept high-level on purpose.
- **Category:** investigation (architecture).
- **Dependencies:** E-05. The related upstream report is R-06, tracked separately.
- **Duplicates merged:** plan.md:970 (decoder release item) and plan.md:993 (licence replacement), only at the level of "Vorbis decoder dependency work".
- **Flags:** HIST, SEC-SEP (for the related report).

### E-16 Config reader: TOML semantics and inline comments
- **Outcome:** `config.Load` reads `config.toml` as TOML means it: comments after a section or value, and multi-line arrays.
- **Source:** plan.md:972, 990, 810; `docs/ddsonic/files.md:24`; IDEAS:726.
- **Status / prior decisions:** post-1.0 backlog. "A change to an upstream file of about 1,100 lines and to how `setup` and `Save` edit the text."
- **Category:** architecture (bug-adjacent).
- **Dependencies:** E-10 (an upstream-file change).
- **Duplicates merged:** both plan lines and IDEAS:726.
- **Flags:** none.

### E-17 Remove the temporary `audio_device` syntax exception
- **Outcome:** drop `quoteLegacyAudioDevice` and its tests once pre-rc.4 configs no longer need to start unedited.
- **Source:** plan.md:971, 875; `config/syntax_ddsonic.go:135-138` (the only deferred-item TODO in the code); `docs/ddsonic/files.md:23`.
- **Status / prior decisions:** "when that is, is the owner's call".
- **Category:** architecture (cleanup).
- **Dependencies:** none.
- **Duplicates merged:** all sources.
- **Flags:** OWNER.

### E-18 Broad-prefix search speed (P3)
- **Outcome:** faster broad FTS prefixes at 100k tracks.
- **Source:** plan.md:765, 939-943, 986; IDEAS:722; review:288-293.
- **Status / prior decisions:** HIST (owner): "only if it becomes a complaint". `ORDER BY +rank` is a candidate.
- **Category:** architecture (performance).
- **Dependencies:** none.
- **Duplicates merged:** all sources.
- **Flags:** HIST.

### E-19 2026-09-30 review items not visibly closed
- **Outcome:** confirm the status of:
  - P1, unbounded session artwork memory (review:270-276);
  - P2, a full-list allocation on every library redraw (review:278-286);
  - R9, incomplete catalog invalidation (review:250-268).
- **Source:** `docs/ddsonic/reviews/independent-review-2026-09-30.txt`.
- **Status / prior decisions:** not confirmed fixed in the docs read.
- **Category:** investigation (bug).
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** GAP, OWNER (verify).

### E-20 Smaller engineering items left for 1.0
- **Outcome:** reconsider them as a group:
  - `ensureSession` ignores the sync context (upstream code); the same-total edit mid-read; `ArtistAlbums` lists albums outside the library (plan.md:418, 622, 628);
  - partial results when one kind errors, and paging the 4,800-row Library list (plan.md:634);
  - untested last-attempt and `keepRateLimit` paths; the retry sleeps without closing the gate (plan.md:733);
  - per-pixel `At` in scaling, and a 1-in-255 Kitty image-id collision between two instances (plan.md:743);
  - R4 album-membership modeling (plan.md:744, partly addressed by migration 007);
  - the test-only credential guard covers Spotify only and runs on Linux only; the `TestMain` pattern remains in nine packages (plan.md:822-824, 828);
  - staticcheck is pinned to a commit until a release supports it (plan.md:872);
  - the `go.mod` `go` directive stays 1.26.x until upstream raises it or a 1.27 feature is needed (plan.md:944-946).
- **Source:** as listed.
- **Status / prior decisions:** HIST, "not taken" or "left".
- **Category:** architecture (maintenance).
- **Dependencies:** E-10 for upstream-code items.
- **Duplicates merged:** the listed lines.
- **Flags:** HIST, GAP (not in the backlog).

---

# Part 3. Reliability and security

### R-01 Intermittent Linux player test: `TestStopCancelsTheDurationProbeOfAYTDLLoad`
- **Outcome:** a deterministic test, or a player fix, so that a stopped load's duration-probe temp directory is gone when the test checks.
- **Source:** private maintenance records (`player/ytdl_load_ddsonic_test.go:161`).
- **Status / prior decisions:** open.
  - Failed 1 in 4 full `-race` runs at commit e3af1044; passed 30/30 alone.
  - Likely, unverified: the test does not wait for the probe's `release` (`internal/ytdlp/command.go:73-90`).
  - Decide test vs player first.
- **Category:** reliability-security (bug).
- **Dependencies:** relates R-02 and R-03.
- **Duplicates merged:** the macOS occurrence of the same test (R-02 issue 1).
- **Flags:** OOR (no entry in the repository).

### R-02 macOS yt-dlp process lifecycle and temporary-file cleanup
- **Outcome:** establish whether the player ends yt-dlp children and removes their temp directories on macOS, or whether the failures are fixture properties.
- **Source:**
  - private maintenance records of CI runs 41364009703 and 41374046108: `TestStopCancelsTheDurationProbeOfAYTDLLoad` leaves a temporary directory; `TestStopLeavesOtherYTDLRunsAlone` holds the alive pipe to the 10 s teardown deadline; `TestRepeatedSkipsDoNotAccumulateYTDLRuns` times out in teardown; a FIFO `SetReadDeadline` fails in the test fixture;
  - `README.md:49`; ci.yml comment at efbc1eba.
- **Status / prior decisions:**
  - Open and unresolved. Off Linux there is no exit proof (`internal/ytdlp/ytdlptest/hold_unix.go:176-178`).
  - Owner (2026-11-02): resolution is a prerequisite before macOS CI is restored or macOS binaries are distributed (commit efbc1eba).
- **Category:** reliability-security (bug).
- **Dependencies:** R-01. Blocks D-09.
- **Duplicates merged:** the three macOS records.
- **Flags:** OOR (details), OWNER.

### R-03 Possible browser-cookie privacy exposure through leftover yt-dlp temporary folders on macOS
- **Outcome:** establish whether, in real use on macOS, a temporary folder left behind can hold a copy of a browser's cookie database.
- **Source:** `README.md:49`: one player cleanup test "leaves behind a temporary folder where yt-dlp may copy a browser's cookie database; whether that happens in real use on macOS is unverified".
- **Status / prior decisions:** open, unverified in real use.
- **Category:** reliability-security.
- **Dependencies:** R-01, R-02.
- **Duplicates merged:** none. It is kept separate from R-02 because of its privacy side.
- **Flags:** OWNER.

### R-04 Spotify seek-test flake (`error of a replaced read`)
- **Outcome:** closed.
- **Source:** CI run 41377733860; commit 03f9658b.
- **Status / prior decisions:** **RESOLVED** in 03f9658b, now on `main` (efbc1eba). It was test synchronisation, not a playback defect.
- **Category:** bug.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** RESOLVED.

### R-05 Summary redaction collision
- **Outcome:** the acceptance summary's redaction does not rewrite its own wording. On a host whose username is `runner`, "no runner yet" becomes "no <user> yet".
- **Source:** private maintenance records (`acceptance/evidence.go:233-263`).
- **Status / prior decisions:** "Do not fix now" (2026-11-01). It is cosmetic. The test was made host-independent, and production behaviour is unchanged.
- **Category:** bug.
- **Dependencies:** none.
- **Duplicates merged:** both records.
- **Flags:** OOR.

### R-06 Upstream Vorbis report
- **Outcome:** handled by the owner with upstream.
- **Source:** tracked separately. The public checklist names it: "The owner handles the upstream report" (RP:245).
- **Status / prior decisions:** tracked separately; no detail here.
- **Category:** reliability-security.
- **Dependencies:** E-05, E-15.
- **Duplicates merged:** none.
- **Flags:** SEC-SEP.

### R-07 Decoder memory limitation from 1.0
- **Outcome:** follow up the published 1.0.0 known limitation on Ogg Vorbis decoder memory.
- **Source:** `CHANGELOG.md:48` (Known limitations in 1.0.0).
- **Status / prior decisions:** HIST: documented as a 1.0.0 known limitation. Kept high-level.
- **Category:** reliability-security.
- **Dependencies:** E-05, E-15.
- **Duplicates merged:** none.
- **Flags:** HIST, SEC-SEP.

### R-08 Pre-1.0 adversarial security audit: status unknown
- **Outcome:** establish whether the required audit ran for v1.0.0. If not, decide how it is handled now.
- **Source:** plan.md:843 (unchecked: "required, release-blocking, not yet run"); plan.md:870 (2026-10-10, "may not be waived"); RP:252-260.
- **Status / prior decisions:** plan.md still shows it unchecked. Whether and how it was carried out for v1.0.0 is not recorded in this repository; the owner confirms it.
- **Category:** reliability-security.
- **Dependencies:** none.
- **Duplicates merged:** plan.md:819 (the sign-in address in `ddsonic.log` "stays an item for the security audit, with no waiver").
- **Flags:** OWNER, STALE (or GAP).

### R-09 Privacy audit of the public source, docs and artifacts
- **Outcome:** check for personal names, usernames, emails, hostnames, home paths and account or device identifiers in the source, docs, release pages, SBOM, notices, source archive and binary paths.
- **Source:** `docs/ddsonic/releasing.md:42` ("Status (2026-10-19): no audit has been done; the assignment to run it is blocked by a platform denial, unresolved"). The README screenshot is part of the check (D-06).
- **Status / prior decisions:** blocked, unresolved.
- **Category:** reliability-security.
- **Dependencies:** D-06 (the screenshot).
- **Duplicates merged:** none.
- **Flags:** OWNER.

### R-10 Spotify known limitations shipped in 1.0
- **Outcome:** decide which 1.0 limitations get remediation:
  - the refresh-token race at quit (plan.md:860-861);
  - retired sessions piling up during a network fault, with "no bound claimed" (plan.md:856);
  - a track open failing when its session is replaced;
  - whether 1.0 should guarantee recovery from an unobserved network failure, "an open owner question" (plan.md:829);
  - the spurious quarantine after a cancelled open (plan.md:837);
  - the login proof-of-work not honouring cancellation (`releases/v1.0.0-rc.1.md:107`);
  - the best-effort sign-in hyperlink fallback, with no copy key (plan.md:865).
- **Source:** `CHANGELOG.md:44-48`; `docs/ddsonic/catalog.md:49-51`; as listed.
- **Status / prior decisions:** HIST: accepted 1.0 limitations.
- **Category:** reliability-security.
- **Dependencies:** E-05, E-06.
- **Duplicates merged:** the listed lines.
- **Flags:** HIST, OWNER.

### R-11 Findings carried since rc.1 and rc.3
- **Outcome:** decide whether to fix each:
  - the yt-dlp prompt (Ctrl+C quits; Enter runs `pip install`);
  - "sync failed" before setup;
  - LRCLIB busy shows "No lyrics found";
  - a second instance cannot take the MPRIS name;
  - an immediate token-refresh failure gives a sign-in message;
  - a carriage return in the install path gives an invalid launcher entry (LOW).
- **Source:** `docs/ddsonic/releases/v1.0.0-rc.1.md:109-115` (carried at rc.2:81 and rc.3:95); rc.3:97; plan.md:1002, 795; IDEAS:733.
- **Status / prior decisions:**
  - HIST: carried and not fixed.
  - The carriage-return issue was "deferred until after v1.0.0 (owner, 2026-10-05)". IDEAS does not change their severity.
- **Category:** bug.
- **Dependencies:** none.
- **Duplicates merged:** the five rc.1 findings and the rc.3 launcher finding.
- **Flags:** HIST.

### R-12 Audio bounce on track switch and resume
- **Outcome:** no brief drop after a manual switch or on pause/resume.
- **Source:** IDEAS:534-542.
- **Status / prior decisions:** "LOW; post-1.0, as the owner deferred it". A shared pipeline cause is "suspected, not established". YouTube Music and radio are untested.
- **Category:** bug.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** HIST.

### R-13 Raw radio metadata in the now-playing text
- **Outcome:** stations such as 102.5 KZDK and Q104.3 show a title, not raw `key=value` metadata.
- **Source:** IDEAS:544-550.
- **Status / prior decisions:** LOW. Reproduced in upstream cliamp, so possibly fixed by E-10.
- **Category:** bug.
- **Dependencies:** E-10 (check upstream first).
- **Duplicates merged:** none.
- **Flags:** none.

### R-14 yt-dlp and plugin edges not covered by the 1.0 guards
- **Outcome:** decide on:
  - yt-dlp plugins on the user's machine still loading;
  - the Lua plugin API's `exec`;
  - ddsonic being killed while yt-dlp runs;
  - copies left by earlier builds.
- **Source:** plan.md:803-804, 867; `docs/ddsonic/youtube.md:54, 56`.
- **Status / prior decisions:** HIST: "Not covered" (2026-10-10).
- **Category:** reliability-security.
- **Dependencies:** R-03.
- **Duplicates merged:** the listed lines.
- **Flags:** HIST, GAP (no backlog entry).

### R-15 Acceptance coverage: owed live checks, known gaps and open evidence policy
- **Outcome:**
  - The owed live and platform acceptance is done or explicitly retired: browser sign-in in Kitty, Ghostty and tmux; LIVE-SPOT-001 in a fresh home; real librespot recovery and teardown.
  - The known gaps get cases or explicit waivers: setup and upgrade from 0.10, degraded states, Spotify playlist and Artist transitions, YouTube OAuth mode, Local Artists and Genres, artwork, lyrics, queue keys, EQ, terminal sizes, macOS, and the binary lane not in CI.
  - The evidence policy is decided: evidence age, test accounts, permitted account mutations, waiver recording.
- **Source:** `docs/ddsonic/acceptance.md:140-147, 228-248`; plan.md:809, 877, 869; `releases/v1.0.0-rc.3.md:103` (Gate C hands-on scenarios waived).
- **Status / prior decisions:** HIST: the evidence policy was "Not decided, and not to be assumed" (plan.md:877).
- **Category:** reliability-security (QA).
- **Dependencies:** E-04 (proportional QA).
- **Duplicates merged:** the listed sources.
- **Flags:** HIST, OWNER.

---

# Part 4. Distribution, documentation and infrastructure

### D-01 ddsonic website (CONFIRMED owner sequencing constraint: before new product features)
- **Outcome:** the existing Astro ddsonic website completed and launched before new product features start (owner, 2026-11-02).
- **Source:** owner. Repo records: RP:161-170 (Workstream 4), RP:216, 235, 337; plan.md:774, 785, 788, 791-796, 955, 964; waivers in rc.1:123, rc.2:87, rc.3:106; `docs/ddsonic/upstream.md:48-53`; `docs/ddsonic/releasing.md:63, 77`; `README.md:194`.
- **Status / prior decisions:**
  - **The Astro ddsonic website already exists,** developed in a separate project and environment (owner, 2026-11-02). The work is to complete and launch it, not to build it from scratch.
  - **Open question:** its repository and checkout locations are to be confirmed by the owner. It is not in this repository.
  - The repo records describe an earlier plan. HIST: "one hand-written HTML page on GitHub Pages, no generator… serve from `daphnisduck.github.io/ddsonic`… live no later than the v1.0.0 release" (RP:161-170).
  - HIST: "The website stays a requirement before v1.0.0 but not of rc.3" (plan.md:964).
  - HIST: 1.0.0 shipped on 2026-10-31 without a launched site. The README says "A ddsonic website is not launched yet".
  - The inherited Pages workflow must stay disabled (releasing.md:63).
- **Category:** distribution.
- **Dependencies:**
  - owner confirmation of the Astro project's repository and checkout locations;
  - hosting and deployment (the Pages workflow policy above);
  - the demo and screenshot (D-06, D-17).
- **Duplicates merged:** every website line listed above.
- **Flags:** OWNER (locations; what "launched" means), STALE (the HIST "hand-written HTML, no generator" plan is overtaken by the existing Astro site).

### D-02 AUR publication (`ddsonic-bin`)
- **Outcome:** publish the prepared AUR package and update the docs that say "planned".
- **Source:**
  - a package prepared and validated in private maintenance records, not published;
  - `docs/ddsonic/releasing.md:35`; RP:87, 334 (HIST: an AUR channel for 1.0);
  - plan.md:951, 791-796;
  - `README.md:11, 80, 212`; `SECURITY.md:17`; `CHANGELOG.md:12, 16-17, 42`; the 1.0.0 release notes.
- **Status / prior decisions:** prepared and validated. Publication is owner-only and not done. HIST: decided as a 1.0 channel, not delivered.
- **Category:** distribution.
- **Dependencies:** the owner's packaging decisions. D-03 and D-04 follow it.
- **Duplicates merged:** all the "planned" mentions and their post-publication updates.
- **Flags:** OOR (package), OWNER, HIST.

### D-03 `CHANGELOG.md:21` lists the AUR package in the 1.0.0 rename entry
- **Outcome:** the 1.0.0 entry does not name as renamed a package that was never published, or it is explicitly left as history.
- **Source:** `CHANGELOG.md:21`; private maintenance records (an open owner item).
- **Status / prior decisions:** open owner item. Released CHANGELOG sections are not altered without the owner.
- **Category:** documentation.
- **Dependencies:** D-02.
- **Duplicates merged:** both records.
- **Flags:** OWNER.

### D-04 `ddsonic upgrade` error text points to the unpublished AUR package
- **Outcome:** the message does not send users to a package that does not exist, or the package exists.
- **Source:** `commands.go:237` (`errUpgradeDisabled`: "…update it with your package manager (AUR: ddsonic-bin), or download the latest release…").
- **Status / prior decisions:** code unchanged. The text is accurate once D-02 lands.
- **Category:** documentation (user-facing text).
- **Dependencies:** D-02.
- **Duplicates merged:** both records.
- **Flags:** OWNER.

### D-05 `SECURITY.md:31`: `--repo DaphnisDuck/ddmus` attestation check untested
- **Outcome:** confirm that `gh attestation verify --repo DaphnisDuck/ddmus` still verifies rc.1 now that the repository was renamed, or reword the line.
- **Source:** `SECURITY.md:31` (the old repository name now redirects; whether the check still works needs an authenticated run); `docs/ddsonic/releasing.md:84` ("No repository named `ddmus` is ever created").
- **Status / prior decisions:** untested.
- **Category:** documentation (security).
- **Dependencies:** an authenticated `gh` check (owner).
- **Duplicates merged:** none.
- **Flags:** OWNER.

### D-06 README screenshot: stale provenance text and contents
- **Outcome:** the provenance text matches the image, and the image's visible data is acceptable.
- **Source:**
  - `plan.md:794` ("the owner's capture from a clean build of `2bcd0e5`");
  - `docs/ddsonic/release-plan.md:143` ("from a build of `2bcd0e5`");
  - private maintenance records note that the image now shows v1.0.0, that the release archive keeps the earlier image, and the `Ctrl+I Metadata` key-bar question (P-02).
- **Status / prior decisions:** **STALE** (verified at efbc1eba: both lines still name `2bcd0e5`).
- **Category:** documentation.
- **Dependencies:** R-09 (privacy), P-02.
- **Duplicates merged:** both lines and both records.
- **Flags:** STALE, OWNER.

### D-07 Version identification of source builds
- **Outcome:** a source build of `main` reports a sensible version. Today `git describe` picks `v1.0.0-rc.3` and reports `v1.0.0-rc.3-N-g<sha>`.
- **Source:** private maintenance records ("fix before the next release"); `Makefile:1`.
- **Status / prior decisions:** accepted as a limitation for now (owner).
  - Candidates, none chosen: `describe --match` on stable tags or `--first-parent`; derive from `internal/appmeta` plus the commit; or let the next tag land on `main`'s first-parent line.
  - Related: F2, blame and log attribute all 1.0.0 lines to root `9ae61bcf` (use `--first-parent`). No action planned.
- **Category:** infrastructure (bug).
- **Dependencies:** none. Due before the next release.
- **Duplicates merged:** two private records of the same item. The blame note is kept with it.
- **Flags:** OOR, OWNER.

### D-08 Deferred CHANGELOG "Unreleased" entry for disabling macOS CI
- **Outcome:** decide whether to add it.
- **Source:** private maintenance records. Proposed text: "CI no longer runs the macOS build-and-test job. It is disabled pending open macOS issues in how ddsonic runs yt-dlp (process lifecycle, temporary-file cleanup and possible cookie privacy) that its tests found; macOS remains provisional and unverified." It comes with an `[Unreleased]` compare link.
- **Status / prior decisions:** proposed only, and left out of commit efbc1eba because released CHANGELOG sections are not altered without the owner.
- **Category:** documentation.
- **Dependencies:** none.
- **Duplicates merged:** an earlier consistency-scan proposal of the same text.
- **Flags:** OOR, OWNER.

### D-09 Future macOS qualification, CI restoration and distribution
- **Outcome:** macOS is verified, its CI job restored, and possibly a macOS release build.
- **Source:** `README.md:49` (efbc1eba); ci.yml comment (efbc1eba); RP:46 (finding 10), 89-95 ("macOS | Later"), 321; plan.md:987, 781 (HIST: a macOS CI job existed), 876; `docs/ddsonic/acceptance.md:61, 247`; `.github/scripts/build-darwin-amd64-codecs.sh` (referenced by nothing).
- **Status / prior decisions:** provisional and unverified. The prerequisites are R-02 and R-03 (owner, 2026-11-02).
- **Category:** distribution.
- **Dependencies:** R-01, R-02, R-03; a macOS tester.
- **Duplicates merged:** the macOS rows of RP and plan.md:987.
- **Flags:** OWNER. **GAP:** plan.md Status/Decisions has no entry recording that macOS CI was disabled.

### D-10 Other platforms and packaging
- **Outcome:** decide on further platforms and packaging:
  - Linux arm64, Homebrew and `.deb`/`.rpm`, "once someone can test them";
  - a Nix package written and tested for ddsonic;
  - Flatpak, Snap and Windows are recorded as "No".
- **Source:** plan.md:987, 992, 786, 962 (HIST: "Nix packaging stays deferred (owner)"); RP:89-95, 321; IDEAS:723, 728.
- **Status / prior decisions:** HIST.
- **Category:** distribution.
- **Dependencies:** testers.
- **Duplicates merged:** the listed lines.
- **Flags:** HIST.

### D-11 Release provenance for future releases
- **Outcome:** decide whether later releases get a build attestation, or a CI-built or networked release workflow. v1.0.0 has none: it was built locally.
- **Source:** `SECURITY.md:25`; plan.md:832 (not established: a root-owned byte-identical source archive, CycloneDX schema validation, the networked release workflow).
- **Status / prior decisions:** open. No claims are made about 1.0.0 here.
- **Category:** infrastructure.
- **Dependencies:** E-04.
- **Duplicates merged:** none.
- **Flags:** OWNER.

### D-12 Cleanup of temporary release and staging artifacts (owner approval needed for every item)
- **Outcome:** remove what is no longer needed and keep what is durable. Nothing has been removed.
- **Source:** private maintenance records.
- **Status / prior decisions:** every item needs owner approval. The categories are:
  - **remote staging branches** on GitHub: the four `align/v1.0.0-*` branches used to align `main` with v1.0.0, all ancestors of or equal to `main`;
  - **local release-work clones and checkouts**, including ones that keep a push configuration, and a documentation worktree with its branch;
  - **archives and preparation directories** from the 1.0 release work;
  - **durable records to keep**, such as the copy of the published `v1.0.0` tag and the prepared AUR package (D-02).
  - The path inventory is kept in the private records, not here.
- **Category:** infrastructure.
- **Dependencies:** D-02 (keep the prepared package until published). D-01: the Astro site lives in a separate project.
- **Duplicates merged:** none.
- **Flags:** OWNER (each item), OOR.

### D-13 Docs that read as current but are stale after 1.0.0
- **Outcome:** a truth pass so in-repo status text matches the published 1.0.0:
  - `docs/ddsonic/acceptance.md:22` ("No release run has been made; the first is rc.4's") and `:24` ("None has been executed");
  - RP:205-235 unchecked Gate B/C boxes, and RP:111 Go version text (1.26.6 / 1.27.1) inconsistent with RP:100 and README:84 (1.27.3);
  - plan.md Status items still "in designated review, not accepted" or "uncommitted" for work that shipped (plan.md:819, 829, 833, 837, 840) and plan.md:842 (Gate D unchecked);
  - `docs/ddsonic/design/spotify-token-coordinator.md:7-8` ("in review and is not accepted");
  - plan.md:115 sync recipe `git merge upstream/main`, superseded by plan.md:959/989 (release tag, never `main`);
  - `docs/ddsonic/navigation.md:110` (`mono = true` still works) against the M8 removal of mono (HIST, plan.md:534, 731-733);
  - `docs/ddsonic/cli-audit.md:12-55` rows still "to verify".
- **Source:** as listed.
- **Status / prior decisions:** not tracked as a single item.
- **Category:** documentation.
- **Dependencies:** R-08 (the audit status determines the plan.md:843 text).
- **Duplicates merged:** the listed lines.
- **Flags:** STALE, GAP.

### D-14 Agent instructions (`CLAUDE.md`) stale on `site/`
- **Outcome:** the agent docs stop telling agents to keep `site/index.html` in sync; `site/` was removed on 2026-10-04.
- **Source:** `CLAUDE.md:57, 82, 115, 140, 198` versus `CLAUDE.md:13`; `docs/ddsonic/releasing.md:43` (agent-docs cleanup).
- **Status / prior decisions:** agreed and pending.
- **Category:** documentation.
- **Dependencies:** part of E-04. D-01: the Astro site is a separate project, so the agent docs may need to say how it relates to this repo.
- **Duplicates merged:** with E-04's agent-docs cleanup.
- **Flags:** HIST.

### D-15 Upstream cliamp docs not adapted
- **Outcome:** decide whether `docs/` (cliamp's own) is adapted, pruned or left as upstream's.
- **Source:** `README.md:175-182` ("not yet adapted to ddsonic"); plan.md:113-119 (HIST: "Upstream's `docs/` is left untouched to avoid conflicts").
- **Status / prior decisions:** HIST policy.
- **Category:** documentation.
- **Dependencies:** E-10, E-11.
- **Duplicates merged:** none.
- **Flags:** HIST.

### D-16 Repository presentation
- **Outcome:** a social preview image, repository topics and homepage, a favicon and AppStream metadata.
- **Source:** `docs/ddsonic/releases/v1.0.0-rc.3.md:113`; plan.md:964 ("Left out").
- **Status / prior decisions:** HIST: left out of rc.3, with no later decision.
- **Category:** distribution.
- **Dependencies:** D-01 (the homepage).
- **Duplicates merged:** both sources.
- **Flags:** HIST, GAP.

### D-17 Demo and README claim verification
- **Outcome:** an animated `vhs` demo, and a verified README feature list (instant and offline, search operators, background sync, artwork terminals, Radio/Local with no accounts).
- **Source:** RP:151-157 (unchecked); plan.md:955 (HIST: "demo recorded with `vhs`").
- **Status / prior decisions:** no evidence the demo was made.
- **Category:** documentation.
- **Dependencies:** D-01.
- **Duplicates merged:** both sources.
- **Flags:** HIST, STALE.

### D-18 Earlier docs-publication items now resolved
- **Outcome:** closed.
- **Source:** private maintenance records (earlier documentation-publication items): `main`'s code being rc.3-era, and two broken links (README:40 to a `youtube.md` anchor, and a CHANGELOG `youtube.md` anchor).
- **Status / prior decisions:** **RESOLVED** at efbc1eba: `main` carries the 1.0.0 source, and a link check of README (58 links) and CHANGELOG (10 links) found no failures.
- **Category:** documentation.
- **Dependencies:** none.
- **Duplicates merged:** none.
- **Flags:** RESOLVED.

---

# (a) Consolidated duplicates

| Kept as | Consolidated from |
|---|---|
| P-01 sleep timer | owner item, with the owner's detail |
| P-04 YouTube Music Provider v2 | IDEAS:79-119, IDEAS:720; plan.md:645, 726, 797, 879, 880, 911, 917, 976-977; youtube.md:9, 11, 74, 80-87, 135-136; RP:312 |
| P-09 provider-independent library | IDEAS:33-59; plan.md:69, 77, 248, 904 (dedup and merge deferred) |
| P-10 cross-provider playlists | IDEAS:393; plan.md:710; RP:310-320 |
| P-17 genre EQ | IDEAS:291 ("Auto EQ from normalized genre metadata"); RP:310-320 |
| P-21 `X` clears queue | plan.md:985; IDEAS:721; RP:310-320 |
| P-22 / P-23 noise and ambient | "Ambient or generated sounds" (plan.md:974; RP:310-320) split by IDEAS:412, 429 |
| P-24 artwork across terminals | IDEAS:201; plan.md:743, 983; RP:313; artwork.md:21 |
| P-29 ASCII duck visualizer | plan.md:991; IDEAS:727 |
| P-30 favourite / like | plan.md:492, 710 |
| P-36 `music_dir` | plan.md:218, 1007; IDEAS:731; RP:310-320 |
| P-37 offline artwork | plan.md:1008; IDEAS:731 |
| P-38 small UX items | plan.md:674, 714, 797, 834, 847, 873, 957 |
| P-43 queue/session restoration | owner's queue restoration and saved-position resume, kept as one entry; the inherited `internal/resume` is related, not a duplicate |
| P-44 external radio services | owner's Pandora, SiriusXM and AccuRadio idea, kept as one entry; related to P-07, P-08 and E-13, not merged into them |
| E-04 dev/release process | plan.md:1000; releasing.md:43; owner 2026-11-02 |
| E-10 upstream merge | plan.md:779, 959, 981, 989; RP:324; IDEAS:77, 718, 725; upstream-triage.md:23-35, 48 |
| E-11 independence / module | IDEAS:692, 724; plan.md:963, 988; RP:323; rc.3:91; cli-audit.md:101 |
| E-14 `PartialAlbums` | plan.md:797, 984; IDEAS:161 |
| E-15 Vorbis dependency | plan.md:970, 993; RP:121, 322, 336; IDEAS:729; releasing.md:22 (high-level only) |
| E-16 TOML | plan.md:810, 972, 990; files.md:24; IDEAS:726 |
| E-17 `audio_device` | plan.md:875, 971; config/syntax_ddsonic.go:135-138; files.md:23 |
| E-18 search P3 | plan.md:765, 939-943, 986; IDEAS:722; review:288-293 |
| R-01 Linux player flake | a private reliability record; the same test in the macOS records (R-02) |
| R-02 macOS lifecycle | private records of CI runs 41364009703 and 41374046108; README:49 |
| R-05 redaction | two private records of the same item |
| R-08 security audit | plan.md:819, 843, 870; RP:252-260 |
| R-10 Spotify limitations | CHANGELOG:44-48; catalog.md:49-51; plan.md:829, 837, 856, 860-861, 865; rc.1:107 |
| R-11 carried findings | rc.1:109-115; rc.2:81; rc.3:95, 97; plan.md:795, 1002; IDEAS:733 |
| R-15 acceptance | acceptance.md:140-147, 228-248; plan.md:809, 869, 877; rc.3:103 |
| D-01 website | RP:161-170, 216, 235, 337; plan.md:774, 785, 788, 791-796, 955, 964; rc.1:123; rc.2:87; rc.3:106; upstream.md:48-53; releasing.md:63, 77; README:194 |
| D-02 AUR | the prepared package (private records); releasing.md:35; RP:87, 334; plan.md:951, 791-796; README:11, 80, 212; SECURITY:17; CHANGELOG:12, 16-17, 42; the 1.0.0 release notes |
| D-06 screenshot | plan.md:794; RP:143; private records |
| D-07 version id | two private records |
| D-08 CHANGELOG Unreleased | two private proposals of the same text |
| D-10 platforms | plan.md:786, 962, 987, 992; RP:89-95, 321; IDEAS:723, 728 |
| D-14 CLAUDE.md | CLAUDE.md:57, 82, 115, 140, 198; releasing.md:43 |

# (b) Conflicting, outdated or ambiguous decisions needing owner attention

1. **The website: the existing Astro site against the repo's earlier plan.**
   - The confirmed constraint is to complete and launch the existing Astro site, developed in a separate project, before new product features (D-01).
   - Its repository and checkout locations need owner confirmation; it is outside every location searched.
   - The repo's own records still carry the HIST plan, "one hand-written HTML page on GitHub Pages, no generator", due "no later than v1.0.0". That deadline passed without a launch. The HIST plan is overtaken by the Astro site, and the records do not say so yet.
   - The inherited Pages workflow is recorded as one that must stay disabled (releasing.md:63), so hosting and deployment of the Astro site also need a decision.
2. **Three "firsts" for post-1.0, all historical:**
   - "the full upstream merge… the first 1.1 work" (plan.md:989; IDEAS:718);
   - "YouTube Music Provider v2, the first major post-1.0 provider initiative" (plan.md:977);
   - now the confirmed "website before new product features".
   - The website constraint does not strictly conflict with the merge, which is maintenance, not a product feature. The relative order of the website, the upstream merge, and the defect fixes that IDEAS:788 says come first ("Fix defects and usability problems first") is not settled.
   - The YouTube Music v2 "first" is now only a "strong candidate" (owner, 2026-11-02), and IDEAS:794 says the provider order is "already decided, which this approach does not reopen".
   - All of this **requires owner review**.
3. **Non-goals versus the ideas catalog.**
   - plan.md:15-18 keep as non-goals "until explicitly scheduled": "No automatic metadata correction or MusicBrainz enrichment, no recommendations, no smart playlists".
   - IDEAS proposes P-14 (enrichment), P-18 (generated playlists) and P-19 (history and rediscovery).
   - The IDEAS framing ("not a recommendation engine", "transparent rules") may reconcile them. Whether the non-goals still stand is the owner's call.
   - Under the owner's dream-catalog framing, this tension is a note only. All three ideas stay in the catalog.
4. **Provider order versus the Apple Music interest.** HIST: Apple Music comes only after the Admission Contract (IDEAS:69-77, 465). The current interest in Apple Music may argue for an early, cheap feasibility study (P-07) that does not depend on the contract. That would reverse the recorded order and needs an explicit decision.
5. **M4's "one provider per release, starting with YouTube Music"** (plan.md:80-83) is superseded in substance by the 2026-10-09 order (plan.md:976-979) but still stands in the Milestones text.
6. **Upstream sync recipe.** plan.md:115 says `git merge upstream/main`, while plan.md:959/989 say to merge upstream's latest release tag, never `main`.
7. **AUR:** decided as a 1.0 channel (RP:87, 334), prepared but not published. The docs mix "planned" with present-tense mentions (CHANGELOG:21, `commands.go:237`). See D-02, D-03, D-04.
8. **The pre-1.0 security audit** was "required, release-blocking, may not be waived". It is still unchecked in public plan.md:843, and no record of it was found (R-08).
9. **The privacy audit** before publication is recorded as "blocked by a platform denial, unresolved" (releasing.md:42), and the README screenshot shows a local path and a commercial cover (R-09, D-06).
10. **Status text contradicted by the shipped 1.0.0:** acceptance.md:22/24, the RP gates, plan.md:819-842, and the token-coordinator header (D-13); the stale screenshot provenance (D-06).
11. **Spotify SDK.** plan.md:770 ("stays at 0.9.1 until upstream moves") is overtaken by the local patched copy (plan.md:849). The regularization choice (E-05) is coupled with the Vorbis dependency work (E-15) and with the separately tracked upstream report (R-06).
12. **The open question on artwork caching** (plan.md:1008) is effectively answered by M10's cache (P-37).
13. **macOS:** plan.md:781 (HIST) records a macOS CI job. efbc1eba removed it, and plan.md has no Status/Decisions entry for that (D-09).
14. **Key-bar inconsistency:** the screenshot shows `Ctrl+I Metadata`, but navigation.md:116 says such keys are disabled (P-02, D-06).
15. **Local release-work clones** include some that keep a push configuration. They are part of the cleanup decision (D-12).

# (c) Gaps: mentioned somewhere, with no tracked entry

- **Owner items with no record anywhere:**
  - P-01 sleep timer;
  - E-01 architecture review;
  - E-02 the learning guide;
  - E-03 CI performance of the SDK contract suite;
  - E-08 complexity investigation;
  - E-09 the reasoning-level experiment.
- **Recorded only outside the repo** (no entry in plan.md or IDEAS):
  - P-02 sidebar default;
  - R-01 the Linux player flake;
  - R-02 and R-03 macOS lifecycle and cookie privacy (the README now states them, but there is no backlog entry);
  - R-05 redaction;
  - D-07 version identification ("fix before the next release");
  - D-08 the CHANGELOG entry;
  - D-12 artifact cleanup.
- **Recorded as behaviour or deferrals in the repo, with no backlog entry:**
  - P-05 OAuth live search;
  - P-06 YouTube Music and radio artwork;
  - P-30 favourite;
  - P-31 per-band EQ;
  - P-32 YouTube Music in-TUI sign-in;
  - P-38 the small UX items;
  - E-20 the small engineering items;
  - R-14 yt-dlp/plugin edges.
- **Review findings not visibly closed:** E-19 (P1 artwork memory, P2 redraw allocation, R9 catalog invalidation).
- **Housekeeping:**
  - D-09 no plan.md record of the macOS CI decision;
  - D-13 no single "status truth pass" item;
  - D-16 repository presentation left out with no later decision.
- **Website:** the existing Astro site, developed in a separate project, has no record in this repository, and its repository and checkout locations are unconfirmed (D-01).
- **Recorded for the first time in this catalog (2026-11-02):**
  - P-40 column alignment;
  - P-41 word-boundary truncation;
  - P-42 Folders showing audio by default;
  - P-43 queue and session restoration (an investigation; the inherited `internal/resume` covers only a narrow case).
  - Also the owner's P-01 sleep-timer detail.
  - P-44 external radio service integrations (Pandora, SiriusXM, AccuRadio). None of them is named elsewhere in the repository.

# (d) Decisions the owner will eventually need to make (not a ranking)

- **Website (D-01):** confirm the existing Astro project's repository and checkout locations; hosting and deployment; what "launched" means for the constraint.
- **Post-1.0 ordering:**
  - whether the HIST "firsts" still stand: the upstream merge first in 1.1, YouTube Music v2 first among provider work, the Admission Contract before any provider;
  - how they sit with the website-first constraint and with "fix defects first".
- **Feature priorities** among P-01 to P-44, including whether YouTube Music v2 or another feature leads, and whether an Apple Music feasibility study may run before the contract.
- **When P-40 to P-43 are taken up** (none needs a decision now):
  - the defaults for P-42 (audio-only listing, and whether all files can still be shown);
  - P-43 (what is restored by default, and whether position resume is opt-in);
  - P-40/P-41 (how much alignment is worth against width).
- **When P-44 is taken up** (no decision needed now): whether external radio services belong in the Internet Radio interface or a separate provider model, and which of the three services the owner uses and can test.
- **Non-goals** (plan.md:15-18): keep or revise, for enrichment, generated playlists and rediscovery.
- **Upstream and independence:** when to do the full merge (E-10), and whether the module path still earns its cost (E-11).
- **Spotify SDK strategy (E-05)** and the Vorbis dependency direction (E-15).
- **AUR (D-02):** the packaging decisions; publish or not; then D-03 and D-04 wording.
- **macOS (D-09):** whether to pursue R-02/R-03 now, and the condition for restoring CI or shipping a build.
- **Reliability:** test vs player fix for R-01; whether the Spotify 1.0 limitations (R-10) and carried findings (R-11) get fixes.
- **QA policy (R-15, E-04):** evidence age, test accounts, permitted account mutations, waiver recording; which CI guarantees are mandatory per push (E-03).
- **Security and privacy:** the status of the pre-1.0 adversarial audit (R-08); running the privacy audit (R-09); the screenshot's contents (D-06).
- **Release hygiene:** the version-identification fix before the next release (D-07); attestation or a CI-built release for the next release (D-11).
- **Documentation:**
  - whether to add the CHANGELOG Unreleased entry (D-08);
  - whether `CHANGELOG.md:21` stays as history (D-03);
  - the status truth pass (D-13); upstream docs (D-15).
- **Cleanup (D-12):** which staging branches, clones, worktrees, archives and preparation directories to remove or keep.
- **Smaller calls:**
  - `audio_device` shim removal timing (E-17);
  - key-hint default (P-25);
  - sidebar default (P-02);
  - `vis list` behaviour (P-38);
  - E-09 scope and timing.

---

## Counts

- **By part:** 44 product (P-01 to P-44), 20 engineering (E-01 to E-20), 15 reliability and security (R-01 to R-15), 18 distribution, docs and infrastructure (D-01 to D-18). 97 items in all, of which 2 are RESOLVED (R-04, D-18).
- **By category field:** feature 31, architecture 15, investigation 14, documentation 12, reliability-security 10, bug 5, distribution 5, infrastructure 5. Where an item names two categories (for example "feature / investigation"), it is counted under the first.
