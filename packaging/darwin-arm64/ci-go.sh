#!/bin/sh
# ddsonic: installs Go for the macOS workflow (.github/workflows/macos.yml):
#
#   sh packaging/darwin-arm64/ci-go.sh <dir>
#
# The version is the one mise.toml pins. The darwin/arm64 archive is
# downloaded from go.dev over HTTPS and must match the SHA-256 listed here;
# a version without a listed SHA-256 fails, so a new pin needs its SHA-256
# added in the same change. Go is unpacked into <dir>/go, and <dir>/go/bin is
# added to the job's PATH (GITHUB_PATH).
set -eu

dir=${1:?usage: ci-go.sh <dir>}

fail() { echo "ci-go: $*" >&2; exit 1; }

version=$(awk -F'"' '$1 ~ /^go *= */ { print $2 }' mise.toml)
case "$version" in
1.27.4) sum=91a44a2b8a716600ad94a4f143e156116620db8828086a6e3602291be9bd0bb4 ;;
*) fail "mise.toml pins Go '$version', which has no SHA-256 here" ;;
esac
[ "$(uname -s)/$(uname -m)" = Darwin/arm64 ] || fail "this installs Go for macOS on Apple Silicon only"
[ ! -e "$dir/go" ] || fail "$dir/go exists"

archive="$dir/go$version.darwin-arm64.tar.gz"
curl -fsSL --retry 3 --retry-all-errors --proto '=https' --proto-redir '=https' -o "$archive" "https://go.dev/dl/go$version.darwin-arm64.tar.gz"
echo "$sum  $archive" | shasum -a 256 -c - || fail "go$version.darwin-arm64.tar.gz does not match its SHA-256"
tar -xzf "$archive" -C "$dir"
rm -f "$archive"
[ "$("$dir/go/bin/go" env GOVERSION)" = "go$version" ] || fail "the unpacked Go is not go$version"
if [ -n "${GITHUB_PATH:-}" ]; then
	echo "$dir/go/bin" >> "$GITHUB_PATH"
fi
"$dir/go/bin/go" version
