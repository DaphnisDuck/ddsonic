#!/bin/sh
# Prints the release notes for a tag: its section of CHANGELOG.md. A
# prerelease (v1.0.0-rc.1) uses the section of the version it leads to.
set -eu

version=${1#v}
base=${version%%-*}
notes=$(awk -v v="$base" '
	/^## \[/ { on = index($0, "## [" v "]") == 1; next }
	on { print }
' CHANGELOG.md)
if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
	echo "notes: CHANGELOG.md has no section for $base" >&2
	exit 1
fi
printf '%s\n' "$notes"
