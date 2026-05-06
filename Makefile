BINARY = memo-mcp
PKG = ./cmd/memo-mcp/

.PHONY: build test clean

build:
	CGO_ENABLED=0 go build -o $(BINARY) $(PKG)

test:
	go test ./... -count=1

test-verbose:
	go test ./... -v -count=1

clean:
	rm -f $(BINARY)
