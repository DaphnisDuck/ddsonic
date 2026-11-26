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
- **Build host:** the owner's Apple Silicon (M3) Mac. The v1.0.0 file set was not built in CI.

## The tooling

`packaging/darwin-arm64/` (POSIX `sh`, linted by `make` with the pinned shellcheck):

| Script | What it does |
|---|---|
| `codecs.sh <prefix>` | Builds static arm64 `libogg` 1.3.5, `libvorbis` 1.3.7, `flac` 1.5.0 (libFLAC only) and `mpg123` 1.32.6 from verified source releases into `<prefix>`. It keeps the source archives, the license files and `codecs.txt` (name, version, URL, SHA-256). It refuses Rosetta, a non-empty prefix, and any dynamic or non-arm64 library. |
| `release.sh <version> <prefix> <out>` | Builds the tag's commit. Details in the list after this table. |
| `sources.txt` | Release tags pinned to their tag object and commit, which a build or test of that version must match. Only `v1.0.0` is listed. |
| `notices.sh`, `sbom.sh` | Called by `release.sh`. Their Go-module parts follow the Linux release's `packaging/notices.sh` and `packaging/sbom.sh` step for step, ported to macOS tools; those Linux scripts are not changed. |
| `test-tree.sh <version> <dir> [<fixture-commit>]` | Prepares a test tree: the tag as tagged, or the tag with the corrected test fixtures. See [Test method](#test-method). |

For a version that `sources.txt` lists, `release.sh` and `test-tree.sh` refuse a tag whose object or commit differs from its line there, so a moved or substituted tag cannot be built or tested under that version's name. A version it does not list is refused unless the caller gives the expected commit (`DDSONIC_EXPECTED_COMMIT`). The macOS workflow takes that commit from the tag itself, or from the commit the run was started for, so for such a version the checks keep the tag, the checked-out commit and the build in agreement within one run; they do not authenticate the tag independently. The repository's tag ruleset guards against a moved tag ([releasing.md](releasing.md)). `release.sh` builds the tag's commit with the Linux release's Go flags (`-a -trimpath -mod=vendor`, `-s -w -buildid=`, `-X main.version`, `-X main.sourceSHA`, the same static codec link flags), from a clean tooling checkout. It then checks the result:
- the binary is arm64 only (`lipo`);
- it loads nothing from outside `/usr/lib/` and `/System/Library/`, and no codec library dynamically (`otool -L`);
- its signature verifies (`codesign`);
- `--version` reports the tag and its commit;
- no file names the machine, its user or a home directory.

It writes `RELEASE-MANIFEST.json` and an archive with a fixed order, owner and time, then the SBOM, the notices and `SHA256SUMS-darwin-arm64`. It stages them in a temporary directory, which it always removes, and moves them into `<out>` only after every check has passed. A failed build leaves `<out>` empty.

**Why not reuse `.github/scripts/build-darwin-amd64-codecs.sh`:** it is upstream's Intel recipe. It cross-builds x86_64 with x86_64 flags throughout and is referenced by nothing. Intel macOS is outside this target, and ddsonic keeps upstream files unchanged where it can, so that merges stay cheap. The arm64 script uses the same four source releases at the same SHA-256 sums. It lives with the packaging it serves, not under `.github/`; the macOS workflow (`.github/workflows/macos.yml`) runs it from there.

**Checked on Linux, before any Mac run** (2026-11-08):
- `sh -n`, `bash -n` and the pinned shellcheck 0.9.0 pass on all five scripts.
- `test-tree.sh` runs in both modes. With the overlay, only the nine listed files differ from the tag, plus the record file. It refuses a version not in `sources.txt`, and a `v1.0.0` tag moved to another commit (tried in a scratch clone).
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
   | fails | passes | consistent with a test-fixture or synchronization defect; reviewed case by case (below) |
   | fails | fails | not explained by the fixtures: investigate; possibly a production defect |
   | passes | fails | regression in the corrected fixtures: report, do not count |
   | passes | passes | pass |

   A pass with corrected fixtures is evidence, not proof, that the original failure lay in the test. Each such case gets a written review: what the corrected fixture changes (an assertion, a wait, a helper's teardown), and why that change, and not the program, would account for the original failure. The conclusion stays qualified ("consistent with a fixture defect"). It does not show that the production code has no defect.

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

Each step is shown to the owner with its exact text and is run only after the owner approves it. Steps run in order, in a native arm64 Terminal shell (not Rosetta), zsh or bash.

**How the steps are written:**
- All files go under one new work directory, which step 2 creates with `mktemp -d` under `/tmp`. That keeps the user's name out of paths, and nothing that existed before is reused or removed. Step 2 writes a marker file into it and records its path in `~/ddsonic-arm64-workdir.txt`. Step 12 removes only that recorded directory, and only after checking it.
- Nothing is piped into `tee`. Each command that builds or tests writes its output to a log file. Its exit status is captured right after it, appended to the log and printed.
- Within a step, every command that depends on an earlier one runs only if that one succeeded. A failure ends the step with a `STOP:` line, and nothing later in the step runs. A `STOP:` line, or a non-zero exit where the step expects 0, means: record it, report it, and do not go on to the next step.

`<tooling-commit>` is the reviewed commit that holds `packaging/darwin-arm64/`. The owner's approval names it exactly. `<fixture-commit>` is the commit the corrected fixtures come from; the proposal is `7d7a643369086f4936c2566bba581c8d86a858ed`, the commit they were checked at.

**Step 0. Record the environment.** Read-only: it installs nothing, and reports what is missing.
```sh
sw_vers; uname -m; sysctl -n sysctl.proc_translated
missing=""
if CLT="$(xcode-select -p 2>/dev/null)"; then
  echo "Command Line Tools: $CLT"
  if V="$(clang --version 2>/dev/null)"; then echo "clang: $(printf '%s\n' "$V" | head -n 1)"; else missing="$missing clang"; fi
  if V="$(xcrun --show-sdk-version 2>/dev/null)" && [ -n "$V" ]; then echo "SDK: $V"; else missing="$missing macos-sdk"; fi
  if V="$(git --version 2>/dev/null)"; then echo "$V"; else missing="$missing git"; fi
else
  missing="$missing command-line-tools"
fi
for t in curl shasum tar pkg-config jq; do command -v "$t" >/dev/null 2>&1 && echo "found: $t" || missing="$missing $t"; done
[ "$(uname -m)" = arm64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" != 1 ] || missing="$missing native-arm64-shell"
if [ -z "$missing" ]; then echo "step 0: ok"; else echo "STOP: missing:$missing (step 1)"; fi
```
- **Purpose:** the record's environment lines, and the prerequisites.
- **Prerequisites:** none. `clang`, `xcrun` and `git` run only when the Command Line Tools are present, because without them those commands can offer to install them.
- **Expected:** `arm64`, `0` (not translated), the Command Line Tools path, the `clang` version, the SDK version, the `git` version, the tools found, and last `step 0: ok`. A probe that fails (Command Line Tools, `clang`, the SDK, `git` or a tool) is listed in `STOP: missing: …` instead, and the step does not end `ok`.
- **Risk:** none. Do not paste output that includes the machine's name.

**Step 1. Installs, only what step 0 found missing.** Each line is a separate approval and runs only if needed:
```sh
xcode-select --install    # Command Line Tools: opens Apple's installer
brew install pkgconf      # pkg-config
brew install jq           # jq, if absent
```
- **Purpose:** `pkg-config` resolves the codec libraries for cgo. `jq` writes the manifest and SBOM.
- **Expected:** step 0, run again, finds everything. The versions go in the record, and `pkg-config`'s also in the manifest.
- **Risk:** low; these change the owner's system or Homebrew installation. The codec build reads only its own prefix's `.pc` files, so Homebrew's libraries cannot be linked by accident, and `otool -L` would catch it.

**Step 2. A new work directory, Go 1.27.3, the repository.** Two parts; 2b runs only after 2a printed a work directory.

2a, create and record the work directory:
```sh
if [ -e ~/ddsonic-arm64-workdir.txt ]; then
  echo "STOP: an earlier work directory is still recorded"
else
  W="$(mktemp -d /tmp/ddsonic-arm64.XXXXXXXX)" && echo "$W" > "$W/.ddsonic-arm64-workdir" \
    && echo "$W" > ~/ddsonic-arm64-workdir.txt && export W && echo "work directory: $W"
fi
```

2b, Go and the repository, only inside that directory:
```sh
if [ -n "$W" ] && [ "$(cat "$W/.ddsonic-arm64-workdir" 2>/dev/null)" = "$W" ]; then
  mkdir "$W/records" \
  && ls -A "${TMPDIR:-/tmp}" | wc -l > "$W/records/tmpdir-before.txt" \
  && curl -fsSL -o "$W/go.tgz" https://go.dev/dl/go1.27.3.darwin-arm64.tar.gz \
  && echo "dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2  $W/go.tgz" | shasum -a 256 -c - \
  && tar -C "$W" -xzf "$W/go.tgz" \
  && export PATH="$W/go/bin:$PATH" GOPATH="$W/gopath" GOCACHE="$W/gocache" GOTOOLCHAIN=local \
  && [ "$(go env GOVERSION) $(go env GOHOSTOS)/$(go env GOHOSTARCH)" = "go1.27.3 darwin/arm64" ] \
  && git clone https://github.com/DaphnisDuck/ddsonic.git "$W/ddsonic" \
  && git -C "$W/ddsonic" checkout --detach <tooling-commit> \
  && [ "$(git -C "$W/ddsonic" rev-parse v1.0.0 'v1.0.0^{commit}' | tr '\n' ' ')" = "79ba942dfcdc12a1ad6a02f886b86d5a24e563bc 9ae61bcf6604e032b6e690394d10cb0bade80543 " ] \
  && echo "step 2b: ok" \
  || echo "STOP: step 2b failed; the output above shows where"
else
  echo "STOP: no recorded work directory in this shell"
fi
```
- **Purpose:** a new, empty work directory; an isolated Go of the pinned version; and the tooling with the tag.
- **Expected:**
  - 2a prints `work directory: /tmp/ddsonic-arm64.…`;
  - 2b prints the checksum line `OK` and then `step 2b: ok`. That last line appears only if every command succeeded, including two comparisons: Go is `go1.27.3` for `darwin/arm64`, and the tag resolves to `79ba942dfcdc12a1ad6a02f886b86d5a24e563bc` and `9ae61bcf6604e032b6e690394d10cb0bade80543`.

  `release.sh` and `test-tree.sh` also refuse any other values themselves (`packaging/darwin-arm64/sources.txt`).
- **Risk:** low. About 80 MB is downloaded. Nothing is installed outside `$W` except the one-line `~/ddsonic-arm64-workdir.txt`. If 2a prints STOP, an earlier run was not cleaned up: report it, and do not remove anything by hand.
- **In a new shell,** before any later step, restore the variables:
  ```sh
  export W="$(cat ~/ddsonic-arm64-workdir.txt 2>/dev/null)"
  if [ -n "$W" ] && [ "$(cat "$W/.ddsonic-arm64-workdir" 2>/dev/null)" = "$W" ]; then
    export PATH="$W/go/bin:$PATH" GOPATH="$W/gopath" GOCACHE="$W/gocache" GOTOOLCHAIN=local && echo "restored: $W"
  else
    echo "STOP: no recorded work directory"
  fi
  ```

**Step 3. Codec libraries.**
```sh
(cd "$W/ddsonic" && sh packaging/darwin-arm64/codecs.sh "$W/codecs") > "$W/records/codecs.log" 2>&1
s=$?; echo "exit $s" >> "$W/records/codecs.log"; tail -n 8 "$W/records/codecs.log"
if [ "$s" = 0 ]; then echo "step 3: ok"; else echo "STOP: the codec build failed (exit $s)"; fi
```
- **Purpose:** static arm64 codec libraries for the build and for the tests.
- **Expected:** the log's end lists `codecs.txt`'s four lines, then `step 3: ok`.
- **Risk:** it downloads about 3 MB from xiph.org and mpg123.de and compiles for a few minutes. This recipe has not run on arm64 before, so a configure or compile failure is possible. On a non-zero exit: stop and report, with no retries and no changes.

**Step 4. Original tests.**
```sh
if (cd "$W/ddsonic" && sh packaging/darwin-arm64/test-tree.sh v1.0.0 "$W/t-original") > "$W/records/t-original-tree.log" 2>&1; then
  echo "test-tree: exit 0" >> "$W/records/t-original-tree.log"; tail -n 12 "$W/records/t-original-tree.log"
  (cd "$W/t-original" && PKG_CONFIG_LIBDIR="$W/codecs/lib/pkgconfig" CGO_ENABLED=1 go test -count=1 -race ./...) > "$W/records/tests-original.txt" 2>&1
  s=$?; echo "exit $s" >> "$W/records/tests-original.txt"
  grep -E '^(ok|FAIL|---|panic)' "$W/records/tests-original.txt"
  echo "step 4: tests ran; go test exit $s (a result to record, not a pass)"
else
  s=$?; echo "test-tree: exit $s" >> "$W/records/t-original-tree.log"; tail -n 12 "$W/records/t-original-tree.log"
  echo "STOP: the test tree was not prepared (exit $s); no tests were run"
fi
```
- **Purpose:** the baseline, with the same `go test` flags as CI (`-count=1 -race`).
- **Expected:** the tree log ends `test-tree: exit 0`, then come the per-package results. Failures in some player cleanup tests are possible; they are recorded, not fixed. A non-zero `go test` exit is a result here, not a reason to stop. If the tree cannot be prepared, no test runs, and the step ends with `STOP:`.
- **Risk:**
  - It downloads the Go modules the tag's `go.sum` pins, into `$W/gopath`.
  - Tests start helper processes and may leave temporary files under `$TMPDIR` when they fail.
  - Test output can contain local paths, so the record stays private.
  - Expected to take several minutes. A hang is stopped with Ctrl-C and recorded as such.

**Step 5. Corrected-fixture tests.**
```sh
if (cd "$W/ddsonic" && sh packaging/darwin-arm64/test-tree.sh v1.0.0 "$W/t-fixtures" <fixture-commit>) > "$W/records/t-fixtures-tree.log" 2>&1; then
  echo "test-tree: exit 0" >> "$W/records/t-fixtures-tree.log"; tail -n 12 "$W/records/t-fixtures-tree.log"
  (cd "$W/t-fixtures" && PKG_CONFIG_LIBDIR="$W/codecs/lib/pkgconfig" CGO_ENABLED=1 go test -count=1 -race ./...) > "$W/records/tests-fixtures.txt" 2>&1
  s=$?; echo "exit $s" >> "$W/records/tests-fixtures.txt"
  grep -E '^(ok|FAIL|---|panic)' "$W/records/tests-fixtures.txt"
  echo "step 5: tests ran; go test exit $s (a result to record, not a pass)"
else
  s=$?; echo "test-tree: exit $s" >> "$W/records/t-fixtures-tree.log"; tail -n 12 "$W/records/t-fixtures-tree.log"
  echo "STOP: the test tree was not prepared (exit $s); no tests were run"
fi
```
- **Purpose:** the same production code, with corrected test fixtures.
- **Expected:** the tree log shows `FIXTURE-OVERLAY.txt` with nine files and ends `test-tree: exit 0`, then come the per-package results. As in step 4, a tree that cannot be prepared ends the step with `STOP:` and no tests.
- **Risk:** as in step 4.

**Step 6. Targeted reruns.** Only for failures from steps 4 and 5; at most three per test. Each rerun is one approved command, with the tree, package and test name filled in:
```sh
(cd "$W/t-original" && PKG_CONFIG_LIBDIR="$W/codecs/lib/pkgconfig" CGO_ENABLED=1 go test -count=1 -race -run '^<TestName>$' ./<package>) >> "$W/records/reruns.txt" 2>&1
s=$?; echo "rerun t-original <package> <TestName>: exit $s" >> "$W/records/reruns.txt"; echo "step 6: rerun exit $s (a result to record)"
```
(the same with `t-fixtures` in both places)
- **Purpose:** separate a consistent failure from a flaky one. Every rerun's exit status is in `reruns.txt`.
- **Risk:** as in step 4.

**Step 7. Build the file set.**
```sh
(cd "$W/ddsonic" && sh packaging/darwin-arm64/release.sh v1.0.0 "$W/codecs" "$W/out") > "$W/records/build-1.log" 2>&1
s=$?; echo "exit $s" >> "$W/records/build-1.log"; tail -n 40 "$W/records/build-1.log"
if [ "$s" = 0 ]; then echo "step 7: ok"; else echo "STOP: the build failed (exit $s); $W/out is empty"; fi
```
- **Purpose:** the archive, SBOM, notices and `SHA256SUMS-darwin-arm64`.
- **Expected:**
  - last, `step 7: ok`;
  - a privacy line with 0 machine, user or home-directory strings;
  - the manifest, naming `9ae61bcf…`, `darwin`, `arm64` and `go1.27.3`, with `"reproducible_from_tag_alone": false`;
  - three checksum lines.
- **Risk:** low. `release.sh` stages its files in a temporary directory, which it always removes, and moves them into `$W/out` only after every check has passed. On a non-zero exit `$W/out` is empty: stop and report.

**Step 8. A second build, compared.**
```sh
(cd "$W/ddsonic" && sh packaging/darwin-arm64/release.sh v1.0.0 "$W/codecs" "$W/out2") > "$W/records/build-2.log" 2>&1
s=$?; echo "exit $s" >> "$W/records/build-2.log"
if [ "$s" = 0 ] && [ "$(tail -n 1 "$W/records/build-1.log")" = "exit 0" ]; then
  diff "$W/out/SHA256SUMS-darwin-arm64" "$W/out2/SHA256SUMS-darwin-arm64"; d=$?
  case "$d" in
    0) echo "step 8: ok; same bytes" ;;
    1) echo "step 8: ok; different bytes (a result to record)" ;;
    *) echo "STOP: the comparison itself failed (diff exit $d); no result" ;;
  esac
else
  echo "STOP: a build did not exit 0 (build 2: exit $s); nothing was compared"
fi
```
- **Purpose:** whether two builds on the same machine and tooling give the same files.
- **Expected:** `step 8: ok; same bytes` (diff exit 0), or `step 8: ok; different bytes` (diff exit 1). The comparison runs only if both builds exited 0. A failed build, or a comparison error (diff exit 2 or more, such as a missing file), ends the step with `STOP:` and gives no result. Either result is recorded as found; neither makes the build reproducible from the tag alone.
- **Risk:** low.

**Step 9. Checks on the files.** Read-only, apart from extracting into `$W/x`, and only after the checksums match.
```sh
fail=0
chk() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1: got '$2', want '$3'"; fail=1; fi; }
if (cd "$W/out" && shasum -a 256 -c SHA256SUMS-darwin-arm64) && mkdir "$W/x" \
   && tar -xzf "$W/out/ddsonic-1.0.0-darwin-arm64.tar.gz" -C "$W/x"; then
  B="$W/x/ddsonic-1.0.0-darwin-arm64/ddsonic"
  chk "version" "$("$B" --version 2>&1)" "ddsonic version v1.0.0 (commit 9ae61bcf6604e032b6e690394d10cb0bade80543)"
  chk "architecture" "$(lipo -archs "$B" 2>&1)" "arm64"
  if L="$(otool -L "$B")"; then
    printf '%s\n' "$L"
    chk "libraries listed" "$([ "$(printf '%s\n' "$L" | awk 'NR > 1' | grep -c .)" -gt 0 ] && echo yes || echo no)" "yes"
    chk "libraries outside macOS" "$(printf '%s\n' "$L" | awk 'NR > 1 { print $1 }' | grep -c -v -E '^(/usr/lib/|/System/Library/)')" "0"
    chk "codec libraries loaded dynamically" "$(printf '%s\n' "$L" | grep -c -i -E 'flac|vorbis|ogg|mpg123')" "0"
  else
    chk "otool -L" "failed" "a library list"
  fi
  chk "signature verifies" "$(codesign --verify "$B" >/dev/null 2>&1 && echo yes || echo no)" "yes"
  codesign -dv "$B" 2>&1 | grep -E '^(Signature|Format|CodeDirectory)'
  M="$(vtool -show-build "$B" 2>/dev/null | awk '$1 == "minos" { print $2; exit }')"
  chk "minimum macOS recorded" "$([ -n "$M" ] && echo yes || echo no)" "yes"; echo "minos: $M"
  for f in "$B" "$W/out/"*; do
    if S="$(strings -a "$f")"; then
      chk "privacy ${f##*/}" "$(printf '%s\n' "$S" | grep -c -F -e "$HOME" -e "$(id -un)" -e "$(scutil --get LocalHostName)" -e "$(scutil --get ComputerName)")" "0"
    else
      chk "strings ${f##*/}" "failed" "readable"
    fi
  done
  echo "TMPDIR entries before step 4 / now (coarse, not a check): $(cat "$W/records/tmpdir-before.txt") / $(ls -A "${TMPDIR:-/tmp}" | wc -l)"
  if [ "$fail" = 0 ]; then echo "step 9: ok"; else echo "STOP: step 9 failed; see the FAIL lines above"; fi
else
  echo "STOP: the checksums did not match or the archive could not be extracted; nothing was run"
fi
```
- **Purpose:**
  - checksums;
  - identity: `ddsonic version v1.0.0 (commit 9ae61bcf6604e032b6e690394d10cb0bade80543)`;
  - architecture `arm64`;
  - system libraries only;
  - the signature kind (expected: ad-hoc, made by the linker);
  - the minimum macOS version;
  - the privacy scan;
  - the temporary directory's entry count, before step 4 and now.
- **Expected:** an `ok:` line for each check, and last `step 9: ok`.
  - Any failed check prints `FAIL: <check>: got …, want …`, and the step then ends with `STOP: step 9 failed`; that line, never `step 9: ok`, is the result.
  - Every check runs even after one fails, so the record shows them all.
  - Nothing is extracted or run unless every checksum matches.
  - The privacy checks print counts only, never the matched values.
  - The signature kind (expected: ad-hoc, made by the linker) and `minos` are printed for the record; they are not pass/fail values. The privacy loop prints counts only, never the matched values. The two `$TMPDIR` counts are recorded as found.
- **Limits:** the `$TMPDIR` count is coarse. Other programs also use that directory, so a difference cannot be attributed to the tests or the build; it is a prompt to look, not a result.
- **Risk:** none.

**Step 10. Smoke tests** (the packaged binary from step 9; see [Smoke tests](#smoke-tests)). The owner runs them by hand and records each result.

**Step 11. Gatekeeper behaviour with a synthetic quarantine attribute** (on a copy).
```sh
Q="$W/q/ddsonic-1.0.0-darwin-arm64/ddsonic"
if (cd "$W/out" && shasum -a 256 -c -s SHA256SUMS-darwin-arm64) \
   && cp "$W/out/ddsonic-1.0.0-darwin-arm64.tar.gz" "$W/q.tar.gz" \
   && xattr -w com.apple.quarantine "0083;$(printf %x "$(date +%s)");Safari;" "$W/q.tar.gz" \
   && mkdir "$W/q" && tar -xzf "$W/q.tar.gz" -C "$W/q"; then
  xattr -l "$Q"; spctl --assess --type execute -vv "$Q"; "$Q" --version
  echo "step 11: observations recorded above (not a pass/fail check)"
else
  echo "STOP: the checked copy could not be prepared; nothing was run"
fi
```
- **Purpose:** what macOS does with this binary when a quarantine attribute, written by hand, is present on its archive. Three things are recorded: whether this `tar` passes the attribute on, what `spctl` reports, and what happens when the binary is run.
- **Limits:** the attribute is synthetic. This step does not show how a browser sets the attribute, or whether a browser download, or another extraction tool such as Archive Utility, passes it on. Nothing about a real download is concluded from it.
- **Expected:** to be observed and recorded, including any dialog's text.
- **Risk:** low. A dialog may offer "Move to Trash"; choose "Done" or "Cancel". Nothing outside `$W` changes.

**Step 12. Cleanup**, after the records are copied to the owner's private evidence location. It removes only the recorded work directory, and only if every check passes:
```sh
W="$(cat ~/ddsonic-arm64-workdir.txt 2>/dev/null)"; P=""
case "${W#/tmp/}" in
  ddsonic-arm64.[A-Za-z0-9][A-Za-z0-9][A-Za-z0-9][A-Za-z0-9][A-Za-z0-9][A-Za-z0-9][A-Za-z0-9][A-Za-z0-9])
    [ "/tmp/${W#/tmp/}" = "$W" ] && [ -d "$W" ] && [ ! -L "$W" ] && [ -O "$W" ] \
      && [ "$(cat "$W/.ddsonic-arm64-workdir" 2>/dev/null)" = "$W" ] \
      && P="$(cd -P -- "$W" && pwd -P)" \
      && [ "$P" = "$(cd -P /tmp && pwd -P)/${W#/tmp/}" ] || P="" ;;
esac
if [ -n "$P" ]; then
  chmod -R u+w -- "$P" && rm -rf -- "$P" && rm -f ~/ddsonic-arm64-workdir.txt && echo "removed $W" \
    || echo "STOP: removal of $P did not complete; report it"
else
  echo "REFUSED: '$W' is not the recorded work directory; nothing was removed"
fi
```
- **Purpose:** remove the work directory and its record. `chmod` first, because Go's module cache is read-only.
- **The checks:** all must hold before anything is removed.
  - The recorded path is exactly `/tmp/` plus a name of the form `ddsonic-arm64.` and eight letters or digits, the `mktemp` pattern from step 2. A slash, `.` or `..` anywhere in the name fails this.
  - It is a directory, not a symlink, and owned by the user.
  - Its marker file holds that same path.
  - Its physical path (`cd -P`, `pwd -P`) is the physical `/tmp` plus that name. That rules out a symlinked ancestor, and allows macOS's own `/tmp` → `/private/tmp`.

  The physical path is what is removed.
- **Expected:** `removed /tmp/ddsonic-arm64.…`.
- **Risk:** low. On REFUSED, nothing is removed; report it.

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

Sources: Apple's developer documentation on signing, notarization and Gatekeeper, as generally documented. Every point below is a **platform assumption, not verified here**. Each is to be confirmed against Apple's current pages and on the Mac before any decision relies on it. Step 11 checks only what macOS does with a synthetic quarantine attribute; it does not verify any of these points for real downloads.

- **Signing (assumed):** every native arm64 executable must be signed to run, at least ad hoc, and Apple's linker signs arm64 executables ad hoc automatically ("linker-signed"). `release.sh` verifies the signature and records its kind. An ad-hoc signature carries no identity.
- **Gatekeeper (assumed):**
  - Gatekeeper checks files that carry the quarantine attribute. Browsers, Mail and AirDrop are understood to set it; `curl`, `git` and `scp` are understood not to.
  - A quarantined binary without a Developer ID signature and notarization is expected to be blocked on first run. The user would then allow it in System Settings > Privacy & Security ("Open Anyway"), or remove the attribute (`xattr -d com.apple.quarantine <file>`).
  - Whether extracting a quarantined `.tar.gz` passes the attribute to the binary depends on the tool. Step 11 observes only `tar` with a synthetic attribute.
- **Developer ID signing and notarization (assumed)** need a paid Apple Developer Program membership in the owner's name, and a Developer ID Application certificate. The binary is signed with the hardened runtime and a secure timestamp (`codesign --options runtime --timestamp`). It is then submitted to Apple's notary service (`xcrun notarytool`) as a `.zip`, `.dmg` or `.pkg`; a bare executable cannot be submitted alone. A bare executable cannot be stapled either, so Gatekeeper looks up its notarization online on first run.
- **Effects on this tooling:** signing changes the binary's bytes. A signing step would sit between the build and the manifest, so that `binary_sha256` and the checksums are of the signed file. The step-8 comparison would compare before signing. The certificate's private key and the notary credentials are the owner's. They would never be put in the repository, and never in CI without an owner decision.
- **Recommendation:**
  - For testing now: the unsigned (ad-hoc, linker-signed) local build, as the owner already allowed.
  - For a first published file, decide after step 11:
    - **(A)** publish the ad-hoc-signed build, with install instructions that use `curl`, plus the Gatekeeper steps above;
    - **(B)** enroll, then sign with Developer ID and notarize.

    (A) adds no account, cost or secret, and suits a terminal program installed from a terminal. (B) is the better experience for browser downloads, and the standard for macOS distribution. The recommendation is (A) for this provisional `v1.0.0` file, if step 11 shows the Gatekeeper path is workable and the release text explains it. (B) should be reconsidered if macOS becomes a supported platform. The recommendation rests on the assumptions above and is revisited once they are verified.

## Validation results (2026-11-08)

The owner ran steps 0 to 11 on the owner's Apple Silicon (M3) Mac, under macOS 26.6.2, with Apple clang 21.0.0, the macOS 26.5 SDK, Go 1.27.3 and pkg-config 3.0.10, using this tooling at commit `ab4de5333c76be306e1f260389b92dd01d0e782f`. The records are kept privately. What follows is at the level of facts.

From the transferred records:
- **Codecs (step 3):** the four codec libraries built as static arm64 archives from their pinned source releases.
- **Original tests (step 4):** the tag's own tests against the tag's own code. Of 71 packages with tests, 63 passed and 8 failed: `external/netease`, `external/spotify`, `external/ytmusic`, `internal/ytdlp`, `internal/ytdlp/ytdlptest`, `player`, `resolve` and `ui/model`. In seven of them, the failing tests are built on the test-helper package that the corrected fixtures replace. In `external/spotify`, one test of its test guard fails, a file the overlay also replaces. This baseline stays on record.
- **Corrected fixtures (step 5):** the same production code with the nine-file overlay from `7d7a643369086f4936c2566bba581c8d86a858ed` (test files and the test-helper package only). All 71 packages passed.
- **Classification:** each original failure that passes with corrected fixtures is consistent with a test-fixture or synchronization defect, as [Test method](#test-method) qualifies it. No production defect has been shown, and none has been excluded. Production cleanup behaviour on macOS remains unverified.
- **Release builds (steps 6 to 8):**
  - Two builds gave byte-identical files.
  - The manifest records `minos` 11.0 and an ad-hoc signature.
  - Each build log carries the linker's warning that Go's object was built for macOS 13.0 (see decision 8).
  - `--help` and `--version` name `v1.0.0` and commit `9ae61bcf6604e032b6e690394d10cb0bade80543`.

Owner-attested (reported by the owner, not in the transferred records):
- the step 9 checks of the extracted build;
- the hands-on smoke tests (step 10) passed. Behaviour already known from Linux is tracked separately and not re-described here;
- step 11: with a synthetic quarantine attribute, macOS refused to run the binary ("Killed: 9"), with a dialog offering to move it to the Trash.

Not tested: a real browser download, other Macs, other macOS versions, and Intel Macs. The install instructions are validated on the Mac before they are final.

## Owner decisions (2026-11-08)

- **A narrow exception for this file set only.** The README makes resolving the macOS test failures a prerequisite for macOS distribution. The owner made a narrow, explicit exception to that prerequisite, for the provisional `v1.0.0` macOS ARM64 file set described here and nothing else.
  - It is not macOS support. macOS stays provisional.
  - It waives neither future macOS qualification (the acceptance criteria on #88) nor restoring macOS CI. For both, the prerequisite stands.
  - The record is kept as it is: the historical test failures (the original run above, and the earlier CI failures), the corrected-fixture pass, and the open question. Production cleanup behaviour on macOS is unverified.
  - It authorizes no upload. Publication needs a separate owner approval.
- **The file set** has five files: the archive, its checksum file, its SBOM, its third-party notices, and an archive of its build sources (decisions 6 and 7; [releasing.md](releasing.md)).
- **Signing:** ad hoc; Developer ID signing and notarization are deferred (decision 5).
- **Minimum macOS:** 13 or later, tested on one Apple Silicon Mac only (decision 8).
- **Notices and source:** before publication, the macOS notices and source materials are completed, including the missing notice above. The published Linux files are not changed.

## Licenses and source disclosure

This identifies obligations to check before any publication. It makes no claim that they are met.

| Component | Version | License | Linked | Where its source is | To check before publication |
|---|---|---|---|---|---|
| ddsonic | `v1.0.0` (`9ae61bcf…`) | MIT; the executable as a whole GPL-3.0 (it links go-librespot) | — | `ddsonic-1.0.0-source.tar.gz` on the release (the tag's source, with `vendor/` and `native/`) | GPL-3.0 corresponding source includes the scripts that control the build. The macOS tooling is not in that archive. Decided (owner, 2026-11-08): it is published beside the files, with the four codec source releases, in an archive of the build sources (decision 6). |
| go-librespot | v0.9.1 with ddsonic's patches | GPL-3.0 | static (Go) | `third_party/go-librespot` and `patches/` in the source archive | Same as above; the notices name the patches. |
| Go modules (78, as listed in the macOS SBOM) | per the SBOM | per the notices | static (Go) | `vendor/` in the source archive | The notices reproduce each license; `xlab/vorbis-go` has no license file (noted, as in the Linux notices). |
| Go standard library and runtime | go1.27.3 | BSD-3-Clause | static | go.dev | License text in the notices. |
| mpg123 | 1.32.6 | LGPL-2.1 | static | The same source archive (same SHA-256) is in `native/` of the release's source archive | Static linking under LGPL-2.1: whether the published ddsonic source and the build tooling are enough for a user to relink. One file linked from it, `src/libmpg123/icy2utf8.c`, carries its own permissive notice, which asks that the notice accompany the work; the notices file does not reproduce it yet. |
| libFLAC | 1.5.0 | BSD-3-Clause (Xiph.Org) | static | xiph.org release, SHA-256 recorded | License text in the notices. This is a different version from the Linux binary's Debian-packaged FLAC. |
| libvorbis | 1.3.7 | BSD-3-Clause (Xiph.Org) | static | xiph.org release, SHA-256 recorded | License text in the notices. |
| libogg | 1.3.5 | BSD-3-Clause (Xiph.Org) | static | xiph.org release, SHA-256 recorded | License text in the notices. |
| macOS system libraries and frameworks | the user's macOS | Apple's | dynamic, not shipped | — | Listed in the notices and SBOM; nothing of Apple's is redistributed. |

## Open decisions

For the owner. Decisions 5 to 8 were taken on 2026-11-08 ([below](#owner-decisions-2026-11-08)); the others are open.

1. **The agreed test suite (criterion 2):**
   - which packages and tests count;
   - whether `-race` is required;
   - how each [classification](#test-method) counts, in particular whether an original failure that passes with corrected fixtures blocks acceptance.
2. **The fixture commit** for step 5 (proposed: `7d7a643369086f4936c2566bba581c8d86a858ed`), and the nine-file overlay list.
3. **The smoke-test list** above.
4. **Approval of each Mac step**, by its exact command, as the work reaches it.
5. **Signing and notarization:** (A) or (B) above, decided after step 11. **Decided (owner, 2026-11-08):** (A) for the provisional file set; Developer ID signing and notarization are deferred.
6. **Build tooling as corresponding source:** whether the macOS tooling and the codec sources' references are published beside the file set, as a fifth file or inside the archive, or whether the public repository at the commit the manifest names is enough. **Decided (owner, 2026-11-08):** a fifth file, an archive of the build sources: the four codec source releases and `packaging/darwin-arm64/` at the commit the manifest names.
7. **The file set's wording in [releasing.md](releasing.md):** this change reads the singular "asset" in the `v1.0.0` exception as not clearly covering four files, and states the set. Owner approval of that wording comes through the review of this change. **Decided (owner, 2026-11-08):** the exception names five files, the archive of the build sources included.
8. **The minimum macOS version** any release text states. **Decided (owner, 2026-11-08):** macOS 13 or later. The binary's `minos` is 11.0, the deployment target of the codec libraries and the build. Go 1.27 itself requires macOS 13 or later, and the build's linker warns that Go's own object targets 13.0. So 11.0 is not a supported minimum, and release text states macOS 13 or later, tested on one Apple Silicon Mac only. Future builds use deployment target 13.0; that is a tooling change, and the candidate is not rebuilt for it.
9. **The archive's README:** the archive carries the tag's `README.md`, which says there is no macOS release build. Either keep the tag's text unchanged and explain in the release text, or add a short platform note file to the archive.
10. **Records:** where the private evidence is kept, and what summary goes on #88.
11. **The go-librespot contract gate** (`make sdk-contract`, which CI runs on Linux): whether it runs on the Mac too. Its native checks are unverified on macOS.
