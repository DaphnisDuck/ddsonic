# Human exploratory acceptance, and the manual deterministic checks

This runbook holds the procedures of the manual cases in [cases.yaml](cases.yaml) that need no account. The live-provider procedures are in [live.md](live.md). The gate, and how a result is recorded, are in [../acceptance.md](../acceptance.md).

**Order.** The human pass comes last. It starts only after the agent's pass is green: the scripted cases pass on the candidate's binary and the agent-reviewed manual and live cases have results. A person's time goes to what only a person can judge, not to finding what a script would have.

**The binary.** The candidate's own: the binary extracted from the draft release's archive, the same one the scripted cases ran on. Not a local build.

**Your own setup or a scratch one.** The first two sections below are run by an agent in an isolated home (a scratch `HOME`, with `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` inside it, and `DDSONIC_CONFIG_DIR` and `CLIAMP_CONFIG_DIR` unset). Keep that home's path short: the IPC socket's path has a limit ([../files.md](../files.md)). The human sections are run in your real setup, with your real accounts and a terminal you actually use.

## Recording what you find

Each case gets one result: `pass`, `fail`, `blocked`, `not run` or `not applicable`. Write them in a YAML file and give it to the runner with `--manual`:

```yaml
candidate:
  version: v1.0.0-rc.4
  source_sha: <the 40-character commit>
  archive_sha256: <the archive's SHA-256, as SHA256SUMS lists it>
  binary_sha256: <the SHA-256 of the binary you ran>
results:
  - case_id: HUMAN-001
    execution: pass
    executed_by: Steve
    executed_at: 2026-10-12T15:00:00Z
  - case_id: HUMAN-002
    execution: fail
    executed_by: Steve
    executed_at: 2026-10-12T15:20:00Z
    detail: The note is cut off at 80 columns in Ghostty.
```

- `fail` needs a `detail` that says what you saw, in a sentence someone else could act on.
- `blocked` means you could not reach a verdict (no network, an account problem). Say why in `environment_limitation`. **It is not a pass**, whatever the reason.
- **`candidate` is required, and must be the candidate under test exactly.** Take the four values from the acceptance run's own summary of that candidate (or `sha256sum` the archive and the extracted binary yourself). Results recorded on another build, or with any of the four missing, are refused whole: what you saw on rc.3 says nothing about rc.4.
- `executed_at` is a full time with its zone (`2026-10-12T15:00:00Z`), and not in the future.
- `not applicable` is not yours to choose. A case does not apply only where the catalog says so for this release or platform; anywhere else, record `blocked` or `not run` and say why. It will be triaged like any case that did not pass.
- Do not record `pass` for something you did not do. `not run` is an honest answer.

How much a failure matters and what happens next are separate, and go in the triage file: see [../acceptance.md](../acceptance.md#the-gate).

## Radio favorites

*Case RADIO-001. Run by an agent, isolated home, network available for the directory.*

1. Start ddsonic. Open **Radio → Browse Stations** and star one station. Note its name.
2. Open **Radio → Favorites**: the station is listed.
3. Quit, start again, open **Radio → Favorites**: the station is still listed, before anything is fetched.
4. Unstar it. It leaves Favorites at once, and stays gone after another restart.

Pass when all four hold. Quote the station's name and the Favorites rows you saw in `detail`.

## Unified search

*Case SEARCH-006. Run by an agent, isolated home, with at least Local and one synced provider's cached catalog.*

1. Press `/` and type a word that matches a local track and, where you have one, a synced provider's track.
2. Every row says which source it is from. A recording held in two sources appears once per source, each labelled.
3. Nothing is fetched: the results come from the cached catalog (they appear with the network off).
4. The results do not claim to be the provider's own search. That is a separate screen, reached from the "Search <provider> for …" row.

Pass when all four hold.

## Core smoke set

*Case HUMAN-001. About ten minutes, in your real setup.*

1. Start ddsonic from a cold start.
2. Browse each source you have: Local, Spotify, YouTube Music, Radio.
3. Search with `/` for something you know you have.
4. Play one track from each source and **hear** it.
5. Pause, resume, skip to the next track, go back.
6. Open the queue with `Tab`. Queue a track to play next.
7. Quit. Start again: the library is there, and so is what you would expect to have been kept.

Pass when nothing in that list surprised you. Anything that did is a `fail` with a `detail`, even if you can see why it happened.

## YouTube Music derived lists

*Case HUMAN-002. In your real setup, signed in to YouTube Music.*

1. Open **YouTube Music**. Without the docs: is it clear that **Albums from tracks** and **Artists from tracks** are not your saved albums and followed artists?
2. Open each. Do the notes above the rows read well where they sit, at your usual terminal size?
3. Find an album you have saved at music.youtube.com that none of your liked or playlist tracks belongs to. It is not in Albums from tracks. Is its absence explained by what the screen says, or would you have reported a bug?
4. In **All Music** and in search, are these rows told apart from Spotify's and Local's albums?

This case asks for your judgement. The scripted cases already check that the lists hold what they should; only you can say whether a person would understand them.

## Provider search

*Case HUMAN-003. In your real setup, with a recording you have both locally and in Spotify.*

1. Press `/`, type its name, and choose **Search Spotify for …**.
2. While it loads and when the results show: is it unmistakable that these are Spotify's results, for that query?
3. Press `Esc` twice. Are you back in the library search with your query, its rows and your cursor as you left them?
4. Repeat with **Search YouTube Music for …** if you are signed in.

## Exploratory questions

*Case HUMAN-004. No script. Use ddsonic the way you would, and answer these.*

- Starting cold, could you find your way without remembering anything?
- Where does it matter which provider something is from, and is it clear there?
- Is any label correct and still misleading?
- Are there duplicates you cannot explain?
- Does anything clip or wrap badly, at your usual size and at a small one?
- Does playback respond when you ask it to?
- Artwork and the track info view, in Kitty and in Ghostty: right, and right when there is no artwork?
- Moving between the library, the queue, search and track info: do you keep your place?
- **Anything that felt wrong.** This one is required. "Nothing" is an answer; a blank is not.

Record `pass` only if you would be content to publish what you used. Otherwise `fail`, with each thing you found as its own sentence in `detail`.
