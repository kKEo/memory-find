# memo-tray (macOS menu bar)

memo-tray is an optional macOS companion for [shared HTTP servers](clients.md). It lists the
running `memo-mcp serve --http` servers and their connected clients, opens a server's **Live**
page in a native window, and starts and stops servers. It is a separate binary in its own Go
module (`tray/`). It is the only part of the project built with cgo (AppKit and WebKit); the
memo-mcp binary stays pure Go.

User-facing behaviour is described in the user guide (*The menu-bar app*). This page covers
how it works and what to configure.

![memo-tray polls GET /live.json on every running server, mints login links for its stats window and starts and stops servers. It finds servers through the run files they write, keeps settings in tray.json, logs the servers it started, and contacts GitHub only on the setup assistant's Install step.](../images/tray-architecture.svg)

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

## Installing memo-mcp from the app

The setup assistant installs memo-mcp on demand. On the Install step it asks the GitHub API for
the latest release (`api.github.com/repos/kKEo/memory-find/releases/latest`). memo-tray makes no
other outbound connection, and makes this one only when you open that step.

Installing works like this:

1. Download `checksums.txt` and `memo-mcp_<version>_darwin_<arch>.tar.gz` for this Mac. Only
   https is accepted, redirects included.
2. Compare the archive's SHA-256 with its line in `checksums.txt`. A release without that line
   is refused.
3. Extract only the regular file `memo-mcp` from the archive, into a temporary file in the
   target folder (default `~/.local/bin`).
4. Run `memo-mcp version` on it, and only then rename it over `<folder>/memo-mcp`. A failure at
   any step leaves the previous binary untouched.
5. Set `memo_binary` in `tray.json` to the new file.

The checksum proves the download is the file the release lists. Like the manual install, it
does not protect against a compromised release. Servers that are running keep the old binary
until they restart; the last step offers to restart the ones memo-tray started.

## Settings window and launch at login

The settings window and the setup assistant are pages that memo-tray serves itself:

- They are served on `127.0.0.1:<random port>` and shown in its own WKWebView window.
- A window enters through a link carrying a per-process secret, passed in process. It then
  holds an HttpOnly, SameSite=Strict cookie.
- Requests with any other `Host` header, or without the cookie, are refused.
- Form posts must be same-origin.
- Saving checks that `tray.json` has not changed since the form was opened, and validates
  everything before writing.

**Open memo-tray when you log in** writes `~/Library/LaunchAgents/io.github.kkeo.memo-tray.plist`
(`RunAtLoad`, the app's own executable) and turning it off removes the file. It takes effect at
the next login. memo-tray refuses while macOS runs it from a temporary copy (App Translocation:
move it to Applications first). At each start it updates the path if the app has moved.

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

`$MEMO_HOME/tray.json` (0600) is optional. The settings window edits it. memo-tray also rereads
it when you change it by hand, and adds an entry the first time you start a knowledge base from
the menu. Every write inside memo-tray goes through one lock, from read to write, so the menu and
the settings pages never undo each other.

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
| `~/Library/LaunchAgents/io.github.kkeo.memo-tray.plist` | Present while "Open memo-tray when you log in" is on |
| `~/.local/bin/memo-mcp` (or the folder you chose) | memo-mcp, when installed by the setup assistant |
