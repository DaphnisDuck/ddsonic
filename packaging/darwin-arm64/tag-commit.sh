#!/bin/sh
# ddsonic: resolves a release tag for release.sh and test-tree.sh, once, and
# prints "<tag object> <commit>":
#
#   sh packaging/darwin-arm64/tag-commit.sh <version>
#
# Run it in a checkout of this repository that has the tag. The tag must
# resolve to a commit, and:
# - if sources.txt pins the version, the tag object and the commit must match
#   that line (how v1.0.0 is built);
# - if DDSONIC_EXPECTED_COMMIT is set (a full 40-character commit, which the
#   macOS workflow derives from the tag it was started for), the commit must
#   be that one;
# - a version that sources.txt does not pin needs DDSONIC_EXPECTED_COMMIT.
# Any mismatch fails, so a moved or substituted tag cannot build under the
# label.
set -eu

version=${1:?usage: tag-commit.sh <version>}

fail() { echo "tag-commit: $*" >&2; exit 1; }

here=$(cd "$(dirname "$0")" && pwd -P)
repo=$(git -C "$here" rev-parse --show-toplevel)
tagobj=$(git -C "$repo" rev-parse --verify --quiet "refs/tags/$version") || fail "no tag $version in $repo"
commit=$(git -C "$repo" rev-parse --verify --quiet "refs/tags/$version^{commit}") || fail "tag $version does not resolve to a commit"

expected=${DDSONIC_EXPECTED_COMMIT:-}
if [ -n "$expected" ]; then
	printf '%s\n' "$expected" | grep -Eqx '[0-9a-f]{40}' || fail "DDSONIC_EXPECTED_COMMIT is not a full commit"
	[ "$commit" = "$expected" ] || fail "tag $version is $commit; the expected commit is $expected"
fi

pinned=$(awk -v v="$version" '$1 == v { print $2, $3 }' "$here/sources.txt")
if [ -n "$pinned" ]; then
	[ "$tagobj $commit" = "$pinned" ] || fail "tag $version is $tagobj -> $commit; sources.txt pins $pinned"
elif [ -z "$expected" ]; then
	fail "$version is not pinned in $here/sources.txt and no DDSONIC_EXPECTED_COMMIT is set"
fi
echo "$tagobj $commit"
