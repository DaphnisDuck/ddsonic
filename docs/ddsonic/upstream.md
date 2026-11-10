# Syncing with upstream cliamp

ddsonic tracks [bjarneo/cliamp](https://github.com/bjarneo/cliamp) so we keep getting its improvements.

## Remotes

```sh
git remote -v
# origin    git@github.com:DaphnisDuck/ddsonic.git
# upstream  https://github.com/bjarneo/cliamp.git
```

If `upstream` is missing, add it, and keep cliamp's tags out of your tag list:

```sh
git remote add upstream https://github.com/bjarneo/cliamp.git
git config remote.upstream.tagOpt --no-tags
git config --add remote.upstream.fetch '+refs/tags/*:refs/upstream-tags/*'
```

cliamp's release tags (`v1.0.0` to `v2.3.0` and later) share names with ddsonic's own, and their release workflow has no repository check: pushed to ddsonic's GitHub, each would publish a cliamp release there. With the two settings above, a fetch files them under `refs/upstream-tags/` (`git log refs/upstream-tags/v2.3.0`), where `git tag` doesn't list them and `git push --tags` doesn't send them. A clone that already has them as tags can drop them with `git tag -d` after the next fetch. GitHub also refuses new tags unless the tag ruleset is switched off for a release; see [releasing.md](releasing.md).

## Sync workflow

The baseline for a sync is upstream's latest release tag, reviewed before it is merged: what it brings is triaged against ddsonic's changes, as in the [pre-1.0 triage](upstream-triage.md). `upstream/main` is never merged. It may be inspected for relevant changes, such as a fix that has not reached a release yet, and such a fix is ported on its own (cherry-picked or rewritten, tagged `// ddsonic:`) after review. This is the owner's standing policy (restated 2026-11-09).

```sh
git fetch upstream                                         # files upstream's tags under refs/upstream-tags/
git log --oneline refs/upstream-tags/<tag>..upstream/main  # inspect only: what upstream has after the tag
git switch -c sync/upstream-$(date +%Y%m%d) main
git merge refs/upstream-tags/<tag>                         # the reviewed release tag; merge, don't rebase: main is public
make check                                                 # gofmt + vet + tests must pass
git push -u origin HEAD                                    # open a PR into main
```

When a merge conflicts, start by searching for `// ddsonic:` markers. Every ddsonic edit to an upstream file carries one.

## Rules that keep merges cheap

- Keep the Go module path `github.com/bjarneo/cliamp`. ddsonic keeps its own files; see [files.md](files.md).
  - Go names an executable after the last element of that path, so a plain `go build` or `go install .` writes `cliamp`, and `go install github.com/DaphnisDuck/ddsonic@<version>` is refused. The Makefile and `packaging/release.sh` name the binary themselves (`-o ddsonic`); `TestMakeNamesTheBinary` holds the Makefile to that, and the release build runs the `ddsonic` it made.
- Put new behavior in new files or packages: `library/`, `ui/model/library_*.go`, `external/spotify/library_browse.go`.
- Keep each necessary edit to an upstream file small and tag it with `// ddsonic:` (or `# ddsonic:` in Makefiles and TOML).
- ddsonic docs go in `docs/ddsonic/`. Leave upstream `docs/` alone.
- Don't reformat or reorganize upstream code opportunistically.

## Upstream files ddsonic removed

These build, install or present cliamp, so ddsonic deleted them rather than carry them (2026-10-04):

- `site/`: cliamp's website and its `install.sh`, which downloads cliamp.
- `flake.nix`, `flake.lock`, `nix/`: the Nix package, which builds this checkout as `cliamp`. ddsonic has no Nix package.
- `cliamp.desktop`, `Cliamp.png`, `Cliamp.svg`, `Cliamp.ico`, `cliamp_windows.rc`, `logo.txt`: cliamp's launcher, logo and Windows icon resource.
- `CNAME`.

When upstream changes one of them, the merge reports a modify/delete conflict. Keep it deleted (`git rm <path>`); a file upstream adds under these paths is deleted the same way. ddsonic's own icon and launcher are new files under ddsonic's names: the artwork in `assets/branding`, the entry in `packaging/linux/ddsonic.desktop`, installed by `packaging/desktop.sh`. A website, when it exists, is added the same way. ddsonic has no Nix package.

The `protocol` and `open` commands' code (`cmd/protocol*.go`, with its `cliamp-url-handler.desktop` template) stays: it is upstream Go code that ddsonic's command line doesn't reach (`cli_ddsonic.go`).

## Upstream Spotify code ddsonic replaced

In `external/spotify`, ddsonic's token coordinator, playback bundle and build phases (the `*_ddsonic.go` files; design in [design/spotify-token-coordinator.md](design/spotify-token-coordinator.md)) replace parts of upstream's `session.go` and `provider.go`. A merge that touches these will conflict; keep ddsonic's side and port what upstream changed into it by hand:

- Removed from `session.go`: `persistingTokenSource`, `webAPITokenSource`, `Session.webApiWithBody`, `awaitSpotifyStream`, the bodies of `NewStream`, `Close`, `Reconnect`, `ReconnectInteractive` and `reconnect` (now in `playback_ddsonic.go` and `session_auth_ddsonic.go`), and the `sess`, `player`, `devID` and `tokenSource` fields of `Session`. `Session.mu` is a plain mutex.
- Removed from `provider.go`: the body of `ensureSession` (now `build_ddsonic.go`), and the retry loop of `webAPIWithBody` (now `request_ddsonic.go`).
- Removed upstream tests, each re-expressed against ddsonic's code: `TestAwaitSpotifyStreamTimeoutCancelsTransportAndReleasesReadLock`, `TestAwaitSpotifyStreamRecoversSetupPanic`, `TestPersistingTokenSourcePersistsRotationOnce`, `TestPersistingTokenSourceDoesNotBlockOnSaveFailure`, `TestWebAPITokenSourcePersistsRotatedRefreshToken` (`session_test.go`) and `TestWebAPITokenRefreshIsBounded` (`token_timeout_test.go`).
- `testmain_test.go` in `external/spotify` and `external/ytmusic` sets `HOME` to a temporary directory; upstream's leaves it alone. Keep ddsonic's: with upstream's, a test that sets no config directory reads and writes the user's own stored sign-in.

## Bumping go-librespot

The contract gate (`make sdk-contract`, [acceptance.md](acceptance.md#the-go-librespot-contract-gate)) is written against one version. A bump of `github.com/devgianlu/go-librespot` in `go.mod`:

1. changes `sdkPin` in `external/spotify/sdkcontract/runner_test.go` in the same change;
2. runs the gate. If the helper or a test in `testdata` no longer compiles against the SDK's internals, port it;
3. reads each failure by what its test says of itself. A test marked **STOP** is an assumption the design depends on: the bump waits until section 4.3 of the design has been re-examined. A test marked as a **reviewed expectation** pins a limitation ddsonic works around; if the SDK has lifted it the test fails with "EXPECTATION CHANGED", ddsonic stays correct, and the expectation is updated in a change the reviewer sees, never silently.

Known behaviour of v0.9.1's access point to look at on every bump (2026-10-13, revised 2026-10-14 with patch `0002`; `external/spotify/playback_ddsonic.go`, `external/spotify/recovery_ddsonic.go`, and the contract tests named `...PeerClose...`, `...InFlightKeyAcrossReconnect`, `...FailedReconnectClosesAccessPoint` and `...CloseEndsTheKeyServiceAndLateAnswers`). Nothing reads the access point's connection until something calls `Accesspoint.Receive`, and the SDK's first caller is the first audio key request: until then pings go unanswered and a close by the peer goes unnoticed. ddsonic therefore calls `Receive` (with no packet types) when it makes a playback bundle. In v0.9.1 a key request that was sent before a reconnect was not sent again, waited out its 15-second deadline and stayed in the key provider until its access point closed; patch `0002` (below) ends it at once with `ConnectionReplacedError` instead, from the completed reconnect, and never sends it again. An access point whose reconnect fails for good closes itself. ddsonic's one recovery per play acts only on those typed outcomes, about the bundle and session build the failed open ran on (`originError`): a replaced connection opens the track once more on the same, healthy session; a closed access point rebuilds that build's playback and opens it once more; a failed write waits for positive evidence on its access point (a newer connection, its close, or the session's owner changing) before it does either; a key refusal keeps the existing silent rebuild and sign-in surfacing. None of it is a drop of a healthy bundle, and nothing is retried twice. If a later go-librespot starts its receive loop on connect, or changes what patch `0002` does, the matching contract tests fail and this is reconsidered in a reviewed change. None of this is reported upstream yet; whether to report it is the owner's decision.

A known defect of v0.9.1 to look at on every bump: `vorbis.Decoder.Close` can abort the process. ddsonic therefore closes no Vorbis decoder, whether its stream was accepted or not (`releaseDecoder` in `external/spotify/playback_ddsonic.go`, and `spotifyStreamer.Close` in `external/spotify/streamer.go`, which closes only MP3 and FLAC), so every opened Vorbis decoder's native memory stays retained until the program exits. About 141 KiB per open was observed with a test fixture and the host's libvorbis, which is not an established cost for every platform and not a measurement of a release binary ([acceptance.md](acceptance.md#decoder-items-corrections-2026-11-10)). The owner accepted that on 2026-10-13 as a documented 1.0 limitation, to be measured on the next candidate (`plan.md`, R9-4 and the Decisions log; [acceptance.md](acceptance.md)). The owner handles reporting it to go-librespot, outside this repository; 1.0 does not wait for an answer. The local replacement (below) does not patch this, and ddsonic does not reach into the decoder's unexported fields. If a later go-librespot fixes it, that exception can go, with a real-decoder test in its place; a fork or a replacement is a separate decision after review.

## The local go-librespot replacement

Since 2026-10-14 (owner decision: a tracked local downstream replacement for 1.0, no fork, nothing published, no upstream contact) `go.mod` replaces `github.com/devgianlu/go-librespot v0.9.1` with `./third_party/go-librespot`, the SDK's own tree checked into this repository, with its own `go.mod` and `go.sum` unchanged, so the build list is the one v0.9.1 selects. What binds it:

- `third_party/go-librespot.provenance`: the upstream module and version, its go.sum hashes (`base-h1`, `base-gomod-h1`; Go authenticates these when the gate downloads the base), each patch in the order it applies with its SHA-256, and `active-h1`, the dirhash of the tracked tree computed locally (it is not an upstream hash).
- `patches/go-librespot/`: the changes, one unified diff each against the base, applied in order. The changed and added files carry a dated modification notice (GPL-3 section 5(a)).
  - `0001-player-guard-cdn-quarantine-map.patch` guards `Player.cdnQuarantine` with a mutex held for each map access alone and never across network work (the CDN-map race, `plan.md`).
  - `0002-audio-key-requests-across-connection-changes.patch` (the owner-authorized SDK change, design revision 10, S-A) changes `ap` and `audio` and adds `internal/keylink`:
    - the access point numbers its connections (`Accesspoint.Generation`: 0 before the first connect, one more for each successful authenticated connect or reconnect, with a channel closed when it moves on);
    - before it publishes a new generation, under its connection lock, it tells the one key provider attached to it (`internal/keylink`, installed by `ap`'s `init`; the first `NewAudioKeyProvider` on an access point owns it, a second one refuses every request with `ErrKeyProviderNotOwner`);
    - `Accesspoint.SendStamped` reports the generation of the connection it selects for a write, before the write is attempted (a stamp, not proof that the packet reached that connection), and a delivery to a subscriber gives up once the access point is done;
    - the key provider is a writer and a receiver over a table of fenced requests: a request is ended once, logically, by its answer, its caller's context (also before it is committed), the access point's `Done`, a write that failed while the access point was open (`KeySendError`, or `ErrAccesspointClosed` if the access point is done by the time that result is decided), the replacement of the connection selected for its write (`ConnectionReplacedError{Sent, Now}`, decided from a completed publication, whether or not the write had completed) or its 15-second window (`DeadlineExceeded`, deferred while a newer connection is announced). Nothing is sent again by the SDK; sequence numbers are not reused (`ErrKeySequenceExhausted`); an answer that still arrives for an ended request is counted and dropped, no longer logged.
    `Send` and `Request` keep their signatures; T-S2 (below) holds the public API to exactly these additions.
  - `0003-audio-prefetch-single-flight.patch` (2026-10-15, design `reader-correction-design-proposal-rev5`, review 41) changes `audio/chunked-reader.go` only.
    - Before, every `ReadAt` started a worker for each of the next three chunks with no check. A worker for a chunk already being fetched waited on that chunk's condition. After a skip, those waiters stayed and drained about one per three seconds per stuck chunk.
    - Now `startPrefetch` reserves the chunk (its `fetching` flag, test-and-set under the chunk's lock) before it starts a worker. A chunk with data or an owner gets none. The worker, `prefetchWorker`, never waits on the condition.
    - On failure the worker releases the reservation and ends its WaitGroup accounting in one critical section, so a later admission of the chunk follows it. On success the cached data forbids another admission.
    - `fetchChunk`, `ReadAt`, `Close` and the retry schedule are unchanged, and no exported symbol changes.
- The gate (`make sdk-contract`) rebuilds the tracked tree from the authenticated base and the patches and requires the two to be identical, both to hash to `active-h1`, and the tracked tree with its patches reversed to hash to `base-h1`. The tests run against a copy of the tracked tree, which is the tree the build and `go mod vendor` use. It then runs the symbols gate, `tools/sdkgate`, a module of its own so that its loader (`golang.org/x/tools` v0.50.0, pinned there, test tooling only) is in no graph ddsonic builds: T-S1 holds the hook, the announcer slot and `ErrTaken` to their whitelisted uses (W0-W3, with the installer's whole form: the callback stored under that access point's lock, and tests only comparing the slot with nil), its source controls (C1-C5, three W1/W3 shape controls and K1-K5, each of which must load) must each be rejected, and T-S2 compares the public API of `ap` and `audio` with the base. Last, two behavioural mutations of the staged tree (B1, B2) must each turn their contract test red.

To change the replacement: edit a copy of the base, regenerate the patch (`diff -uN --label a/<file> --label b/<file>`), apply it to `third_party/go-librespot`, record the patch's SHA-256 and the new `active-h1` in the provenance, and run the gate. Nothing in the module cache is modified, and no developer-local checkout is used. A bump of go-librespot replaces the tree with the new version's and re-applies every patch. The release build's notices and SBOM name the replacement as modified, with its base, its patches and their hashes ([releasing.md](releasing.md#how-the-binary-is-built)); a new patch is listed there once the provenance records it. ddsonic's session code uses it since 2026-10-14 (`recovery_ddsonic.go`).

