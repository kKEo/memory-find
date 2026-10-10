# Release archives

Every tag `vX.Y.Z` publishes five archives and a checksum file on the
[releases page](https://github.com/kKEo/memors/releases):

| Platform | Archive |
|---|---|
| macOS, Apple silicon | `memors-mcp_X.Y.Z_darwin_arm64.tar.gz` |
| macOS, Intel | `memors-mcp_X.Y.Z_darwin_amd64.tar.gz` |
| Linux, x86-64 | `memors-mcp_X.Y.Z_linux_amd64.tar.gz` |
| Linux, ARM64 | `memors-mcp_X.Y.Z_linux_arm64.tar.gz` |
| Windows, x86-64 | `memors-mcp_X.Y.Z_windows_amd64.zip` |
| All | `checksums.txt` (SHA-256) |

Each archive contains the binary, `LICENSE`, `README.md` and `SKILL.md`.

The optional macOS menu-bar app comes as one more asset, `memors-tray_X.Y.Z_darwin_universal.zip`,
with its own `.sha256` file; see [memors-tray](tray.md).

## Install

```bash
VERSION=1.4.0
ARCH=darwin_arm64            # see the table above
curl -LO https://github.com/kKEo/memors/releases/download/v${VERSION}/memors-mcp_${VERSION}_${ARCH}.tar.gz
curl -LO https://github.com/kKEo/memors/releases/download/v${VERSION}/checksums.txt
shasum -a 256 -c --ignore-missing checksums.txt      # Linux: sha256sum -c --ignore-missing
tar xzf memors-mcp_${VERSION}_${ARCH}.tar.gz
install -m 0755 memors-mcp ~/.local/bin/memors-mcp       # any directory; note the absolute path
memors-mcp version
```

`memors-mcp version` prints the build version, the MCP protocol version (`2026-07-28`), the Go
version and the model directory.

> **macOS Gatekeeper.** A binary downloaded by a browser carries a quarantine attribute and is
> blocked on first run. Clear it with `xattr -d com.apple.quarantine ~/.local/bin/memors-mcp`, or
> download with `curl`, which does not set it.

## MCP registry

The server is listed in the MCP registry as `io.github.kKEo/memors`. Its `server.json`
points at the release archives with their checksums, so registry-aware clients can install it
directly.

## What the binary needs at runtime

- No shared libraries, no CGo, no interpreter. (This is the memors-mcp binary. memors-tray, the
  separate macOS app, links AppKit and WebKit.)
- A writable `MEMORS_HOME`, by default `~/.memors-mcp`, and a writable `~/.cache/memors-mcp/models`.
- About 200 MB of disk for the default model, plus the knowledge-base files. See
  [Capacity and performance](../operations/capacity.md).
- Network access to `huggingface.co` once per model, unless you pre-seed the cache.
