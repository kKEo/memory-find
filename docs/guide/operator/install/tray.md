# memo-tray (macOS menu bar)

memo-tray is an optional macOS companion for [shared HTTP servers](clients.md). It lists the
running `memo-mcp serve --http` servers and their connected clients, opens a server's **Live**
page in a native window, and starts and stops servers. It is a separate binary in its own Go
module (`tray/`). It is the only part of the project built with cgo (AppKit and WebKit); the
memo-mcp binary stays pure Go.

User-facing behaviour is described in the user guide (*The menu-bar app*). This page covers
how it works and what to configure.

## Install

Release assets: `memo-tray_X.Y.Z_darwin_universal.zip` and its `.sha256`, next to the
server archives. The zip holds `memo-tray.app`: one universal binary (arm64 and x86-64),
macOS 12 or newer, ad-hoc signed and **not notarized**. On first open, allow it under
*System Settings › Privacy & Security*, or remove the quarantine attribute:

```bash
shasum -a 256 -c memo-tray_X.Y.Z_darwin_universal.zip.sha256
ditto -x -k memo-tray_X.Y.Z_darwin_universal.zip /Applications
xattr -dr com.apple.quarantine /Applications/memo-tray.app
```

From source (needs the Xcode command-line tools):

```bash
make tray        # tray/memo-tray, for this machine
make tray-app    # tray/dist/memo-tray.app and the release zip
make tray-test   # vet and tests of the tray module
```

## How it finds servers

Every `serve --http` writes a run file, `$MEMO_HOME/run/serve-<pid>.json` (see
[Files on disk](../config/files.md)). memo-tray scans that directory every 2 seconds and
whenever its menu opens. It ignores the directory if it is not owned by you or is writable by
others. It deletes a run file only when the process is gone, or when the pid now belongs to
another program: the name is not `memo-mcp`, or it started after the file was written.

For each server it polls `GET /live.json` (see
[For companion apps](clients.md#for-companion-apps)). In token mode it reads the token from the
`token_file` named in the run file, at every poll, so a rotated token is picked up after the
server restarts. It sends the token only to loopback HTTP or to HTTPS, through no proxy, and
never follows a redirect. Servers that require a client certificate (mTLS) are listed but
cannot be polled or opened in a window.

Only servers on this machine and in this `MEMO_HOME` are discovered. memo-tray resolves
`MEMO_HOME` like memo-mcp; apps launched from Finder see no shell variables, so that means
`~/.memo-mcp`.

## Starting and stopping

A server memo-tray starts runs as:

```bash
memo-mcp serve --http 127.0.0.1:<port> [--auth token]
```

It runs with `MEMO_HOME` and `MEMO_KB` set. stdin is `/dev/null`, and stdout and stderr go to
`$MEMO_HOME/logs/serve-<kb>.log` (0600, moved to `.1` above 5 MB). Variables that would change
where or how it serves are removed from its environment: `MEMO_HTTP_*`, `MEMO_TLS_*`,
`MEMO_PUBLIC_URL`, `MEMO_METRICS_ADDR` and `JOURNAL_*`. **Stop** sends SIGTERM and kills the
process if it is still running after 10 seconds.

A server started elsewhere gets SIGTERM only, and only after memo-tray has checked that the pid
is still that server: a `memo-mcp` process that started before its run file was written, and,
if it answers, one whose `/live.json` reports the same pid and instance id.

memo-tray starts loopback servers only. For remote or proxied servers, keep using a service
manager.

## tray.json

`$MEMO_HOME/tray.json` (0600) is optional. memo-tray rereads it when it changes and adds an entry
the first time you start a knowledge base from the menu.

| Key | Meaning |
|---|---|
| `memo_binary` | Path to memo-mcp. Default: next to the app, then `$PATH`, `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin` |
| `servers[].kb` | Knowledge-base name (`MEMO_KB`) |
| `servers[].addr` | Loopback `host:port`. Assigned from 8765–8799 when you first start the knowledge base from the menu |
| `servers[].auth` | `none` (default) or `token` (`<MEMO_HOME>/http-token`) |
| `servers[].autostart` | Start it when memo-tray starts |
| `servers[].env`, `env` | Extra environment for that server, or for every server. Variables memo-tray controls (above, plus `MEMO_KB` and `MEMO_HOME`) are refused |
| `stop_on_quit` | Stop the servers memo-tray started when it quits (default `true`) |
| `debug` | Enable the web inspector in stats windows |

## The stats window

The window is a WKWebView in memo-tray's own process:

- It logs in with a single-use link from `POST /login-link`, so the token never reaches the
  web view, a URL or the process arguments.
- It uses a private, non-persistent website data store. The session cookie is never written
  to disk.
- JavaScript is off; memo-mcp's UI does not use any.
- It shows only the server's own origin. A clicked link elsewhere opens in the default
  browser; any other navigation (redirects off the server, `file:`, custom schemes) is refused.

**Open in Browser** passes a single-use login link to the browser through `open(1)`. Like the
link the server prints at startup, it is visible to other processes for an instant and becomes
useless once it is used.

## Files

| Path | Contents |
|---|---|
| `$MEMO_HOME/tray.json` | Settings (above) |
| `$MEMO_HOME/tray.lock` | Held while memo-tray runs; a second copy refuses to start |
| `$MEMO_HOME/logs/tray.log` | memo-tray's own log (login codes are redacted) |
| `$MEMO_HOME/logs/serve-<kb>.log` | Output of the servers memo-tray started |
