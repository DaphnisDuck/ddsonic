VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# ddsonic: fork binary name; `make build BINARY=cliamp` for the upstream name
BINARY  ?= ddsonic
LDFLAGS := -s -w -X main.version=$(VERSION)

# ddsonic: the pinned developer tools (tools/devtools.sh, `make tools`) live
# outside the repository; their bin/ comes first on PATH when it exists. CI
# has no such directory and installs the tools its own way.
DEVTOOLS ?= $(HOME)/.local/share/ddsonic/devtools
ifneq ($(wildcard $(DEVTOOLS)/bin/.),)
export PATH := $(DEVTOOLS)/bin:$(PATH)
endif

.PHONY: build test vet lint staticcheck fmt fmt-check coverage security ci check clean install acceptance acceptance-validate sdk-contract tools preflight preflight-core preflight-contract ci-core ci-checks ci-tail ci-tests test-race-cover validate validate-docs validate-test

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed — skipping (make tools installs the pinned one)"; fi

staticcheck:
	@command -v staticcheck >/dev/null 2>&1 || { echo "staticcheck is required: make tools"; exit 1; }
	staticcheck ./...

fmt:
	gofmt -l -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

coverage:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# ddsonic: the tests once, under the race detector and with coverage; -race
# requires the atomic mode. make ci and make ci-core run ci-tests, in place of
# the race run and make coverage, which ran the suite a second time; its
# package list is written out, with no Make variable a caller could set, and
# preflight rejects a GOFLAGS that could narrow it.
# test-race-cover is the same for use by hand, on TESTPKGS (default ./...).
ci-tests:
	go test -count=1 -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

TESTPKGS ?= ./...
test-race-cover:
	go test -count=1 -race -covermode=atomic -coverprofile=coverage.out $(TESTPKGS)
	go tool cover -func=coverage.out

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck is required: make tools"; exit 1; }
	govulncheck ./...

# ddsonic: the pinned tools (CI's pins) into $(DEVTOOLS)/bin, and the module
# archives the contract gate reads offline. Idempotent.
tools:
	DDSONIC_DEVTOOLS=$(DEVTOOLS) sh tools/devtools.sh install

# ddsonic: a fast check, without network, that the tools PATH finds are at the
# pins and the gate's archives are cached; it names the remedy. make ci and
# make sdk-contract run it before anything expensive.
preflight:
	@DDSONIC_DEVTOOLS=$(DEVTOOLS) sh tools/devtools.sh preflight all
preflight-core:
	@DDSONIC_DEVTOOLS=$(DEVTOOLS) sh tools/devtools.sh preflight core
preflight-contract:
	@DDSONIC_DEVTOOLS=$(DEVTOOLS) sh tools/devtools.sh preflight contract

# ddsonic: the full validation. CI runs the same steps as two jobs: go
# (make ci-core) and sdk-contract (make sdk-contract).
ci: preflight
	$(MAKE) ci-checks
	$(MAKE) ci-tests
	$(MAKE) sdk-contract
	$(MAKE) ci-tail

# ddsonic: everything in ci but the contract gate, in the same order.
ci-core: preflight-core
	$(MAKE) ci-checks
	$(MAKE) ci-tests
	$(MAKE) ci-tail

ci-checks: fmt-check vet staticcheck security

ci-tail:
	shellcheck packaging/*.sh tools/*.sh
	git diff --exit-code

check: fmt vet test

# ddsonic: proportional validation (docs/ddsonic/acceptance.md). make
# validate classifies the change against a freshly fetched main and runs the
# profile for its class; PROFILE=test|code|full may raise it, never lower it
# (tools/validate.sh). It prints the class line a review request quotes.
# The classifier is started in one place, tools/ci-classify.sh, which CI
# runs too; it is not a Make variable, which a command line could replace.
validate:
	@MAKE='$(MAKE)' sh tools/validate.sh

# The docs and test profiles. validate-docs needs the base commit, which
# make validate and CI pass; the citation check is strict, so a missing base
# is a failure, never a skip.
validate-docs: preflight-core fmt-check
	@expr "$(VALIDATE_BASE)" : '[0-9a-f]\{40,64\}$$' >/dev/null || { echo "validate-docs: VALIDATE_BASE must be a commit ID"; exit 2; }
	git diff --check $(VALIDATE_BASE)
	DDSONIC_CITATION_BASE=$(VALIDATE_BASE) DDSONIC_CITATION_STRICT=1 go test -count=1 .

validate-test: preflight-core
	$(MAKE) vet staticcheck ci-tests
	shellcheck packaging/*.sh tools/*.sh

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
sdk-contract: preflight-contract
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
