#!/bin/sh
# Writes THIRD_PARTY_NOTICES for the binary given as $1, to stdout.
#
# The list comes from the binary itself (`go version -m`), so it names what
# ships, not what go.mod mentions. Runs inside the build image
# (packaging/Dockerfile), which holds the native libraries' license files,
# from the build tree release.sh prepares (ddsonic's source with vendor/).
set -eu

binary=$1

cat <<'HEAD'
ddsonic: third-party notices

ddsonic's own source is MIT-licensed (see LICENSE). The ddsonic executable links
a modified go-librespot (its section below says how it differs from upstream),
which is GPL-3.0, so the executable as a whole is distributed
under GPL-3.0 (see LICENSE-GPL-3.0). The complete corresponding source for
this build is the ddsonic-<version>-source.tar.gz published beside it.

This file lists everything compiled or linked into the executable, with each
component's license text.

HEAD

section() {
	printf '\n%s\n%s\n\n' "================================================================================" "$1"
}

section "Native libraries"
cat <<NATIVE
Linked statically:
  mpg123 ${MPG123_VERSION} (LGPL-2.1)
  FLAC $(dpkg-query -W -f '${Version}' libflac-dev) (BSD-3-Clause; Xiph.Org)
  libvorbis $(dpkg-query -W -f '${Version}' libvorbis-dev) (BSD-3-Clause; Xiph.Org)
  libogg $(dpkg-query -W -f '${Version}' libogg-dev) (BSD-3-Clause; Xiph.Org)
Linked dynamically, from the system ddsonic runs on:
  alsa-lib (LGPL-2.1), glibc (LGPL-2.1)

mpg123 is LGPL-2.1: you may relink ddsonic against a modified mpg123. Its source
and ddsonic's build scripts are in the source archive.
NATIVE

native() {
	section "$1"
	cat "$2"
}
native "mpg123" /opt/mpg123/COPYING
# ddsonic: one libmpg123 source file, src/libmpg123/icy2utf8.c, carries its
# own notice, whose permission asks that it be retained or reproduced in an
# accompanying document (#121). It is reproduced here from the mpg123 source
# release in the build tree's native/ (release.sh), the archive the source
# tarball ships, as packaging/darwin-arm64/notices.sh does.
icy=$(tar -xOjf "native/mpg123-$MPG123_VERSION.tar.bz2" "mpg123-$MPG123_VERSION/src/libmpg123/icy2utf8.c" |
	awk '$0 == "/*-" { on = 1; next } on && $0 == " *-" { exit } on')
case $icy in
*"retained or reproduced in an accompanying document"*) ;;
*)
	echo "notices: no notice found in mpg123's src/libmpg123/icy2utf8.c" >&2
	exit 1
	;;
esac
section "mpg123: src/libmpg123/icy2utf8.c"
cat <<'ICY'
This file of libmpg123 is linked into the executable. It carries its own
notice, reproduced here from the file:

ICY
printf '%s\n' "$icy"
native "FLAC" /usr/share/doc/libflac-dev/copyright
native "libvorbis" /usr/share/doc/libvorbis-dev/copyright
native "libogg" /usr/share/doc/libogg-dev/copyright
native "alsa-lib" /usr/share/doc/libasound2-dev/copyright

section "Go standard library and runtime ($(go env GOVERSION))"
cat "$(go env GOROOT)/LICENSE"

# The Go modules. The binary names them (`go version -m`); the vendored build
# tree (the current directory) says which of their packages are compiled in.
# A package's license can sit beside it rather than at the module's root, so
# every directory from each shipped package up to its module root is searched,
# in the module cache: `go mod vendor` leaves some license files out.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

go version -m "$binary" > "$tmp/buildinfo"
awk '$1 == "dep" { print $2, $3 }' "$tmp/buildinfo" | sort -u > "$tmp/shipped"

# ddsonic: go-librespot is replaced by the tracked tree third_party/go-librespot
# (go.mod; docs/ddsonic/upstream.md), upstream v0.9.1 with ddsonic's patches.
# The binary names a replacement on a "=>" line under its "dep" line. This one
# replacement, exactly, is allowed, and vendor/modules.txt must record it. Its
# license comes from the replacement tree, never from the module cache, which
# would hold upstream's unmodified module.
replaced_mod=github.com/devgianlu/go-librespot
replaced_ver=v0.9.1
replaced_dir=./third_party/go-librespot
provenance=third_party/go-librespot.provenance
awk '$1 == "=>" { print last, $2, $3 } { last = ($1 == "dep") ? $2 " " $3 : "" }' "$tmp/buildinfo" > "$tmp/replaced"
printf '%s %s %s (devel)\n' "$replaced_mod" "$replaced_ver" "$replaced_dir" > "$tmp/replaced.want"
if ! cmp -s "$tmp/replaced" "$tmp/replaced.want"; then
	echo "notices: the binary's module replacements are not exactly $replaced_mod $replaced_ver => $replaced_dir:" >&2
	diff "$tmp/replaced.want" "$tmp/replaced" >&2 || true
	exit 1
fi
if [ "$(grep -c '=>' vendor/modules.txt)" != 1 ] || ! grep -qxF "# $replaced_mod $replaced_ver => $replaced_dir" vendor/modules.txt; then
	echo "notices: vendor/modules.txt does not record exactly that replacement" >&2
	exit 1
fi
# The provenance names the upstream base and each patch in the order it
# applies; every patch must be in the tree at its recorded SHA-256, and no
# other file may be in patches/go-librespot.
if ! grep -qxF "module $replaced_mod" "$provenance" || ! grep -qxF "base-version $replaced_ver" "$provenance"; then
	echo "notices: $provenance does not name $replaced_mod $replaced_ver" >&2
	exit 1
fi
awk '$1 == "patch" { sub(/^sha256:/, "", $3); print $3 "  " $2 }' "$provenance" > "$tmp/patches"
awk '{ print $2 }' "$tmp/patches" | sort > "$tmp/patches.listed"
find patches/go-librespot -type f | sort > "$tmp/patches.present"
if [ ! -s "$tmp/patches" ] || ! cmp -s "$tmp/patches.listed" "$tmp/patches.present" || ! sha256sum -c --quiet "$tmp/patches" >&2; then
	echo "notices: the patches in patches/go-librespot are not the ones $provenance records" >&2
	exit 1
fi
prov() { awk -v k="$1" '$1 == k { print $2 }' "$provenance"; }
go list -mod=vendor -deps -f '{{with .Module}}{{.Path}} {{.Version}} {{end}}{{.ImportPath}}' . > "$tmp/list"
# Three fields: a dependency's package. Fewer: the standard library or ddsonic.
awk 'NF == 3' "$tmp/list" > "$tmp/pkgs"
awk '{ print $1, $2 }' "$tmp/pkgs" | sort -u > "$tmp/mods"
if [ ! -s "$tmp/mods" ] || ! cmp -s "$tmp/shipped" "$tmp/mods"; then
	echo "notices: the binary's modules and the build tree's differ:" >&2
	diff "$tmp/shipped" "$tmp/mods" >&2 || true
	exit 1
fi

modcache=$(go env GOMODCACHE)
# Module paths in the cache escape capitals as "!" plus the lower-case letter.
escape() { printf '%s' "$1" | sed 's/[A-Z]/!\L&/g'; }

while read -r mod ver; do
	if [ "$mod $ver" = "$replaced_mod $replaced_ver" ]; then
		root=$replaced_dir
		section "$mod $ver, modified by ddsonic"
		cat <<MODIFIED
This is a modified version of go-librespot. The executable is built from the
tree third_party/go-librespot in the source archive: upstream $mod $ver
(Go module checksum $(prov base-h1)) with these patches, in
patches/go-librespot, applied in this order:
$(awk '{ print "  " $2 "  sha256:" $1 }' "$tmp/patches")
The modified tree's module hash is $(prov active-h1); ddsonic
computes it, it is not an upstream hash. Each file the patches change or add
starts with a dated notice of the change. go-librespot's license follows.

MODIFIED
	else
		root="$modcache/$(escape "$mod")@$ver"
		if [ ! -d "$root" ]; then
			echo "notices: $mod@$ver is not in the module cache" >&2
			exit 1
		fi
		section "$mod $ver"
	fi
	awk -v m="$mod" '$1 == m {
		d = $3
		while (index(d, m) == 1) {
			print d
			if (d == m) break
			sub(/\/[^\/]*$/, "", d)
		}
	}' "$tmp/pkgs" | sort -u > "$tmp/dirs"
	found=0
	while read -r dir; do
		find "$root${dir#"$mod"}" -maxdepth 1 -type f ! -name '*.go' \( \
			-iname 'licen[sc]e*' -o -iname 'copying*' -o -iname 'notice*' -o \
			-iname 'unlicense*' -o -iname 'copyright*' -o -iname 'patents*' \) | sort > "$tmp/files"
		while read -r f; do
			found=1
			printf -- '--- %s ---\n' "$mod${f#"$root"}"
			cat "$f"
			echo
		done < "$tmp/files"
	done < "$tmp/dirs"
	if [ "$found" = 0 ]; then
		case "$mod" in
		github.com/xlab/vorbis-go)
			cat <<'NOTE'
This module ships no license file at the pinned revision. It is generated Go
bindings (c-for-go) to libvorbis, whose BSD-3-Clause license is reproduced
above. go-librespot's Vorbis decoder pulls it in. ddsonic ships it as inherited,
as cliamp does, and names the gap here.
NOTE
			;;
		*)
			echo "notices: $mod@$ver has no license file; add a note for it in packaging/notices.sh" >&2
			exit 1
			;;
		esac
	fi
done < "$tmp/mods"
