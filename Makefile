VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# ddsonic: fork binary name; `make build BINARY=cliamp` for the upstream name
BINARY  ?= ddsonic
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet lint staticcheck fmt fmt-check coverage security ci check clean install acceptance acceptance-validate sdk-contract

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed — skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; fi

staticcheck:
	@command -v staticcheck >/dev/null 2>&1 || { echo "staticcheck is required"; exit 1; }
	staticcheck ./...

fmt:
	gofmt -l -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

coverage:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck is required"; exit 1; }
	govulncheck ./...

ci: fmt-check vet staticcheck security
	go test -count=1 -race ./...
	$(MAKE) sdk-contract
	$(MAKE) coverage
	shellcheck packaging/*.sh
	git diff --exit-code

check: fmt vet test

# ddsonic: the go-librespot contract gate (external/spotify/sdkcontract,
# docs/ddsonic/acceptance.md). It is not part of check or test: it stages the
# tracked go-librespot replacement (third_party/go-librespot), checked against
# its authenticated upstream base and recorded patches, and compiles it from
# empty private caches, offline, and runs eighty-one contract tests in it under
# the race detector, then the symbols gate (tools/sdkgate), two behavioural
# mutations and patch 0003's three negative controls that must each turn their
# test red, the composed stage (sixty-one tests and six child controls) and the
# stock stage (two probes of rc.4). It is mandatory for a
# full validation, for a release candidate, and for any change at the
# boundary with go-librespot or to its version. It needs the pinned Go
# toolchain, cgo with the codec libraries, and a module download cache that
# holds the archives (.zip, .mod, .info) of every module the repository
# selects, of upstream go-librespot v0.9.1, and of x/tools v0.50.0, x/mod
# v0.41.0 and x/sync v0.23.0 for tools/sdkgate (go mod download there fetches
# them). A build does not download the
# upstream SDK archive, since go.mod replaces it with the tracked tree: fetch it
# once with `go mod download github.com/devgianlu/go-librespot@v0.9.1` in a
# scratch module, or carry it in the release cache. No extracted copy is
# needed. It fails, and never skips, when one of those is missing.
sdk-contract:
	go test -tags sdkcontract -count=1 -timeout 30m ./external/spotify/sdkcontract

# ddsonic: release acceptance (docs/ddsonic/acceptance.md). acceptance runs the
# scripted cases on a local build, which is never release evidence; a release
# run is given the draft release's archive (see the doc).
acceptance-validate:
	go run ./acceptance/cmd/acceptance validate

acceptance: acceptance-validate
	go run ./acceptance/cmd/acceptance run --dev

clean:
	rm -f $(BINARY)

install: build
	install -d $(HOME)/.local/bin
	install -m 755 $(BINARY) $(HOME)/.local/bin/$(BINARY)
	# ddsonic: the launcher entry and icons, for the ddsonic binary only
	@if [ "$(BINARY)" = ddsonic ]; then sh packaging/desktop.sh "$${XDG_DATA_HOME:-$(HOME)/.local/share}" "$(HOME)/.local/bin/$(BINARY)"; fi
