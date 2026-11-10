#!/bin/sh
# ddsonic: prints the version of a source build (`make build`, #87).
#
# The base is the highest stable release tag (vX.Y.Z, no pre-release suffix)
# that is an ancestor of HEAD; pre-release tags (v1.0.0-rc.3) never are. The
# count is the commits that descend from the base, so history merged in from
# before the tag is not counted:
#
#   v1.0.0             on the tagged commit
#   v1.0.0-56-g1a2b3c4 56 commits after it
#   ...-dirty          when tracked files differ from HEAD (as git describe)
#
# With no stable tag among HEAD's ancestors (no tags fetched, only
# pre-release tags), it prints what `git describe --tags --always --dirty`
# does, as the Makefile did before. Outside a git work tree it prints
# nothing and fails, and the Makefile says "dev". It prints only once
# everything has worked. Release builds name their version themselves
# (packaging/release.sh) and do not run this.
set -eu

git rev-parse --verify -q HEAD >/dev/null

# Highest stable tag merged into HEAD, by number: v0.10.0 is above v0.9.0.
base=$(git tag --merged HEAD --list 'v*' |
	grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' |
	sort -t. -k1.2,1n -k2,2n -k3,3n |
	tail -n 1) || true

if [ -z "$base" ]; then
	version=$(git describe --tags --always --dirty)
else
	count=$(git rev-list --count --ancestry-path "$base..HEAD")
	version=$base
	if [ "$count" != 0 ]; then
		version="$base-$count-g$(git rev-parse --short HEAD)"
	fi
	# As git describe --dirty: refresh the index, then compare tracked files.
	git update-index -q --refresh >/dev/null 2>&1 || true
	if ! git diff-index --quiet HEAD --; then
		version="$version-dirty"
	fi
fi
printf '%s\n' "$version"
