# macOS ARM64 build of v1.0.0: validation strategy

This is the plan for building and validating a macOS ARM64 (Apple Silicon, `darwin/arm64`) build of the existing `v1.0.0` release. The target and its conditions are the owner's decisions on [#88](https://github.com/DaphnisDuck/ddsonic/issues/88). Those decisions control; this file only describes how the work is done.

Nothing here authorizes publication. Building, testing and packaging produce local files on the owner's Mac. Uploading anything to the `v1.0.0` release, or changing that release in any other way, needs a separate owner decision. Every command in [The Mac commands](#the-mac-commands) runs only after the owner has approved that exact command. macOS remains provisional and unverified until the acceptance criteria on #88 are met and the owner says so. This file is not a support statement.

## What is built, from what

- **Application source:** the `v1.0.0` tag (tag object `79ba942dfcdc12a1ad6a02f886b86d5a24e563bc`), commit `9ae61bcf6604e032b6e690394d10cb0bade80543`, unchanged. The build archives exactly that commit's tracked files. `ddsonic --version` and `RELEASE-MANIFEST.json` name that commit.
- **Difference from the Linux binary:** the published Linux binary names a different commit, the private build commit described in [SOURCE-PROVENANCE.md](https://github.com/DaphnisDuck/ddsonic/blob/v1.0.0/SOURCE-PROVENANCE.md). That file explains that the public tag commit is a documentation-adjusted copy of the Linux build's source and differs from it in documentation only. So the two binaries of `v1.0.0` name two different commits, and the macOS binary's commit is the public one. Any release text that presents both must say so.
- **Tooling outside the tag:** the tag holds no macOS build tooling. The build also depends on:
  - `packaging/darwin-arm64/` at a reviewed commit of this repository;
  - four codec source releases, verified by SHA-256;
  - Go 1.27.3 for `darwin/arm64`;
  - Apple's Command Line Tools (compiler, linker, SDK);
  - `pkg-config` and `jq`.

  `RELEASE-MANIFEST.json` records each of them. The build is **not reproducible from the tag alone**, and nothing should describe it as if it were.
- **Build host:** the owner's Apple Silicon (M3) Mac. No CI workflow builds or tests macOS.

## The tooling

`packaging/darwin-arm64/` (POSIX `sh`, linted by `make` with the pinned shellcheck):

| Script | What it does |
|---|---|
| `codecs.sh <prefix>` | Builds static arm64 `libogg` 1.3.5, `libvorbis` 1.3.7, `flac` 1.5.0 (libFLAC only) and `mpg123` 1.32.6 from verified source releases into `<prefix>`. It keeps the source archives, the license files and `codecs.txt` (name, version, URL, SHA-256). It refuses Rosetta, a non-empty prefix, and any dynamic or non-arm64 library. |
| `release.sh <version> <prefix> <out>` | Builds the tag's commit. Details in the list after this table. |
| `notices.sh`, `sbom.sh` | Called by `release.sh`. Their Go-module parts follow the Linux release's `packaging/notices.sh` and `packaging/sbom.sh` step for step, ported to macOS tools; those Linux scripts are not changed. |
| `test-tree.sh <version> <dir> [<fixture-commit>]` | Prepares a test tree: the tag as tagged, or the tag with the corrected test fixtures. See [Test method](#test-method). |

`release.sh` builds the tag's commit with the Linux release's Go flags (`-a -trimpath -mod=vendor`, `-s -w -buildid=`, `-X main.version`, `-X main.sourceSHA`, the same static codec link flags), from a clean tooling checkout. It then checks the result:
- the binary is arm64 only (`lipo`);
- it loads nothing from outside `/usr/lib/` and `/System/Library/`, and no codec library dynamically (`otool -L`);
- its signature verifies (`codesign`);
- `--version` reports the tag and its commit;
- no file names the machine, its user or a home directory.

It writes `RELEASE-MANIFEST.json` and an archive with a fixed order, owner and time, then the SBOM, the notices and `SHA256SUMS-darwin-arm64`.

**Why not reuse `.github/scripts/build-darwin-amd64-codecs.sh`:** it is upstream's Intel recipe. It cross-builds x86_64 with x86_64 flags throughout and is referenced by nothing. Intel macOS is outside this target, and ddsonic keeps upstream files unchanged where it can, so that merges stay cheap. The arm64 script uses the same four source releases at the same SHA-256 sums. It lives with the packaging it serves, not under `.github/`, because no workflow runs it.

**Checked on Linux, before any Mac run** (2026-11-08):
- `sh -n`, `bash -n` and the pinned shellcheck 0.9.0 pass on all five scripts.
- `test-tree.sh` runs in both modes. With the overlay, only the nine listed files differ from the tag, plus the record file.
- The four codec source archives match their pinned SHA-256 sums. Their license files are `COPYING`, except FLAC's `COPYING.Xiph`.
- `notices.sh` and `sbom.sh` were run on a Linux build of the tag, using a stand-in for `otool`. The Go-module section of the notices is byte-identical to the published `ddsonic-1.0.0-THIRD_PARTY_NOTICES.txt`, and the SBOM's 76 Go components equal the published SBOM's.

Nothing macOS-specific was built or run.

## Test method

The `v1.0.0` tests are known to fail on macOS CI runners in some player cleanup tests. Later commits on `main` changed only test code there, to make tests independent of the host and to synchronize their teardown. No production defect has been shown. The method keeps those two possibilities apart.

1. **Original:** the tag's own tests against the tag's own code, exactly as tagged (`test-tree.sh v1.0.0 <dir>`). Every result is recorded, failures included. This is the baseline, and it is never replaced by a later run.
2. **Corrected fixtures:** the same production code, with these test files replaced by their versions at a stated later commit (`test-tree.sh v1.0.0 <dir> <commit>`):
   - `acceptance/acceptance_test.go`
   - `external/spotify/prefetch_ddsonic_test.go`
   - `external/spotify/testguard_ddsonic_test.go`
   - `internal/ytdlp/ytdlptest/` (a test-helper package)
   - `player/ytdl_load_ddsonic_test.go`

   The script refuses the overlay when:
   - a listed file is not a test file or part of the helper package;
   - any non-test code imports the helper;
   - the program's own dependencies change.

   It records each file's SHA-256 before and after. This set was checked on Linux against `main` at `7d7a643369086f4936c2566bba581c8d86a858ed`. With the tag's production code, the overlaid packages (`internal/ytdlp/...`, `player`, `external/spotify`, `acceptance`) compile and their tests pass on Linux. That says nothing yet about macOS.
3. **Classification of each failure:**

   | Original | Corrected fixtures | Classification |
   |---|---|---|
   | fails | passes | test-fixture or synchronization defect: the test, not the program |
   | fails | fails | not explained by the fixtures: investigate; possibly a production defect |
   | passes | fails | regression in the corrected fixtures: report, do not count |
   | passes | passes | pass |

   A failure is rerun at most three times, alone (`-run '^Name$'`), and every rerun is recorded. A test that fails and passes in turn is recorded as flaky with all its results.
4. **Stop condition:** if a credible validation would need a change to the `v1.0.0` production code, the work stops and the owner decides. `main`'s production code is never substituted.
5. **What counts:** which tests make up "the agreed test suite" for acceptance criterion 2 on #88, and how each classification above is treated, are the owner's decisions (see [Open decisions](#open-decisions)).

### Test record template

Records are kept privately, outside the repository. #88 gets a summary that follows the privacy rules.

```
Run: <id>   Date (UTC): <…>   Approved command: <step number>
Tree: original v1.0.0 (9ae61bcf…) | corrected fixtures from <commit>
FIXTURE-OVERLAY.txt sha256 (corrected only): <…>
macOS: <sw_vers -productVersion>   Go: <go version>   CLT/SDK: <xcrun --show-sdk-version>
Command: <exact>   Exit status: <n>   Output file sha256: <…>

| Package | Result | Failing tests | Reruns (result each) | Classification | Note |
|---|---|---|---|---|---|
```

## The Mac commands

Each step is shown to the owner with its exact text and is run only after the owner approves it. Steps run in order, in one native arm64 Terminal shell (not Rosetta). Every file goes under one work directory outside the home directory, which keeps the user's name out of paths and makes cleanup one command.

`<tooling-commit>` is the reviewed commit that holds `packaging/darwin-arm64/`. The owner's approval names it exactly. `<fixture-commit>` is the commit the corrected fixtures come from; the proposal is `7d7a643369086f4936c2566bba581c8d86a858ed`, the commit they were checked at.

**Step 0. Record the environment.** Read-only.
```sh
sw_vers; uname -m; sysctl -n sysctl.proc_translated
xcode-select -p; clang --version | head -n 1; xcrun --show-sdk-version
command -v git curl shasum tar pkg-config jq; git --version
```
- **Purpose:** the record's environment lines, and the prerequisites.
- **Prerequisites:** Xcode Command Line Tools (`xcode-select --install` if missing).
- **Expected:** `arm64`, `0` (not translated), a CLT path. `pkg-config` or `jq` may be missing; step 1 covers that.
- **Risk:** none. Do not paste output that includes the machine's name.

**Step 1. Missing tools only.** Run only for what step 0 found missing; Homebrew changes are the owner's call.
```sh
brew install pkgconf      # provides pkg-config
brew install jq           # only if /usr/bin/jq is absent
```
- **Purpose:** `pkg-config` resolves the codec libraries for cgo. `jq` writes the manifest and SBOM.
- **Expected:** both found. Their versions go in the record, and `pkg-config`'s also in the manifest.
- **Risk:** low; it changes the owner's Homebrew installation. The codec build reads only its own prefix's `.pc` files, so Homebrew's libraries cannot be linked by accident, and `otool -L` would catch it.

**Step 2. Work directory, Go 1.27.3, the repository.**
```sh
export W=/tmp/ddsonic-arm64
mkdir -p "$W/records"
ls -A "${TMPDIR:-/tmp}" | wc -l > "$W/records/tmpdir-before.txt"
curl -fsSL -o "$W/go.tgz" https://go.dev/dl/go1.27.3.darwin-arm64.tar.gz
echo "dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2  $W/go.tgz" | shasum -a 256 -c -
tar -C "$W" -xzf "$W/go.tgz"
export PATH="$W/go/bin:$PATH" GOPATH="$W/gopath" GOCACHE="$W/gocache" GOTOOLCHAIN=local
go version
git clone https://github.com/DaphnisDuck/ddsonic.git "$W/ddsonic"
git -C "$W/ddsonic" checkout --detach <tooling-commit>
git -C "$W/ddsonic" rev-parse v1.0.0 'v1.0.0^{commit}'
```
- **Purpose:** an isolated Go of the pinned version, and the tooling with the tag.
- **Expected:** the checksum line says OK. `go version go1.27.3 darwin/arm64`. The last line prints `79ba942dfcdc12a1ad6a02f886b86d5a24e563bc` and `9ae61bcf6604e032b6e690394d10cb0bade80543`.
- **Risk:** low; about 80 MB downloaded and nothing installed outside `$W`. The variables last for this shell only, so later steps run in the same shell, or after re-exporting them.

**Step 3. Codec libraries.**
```sh
cd "$W/ddsonic" && sh packaging/darwin-arm64/codecs.sh "$W/codecs" 2>&1 | tee "$W/records/codecs.log"
```
- **Purpose:** static arm64 codec libraries for the build and for the tests.
- **Expected:** four `OK` checksum lines, and `codecs.txt` printed at the end.
- **Risk:** it downloads about 3 MB from xiph.org and mpg123.de and compiles for a few minutes. This recipe has not run on arm64 before, so a configure or compile failure is possible. If one happens: stop and report, with no retries and no changes.

**Step 4. Original tests.**
```sh
cd "$W/ddsonic" && sh packaging/darwin-arm64/test-tree.sh v1.0.0 "$W/t-original"
cd "$W/t-original" && PKG_CONFIG_LIBDIR="$W/codecs/lib/pkgconfig" CGO_ENABLED=1 \
  go test -count=1 -race ./... > "$W/records/tests-original.txt" 2>&1; echo "exit $?" >> "$W/records/tests-original.txt"
grep -E '^(ok|FAIL|---|panic)' "$W/records/tests-original.txt"
```
- **Purpose:** the baseline, using the same `go test` flags as CI (`-count=1 -race`).
- **Expected:** per-package results. Failures in some player cleanup tests are possible and are recorded, not fixed.
- **Risk:**
  - It downloads the Go modules the tag's `go.sum` pins, into `$W/gopath`.
  - Tests start helper processes and may leave temporary files under `$TMPDIR` when they fail. Step 9 compares the directory's entry count with step 2's.
  - Test output can contain local paths, so the record stays private.
  - Expected to take several minutes. A hang is stopped with Ctrl-C and recorded as such.

**Step 5. Corrected-fixture tests.**
```sh
cd "$W/ddsonic" && sh packaging/darwin-arm64/test-tree.sh v1.0.0 "$W/t-fixtures" <fixture-commit>
cd "$W/t-fixtures" && PKG_CONFIG_LIBDIR="$W/codecs/lib/pkgconfig" CGO_ENABLED=1 \
  go test -count=1 -race ./... > "$W/records/tests-fixtures.txt" 2>&1; echo "exit $?" >> "$W/records/tests-fixtures.txt"
grep -E '^(ok|FAIL|---|panic)' "$W/records/tests-fixtures.txt"
```
- **Purpose:** the same production code, with corrected test fixtures.
- **Expected:** `FIXTURE-OVERLAY.txt` printed with nine files, then per-package results.
- **Risk:** as in step 4.

**Step 6. Targeted reruns.** Only for failures from steps 4 and 5; at most three per test.
```sh
cd "$W/t-original" && PKG_CONFIG_LIBDIR="$W/codecs/lib/pkgconfig" CGO_ENABLED=1 \
  go test -count=1 -race -run '^<TestName>$' ./<package> 2>&1 | tee -a "$W/records/reruns.txt"
```
(the same with `$W/t-fixtures`)
- **Purpose:** separate a consistent failure from a flaky one.
- **Risk:** as in step 4.

**Step 7. Build the file set.**
```sh
cd "$W/ddsonic" && sh packaging/darwin-arm64/release.sh v1.0.0 "$W/codecs" "$W/out" 2>&1 | tee "$W/records/build-1.log"
```
- **Purpose:** the archive, SBOM, notices and `SHA256SUMS-darwin-arm64`.
- **Expected:**
  - a privacy line with 0 machine, user or home-directory strings;
  - the manifest, naming `9ae61bcf…`, `darwin`, `arm64` and `go1.27.3`, with `"reproducible_from_tag_alone": false`;
  - three checksum lines.
- **Risk:** low; it writes only `$W/out` and a temporary directory. Any failed check stops it, with nothing kept.

**Step 8. A second build, compared.**
```sh
cd "$W/ddsonic" && sh packaging/darwin-arm64/release.sh v1.0.0 "$W/codecs" "$W/out2" > "$W/records/build-2.log" 2>&1
diff "$W/out/SHA256SUMS-darwin-arm64" "$W/out2/SHA256SUMS-darwin-arm64" && echo "same bytes"
```
- **Purpose:** whether two builds on the same machine and tooling give the same files.
- **Expected:** "same bytes", or a recorded difference to look into. Either result is recorded as found; neither makes the build reproducible from the tag alone.
- **Risk:** low.

**Step 9. Checks on the files.**
```sh
cd "$W/out" && shasum -a 256 -c SHA256SUMS-darwin-arm64
mkdir "$W/x" && tar -xzf "$W/out/ddsonic-1.0.0-darwin-arm64.tar.gz" -C "$W/x"
B="$W/x/ddsonic-1.0.0-darwin-arm64/ddsonic"
"$B" --version; lipo -archs "$B"; otool -L "$B"; codesign -dv "$B" 2>&1 | grep -E '^(Signature|Format|CodeDirectory)'; vtool -show-build "$B" | grep -E 'minos|sdk'
for f in "$B" "$W/out/"*; do printf '%s ' "${f##*/}"; strings -a "$f" | grep -c -F -e "$HOME" -e "$(id -un)" -e "$(scutil --get LocalHostName)" -e "$(scutil --get ComputerName)"; done
cat "$W/records/tmpdir-before.txt"; ls -A "${TMPDIR:-/tmp}" | wc -l
```
- **Purpose:**
  - checksums;
  - identity: `ddsonic version v1.0.0 (commit 9ae61bcf6604e032b6e690394d10cb0bade80543)`;
  - architecture `arm64`;
  - system libraries only;
  - the signature kind (expected: ad-hoc, made by the linker);
  - the minimum macOS version;
  - the privacy scan;
  - whether the temporary directory holds more entries than before step 4 (a count only; entries are not listed).
- **Expected:** every privacy count `0`. The privacy loop prints counts only, never the matched values. The leftover count is recorded as found.
- **Risk:** none (read-only).

**Step 10. Smoke tests** (the packaged binary from step 9; see [Smoke tests](#smoke-tests)). The owner runs them by hand and records each result.

**Step 11. Gatekeeper behaviour** (an unsigned download, simulated on a copy).
```sh
cp "$W/out/ddsonic-1.0.0-darwin-arm64.tar.gz" "$W/q.tar.gz"
xattr -w com.apple.quarantine "0083;$(printf %x "$(date +%s)");Safari;" "$W/q.tar.gz"
mkdir "$W/q" && tar -xzf "$W/q.tar.gz" -C "$W/q" && xattr -l "$W/q/ddsonic-1.0.0-darwin-arm64/ddsonic"
spctl --assess --type execute -vv "$W/q/ddsonic-1.0.0-darwin-arm64/ddsonic"; "$W/q/ddsonic-1.0.0-darwin-arm64/ddsonic" --version
```
- **Purpose:** what a user who downloads the archive in a browser would see: whether the quarantine flag reaches the extracted binary, and what macOS does when it runs.
- **Expected:** to be observed and recorded, including any dialog's text.
- **Risk:** low. A dialog may offer "Move to Trash"; choose "Done" or "Cancel". Nothing outside `$W` changes.

**Step 12. Cleanup**, after the records are copied to the owner's private evidence location:
```sh
rm -rf /tmp/ddsonic-arm64
```

## Smoke tests

These are proposed for the owner to agree. They run on the binary extracted from the step-9 archive, first with a clean configuration (`DDSONIC_CONFIG_DIR="$W/cfg"`), then for the owner's hands-on acceptance with the owner's own.

1. `ddsonic --version` and `ddsonic --help` exit 0, and the version names `v1.0.0` and `9ae61bcf…`.
2. The interface starts, draws, resizes with the window, and quits with `q`, without errors.
3. Local playback: an MP3, a FLAC and an Ogg Vorbis file play, seek, pause and resume, and move to the next track. These cover the three statically linked decoders.
4. Internet radio: a station from the radio list plays.
5. Spotify: sign-in and playback with the owner's account.
6. YouTube Music: a track plays.
7. macOS Now Playing: the current track shows, and play/pause from it works.
8. Remote control: from a second terminal, `ddsonic status`, `ddsonic pause` and `ddsonic next` act on the running player.
9. After quitting, no ddsonic process remains (`pgrep -l ddsonic` prints nothing).
10. The owner's hands-on acceptance (#88, criterion 4): normal use for as long as the owner decides, recorded with the outcome.

## Signing, notarization and Gatekeeper (research for a separate owner decision)

Sources: Apple's developer documentation on signing, notarization and Gatekeeper, as generally documented. These points are to be confirmed against the current Apple pages and by step 11 before any decision relies on them.

- **Every native arm64 executable must be signed to run**, at least ad hoc. Apple's linker signs arm64 executables ad hoc automatically ("linker-signed"). `release.sh` verifies the signature and records its kind. An ad-hoc signature carries no identity.
- **Gatekeeper checks files that carry the quarantine flag**, which browsers, Mail and AirDrop set; `curl`, `git` and `scp` do not. A quarantined binary without a Developer ID signature and notarization is blocked on first run. On current macOS the user allows it in System Settings > Privacy & Security ("Open Anyway"), or removes the flag (`xattr -d com.apple.quarantine <file>`). Whether extracting a quarantined `.tar.gz` passes the flag to the binary depends on the tool used. Step 11 measures it.
- **Developer ID signing and notarization** need a paid Apple Developer Program membership in the owner's name, and a Developer ID Application certificate. The binary is signed with the hardened runtime and a secure timestamp (`codesign --options runtime --timestamp`). It is then submitted to Apple's notary service (`xcrun notarytool`) as a `.zip`, `.dmg` or `.pkg`; a bare executable cannot be submitted alone. A bare executable cannot be stapled either, so Gatekeeper looks up its notarization online on first run.
- **Effects on this tooling:** signing changes the binary's bytes. A signing step would sit between the build and the manifest, so that `binary_sha256` and the checksums are of the signed file. The step-8 comparison would compare before signing. The certificate's private key and the notary credentials are the owner's. They would never be put in the repository, and never in CI without an owner decision.
- **Recommendation:**
  - For testing now: the unsigned (ad-hoc, linker-signed) local build, as the owner already allowed.
  - For a first published file, decide after step 11:
    - **(A)** publish the ad-hoc-signed build, with install instructions that use `curl`, plus the Gatekeeper steps above;
    - **(B)** enroll, then sign with Developer ID and notarize.

    (A) adds no account, cost or secret, and suits a terminal program installed from a terminal. (B) is the better experience for browser downloads, and the standard for macOS distribution. The recommendation is (A) for this provisional `v1.0.0` file, if step 11 shows the Gatekeeper path is workable and the release text explains it. (B) should be reconsidered if macOS becomes a supported platform.

## Licenses and source disclosure

This identifies obligations to check before any publication. It makes no claim that they are met.

| Component | Version | License | Linked | Where its source is | To check before publication |
|---|---|---|---|---|---|
| ddsonic | `v1.0.0` (`9ae61bcf…`) | MIT; the executable as a whole GPL-3.0 (it links go-librespot) | — | `ddsonic-1.0.0-source.tar.gz` on the release (the tag's source, with `vendor/` and `native/`) | GPL-3.0 corresponding source includes the scripts that control the build. The macOS tooling is not in that archive: publishing it beside the files, or relying on the public repository at the commit the manifest names, is an [open decision](#open-decisions). |
| go-librespot | v0.9.1 with ddsonic's patches | GPL-3.0 | static (Go) | `third_party/go-librespot` and `patches/` in the source archive | Same as above; the notices name the patches. |
| Go modules (76, as listed in the SBOM) | per the SBOM | per the notices | static (Go) | `vendor/` in the source archive | The notices reproduce each license; `xlab/vorbis-go` has no license file (noted, as in the Linux notices). |
| Go standard library and runtime | go1.27.3 | BSD-3-Clause | static | go.dev | License text in the notices. |
| mpg123 | 1.32.6 | LGPL-2.1 | static | The same source archive (same SHA-256) is in `native/` of the release's source archive | Static linking under LGPL-2.1: whether the published ddsonic source and the build tooling are enough for a user to relink. |
| libFLAC | 1.5.0 | BSD-3-Clause (Xiph.Org) | static | xiph.org release, SHA-256 recorded | License text in the notices. This is a different version from the Linux binary's Debian-packaged FLAC. |
| libvorbis | 1.3.7 | BSD-3-Clause (Xiph.Org) | static | xiph.org release, SHA-256 recorded | License text in the notices. |
| libogg | 1.3.5 | BSD-3-Clause (Xiph.Org) | static | xiph.org release, SHA-256 recorded | License text in the notices. |
| macOS system libraries and frameworks | the user's macOS | Apple's | dynamic, not shipped | — | Listed in the notices and SBOM; nothing of Apple's is redistributed. |

## Open decisions

For the owner. None is taken here.

1. **The agreed test suite (criterion 2):**
   - which packages and tests count;
   - whether `-race` is required;
   - how each [classification](#test-method) counts, in particular whether an original failure that passes with corrected fixtures blocks acceptance.
2. **The fixture commit** for step 5 (proposed: `7d7a643369086f4936c2566bba581c8d86a858ed`), and the nine-file overlay list.
3. **The smoke-test list** above.
4. **Approval of each Mac step**, by its exact command, as the work reaches it.
5. **Signing and notarization:** (A) or (B) above, decided after step 11.
6. **Build tooling as corresponding source:** whether the macOS tooling and the codec sources' references are published beside the file set, as a fifth file or inside the archive, or whether the public repository at the commit the manifest names is enough.
7. **The file set's wording in [releasing.md](releasing.md):** this change reads the singular "asset" in the `v1.0.0` exception as not clearly covering four files, and states the set. Owner approval of that wording comes through the review of this change.
8. **The minimum macOS version** any release text states. The manifest records the binary's own minimum (`minos`). The codec libraries target 11.0, and Go may require a later version.
9. **The archive's README:** the archive carries the tag's `README.md`, which says there is no macOS release build. Either keep the tag's text unchanged and explain in the release text, or add a short platform note file to the archive.
10. **Records:** where the private evidence is kept, and what summary goes on #88.
11. **The go-librespot contract gate** (`make sdk-contract`, which CI runs on Linux): whether it runs on the Mac too. Its native checks are unverified on macOS.
