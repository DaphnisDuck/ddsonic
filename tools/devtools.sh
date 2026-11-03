#!/bin/sh
# ddsonic: the developer tools `make ci` needs, at CI's pins, and the module
# archives the go-librespot contract gate reads offline.
#
#   tools/devtools.sh install              make tools: the pinned tools into
#                                          $DDSONIC_DEVTOOLS/bin, then fetch
#   tools/devtools.sh fetch                the contract gate's module archives
#                                          (CI's own step runs this)
#   tools/devtools.sh preflight [PROFILE]  make preflight: what is present,
#                                          missing or at the wrong version;
#                                          fast, no network. PROFILE is all
#                                          (the default), core (CI's job go)
#                                          or contract (make sdk-contract)
#
# $DDSONIC_DEVTOOLS defaults to ~/.local/share/ddsonic/devtools, outside the
# repository and outside /tmp, so the tools outlive a session. The Makefile
# puts its bin/ first on PATH when it exists. preflight checks the tools PATH
# finds, not that directory, so CI, which installs them its own way, passes
# it too.
#
# The pins. staticcheck and govulncheck are pinned in
# .github/workflows/ci.yml, which stays where CI installs them; preflight
# fails when the pins here differ from ci.yml's, and when ci.yml's Go
# differs from mise.toml's. Go itself is pinned in mise.toml. shellcheck
# comes from apt in CI (Ubuntu 24.04: 0.9.0); here it is the static release
# binary of the same version.
set -eu

STATICCHECK_MOD=honnef.co/go/tools
STATICCHECK_PIN=v0.7.0-0.dev.0.20261009221906-f1838cc308e5
GOVULNCHECK_MOD=golang.org/x/vuln
GOVULNCHECK_PIN=v1.8.0
SHELLCHECK_PIN=0.9.0
SHELLCHECK_URL=https://github.com/koalaman/shellcheck/releases/download/v0.9.0/shellcheck-v0.9.0.linux.x86_64.tar.xz
# SHA-256 of that archive. koalaman publishes no checksum file for 0.9.0
# and GitHub's release API gives no digest for it. This is the value the
# Python package of it (github.com/shellcheck-py/shellcheck-py, setup.py at
# its v0.9.0.6 tag) pins for the same upstream archive, and the value the
# archive hashed to when it was first downloaded for ddsonic (2026-11-03).
SHELLCHECK_SHA256=700324c6dd0ebea0117591c6cc9d7350d9c7c5c287acbad7630fa17b1d4d9e2f

# The archives the contract gate needs in the module download cache beyond
# what a build fetches (Makefile, sdk-contract): upstream go-librespot,
# tools/sdkgate's x/ modules, and the modules whose source no build needs
# but whose .zip the gate's stock stage downloads (found missing on
# 2026-11-03). fetch gets them; preflight looks for them.
CONTRACT_ARCHIVES="
github.com/devgianlu/go-librespot/@v/v0.9.1
golang.org/x/tools/@v/v0.50.0
golang.org/x/mod/@v/v0.41.0
golang.org/x/sync/@v/v0.23.0
github.com/mattn/go-isatty/@v/v0.0.24
github.com/ncruces/go-strftime/@v/v1.0.0
github.com/sirupsen/logrus/@v/v1.10.2
github.com/stretchr/objx/@v/v0.5.3
github.com/stretchr/testify/@v/v1.12.1
"

root=$(cd "$(dirname "$0")/.." && pwd)
devtools=${DDSONIC_DEVTOOLS:-$HOME/.local/share/ddsonic/devtools}

# go_pin is the Go version mise.toml pins, e.g. 1.27.3.
go_pin() {
	sed -n 's/^go *= *"\([^"]*\)".*/\1/p' "$root/mise.toml"
}

# module_version prints the version of module $2 that binary $1 was built
# from (go version -m), or nothing.
module_version() {
	go version -m "$1" 2>/dev/null | awk -v m="$2" '$1 == "mod" && $2 == m { print $3 }'
}

install_tools() {
	want=go$(go_pin)
	got=$(go env GOVERSION)
	if [ "$got" != "$want" ]; then
		echo "devtools: go is $got, mise.toml pins $want; install $want first" >&2
		exit 1
	fi
	mkdir -p "$devtools/bin" "$devtools/downloads"
	bin=$devtools/bin
	if [ "$(module_version "$bin/staticcheck" "$STATICCHECK_MOD")" != "$STATICCHECK_PIN" ]; then
		GOBIN=$bin GOTOOLCHAIN=local GOFLAGS='' go install "$STATICCHECK_MOD/cmd/staticcheck@$STATICCHECK_PIN"
	fi
	if [ "$(module_version "$bin/govulncheck" "$GOVULNCHECK_MOD")" != "$GOVULNCHECK_PIN" ]; then
		GOBIN=$bin GOTOOLCHAIN=local GOFLAGS='' go install "$GOVULNCHECK_MOD/cmd/govulncheck@$GOVULNCHECK_PIN"
	fi
	if [ "$(shellcheck_version "$bin/shellcheck")" != "$SHELLCHECK_PIN" ]; then
		case "$(uname -s)/$(uname -m)" in
		Linux/x86_64) ;;
		*)
			echo "devtools: the pinned shellcheck is for Linux x86-64; install shellcheck $SHELLCHECK_PIN another way" >&2
			exit 1
			;;
		esac
		archive=$devtools/downloads/$(basename "$SHELLCHECK_URL")
		if ! echo "$SHELLCHECK_SHA256  $archive" | sha256sum -c - >/dev/null 2>&1; then
			curl -fsSL --proto '=https' --proto-redir '=https' -o "$archive.part" "$SHELLCHECK_URL"
			mv "$archive.part" "$archive"
		fi
		if ! echo "$SHELLCHECK_SHA256  $archive" | sha256sum -c -; then
			echo "devtools: $archive does not have the pinned SHA-256; not installed" >&2
			exit 1
		fi
		work=$(mktemp -d)
		tar -xJf "$archive" -C "$work"
		install -m 755 "$work/shellcheck-v$SHELLCHECK_PIN/shellcheck" "$bin/shellcheck"
		rm -r "${work:?}"
	fi
	fetch_archives
	echo "devtools: tools in $bin"
}

# fetch_archives is CI's step "Fetch the contract gate's modules". Go
# verifies each archive against go.sum or the checksum database; the gate
# authenticates the SDK archive again against its provenance.
fetch_archives() {
	cd "$root"
	go mod download
	go list -m all >/dev/null
	(cd tools/sdkgate && go mod download)
	scratch=$(mktemp -d)
	(cd "$scratch" && go mod init ddsonic.invalid/sdkfetch >/dev/null 2>&1 && go mod download github.com/devgianlu/go-librespot@v0.9.1)
	rm -r "${scratch:?}"
	missing=$(missing_archives)
	if [ -n "$missing" ]; then
		echo "devtools: still missing after the fetch:$missing" >&2
		exit 1
	fi
}

missing_archives() {
	d=$(go env GOMODCACHE)/cache/download
	for m in $CONTRACT_ARCHIVES; do
		for ext in info mod zip; do
			test -s "$d/$m.$ext" || printf ' %s' "$m.$ext"
		done
	done
}

shellcheck_version() {
	"$1" --version 2>/dev/null | sed -n 's/^version: //p'
}

# ci_pin prints the version ci.yml installs module path $1 at.
ci_pin() {
	sed -n "s|.*go install $1@\([^ ]*\).*|\1|p" "$root/.github/workflows/ci.yml" | head -n 1
}

failed=0
row() { # status, item, found, wanted
	printf '%-8s %-30s %-48s %s\n' "$1" "$2" "$3" "$4"
	[ "$1" = ok ] || failed=1
}

preflight() {
	profile=${1:-all}
	case $profile in all | core | contract) ;; *)
		echo "usage: tools/devtools.sh preflight [all|core|contract]" >&2
		exit 2
		;;
	esac
	printf '%-8s %-30s %-48s %s\n' status item found wanted

	# Pins: this script, ci.yml and mise.toml agree.
	want=$(go_pin)
	ci_go=$(sed -n 's/^ *go-version: *"\([^"]*\)".*/\1/p' "$root/.github/workflows/ci.yml" | sort -u | tr '\n' ' ' | sed 's/ $//')
	if [ "$ci_go" = "$want" ]; then row ok "pin: ci.yml go-version" "$ci_go" "$want (mise.toml)"; else row WRONG "pin: ci.yml go-version" "${ci_go:-none}" "$want (mise.toml)"; fi
	for pair in "honnef.co/go/tools/cmd/staticcheck $STATICCHECK_PIN" "golang.org/x/vuln/cmd/govulncheck $GOVULNCHECK_PIN"; do
		# shellcheck disable=SC2086 # the pair splits into its two words
		set -- $pair
		got=$(ci_pin "$1")
		if [ "$got" = "$2" ]; then row ok "pin: ci.yml ${1##*/}" "$got" "$2 (devtools.sh)"; else row WRONG "pin: ci.yml ${1##*/}" "${got:-none}" "$2 (devtools.sh)"; fi
	done

	got=$(go env GOVERSION 2>/dev/null || true)
	if [ "$got" = "go$want" ]; then row ok go "$got" "go$want"; else row WRONG go "${got:-not found}" "go$want"; fi

	# GOFLAGS reaches every go test the targets run (a -run there could leave
	# make ci with no tests to fail), so it must be empty. go env reads both
	# the environment and the go env -w file.
	got=$(go env GOFLAGS 2>/dev/null || true)
	if [ -z "$got" ]; then
		row ok GOFLAGS "empty" "empty"
	else
		row WRONG GOFLAGS "$got" "empty"
		goflags_set=1
	fi

	if [ "$profile" != contract ]; then
		for pair in "staticcheck $STATICCHECK_MOD $STATICCHECK_PIN" "govulncheck $GOVULNCHECK_MOD $GOVULNCHECK_PIN"; do
			# shellcheck disable=SC2086 # the triple splits into its three words
			set -- $pair
			path=$(command -v "$1" || true)
			if [ -z "$path" ]; then row MISSING "$1" "not on PATH" "$3"; continue; fi
			got=$(module_version "$path" "$2")
			if [ "$got" = "$3" ]; then row ok "$1" "$got" "$3"; else row WRONG "$1" "${got:-unknown} ($path)" "$3"; fi
		done
		path=$(command -v shellcheck || true)
		if [ -z "$path" ]; then
			row MISSING shellcheck "not on PATH" "$SHELLCHECK_PIN"
		else
			got=$(shellcheck_version "$path")
			if [ "$got" = "$SHELLCHECK_PIN" ]; then row ok shellcheck "$got" "$SHELLCHECK_PIN"; else row WRONG shellcheck "${got:-does not run} ($path)" "$SHELLCHECK_PIN"; fi
		fi
	fi

	if [ "$profile" != core ]; then
		missing=$(missing_archives)
		n=$(echo "$CONTRACT_ARCHIVES" | grep -c .)
		if [ -z "$missing" ]; then row ok "contract gate module archives" "$n modules, .info .mod .zip" "present"; else row MISSING "contract gate module archives" "missing:$missing" "present"; fi
	fi

	if [ "$failed" != 0 ]; then
		echo
		echo "preflight ($profile): not ready. Locally: make tools (installs into $devtools/bin and fetches the archives)."
		echo "In CI: the workflow's install and fetch steps. A pin mismatch: make ci.yml and tools/devtools.sh agree."
		if [ -n "${goflags_set:-}" ]; then
			echo "GOFLAGS: unset GOFLAGS (or run with GOFLAGS=), and go env -u GOFLAGS if it is in the go env file."
		fi
		exit 1
	fi
	echo "preflight ($profile): ready"
}

case ${1:-} in
install) install_tools ;;
fetch) fetch_archives ;;
preflight) preflight "${2:-all}" ;;
*)
	echo "usage: tools/devtools.sh install | fetch | preflight [all|core|contract]" >&2
	exit 2
	;;
esac
