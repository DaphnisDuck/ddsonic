# Releasing ddsonic

ddsonic releases one build: Linux x86-64, from the pinned environment in `packaging/Dockerfile`. The executable is GPL-3.0 (it links go-librespot); ddsonic's source is MIT.

## What a release holds

| File | What it is |
| --- | --- |
| `ddsonic-<version>-linux-amd64.tar.gz` | The `ddsonic` binary, `LICENSE`, `LICENSE-GPL-3.0`, `THIRD_PARTY_NOTICES`, `README.md` with the images it shows (`assets/branding/ddsonic-logo-horizontal.png`, `docs/ddsonic/images/library.png`), `CHANGELOG.md`, and `share/`: the launcher entry (`applications/ddsonic.desktop`) and the icons (`icons/hicolor/<size>/apps/ddsonic.png`) |
| `ddsonic-<version>-source.tar.gz` | The complete corresponding source: ddsonic at the tag (with the modified go-librespot in `third_party/go-librespot`, its patches and its provenance), every Go dependency (`vendor/`), the codec libraries' sources (`native/`) and the build scripts (`packaging/`) |
| `ddsonic-<version>-THIRD_PARTY_NOTICES.txt` | The notices, also inside both archives |
| `ddsonic-<version>-sbom.cdx.json` | A CycloneDX SBOM: Go modules, the Go toolchain, native libraries |
| `SHA256SUMS` | Checksums of the four files above; the build attestation covers the same files |

## How the binary is built

- **Toolchain:** Go 1.27.3, from an image pinned by digest.
- **Codecs:** FLAC, Vorbis and Ogg from one Debian 12 snapshot and mpg123 from a checksummed source archive, all linked statically, so the binary doesn't depend on the codec versions of the machine it runs on.
- **Dynamic:** ALSA and glibc. Debian 12 sets the baseline, glibc 2.36. Tested on Arch and Debian 13.
- **Reproducible:** two builds of the same commit in this image give the same bytes, and the source archive rebuilds to the same binary with no network.

`THIRD_PARTY_NOTICES` and the SBOM are generated from the built binary (`go version -m`), so they list what ships. A dependency with no license file stops the build until `packaging/notices.sh` has a note for it; today that is `xlab/vorbis-go` only.

go-librespot ships modified: `go.mod` replaces it with the tracked tree `third_party/go-librespot` ([upstream.md](upstream.md#the-local-go-librespot-replacement)), which the source archive carries with `patches/go-librespot/` and `third_party/go-librespot.provenance`. The notices name it as modified, with its upstream base and that base's Go module checksum, each patch in order with its SHA-256, and the modified tree's hash, and take its license from that tree; the SBOM gives it no package URL of its own and records upstream v0.9.1 as its ancestor and the patches as its pedigree. Any other replacement, a replacement `vendor/modules.txt` does not record, or a patch that is not the one the provenance records stops the build.

## Cutting a release

Before a release candidate, from `v1.0.0-rc.4` on: the human acceptance checklist has been executed against the candidate and its findings triaged ([release-plan.md](release-plan.md#rc-audit-and-release-gate)).

The checklist is now [Release Acceptance](acceptance.md), and it runs on the candidate itself, so it comes after the tag and before publication: the tag and the workflow produce the draft release; download its archive and `SHA256SUMS`; run acceptance on **the binary extracted from that archive** (`acceptance run --archive … --sums … --version … --source-sha …`), then the live and human runbooks; publish the draft only when the gate is met; then commit the acceptance summary into the release record. The tag is not moved and the candidate is not rebuilt: a local build of the same source is not the candidate. The archive says what it is: it carries `RELEASE-MANIFEST.json` (version, source commit, the binary's SHA-256), and the release binary's `--version` names its source commit; acceptance requires the commit you give it, the manifest's and the binary's to be the same, and stops if any is missing or differs. A candidate that fails is abandoned, and the fix ships as the next number.

1. `CHANGELOG.md` has a section for the version (`## [1.0.0]`). A candidate (`v1.0.0-rc.2`) uses the section of the version it leads to.
2. Switch the repository's tag ruleset off (Settings → Rules → Rulesets), tag the commit, push that one tag (`git push origin v1.0.0`), and switch the ruleset back on. The ruleset refuses every new, moved or deleted tag, so that cliamp's tags can never reach ddsonic's repository by accident: a tag push runs the workflow of the tagged commit, and cliamp's would publish cliamp releases here. Locally, cliamp's tags live under `refs/upstream-tags/` for the same reason; see [upstream.md](upstream.md).
3. The Release workflow (actions pinned by commit) builds the files, installs the archive in a fresh Debian 13 container, attests the build, and creates a **draft** release. A tag with a hyphen is marked as a prerelease.
4. Check the draft and the [checks before anything is made public](#before-anything-is-made-public), then publish it by hand.
5. Stable releases only: update `packaging/aur/PKGBUILD` (`pkgver`, `updpkgsums`, `.SRCINFO`) and push it to the AUR. A candidate is tested from the recipe locally and never pushed.

### Before anything is made public

These checks are mandatory before a release, a source archive or the repository is made public. The privacy audit was done on 2026-11-05 (its status is below); the agent docs cleanup is still pending.

- **Privacy:** check for personal names, usernames, email addresses, machine hostnames, home-directory paths and private account or device identifiers in the public source and docs, the release pages, the generated SBOM and notices, the source archive, and the paths embedded in the binary. Exposure in Git history is a separate question: history is not rewritten without the owner's approval. Status (2026-11-05): a read-only audit of these places and of the public Git history found no live secret and no urgent exposure, and the owner accepted what it found (catalog R-09). The earlier note (2026-10-19) that the audit was blocked by a platform denial no longer applies; what that denial was is unverified.
- **Agent docs cleanup** (agreed, pending): one canonical, concise set of agent instructions; a practical checklist; a prioritized backlog; history archived. Retire the PV-5/AUTH-PV5 requirements still written as active, keeping their historical evidence. Progress reports stay light, with full technical handoffs for ChatGPT and a clear summary for people. Tracked here only; not started.

## A dry run on your machine

```sh
docker build -t ddsonic-build -f packaging/Dockerfile packaging
mkdir -p dist
docker run --rm -v "$PWD":/src:ro -v "$PWD/dist":/out ddsonic-build \
    /src/packaging/release.sh v1.0.0-test.1 "$(git log -1 --format=%ct)"
```

This builds the committed tree (`HEAD`); `dist/` is ignored by git. The files are owned by root, as Docker wrote them. Uncommitted work is built only from a commit that holds all of it, passed as the third argument; `git stash create` leaves untracked files out.

## Moving a pin

Change it in `packaging/Dockerfile` (the Go image digest, `DEBIAN_SNAPSHOT`, the mpg123 version and checksum), keep `mise.toml` and the Go version in `.github/workflows/ci.yml` in step, and cut a new candidate.

## GitHub settings the workflows rely on

- **A tag ruleset** on all tags that restricts creations, updates and deletions, with an empty bypass list. It is switched off only while a release tag is pushed.
- **The "Deploy to GitHub Pages" workflow stays disabled** (Actions → the workflow → Disable workflow). It was cliamp's and is gone from `main`, but the copies in older tags can be started by hand and have no repository check.
- **Private vulnerability reporting** is on (`SECURITY.md` points to it).

## The rename from ddmus (2026-10-04)

ddsonic was called ddmus up to `v1.0.0-rc.1`. That candidate is history and is never touched: its tag, its release page, its five files (`ddmus-1.0.0-rc.1-…` and `SHA256SUMS`) and its attestation stay as published, under the old name. Everything from the next tag on is `ddsonic-…`.

The repository on GitHub is renamed from `DaphnisDuck/ddmus` to `DaphnisDuck/ddsonic` by the owner, in this order, so that `main` never points at pages that don't exist:

1. Push the rename branch and let CI pass on it, while the repository still has its old name.
2. Rename the repository (Settings → General → Repository name). GitHub then redirects the old web, clone, API and release-download addresses to the new ones.
3. Merge the branch into `main` and push, straight away. Between steps 2 and 3 `main` still says ddmus but every link in it redirects; the other order would leave `main` linking to ddsonic addresses that return 404.
4. Point each clone at the new address: `git remote set-url origin git@github.com:DaphnisDuck/ddsonic.git`.
5. Change the repository's description, which names ddmus.
6. Check what the workflows rely on survived the rename: the tag ruleset is active, private vulnerability reporting is on, the "Deploy to GitHub Pages" workflow is still disabled. Check that the `v1.0.0-rc.1` release page and its downloads open at the new address, and that its attestation still verifies (`gh attestation verify ddmus-1.0.0-rc.1-linux-amd64.tar.gz --repo DaphnisDuck/ddmus`; `SECURITY.md` says which form to use).

Two rules follow from the rename:

- **No tag is pushed between steps 1 and 3.** The Release workflow runs only in the repository it names (`github.repository == 'DaphnisDuck/ddsonic'`): a tag pushed while the name and the check disagree builds nothing and says nothing.
- **No repository named `ddmus` is ever created under `DaphnisDuck`.** A new repository of that name takes the old addresses and ends the redirects, including the ones `v1.0.0-rc.1`'s links rely on.

The AUR package is `ddsonic-bin`. `ddmus-bin` was never pushed to the AUR, so there is nothing to replace there; a `ddmus-bin` built locally from the candidate is removed by hand (`pacman -R ddmus-bin`).

## Versioning

ddsonic follows [Semantic Versioning](https://semver.org/) (owner, 2026-11-03):

- **v1.0.0** is the published stable release. Its tag and artifacts are never altered.
- **v1.0.x** is reserved for compatible bug fixes, security fixes and maintenance releases, when they are needed.
- **v1.1.0** is the next planned feature release. It takes post-1.0 usability improvements, such as P-27 (alphabetical radio sorting). Its scope and timing stay deliberately open: no fixed feature count, date or milestone threshold.
- **v2.0.0** is reserved for changes that break an established compatibility commitment.

A change moves through three separate statuses: development complete (reviewed, and accepted by the owner), integrated (merged into `main`), and released (published as part of a version). Reaching one does not imply the next.
