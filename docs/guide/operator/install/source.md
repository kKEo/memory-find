# Build from source

Requirements: Go 1.26 or newer. No C toolchain is needed for memors-mcp. The optional macOS
menu-bar app ([memors-tray](tray.md)) lives in its own module under `tray/` and needs the Xcode
command-line tools: `make tray`, `make tray-app`.

```bash
git clone https://github.com/kKEo/memors
cd memors
make build            # CGO_ENABLED=0, stripped, version from `git describe`
./memors-mcp version
```

`make build` runs:

```bash
CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o memors-mcp ./cmd/memors-mcp/
```

Cross-compiling is plain Go, for example
`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/memors-mcp`.

## Reproducing a release

`make snapshot` runs the same GoReleaser configuration as the release workflow without
publishing. It writes the five archives and `checksums.txt` under `dist/`. Release builds use
`-trimpath` and the commit timestamp, so rebuilding a tag gives the same binaries.

## Development targets

| Target | What it does |
|---|---|
| `make check` | gofmt, vet, golangci-lint, then all tests under the race detector |
| `make test` | All tests, no race detector |
| `make eval` | The retrieval benchmark against the recorded baseline |
| `make golden-update` | Re-record the `tools/list` golden after a deliberate tool-surface change |
| `make baseline-update` | Re-record the eval baseline after a deliberate retrieval change |
| `make docs` | Build these guides with mdBook into `site/` |
| `make tray-test` | Vet and test the memors-tray module (macOS) |
| `make tray-app` | Build `tray/dist/memors-tray.app` and its release zip (macOS) |

Tests use a deterministic hash embedder and need no network or model download.
