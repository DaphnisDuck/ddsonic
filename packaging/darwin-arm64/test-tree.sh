#!/bin/sh
# ddsonic: prepares a tree for testing a release tag's source on a platform
# it was not tested on, such as macOS ARM64:
#
#   sh packaging/darwin-arm64/test-tree.sh <version> <dir>
#   sh packaging/darwin-arm64/test-tree.sh <version> <dir> <fixture-commit>
#
# With two arguments, <dir> receives the tag's tracked files exactly: the
# original tests against the original code.
#
# With a third, the test fixtures listed below are replaced by their versions
# at <fixture-commit> (a later commit of this repository). Those files are
# test code only: *_test.go files, and the test-helper package
# internal/ytdlp/ytdlptest, which only tests import. The script refuses the
# overlay if a listed file is anything else, if any package outside tests
# imports the helper package, or if the program's own dependencies change, so
# the production code under test stays the tag's, byte for byte.
# <dir>/FIXTURE-OVERLAY.txt records each replaced file with its SHA-256 at the
# tag and at <fixture-commit>.
#
# Run it from a checkout of this repository that has the tag and, for an
# overlay, <fixture-commit>. It writes nothing outside <dir> and runs no test.
set -eu

version=${1:?usage: test-tree.sh <version> <dir> [<fixture-commit>]}
dir=${2:?usage: test-tree.sh <version> <dir> [<fixture-commit>]}
fixture=${3:-}

# The fixture files: test code that changed after v1.0.0 to make tests
# independent of the CI host and to synchronize their teardown, without a
# production change. Each must be a test file or in the test-helper package.
fixtures="
acceptance/acceptance_test.go
external/spotify/prefetch_ddsonic_test.go
external/spotify/testguard_ddsonic_test.go
internal/ytdlp/ytdlptest/copier.go
internal/ytdlp/ytdlptest/copier_release_unix_test.go
internal/ytdlp/ytdlptest/copier_teardown_test.go
internal/ytdlp/ytdlptest/hold_unix.go
internal/ytdlp/ytdlptest/listed_unix.go
player/ytdl_load_ddsonic_test.go
"
helper=internal/ytdlp/ytdlptest

fail() { echo "test-tree: $*" >&2; exit 1; }

repo=$(git rev-parse --show-toplevel)
here=$(cd "$(dirname "$0")" && pwd -P)
# The tag must match its pinned line in sources.txt and/or the commit the
# caller expects, as for release.sh (tag-commit.sh).
resolved=$(sh "$here/tag-commit.sh" "$version") || fail "tag $version did not resolve as expected"
commit=${resolved#* }
if [ -e "$dir" ] && [ -n "$(ls -A "$dir")" ]; then
	fail "$dir is not empty"
fi
mkdir -p "$dir"
git -C "$repo" archive --format=tar "$commit" | tar -x -C "$dir"
if [ -z "$fixture" ]; then
	echo "test-tree: $dir holds $version ($commit) as tagged"
	exit 0
fi

fixture=$(git -C "$repo" rev-parse --verify "$fixture^{commit}") || fail "no commit $fixture"
cd "$dir"
module=$(go list -m)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# The program's own dependencies, before: the overlay may not change them.
go list -mod=mod -deps . > "$tmp/deps.before"

{
	echo "Fixture overlay on $version ($commit), from commit $fixture."
	echo "Each line: path, SHA-256 as tagged (or 'new'), SHA-256 as overlaid."
} > FIXTURE-OVERLAY.txt
for path in $fixtures; do
	case "$path" in
	*_test.go | "$helper"/*) ;;
	*) fail "$path is not a test file or in $helper" ;;
	esac
	before=new
	if [ -f "$path" ]; then
		before=$(shasum -a 256 "$path" | cut -d' ' -f1)
	fi
	mkdir -p "$(dirname "$path")"
	git -C "$repo" show "$fixture:$path" > "$path"
	printf '%s %s %s\n' "$path" "$before" "$(shasum -a 256 "$path" | cut -d' ' -f1)" >> FIXTURE-OVERLAY.txt
done

# No package's non-test code imports the helper, except the helper itself.
go list -mod=mod -f '{{.ImportPath}} {{join .Imports " "}}' ./... |
	awk -v h="$module/$helper" '$1 != h { for (i = 2; i <= NF; i++) if ($i == h) print $1 }' > "$tmp/importers"
if [ -s "$tmp/importers" ]; then
	fail "non-test code imports $helper: $(cat "$tmp/importers")"
fi
go list -mod=mod -deps . > "$tmp/deps.after"
cmp -s "$tmp/deps.before" "$tmp/deps.after" || fail "the overlay changed the program's dependencies"
echo "test-tree: $dir holds $version ($commit) with the fixture overlay from $fixture:"
cat FIXTURE-OVERLAY.txt
