BINARY = memo-mcp
PKG = ./cmd/memo-mcp/
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test test-verbose test-race cover fmt vet lint check clean eval golden-update baseline-update spike

build:
	CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) $(PKG)

# eval runs the retrieval benchmark (internal/eval) against the recorded
# baseline and prints the per-query report.
eval:
	go test ./internal/eval/ -run TestRetrievalEval -count=1 -v

# baseline-update re-records the eval baseline after a deliberate retrieval
# change. Commit the resulting testdata/baseline.json with the change.
baseline-update:
	go test ./internal/eval/ -run TestRetrievalEval -count=1 -update-baseline

# golden-update re-records the tools/list golden file after a deliberate
# tool-surface change. Review the diff before committing.
golden-update:
	go test ./internal/server/ -run TestListToolsGolden -count=1 -update

# spike runs a throwaway experiment under spikes/<name> (never part of the
# binary or of PR CI). Usage: make spike NAME=s2-vectors
spike:
	go run -tags spike ./spikes/$(NAME)

test:
	go test ./... -count=1

test-verbose:
	go test ./... -v -count=1

test-race:
	go test ./... -race -count=1

# Only packages with test files: some Go toolchain installs fail with
# "no such tool covdata" when asked to report coverage for a package that
# has none. cmd/memo-mcp currently has no tests (tracked in docs/roadmap.md);
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

clean:
	rm -f $(BINARY) coverage.out
