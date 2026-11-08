#!/bin/sh
# ddsonic: builds the static codec libraries for a macOS ARM64 (darwin/arm64)
# build of ddsonic, natively on an Apple Silicon Mac:
#
#   sh packaging/darwin-arm64/codecs.sh <prefix>
#
# It is the ARM64 counterpart of .github/scripts/build-darwin-amd64-codecs.sh,
# upstream's Intel recipe, which stays as upstream wrote it: the same four
# source releases at the same SHA-256 sums, built for arm64 on the machine
# itself instead of cross-built for x86_64. Static archives mean the ddsonic
# binary needs no Homebrew or other third-party libraries at run time.
#
# <prefix> must not exist yet, or be empty. It receives lib/ (static archives
# only) with lib/pkgconfig/, include/, src/ (the verified source archives),
# licenses/<library>/ (each library's license files, for the notices) and
# codecs.txt, one line per library: name, version, source URL, SHA-256.
# A path outside the home directory, such as /tmp/ddsonic-arm64/codecs, keeps
# the user's name out of the build logs.
#
# The deployment target is macOS 13.0, Go 1.27's minimum, for new builds.
# DDSONIC_MACOS_TARGET=11.0 rebuilds the codecs as v1.0.0 was built. The
# target is recorded in <prefix>/deployment-target; release.sh checks it.
#
# Needs: Xcode Command Line Tools (clang, make, lipo), curl, shasum, tar and
# pkg-config. Nothing is installed outside <prefix>.
set -eu

prefix=${1:?usage: codecs.sh <prefix>}

if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
	echo "codecs: this builds on an Apple Silicon Mac (Darwin arm64) only" >&2
	exit 1
fi
if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	echo "codecs: this shell runs under Rosetta; start a native arm64 shell" >&2
	exit 1
fi
for tool in clang make lipo curl shasum tar pkg-config; do
	command -v "$tool" >/dev/null || { echo "codecs: $tool is missing" >&2; exit 1; }
done
if [ -e "$prefix" ] && [ -n "$(ls -A "$prefix")" ]; then
	echo "codecs: $prefix is not empty" >&2
	exit 1
fi
mkdir -p "$prefix/src" "$prefix/licenses"
prefix=$(cd "$prefix" && pwd -P)
work="$prefix/build"
mkdir -p "$work"

# -ffile-prefix-map keeps the build directory's path out of the objects
# (assertion messages name files).
target=${DDSONIC_MACOS_TARGET:-13.0}
case "$target" in
11.0 | 13.0) ;;
*)
	echo "codecs: DDSONIC_MACOS_TARGET must be 13.0 (new builds) or 11.0 (v1.0.0)" >&2
	exit 1
	;;
esac
export MACOSX_DEPLOYMENT_TARGET="$target"
export CC="clang -arch arm64"
export CFLAGS="-arch arm64 -O2 -mmacosx-version-min=$target -ffile-prefix-map=$work=/ddsonic-codecs"
# Only this prefix's .pc files: never Homebrew's or the system's.
export PKG_CONFIG_LIBDIR="$prefix/lib/pkgconfig"
unset PKG_CONFIG_PATH
triplet=aarch64-apple-darwin
configure_args="--build=$triplet --host=$triplet --prefix=$prefix --disable-shared --enable-static"
jobs=$(sysctl -n hw.ncpu)

# fetch <name> <version> <url> <sha256>: download, verify, unpack, record.
fetch() {
	file="$prefix/src/${3##*/}"
	curl -fsSL --proto '=https' --proto-redir '=https' "$3" -o "$file"
	echo "$4  $file" | shasum -a 256 -c -
	tar -xf "$file" -C "$work"
	printf '%s %s %s %s\n' "$1" "$2" "$3" "$4" >> "$prefix/codecs.txt"
}

# licenses <name> <file>...: keep a library's license files for the notices.
licenses() {
	name=$1
	shift
	mkdir -p "$prefix/licenses/$name"
	cp "$@" "$prefix/licenses/$name/"
}

fetch libogg 1.3.5 https://downloads.xiph.org/releases/ogg/libogg-1.3.5.tar.xz \
	c4d91be36fc8e54deae7575241e03f4211eb102afb3fc0775fbbc1b740016705
(
	cd "$work/libogg-1.3.5"
	# shellcheck disable=SC2086 # configure_args is a list of words
	./configure $configure_args
	make -j"$jobs"
	make install
	licenses libogg COPYING
)

fetch libvorbis 1.3.7 https://downloads.xiph.org/releases/vorbis/libvorbis-1.3.7.tar.xz \
	b33cc4934322bcbf6efcbacf49e3ca01aadbea4114ec9589d1b1e9d20f72954b
(
	cd "$work/libvorbis-1.3.7"
	# The 1.3.7 configure script adds -force_cpusubtype_ALL on Darwin, which
	# current Apple toolchains reject (as in the Intel recipe).
	sed -i.orig 's/-force_cpusubtype_ALL//g' configure
	# shellcheck disable=SC2086
	./configure $configure_args --with-ogg="$prefix" --disable-docs --disable-examples --disable-oggtest
	make -j"$jobs"
	make install
	licenses libvorbis COPYING
)

fetch flac 1.5.0 https://downloads.xiph.org/releases/flac/flac-1.5.0.tar.xz \
	f2c1c76592a82ffff8413ba3c4a1299b6c7ab06c734dee03fd88630485c2b920
(
	cd "$work/flac-1.5.0"
	# shellcheck disable=SC2086
	./configure $configure_args --disable-ogg --disable-programs --disable-examples \
		--disable-cpplibs --disable-doxygen-docs
	make -j"$jobs"
	make install
	# libFLAC is under the Xiph.Org BSD-style license (COPYING.Xiph); the
	# tools and C++ library, which are not built, are under GPL or LGPL.
	licenses flac COPYING.Xiph
)

fetch mpg123 1.32.6 https://www.mpg123.de/download/mpg123-1.32.6.tar.bz2 \
	ccdd1d0abc31d73d8b435fc658c79049d0a905b30669b6a42a03ad169dc609e6
(
	cd "$work/mpg123-1.32.6"
	# shellcheck disable=SC2086
	./configure $configure_args --with-cpu=aarch64 --disable-components \
		--enable-libmpg123 --disable-network
	make -j"$jobs"
	make install
	licenses mpg123 COPYING
)

rm -rf "$work"

# Static arm64 archives only: a dynamic library here would be linked instead.
if ls "$prefix"/lib/*.dylib >/dev/null 2>&1; then
	echo "codecs: $prefix/lib holds a dynamic library" >&2
	exit 1
fi
for lib in libogg libvorbis libvorbisenc libFLAC libmpg123; do
	archs=$(lipo -archs "$prefix/lib/$lib.a")
	if [ "$archs" != arm64 ]; then
		echo "codecs: $lib.a is for '$archs', not arm64" >&2
		exit 1
	fi
done
for pc in ogg vorbis vorbisenc flac libmpg123; do
	pkg-config --exists "$pc" || { echo "codecs: pkg-config does not find $pc in $prefix" >&2; exit 1; }
done
printf '%s\n' "$target" > "$prefix/deployment-target"
cat "$prefix/codecs.txt"
