# Live-provider acceptance

The scripted cases use no account: Spotify is a fake client inside the test, and YouTube Music a fake `yt-dlp`. They show that ddsonic handles what a provider says. They cannot show that the real services, a real sign-in or the installed `yt-dlp` say what the fakes say. This runbook covers that part, by hand, with real accounts.

**For `v1.0.0-rc.4`, live acceptance of both Spotify and YouTube Music is required.** Neither stands in for the other.

Nothing here has been executed as part of building the acceptance system. A case in this runbook has a result only when someone runs it against a candidate and records it.

## Ground rules

- **The binary** is the candidate's: extracted from the draft release's archive, the one the scripted cases ran on.
- **Read-only.** Every procedure below only reads the account, except [Account changes](#account-changes), which is not to be run until its policy exists.
- **Record what you used:** the date, the `yt-dlp` and `ffmpeg` versions, how each provider was signed in (Spotify: your own client ID or the shared one; YouTube Music: browser cookies or OAuth), and the account type. A result means something only with these beside it.
- **These cases run on the real network,** outside the isolation the scripted cases run in. That is the point of them.
- **Every case here applies to rc.4.** None can be recorded `not applicable`: the catalog grants that, and grants it to none of them.
- **A check that cannot be run is `blocked`,** with the reason in `environment_limitation`: an outage, a sign-in that fails for reasons outside ddsonic, a rate limit. Blocked is not a pass. For rc.4 a blocked Spotify or YouTube Music case still has to be triaged like any other case that did not pass.
- **After a session that used YouTube Music,** check that nothing was left in the temporary folder: no `yt_dlp*` and no `ddsonic-yt-dlp-*` entry that the session made. Look at names only. A `yt_dlp*` folder is a copy of the browser's cookies; one that the candidate left behind is a `fail`, and is reported, not opened.
- **Keep secrets out of what you record.** No tokens, cookies or account identifiers in `detail`. The evidence bundle is redacted before it is sealed, but do not rely on that for something you typed yourself.

Results are recorded as in [human.md](human.md#recording-what-you-find).

## Not configured

These are owner decisions that have not been made. Until they are, the tooling and this runbook assume the strictest reading, and nobody chooses for the owner:

| Policy | Until it is decided |
|---|---|
| How old live evidence may be and still be reused for a later candidate | No reuse: every candidate needs its own live results |
| Which accounts are test accounts, and whether new ones are created | Use only the accounts the owner already uses with ddsonic; create nothing |
| Which changes to an account an agent or a person may make for a test | None: see [Account changes](#account-changes) |
| How the owner's approval of a waiver is recorded | A waiver needs a reference to the owner's own written words; see [../acceptance.md](../acceptance.md#the-gate) |

## Spotify sign-in and sync

*Case LIVE-SPOT-001.*

1. In a home where Spotify is configured but not signed in, open **Spotify** in the library and sign in as the screen says.
2. The sync runs: the heading shows `↻ syncing`, then `✓ synced`.
3. Quit and start again. Spotify's lists are there at once, and no sign-in is asked for.

Pass when the sync completes without an error line and the restart needs no sign-in.

## Spotify collections

*Case LIVE-SPOT-002.*

Compare ddsonic with the Spotify app, on the same account:

1. **Liked Songs:** the count, and the first and last few tracks.
2. **Albums:** the count of saved albums, and a few you know you saved recently and long ago.
3. **Artists:** the artists you follow.
4. **Playlists:** the playlists you own and those you follow, and for two of them the tracks, in order.

These four are authoritative ([semantics.md](semantics.md)): after a sync they hold what the account holds. A difference is a `fail`; say which list and what differs, without naming private playlists.

## Spotify playback

*Case LIVE-SPOT-003. A person, listening.*

Play a track from Liked Songs, one from an album and one from a playlist. Each starts within a few seconds and is audible. Pause, resume and skip work. Leave one playing through a track change.

## Spotify live search

*Case LIVE-SPOT-004.*

1. Press `/`, type an artist you do not have in your library, and choose **Search Spotify for …**.
2. The screen says it is searching Spotify for that query, then shows Spotify's results under Spotify's name.
3. Play one result.

**Do not press `p` on this screen.** There, `p` adds the highlighted track to one of the account's Spotify playlists, or to a new one it creates: a change to the account, made on purpose by the player and not needed by this case. It is allowed only under an explicit authorization for [account changes](#account-changes). `a` and `q` change only ddsonic's own queue.

## Account changes

*Case LIVE-SPOT-005. **Do not run this yet.***

The state-transition checks (change the account, see nothing change, refresh, see the change, restart, undo it) are scripted against fakes. Running them against a real account means changing that account, and **no policy says which changes are allowed, on which account, by whom, or how they are undone.** That is the owner's decision.

Until it is made, this case's result is `not run`, and it is triaged like any other required case that did not pass. Do not save, unsave, like, unlike, follow or edit anything in an account in order to test.

## YouTube Music sign-in and sync

*Case LIVE-YTM-001.*

1. In a home where YouTube Music is configured the way the owner uses it (a browser's cookies, OAuth, or both), start ddsonic.
2. The sync runs and completes: `✓ synced`, with no error line.
3. If the sign-in has expired, the screen says so in words a person can act on. Sign in again; the next sync works without a restart.

Record which sign-in mode was used. The two modes read the account differently, and a result for one says nothing about the other.

## YouTube Music collections

*Case LIVE-YTM-002.*

1. **Liked Music:** compare with Liked Music at music.youtube.com: the count, and the first few tracks. Private and deleted videos are skipped, so a small difference of that kind is expected; say so if you see it.
2. **Playlists:** the playlists of your own that the account's feed lists are here, with their tracks in order. A playlist listed in `youtube_playlists` in the config is here too.

Playlists is **not** claimed to hold every playlist the account follows ([semantics.md](semantics.md)). A followed playlist that is missing is not a failure of this case. A playlist of your own that is missing, or one whose tracks are wrong, is.

## YouTube Music derived lists

*Case LIVE-YTM-003.*

1. Open **Albums from tracks**. Pick two albums in it and confirm that a liked or playlist track of yours belongs to each.
2. Find an album you have saved at music.youtube.com that none of your synced tracks belongs to. It is **not** in the list. That is correct.
3. Do the same for **Artists from tracks** with an artist you follow but have no synced track by.
4. The list, its note, and its rows in All Music and search all call it "from tracks".

Step 2 needs such an album to exist in the account. If there is none, do not save an album to create one, and do not record the case as not applicable: it is `blocked`, with that reason, and is triaged like any other case that did not pass.

## YouTube Music playback

*Case LIVE-YTM-004. A person, listening.*

Play a track from Liked Music and one from a playlist. Each starts and is audible; pause, resume and skip work.

## Radio

*Case LIVE-RADIO-001. A person, listening.*

Open **Radio → Browse Stations**: the directory loads. Play a station and hear it. Pick a station that does not answer, if you find one: it fails with a message and ddsonic stays usable.
