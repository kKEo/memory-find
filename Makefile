BINARY = memors-mcp
PKG = ./cmd/memors-mcp/
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build snapshot test test-verbose test-race cover fmt vet lint check clean eval golden-update baseline-update spike docs docs-check docs-serve start-http tray tray-test tray-app

build:
	CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) $(PKG)

# snapshot runs the release build locally (five archives under dist/) without
# a tag or publishing. Uses the GoReleaser pinned in the CI workflow.
snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish

# eval runs the retrieval benchmark (internal/eval) against the recorded
# baseline and prints the per-query report.
eval:
	go test ./internal/eval/ -run TestRetrievalEval -count=1 -v

# baseline-update re-records the eval baseline after a deliberate retrieval
# change. Commit the resulting testdata/baseline.json with the change.
baseline-update:
	go test ./internal/eval/ -run TestRetrievalEval -count=1 -update-baseline

# golden-update re-records the tools/list and /live.json golden files after a
# deliberate contract change. Review the diff before committing.
golden-update:
	go test ./internal/server/ -run TestListToolsGolden -count=1 -update
	go test ./internal/ui/ -run TestLiveJSONGolden -count=1 -update

# spike runs a throwaway experiment under spikes/<name> (never part of the
# binary or of PR CI). Usage: make spike NAME=s2-vectors
spike:
	go run -tags spike ./spikes/$(NAME)

# docs builds the user and operator guides (docs/guide/, GitBook-compatible
# markdown) into site/ with mdBook, the same way the Pages workflow does.
# mdBook is a single static binary: https://github.com/rust-lang/mdBook/releases
MDBOOK ?= mdbook
BOOK ?= user

docs: docs-check
	@command -v $(MDBOOK) >/dev/null || { echo "mdbook not found; install it (brew install mdbook, or a release binary) or pass MDBOOK=/path/to/mdbook"; exit 1; }
	rm -rf site
	$(MDBOOK) build docs/guide/user
	$(MDBOOK) build docs/guide/operator
	rm -f site/*/book.toml site/*/.gitbook.yaml
	cp docs/guide/index.html site/index.html
	touch site/.nojekyll
	@echo "site/index.html: open it, or run 'make docs-serve BOOK=user|operator'"

# docs-check fails when an env var, command or metric in the code is missing
# from the operator guide, or a relative link in either guide is broken.
docs-check:
	./scripts/docs-check.sh

# docs-serve previews one book with live reload (BOOK=user or BOOK=operator).
docs-serve:
	$(MDBOOK) serve docs/guide/$(BOOK) --open

test:
	go test ./... -count=1

test-verbose:
	go test ./... -v -count=1

test-race:
	go test ./... -race -count=1

# Only packages with test files: some Go toolchain installs fail with
# "no such tool covdata" when asked to report coverage for a package that
# has none. cmd/memors-mcp currently has no tests (tracked in docs/roadmap.md);
# once it does, it will show up here automatically.
cover:
	go test $$(go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' ./...) -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

vet:
	go vet ./...

# lint uses an installed golangci-lint when present and otherwise runs it
# through `go run`, so the gate works on a fresh machine (slower first run).
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...; fi

# check is the full local gate, in the order that fails fastest. It mirrors
# the CI planned in docs/roadmap.md (P0 minimal, P6 full matrix).
check: fmt vet lint test-race

# memors-tray, the macOS menu-bar app, is its own Go module under tray/ and
# the only cgo build in the repository; memors-mcp itself stays pure Go.
tray:
	cd tray && CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=$(VERSION)" -o memors-tray ./cmd/memors-tray

tray-test:
	cd tray && go vet ./... && go test ./... -race -count=1

# tray-app builds tray/dist/memors-tray.app (universal, ad-hoc signed) and its
# release zip.
tray-app:
	./tray/scripts/build-app.sh $(VERSION)

clean:
	rm -f $(BINARY) coverage.out tray/memors-tray
	rm -rf site tray/dist

start-http:
	MEMORS_KB=crportal memors-mcp serve --http 127.0.0.1:8765
