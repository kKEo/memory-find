BINARY = memo-mcp
PKG = ./cmd/memo-mcp/

.PHONY: build test test-verbose test-race cover fmt vet lint check clean

build:
	CGO_ENABLED=0 go build -o $(BINARY) $(PKG)

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

lint:
	golangci-lint run ./...

# check is the full local gate, in the order that fails fastest. It mirrors
# the CI planned in docs/roadmap.md (P0 minimal, P6 full matrix).
check: fmt vet lint test-race

clean:
	rm -f $(BINARY) coverage.out
