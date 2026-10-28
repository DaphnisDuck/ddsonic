# Product semantics: what each list promises

Every acceptance case is written against a row here. A case that expects more than a row promises is wrong, and so is a case that expects less. The same rows, with their IDs, are the `semantics:` section of [cases.yaml](cases.yaml), which `make acceptance-validate` checks every case against.

This describes what ddsonic 1.0 shows today. It is not a contract for future providers.

## Three kinds of list

- **Authoritative.** The list is a copy of something that exists elsewhere as a list: the files in a folder, the albums an account has saved. After a successful sync it holds what the source holds.
- **Derived.** ddsonic builds the list from something else it synced. Nothing at the provider corresponds to it, so it is never complete, and a change in the account's own lists does not reach it.
- **Live.** The list is read from the network when you open it. Nothing is cached.

## The lists

| Provider | List | Kind | It holds | Known limits |
|---|---|---|---|---|
| Local | Albums, Artists, Genres | Authoritative | The tagged files under the music folder | A missing folder, or an empty one while the catalog still holds files, fails the sync and keeps the catalog |
| Spotify | Liked Songs | Authoritative | The account's saved tracks | — |
| Spotify | Albums | Authoritative | The account's saved albums | — |
| Spotify | Artists | Authoritative | The artists the account follows | — |
| Spotify | Playlists | Authoritative | The playlists the account owns or follows | — |
| YouTube Music | Liked Music | Authoritative | The account's liked tracks, as synced | — |
| YouTube Music | Playlists | Authoritative for what it reads | The playlists in the account's current feed, plus those named in `youtube_playlists` | **Not** claimed to be every playlist the account follows |
| YouTube Music | Albums from tracks | **Derived** | The albums named by the details of synced tracks | Incomplete by nature; see below |
| YouTube Music | Artists from tracks | **Derived** | The artists credited on synced tracks | Incomplete by nature; see below |
| Radio | Favorites | Authoritative | The stations starred on this machine | — |
| Radio | Browse Stations | Live | The station directory | Needs the network |

## YouTube Music's derived lists

These four statements are what the cases check, and what the rc.3 report got us to write down:

1. Saving an album that no synced track represents does **not**, by that alone, make it appear in Albums from tracks.
2. An album that a synced track represents **may** appear whether or not it is saved.
3. Following an artist that no synced track credits does **not**, by that alone, make it appear in Artists from tracks.
4. An artist that a synced track credits **may** appear whether or not it is followed.

A check of these is only worth something if it is not vacuous. "The saved album is absent" proves nothing on an empty list, or before the sync and the reading of track details have finished. So every such case pairs the album that must be absent with one that must be present, and asserts after a known completion point.

## Search

- **Ordinary search** (`/`) reads the cached catalog of every source. It finds what has been synced, and each row says which source it is from.
- **A provider's own search** ("Search Spotify for …") asks that provider's service. It is a separate screen, and it names the provider and the query while it loads and in its results.

The two are different things and must not be mistaken for each other on screen.

## What holds for every synced list

These are the state-transition checks. A case applies the ones that fit its source.

| Check | What is asserted |
|---|---|
| Quiescent baseline | No sync is running and none is due before "nothing has changed yet" is asserted |
| No spontaneous change | After the source changes, the list is unchanged until a sync |
| Refresh | After a sync the list holds the source's new state |
| Persistence | After a restart the cached list is shown **before** the source is read again |
| Reversal | Undoing the change and syncing removes it again |
| Idempotence | A second sync with nothing changed adds nothing and duplicates nothing |
| Stable identity | An item that survives keeps its catalog identity across syncs and restarts |
| Order and repeats | A playlist keeps its order, and a track listed twice stays twice |
| Isolation | Other collections and other providers are untouched |
| Failure | A collection whose sync fails keeps its cache, **and** the failure is shown |
| Recovery | The next successful sync works with no restart |
| Cancellation | A cancelled sync commits nothing incomplete or stale |

Collections commit one by one. When one collection syncs and another fails, the first is updated and the second kept: no case expects a provider-wide rollback.
