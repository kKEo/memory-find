# Release archives

Every tag `vX.Y.Z` publishes five archives and a checksum file on the
[releases page](https://github.com/kKEo/memory-find/releases):

| Platform | Archive |
|---|---|
| macOS, Apple silicon | `memo-mcp_X.Y.Z_darwin_arm64.tar.gz` |
| macOS, Intel | `memo-mcp_X.Y.Z_darwin_amd64.tar.gz` |
| Linux, x86-64 | `memo-mcp_X.Y.Z_linux_amd64.tar.gz` |
| Linux, ARM64 | `memo-mcp_X.Y.Z_linux_arm64.tar.gz` |
| Windows, x86-64 | `memo-mcp_X.Y.Z_windows_amd64.zip` |
| All | `checksums.txt` (SHA-256) |

Each archive contains the binary, `LICENSE`, `README.md` and `SKILL.md`.

## Install

```bash
VERSION=1.4.0
ARCH=darwin_arm64            # see the table above
curl -LO https://github.com/kKEo/memory-find/releases/download/v${VERSION}/memo-mcp_${VERSION}_${ARCH}.tar.gz
curl -LO https://github.com/kKEo/memory-find/releases/download/v${VERSION}/checksums.txt
shasum -a 256 -c --ignore-missing checksums.txt      # Linux: sha256sum -c --ignore-missing
tar xzf memo-mcp_${VERSION}_${ARCH}.tar.gz
install -m 0755 memo-mcp ~/.local/bin/memo-mcp       # any directory; note the absolute path
memo-mcp version
```

`memo-mcp version` prints the build version, the MCP protocol version (`2026-07-28`), the Go
version and the model directory.

> **macOS Gatekeeper.** A binary downloaded by a browser carries a quarantine attribute and is
> blocked on first run. Clear it with `xattr -d com.apple.quarantine ~/.local/bin/memo-mcp`, or
> download with `curl`, which does not set it.

## MCP registry

The server is listed in the MCP registry as `io.github.kKEo/memory-find`. Its `server.json`
points at the release archives with their checksums, so registry-aware clients can install it
directly.

## What the binary needs at runtime

- No shared libraries, no CGo, no interpreter.
- A writable `MEMO_HOME`, by default `~/.memo-mcp`, and a writable `~/.cache/memo-mcp/models`.
- About 200 MB of disk for the default model, plus the knowledge-base files. See
  [Capacity and performance](../operations/capacity.md).
- Network access to `huggingface.co` once per model, unless you pre-seed the cache.
