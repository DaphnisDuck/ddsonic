# ddsonic Post-1.0 Ideas and Investigations

> **Status:** Brainstorming and investigation only  
> **Scope:** After the 1.0 release  
> **Important:** Nothing in this document is a committed feature, milestone, release target, or implementation plan unless it is promoted into the project roadmap separately.

This document preserves ideas discussed during development of ddsonic that may be worth investigating after 1.0. Its purpose is to prevent good ideas from being lost while keeping the 1.0 release focused.


This document is the original brainstorming source and rationale for post-1.0 ideas and investigations. The consolidated post-1.0 catalog is [POST-1.0-CATALOG.md](POST-1.0-CATALOG.md), which indexes these ideas together with the project's other deferred work; it is a dream catalog, not a committed backlog. Recording an idea here does not promote it into a roadmap or change the 1.0 feature freeze. See [existing deferred work](#existing-deferred-work) for the items the [project plan](../../plan.md#post-10-backlog) recorded.

## Guiding Principle

ddsonic should remain a **fast, terminal-native music player**, not gradually become a sprawling media-management platform.

Every post-1.0 feature should justify the complexity it introduces.

When evaluating an idea, ask:

- Does this make listening to music better?
- Does it reinforce ddsonic's provider-independent architecture?
- Can it remain understandable and discoverable in a terminal UI?
- Does it introduce significant background machinery, configuration, or operational complexity?
- Could the same benefit be achieved with a smaller design?
- Would removing this feature make ddsonic feel meaningfully worse?

Complexity is a cost, not a sign of maturity.

---

## Architectural Direction

### Music should belong to the user, not the provider

The long-term conceptual model should increasingly treat Spotify, YouTube Music, local files, and possible future providers as **sources of music**, rather than allowing provider boundaries to define the user's library.

Ideally:

- an album is an album;
- an artist is an artist;
- a track is a track;
- a playlist is a playlist;

regardless of where ddsonic can obtain the playable content.

Providers then become implementations capable of satisfying some subset of those objects.

This direction could eventually enable:

- cross-provider playlists;
- source-independent favorites;
- local-first playback resolution;
- graceful provider substitution;
- unified search and discovery;
- richer metadata assembled from multiple sources.

This is a direction, not a requirement to build a giant universal music database.

Radio may reasonably remain a somewhat separate concept because its semantics differ from catalog-based music.

---

## Provider Foundations

Decided on 2026-10-09, after the `v1.0.0-rc.3` report that albums saved in YouTube Music never appeared in ddsonic. The triage found no sync defect: ddsonic had never read saved Library Albums or followed artists, and listed albums and artists derived from synced tracks under the same names, "Albums" and "Artists", that Spotify uses for saved albums and followed artists. 1.0 corrects the wording only ("Albums from tracks", "Artists from tracks"; see [youtube.md](youtube.md#albums-from-tracks-and-artists-from-tracks-enrichment)). The work below is what the triage showed to be missing underneath.

All of it is post-1.0. None of it is required for v1.0.0, and nothing here is built before 1.0.

### Order of provider work

Unlike the ideas further down, this order is an owner decision, recorded in [plan.md](../../plan.md#post-10-backlog):

1. [YouTube Music Provider v2](#youtube-music-provider-v2), together with the provider-contract work it informs. It is the first major post-1.0 provider initiative.
2. The [Provider Admission Contract](#provider-admission-contract) is established and applied before any additional provider is integrated.
3. Further providers ([Additional Providers](#additional-providers), [Apple Music](#apple-music-provider)) come only after each has been evaluated against that contract.

Providers are not added first and normalized later. This order is among provider work only: the full upstream merge remains the first 1.1 work (see [Existing Deferred Work](#existing-deferred-work)).

### YouTube Music Provider v2

The goal is broader than adding saved albums. Re-evaluate the YouTube Music integration as a first-class ddsonic provider whose library semantics are explicit, trustworthy and complete enough for the "all your music in one place" model.

**Where it stands at 1.0.** Playlists and Liked Music sync, through the youtube.com playlists feed with browser cookies (yt-dlp) or the YouTube Data API with OAuth. Albums and artists are derived from those tracks by enrichment, keyed by name, not by a YouTube ID. Neither current ddsonic integration enumerates saved Library Albums, followed artists or playlists saved from others; `youtube_playlists` is the manual workaround, also for a whole album through its `OLAK5uy_` playlist. The cookie playlists feed carries no count to check a short read against.

**Approaches to compare**, at least:

- keep the current stack and fill the missing capabilities with a thin direct YouTube Music library layer;
- adopt a stronger existing YouTube Music client or library;
- implement a narrow ddsonic-owned YouTube Music interface for the capabilities ddsonic actually needs.

One promising architecture to evaluate, not a decision: ddsonic owns the authoritative YouTube Music library semantics and identity, while yt-dlp stays responsible for media and track extraction, where it is strong.

**The design should consider:**

- saved Library Albums;
- followed or saved artists;
- liked tracks;
- playlists and playlist contents;
- complete album contents;
- stable provider IDs for tracks, albums and playlists;
- additions and removals;
- refresh semantics;
- pagination;
- authentication and reauthentication;
- reconciliation;
- authoritative versus derived provenance;
- cache safety;
- failure behavior;
- interaction with yt-dlp.

**Follow-ups gathered here.** These were separate backlog entries; they are now parts of this initiative, and their recorded conditions stand:

- True saved Library Albums: reading the user's YouTube Music library albums, in place of or beside "Albums from tracks". Open questions from the triage: album identity against the name-keyed derived albums, and how the two reconcile.
- Followed artists, in place of or beside "Artists from tracks".
- InnerTube (YouTube Music's private API) investigation: cookie-authenticated and unofficial; authentication, continuation tokens, response parsing and maintenance risk are open. Originally recorded for discovering saved playlists the Data API does not list (2026-09-29), and extended to saved albums and followed artists on 2026-10-09.
- The cookie playlists feed's missing count: guard against a short read reconciling playlists away.
- Known limits recorded in [plan.md's M4 notes](../../plan.md#m4-implementation-plan-youtube-music-v04): the Data API lists few or none of some accounts' playlists; a Testing-status OAuth client loses its sign-in about weekly; the feed does not reliably tell own playlists from saved ones.

A legitimate result for any capability is that it cannot be provided reliably. The [admission contract](#provider-admission-contract) then decides how YouTube Music is presented.

### Provider Admission Contract

Before any music provider is added after 1.0, define a formal minimum baseline of capabilities a provider needs in order to take part honestly in "all your music in one place". The capability lists below are candidates for evaluation, not a settled contract.

**Candidate minimum capabilities:**

- reliable and recoverable authentication;
- saved or liked tracks;
- saved albums;
- playlists, and playlist contents;
- complete album contents;
- stable track, album and playlist IDs;
- additions detected, and removals detected;
- manual refresh, and startup or background refresh;
- complete pagination;
- enough metadata for a unified catalog identity;
- playback;
- safe failure behavior that does not destroy valid cached state.

Followed artists are to be evaluated deliberately, not assumed mandatory.

**Authoritative and derived collections.** The contract must distinguish a collection the provider's library actually holds (authoritative: Spotify's saved albums) from one ddsonic derives from other data (derived: YouTube Music's albums from tracks). The rc.3 YouTube Music Albums and Artists issue is the example: a derived collection was shown under an authoritative collection's name, the sync reported success, and nothing told the user why a saved album was missing. The two kinds have to be modeled separately so that the UI cannot present one as the other.

**How each capability is available.** For every provider, record for each capability which of these supplies it:

- official documented API;
- unofficial or private API;
- maintained third-party library;
- browser or session integration;
- extraction tool;
- unsupported or unknown.

**When a provider cannot satisfy the contract**, the acceptable outcomes are:

- A. ddsonic implements and owns the missing integration;
- B. the provider is presented explicitly as a limited, non-library source, with accurate UI semantics;
- C. the provider is removed from, or deferred on, the roadmap.

Partial provider semantics are never shipped silently under the same UI contract as first-class providers.

**Later investigation.** Express the contract in Go provider capabilities, not only in documentation, so UI behavior follows from it; see [Capability-Based Provider Architecture](#capability-based-provider-architecture). A small related cleanup, done: the flag `library.SyncedSource.PartialAlbums`, which governs both "Albums from tracks" and "Artists from tracks", was renamed `ListsFromTracks` on 2026-11-04 (E-14), after being left alone in the 1.0 fix; this work may still replace it with whatever models derived collections.

Spotify and Local are to be recorded against the contract too, once it exists, so that the baseline describes what ships and not only what is added.

---

## Ideas

### Track Info / Metadata Redesign


**Status:** post-1.0 investigation/design item; unscheduled, not an approved implementation specification.

**Motivation:** long metadata values beside the album artwork can be truncated at the terminal's right edge, with no value scrolling or other way to expose the hidden content. The existing vertical field-list scrolling does not solve this. Investigate the information architecture and responsive layout, not just adding marquee scrolling.

**Investigation scope:**

- Inventory metadata available from Local files, Spotify, YouTube Music and Internet Radio where applicable. Distinguish what each source can supply from what ddsonic currently reads, stores and displays; record missing or conditional fields rather than assuming parity.
- Extend that inventory as additional providers are investigated or restored after 1.0, including Apple Music and inherited providers. This does not approve adding or restoring a provider.
- Determine the common, source-agnostic Track Info core and which richer fields should appear opportunistically when a provider or metadata-enrichment source supplies them.
- Investigate field priority, grouping and placement beside, below or elsewhere around the album artwork.
- Compare additional horizontal space, wrapping, truncation, marquee/scrolling values, adaptive relocation of fields, and expandable or secondary detail presentation. Do not assume marquee scrolling is preferred; consider how users reach the full value under each option.
- Test candidate designs across narrow, typical, wide and very wide terminals, resizing between them, and different album-art sizes, including absent or unsupported artwork. Reuse the responsive-layout size matrix as a starting point, not a fixed design constraint.
- Include unusually long classical titles, works, movements, composer/performer credits, orchestra, conductor, soloists and similarly verbose metadata, alongside sparse metadata.

**Architectural relationships (reuse these workstreams rather than duplicate them):**

- Provider capability modeling and source abstraction: [Capability-Based Provider Architecture](#capability-based-provider-architecture), building on [M4](../../plan.md#m4-multi-provider-expansion), [capability-driven menus](../../plan.md#capability-driven-menus-m41) and the [architecture invariants](../../plan.md#architecture-invariants). Capability decisions belong in the adapters, not provider-specific view branches; the metadata inventory should inform the shared model.
- Metadata enrichment: [Metadata Enrichment](#metadata-enrichment), building on [M4's YouTube Music plan](../../plan.md#m4-implementation-plan-youtube-music-v04), [YouTube enrichment](youtube.md#albums-from-tracks-and-artists-from-tracks-enrichment) and the [catalog](catalog.md). Reuse enrichment results; this item does not independently authorize enrichment or metadata correction.
- Full provider/library unification and source abstraction: [music should belong to the user, not the provider](#music-should-belong-to-the-user-not-the-provider). Keep the Track Info presentation aligned with that shared music model without defining a parallel one.
- Classical music modeling: [Classical Music Metadata](#classical-music-metadata); use its work/movement and performer relationships when available, without deciding the underlying schema in this UI investigation.
- Responsive terminal UI and artwork: [M9](../../plan.md#m9-implementation-plan-responsive-layout-v09), [layout notes](layout.md), [M10](../../plan.md#m10-implementation-plan-album-artwork-in-track-info-v010) and [artwork layout](artwork.md#layout). Investigate changes to field/artwork geometry within this item; protocol and library coverage belongs to [Album Artwork Across More Terminals](#album-artwork-across-more-terminals).
- Additional providers: [Additional Providers](#additional-providers) and [Apple Music Provider](#apple-music-provider) remains the home for provider expansion; this investigation covers how their metadata fits the common presentation.

**Complexity guardrail:** ddsonic models music first and sources second. Richer information should be available when useful without making Track Info provider-specific or visually cluttered. Prioritize comprehension over exposing every field a provider returns: more capable without becoming more complicated.

**Investigation output:** a source/field inventory, candidate layouts and overflow tradeoffs tested against the size/artwork/metadata cases above, and a recommendation with open decisions for review. Field selection, layout, interaction and implementation remain undecided; nothing here changes the 1.0 feature freeze.

---

### Album Artwork Across More Terminals

Investigate whether an alternative terminal-image library, or extending the current approach, could display album artwork on more terminal types. Consolidates the existing Sixel/iTerm2/other-artwork-protocol backlog item; this is a post-1.0 feasibility investigation, not approval to replace a library or promise terminal support.

Use the [current artwork architecture](artwork.md) and [M10 findings](../../plan.md#m10-implementation-plan-album-artwork-in-track-info-v010) as the baseline. Reassess library maturity when investigating; historical library assessments are not permanent exclusions.

Compare candidates on:

- actual terminal/protocol coverage, including Sixel, iTerm2 and candidates such as WezTerm, Konsole and foot, while retaining the existing Kitty/Ghostty behavior;
- integration with Bubble Tea's cell-based rendering, resizing, placement and reliable cleanup without image artifacts;
- capability detection, behavior through tmux, and a clean metadata-only fallback when graphics are unavailable;
- image quality, performance, dependency size, licensing, maintenance and implementation complexity.

Produce a tested terminal/protocol compatibility matrix and a recommendation comparing alternative libraries with extending or retaining the current implementation. Do not assume a library change alone guarantees broader support. Coordinate geometry and fallback behavior with [Track Info / Metadata Redesign](#track-info--metadata-redesign), keeping protocol/library selection in this investigation.

---

### Metadata Enrichment

Existing foundation: [YouTube enrichment](youtube.md#albums-from-tracks-and-artists-from-tracks-enrichment) already supplies catalog metadata. Investigate broader coverage and provenance without replacing that work by default. Track Info presentation is covered [separately](#track-info--metadata-redesign).

Investigate automatically enriching incomplete metadata supplied by local files or providers.

Examples include:

- missing album art;
- incomplete release information;
- composer/work/movement metadata;
- inconsistent genres;
- missing artist or album relationships.

Prefer **automated, read-only enrichment** over turning ddsonic into a tag editor.

Enrichment should:

- never silently overwrite source files;
- never modify provider-owned metadata;
- retain provenance where practical;
- distinguish authoritative source metadata from inferred/enriched metadata;
- account for confidence when matching records.

An open design question is whether enriched metadata should be:

1. persisted in ddsonic's database,
2. cached with expiration/refresh behavior, or
3. resolved dynamically.

Do not decide this merely because one implementation is easiest.

---

### Classical Music Metadata

Investigate first-class representation of classical music.

The normal artist → album → track model poorly represents:

- composers;
- works;
- movements;
- conductors;
- orchestras;
- soloists;
- catalog numbers;
- compositions spanning multiple tracks.

A useful model might recognize a work such as a symphony or concerto as an object containing movements while retaining compatibility with providers that expose those movements as ordinary tracks.

Avoid designing a musicological database for its own sake. The goal is to make actual listening, browsing, searching, and queueing better.

---

### Genre Normalization

Provider and local-file genre metadata can be inconsistent, overly specific, contradictory, or absent.

Investigate a normalized genre layer that can map source metadata into useful ddsonic categories without destroying the original metadata.

Possible uses include:

- browsing;
- searching;
- generated playlists;
- rediscovery;
- automatic EQ selection.

Normalization should be reversible or retain source provenance.

---

### Automatic Genre-Based EQ

Consolidates the existing "Auto EQ from normalized genre metadata" backlog item; depends on [Genre Normalization](#genre-normalization).

If useful genre normalization exists, investigate automatically selecting EQ profiles based on the current music.

This should remain predictable and overrideable.

Avoid behavior where playback suddenly sounds different for reasons the user cannot discover.

---

### Generated / Random Playlists

Explore richer ways to generate listening sessions from the unified library.

Potential dimensions include:

- genre;
- artist;
- era;
- neglected music;
- favorites;
- recently added music;
- listening history;
- combinations of those criteria.

The goal should be useful rediscovery rather than building a recommendation engine merely because recommendation engines exist.

---

### Listening History and Rediscovery

Investigate maintaining enough listening history to enable useful personal-library features.

Examples:

- albums not played in a long time;
- forgotten favorites;
- recently discovered artists;
- frequently skipped material;
- music added but never played.

Prefer transparent rules over opaque recommendation algorithms.

---

### Smarter Queue

Investigate ways the queue can become more useful without becoming unpredictable.

Possibilities include:

- inserting related material;
- continuing playback after the explicit queue ends;
- queue generation from albums, artists, genres, or playlists;
- better manipulation of upcoming items.

The user should always be able to understand why something is playing.

---

### Library Health

Investigate tools for identifying problems in the unified catalog.

Examples:

- missing local files;
- provider items that are no longer available;
- duplicate or suspicious matches;
- unresolved playlist entries;
- incomplete metadata;
- stale cached provider information.

This may become especially useful as ddsonic's source-independent catalog grows.

---

### Offline Awareness

Investigate explicitly representing whether music is currently playable without network access.

This could allow ddsonic to:

- distinguish local and remote availability;
- filter/search for offline-playable music;
- avoid repeatedly attempting unavailable providers;
- make sensible source-resolution decisions while disconnected.

---

### Graceful Provider Failover

If ddsonic knows that multiple providers can satisfy the same logical recording, investigate resolving playback through another source when the preferred source is unavailable.

For example, a track requested through a provider-backed playlist might also exist locally.

Any failover system must be conservative about track identity. Playing the wrong recording is worse than reporting that the requested track is unavailable.

---

### Portable / Cross-Provider Playlists

Consolidates the existing "Cross-source, ddsonic-owned playlists" backlog item; relate identity and resolution to [source abstraction](#music-should-belong-to-the-user-not-the-provider) and [provider failover](#graceful-provider-failover).

Investigate playlists whose identity belongs to ddsonic rather than a specific provider.

A playlist could contain logical music references that resolve at playback time through available providers.

This could allow one playlist to contain music sourced from:

- local files;
- Spotify;
- YouTube Music;
- future providers.

Provider-native playlists should still remain usable without forcing conversion into ddsonic-native playlists.

---

### Generated Noise Source

Together with [Ambient / Nature Audio](#ambient--nature-audio), this expands the existing "Ambient or generated sounds" backlog item into two related investigations.

Investigate a lightweight source capable of generating continuous audio such as:

- white noise;
- pink noise;
- brown noise;
- simple ambient noise.

This may be useful for sleep, concentration, or background listening.

Keep this implementation small. ddsonic should not become an audio synthesis workstation.

---

### Ambient / Nature Audio

Investigate optional access to appropriately licensed ambient material such as:

- rain;
- streams;
- forests;
- ocean;
- similar soundscapes.

Prefer generated audio or clearly licensed/CC0 sources.

Avoid introducing licensing uncertainty or maintaining a large bundled audio collection.

---

### Additional Providers

No provider is added before the [Provider Admission Contract](#provider-admission-contract) exists and the candidate has been evaluated against it; see the [order of provider work](#order-of-provider-work).

Preserve the existing selection constraint: only providers the owner uses and can test; inherited providers hidden for 1.0 are candidates, not promised restorations. Include metadata capabilities in the [Track Info inventory](#track-info--metadata-redesign).

Continue evaluating providers that make sense within ddsonic's architecture.

Provider additions should not require provider-specific behavior to leak throughout the application.

The usefulness of a provider should be weighed against:

- API stability;
- authentication complexity;
- playback restrictions;
- maintenance burden;
- architectural exceptions required.

---

### Apple Music Provider

Investigate whether Apple Music can realistically be supported as a provider.

This is specifically a **feasibility investigation**, not a promised provider. It comes after the [Provider Admission Contract](#provider-admission-contract), and its questions include whether Apple Music can meet that contract.

Questions include:

- API accessibility;
- authentication;
- playback restrictions;
- DRM;
- Linux compatibility;
- whether playback can fit ddsonic's existing architecture without unreasonable hacks.

A legitimate result of the investigation may be **do not implement**.

---

### Three-State Keymap / Key-Hint Display

**Status:** post-1.0 UX investigation; unscheduled, not a committed feature or roadmap item.

Some screens expose enough actions that the keybinding footer consumes substantial vertical space. Investigate extending the existing on/off keymap toggle into three display modes:

- **Minimal:** deliberately selected important/common controls for the current screen, preserving discoverability while reclaiming list space.
- **Full:** the complete set of relevant keybindings currently exposed by the screen.
- **Hidden:** no keybinding footer/help information.

The existing toggle key could cycle through `Minimal → Full → Hidden → Minimal`. Evaluate whether this order is intuitive or another order works better; it is not fixed.

**Default behavior:** compare Minimal's useful guidance and smaller footprint with Full's maximum discoverability for new users. Minimal appears attractive because complex screens would no longer devote several rows to keybinding documentation while still showing enough to operate the interface. Recording this idea does not decide the default.

**Defining Minimal:** each screen should deliberately select its essential actions, rather than take the first N bindings or truncate the Full list. Where applicable, candidates include navigation, play/select/open, back, search and the key that changes or expands the key-hint display. Less common, advanced or contextual actions remain available in Full. Investigate whether existing action/keybinding metadata can express this selection without maintaining duplicated lists or keybinding definitions.

**Persistence and scope:** investigate whether the selected mode should apply globally across screens, persist across sessions and have a configurable default. A global user preference seems desirable, but validate it against the existing UI/configuration architecture. Individual screens may still define which actions qualify as Minimal.

**Relationship to the [Searchable Command Palette](#searchable-command-palette):** a coherent future discoverability model could offer:

- Minimal key hints for everyday actions immediately visible;
- Full key hints for all relevant shortcuts on the current screen;
- a command palette for searchable access to available commands/actions.

If a common command/action registry shared by hotkeys, the command palette and CLI/IPC control is developed, investigate deriving key hints from the same action metadata to avoid independent descriptions of the same actions. Command-palette work is not a prerequisite if three-state key hints can be implemented cleanly on their own.

**Design goals:** preserve useful discoverability, reclaim vertical space, select essential actions by screen, cycle predictably and behave consistently across screens. Reuse existing metadata where practical. Avoid arbitrary truncation, hiding essential navigation from inexperienced users, duplicated definitions and significant architecture for a small UX improvement.

**Investigation output:** screen-specific essential-action selections and a recommendation on cycling order, default mode, preference scope/persistence and metadata reuse. These decisions and implementation remain open; current keymap behavior is unchanged.

---

### Alphabetical Radio Station Sorting

**Status:** COMPLETED, independently reviewed (Reviewer PASS on the final delta, P-27d, `7c67c52`), OWNER-ACCEPTED after a hands-on test on Titan of the candidate `p27d-7c67c52` (2026-11-03), and INTEGRATED into main. Final implementation commit `7c67c52f962b763c2e01a6b6db8b06fbe3f8329b`. PR: [#3 (DaphnisDuck/ddsonic/pull/3)](https://github.com/DaphnisDuck/ddsonic/pull/3), merged 2026-11-03 by fast-forward. Integrated into main at `7934c4d85308f86c102fa6948fe3b17a4209ab4d` (fast-forward; includes a staticcheck test-only correction). Planned release target: v1.1.0 (planned, not released). History: implemented on a local branch (P-27, 2026-11-03) and reviewed in deltas (P-27, P-27b, P-27c, P-27d); see [navigation.md](navigation.md) and the `plan.md` Status entry for what sorts and what keeps its order. Decisions kept: the Genres & Tags categories stay in the directory's popularity order (P-27d); the Top 200 by votes cap stays. Deferred to a roadmap discussion: a choice between the directory's most-voted 200 and all of a country's or a tag's stations ("Popular vs All"), now part of [P-45 Full-directory Radio Browsing](POST-1.0-CATALOG.md#p-45-full-directory-radio-browsing).

Radio station lists currently appear in an unintuitive or effectively arbitrary order. After 1.0, present stations sorted alphabetically by their displayed station name.

**Desired behavior:**

- Sorting is deterministic.
- Sort by the user-visible station name.
- Sorting is case-insensitive, unless the existing UI has an established sorting convention that should be followed instead.
- Apply the behavior consistently anywhere ddsonic presents a comparable list of radio stations, where appropriate.
- Backend, database or provider retrieval order must not determine the visible ordering accidentally.

**Before implementation:** verify whether any radio view currently has an intentional ordering semantic that should be preserved, such as favorites with explicit user-defined ordering. If such a case exists, document it rather than blindly replacing it.

---

### Audio Bounce on Track Switch and Resume

**Status:** LOW; post-1.0, as the owner deferred it earlier. Recorded only: no investigation or engineering now, and it does not reopen `v1.0.0-rc.4`.

Owner-reported: after a manual switch to a new track, playback starts, drops briefly (under a second), then resumes. The owner has since seen the same on Spotify pause and resume, including after a pause of 10-15 seconds, and on local-file pause and resume. YouTube Music and radio have not been tested.

A shared cause in the playback pipeline is suspected, not established.

---

### Raw Radio Metadata in the Now-Playing Text

**Status:** LOW; 1.0 triage, not a mandatory fix. Triage whether a small, isolated fix before 1.0 is appropriate; otherwise it moves to post-1.0. Not investigated; no new release gate is expected.

On some stations the scrolling now-playing text shows the station's raw metadata, space-delimited `key=value` pairs such as `text`, `song_spot`, `cartcutId` and `amgArtworkURL`, instead of a title. Seen on 102.5 KZDK and Q104.3 New York Classic Rock; other stations show normally. It appears inherited rather than an `rc.4` regression, based on the owner's reproduction in original cliamp.

---

### Sign In from a Spotify Play Request

**Status:** LOW; post-1.0 UX enhancement. Not a defect and not a release blocker.

Playing a Spotify track while signed out shows a warning that directs the user to sign in, and the existing **Sign in to Spotify** entry works; per the owner, browser sign-in passed on the `v1.0.0-rc.4` candidate. The enhancement: offer to open the browser sign-in from the play request itself, and optionally resume the requested track once sign-in completes.

This does not resolve LIVE-SPOT-003: its aged-session case stays unresolved.

---

### Local Audio Format Compatibility Audit and Documentation

**Status:** post-1.0 investigation; unscheduled, not a committed feature. Not a product blocker and no new release gate. Recorded only: no investigation, code, dependency or release change.

**Objective:** audit which local-file formats ddsonic supports intentionally and reliably, document them accurately, and identify genuine gaps. It does not assume that formats need adding.

**Owner observations:** in human playback on the private `v1.0.0-rc.4` candidate, sample files with the extensions `.wav`, `.flac`, `.aac`, `.m4a`, `.mp3`, `.opus`, `.ogg` and `.wma` worked, and one `.aiff` sample failed. These are single samples, not universal support for any codec, profile or platform. The codec in the `.m4a` sample (AAC or ALAC) and the codec variant of the `.ogg` sample are unknown and not verified.

The addition priorities proposed earlier (WAV, AAC and M4A high, AIFF medium, WMA low) are withdrawn; none is active or a feature commitment.

**Future investigation:**

1. Scanner extensions: which are recognized explicitly and which only by a fallback.
2. The playback backend for each format: containers as distinct from codecs and profiles, and the dependencies each requires.
3. Artist, album, title, duration and artwork: the limitations of each format.
4. Start, seek, pause and resume, skip, and the natural transition to the next track.
5. Portability of the packaged release on the supported operating systems, as distinct from dependencies present only by accident on a development workstation. This adds no operating-system commitment.
6. The cause of the AIFF failure: extension recognition, the file's encoding, or the decoder. Add support only if that is reasonable.
7. Small, representative regression tests for the formats intended as officially supported, without excessive test infrastructure.

**Outcome:** an accurate list of supported formats, with meaningful container and codec limits. No universal claim rests on one file.

**Constraint:** weigh maintenance, packaging, licensing and dependencies before any change; FFmpeg or any other large dependency needs a justification of its benefit against those costs. This entry makes no claim about whether ddsonic uses FFmpeg today.

---

## Architectural Investigations

### Capability-Based Provider Architecture

Extend the existing [M4 capability-driven menus](../../plan.md#capability-driven-menus-m41) and [architecture invariants](../../plan.md#architecture-invariants), rather than treating capability detection as new. Include metadata availability as well as operations; adapters retain capability decisions outside the view layer.

Investigate formalizing provider capabilities.

Rather than assuming every provider supports every operation, providers could advertise capabilities such as:

- search;
- streaming;
- library browsing;
- favorites;
- playlists;
- playlist modification;
- metadata lookup;
- offline/local playback.

The UI and command layer could then expose functionality based on capabilities rather than provider-specific conditionals.

This could substantially simplify future provider additions.

The [Provider Admission Contract](#provider-admission-contract) is the first consumer: its capabilities, and its split between authoritative and derived collections, are candidates for what a provider advertises.

---

### Searchable Command Palette

Investigate an in-TUI searchable command palette.

A hotkey could open a menu of available actions with fuzzy/search filtering.

Goals:

- improve discoverability;
- reduce dependence on memorized hotkeys;
- avoid permanently cluttering the terminal UI;
- expose less-frequently-used actions without burying them.

Longer term, consider whether:

- hotkeys,
- the command palette, and
- the public CLI/IPC control interface

should consume a common **command/action registry**.

This could prevent the same action from being independently implemented three different ways.

Related UX investigation: [Three-State Keymap / Key-Hint Display](#three-state-keymap--key-hint-display) could complement the palette and reuse common action metadata, without depending on palette implementation.

---

### Daemon / Persistent Playback

Consolidates the existing daemon/headless backlog idea, previously described as likely a 2.0 architectural project. That description is a scale estimate, not a release commitment; the inherited daemon surface remains outside 1.0.

Investigate whether playback should eventually be able to survive the lifetime of the TUI.

Potential benefits include:

- closing/restarting the interface without interrupting music;
- external control;
- better integration with desktop/media controls;
- multiple control clients.

However, this introduces significant architectural complexity.

Questions include:

- process lifecycle;
- IPC;
- ownership of playback state;
- configuration reload behavior;
- upgrades;
- crash recovery;
- debugging;
- cross-platform behavior.

Do not implement a daemon merely because mature music players often have one.

A valid outcome is that keeping playback inside the TUI remains the better design.

---

### Multi-Device Synchronization

Investigate whether any useful form of synchronization across ddsonic installations is worth supporting.

Possible synchronized state might include:

- playlists;
- favorites;
- listening history;
- configuration;
- library metadata.

Actual synchronized playback is a substantially different and harder problem and should not be implied by basic state synchronization.

This area carries a high risk of turning ddsonic into a distributed application. Require a compelling use case before introducing that complexity.

---

### Long-Term cliamp Independence

Related existing decision: [module naming and go install](../../plan.md#post-10-backlog) stays deferred while upstream merge compatibility earns its cost. This broader investigation does not authorize a module rename or supersede the already-recorded first-1.1 upstream merge.

Investigate whether ddsonic should eventually reduce or eliminate its architectural dependence on upstream cliamp.

ddsonic began as a cliamp fork, but its goals and architecture may eventually diverge enough that inherited structures become liabilities rather than useful foundations.

The investigation should include:

- identifying inherited subsystems still closely modeled on cliamp;
- determining which remain appropriate for ddsonic;
- identifying compatibility constraints that no longer provide value;
- evaluating opportunities to simplify or modernize inherited code;
- deciding whether continued upstream conceptual compatibility provides meaningful benefits.

This does **not** imply a rewrite.

Prefer incremental replacement or refactoring when justified.

The goal is ddsonic architectural ownership, not independence for symbolic reasons.

---

## Existing Deferred Work

These items already have a home in [plan.md's post-1.0 backlog](../../plan.md#post-10-backlog); this index preserves visibility without creating parallel specifications. Their recorded decisions and conditions remain in force. In particular, the full upstream merge is already designated the first 1.1 work; the brainstorming status of new ideas here does not unschedule it.

- YouTube Music follow-ups (InnerTube discovery of saved playlists, saved Library Albums and followed artists; the cookie playlist count check) are now parts of [YouTube Music Provider v2](#youtube-music-provider-v2).
- Clear the whole queue with `X`, with Ctrl+Z undo; related to [Smarter Queue](#smarter-queue).
- Broad-prefix search speed, only if it becomes a complaint.
- Linux arm64, macOS, Homebrew and `.deb`/`.rpm`, once someone can test them.
- Module naming and `go install`; related to [Long-Term cliamp Independence](#long-term-cliamp-independence).
- Full upstream merge of the latest release tag, not `main`, as the first 1.1 work; preserve [upstream-triage decisions](upstream-triage.md).
- TOML inline comments.
- A ddsonic ASCII-duck visualizer.
- Nix packaging written and tested for ddsonic.
- A properly licensed replacement for `xlab/vorbis-go`, including feasibility of `jfreymuth/oggvorbis` and go-librespot changes.

Also retain [plan.md's open questions](../../plan.md#open-questions): separating Local's scan folder from the file browser's start folder, and catalog/offline artwork storage. The existing [artwork cache](artwork.md) already stores downloaded images; clarify any additional offline guarantee before treating artwork caching as new work.

Release findings remain in their release records: [rc.1 carried findings](releases/v1.0.0-rc.1.md) and [rc.3's deferred carriage-return install-path issue](releases/v1.0.0-rc.3.md). This catalog does not change their severity, waivers or release gates.

---

## Explicit Non-Commitments

The following distinction is important:

**An idea appearing in this document does not mean it will be implemented.**

In particular, this document does not commit ddsonic to:

- Apple Music support;
- daemon mode;
- multi-device synchronization;
- provider failover;
- a metadata service;
- classical-specific database architecture;
- automatic recommendations;
- synchronized playback;
- severing all cliamp-derived architecture;
- any particular post-1.0 release schedule;
- any particular YouTube Music architecture, or that every capability in the [Provider Admission Contract](#provider-admission-contract)'s candidate list ends up mandatory.

Some investigations should end with a deliberate decision **not to build the feature**.

That is a successful outcome.

---

## Evaluation Framework

Before promoting one of these ideas into the roadmap, answer:

1. **User value:** What concrete listening problem does this solve?
2. **Architectural fit:** Does it reinforce or fight ddsonic's existing design?
3. **Complexity:** What permanent machinery must exist afterward?
4. **Discoverability:** Can users understand and find the feature?
5. **Predictability:** Can users understand what ddsonic is doing?
6. **Maintenance:** What external APIs, services, protocols, or data must remain functional?
7. **Failure behavior:** What happens when the feature or dependency breaks?
8. **Smaller alternative:** Is there an 80% solution with 20% of the architecture?
9. **Exit cost:** Could the feature be removed later without destabilizing the application?
10. **Identity:** Does this still feel like ddsonic?

---

## Suggested Post-1.0 Approach

Do not treat this document as the 1.1 backlog.

After 1.0:

1. Gather actual user experience with the released application.
2. Fix defects and obvious usability problems first.
3. Identify architectural friction revealed by real usage.
4. Select a small number of investigations from this document.
5. Prototype when uncertainty is high.
6. Promote only validated ideas into the roadmap.
7. Continue aggressively rejecting unnecessary complexity.

Provider work has an order already decided, which this approach does not reopen: see [Provider Foundations](#provider-foundations).

The best post-1.0 ddsonic is not necessarily the version with the most features.

It is the version where the music player feels increasingly coherent while its internals become easier, rather than harder, to understand.
