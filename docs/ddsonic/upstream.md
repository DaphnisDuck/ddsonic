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

```sh
git fetch upstream
git switch -c sync/upstream-$(date +%Y%m%d) main
git merge upstream/main          # merge, don't rebase: main is public
make check                       # gofmt + vet + tests must pass
git push -u origin HEAD          # open a PR into main
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

A known defect of v0.9.1 to look at on every bump: `vorbis.Decoder.Close` frees the stream's info before it clears the dsp state that points at it, and can abort the process. ddsonic therefore does not close a Vorbis decoder (`releaseDecoder` in `external/spotify/playback_ddsonic.go`), and the decoder's native memory may stay retained. The owner accepted that on 2026-10-13 as a documented 1.0 limitation, to be measured on the next candidate (`plan.md`, R9-4 and the Decisions log; [acceptance.md](acceptance.md)). A report for go-librespot is drafted and **not yet filed**: [upstream-reports/go-librespot-vorbis-close.md](upstream-reports/go-librespot-vorbis-close.md); the owner files it, and 1.0 does not wait for an answer. ddsonic does not patch, fork or replace the SDK for this, and does not reach into its unexported fields. If a later go-librespot fixes the order, that exception can go, with a real-decoder test in its place; a fork or a replacement is a separate decision after review.
