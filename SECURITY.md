# Security

## Reporting a vulnerability

Please report it privately, not in a public issue: open a report under **Security → Report a vulnerability** on [the ddsonic repository](https://github.com/DaphnisDuck/ddsonic/security/advisories/new).

If that page isn't available to you, write to daphnisduck@wompernet.us.

Say what you found, the ddsonic version (`ddsonic --version`), and how to reproduce it. ddsonic is maintained by one person in spare time: expect an answer within a week, and a fix or a plan for one within 30 days for anything serious. You'll be credited in the release notes unless you'd rather not be.

## What is supported

The latest release. Fixes are not ported to older versions.

## Official builds

The only official binaries are the files published on this repository's [Releases](https://github.com/DaphnisDuck/ddsonic/releases) page (release candidates are marked "Pre-release"), and the AUR package `ddsonic-bin`, which installs those files, once it exists. Anything else is someone else's build; ddsonic can always be built from source (see the [README](README.md#install)).

Each release carries `SHA256SUMS`, to check a download with:

```sh
sha256sum -c --ignore-missing SHA256SUMS
```

`v1.0.0` has no build attestation. It was built locally in the project's pinned build image, not by the release workflow, so `gh attestation verify` has nothing to check for it; [SOURCE-PROVENANCE.md](https://github.com/DaphnisDuck/ddsonic/blob/v1.0.0/SOURCE-PROVENANCE.md) at the `v1.0.0` tag says which source it was built from. The release candidates `v1.0.0-rc.1` to `v1.0.0-rc.3` were built by the release workflow and carry a build attestation, checked with:

```sh
gh attestation verify ddsonic-<version>-linux-amd64.tar.gz --repo DaphnisDuck/ddsonic
```

One candidate, `v1.0.0-rc.1`, was published before the project was renamed from ddmus. It is official and unchanged: its files are named `ddmus-1.0.0-rc.1-…`, and its attestation names the repository as it was called then, so it is checked with `--repo DaphnisDuck/ddmus`.

A vulnerability in cliamp, which ddsonic is forked from, should also go to [cliamp](https://github.com/bjarneo/cliamp).

## If ddsonic stops being maintained

The repository will be archived with a notice saying so, and `ddsonic-bin`, if it exists by then, will be handed to a named co-maintainer or deleted from the AUR on request. It won't be left orphaned for someone else to adopt silently.
