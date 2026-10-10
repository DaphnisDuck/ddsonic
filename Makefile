VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# ddsonic: fork binary name; `make build BINARY=cliamp` for the upstream name
BINARY  ?= ddsonic
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet lint staticcheck fmt fmt-check coverage security ci check clean install acceptance acceptance-validate

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
	$(MAKE) coverage
	shellcheck packaging/*.sh
	git diff --exit-code

check: fmt vet test

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
