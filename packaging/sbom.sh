#!/bin/sh
# Writes a minimal CycloneDX 1.5 SBOM for the binary $1 (version $2) to stdout:
# the Go modules the binary reports, the Go toolchain, and the native
# libraries. Runs inside the build image.
set -eu

binary=$1
version=$2

native() { # name version license linkage
	jq -n --arg n "$1" --arg v "$2" --arg l "$3" --arg k "$4" \
		'{type: "library", name: $n, version: $v, licenses: [{license: {id: $l}}], properties: [{name: "ddsonic:linkage", value: $k}]}'
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

go version -m "$binary" > "$tmp/buildinfo"
awk '$1 == "dep" { print $2, $3, $4 }' "$tmp/buildinfo" | sort -u > "$tmp/mods"
# ddsonic: the one allowed replacement (as in packaging/notices.sh): the
# tracked, modified go-librespot. It is recorded as modified, with upstream
# v0.9.1 as its ancestor and the patches as its pedigree, and no package URL
# of its own, since pkg:golang/...@v0.9.1 names the unmodified module.
replaced_mod=github.com/devgianlu/go-librespot
replaced_ver=v0.9.1
replaced_dir=./third_party/go-librespot
provenance=third_party/go-librespot.provenance
awk '$1 == "=>" { print last, $2, $3 } { last = ($1 == "dep") ? $2 " " $3 : "" }' "$tmp/buildinfo" > "$tmp/replaced"
if [ "$(cat "$tmp/replaced")" != "$replaced_mod $replaced_ver $replaced_dir (devel)" ]; then
	echo "sbom: the binary's module replacements are not exactly $replaced_mod $replaced_ver => $replaced_dir" >&2
	exit 1
fi
prov() { awk -v k="$1" '$1 == k { print $2 }' "$provenance"; }
awk '$1 == "patch" { print $2, $3 }' "$provenance" > "$tmp/patches"
if [ ! -s "$tmp/patches" ] || [ -z "$(prov base-h1)" ] || [ -z "$(prov active-h1)" ]; then
	echo "sbom: $provenance does not record the base, the patches and active-h1" >&2
	exit 1
fi
modified() { # module version
	jq -R -n --arg n "$1" --arg v "$2" --arg dir "${replaced_dir#./}" --arg base "$(prov base-h1)" --arg active "$(prov active-h1)" '
		[inputs | split(" ") | {path: .[0], sum: .[1]}] as $p
		| {type: "library", name: $n, version: $v,
		   description: ("go-librespot " + $v + " modified by ddsonic: built from " + $dir + " in the source archive"),
		   properties: ([{name: "ddsonic:modified", value: "true"},
		                 {name: "ddsonic:replacement", value: $dir},
		                 {name: "ddsonic:active-h1", value: $active}]
		                + [$p[] | {name: "ddsonic:patch", value: (.path + " " + .sum)}]),
		   pedigree: {
		     ancestors: [{type: "library", name: $n, version: $v, purl: ("pkg:golang/" + $n + "@" + $v),
		                  properties: [{name: "go:sum", value: $base}]}],
		     patches: [$p[] | {type: "unofficial", diff: {url: .path}}],
		     notes: ("upstream " + $n + " " + $v + " (Go module checksum " + $base + ") with the listed patches applied in order; active-h1 " + $active + " is computed by ddsonic, not an upstream hash")}}' < "$tmp/patches"
}
if [ ! -s "$tmp/mods" ]; then
	echo "sbom: $binary names no Go modules" >&2
	exit 1
fi

{
	while read -r mod ver sum; do
		if [ "$mod $ver" = "$replaced_mod $replaced_ver" ]; then
			modified "$mod" "$ver"
			continue
		fi
		jq -n --arg n "$mod" --arg v "$ver" --arg s "$sum" \
			'{type: "library", name: $n, version: $v, purl: ("pkg:golang/" + $n + "@" + $v)}
			 + (if $s == "" then {} else {properties: [{name: "go:sum", value: $s}]} end)'
	done < "$tmp/mods"
	jq -n --arg v "$(go env GOVERSION)" '{type: "library", name: "go (standard library and runtime)", version: $v, licenses: [{license: {id: "BSD-3-Clause"}}]}'
	native mpg123 "$MPG123_VERSION" LGPL-2.1-only static
	native flac "$(dpkg-query -W -f '${Version}' libflac-dev)" BSD-3-Clause static
	native libvorbis "$(dpkg-query -W -f '${Version}' libvorbis-dev)" BSD-3-Clause static
	native libogg "$(dpkg-query -W -f '${Version}' libogg-dev)" BSD-3-Clause static
	native alsa-lib "system" LGPL-2.1-or-later dynamic
	native glibc "system (built against $(dpkg-query -W -f '${Version}' libc6))" LGPL-2.1-or-later dynamic
} > "$tmp/components"

jq -s --arg v "$version" '{
	bomFormat: "CycloneDX",
	specVersion: "1.5",
	version: 1,
	metadata: {component: {type: "application", name: "ddsonic", version: $v, licenses: [{license: {id: "GPL-3.0-only"}}]}},
	components: .
}' "$tmp/components"
