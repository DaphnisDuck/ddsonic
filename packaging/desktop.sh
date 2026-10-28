#!/bin/sh
# Installs ddsonic's launcher entry and icons under a share directory:
#
#   sh packaging/desktop.sh ~/.local/share [/path/to/ddsonic]
#
# writes applications/ddsonic.desktop and icons/hicolor/<size>/apps/ddsonic.png
# for every size in assets/branding/icons. The release archive carries the
# same tree as share/ (packaging/release.sh), and `make install` runs this for
# a source install. The icons are the supplied PNG exports, copied unchanged.
#
# The entry starts `ddsonic` from the session's PATH, which is right for a
# package in /usr/bin. An install under the home directory passes the
# executable's full path as the second argument: a desktop session's PATH
# often lacks ~/.local/bin, and a launcher drops an entry whose program it
# cannot find.
set -eu

dest=$1
exe=${2:-}
root=$(cd "$(dirname "$0")/.." && pwd)

entry="$dest/applications/ddsonic.desktop"
install -Dm644 "$root/packaging/linux/ddsonic.desktop" "$entry"
if [ -n "$exe" ]; then
	# One quoted Exec argument, encoded twice as the Desktop Entry spec reads
	# it. Inside the quotes \, ", ` and $ take a backslash, and a literal % is
	# written %%. Then, as in every string value of the file, each backslash
	# is doubled: a lone \$ is no escape the file's parser knows, and it
	# rejects the entry.
	quoted=$(printf '%s\n' "$exe" | sed -e 's/[\\"`$]/\\&/g' -e 's/\\/\\\\/g' -e 's/%/%%/g')
	EXEC_LINE="Exec=\"$quoted\"" awk '/^Exec=/ { print ENVIRON["EXEC_LINE"]; next } { print }' \
		"$root/packaging/linux/ddsonic.desktop" > "$entry"
fi
for icon in "$root"/assets/branding/icons/ddsonic-*.png; do
	size=${icon##*/ddsonic-}
	size=${size%.png}
	install -Dm644 "$icon" "$dest/icons/hicolor/${size}x${size}/apps/ddsonic.png"
done
