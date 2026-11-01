# Source provenance of ddsonic 1.0.0

This source is a documentation-adjusted copy of the source that the `v1.0.0` binary was built from. It is not the snapshot the build used.

- **The build's source.** The binary in `ddsonic-1.0.0-linux-amd64.tar.gz` was built from commit `497f88ef0c4ac2f8771f01c63b99950c9e5efc2b` (tree `3b891f57e7cb590e2fa6af9a801effcd851f9bae`). `ddsonic --version` and the archive's `RELEASE-MANIFEST.json` name that commit. It is a private snapshot and is not part of the public history.
- **This source.** Its commit has no parent and did not build the binary. It differs from the build's source in documentation only:
  - one documentation file is withheld, for responsible disclosure;
  - for the same reason, prose is shortened in `plan.md`, `docs/ddsonic/upstream.md`, `docs/ddsonic/acceptance.md`, `docs/ddsonic/release-plan.md` and `docs/ddsonic/design/spotify-token-coordinator.md`, and one comment in `external/spotify/playback_ddsonic.go` (a comment only, with the same number of lines);
  - `SECURITY.md` and `CHANGELOG.md` no longer say that `v1.0.0` has a build attestation;
  - this file is added.
- **Unchanged.** Everything else is byte for byte the build's source: all other code and every test, the build scripts (`Makefile`, `packaging/`, `.github/`), `go.mod` and `go.sum`, `third_party/go-librespot` with its patches and provenance, the licenses, and, in the source archive, `THIRD_PARTY_NOTICES`, every Go dependency (`vendor/`) and the codec libraries' sources (`native/`).
- **No attestation.** `v1.0.0` was built locally in the project's pinned build image (Go 1.27.3), not by the release workflow. Nothing attests that this source, or the build's commit, produced the binary. A build from this source names a different commit, so its binary is not byte-identical to the release.

The binary archive carries the build's own copy of `CHANGELOG.md`, which still lists a build attestation under 1.0.0; this file and `SECURITY.md` correct that.
