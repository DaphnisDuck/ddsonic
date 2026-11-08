#!/bin/sh
# ddsonic: builds the macOS ARM64 (darwin/arm64) files for an existing release
# tag, natively on an Apple Silicon Mac:
#
#   sh packaging/darwin-arm64/release.sh <version> <codec-prefix> <out>
#
# Run it from a clean checkout of this repository's tooling that also has the
# release tag. <codec-prefix> is what packaging/darwin-arm64/codecs.sh built;
# <out> must not exist yet, or be empty. The files are staged and moved into
# <out> only when every check has passed; on failure <out> stays empty. The
# version must be pinned in sources.txt. It writes four files to <out>:
#
#   ddsonic-<v>-darwin-arm64.tar.gz                  the binary and its papers
#   ddsonic-<v>-darwin-arm64-sbom.cdx.json           CycloneDX SBOM
#   ddsonic-<v>-darwin-arm64-THIRD_PARTY_NOTICES.txt notices
#   SHA256SUMS-darwin-arm64                          checksums of those three
#
# The checksum file has its own name, so it cannot be confused with, or
# replace, a release's SHA256SUMS. Nothing is uploaded or published.
#
# The source built is the tag's commit and nothing else: the tag is resolved
# once, its tracked files are archived, and that commit goes into the binary
# (-X main.sourceSHA) and the manifest, as in packaging/release.sh. The build
# also depends on tooling outside the tag (this directory, the codec sources,
# Go, Xcode's compiler and SDK, pkg-config); the manifest records each, and
# the build is not reproducible from the tag alone.
set -eu

version=${1:?usage: release.sh <version> <codec-prefix> <out>}
prefix=${2:?usage: release.sh <version> <codec-prefix> <out>}
out=${3:?usage: release.sh <version> <codec-prefix> <out>}

fail() { echo "release-darwin: $*" >&2; exit 1; }

if ! printf '%s\n' "$version" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?'; then
	fail "'$version' is not a version tag (v1.2.3 or v1.2.3-rc.1)"
fi
if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
	fail "this builds on an Apple Silicon Mac (Darwin arm64) only"
fi
[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" != 1 ] || fail "this shell runs under Rosetta; start a native arm64 shell"
for tool in git go clang lipo otool codesign vtool xcrun pkg-config jq shasum tar gzip strings; do
	command -v "$tool" >/dev/null || fail "$tool is missing"
done
[ -f "$prefix/codecs.txt" ] || fail "$prefix has no codecs.txt; run packaging/darwin-arm64/codecs.sh first"
prefix=$(cd "$prefix" && pwd -P)
if [ -e "$out" ] && [ -n "$(ls -A "$out")" ]; then
	fail "$out is not empty"
fi
mkdir -p "$out"
out=$(cd "$out" && pwd -P)
# Outputs are staged in the work directory and moved into <out> only when
# every check has passed, so a failed build leaves <out> empty.
final=$out

# The tooling: this checkout, which must be clean, so its commit names it.
here=$(cd "$(dirname "$0")" && pwd -P)
repo=$(git -C "$here" rev-parse --show-toplevel)
[ -z "$(git -C "$repo" status --porcelain)" ] || fail "the tooling checkout $repo has uncommitted changes"
tooling=$(git -C "$repo" rev-parse --verify 'HEAD^{commit}')

# The source: the release tag, which must match its pinned line in
# sources.txt (tag object and peeled commit), resolved once.
pinned=$(awk -v v="$version" '$1 == v { print $2, $3 }' "$here/sources.txt")
[ -n "$pinned" ] || fail "$version is not pinned in $here/sources.txt"
tagobj=$(git -C "$repo" rev-parse --verify "refs/tags/$version") || fail "no tag $version in $repo"
commit=$(git -C "$repo" rev-parse --verify "refs/tags/$version^{commit}") || fail "no tag $version in $repo"
[ "$tagobj $commit" = "$pinned" ] || fail "tag $version is $tagobj -> $commit; sources.txt pins $pinned"
epoch=$(git -C "$repo" log -1 --format=%ct "$commit")
name="ddsonic-${version#v}"
bin="$name-darwin-arm64"

# The toolchain: the Go version the tag pins (mise.toml), local only.
export GOTOOLCHAIN=local
want_go="go$(git -C "$repo" show "$commit:mise.toml" | awk -F'"' '$1 ~ /^go *= */ { print $2 }')"
[ "$(go env GOVERSION)" = "$want_go" ] || fail "go is $(go env GOVERSION); the tag pins $want_go"
[ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" = darwin/arm64 ] || fail "go is not the darwin/arm64 toolchain"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
out="$work/out"
mkdir "$out"
tree="$work/$name-source"
mkdir -p "$tree"
git -C "$repo" archive --format=tar "$commit" | tar -x -C "$tree"
cd "$tree"
go mod vendor

# The build environment: only the codec prefix's libraries, no inherited
# cgo flags. The deployment target matches the codec libraries'.
target=11.0
export CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 GOFLAGS=-buildvcs=false
export MACOSX_DEPLOYMENT_TARGET="$target"
export PKG_CONFIG_LIBDIR="$prefix/lib/pkgconfig"
unset PKG_CONFIG_PATH CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_LDFLAGS CC CXX

# The same flags as packaging/release.sh.
go build -a -trimpath -mod=vendor \
	-ldflags="-s -w -buildid= -X main.version=${version} -X main.sourceSHA=${commit} -extldflags '-lvorbisenc -lvorbis -lFLAC -logg -lmpg123 -lm'" \
	-o "$work/ddsonic" .

# Checks on what was built.
[ "$(lipo -archs "$work/ddsonic")" = arm64 ] || fail "the binary is not arm64 only: $(lipo -archs "$work/ddsonic")"
otool -L "$work/ddsonic" | awk 'NR > 1 { print $1 }' > "$work/dylibs"
if grep -v -E '^(/usr/lib/|/System/Library/)' "$work/dylibs"; then
	fail "the binary loads the libraries above, from outside macOS"
fi
if grep -i -E 'flac|vorbis|ogg|mpg123' "$work/dylibs"; then
	fail "codec libraries must be linked statically"
fi
codesign --verify "$work/ddsonic" || fail "the binary's signature does not verify"
signature=$(codesign -dv "$work/ddsonic" 2>&1 | awk -F= '$1 == "Signature" { print $2 }')
minos=$(vtool -show-build "$work/ddsonic" | awk '$1 == "minos" { print $2; exit }')
got=$("$work/ddsonic" --version)
[ "$got" = "ddsonic version ${version} (commit ${commit})" ] || fail "--version says '$got'"

# Privacy: the files must not name this machine, its user or a home
# directory. Only counts are printed, never the values. A hit fails the build;
# the generic "/Users/" count is reported for review, not failed on.
privacy() { # file
	n=0
	for value in "$HOME" "$(id -un)" "$(scutil --get LocalHostName 2>/dev/null || true)" \
		"$(scutil --get ComputerName 2>/dev/null || true)" "$(hostname -s)"; do
		[ -n "$value" ] || continue
		c=$(strings -a "$1" | grep -c -F -- "$value" || true)
		n=$((n + c))
	done
	echo "$n"
}
hits=$(privacy "$work/ddsonic")
[ "$hits" = 0 ] || fail "the binary contains $hits strings naming this machine, its user or a home directory"
echo "release-darwin: privacy: 0 machine, user or home-directory strings; generic /Users/ strings: $(strings -a "$work/ddsonic" | grep -c -F /Users/ || true)"

mkdir "$work/$bin"
cp "$work/ddsonic" "$work/$bin/ddsonic"
cp LICENSE LICENSE-GPL-3.0 README.md CHANGELOG.md "$work/$bin/"
if [ -f SOURCE-PROVENANCE.md ]; then
	cp SOURCE-PROVENANCE.md "$work/$bin/"
fi
sed -n 's/.*<img src="\([^":]*\)".*/\1/p' README.md | while read -r img; do
	mkdir -p "$work/$bin/$(dirname "$img")"
	cp "$img" "$work/$bin/$img"
done
sh "$here/notices.sh" "$work/ddsonic" "$prefix" > "$work/$bin/THIRD_PARTY_NOTICES"
for nested in github.com/dop251/goja/ftoa/internal/fast/LICENSE_V8 \
	google.golang.org/api/internal/third_party/uritemplates/LICENSE; do
	if [ -d "vendor/$(dirname "$nested")" ] && ! grep -qF -- "--- $nested ---" "$work/$bin/THIRD_PARTY_NOTICES"; then
		fail "THIRD_PARTY_NOTICES lacks $nested"
	fi
done

# The manifest: the fields release acceptance reads (as in packaging/
# release.sh), then what this build depended on outside the tag.
jq -R -n \
	--arg version "$version" --arg commit "$commit" \
	--arg goos "$(go env GOOS)" --arg goarch "$(go env GOARCH)" --arg gover "$(go env GOVERSION)" \
	--arg sum "$(shasum -a 256 "$work/ddsonic" | cut -d' ' -f1)" \
	--arg tooling "$tooling" --arg macos "$(sw_vers -productVersion)" \
	--arg clang "$(clang --version | head -n 1)" --arg sdk "$(xcrun --show-sdk-version)" \
	--arg target "$target" --arg minos "$minos" --arg signature "$signature" \
	--arg pkgconfig "$(pkg-config --version)" '
	{
		name: "ddsonic",
		version: $version,
		source_sha: $commit,
		goos: $goos,
		goarch: $goarch,
		go_version: $gover,
		binary_sha256: $sum,
		source_tag: $version,
		build: {
			reproducible_from_tag_alone: false,
			tooling: ("packaging/darwin-arm64 at commit " + $tooling + " of github.com/DaphnisDuck/ddsonic"),
			host: ("macOS " + $macos + " arm64"),
			compiler: $clang,
			macos_sdk: $sdk,
			deployment_target: $target,
			minos: $minos,
			signature: $signature,
			pkg_config: $pkgconfig,
			codecs: [inputs | split(" ") | {name: .[0], version: .[1], url: .[2], sha256: .[3]}]
		}
	}' < "$prefix/codecs.txt" > "$work/$bin/RELEASE-MANIFEST.json"

# The archive: a fixed order, owner and time, and no macOS metadata, so two
# builds of the same binary give the same archive.
find "$work/$bin" -exec touch -h -t "$(TZ=UTC date -r "$epoch" +%Y%m%d%H%M.%S)" {} +
(cd "$work" && find "$bin" | LC_ALL=C sort) > "$work/files"
COPYFILE_DISABLE=1 TZ=UTC tar -C "$work" --format ustar --numeric-owner --uid 0 --gid 0 \
	--no-mac-metadata -n -c -f - -T "$work/files" | gzip -n -9 > "$out/$bin.tar.gz"
sh "$here/sbom.sh" "$work/ddsonic" "$version" "$prefix" > "$out/$bin-sbom.cdx.json"
cp "$work/$bin/THIRD_PARTY_NOTICES" "$out/$bin-THIRD_PARTY_NOTICES.txt"

for f in "$out/$bin-sbom.cdx.json" "$out/$bin-THIRD_PARTY_NOTICES.txt"; do
	hits=$(privacy "$f")
	[ "$hits" = 0 ] || fail "$(basename "$f") contains $hits strings naming this machine, its user or a home directory"
done

cd "$out"
shasum -a 256 "$bin.tar.gz" "$bin-sbom.cdx.json" "$bin-THIRD_PARTY_NOTICES.txt" > SHA256SUMS-darwin-arm64
shasum -a 256 -c -s SHA256SUMS-darwin-arm64 || fail "the staged files do not match their checksums"
mv "$bin.tar.gz" "$bin-sbom.cdx.json" "$bin-THIRD_PARTY_NOTICES.txt" SHA256SUMS-darwin-arm64 "$final/"
cat "$work/$bin/RELEASE-MANIFEST.json"
cat "$final/SHA256SUMS-darwin-arm64"
