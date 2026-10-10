# Part II: Install and deploy

memors-mcp has no installer and no service to register. Installing it means putting one binary
on disk and telling an MCP client how to start it.

- [Release archives](release.md): download, verify and place the binary.
- [Build from source](source.md): reproducible pure-Go builds.
- [Connecting MCP clients](clients.md): Claude Code, Claude Desktop and any stdio client.
- [Knowledge bases and MEMORS_HOME](layout.md): one file per KB, per-project setups.
- [Upgrading and migrations](upgrading.md): schema versions and what to run after an upgrade.
- [Air-gapped installs](air-gapped.md): no network at runtime, not even the model download.
