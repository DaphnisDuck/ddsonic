#!/bin/sh
# ddsonic: decides what the macOS workflow's package job builds
# (.github/workflows/macos.yml), and prints it as lines for GITHUB_ENV:
#
#   sh packaging/darwin-arm64/ci-version.sh <event> [<version>]
#
#   VERSION=<tag>            the release tag to build
#   EXPECTED_COMMIT=<sha>    the commit that tag must resolve to
#   MACOS_TARGET=<target>    the deployment target (11.0 for v1.0.0, else 13.0)
#   ARTIFACT=<name>          the workflow artifact's name (a pull request's
#                            dry run says so in its name)
#
# By event:
# - push (a tag): the pushed tag (GITHUB_REF_NAME); the commit the run is for
#   (GITHUB_SHA) must be the tag's commit.
# - workflow_dispatch: <version>, which must be a version tag in this
#   repository; it is resolved once, here. The caller then checks out exactly
#   that commit, so the tooling is the tag's own.
# - pull_request: a dry run of v1.0.0 (pinned in sources.txt) with the pull
#   request's tooling.
# In every case tag-commit.sh resolves the tag, and release.sh resolves it
# again and fails on any mismatch.
set -eu

event=${1:?usage: ci-version.sh <event> [<version>]}
input=${2:-}

fail() { echo "ci-version: $*" >&2; exit 1; }

here=$(cd "$(dirname "$0")" && pwd -P)
is_version() { printf '%s\n' "$1" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?'; }

case "$event" in
push)
	[ "${GITHUB_REF_TYPE:-}" = tag ] || fail "a push builds only for a tag"
	version=${GITHUB_REF_NAME:?}
	is_version "$version" || fail "'$version' is not a version tag"
	expected=${GITHUB_SHA:?}
	;;
workflow_dispatch)
	version=$input
	is_version "$version" || fail "'$version' is not a version tag (v1.2.3 or v1.2.3-rc.1)"
	expected=$(git -C "$here" rev-parse --verify --quiet "refs/tags/$version^{commit}") || fail "no tag $version"
	;;
pull_request)
	version=v1.0.0
	expected=$(git -C "$here" rev-parse --verify --quiet "refs/tags/$version^{commit}") || fail "no tag $version"
	;;
*) fail "unexpected event '$event'" ;;
esac

resolved=$(DDSONIC_EXPECTED_COMMIT=$expected sh "$here/tag-commit.sh" "$version") || fail "tag $version did not resolve as expected"
commit=${resolved#* }
printf '%s\n' "$commit" | grep -Eqx '[0-9a-f]{40}' || fail "tag $version did not resolve to a commit"
case "$version" in
v1.0.0) target=11.0 ;;
*) target=13.0 ;;
esac
echo "VERSION=$version"
echo "EXPECTED_COMMIT=$commit"
echo "MACOS_TARGET=$target"
if [ "$event" = pull_request ]; then
	echo "ARTIFACT=ddsonic-$version-darwin-arm64-dry-run"
else
	echo "ARTIFACT=ddsonic-$version-darwin-arm64"
fi
