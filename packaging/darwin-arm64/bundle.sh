#!/bin/sh
# ddsonic: writes the build-sources archive of a macOS ARM64 build:
#
#   sh packaging/darwin-arm64/bundle.sh <version> <commit> <codec-prefix> <out>
#
# release.sh calls it. <out>/ddsonic-<v>-darwin-arm64-build-sources.tar.gz
# holds what the build used besides ddsonic's own source:
#   codecs/                 the four codec source releases from
#                           <codec-prefix>/src, each checked against
#                           <codec-prefix>/codecs.txt and the pins in
#                           codecs.sh;
#   packaging/darwin-arm64/ these scripts, as committed at the tooling commit
#                           (this checkout's HEAD, which must be clean);
#   README                  what the archive is and how to rebuild, including
#                           with a modified codec library.
# The archive is deterministic: a sorted ustar archive, owner 0:0, fixed
# modes, every file dated to <commit>'s commit time, gzip -n. It runs with the
# macOS tar (bsdtar) or GNU tar.
set -eu

version=${1:?usage: bundle.sh <version> <commit> <codec-prefix> <out>}
commit=${2:?usage: bundle.sh <version> <commit> <codec-prefix> <out>}
prefix=${3:?usage: bundle.sh <version> <commit> <codec-prefix> <out>}
out=${4:?usage: bundle.sh <version> <commit> <codec-prefix> <out>}

fail() { echo "bundle: $*" >&2; exit 1; }

printf '%s\n' "$version" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?' || fail "'$version' is not a version tag"
printf '%s\n' "$commit" | grep -Eqx '[0-9a-f]{40}' || fail "'$commit' is not a full commit"
[ -f "$prefix/codecs.txt" ] || fail "$prefix has no codecs.txt"
[ -d "$out" ] || fail "$out is not a directory"

here=$(cd "$(dirname "$0")" && pwd -P)
repo=$(git -C "$here" rev-parse --show-toplevel)
[ -z "$(git -C "$repo" status --porcelain)" ] || fail "the tooling checkout $repo has uncommitted changes"
tooling=$(git -C "$repo" rev-parse --verify 'HEAD^{commit}')
epoch=$(git -C "$repo" log -1 --format=%ct "$commit") || fail "no commit $commit in $repo"
case "$version" in
v1.0.0) target=11.0 ;;
*) target=13.0 ;;
esac
v=${version#v}
name="ddsonic-$v-darwin-arm64-build-sources"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
root="$work/$name"
mkdir -p "$root/codecs"

# The codec sources: each line of codecs.txt (name version url sha256) must be
# one of codecs.sh's pins at the tooling commit, and its archive must match.
git -C "$repo" show "$tooling:packaging/darwin-arm64/codecs.sh" |
	awk '/^fetch [a-z0-9]+ / { name = $2; ver = $3; url = $4; getline; gsub(/[ \t]/, ""); print name, ver, url, $0 }' > "$work/pins"
[ "$(wc -l < "$work/pins" | tr -d ' ')" = 4 ] || fail "expected 4 pins in codecs.sh"
[ "$(wc -l < "$prefix/codecs.txt" | tr -d ' ')" = 4 ] || fail "expected 4 lines in $prefix/codecs.txt"
while read -r cname cver curl csha; do
	grep -qxF "$cname $cver $curl $csha" "$work/pins" || fail "$cname $cver in codecs.txt is not pinned in codecs.sh"
	f=${curl##*/}
	[ -f "$prefix/src/$f" ] || fail "$prefix/src/$f is missing"
	[ "$(shasum -a 256 < "$prefix/src/$f" | cut -c1-64)" = "$csha" ] || fail "$prefix/src/$f does not match its SHA-256"
	cp "$prefix/src/$f" "$root/codecs/$f"
done < "$prefix/codecs.txt"

git -C "$repo" archive --format=tar "$tooling" packaging/darwin-arm64 | tar -x -C "$root"

table=$(awk '{ printf "  %-16s %s\n  %-16s %s\n", $1 " " $2, $4, "", $3 }' "$prefix/codecs.txt")
cat > "$root/README" <<EOF
ddsonic $v, macOS ARM64 build: build sources
==============================================

This archive goes with the macOS ARM64 (Apple Silicon) files of the ddsonic
$version release:

  ddsonic-$v-darwin-arm64.tar.gz                    the executable
  ddsonic-$v-darwin-arm64-sbom.cdx.json             its SBOM
  ddsonic-$v-darwin-arm64-THIRD_PARTY_NOTICES.txt   its third-party notices
  SHA256SUMS-darwin-arm64                           checksums of all four files

It holds what that build used besides ddsonic's own source:

  codecs/                 the four codec source releases, as downloaded,
                          checked against the SHA-256 sums below
  packaging/darwin-arm64/ the scripts that built the codecs and the
                          executable, exactly as in commit
                          $tooling
                          of https://github.com/DaphnisDuck/ddsonic

ddsonic's own source, its Go dependencies (vendor/) and the modified
go-librespot (third_party/go-librespot) are in ddsonic-$v-source.tar.gz on the
same release. The executable was built from commit
$commit, which it names in
\`ddsonic --version\`.


Provenance
----------

- Source: tag $version, commit $commit.
- RELEASE-MANIFEST.json, inside the executable's archive, records the tooling
  commit, the Go version, and the macOS, compiler, SDK and pkg-config
  versions the build used.
- The build is not reproducible from the tag alone: it also depends on the
  scripts here, the codec sources, the toolchain and the SDK. No claim is made
  that another machine produces the same bytes.

Codec sources (name and version, SHA-256, URL):

$table

Check them with: shasum -a 256 codecs/*


Licenses
--------

- ddsonic's source is MIT. The executable links go-librespot, which is
  GPL-3.0, so the executable as a whole is distributed under GPL-3.0.
- Linked statically into the executable: libmpg123 (LGPL-2.1; one file in
  it, icy2utf8.c, carries its own permissive notice), libFLAC, libvorbis
  and libogg (BSD-3-Clause). flac's command-line tools and C++ library were
  not built or linked.
- Linked dynamically: macOS system frameworks and libraries only.
- The license texts and notices, including the icy2utf8.c notice, are in the
  release's ddsonic-$v-darwin-arm64-THIRD_PARTY_NOTICES.txt and in each
  source release.

This file describes how the build was made and how to rebuild it. It makes no
claim about license compliance.


Rebuilding, and relinking with a modified codec library
-------------------------------------------------------

You need an Apple Silicon Mac with Xcode Command Line Tools
(xcode-select --install), Go as pinned in the tag's mise.toml
(https://go.dev/dl/), pkg-config, git and jq. Run everything in a native
arm64 shell, not under Rosetta.

Route 1: the published build, with its scripts.

  git clone https://github.com/DaphnisDuck/ddsonic.git
  cd ddsonic
  git checkout $tooling
  DDSONIC_MACOS_TARGET=$target sh packaging/darwin-arm64/codecs.sh "\$HOME/ddsonic-codecs"
  DDSONIC_EXPECTED_COMMIT=$commit \\
    sh packaging/darwin-arm64/release.sh $version "\$HOME/ddsonic-codecs" "\$HOME/ddsonic-out"

codecs.sh downloads the four codec releases and checks their SHA-256.
release.sh needs a clean checkout that also has the $version tag; it builds
the tag's commit and writes the release files to the output directory.

Route 2: relink with your own libmpg123 (or another codec library).
The executable links each codec from a static library (.a). To use a
modified library, build it as a static arm64 library into a prefix that holds
the other three, then link ddsonic's published source against that prefix.

1. Build the four codecs into a prefix, with codecs.sh (route 1) or by hand
   from the archives in codecs/, using the configure options in
   packaging/darwin-arm64/codecs.sh.
2. Build your modified mpg123 into the same prefix, replacing libmpg123.a,
   with the options the release used:

     export MACOSX_DEPLOYMENT_TARGET=$target CC="clang -arch arm64"
     export CFLAGS="-arch arm64 -O2 -mmacosx-version-min=$target"
     ./configure --build=aarch64-apple-darwin --host=aarch64-apple-darwin \\
       --prefix="\$PREFIX" --disable-shared --enable-static \\
       --with-cpu=aarch64 --disable-components --enable-libmpg123 \\
       --disable-network
     make && make install

   Your library must keep the libmpg123 interface that
   third_party/go-librespot/mp3 uses (found with "pkg-config libmpg123").
3. Unpack ddsonic-$v-source.tar.gz from the release and build in it:

     cd ddsonic-$v-source
     export GOTOOLCHAIN=local CGO_ENABLED=1 GOOS=darwin GOARCH=arm64
     export GOFLAGS=-buildvcs=false MACOSX_DEPLOYMENT_TARGET=$target
     export PKG_CONFIG_LIBDIR="\$PREFIX/lib/pkgconfig"
     unset PKG_CONFIG_PATH CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_LDFLAGS CC CXX
     go build -a -trimpath -mod=vendor \\
       -ldflags="-s -w -buildid= -X main.version=$version -X main.sourceSHA=$commit -extldflags '-lvorbisenc -lvorbis -lFLAC -logg -lmpg123 -lm'" \\
       -o ddsonic .

   These are the flags of packaging/darwin-arm64/release.sh. The result is a
   ddsonic executable linked with your library.
EOF

# Fixed modes and times, then a sorted ustar archive with owner 0:0.
find "$root" -type d -exec chmod 755 {} +
find "$root" -type f -exec chmod 644 {} +
(cd "$work" && find "$name" | LC_ALL=C sort) > "$work/files"
case "$(tar --version 2>&1)" in
*bsdtar*)
	find "$root" -exec touch -h -t "$(TZ=UTC date -r "$epoch" +%Y%m%d%H%M.%S)" {} +
	COPYFILE_DISABLE=1 TZ=UTC tar -C "$work" --format ustar --numeric-owner --uid 0 --gid 0 \
		--no-mac-metadata -n -c -f - -T "$work/files" | gzip -n -9 > "$out/$name.tar.gz"
	;;
*"GNU tar"*)
	tar -C "$work" --format=ustar --numeric-owner --owner=0 --group=0 --mtime="@$epoch" \
		--no-recursion -c -f - -T "$work/files" | gzip -n -9 > "$out/$name.tar.gz"
	;;
*) fail "tar is neither bsdtar nor GNU tar" ;;
esac
echo "bundle: $out/$name.tar.gz"
