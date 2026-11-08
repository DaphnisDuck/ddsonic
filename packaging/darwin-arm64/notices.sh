#!/bin/sh
# ddsonic: writes THIRD_PARTY_NOTICES for the macOS ARM64 binary $1, built
# against the codec prefix $2 (packaging/darwin-arm64/codecs.sh), to stdout.
# Run from the build tree packaging/darwin-arm64/release.sh prepares (the
# tag's source with vendor/).
#
# The Go part follows packaging/notices.sh, the Linux release's, step for
# step, written for macOS's tools (shasum, awk) instead of GNU's. The native
# part comes from the codec prefix and from the binary's own list of the
# system libraries it loads (otool -L). packaging/notices.sh is not changed:
# it is the Linux release's, unchanged since v1.0.0.
set -eu
# The C locale, as in the Linux build image: sort order and awk character classes.
export LC_ALL=C

binary=$1
prefix=$2

cat <<'HEAD'
ddsonic: third-party notices (macOS ARM64 build)

ddsonic's own source is MIT-licensed (see LICENSE). The ddsonic executable links
a modified go-librespot (its section below says how it differs from upstream),
which is GPL-3.0, so the executable as a whole is distributed
under GPL-3.0 (see LICENSE-GPL-3.0). It was built from the source of the
release tag that RELEASE-MANIFEST.json names; the release's
ddsonic-<version>-source.tar.gz holds that source. The tooling that built this
macOS executable, and the codec libraries' source releases, are named in
RELEASE-MANIFEST.json; they are not part of that source archive.

This file lists everything compiled or linked into the executable, with each
component's license text.

HEAD

section() {
	printf '\n%s\n%s\n\n' "================================================================================" "$1"
}

version_of() { awk -v n="$1" '$1 == n { print $2 }' "$prefix/codecs.txt"; }

section "Native libraries"
cat <<NATIVE
Linked statically, built from the source releases RELEASE-MANIFEST.json lists:
  mpg123 $(version_of mpg123) (LGPL-2.1)
  FLAC (libFLAC) $(version_of flac) (BSD-3-Clause; Xiph.Org)
  libvorbis $(version_of libvorbis) (BSD-3-Clause; Xiph.Org)
  libogg $(version_of libogg) (BSD-3-Clause; Xiph.Org)
Linked dynamically, from the macOS system it runs on (part of macOS, not
shipped with ddsonic):
$(otool -L "$binary" | awk 'NR > 1 { print "  " $1 }')

mpg123 is LGPL-2.1. The same mpg123 source release (the same file, by its
SHA-256) is in native/ of the release's ddsonic-<version>-source.tar.gz.
NATIVE

native() {
	section "$1"
	for f in "$prefix/licenses/$2"/*; do
		cat "$f"
	done
}
native "mpg123" mpg123
# One libmpg123 source file, src/libmpg123/icy2utf8.c, carries its own notice,
# whose permission asks that it be retained or reproduced in an accompanying
# document. It is reproduced here from the mpg123 source release in the prefix.
mpg123_version=$(version_of mpg123)
icy=$(tar -xOjf "$prefix/src/mpg123-$mpg123_version.tar.bz2" "mpg123-$mpg123_version/src/libmpg123/icy2utf8.c" |
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
native "FLAC (libFLAC)" flac
native "libvorbis" libvorbis
native "libogg" libogg

section "Go standard library and runtime ($(go env GOVERSION))"
cat "$(go env GOROOT)/LICENSE"

# The Go modules, as in packaging/notices.sh: the binary names them (go
# version -m); the vendored build tree says which of their packages are
# compiled in; each package's directories up to its module root are searched
# for license files in the module cache.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

go version -m "$binary" > "$tmp/buildinfo"
awk '$1 == "dep" { print $2, $3 }' "$tmp/buildinfo" | sort -u > "$tmp/shipped"

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
if ! grep -qxF "module $replaced_mod" "$provenance" || ! grep -qxF "base-version $replaced_ver" "$provenance"; then
	echo "notices: $provenance does not name $replaced_mod $replaced_ver" >&2
	exit 1
fi
awk '$1 == "patch" { sub(/^sha256:/, "", $3); print $3 "  " $2 }' "$provenance" > "$tmp/patches"
awk '{ print $2 }' "$tmp/patches" | sort > "$tmp/patches.listed"
find patches/go-librespot -type f | sort > "$tmp/patches.present"
if [ ! -s "$tmp/patches" ] || ! cmp -s "$tmp/patches.listed" "$tmp/patches.present" || ! shasum -a 256 -c -s "$tmp/patches" >&2; then
	echo "notices: the patches in patches/go-librespot are not the ones $provenance records" >&2
	exit 1
fi
prov() { awk -v k="$1" '$1 == k { print $2 }' "$provenance"; }
go list -mod=vendor -deps -f '{{with .Module}}{{.Path}} {{.Version}} {{end}}{{.ImportPath}}' . > "$tmp/list"
awk 'NF == 3' "$tmp/list" > "$tmp/pkgs"
awk '{ print $1, $2 }' "$tmp/pkgs" | sort -u > "$tmp/mods"
if [ ! -s "$tmp/mods" ] || ! cmp -s "$tmp/shipped" "$tmp/mods"; then
	echo "notices: the binary's modules and the build tree's differ:" >&2
	diff "$tmp/shipped" "$tmp/mods" >&2 || true
	exit 1
fi

modcache=$(go env GOMODCACHE)
# Module paths in the cache escape capitals as "!" plus the lower-case letter
# (awk, as macOS's sed has no \L).
escape() {
	printf '%s\n' "$1" | awk '{
		out = ""
		for (i = 1; i <= length($0); i++) {
			c = substr($0, i, 1)
			out = out (c ~ /[A-Z]/ ? "!" tolower(c) : c)
		}
		print out
	}'
}

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
			echo "notices: $mod@$ver has no license file; add a note for it in packaging/darwin-arm64/notices.sh" >&2
			exit 1
			;;
		esac
	fi
done < "$tmp/mods"
